package imp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// testDeps xây dựng Deps tối thiểu để ba hàm ngữ nghĩa cùng dùng chung một mock slot.
func testDeps(st *store.Store, m callModel) Deps {
	c := Caller{Model: m}
	return Deps{
		Store:         st,
		CommitChapter: tools.NewCommitChapterTool(st, tools.NewStyleStatsIndex(st)),
		Segment:       c,
		Analyze:       c,
		Synthesize:    c,
		Prompts:       Prompts{Segment: "seg", Analyze: "ana", Synthesize: "syn", Range: "range"},
	}
}

// TestRunEndToEnd dùng mock model điều khiển toàn bộ pipeline ingest→segment→analyze→synthesize→publish,
// qua commit_chapter thật ghi xuống đĩa, xác minh Foundation chính thức và mọi chương đã sẵn sàng.
func TestRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("store init: %v", err)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung một\nChương hai\nNội dung hai\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	seg := boundariesJSON(
		boundaryFixture("L1", "", kindChapter, "Chương một"),
		boundaryFixture("L3", "", kindChapter, "Chương hai"),
	)
	ana := `{"chapters":[` + factsJSON(1, "Chương một") + `,` + factsJSON(2, "Chương hai") + `]}`
	syn := synthesisFixtureJSON(2, storyClosed)
	m := &mockModel{responses: []string{seg, ana, syn}}

	ch, err := Run(context.Background(), testDeps(st, m), Options{SourcePath: src, AutoConfirm: true, ContinueAfter: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var runErr error
	var doneSeen bool
	for ev := range ch {
		if ev.Stage == StageError {
			runErr = ev.Err
		}
		if ev.Stage == StageDone {
			doneSeen = true
		}
	}
	if runErr != nil {
		t.Fatalf("Pipeline thất bại: %v", runErr)
	}
	if !doneSeen {
		t.Fatal("Chưa nhận được StageDone")
	}
	// Trạng thái chính thức đã sẵn sàng: thông tin tác phẩm, premise và dàn ý phẳng bao phủ toàn bộ chương đã được ghi xuống đĩa (world_rules có thể rỗng hợp lệ, không bắt buộc).
	if book, _ := st.Book.Load(); book == nil || book.Synopsis == "" {
		t.Fatalf("Thông tin tác phẩm chưa được ghi xuống đĩa: %+v", book)
	}
	if p, _ := st.Outline.LoadPremise(); p == "" {
		t.Fatal("premise chưa được ghi xuống đĩa")
	}
	if o, _ := st.Outline.LoadOutline(); len(o) != 2 {
		t.Fatalf("Dàn ý phẳng phải bao phủ 2 chương, nhận %d", len(o))
	}
	prog, _ := st.Progress.Load()
	if prog == nil || len(prog.CompletedChapters) != 2 {
		t.Fatalf("Phải hoàn thành 2 chương: %+v", prog)
	}
	if active, done, err := ResumeStatus(st); err != nil || !active || !done {
		t.Fatalf("ResumeStatus phải là active&done, nhận active=%v done=%v", active, done)
	}
	// --continue: không đặt Hold hoàn tất nhập (để host tự động tiếp nối).
	if meta, _ := st.RunMeta.Load(); meta != nil && meta.AdvanceHold != nil {
		t.Fatalf("--continue không nên để lại Hold hoàn tất nhập: %+v", meta.AdvanceHold)
	}
}

// TestRunSetsCompletionHold xác minh sau khi nhập xong mà không dùng --continue thì sẽ đặt boundary Hold (RFC §12.4).
// Hold là bảo đảm duy nhất cho việc "sau khi nhập không ghi tiếp nhầm", bắt buộc phải được lưu bền vững trên đường phát hành.
func TestRunSetsCompletionHold(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("store init: %v", err)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung một\nChương hai\nNội dung hai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seg := boundariesJSON(
		boundaryFixture("L1", "", kindChapter, "Chương một"),
		boundaryFixture("L3", "", kindChapter, "Chương hai"),
	)
	ana := `{"chapters":[` + factsJSON(1, "Chương một") + `,` + factsJSON(2, "Chương hai") + `]}`
	syn := synthesisFixtureJSON(2, storyClosed)
	m := &mockModel{responses: []string{seg, ana, syn}}

	ch, err := Run(context.Background(), testDeps(st, m), Options{SourcePath: src, AutoConfirm: true}) // không có --continue
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for ev := range ch {
		if ev.Stage == StageError {
			t.Fatalf("Pipeline thất bại: %v", ev.Err)
		}
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		t.Fatalf("load run meta: %v", err)
	}
	if meta == nil || meta.AdvanceHold == nil {
		t.Fatalf("Nhập xong phải đặt boundary Hold, nhận %+v", meta)
	}
}

// TestRunRejectsDifferentSource bảo vệ chặn đổi nguồn (RFC §12.1/§18.2): khi workspace đang chạy mà truyền vào
// một nguồn có nội dung khác thì phải báo lỗi rõ ràng — ingest chỉ chạy khi chưa có workspace, nếu không đối chiếu thì sẽ âm thầm tiếp tục từ điểm ngắt của sách cũ,
// phát hành xong sách cũ mà tệp mới thì chưa đọc một byte nào. Trùng đường dẫn của cùng một tệp là thói quen khôi phục phổ biến, nên so khớp theo digest nội dung để cho qua.
func TestRunRejectsDifferentSource(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte("Chương một\nNội dung một\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ingest(dir, a, Options{}.intent()); err != nil {
		t.Fatalf("Tạo workspace: %v", err)
	}
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(b, []byte("Một cuốn sách khác hoàn toàn\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ch, err := Run(context.Background(), testDeps(st, &mockModel{responses: []string{"{}"}}), Options{SourcePath: b})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var runErr error
	for ev := range ch {
		if ev.Stage == StageError {
			runErr = ev.Err
		}
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "nội dung khác nhau") {
		t.Fatalf("Nguồn khác phải bị từ chối rõ ràng, nhận %v", runErr)
	}
}

// TestConfirmNotesGate bảo vệ ngưỡng chịu lỗi của --yes: cấu trúc cắt tách có phát sinh chịu lỗi ngữ nghĩa (Notes không rỗng)
// đã bị viết lại một cách xác định, nên --yes không được cho qua mù khi chưa xem trước; sau khi TUI xem preview và nhấn y (AcceptSegmentation) thì cho qua,
// phương pháp xác nhận sẽ được ghi là user_confirmed để truy vết.
func TestConfirmNotesGate(t *testing.T) {
	newRunner := func(opts Options, notes []string) *runner {
		ws := &Workspace{dir: t.TempDir()}
		if err := ws.writeJSON(fileIntent, Intent{}); err != nil {
			t.Fatal(err)
		}
		seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "Chương một", End: 10}}, Notes: notes}
		if err := writeArtifact(ws, fileSegmentation, "d", seg); err != nil {
			t.Fatal(err)
		}
		return &runner{opts: opts, events: make(chan Event, 8), ws: ws}
	}
	r := newRunner(Options{AutoConfirm: true}, []string{"Phần nội dung trống được gộp vào phần trước"})
	if r.confirm() {
		t.Fatal("--yes không được cho qua khi có ghi chú chịu lỗi")
	}
	if ev := <-r.events; !strings.Contains(ev.Message, "chưa được tự động chấp nhận") {
		t.Fatalf("Preview phải nói rõ lý do chưa cho qua: %q", ev.Message)
	}
	if !newRunner(Options{AutoConfirm: true}, nil).confirm() {
		t.Fatal("--yes phải cho qua khi không có ghi chú chịu lỗi")
	}
	r = newRunner(Options{AcceptSegmentation: true}, []string{"Phần nội dung trống được gộp vào phần trước"})
	if !r.confirm() {
		t.Fatal("Sau preview, nhấn y thủ công phải cho qua cấu trúc có ghi chú chịu lỗi")
	}
	conf, err := readArtifact[Confirmation](r.ws, fileConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if conf.Payload.Method != confirmMethodUser {
		t.Fatalf("Xác nhận thủ công phải ghi user_confirmed, nhận %q", conf.Payload.Method)
	}
}

// TestStoryChoiceIgnoresStaleResolution bảo vệ #5: sau khi tổng hợp lại thì phán quyết cũ về chuyện sẽ hết hiệu lực,
// storyChoice không được âm thầm áp open/closed cũ lên synthesis mới (nếu không người dùng sẽ không bị hỏi lại).
func TestStoryChoiceIgnoresStaleResolution(t *testing.T) {
	ws := OpenWorkspace(t.TempDir())
	if err := ws.writeJSON(fileIntent, Intent{}); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifact(ws, fileSynthesis, "d", BookSynthesis{Premise: "p1", StoryStatus: storyUncertain}); err != nil {
		t.Fatal(err)
	}
	raw, _ := ws.readBytes(fileSynthesis)
	if err := writeArtifact(ws, fileStoryResolve, Digest(raw), StoryResolution{Choice: storyClosed}); err != nil {
		t.Fatal(err)
	}
	r := &runner{ws: ws}
	if got, err := r.storyChoice(); err != nil || got != storyClosed {
		t.Fatalf("Phán quyết gắn với synthesis hiện tại phải trả về closed, nhận %q", got)
	}
	// Tổng hợp lại: viết lại synthesis → InputDigest của phán quyết cũ không khớp, phải bị bỏ qua, quay về "cần hỏi lại" (trả về rỗng).
	if err := writeArtifact(ws, fileSynthesis, "d", BookSynthesis{Premise: "p2", StoryStatus: storyUncertain}); err != nil {
		t.Fatal(err)
	}
	if got, err := r.storyChoice(); err != nil || got != "" {
		t.Fatalf("Sau khi tổng hợp lại, phán quyết cũ phải hết hiệu lực và trả về rỗng, nhận %q", got)
	}
}

// TestBudgetsFromDepsPerTier bảo vệ nút chỉnh tier (RFC §13.1): ngân sách của từng hàm ngữ nghĩa được suy ra theo tier riêng,
// cửa sổ nhỏ ở tier rẻ chỉ ràng buộc chính hàm của nó, không kéo tụt các giai đoạn khác.
func TestBudgetsFromDepsPerTier(t *testing.T) {
	small := ModelRuntime{ContextTokens: 32000, MaxOutputTokens: 4000}
	big := ModelRuntime{ContextTokens: 200000, MaxOutputTokens: 16000}
	b := budgetsFromDeps(Deps{
		Segment:    Caller{Runtime: small},
		Analyze:    Caller{Runtime: big},
		Synthesize: Caller{Runtime: big},
	})
	if b.SegmentChunkBytes >= b.Analyze.ContextBytes {
		t.Fatalf("Cửa sổ gói nhỏ của segment chỉ nên ràng buộc chính nó: seg=%d analyze=%d", b.SegmentChunkBytes, b.Analyze.ContextBytes)
	}
	if b.Analyze.MaxOutputTokens != 16000 || b.SegmentMaxTokens != 4000 {
		t.Fatalf("Ngân sách đầu ra phải lấy giới hạn của chính từng tier: analyze=%d segment=%d", b.Analyze.MaxOutputTokens, b.SegmentMaxTokens)
	}
}

// TestRunSavesFailureOnContractViolation bảo vệ §14.2: vi phạm hợp đồng Schema gốc
// phải được lộ ra ngay, đồng thời lưu phản hồi nguyên bản và metadata vào failures/.
func TestRunSavesFailureOnContractViolation(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &nativeImportModel{mockModel: &mockModel{responses: []string{"Đây không phải JSON"}}}
	ch, err := Run(context.Background(), testDeps(st, m), Options{SourcePath: src, AutoConfirm: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var failed bool
	for ev := range ch {
		if ev.Stage == StageError {
			failed = true
		}
	}
	if !failed {
		t.Fatal("Đầu ra không hợp lệ phải kết thúc bằng StageError")
	}
	ws := OpenWorkspace(dir)
	if !ws.has("failures/last-response.txt") {
		t.Fatal("Phải lưu phản hồi thô cuối cùng của model")
	}
	var meta FailureMeta
	if err := ws.readJSON("failures/last.json", &meta); err != nil {
		t.Fatalf("đọc metadata thất bại: %v", err)
	}
	if meta.Stage != string(ActionSegment) {
		t.Fatalf("Metadata thất bại phải đánh dấu giai đoạn segment, nhận %q", meta.Stage)
	}
}

// TestRunGuidanceResegments bảo vệ §18.3: khi khôi phục có kèm --guide thì cấu trúc cắt tách cũ sẽ tự nhiên không khớp,
// phải nhận diện lại theo hướng dẫn mới và dừng ở bước xác nhận lần nữa; InputDigest của cấu trúc mới phải gắn với văn bản hướng dẫn.
func TestRunGuidanceResegments(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung một\nChương hai\nNội dung hai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drain := func(ch <-chan Event) (awaiting bool) {
		for ev := range ch {
			if ev.Stage == StageError {
				t.Fatalf("Pipeline thất bại: %v", ev.Err)
			}
			if ev.Stage == StageAwaitingConfirmation {
				awaiting = true
			}
		}
		return awaiting
	}
	// Lần nhập tương tác đầu tiên: model cắt cả sách thành 1 chương, dừng ở bước xác nhận.
	one := boundariesJSON(boundaryFixture("L1", "", kindChapter, "Chương một"))
	ch, err := Run(context.Background(), testDeps(st, &mockModel{responses: []string{one}}), Options{SourcePath: src})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !drain(ch) {
		t.Fatal("Lần nhập đầu phải dừng ở bước xác nhận chia cắt")
	}
	// Khôi phục kèm hướng dẫn: cấu trúc cũ không khớp → nhận diện lại thành 2 chương, rồi lại dừng ở bước xác nhận.
	two := boundariesJSON(
		boundaryFixture("L1", "", kindChapter, "Chương một"),
		boundaryFixture("L3", "", kindChapter, "Chương hai"),
	)
	guidance := "Chương hai cũng là một chương độc lập"
	ch2, err := Run(context.Background(), testDeps(st, &mockModel{responses: []string{two}}), Options{Guidance: guidance})
	if err != nil {
		t.Fatalf("Run khôi phục: %v", err)
	}
	if !drain(ch2) {
		t.Fatal("Sau khi nhận diện lại phải dừng ở bước xác nhận chia cắt")
	}
	ws := OpenWorkspace(dir)
	art, err := readArtifact[Segmentation](ws, fileSegmentation)
	if err != nil {
		t.Fatalf("đọc artifact chia cắt: %v", err)
	}
	if len(art.Payload.Chapters) != 2 {
		t.Fatalf("Phải cắt thành 2 chương theo hướng dẫn, nhận %d", len(art.Payload.Chapters))
	}
	norm, _ := ws.LoadSource()
	if art.InputDigest != segmentInputDigest(Digest(norm), guidance, segmentPromptVersion) {
		t.Fatal("InputDigest của cấu trúc mới phải gắn với văn bản hướng dẫn")
	}
}

// TestBudgetsFromRuntime xác minh hai ngân sách sẽ tăng theo năng lực thực của model, và khi năng lực chưa biết thì quay về mặc định thận trọng (RFC §9.2/§21).
func TestBudgetsFromRuntime(t *testing.T) {
	if got := budgetsFromRuntime(ModelRuntime{}); got != DefaultRunBudgets() {
		t.Fatal("Khi năng lực chưa biết thì phải quay về mặc định thận trọng")
	}
	small := budgetsFromRuntime(ModelRuntime{ContextTokens: 32000, MaxOutputTokens: 4000})
	big := budgetsFromRuntime(ModelRuntime{ContextTokens: 200000, MaxOutputTokens: 16000})
	if big.Analyze.ContextBytes <= small.Analyze.ContextBytes {
		t.Fatalf("Context lớn hơn phải tăng ngân sách đầu vào cho analyze: small=%d big=%d", small.Analyze.ContextBytes, big.Analyze.ContextBytes)
	}
	if big.Analyze.MaxOutputTokens != 16000 {
		t.Fatalf("Ngân sách đầu ra phải lấy giới hạn completion của model, nhận %d", big.Analyze.MaxOutputTokens)
	}
}

// TestProfileForKeyPolicy bảo vệ phạm vi gộp sự kiện: backoff của request (có thời điểm hết hạn) sẽ nhảy tại chỗ với cùng Key;
// kiểm tra hỏi lại là sự kiện ngữ nghĩa xuyên suốt nhiều lời gọi, không có Key thì mỗi cái một dòng — chia tách theo từng khối, nếu dùng chung Key thì khối sau sẽ ghi đè khối trước,
// trên bảng chỉ còn một dòng unit_id cứ đổi liên tục, toàn bộ manh mối tra lỗi sẽ mất sạch; step là sự kiện tiến độ bình thường (không có mức cảnh báo).
func TestProfileForKeyPolicy(t *testing.T) {
	r := &runner{events: make(chan Event, 3)}
	prof := r.profileFor(Caller{}, StageSegmenting)
	prof.notify("lùi lại", time.Now().Add(time.Second))
	prof.notify("hỏi lại", time.Time{})
	prof.step(2, 12, "Đang tách khối %d/%d...", 2, 12)
	backoff, reask, step := <-r.events, <-r.events, <-r.events
	if backoff.Key == "" || backoff.Level != "warn" || backoff.RetryAt.IsZero() {
		t.Fatalf("Sự kiện backoff của request phải là warn có Key và thời điểm hết hạn: %+v", backoff)
	}
	if reask.Key != "" || reask.Level != "warn" {
		t.Fatalf("Sự kiện hỏi lại kiểm tra phải là warn không có Key (mỗi dòng riêng): %+v", reask)
	}
	if step.Level != "" || step.Current != 2 || step.Total != 12 {
		t.Fatalf("step phải là sự kiện tiến độ bình thường: %+v", step)
	}
}

// TestCallProfileOptions xác minh callProfile chỉ phụ trách ngân sách đầu ra và thinking; response_format
// được callStructured chọn theo sự thật của model và Contract, không được lắp ráp lại trong Profile.
func TestCallProfileOptions(t *testing.T) {
	if got := (callProfile{}).callOptions(100); len(got) != 1 {
		t.Fatalf("Giá trị zero chỉ nên kèm maxTokens, nhận %d option", len(got))
	}
	if got := (callProfile{thinking: "high"}).callOptions(100); len(got) != 2 {
		t.Fatalf("thinking nên kèm 2 option, nhận %d", len(got))
	}
}
