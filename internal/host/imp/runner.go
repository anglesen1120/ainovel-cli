package imp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/logger"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Đưa phiên bản prompt/schema vào InputDigest của từng giai đoạn; khi nâng cấp hợp đồng prompt thì tăng lên để các artefact hạ nguồn tự nhiên hết hiệu lực.
const (
	segmentPromptVersion = "seg-v2" // v2: ranh giới chỉ rơi đúng chỗ phân tách thật, tiêu đề được sao chép nguyên văn (kèm kiểm tra hồi chiếu tiêu đề)
	analyzePromptVersion = "analyze-v1"
	confirmMethodAuto    = "auto_authorized"
	confirmMethodUser    = "user_confirmed" // xác nhận thủ công rõ ràng sau khi xem trước trên TUI và nhấn y
)

// Prompts là các prompt hệ thống cho từng hàm ngữ nghĩa. Phần tổng hợp chia làm hai giai đoạn: Synthesize ra BookSynthesis cho toàn sách,
// Range ra RangeDigest cho các đoạn liên tục của sách dài; đầu ra của hai bên khác nhau, nên phải dùng prompt tương ứng.
type Prompts struct {
	Segment    string
	Analyze    string
	Synthesize string
	Range      string
}

// RunBudgets là ngân sách input/output cho từng hàm ngữ nghĩa. Bản đầu dùng hằng số thận trọng;
// về sau nên suy ra từ giới hạn context window / completion của model architect hiện tại để batch tự phình theo năng lực (RFC §9.2/§21).
type RunBudgets struct {
	MaxUnitBytes         int
	SegmentChunkBytes    int
	SegmentContextMargin int
	SegmentMaxTokens     int
	Analyze              AnalyzeBudget
	SynthesizeRangeBytes int
	SynthesizeMaxTokens  int
}

// DefaultRunBudgets trả về ngân sách mặc định thận trọng, dùng làm phương án dự phòng khi chưa biết năng lực model (probe thất bại).
func DefaultRunBudgets() RunBudgets {
	return RunBudgets{
		MaxUnitBytes:         8000,
		SegmentChunkBytes:    24000,
		SegmentContextMargin: 20,
		SegmentMaxTokens:     8192,
		Analyze:              AnalyzeBudget{ContextBytes: 24000, MaxOutputTokens: 8000, PerChapterOutput: 900, PromptOverhead: 2000},
		SynthesizeRangeBytes: 16000,
		SynthesizeMaxTokens:  8192,
	}
}

// ModelRuntime chứa các факт năng lực model cần cho lời gọi imp, do Host tiêm vào sau khi probe biên (RFC §13/§17).
// Cho hai ngân sách tự phình theo context/completion, thinking được gửi theo năng lực; khi toàn bộ giá trị bằng 0 thì rơi về mặc định bảo thủ,
// hành vi sẽ giống như trước khi tích hợp năng lực. Output có cấu trúc không phát response_format theo năng lực provider (xem chú thích callProfile).
type ModelRuntime struct {
	ContextTokens   int                     // giới hạn context đầu vào (token)
	MaxOutputTokens int                     // giới hạn output nhìn thấy trong một lần (token)
	Thinking        agentcore.ThinkingLevel // đã resolve theo năng lực; ThinkingAuto("") nghĩa là không gửi tường minh
}

// profile suy ra các tuỳ chọn gọi của runtime này (thinking).
func (rt ModelRuntime) profile() callProfile {
	return callProfile{thinking: rt.Thinking}
}

// Caller là một tier model cho một hàm ngữ nghĩa: model + факт năng lực của model đó (RFC §13.1/§17).
// segment/analyze/synthesize đều giữ tier riêng; ngân sách và tuỳ chọn gọi đều suy ra từ tier của chính nó,
// tier rẻ với cửa sổ nhỏ chỉ giới hạn hàm của nó, không kéo tụt các giai đoạn khác.
type Caller struct {
	Model   callModel
	Runtime ModelRuntime
}

// budgetsFromRuntime suy ra ngân sách cho từng hàm ngữ nghĩa từ giới hạn context/completion thật của model (RFC §9.2/§21).
// Chỉ như vậy mới bảo đảm "đổi model mạnh hơn thì batch tự phình to, số lần gọi giảm"; khi năng lực chưa biết thì rơi về mặc định thận trọng.
func budgetsFromRuntime(rt ModelRuntime) RunBudgets {
	if rt.ContextTokens <= 0 || rt.MaxOutputTokens <= 0 {
		return DefaultRunBudgets()
	}
	const bytesPerToken = 3 // Quy đổi bảo thủ UTF-8 tiếng Trung: token→byte (ước lượng thấp hơn thì an toàn hơn)
	out := rt.MaxOutputTokens
	// Ngân sách input: lấy context trừ output nhìn thấy và phần dự trữ ~10% cho suy luận/hệ thống, rồi quy đổi sang byte.
	reserve := rt.ContextTokens / 10
	inTokens := rt.ContextTokens - out - reserve
	if inTokens < 2000 {
		inTokens = 2000
	}
	inBytes := inTokens * bytesPerToken
	return RunBudgets{
		MaxUnitBytes:         min(inBytes/2, 32000),
		SegmentChunkBytes:    inBytes,
		SegmentContextMargin: 20,
		SegmentMaxTokens:     out,
		Analyze: AnalyzeBudget{
			ContextBytes:     inBytes,
			MaxOutputTokens:  out,
			PerChapterOutput: 900,
			PromptOverhead:   2000,
		},
		SynthesizeRangeBytes: inBytes,
		SynthesizeMaxTokens:  out,
	}
}

// Confirmation là artefact xác nhận phân đoạn, gắn với segmentation hiện tại (RFC §8.4).
type Confirmation struct {
	Method   string `json:"method"`
	Chapters int    `json:"chapters"`
}

// StoryResolution là quyết định của người dùng cho trạng thái câu chuyện uncertain, gắn với synthesis hiện tại (RFC §10.4).
type StoryResolution struct {
	Choice string `json:"choice"` // open / closed
}

// Deps là các phụ thuộc hẹp của runner (RFC §17). Ba hàm ngữ nghĩa mỗi cái khai báo tier model riêng;
// Host mặc định đều rơi vào architect, còn lớp cấu hình có thể trỏ hàm cơ giới hơn sang tier rẻ hơn (RFC §13.1).
type Deps struct {
	Store         *store.Store
	CommitChapter ChapterCommitter
	Segment       Caller
	Analyze       Caller
	Synthesize    Caller // range digest và book synthesis dùng cùng tier (cùng một giai đoạn tổng hợp)
	Prompts       Prompts
	Budgets       RunBudgets
}

// budgetsFromDeps suy ra ngân sách dựa trên năng lực tier riêng của từng hàm ngữ nghĩa (RFC §9.2/§13.1).
func budgetsFromDeps(d Deps) RunBudgets {
	seg := budgetsFromRuntime(d.Segment.Runtime)
	ana := budgetsFromRuntime(d.Analyze.Runtime)
	syn := budgetsFromRuntime(d.Synthesize.Runtime)
	return RunBudgets{
		MaxUnitBytes:         seg.MaxUnitBytes,
		SegmentChunkBytes:    seg.SegmentChunkBytes,
		SegmentContextMargin: seg.SegmentContextMargin,
		SegmentMaxTokens:     seg.SegmentMaxTokens,
		Analyze:              ana.Analyze,
		SynthesizeRangeBytes: syn.SynthesizeRangeBytes,
		SynthesizeMaxTokens:  syn.SynthesizeMaxTokens,
	}
}

// Run thực thi toàn bộ pipeline nhập: LoadState → NextAction → chạy một hành động → đọc lại факт.
// Chạy trong goroutine riêng; channel sự kiện trả về sẽ do hàm này đóng.
func Run(ctx context.Context, deps Deps, opts Options) (<-chan Event, error) {
	if deps.Store == nil || deps.CommitChapter == nil ||
		deps.Segment.Model == nil || deps.Analyze.Model == nil || deps.Synthesize.Model == nil {
		return nil, fmt.Errorf("deps không đầy đủ")
	}
	if deps.Budgets == (RunBudgets{}) {
		deps.Budgets = budgetsFromDeps(deps)
	}
	// Log riêng cho quy trình nhập: bản ghi đầy đủ của một lần import (sự kiện, retry, toàn bộ chuỗi lỗi) không trộn
	// với log của engine/TUI, để lúc điều tra chỉ cần nhìn một file này. Nếu tạo thất bại phải hiện lại rõ ràng — panel sẽ
	// dẫn người dùng xem logs/import.log; quay về im lặng tương đương chỉ vào một file không tồn tại (Debug-First).
	log, closeLog, logErr := logger.FileLogger(deps.Store.Dir(), "import.log")
	log.Info("imp thời gian chạy model nhập",
		"segment_ctx", deps.Segment.Runtime.ContextTokens,
		"analyze_ctx", deps.Analyze.Runtime.ContextTokens,
		"synthesize_ctx", deps.Synthesize.Runtime.ContextTokens,
		"analyze_max_output", deps.Analyze.Runtime.MaxOutputTokens,
		"analyze_context_bytes", deps.Budgets.Analyze.ContextBytes)
	events := make(chan Event, 32)
	go func() {
		defer close(events)
		defer closeLog()
		r := &runner{deps: deps, opts: opts, events: events, ws: OpenWorkspace(deps.Store.Dir()), log: log}
		if logErr != nil {
			r.emit(StageIngesting, 0, 0, fmt.Sprintf("Tạo file log nhập thất bại (%v), bản ghi lần này sẽ dùng log mặc định", logErr), nil)
		}
		r.run(ctx)
	}()
	return events, nil
}

type runner struct {
	deps   Deps
	opts   Options
	events chan Event
	ws     *Workspace
	act    Action       // hành động đang chạy hiện tại, dùng để gắn giai đoạn cho artefact lỗi
	log    *slog.Logger // log riêng cho import (logs/import.log); nil thì rơi về logger mặc định
}

func (r *runner) emit(stage Stage, current, total int, msg string, err error) {
	r.send(Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg, Err: err})
}

func (r *runner) send(ev Event) {
	r.logEvent(ev)
	// Sự kiện ở trạng thái cuối và điểm dừng phải mang đúng tín hiệu thành/bại hoặc cần hành động (xem trước xác nhận, --story mà mất gợi ý thì người dùng sẽ không biết phải làm gì),
	// nên phải được chuyển giao tin cậy; chỉ các sự kiện tiến độ trung gian mới có thể bị bỏ khi hàng đợi đầy.
	if ev.Stage == StageError || ev.Stage == StageDone ||
		ev.Stage == StageAwaitingConfirmation || ev.Stage == StageAwaitingStoryStatus {
		r.events <- ev
		return
	}
	select {
	case r.events <- ev:
	default: // khi channel đầy thì bỏ tiến độ, tuyệt đối không chặn pipeline
	}
}

// logEvent ghi lại từng sự kiện tiến độ vào log riêng của import (<root sách>/logs/import.log): dòng retry trên panel bị ghi đè ngay tại chỗ,
// panel biến mất khi nhấn Esc, còn log là bản ghi quy trình đầy đủ duy nhất để tra cứu về sau (§14.1).
func (r *runner) logEvent(ev Event) {
	log := r.log
	if log == nil {
		log = slog.Default()
	}
	args := []any{"stage", string(ev.Stage)}
	if ev.Total > 0 {
		args = append(args, "progress", fmt.Sprintf("%d/%d", ev.Current, ev.Total))
	}
	if ev.Err != nil {
		args = append(args, "err", ev.Err)
	}
	level := slog.LevelInfo
	switch {
	case ev.Stage == StageError:
		level = slog.LevelError // trạng thái lỗi cuối cùng là dòng cần được lọc nổi bật nhất trong log, không được để thành INFO
	case ev.Level == "warn":
		level = slog.LevelWarn
	}
	log.Log(context.Background(), level, ev.Message, args...)
}

func (r *runner) fail(msg string, err error) {
	r.saveFailure(err)
	r.emit(StageError, 0, 0, msg, err)
}

// saveFailure thống nhất ghi các thất bại có kèm phản hồi thô vào failures/ (điểm rơi thứ ba của RFC §14.2),
// dùng chung cho mọi hàm ngữ nghĩa như segment/synthesize; đường cứu hộ cho phân tích bị cắt đã ghi metadata chi tiết ngay tại chỗ.
// Các thất bại không có phản hồi thô (IO, huỷ, kiểm tra tiền đề) không có output model để lưu, nên không ghi.
func (r *runner) saveFailure(err error) {
	var se *errSemantic
	var tr *errTruncated
	switch {
	case errors.As(err, &se):
		r.ws.writeFailure(FailureMeta{Stage: string(r.act), Detail: err.Error()}, se.Raw)
	case errors.As(err, &tr):
		r.ws.writeFailure(FailureMeta{Stage: string(r.act), Detail: err.Error(), StopReason: "length"}, tr.Raw)
	}
}

// facts ghép факт của workspace với đối soát phát hành chính thức.
func (r *runner) facts() (Facts, error) {
	return CollectFacts(r.deps.Store, r.ws)
}

// profileFor suy ra tuỳ chọn gọi cho một tier nào đó, đồng thời phát sự kiện phản hồi lại áp dụng backoff/nhắc kiểm tra
// vào luồng sự kiện của đúng giai đoạn đó — backoff retry có thể lặng lẽ tích luỹ hơn 2 phút, không hiện ra thì người dùng sẽ tưởng là treo (§14.1).
// Key chỉ dành cho backoff request (có thời điểm hết hạn): đó là trạng thái tức thời của cùng một lần gọi, UI cập nhật tại chỗ một dòng ("lần N" nhấp nháy).
// Kiểm tra lại là sự kiện ngữ nghĩa xuyên suốt nhiều lần gọi — phân đoạn gọi theo từng khối, mỗi khối tự kiểm tra riêng, dùng chung Key sẽ làm khối sau đè lên khối trước,
// nuốt mất dấu vết điều tra (thực tế panel chỉ còn một dòng unit_id đổi liên tục), nên phải để mỗi dòng riêng nhằm giữ lịch sử.
func (r *runner) profileFor(c Caller, stage Stage) callProfile {
	prof := c.Runtime.profile()
	prof.log = r.log
	prof.notify = func(msg string, retryAt time.Time) {
		ev := Event{Time: time.Now(), Stage: stage, Message: msg, Level: "warn", RetryAt: retryAt}
		if !retryAt.IsZero() {
			ev.Key = "retry:" + string(stage)
		}
		r.send(ev)
	}
	prof.progress = func(current, total int, msg string) {
		r.send(Event{Time: time.Now(), Stage: stage, Current: current, Total: total, Message: msg})
	}
	return prof
}

// applyGuidance lưu bền hướng dẫn --guide hiện tại thành input ngữ nghĩa của workspace (RFC §18.3).
// Hướng dẫn là một trong các input của segmentation InputDigest: khi nội dung đổi thì các lần chia cũ và toàn bộ hạ nguồn sẽ tự lệch và phải làm lại,
// không cần quy tắc vô hiệu hoá thủ công. Khi workspace chưa được tạo thì bỏ qua, sẽ ghi ở vòng lặp kế tiếp sau ingest.
func (r *runner) applyGuidance() error {
	g := strings.TrimSpace(r.opts.Guidance)
	if g == "" || !r.ws.Active() {
		return nil
	}
	existing, err := r.ws.LoadGuidance()
	if err != nil {
		return fmt.Errorf("đọc hướng dẫn phân đoạn hiện có: %w", err)
	}
	if existing == g {
		return nil
	}
	// Sau khi publish bắt đầu thì artefact chính thức không được ghi đè (¶12.2): lúc này nếu chia lại thì chắc chắn sẽ đâm vào "tường từ chối ghi đè" ở publish,
	// mà trước khi đâm tường còn phải trả thêm toàn bộ chi phí gọi model cho chia/analyze/tổng hợp — tốt nhất là đẩy lỗi lên ngay ở chỗ không tốn gì.
	// book là lần ghi đầu tiên của publish, nó tồn tại tức là publish đã bắt đầu (kiểm tra tiền đề của import bảo đảm book ban đầu còn trống).
	book, err := r.deps.Store.Book.Load()
	if err != nil {
		return fmt.Errorf("đọc book chính thức: %w", err)
	}
	if book != nil {
		return fmt.Errorf("Foundation chính thức đã bắt đầu phát hành, --guide chia lại sẽ xung đột với nội dung đã phát hành và bị từ chối ghi đè, không còn nhận hướng dẫn phân đoạn nữa")
	}
	return r.ws.writeAtomic(fileGuidance, []byte(g))
}

// checkSourceIdentity chặn trường hợp "workspace đang chạy nhưng lại đưa vào một file nguồn khác": ingest chỉ chạy khi chưa có workspace,
// nếu không đối chiếu thì /import B.txt có thể âm thầm nối tiếp từ điểm dừng của A, phát hành xong A mà B chưa đọc một byte nào (RFC §12.1/§18.2).
// Việc truyền lại cùng một đường dẫn cho cùng một file là thói quen phổ biến (/import tiếp tục với cùng path), nên so sánh theo digest nội dung thay vì cấm mọi path.
func (r *runner) checkSourceIdentity() error {
	if r.opts.SourcePath == "" || !r.ws.Active() {
		return nil
	}
	m, err := r.ws.LoadManifest()
	if err != nil {
		return nil // bộ ba nhận dạng không đọc được thì đi theo chẩn đoán hỏng của ingest, không báo lỗi trùng ở đây
	}
	raw, err := os.ReadFile(r.opts.SourcePath)
	if err != nil {
		return fmt.Errorf("đọc tệp nguồn %s: %w", r.opts.SourcePath, err)
	}
	if Digest(raw) != m.RawSHA256 {
		return fmt.Errorf("đã có một lần nhập %q đang chạy, nhưng tệp nguồn lần này có nội dung khác nhau: hãy hoàn tất hoặc bỏ dở lần nhập cũ trước (xóa meta/import/) rồi nhập sách mới", m.SourceName)
	}
	return nil
}

func (r *runner) run(ctx context.Context) {
	if err := r.checkSourceIdentity(); err != nil {
		r.fail("kiểm tra danh tính file nguồn", err)
		return
	}
	var previous *Facts
	for {
		if ctx.Err() != nil {
			r.fail("người dùng đã hủy", ctx.Err())
			return
		}
		if err := r.applyGuidance(); err != nil {
			r.fail("ghi hướng dẫn phân đoạn", err)
			return
		}
		facts, err := r.facts()
		if err != nil {
			r.fail("đọc trạng thái import", err)
			return
		}
		if previous != nil && facts == *previous {
			r.fail("import bị đình trệ", fmt.Errorf("sau khi thực hiện hành động thì факт không đổi, hành động tiếp theo vẫn là %q", NextAction(facts)))
			return
		}
		snapshot := facts
		previous = &snapshot
		act := NextAction(facts)
		r.act = act
		err = nil
		switch act {
		case ActionIngest:
			err = r.ingest(ctx)
		case ActionSegment:
			err = r.segment(ctx)
		case ActionAwaitConfirmation:
			if !r.confirm() {
				return // chế độ tương tác: chờ người dùng xác nhận, dừng ở đây
			}
		case ActionAnalyze:
			err = r.analyze(ctx)
		case ActionSynthesize:
			err = r.synthesize(ctx)
		case ActionAwaitStoryResolution:
			if !r.resolveStoryStatus() {
				return // chưa có quyết định rõ ràng: dừng ở đây, chờ --story=open|closed
			}
		case ActionPublish:
			err = r.publish(ctx)
		case ActionDone:
			r.emit(StageDone, 0, 0, "Nhập hoàn tất, chờ nghiệm thu rồi tiếp tục viết", nil)
			return
		default:
			err = fmt.Errorf("hành động không xác định %q", act)
		}
		if err != nil {
			r.fail("nhập thất bại", err)
			return
		}
	}
}

func (r *runner) ingest(ctx context.Context) error {
	// Đi tới ingest mà thư mục đã tồn tại = bộ ba nhận dạng (manifest/source/intent) bị thiếu hoặc hỏng:
	// createWorkspace sẽ từ chối vì "đã tồn tại (có thể khôi phục bằng /import không tham số)", còn chạy lại không tham số
	// thì vì WorkspaceReady=false mà quay về đây đòi đường dẫn nguồn — hai lời nhắc này đánh nhau, người dùng không có đường đi.
	if r.ws.Active() {
		return fmt.Errorf("meta/import/ đã tồn tại nhưng danh tính workspace không dùng được (manifest/source/intent bị thiếu hoặc hỏng), vui lòng xác nhận thủ công rồi xóa thư mục đó và nhập lại")
	}
	if err := checkImportPreconditions(r.deps.Store); err != nil {
		return err
	}
	if r.opts.SourcePath == "" {
		return fmt.Errorf("nhập mới cần đường dẫn tệp nguồn")
	}
	r.emit(StageIngesting, 0, 0, "Đang đọc, giải mã, chuẩn hóa và chụp nhanh tệp nguồn...", nil)
	_, m, err := Ingest(r.deps.Store.Dir(), r.opts.SourcePath, r.opts.intent())
	if err != nil {
		return err
	}
	r.emit(StageIngesting, 0, 0, fmt.Sprintf("Ảnh chụp nguồn đã sẵn sàng: %s (mã hóa %s, %d byte)", m.SourceName, m.Encoding, m.SizeBytes), nil)
	return nil
}

func (r *runner) segment(ctx context.Context) error {
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	units := buildSourceUnits(src, r.deps.Budgets.MaxUnitBytes)
	guidance, err := r.ws.LoadGuidance()
	if err != nil {
		return fmt.Errorf("đọc hướng dẫn phân đoạn: %w", err)
	}
	r.emit(StageSegmenting, 0, 0, fmt.Sprintf("Nhận diện ngữ nghĩa ranh giới chương (%d đơn vị tọa độ)...", len(units)), nil)
	digest := segmentInputDigest(Digest(src), guidance, segmentPromptVersion)
	// Identity của bộ nhớ đệm khối còn gắn thêm MaxUnitBytes: bảng unit được xác định duy nhất bởi (nguồn đã chuẩn hóa, MaxUnitBytes),
	// khi đổi tier model, MaxUnitBytes thay đổi sẽ tái tạo cách chia ảo của các dòng quá dài — dãy ID (L1.1…) và các điểm cuối khối vẫn tái hiện được,
	// nhưng phạm vi byte đã khác; nếu chỉ khớp theo ID điểm cuối sẽ tái sử dụng nhầm ranh giới cũ bị lệch (anchor mismatch gây lỗi xác định hoặc cắt nhầm âm thầm).
	chunkIdentity := fmt.Sprintf("%s\x00units:%d", digest, r.deps.Budgets.MaxUnitBytes)
	seg, err := Segment(ctx, r.deps.Segment.Model, r.deps.Prompts.Segment, src, units, guidance,
		r.deps.Budgets.SegmentChunkBytes, r.deps.Budgets.SegmentContextMargin, r.deps.Budgets.SegmentMaxTokens,
		r.profileFor(r.deps.Segment, StageSegmenting), r.ws, chunkIdentity)
	if err != nil {
		return err
	}
	if err := writeArtifact(r.ws, fileSegmentation, digest, *seg); err != nil {
		return err
	}
	// Phân đoạn cuối cùng đã được ghi xuống đĩa, bộ nhớ đệm cấp khối đã hoàn thành nhiệm vụ; dọn thất bại thì không ảnh hưởng tính đúng (digest vẫn khớp), nhưng vẫn cần để lại dấu vết.
	if cerr := r.ws.clearDir(dirSegmentChunks); cerr != nil {
		r.emit(StageSegmenting, 0, 0, fmt.Sprintf("Dọn bộ nhớ đệm cấp khối thất bại (không ảnh hưởng kết quả phân đoạn): %v", cerr), nil)
	}
	r.emit(StageSegmenting, len(seg.Chapters), len(seg.Chapters),
		fmt.Sprintf("Phân đoạn hoàn tất: %d chương, %d vùng phụ", len(seg.Chapters), len(seg.Matter)), nil)
	return nil
}

// confirm xử lý xác nhận phân đoạn. --yes tự động chấp nhận và ghi artefact confirmation; nếu không thì hiển thị bản xem trước và dừng.
func (r *runner) confirm() bool {
	seg, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		r.fail("đọc kết quả phân đoạn", err)
		return false
	}
	in, err := r.ws.LoadIntent()
	if err != nil {
		r.fail("đọc ý định nhập", err)
		return false
	}
	accept := r.opts.AcceptSegmentation
	auto := r.opts.AutoConfirm || (in != nil && in.AutoConfirm)
	// Tolerances ngữ nghĩa đã từng xảy ra (Notes không rỗng: hấp thụ chương trống / dự phòng đầu vào / khử trùng lặp vùng chồng lấn) thì không được để --yes cho qua mù:
	// cấu trúc đã bị viết lại một cách xác định, bắt buộc phải kiểm tra thủ công — nếu không thì ghi chú dung sai ở dưới --yes sẽ không ai nhìn thấy, chẳng khác gì tự ý sửa âm thầm.
	// Sau khi xem trước trên TUI rồi nhấn y sẽ đi theo AcceptSegmentation (quyết định rõ ràng sau khi đã xem preview), không bị ràng buộc này.
	blockedByNotes := auto && !accept && len(seg.Payload.Notes) > 0
	if blockedByNotes {
		auto = false
	}
	if !auto && !accept {
		msg := buildConfirmPreview(&seg.Payload)
		if blockedByNotes {
			msg += "  ! Có ghi chú dung sai phân đoạn, --yes chưa được tự động chấp nhận, vui lòng kiểm tra thủ công\n"
		}
		r.emit(StageAwaitingConfirmation, len(seg.Payload.Chapters), len(seg.Payload.Chapters), msg, nil)
		return false
	}
	raw, err := r.ws.readBytes(fileSegmentation)
	if err != nil {
		r.fail("đọc artefact phân đoạn", err)
		return false
	}
	method, doneMsg := confirmMethodAuto, "Đã tự động chấp nhận phân đoạn (--yes)"
	if accept {
		method, doneMsg = confirmMethodUser, "Đã xác nhận phân đoạn (kiểm tra thủ công)"
	}
	conf := Confirmation{Method: method, Chapters: len(seg.Payload.Chapters)}
	if err := writeArtifact(r.ws, fileConfirmation, Digest(raw), conf); err != nil {
		r.fail("ghi artefact xác nhận", err)
		return false
	}
	r.emit(StageAwaitingConfirmation, len(seg.Payload.Chapters), len(seg.Payload.Chapters), doneMsg, nil)
	return true
}

// buildConfirmPreview ghép bản xem trước để xác nhận phân đoạn: số chương, vùng phụ, toàn bộ tiêu đề chương và cờ uncertain (RFC §8.4).
// Liệt kê toàn bộ, panel viewport có thể cuộn để xem; không đặt giới hạn cắt ngắn.
func buildConfirmPreview(seg *Segmentation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Đã chia %d chương", len(seg.Chapters))
	if len(seg.Matter) > 0 {
		fmt.Fprintf(&b, " và %d vùng phụ", len(seg.Matter))
	}
	if len(seg.Uncertain) > 0 {
		fmt.Fprintf(&b, " (%d chương còn nghi vấn)", len(seg.Uncertain))
	}
	b.WriteString(", vui lòng kiểm tra:\n")
	uncertain := make(map[int]bool, len(seg.Uncertain))
	for _, n := range seg.Uncertain {
		uncertain[n] = true
	}
	for _, c := range seg.Chapters {
		fmt.Fprintf(&b, "  Chương %d %s", c.Number, c.Title)
		if uncertain[c.Number] {
			b.WriteString("  [nghi vấn]")
		}
		b.WriteByte('\n')
	}
	for _, mt := range seg.Matter {
		fmt.Fprintf(&b, "  [%s] %s\n", mt.Kind, mt.Title)
	}
	// Ghi chú dung sai ở giai đoạn phân đoạn (như tiêu đề placeholder của phần thân rỗng được hút vào đoạn trước) phải được hiện trên điểm dừng thủ công, nếu không hành vi hấp thụ sẽ biến thành sửa đổi âm thầm.
	for _, n := range seg.Notes {
		fmt.Fprintf(&b, "  ! %s\n", n)
	}
	// Gợi ý thao tác (y để xác nhận / --guide để chia lại / Esc) do khối tạm dừng của TUI tự vẽ thống nhất, ở đây chỉ giữ факт để tránh lệch nội dung hai nơi.
	return b.String()
}

func (r *runner) analyze(ctx context.Context) error {
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	seg := &segArt.Payload
	total := len(seg.Chapters)
	// Digest theo từng chương chỉ gắn với chính văn của chương đó, không gồm context batch hay ledger trước đó. Nếu chương K vì thiếu/không khớp mà phải phân tích lại,
	// các artefact cũ phía sau có digest trùng khớp lại mang ledger đã hết hạn sẽ bị tái sử dụng. Dọn phần đuôi vượt qua tiền tố mới ngay trước khi phân tích,
	// buộc "phân tích lại một chương tức là làm mất hiệu lực toàn bộ phân tích sau nó", sau đó phân tích xuôi sẽ không còn sinh đuôi lỗi thời (RFC §9.6 / #4a).
	if err := discardAnalysesAfter(r.ws, analyzedChapters(r.ws, seg, src, segArt.InputDigest, analyzePromptVersion), total); err != nil {
		return err
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		start := analyzedChapters(r.ws, seg, src, segArt.InputDigest, analyzePromptVersion)
		if start >= total {
			break
		}
		r.emit(StageAnalyzing, start, total, fmt.Sprintf("Phân tích batch liên tiếp bắt đầu từ chương %d...", start+1), nil)
		done, err := AnalyzeNext(ctx, r.deps.Analyze.Model, r.deps.Prompts.Analyze, r.ws, src, seg, segArt.InputDigest, analyzePromptVersion, r.deps.Budgets.Analyze, r.profileFor(r.deps.Analyze, StageAnalyzing))
		if err != nil {
			return err
		}
		if done == 0 {
			break
		}
	}
	r.emit(StageAnalyzing, total, total, "Trích xuất факт theo từng chương hoàn tất", nil)
	return nil
}

func (r *runner) synthesize(ctx context.Context) error {
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	total := len(segArt.Payload.Chapters)
	facts := loadPriorFacts(r.ws, total)
	if len(facts) != total {
		return fmt.Errorf("phân tích theo từng chương chưa đầy đủ: %d/%d", len(facts), total)
	}
	r.emit(StageSynthesizing, 0, total, "Đang tổng hợp ngữ nghĩa toàn sách theo tầng...", nil)
	syn, err := Synthesize(ctx, r.deps.Synthesize.Model, r.deps.Prompts.Synthesize, r.deps.Prompts.Range, r.ws, facts,
		r.deps.Budgets.SynthesizeRangeBytes, r.deps.Budgets.SynthesizeMaxTokens, r.profileFor(r.deps.Synthesize, StageSynthesizing))
	if err != nil {
		return err
	}
	if err := writeArtifact(r.ws, fileSynthesis, synthesisInputDigest(facts), *syn); err != nil {
		return err
	}
	r.emit(StageSynthesizing, total, total, fmt.Sprintf("Tổng hợp hoàn tất: %d tập, trạng thái câu chuyện %s", len(syn.Structure), syn.StoryStatus), nil)
	return nil
}

func (r *runner) publish(ctx context.Context) error {
	synArt, err := readArtifact[BookSynthesis](r.ws, fileSynthesis)
	if err != nil {
		return err
	}
	segArt, err := readArtifact[Segmentation](r.ws, fileSegmentation)
	if err != nil {
		return err
	}
	seg := &segArt.Payload
	src, err := r.ws.LoadSource()
	if err != nil {
		return err
	}
	total := len(seg.Chapters)
	facts := loadPriorFacts(r.ws, total)
	if len(facts) != total {
		return fmt.Errorf("phân tích trước khi phát hành chưa đầy đủ: %d/%d", len(facts), total)
	}
	closed, err := r.resolveStory(&synArt.Payload)
	if err != nil {
		return err
	}
	manifest, err := r.ws.LoadManifest()
	if err != nil {
		return err
	}
	f, err := AssembleFoundation(&synArt.Payload, facts, closed, manifest.SourceName)
	if err != nil {
		return err
	}
	r.emit(StageValidating, 0, total, "Kiểm tra lắp ghép Foundation thành công", nil)

	r.emit(StagePublishing, 0, total, "Đang phát hành Foundation chính thức...", nil)
	if err := publishFoundation(r.deps.Store, f); err != nil {
		return err
	}
	// Hold khi hoàn tất import phải được đặt trước mọi lần commit chapter để persisting: nếu giữa "commit chương cuối"
	// và "đặt Hold" xảy ra crash, khi khởi động lại isPublished=true → import bị xem là xong nhưng Hold lại thiếu, Engine sẽ nhầm sách nhập
	// thành một lần dừng bình thường và tiếp tục viết.
	// Đặt nó sau publishFoundation (RunMeta đã được khởi tạo) nhưng trước khi commit chương, để đóng hẳn cửa sổ này; khi chạy lại publish thì sẽ idempotent
	// đặt lại ( --continue không đặt Hold, giao cho tiếp nối tự động, RFC §12.4 ).
	if err := r.setCompletionHold(); err != nil {
		return fmt.Errorf("thiết lập Hold hoàn tất nhập: %w", err)
	}
	for i, c := range seg.Chapters {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.emit(StagePublishing, c.Number, total, fmt.Sprintf("Phát hành chương %d/%d: %s", c.Number, total, c.Title), nil)
		if err := publishChapter(ctx, r.deps.Store, r.deps.CommitChapter, c.Number, seg.Content(src, i), facts[i]); err != nil {
			return err
		}
	}
	return nil
}

// storyChoice trả về quyết định hợp lệ cho trạng thái uncertain: ưu tiên quyết định đã ghi đĩa của synthesis hiện tại, sau đó đến opts lần này, rồi mới đến intent gốc.
// Quyết định đã ghi đĩa phải kiểm tra InputDigest khớp với synthesis hiện tại — nếu tổng hợp lại thì quyết định cũ hết hiệu lực, không được âm thầm áp open/closed cũ
// lên kết quả mới, nếu không người dùng sẽ không bị hỏi lại (RFC §10.4). --story tường minh (intent) là chỉ thị thường trú của người dùng xuyên suốt các lần tổng hợp, có thể giữ.
func (r *runner) storyChoice() (string, error) {
	if raw, err := r.ws.readBytes(fileSynthesis); err == nil {
		if art, aerr := readArtifact[StoryResolution](r.ws, fileStoryResolve); aerr == nil && art.InputDigest == Digest(raw) {
			return art.Payload.Choice, nil
		} else if aerr != nil && !os.IsNotExist(aerr) {
			return "", fmt.Errorf("đọc quyết định trạng thái câu chuyện: %w", aerr)
		}
	} else {
		return "", fmt.Errorf("đọc artefact tổng hợp: %w", err)
	}
	if r.opts.StoryResolution != "" {
		return r.opts.StoryResolution, nil
	}
	in, err := r.ws.LoadIntent()
	if err != nil {
		return "", fmt.Errorf("đọc ý định nhập: %w", err)
	}
	return in.StoryResolution, nil
}

// resolveStoryStatus khi uncertain và đã có quyết định tường minh thì ghi story-resolution.json xuống đĩa (gắn với synthesis hiện tại),
// để NextAction của hạ nguồn tự nhiên được phép chạy tiếp; nếu chưa có quyết định thì hiện trạng thái chờ và dừng.
func (r *runner) resolveStoryStatus() bool {
	choice, err := r.storyChoice()
	if err != nil {
		r.fail("đọc quyết định trạng thái câu chuyện", err)
		return false
	}
	if choice != storyOpen && choice != storyClosed {
		r.emit(StageAwaitingStoryStatus, 0, 0, "Synthesis đánh giá trạng thái câu chuyện là uncertain, vui lòng dùng --story=open|closed để xác định rồi thử lại", nil)
		return false
	}
	raw, err := r.ws.readBytes(fileSynthesis)
	if err != nil {
		r.fail("đọc kết quả tổng hợp", err)
		return false
	}
	if err := writeArtifact(r.ws, fileStoryResolve, Digest(raw), StoryResolution{Choice: choice}); err != nil {
		r.fail("ghi quyết định trạng thái câu chuyện xuống đĩa", err)
		return false
	}
	return true
}

// resolveStory dựa trên kết quả tổng hợp và quyết định tường minh của người dùng để xác định trạng thái khép lại của câu chuyện (RFC §10.4).
func (r *runner) resolveStory(syn *BookSynthesis) (bool, error) {
	switch syn.StoryStatus {
	case storyClosed:
		return true, nil
	case storyOpen:
		return false, nil
	case storyUncertain:
		choice, err := r.storyChoice()
		if err != nil {
			return false, err
		}
		switch choice {
		case storyClosed:
			return true, nil
		case storyOpen:
			return false, nil
		default:
			return false, fmt.Errorf("trạng thái câu chuyện uncertain, cần --story=open|closed")
		}
	default:
		return false, fmt.Errorf("story_status không xác định: %q", syn.StoryStatus)
	}
}

// setCompletionHold đặt một Hold khi hoàn tất import; chỉ --continue mới bỏ qua (RFC §12.4).
// Lỗi phải được truyền lên — Hold là bảo đảm duy nhất để "không tiếp tục viết nhầm sau khi nhập xong", thất bại âm thầm đồng nghĩa với mất tác dụng bảo vệ.
func (r *runner) setCompletionHold() error {
	in, err := r.ws.LoadIntent()
	if err != nil {
		return fmt.Errorf("đọc ý định nhập: %w", err)
	}
	if r.opts.ContinueAfter || (in != nil && in.ContinueAfterImport) {
		return nil
	}
	return r.deps.Store.RunMeta.SetAdvanceHold(domain.AdvanceHold{
		After:  domain.AdvanceHoldAtBoundary,
		Reason: "Đã hoàn tất nhập tiểu thuyết bên ngoài, chờ nghiệm thu rồi tiếp tục viết",
	})
}
