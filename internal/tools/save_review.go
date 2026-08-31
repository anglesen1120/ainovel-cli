package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveReviewTool lưu kết quả đánh giá của Editor.
type SaveReviewTool struct {
	store *store.Store
}

func NewSaveReviewTool(store *store.Store) *SaveReviewTool {
	return &SaveReviewTool{store: store}
}

func (t *SaveReviewTool) Name() string { return "save_review" }
func (t *SaveReviewTool) Description() string {
	return "Lưu kết quả đánh giá và cập nhật trạng thái luồng. verdict chỉ có thể là accept/polish/rewrite." +
		"Editor quyết định verdict dựa trên toàn bộ ngữ cảnh, công cụ chỉ kiểm tra dữ kiện và cập nhật Progress nguyên tử." +
		"Trả về các dữ kiện có cấu trúc: verdict / affected_chapters / next_flow / next_chapter"
}
func (t *SaveReviewTool) Label() string { return "Lưu đánh giá" }

// Công cụ ghi (đồng thời cập nhật reviews/ và PendingRewrites/Flow của Progress), không cho phép chạy song song.
func (t *SaveReviewTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveReviewTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *SaveReviewTool) StrictSchema() bool                     { return true }

func (t *SaveReviewTool) Schema() map[string]any {
	issueSchema := schema.Object(
		schema.Property("type", schema.String("Trục vấn đề; có thể dùng các trục cơ bản trong gợi ý đánh giá hoặc viết trục cụ thể hơn")).Required(),
		schema.Property("severity", schema.Enum("Mức độ nghiêm trọng", "critical", "error", "warning")).Required(),
		schema.Property("description", schema.String("Mô tả vấn đề")).Required(),
		schema.Property("evidence", schema.String("Bằng chứng: trích đoạn gốc, tình tiết cụ thể hoặc dữ liệu trạng thái")).Required(),
		schema.Property("suggestion", llmcontract.Nullable(schema.String("Gợi ý sửa đổi; nếu không cần gợi ý thì để null"))).Required(),
		schema.Property("chapters", schema.Array("Chương thực sự chứa bằng chứng của vấn đề này; đánh giá cung phải nằm trong khoảng do nhiệm vụ chỉ định", schema.Int("Số chương"))).Required(),
		schema.Property("requires_change", schema.Bool("Vấn đề này có nên lập tức kích hoạt sửa lại các chương đã nêu hay không, do Editor quyết định dựa trên trải nghiệm đọc tổng thể")).Required(),
	)
	dimensionSchema := schema.Object(
		schema.Property("dimension", schema.String("Trục đánh giá; do nhiệm vụ đánh giá hiện tại và rubric quyết định")).Required(),
		schema.Property("score", schema.Int("Điểm số (0-100)")).Required(),
		schema.Property("comment", schema.String("Kết luận ngắn và bằng chứng cho trục này; mỗi trục đều bắt buộc")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương được đánh giá (đánh giá toàn cục điền số chương mới nhất)")).Required(),
		schema.Property("scope", schema.Enum("Phạm vi đánh giá", "chapter", "global", "arc")).Required(),
		schema.Property("dimensions", schema.Array("Chấm điểm theo từng trục; rubric cơ bản do gợi ý của Editor cung cấp, có thể bổ sung trục cụ thể hơn theo nhiệm vụ", dimensionSchema)).Required(),
		schema.Property("issues", schema.Array("Các vấn đề phát hiện được", issueSchema)).Required(),
		schema.Property("contract_status", llmcontract.Nullable(schema.Enum("Mức hoàn thành contract của chương; nếu không áp dụng thì null", "met", "partial", "missed"))).Required(),
		schema.Property("contract_misses", schema.Array("Các mục contract chưa hoàn thành hoặc vi phạm; nếu không có thì là mảng rỗng", schema.String(""))).Required(),
		schema.Property("contract_notes", llmcontract.Nullable(schema.String("Mô tả ngắn về mức thực hiện contract; nếu không có thì null"))).Required(),
		schema.Property("verdict", schema.Enum("Kết luận đánh giá", "accept", "polish", "rewrite")).Required(),
		schema.Property("summary", schema.String("Tổng kết đánh giá")).Required(),
	)
}

func (t *SaveReviewTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var r domain.ReviewEntry
	if err := json.Unmarshal(args, &r); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if r.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0")
	}
	boundary, err := t.normalizeReviewEntry(&r)
	if err != nil {
		return nil, err
	}
	reviewOutcome, err := reviewFlow(r.Verdict)
	if err != nil {
		return nil, err
	}

	affected := r.AffectedChapters

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil || !slices.Contains(progress.CompletedChapters, r.Chapter) {
		return nil, fmt.Errorf("review chapter %d must be completed", r.Chapter)
	}
	scope := domain.ChapterScope(r.Chapter)
	artifact := fmt.Sprintf("reviews/%02d.json", r.Chapter)
	var existing *domain.ReviewEntry
	switch r.Scope {
	case "arc":
		scope = domain.ArcScope(boundary.Volume, boundary.Arc)
		existing, err = t.store.World.LoadReview(r.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load arc review: %w", err)
		}
		if existing != nil && existing.Scope != "arc" {
			existing = nil
		}
	case "global":
		artifact = fmt.Sprintf("reviews/%02d-global.json", r.Chapter)
		existing, err = t.store.World.LoadGlobalReview(r.Chapter)
		if err != nil {
			return nil, fmt.Errorf("load global review: %w", err)
		}
	}
	if existing != nil {
		if !reflect.DeepEqual(*existing, r) {
			return nil, fmt.Errorf("Đánh giá tổng hợp của chương %d đã tồn tại và nội dung khác, từ chối ghi đè: %w", r.Chapter, errs.ErrToolConflict)
		}
		return t.finishReview(r, progress, scope, artifact)
	}
	switch r.Scope {
	case "arc":
		if err := requireAggregateTarget(t.store, flow.AggregateArcReview, boundary.Volume, boundary.Arc, r.Chapter); err != nil {
			return nil, err
		}
	case "global":
		if err := requireAggregateTarget(t.store, flow.AggregateGlobalReview, 0, 0, r.Chapter); err != nil {
			return nil, err
		}
	}

	// Trước tiên áp dụng nguyên tử trạng thái điều khiển, sau đó mới lưu công cụ đánh giá. Nếu bước hai thất bại, ý định sửa lại vẫn còn;
	// Sau khi Writer dọn hết hàng đợi, router sẽ vì thiếu công cụ đánh giá mà phân phát lại Editor, không bỏ qua phần đánh giá.
	latest, err := t.store.Progress.ApplyReviewOutcome(reviewOutcome, affected, r.Summary)
	if err != nil {
		return nil, fmt.Errorf("apply review outcome: %w", err)
	}
	if err := t.store.World.SaveReview(r); err != nil {
		return nil, fmt.Errorf("save review: %w", err)
	}

	return t.finishReview(r, latest, scope, artifact)
}

func (t *SaveReviewTool) finishReview(
	r domain.ReviewEntry,
	progress *domain.Progress,
	scope domain.Scope,
	artifact string,
) (json.RawMessage, error) {
	if _, err := t.store.Checkpoints.AppendArtifact(scope, "review", artifact); err != nil {
		return nil, fmt.Errorf("checkpoint review: %w", err)
	}

	// Dùng snapshot Progress trả về từ cập nhật nguyên tử làm dữ kiện, tránh đọc lại lần hai tạo thêm cửa sổ thất bại mới.
	nextFlow := string(domain.FlowWriting)
	nextChapter := 0
	if progress != nil {
		nextFlow = string(progress.Flow)
		nextChapter = progress.NextChapter()
	}

	return json.Marshal(map[string]any{
		"saved":             true,
		"chapter":           r.Chapter,
		"scope":             r.Scope,
		"verdict":           r.Verdict,
		"affected_chapters": r.AffectedChapters,
		"issues":            len(r.Issues),
		"next_flow":         nextFlow,
		"next_chapter":      nextChapter,
	})
}

func (t *SaveReviewTool) normalizeReviewEntry(r *domain.ReviewEntry) (*store.ArcBoundary, error) {
	switch r.Scope {
	case "chapter", "global", "arc":
	default:
		return nil, fmt.Errorf("phạm vi đánh giá không hợp lệ: %q", r.Scope)
	}
	if len(r.AffectedChapters) > 0 {
		return nil, fmt.Errorf("affected_chapters được suy ra từ issues[].chapters; không được gửi trực tiếp")
	}
	if strings.TrimSpace(r.Summary) == "" {
		return nil, fmt.Errorf("summary là bắt buộc")
	}
	if r.ContractStatus != "" && r.ContractStatus != "met" && r.ContractStatus != "partial" && r.ContractStatus != "missed" {
		return nil, fmt.Errorf("contract_status không hợp lệ: %q", r.ContractStatus)
	}
	for _, miss := range r.ContractMisses {
		if strings.TrimSpace(miss) == "" {
			return nil, fmt.Errorf("contract_misses không được chứa phần tử rỗng")
		}
	}
	var boundary *store.ArcBoundary
	if r.Scope == "arc" {
		var err error
		boundary, err = t.store.Outline.CheckArcBoundary(r.Chapter)
		if err != nil {
			return nil, fmt.Errorf("kiểm tra phạm vi cung: %w", err)
		}
		if boundary == nil || !boundary.IsArcEnd || boundary.EndChapter != r.Chapter {
			return nil, fmt.Errorf("chương đánh giá cung phải là điểm cuối cung")
		}
	}
	affectedSet := make(map[int]struct{})
	for i := range r.Issues {
		issue := &r.Issues[i]
		if strings.TrimSpace(issue.Description) == "" {
			return nil, fmt.Errorf("mô tả issue là bắt buộc")
		}
		if strings.TrimSpace(issue.Evidence) == "" {
			return nil, fmt.Errorf("bằng chứng issue là bắt buộc")
		}
		switch issue.Severity {
		case "critical", "error", "warning":
		default:
			return nil, fmt.Errorf("mức độ issue không hợp lệ: %q", issue.Severity)
		}
		if len(issue.Chapters) == 0 && r.Scope == "chapter" {
			issue.Chapters = []int{r.Chapter}
		}
		if len(issue.Chapters) == 0 {
			return nil, fmt.Errorf("issue chapters là bắt buộc khi scope=%s", r.Scope)
		}
		issue.Chapters = uniqueSortedChapters(issue.Chapters)
		for _, chapter := range issue.Chapters {
			switch r.Scope {
			case "chapter":
				if chapter != r.Chapter {
					return nil, fmt.Errorf("issue đánh giá chương phải tham chiếu chương %d, nhận %d", r.Chapter, chapter)
				}
			case "global":
				if chapter <= 0 || chapter > r.Chapter {
					return nil, fmt.Errorf("chương issue đánh giá toàn cục %d nằm ngoài phạm vi 1-%d", chapter, r.Chapter)
				}
			case "arc":
				if chapter < boundary.StartChapter || chapter > boundary.EndChapter {
					return nil, fmt.Errorf("chương issue đánh giá cung %d nằm ngoài phạm vi %d-%d", chapter, boundary.StartChapter, boundary.EndChapter)
				}
			}
			if issue.RequiresChange {
				affectedSet[chapter] = struct{}{}
			}
		}
	}
	if err := validateDimensions(r.Dimensions); err != nil {
		return nil, err
	}
	derived := make([]int, 0, len(affectedSet))
	for chapter := range affectedSet {
		derived = append(derived, chapter)
	}
	slices.Sort(derived)
	if r.Verdict == "accept" && len(derived) > 0 {
		return nil, fmt.Errorf("đánh giá accept không được chứa issue có requires_change=true")
	}
	if (r.Verdict == "rewrite" || r.Verdict == "polish") && len(derived) == 0 {
		return nil, fmt.Errorf("verdict=%s yêu cầu ít nhất một issue có requires_change=true", r.Verdict)
	}
	r.AffectedChapters = derived
	return boundary, nil
}

func uniqueSortedChapters(chapters []int) []int {
	seen := make(map[int]struct{}, len(chapters))
	for _, chapter := range chapters {
		seen[chapter] = struct{}{}
	}
	result := make([]int, 0, len(seen))
	for chapter := range seen {
		result = append(result, chapter)
	}
	slices.Sort(result)
	return result
}

// reviewFlow là điểm ánh xạ duy nhất giữa phán quyết văn học và giao thức lưu bền vững. verdict do Editor quyết định;
// ở đây chỉ chấp nhận ba kết quả điều khiển mà Router có thể khôi phục.
func reviewFlow(verdict string) (domain.FlowState, error) {
	switch verdict {
	case "accept":
		return domain.FlowWriting, nil
	case "polish":
		return domain.FlowPolishing, nil
	case "rewrite":
		return domain.FlowRewriting, nil
	default:
		return "", fmt.Errorf("verdict đánh giá không hợp lệ: %q", verdict)
	}
}

func validateDimensions(dimensions []domain.DimensionScore) error {
	if len(dimensions) == 0 {
		return fmt.Errorf("dimensions phải chứa ít nhất một đánh giá dựa trên bằng chứng")
	}

	seen := make(map[string]struct{}, len(dimensions))
	for _, dim := range dimensions {
		name := strings.TrimSpace(dim.Dimension)
		if name == "" {
			return fmt.Errorf("tên dimension là bắt buộc")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("dimension trùng lặp: %s", name)
		}
		seen[name] = struct{}{}
		if dim.Score < 0 || dim.Score > 100 {
			return fmt.Errorf("điểm không hợp lệ cho %s: %d", dim.Dimension, dim.Score)
		}
		if strings.TrimSpace(dim.Comment) == "" {
			return fmt.Errorf("bình luận cho dimension là bắt buộc: %s", dim.Dimension)
		}
	}
	return nil
}
