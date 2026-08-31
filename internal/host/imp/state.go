package imp

import (
	"fmt"
	"os"

	"github.com/voocel/ainovel-cli/internal/store"
)

// Action là hành động xác định tiếp theo mà NextAction suy ra từ các sự kiện trong workspace.
// Trạng thái bền vững không ghi enum giai đoạn dễ trôi; hành động tiếp theo chỉ được suy ra từ artifact (RFC §6.2).
type Action string

const (
	ActionIngest               Action = "ingest"
	ActionSegment              Action = "segment"
	ActionAwaitConfirmation    Action = "await_confirmation"
	ActionAnalyze              Action = "analyze"
	ActionSynthesize           Action = "synthesize"
	ActionAwaitStoryResolution Action = "await_story_resolution"
	ActionPublish              Action = "publish"
	ActionDone                 Action = "done"
)

// Facts là snapshot tối thiểu các sự kiện đọc từ workspace, cần để quyết định hành động tiếp theo.
// Tách quyết định thuần túy (NextAction) khỏi IO (LoadState): NextAction ổn định với cùng một Facts (RFC §20.1).
type Facts struct {
	WorkspaceReady   bool // đủ bộ ba manifest + intent + source
	Segmented        bool
	Confirmed        bool
	ExpectedChapters int // tổng số chương đã xác nhận khi chia đoạn (được điền từ giai đoạn hai)
	AnalyzedChapters int // số phân tích liên tiếp từ chương 1, khớp InputDigest (được điền từ giai đoạn ba)
	Synthesized      bool
	StoryUncertain   bool
	StoryResolved    bool
	Published        bool // artifact chính thức hoàn toàn nhất quán với synthesis (được điền từ giai đoạn năm)
}

// NextAction đi theo pipeline tuyến tính cố định, trả về hành động đầu tiên còn thiếu hoặc chưa thỏa mãn. Hàm thuần, không IO.
func NextAction(f Facts) Action {
	switch {
	case f.Published:
		// Phát hành là trạng thái cuối: đối soát kho chính thức đã khớp toàn bộ, workspace chỉ là bản lưu kiểm toán. Artifact thượng nguồn
		// không còn bị yêu cầu làm lại khi phiên bản prompt / hướng dẫn nâng cấp làm mất độ mới nữa; nếu không, nâng cấp phiên bản sẽ
		// truy hồi đẩy sách đã phát hành về giữa chừng, và Engine sẽ khóa vĩnh viễn qua cổng chặn sau khi khởi động lại.
		return ActionDone
	case !f.WorkspaceReady:
		return ActionIngest
	case !f.Segmented:
		return ActionSegment
	case !f.Confirmed:
		return ActionAwaitConfirmation
	case f.AnalyzedChapters < f.ExpectedChapters:
		return ActionAnalyze
	case !f.Synthesized:
		return ActionSynthesize
	case f.StoryUncertain && !f.StoryResolved:
		return ActionAwaitStoryResolution
	default:
		return ActionPublish
	}
}

// artifactFresh xác định artifact có tồn tại và InputDigest của nó bằng want hiện cần dựng lại hay không;
// thiếu, phân tích lỗi, schema hoặc digest không khớp đều được xem là không mới (cần làm lại).
func artifactFresh[T any](w *Workspace, rel, want string) (bool, error) {
	a, err := readArtifact[T](w, rel)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return a.InputDigest == want, nil
}

// LoadState đọc snapshot sự kiện hiện tại từ workspace (chỉ workspace, không gồm Store chính thức).
// Ngắt tuyến tính: mỗi bước đều kiểm tra InputDigest của artifact có nhất quán với digest hiện có thể dựng lại từ thượng nguồn hay không; chỉ cần một bước không khớp thì xem như bước đó chưa hoàn tất,
// các sự kiện hạ nguồn giữ false, giao cho NextAction làm lại từ đây -- như vậy mới khiến "sửa chia đoạn / phiên bản prompt / nguồn" tự nhiên làm mất hiệu lực hạ nguồn (RFC §6.2/§6.3 / invariant 1).
// Published do bên gọi bổ sung theo đối soát phát hành chính thức (đi thống nhất qua CollectFacts).
func LoadState(w *Workspace) (Facts, error) {
	var f Facts
	if !w.Active() {
		return f, nil
	}
	if !(w.has(fileManifest) && w.has(fileIntent) && w.has(fileSource)) {
		return f, nil
	}
	src, err := w.LoadSource()
	if err != nil {
		return f, fmt.Errorf("đọc snapshot nguồn nhập: %w", err)
	}
	f.WorkspaceReady = true
	guidance, err := w.LoadGuidance()
	if err != nil {
		return f, fmt.Errorf("đọc hướng dẫn chia đoạn: %w", err)
	}

	// segmentation: ràng buộc nguồn đã chuẩn hóa + hướng dẫn người dùng + phiên bản prompt chia đoạn. Thay đổi hướng dẫn (--guide nhận diện lại) sẽ tự nhiên làm mất hiệu lực chia đoạn cũ.
	segArt, err := readArtifact[Segmentation](w, fileSegmentation)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("đọc tạo tác phân đoạn: %w", err)
	}
	if segArt.InputDigest != segmentInputDigest(Digest(src), guidance, segmentPromptVersion) {
		return f, nil
	}
	f.Segmented = true
	seg := &segArt.Payload
	f.ExpectedChapters = len(seg.Chapters)

	// confirmation: ràng buộc byte gốc của artifact segmentation.
	segRaw, err := w.readBytes(fileSegmentation)
	if err != nil {
		return f, fmt.Errorf("đọc nguyên văn tạo tác phân đoạn: %w", err)
	}
	confirmed, err := artifactFresh[Confirmation](w, fileConfirmation, Digest(segRaw))
	if err != nil {
		return f, fmt.Errorf("đọc xác nhận phân đoạn: %w", err)
	}
	if !confirmed {
		return f, nil
	}
	f.Confirmed = true

	// Phân tích từng chương: số lượng liên tiếp mà InputDigest từng chương khớp với danh tính / phiên bản / nội dung chính văn của chia đoạn.
	f.AnalyzedChapters, err = analyzedChaptersStrict(w, seg, src, segArt.InputDigest, analyzePromptVersion)
	if err != nil {
		return f, err
	}
	if f.AnalyzedChapters < f.ExpectedChapters {
		return f, nil
	}

	// synthesis: ràng buộc các sự kiện từng chương có thứ tự.
	facts, err := loadPriorFactsStrict(w, f.ExpectedChapters)
	if err != nil {
		return f, err
	}
	synArt, err := readArtifact[BookSynthesis](w, fileSynthesis)
	if os.IsNotExist(err) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("đọc artifact tổng hợp toàn sách: %w", err)
	}
	if synArt.InputDigest != synthesisInputDigest(facts) {
		return f, nil
	}
	f.Synthesized = true
	f.StoryUncertain = synArt.Payload.StoryStatus == storyUncertain

	// story resolution: khi uncertain thì ràng buộc byte gốc của artifact synthesis, hoặc được chọn sẵn bởi intent.
	synRaw, err := w.readBytes(fileSynthesis)
	if err != nil {
		return f, fmt.Errorf("đọc nguyên văn artifact tổng hợp toàn sách: %w", err)
	}
	resolved, err := artifactFresh[StoryResolution](w, fileStoryResolve, Digest(synRaw))
	if err != nil {
		return f, fmt.Errorf("đọc phán định trạng thái câu chuyện: %w", err)
	}
	if resolved {
		f.StoryResolved = true
	} else if in, iErr := w.LoadIntent(); iErr != nil {
		return f, fmt.Errorf("đọc ý định nhập: %w", iErr)
	} else if in.StoryResolution != "" {
		f.StoryResolved = true
	}
	return f, nil
}

// CollectFacts kết hợp sự kiện workspace với đối soát phát hành chính thức, là đầu vào sự kiện thống nhất của ResumeStatus/ResumeSummary/runner.
// Số chương kỳ vọng khi đối soát phát hành ưu tiên lấy từ chia đoạn còn mới; khi chia đoạn không khớp do phiên bản prompt / nâng cấp hướng dẫn,
// lùi về số chương đã xác nhận trong artifact khi đó -- chương chính thức của sách đã phát hành được ghi vào kho chính theo chính bản chia đoạn ấy,
// dùng phiên bản hiện tại tính lại digest để đối soát thì lại chẳng khớp gì cả.
func CollectFacts(st *store.Store, w *Workspace) (Facts, error) {
	f, err := LoadState(w)
	if err != nil {
		return f, err
	}
	expected := f.ExpectedChapters
	if expected == 0 {
		if segArt, err := readArtifact[Segmentation](w, fileSegmentation); err == nil {
			expected = len(segArt.Payload.Chapters)
		}
	}
	f.Published, err = isPublished(st, expected)
	return f, err
}

// ResumeStatus báo cáo có workspace nhập đang hoạt động hay không, và nó đã hoàn tất triệt để hay chưa (gồm đối soát phát hành chính thức).
// Dùng cho cổng chặn Engine qua khởi động lại (RFC §12.5): khi active && !done thì cấm quy trình sáng tác thông thường tiêu thụ trạng thái phát hành dở dang.
func ResumeStatus(st *store.Store) (active, done bool, err error) {
	w := OpenWorkspace(st.Dir())
	if !w.Active() {
		return false, false, nil
	}
	f, err := CollectFacts(st, w)
	if err != nil {
		return true, false, err
	}
	return true, NextAction(f) == ActionDone, nil
}

// ResumeSummary tạo lời nhắc một dòng cho lần nhập chưa hoàn tất (RFC §18.2); nếu không có lần nhập chưa hoàn tất thì trả về chuỗi rỗng.
// Để host chủ động thông báo ở màn hình khởi động/chào mừng, tránh việc người dùng chỉ phát hiện sách này dừng giữa chừng khi sáng tác bị cổng chặn từ chối.
func ResumeSummary(st *store.Store) string {
	w := OpenWorkspace(st.Dir())
	if !w.Active() {
		return ""
	}
	f, err := CollectFacts(st, w)
	if err != nil {
		return "Phát hiện đọc trạng thái nhập bất thường: " + err.Error() + "; vui lòng chạy /import để xem và sửa"
	}
	var state string
	switch NextAction(f) {
	case ActionDone:
		return ""
	case ActionIngest, ActionSegment:
		state = "chưa hoàn tất việc phân đoạn"
	case ActionAwaitConfirmation:
		state = fmt.Sprintf("đã chia %d chương, đang chờ kiểm tra xác nhận", f.ExpectedChapters)
	case ActionAnalyze:
		state = fmt.Sprintf("đã phân tích %d/%d chương", f.AnalyzedChapters, f.ExpectedChapters)
	case ActionSynthesize:
		state = "phân tích từng chương đã hoàn tất, chờ tổng hợp toàn sách"
	case ActionAwaitStoryResolution:
		state = "chờ làm rõ trạng thái câu chuyện (--story=open|closed)"
	case ActionPublish:
		state = "tổng hợp đã hoàn tất, chờ phát hành trạng thái chính thức"
	}
	return "Phát hiện lần nhập chưa hoàn tất (" + state + "), nhập /import để tiếp tục từ điểm dừng"
}

// checkImportPreconditions kiểm tra điều kiện tiên quyết trước khi nhập mới (RFC §12.1):
// không có thông tin tác phẩm hiện có, chương đã hoàn tất và PendingCommit đang xử lý. Ngữ nghĩa hợp nhất tiểu thuyết hiện có với văn bản bên ngoài mới không rõ ràng, phiên bản đầu tiên từ chối rõ ràng.
func checkImportPreconditions(st *store.Store) error {
	book, err := st.Book.Load()
	if err != nil {
		return fmt.Errorf("đọc thông tin tác phẩm: %w", err)
	}
	if book != nil {
		return fmt.Errorf("đã có tác phẩm \"%s\", từ chối nhập tiểu thuyết bên ngoài vào sách không rỗng", book.Title)
	}
	prog, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("đọc tiến độ: %w", err)
	}
	if prog != nil && len(prog.CompletedChapters) > 0 {
		return fmt.Errorf("đã có %d chương hoàn tất, từ chối nhập tiểu thuyết bên ngoài vào sách không rỗng", len(prog.CompletedChapters))
	}
	pending, err := st.Signals.LoadPendingCommit()
	if err != nil {
		return fmt.Errorf("đọc lượt commit đang xử lý: %w", err)
	}
	if pending != nil {
		return fmt.Errorf("đang tồn tại lượt commit chương đang xử lý, vui lòng hoàn tất hoặc dọn dẹp trước khi nhập")
	}
	return nil
}
