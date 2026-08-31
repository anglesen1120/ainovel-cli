package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/agentcore/schema"
	agentcoretools "github.com/voocel/agentcore/tools"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// EditChapterTool thực hiện thay thế chuỗi cục bộ trên bản nháp chương, phù hợp cho giai đoạn tinh chỉnh.
// So với việc viết lại toàn bộ chương bằng draft_chapter, công cụ này tiết kiệm token hơn 10 lần trở lên.
//
// Cam kết ghi đĩa: chỉ sửa drafts/{ch:02d}.draft.md, cấm sửa trực tiếp chapters/ (bản cuối chỉ do commit_chapter đảm nhiệm).
// Ngữ nghĩa Seed: drafts chưa tồn tại nhưng chapters có → tự sao chép chapters sang drafts làm điểm khởi đầu.
// Kiểm tra quyền sở hữu: chỉ cho phép sửa các chương đã hoàn thành và nằm trong hàng đợi PendingRewrites.
//
// Công cụ này là lớp bọc mỏng của agentcore.EditTool; toàn bộ logic tìm-thay thế
// (khớp dự phòng nhiều tầng, xuất diff, giữ nguyên ký tự xuống dòng cuối/BOM)
// đều dùng lại triển khai từ upstream.
type EditChapterTool struct {
	store *store.Store
	edit  *agentcoretools.EditTool
}

func NewEditChapterTool(s *store.Store) *EditChapterTool {
	return &EditChapterTool{
		store: s,
		edit:  agentcoretools.NewEdit(s.Dir(), nil),
	}
}

func (t *EditChapterTool) Name() string  { return "edit_chapter" }
func (t *EditChapterTool) Label() string { return "Chỉnh sửa chương" }

// ReadOnly khai báo rõ đây là công cụ ghi dữ liệu (kết hợp với ConcurrencySafeTool để tránh bị lên lịch song song).
func (t *EditChapterTool) ReadOnly(_ json.RawMessage) bool { return false }

// ConcurrencySafe cấm chạy song song một cách rõ ràng: nhiều lần edit_chapter trên cùng chương chạy đồng thời
// sẽ gây tranh chấp đọc-sửa-ghi, và ngay cả các chương khác nhau chạy song song cũng có thể làm lệch thứ tự checkpoint.
// Chạy tuần tự là an toàn nhất.
func (t *EditChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

// ActivityDescription dùng để hiển thị mô tả hoạt động hiện tại của công cụ trong UI/log.
func (t *EditChapterTool) ActivityDescription(_ json.RawMessage) string {
	return "Chỉnh sửa bản nháp chương"
}

func (t *EditChapterTool) Description() string {
	return "Chỉ thực hiện thay thế chuỗi cục bộ trên bản nháp của các chương đã hoàn thành và đã vào hàng đợi PendingRewrites (ưu tiên cho giai đoạn tinh chỉnh, tiết kiệm token hơn so với viết lại toàn bộ bằng draft_chapter)." +
		"Không dùng công cụ này cho bản nháp đầu tiên của chương mới; nếu bản nháp đầu tiên có vấn đề nghiêm trọng, hãy gọi draft_chapter(mode=\"write\") để ghi đè toàn bộ." +
		"Tìm old_string và thay bằng new_string, yêu cầu khớp chính xác và duy nhất (nếu có nhiều vị trí khớp thì phải dùng replace_all=true)." +
		"old_string phải được sao chép nguyên văn từ kết quả lần read_chapter(source=\"draft\") gần nhất; không được tự nhớ rồi tái tạo lại văn bản gốc;" +
		"lưu ý giá trị trả về là chuỗi JSON, \\n phải được khôi phục thành xuống dòng thật. Sau khi draft_chapter đã viết lại bản nháp, bắt buộc phải read_chapter lại rồi mới được chỉnh sửa." +
		"Nếu khớp thất bại, lỗi sẽ kèm theo đoạn gần đúng nhất trong bản nháp; hãy sao chép nguyên văn từ đoạn gợi ý rồi thử lại." +
		"Ghi vào drafts/{ch}.draft.md; khi drafts chưa tồn tại thì tự seed từ chapters." +
		"Khi chương đã hoàn thành nhưng không có trong hàng đợi PendingRewrites thì sẽ bị từ chối thực thi. Mỗi lần gọi chỉ sửa một chỗ; nếu cần sửa nhiều chỗ, hãy gọi nhiều lần."
}

func (t *EditChapterTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("old_string", schema.String("Đoạn gốc chính xác cần thay thế; nếu nhiều dòng thì phải bao gồm ký tự xuống dòng; khi không bật replace_all thì phải chỉ xuất hiện duy nhất trong bản nháp")).Required(),
		schema.Property("new_string", schema.String("Văn bản mới thay thế vào")).Required(),
		schema.Property("replace_all", schema.Bool("Thay thế tất cả các vị trí khớp (mặc định false)")),
	)
}

func (t *EditChapterTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter    int    `json:"chapter"`
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	if a.OldString == "" {
		return nil, fmt.Errorf("old_string không được để trống: %w", errs.ErrToolArgs)
	}
	if a.OldString == a.NewString {
		return nil, fmt.Errorf("old_string và new_string giống nhau, không cần sửa: %w", errs.ErrToolArgs)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		return nil, err
	}

	// Kiểm tra quyền sở hữu: thực thi máy móc giao thức của writer. Bản nháp đầu tiên của chương mới
	// chỉ được ghi đè toàn bộ, không được dựa vào việc mô hình tự tuân thủ lời nhắc rồi mới lộ ra
	// đường đi chỉnh sửa chính xác nhưng mong manh dưới dạng một lệnh có thể thực thi.
	completed, err := t.store.Progress.IsChapterCompleted(a.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if !completed {
		return nil, fmt.Errorf("chương %d chưa hoàn thành, bản nháp đầu tiên không được dùng edit_chapter; nếu có lỗi nghiêm trọng hãy gọi draft_chapter(mode=\"write\", chapter=%d) để ghi đè toàn bộ: %w", a.Chapter, a.Chapter, errs.ErrToolPrecondition)
	}
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil || !slices.Contains(progress.PendingRewrites, a.Chapter) {
		return nil, fmt.Errorf("chương %d đã hoàn thành và không có trong hàng đợi PendingRewrites, không thể chỉnh sửa; nếu cần thay đổi thì trước hết editor phải đánh giá để kích hoạt viết lại/tinh chỉnh: %w", a.Chapter, errs.ErrToolPrecondition)
	}
	if err := EnsureChapterExpanded(t.store, a.Chapter); err != nil {
		return nil, err
	}

	// Seed: nếu drafts chưa tồn tại thì sao chép từ chapters để làm điểm khởi đầu
	if err := t.ensureDraft(a.Chapter); err != nil {
		return nil, err
	}

	// Ủy quyền cho agentcore.EditTool thực hiện tìm-thay thế
	subArgs, _ := json.Marshal(map[string]any{
		"path":        fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"file_path":   fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		"old_text":    a.OldString,
		"old_string":  a.OldString,
		"new_text":    a.NewString,
		"new_string":  a.NewString,
		"replace_all": a.ReplaceAll,
	})
	result, err := t.edit.Execute(ctx, subArgs)
	if err != nil {
		return nil, fmt.Errorf("apply edit: %w: %w", errs.ErrToolPrecondition, err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(a.Chapter), "edit",
		fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint edit: %w: %w", errs.ErrStoreWrite, err)
	}

	// Gợi ý bổ sung: giúp writer biết bước tiếp theo để tránh bỏ sót check_consistency / commit_chapter
	var passthrough map[string]any
	if err := json.Unmarshal(result, &passthrough); err != nil {
		return result, nil
	}
	passthrough["chapter"] = a.Chapter
	passthrough["next_step"] = "edit đã được ghi xuống đĩa. Nếu vẫn còn lỗi nặng có thể gọi edit_chapter lần nữa; nếu không thì chạy check_consistency rồi commit_chapter"
	return json.Marshal(passthrough)
}

// ensureDraft bảo đảm drafts/{ch}.draft.md tồn tại:
//   - Đã có bản nháp → trả về ngay
//   - Chưa có bản nháp nhưng đã có bản cuối → sao chép bản cuối vào drafts làm điểm bắt đầu chỉnh sửa
//     (thường gặp trong giai đoạn tinh chỉnh)
//   - Không có cả hai → báo lỗi, yêu cầu trước hết dùng draft_chapter để tạo bản nháp đầu tiên
func (t *EditChapterTool) ensureDraft(chapter int) error {
	draft, err := t.store.Drafts.LoadDraft(chapter)
	if err != nil {
		return fmt.Errorf("load draft: %w: %w", errs.ErrStoreRead, err)
	}
	if draft != "" {
		return nil
	}
	text, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return fmt.Errorf("load chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if text == "" {
		return fmt.Errorf("chương %d không có bản nháp cũng không có bản cuối, hãy gọi draft_chapter(mode=write, chapter=%d) trước để tạo bản nháp đầu tiên: %w", chapter, chapter, errs.ErrToolPrecondition)
	}
	if err := t.store.Drafts.SaveDraft(chapter, text); err != nil {
		return fmt.Errorf("seed draft from chapter: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}
