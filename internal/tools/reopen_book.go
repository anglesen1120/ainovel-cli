package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ReopenBookTool mở lại sách đã hoàn tất để vào trạng thái làm lại, do Engine gọi tại ranh giới thao tác can thiệp.
// Sau khi hoàn tất sách, completePhaseGate chặn cứng mọi điều phối subagent, nên người dùng không thể làm lại các chương đã viết.
// Công cụ này không phải subagent và có thể gọi trong phase complete: nó chuyển phase về writing và đưa chương mục tiêu vào
// PendingRewrites, đặt flow=rewriting; sau đó Flow Router theo hàng đợi làm lại hiện có để phái writer viết lại từng chương,
// khi hàng đợi chạy xong, commit_chapter tự động kết thúc và hoàn tất lại. Gate / Router / edit / commit đều không cần đổi logic trọng yếu.
type ReopenBookTool struct {
	store *store.Store
}

func NewReopenBookTool(s *store.Store) *ReopenBookTool {
	return &ReopenBookTool{store: s}
}

func (t *ReopenBookTool) Name() string  { return "reopen_book" }
func (t *ReopenBookTool) Label() string { return "Mở lại làm lại" }

func (t *ReopenBookTool) Description() string {
	return "Mở lại toàn bộ sách đã hoàn tất (phase=complete) để vào trạng thái làm lại, dùng khi người dùng yêu cầu viết lại/chỉnh sửa vài chương sau khi sách đã xong." +
		"chapters là danh sách số chương đã hoàn tất cần làm lại; sau khi gọi, các chương này vào hàng đợi viết lại, Host sẽ phái writer viết lại từng chương, và khi sửa xong toàn bộ sẽ tự hoàn tất lại." +
		"Chỉ dùng khi toàn sách đã hoàn tất và người dùng yêu cầu rõ việc sửa chương đã viết; nếu người dùng muốn thêm tình tiết/mở rộng dung lượng thì không phải làm lại, đừng dùng công cụ này."
}

// Công cụ ghi, cấm chạy đồng thời.
func (t *ReopenBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *ReopenBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *ReopenBookTool) ActivityDescription(_ json.RawMessage) string {
	return "Mở lại toàn sách để làm lại"
}

func (t *ReopenBookTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapters", schema.Array("danh sách số chương đã hoàn tất cần làm lại (ít nhất một chương)", schema.Int(""))).Required(),
		schema.Property("reason", schema.String("lý do làm lại (tùy chọn, ví dụ \"dọn ký tự đặc biệt\")")),
	)
}

func (t *ReopenBookTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapters []int  `json:"chapters"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if len(a.Chapters) == 0 {
		return nil, fmt.Errorf("chapters không được để trống, cần chỉ rõ chương cần làm lại: %w", errs.ErrToolArgs)
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return nil, fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
	}
	// Chỉ được làm lại chương đã viết; số chương không nằm trong tập đã hoàn tất là viết tiếp/vượt phạm vi, nên từ chối rõ và hướng người dùng sang điều chỉnh dung lượng.
	var invalid []int
	for _, ch := range a.Chapters {
		if !slices.Contains(progress.CompletedChapters, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("Chương %v chưa viết xong; reopen chỉ có thể làm lại chương đã hoàn tất (thêm/mở rộng tình tiết hãy đi theo luồng điều chỉnh dung lượng): %w", invalid, errs.ErrToolPrecondition)
	}

	// Điều kiện phase được store.Reopen kiểm tra dự phòng (chỉ phase complete mới gọi được).
	if err := t.store.Progress.Reopen(a.Chapters, a.Reason); err != nil {
		return nil, fmt.Errorf("reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint: đối xứng với complete_book (GlobalScope + meta/progress.json).
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "reopen", "meta/progress.json"); err != nil {
		return nil, fmt.Errorf("checkpoint reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(map[string]any{
		"reopened":         true,
		"phase":            string(domain.PhaseWriting),
		"pending_rewrites": a.Chapters,
		"next_step":        "Đã mở lại và đưa chương mục tiêu vào hàng đợi. Hãy chờ chỉ thị Host phái writer làm lại từng chương; sau khi sửa xong toàn bộ sẽ tự hoàn tất lại.",
	})
}
