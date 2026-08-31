package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

func enterEditWritingPhase(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("Cập nhật giai đoạn: %v", err)
	}
}

func queueCompletedChapterForEdit(t *testing.T, s *store.Store, chapter int, wordCount int) {
	t.Helper()
	if err := s.Progress.MarkChapterComplete(chapter, wordCount, "mystery", "quest"); err != nil {
		t.Fatalf("Đánh dấu chương hoàn tất: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{chapter}, "kiểm tra mài giũa"); err != nil {
		t.Fatalf("Đặt danh sách chờ viết lại: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}
}

// TestEditChapterAppliesEdit Đường đi bình thường: drafts đã có nội dung, thay thế khớp duy nhất thành công.
func TestEditChapterAppliesEdit(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)
	if err := s.Drafts.SaveDraft(2, "Anh siết chặt nắm tay, các khớp ngón tay tái nhợt."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	queueCompletedChapterForEdit(t, s, 2, len([]rune("Anh siết chặt nắm tay, các khớp ngón tay tái nhợt.")))

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "các khớp ngón tay tái nhợt",
		"new_string": "các khớp ngón tay ánh lên sắc xanh trắng",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi: %v", err)
	}

	got, err := s.Drafts.LoadDraft(2)
	if err != nil {
		t.Fatalf("Tải bản nháp: %v", err)
	}
	if !strings.Contains(got, "các khớp ngón tay ánh lên sắc xanh trắng") {
		t.Fatalf("mong đợi bản nháp chứa văn bản mới, nhận được %q", got)
	}
	if strings.Contains(got, "các khớp ngón tay tái nhợt") {
		t.Fatalf("văn bản cũ phải được thay thế, nhận được %q", got)
	}
}

func TestEditChapterRejectsIncompleteChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)
	original := "Bản nháp đầu của chương mới phải được ghi đè nguyên chương."
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "phải",
		"new_string": "nên",
	})
	_, err := NewEditChapterTool(s).Execute(context.Background(), args)
	if err == nil || !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("chương chưa hoàn tất phải bị từ chối rõ ràng, nhận được %v", err)
	}
	if !strings.Contains(err.Error(), `draft_chapter(mode="write"`) {
		t.Fatalf("lỗi phải chỉ tới luồng tạo bản nháp toàn chương, nhận được %v", err)
	}
	got, loadErr := s.Drafts.LoadDraft(2)
	if loadErr != nil {
		t.Fatalf("Tải bản nháp: %v", loadErr)
	}
	if got != original {
		t.Fatalf("sau khi từ chối không được sửa bản nháp, nhận được %q", got)
	}
}

// TestEditChapterSeedsFromFinalChapter drafts không tồn tại nhưng chapters có → tự động gieo từ chapters.
func TestEditChapterSeedsFromFinalChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)

	// Mô phỏng chương 3 đã được nộp và đã vào hàng đợi mài giũa
	original := "Gió lùa qua khe cửa sổ, mang theo mùi đất ẩm."
	if err := s.Drafts.SaveFinalChapter(3, original); err != nil {
		t.Fatalf("Lưu chương cuối: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("Đánh dấu chương hoàn tất: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{3}, "kiểm tra mài giũa"); err != nil {
		t.Fatalf("Đặt danh sách chờ viết lại: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    3,
		"old_string": "mùi đất ẩm",
		"new_string": "mùi đất lẫn rỉ sắt",
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi: %v", err)
	}

	// drafts phải được gieo từ bản cuối và chứa văn bản mới
	draft, err := s.Drafts.LoadDraft(3)
	if err != nil {
		t.Fatalf("Tải bản nháp: %v", err)
	}
	if !strings.Contains(draft, "mùi đất lẫn rỉ sắt") {
		t.Fatalf("mong đợi bản nháp đã được gieo và chỉnh sửa, nhận được %q", draft)
	}

	// chapters giữ nguyên (edit_chapter không đụng tới bản cuối)
	final, err := s.Drafts.LoadChapterText(3)
	if err != nil {
		t.Fatalf("Tải văn bản chương: %v", err)
	}
	if final != original {
		t.Fatalf("chương cuối phải giữ nguyên, nhận được %q", final)
	}
}

// TestEditChapterRejectsCompletedWithoutQueue Đã hoàn tất nhưng không nằm trong hàng đợi viết lại → từ chối.
func TestEditChapterRejectsCompletedWithoutQueue(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)
	original := "Nội dung gốc của chương hai."
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, original); err != nil {
		t.Fatalf("Lưu chương cuối: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("Đánh dấu chương hoàn tất: %v", err)
	}

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "nội dung gốc",
		"new_string": "nội dung bị sửa bậy",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("mong đợi từ chối đối với chương đã hoàn tất nhưng không nằm trong PendingRewrites")
	}
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("mong đợi ErrToolPrecondition, nhận được %v", err)
	}
}

// TestEditChapterRejectsAmbiguousMatch Nhiều vị trí khớp và không bật replace_all → báo lỗi.
func TestEditChapterRejectsAmbiguousMatch(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)
	if err := s.Drafts.SaveDraft(2, "Anh ấy cười. Cô ấy cũng cười."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	queueCompletedChapterForEdit(t, s, 2, len([]rune("Anh ấy cười. Cô ấy cũng cười.")))

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "cười",
		"new_string": "im lặng",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("mong đợi từ chối vì khớp mơ hồ")
	}
}

// TestEditChapterReplaceAll Khi replace_all=true, mọi vị trí khớp đều được thay thế.
func TestEditChapterReplaceAll(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)
	if err := s.Drafts.SaveDraft(2, "Anh ấy cười. Cô ấy cũng cười."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	queueCompletedChapterForEdit(t, s, 2, len([]rune("Anh ấy cười. Cô ấy cũng cười.")))

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":     2,
		"old_string":  "cười",
		"new_string":  "im lặng",
		"replace_all": true,
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi: %v", err)
	}

	got, _ := s.Drafts.LoadDraft(2)
	if strings.Contains(got, "cười") {
		t.Fatalf("mọi lần xuất hiện phải được thay thế, nhận được %q", got)
	}
	if strings.Count(got, "im lặng") != 2 {
		t.Fatalf("mong đợi 2 lần thay thế, nhận được %q", got)
	}
}

// TestEditChapterRejectsEmptyOldString old_string rỗng → tham số không hợp lệ.
func TestEditChapterRejectsEmptyOldString(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "",
		"new_string": "xxx",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("mong đợi từ chối vì old_string rỗng")
	}
	if !errors.Is(err, errs.ErrToolArgs) {
		t.Fatalf("mong đợi ErrToolArgs, nhận được %v", err)
	}
}

// TestEditChapterRejectsNoDraftNoFinal drafts và chapters đều không tồn tại → báo lỗi, gợi ý dùng draft_chapter trước.
func TestEditChapterRejectsNoDraftNoFinal(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)
	queueCompletedChapterForEdit(t, s, 5, 0)

	tool := NewEditChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    5,
		"old_string": "bất kỳ",
		"new_string": "thay thế",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("mong đợi từ chối khi không tồn tại cả bản nháp lẫn chương")
	}
	if !errors.Is(err, errs.ErrToolPrecondition) {
		t.Fatalf("mong đợi ErrToolPrecondition, nhận được %v", err)
	}
}

// TestEditChapterWorksWithCommitValidation Toàn bộ luồng: edit_chapter → commit_chapter rút hàng đợi thành công.
// Xác nhận công cụ mới phối hợp tốt với kiểm tra cứng của commit_chapter rằng drafts≠chapters.
func TestEditChapterWorksWithCommitValidation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	enterEditWritingPhase(t, s)

	original := "Gió lùa qua khe cửa sổ, mang theo mùi đất ẩm."
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, original); err != nil {
		t.Fatalf("Lưu chương cuối: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("Đánh dấu chương hoàn tất: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "mài giũa"); err != nil {
		t.Fatalf("Đặt danh sách chờ viết lại: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}

	editTool := NewEditChapterTool(s)
	editArgs, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"old_string": "mùi đất ẩm",
		"new_string": "mùi đất lẫn rỉ sắt",
	})
	if _, err := editTool.Execute(context.Background(), editArgs); err != nil {
		t.Fatalf("edit_chapter: %v", err)
	}

	commitTool := newTestCommitChapterTool(s)
	commitArgs, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"title":      "Chương hai",
		"summary":    "tóm tắt sau khi mài giũa",
		"characters": []string{"nhân vật chính"},
		"key_events": []string{"hoàn tất mài giũa"},
	})
	if _, err := commitTool.Execute(context.Background(), commitArgs); err != nil {
		t.Fatalf("commit_chapter sau khi chỉnh sửa: %v", err)
	}

	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("Tải tiến độ: %v", err)
	}
	if len(progress.PendingRewrites) != 0 {
		t.Fatalf("mong đợi hàng đợi đã được rút hết, nhận được %v", progress.PendingRewrites)
	}
}

func TestEditChapterRejectsPlanningPhaseBeforeMutation(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	original := "Bản nháp ở giai đoạn lập kế hoạch không được phép bị sửa."
	if err := s.Drafts.SaveDraft(1, original); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	args, err := json.Marshal(map[string]any{
		"chapter": 1, "old_string": "không được", "new_string": "đã được",
	})
	if err != nil {
		t.Fatalf("Chuyển đổi JSON: %v", err)
	}
	if _, err := NewEditChapterTool(s).Execute(context.Background(), args); err == nil {
		t.Fatal("chỉnh sửa trong giai đoạn lập kế hoạch phải bị từ chối")
	}
	got, err := s.Drafts.LoadDraft(1)
	if err != nil {
		t.Fatalf("Tải bản nháp: %v", err)
	}
	if got != original {
		t.Fatalf("chỉnh sửa trong giai đoạn lập kế hoạch đã làm thay đổi bản nháp: %q", got)
	}
}
