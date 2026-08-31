package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestSetFlow(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)

	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.Flow != domain.FlowRewriting {
		t.Errorf("mong đợi FlowRewriting, nhưng nhận được %s", p.Flow)
	}
}

func TestSetFlowRejectsInvalidTransition(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)

	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow ở trạng thái viết lại: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowReviewing); err == nil {
		t.Fatal("mong đợi chuyển đổi flow không hợp lệ bị từ chối")
	}
}

func TestUpdatePhaseRejectsRegression(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)

	if err := store.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		t.Fatalf("UpdatePhase dàn ý: %v", err)
	}
	if err := store.Progress.UpdatePhase(domain.PhasePremise); err == nil {
		t.Fatal("mong đợi lùi phase bị từ chối")
	}
}

func TestAdvancePhaseKeepsLaterPhase(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)

	if err := store.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		t.Fatalf("UpdatePhase dàn ý: %v", err)
	}
	if err := store.Progress.AdvancePhase(domain.PhasePremise); err != nil {
		t.Fatalf("AdvancePhase tiền đề: %v", err)
	}
	p, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if p.Phase != domain.PhaseOutline {
		t.Fatalf("phase = %s, muốn là outline", p.Phase)
	}
}

func TestStartChapter(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)

	if err := store.Progress.StartChapter(1); err == nil {
		t.Fatal("mong đợi StartChapter ngoài phase viết thất bại")
	}
	if err := store.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase viết: %v", err)
	}
	if err := store.Progress.StartChapter(1); err != nil {
		t.Fatalf("StartChapter: %v", err)
	}

	p, _ := store.Progress.Load()
	if p.Phase != domain.PhaseWriting {
		t.Fatalf("mong đợi phase writing, nhưng nhận được %s", p.Phase)
	}
	if p.Flow != domain.FlowWriting {
		t.Fatalf("mong đợi flow writing, nhưng nhận được %s", p.Flow)
	}
	if p.CurrentChapter != 1 {
		t.Fatalf("mong đợi chương hiện tại là 1, nhưng nhận được %d", p.CurrentChapter)
	}
	if p.InProgressChapter != 1 {
		t.Fatalf("mong đợi chương đang tiến hành là 1, nhưng nhận được %d", p.InProgressChapter)
	}
}

func TestIsChapterCompleted(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)
	_ = store.Progress.UpdatePhase(domain.PhaseWriting)

	if completed, err := store.Progress.IsChapterCompleted(1); err != nil || completed {
		t.Fatal("chương 1 ban đầu chưa nên hoàn thành")
	}

	_ = store.Progress.StartChapter(1)
	_ = store.Progress.MarkChapterComplete(1, 5000, "", "")

	if completed, err := store.Progress.IsChapterCompleted(1); err != nil || !completed {
		t.Fatal("chương 1 phải được hoàn thành sau MarkChapterComplete")
	}
	if completed, err := store.Progress.IsChapterCompleted(2); err != nil || completed {
		t.Fatal("chương 2 không nên được hoàn thành")
	}
}

func TestSetPendingRewrites(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(7, 3000, "", "")

	chapters := []int{3, 5, 7}
	if err := store.Progress.SetPendingRewrites(chapters, "động cơ nhân vật không liền mạch"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}

	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 3 {
		t.Fatalf("mong đợi 3 mục đang chờ, nhưng nhận được %d", len(p.PendingRewrites))
	}
	if p.RewriteReason != "động cơ nhân vật không liền mạch" {
		t.Errorf("lý do không khớp: %s", p.RewriteReason)
	}
}

func TestSetPendingRewritesRejectsUnfinishedChapters(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")

	if err := store.Progress.SetPendingRewrites([]int{3, 5}, "kiểm thử"); err == nil {
		t.Fatal("mong đợi chương chưa hoàn tất bị từ chối")
	}

	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("pending_rewrites phải vẫn trống, nhưng nhận được %v", p.PendingRewrites)
	}
}

func TestValidateChapterWorkRejectsCorruptPendingRewriteQueue(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(80)
	for ch := 1; ch <= 58; ch++ {
		_ = store.Progress.MarkChapterComplete(ch, 3000, "", "")
	}

	p, _ := store.Progress.Load()
	p.Flow = domain.FlowPolishing
	p.PendingRewrites = []int{65}
	if err := store.Progress.Save(p); err != nil {
		t.Fatalf("Lưu tiến trình bị hỏng: %v", err)
	}

	if err := store.Progress.ValidateChapterWork(65); err == nil {
		t.Fatal("mong đợi pending_rewrites bị hỏng bị từ chối")
	}
}

func TestCompleteRewrite(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(7, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{3, 5, 7}, "kiểm thử viết lại")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	// Hoàn thành chương 5
	if err := store.Progress.CompleteRewrite(5); err != nil {
		t.Fatalf("CompleteRewrite(5): %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 2 {
		t.Fatalf("mong đợi còn 2 mục đang chờ sau khi xóa 5, nhưng nhận được %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowRewriting {
		t.Errorf("flow vẫn nên là rewriting, nhưng nhận được %s", p.Flow)
	}

	// Hoàn thành chương 3
	_ = store.Progress.CompleteRewrite(3)
	p, _ = store.Progress.Load()
	if len(p.PendingRewrites) != 1 {
		t.Fatalf("mong đợi còn 1 mục đang chờ, nhưng nhận được %d", len(p.PendingRewrites))
	}

	// Hoàn thành chương cuối → tự động đặt lại Flow
	_ = store.Progress.CompleteRewrite(7)
	p, _ = store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("mong đợi còn 0 mục đang chờ, nhưng nhận được %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("flow nên được đặt lại thành writing, nhưng nhận được %s", p.Flow)
	}
	if p.RewriteReason != "" {
		t.Errorf("lý do nên được xóa, nhưng nhận được %s", p.RewriteReason)
	}
}

func TestApplyReviewOutcomePreservesExistingRewriteQueue(t *testing.T) {
	s := NewStore(t.TempDir())
	_ = s.Progress.Init(3)
	for _, ch := range []int{1, 2} {
		_ = s.Progress.MarkChapterComplete(ch, 3000, "", "")
	}
	_ = s.Progress.SetPendingRewrites([]int{1, 2}, "đã có phần làm lại")
	_ = s.Progress.SetFlow(domain.FlowRewriting)

	p, err := s.Progress.ApplyReviewOutcome(domain.FlowWriting, nil, "lần duyệt này đạt yêu cầu")
	if err != nil {
		t.Fatal(err)
	}
	if p.Flow != domain.FlowRewriting || len(p.PendingRewrites) != 2 {
		t.Fatalf("lần duyệt mới không được bỏ quên hàng đợi làm lại hiện có: flow=%s queue=%v", p.Flow, p.PendingRewrites)
	}
}

func TestCompleteRewrite_NotInQueue(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(5, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{3, 5}, "kiểm thử")

	// Hoàn thành một chương không có trong hàng đợi thì không nên báo lỗi
	if err := store.Progress.CompleteRewrite(99); err != nil {
		t.Fatalf("CompleteRewrite(99): %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 2 {
		t.Errorf("hàng đợi không nên thay đổi, nhưng nhận được %d", len(p.PendingRewrites))
	}
}

func TestClearPendingRewrites(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	_ = store.Progress.Init(10)
	_ = store.Progress.MarkChapterComplete(1, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(2, 3000, "", "")
	_ = store.Progress.MarkChapterComplete(3, 3000, "", "")
	_ = store.Progress.SetPendingRewrites([]int{1, 2, 3}, "kiểm thử")
	_ = store.Progress.SetFlow(domain.FlowRewriting)

	if err := store.Progress.ClearPendingRewrites(); err != nil {
		t.Fatalf("ClearPendingRewrites: %v", err)
	}
	p, _ := store.Progress.Load()
	if len(p.PendingRewrites) != 0 {
		t.Errorf("mong đợi trống, nhưng nhận được %d", len(p.PendingRewrites))
	}
	if p.Flow != domain.FlowWriting {
		t.Errorf("flow nên là writing, nhưng nhận được %s", p.Flow)
	}
}
