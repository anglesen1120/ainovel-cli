package store

import (
	"errors"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestSaveAndLoadRunMeta(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	meta := domain.RunMeta{
		StartedAt: "2026-03-07T10:00:00+08:00",
		Provider:  "openrouter",
		Style:     "fantasy",
		Model:     "gpt-4o",
	}
	if err := store.RunMeta.Save(meta); err != nil {
		t.Fatalf("SaveRunMeta: %v", err)
	}

	loaded, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if loaded.Style != "fantasy" {
		t.Errorf("kiểu không khớp: %s", loaded.Style)
	}
	if loaded.Provider != "openrouter" {
		t.Errorf("provider không khớp: %s", loaded.Provider)
	}
	if loaded.Model != "gpt-4o" {
		t.Errorf("model không khớp: %s", loaded.Model)
	}
}

func TestLoadRunMeta_Empty(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta khi rỗng: %v", err)
	}
	if meta != nil {
		t.Fatalf("mong đợi nil, nhận được %+v", meta)
	}
}

func TestInitRunMeta_PreservesHistory(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Trước tiên tạo RunMeta có ý định chạy
	_ = store.RunMeta.Save(domain.RunMeta{
		StartedAt:    "old",
		Provider:     "openai",
		Style:        "fantasy",
		Model:        "old-model",
		PendingSteer: "Đang chờ xử lý",
	})

	// Init phải giữ lại các sự kiện về ý định chạy như PendingSteer
	_ = store.RunMeta.Init("suspense", "openrouter", "new-model")

	meta, _ := store.RunMeta.Load()
	if meta.Style != "suspense" {
		t.Errorf("style phải được cập nhật, nhận được %s", meta.Style)
	}
	if meta.Provider != "openrouter" {
		t.Errorf("provider phải được cập nhật, nhận được %s", meta.Provider)
	}
	if meta.Model != "new-model" {
		t.Errorf("model phải được cập nhật, nhận được %s", meta.Model)
	}
	if meta.PendingSteer != "Đang chờ xử lý" {
		t.Errorf("pending steer phải được giữ lại, nhận được %s", meta.PendingSteer)
	}
	if meta.AdvanceMode != domain.ChapterAdvanceAuto {
		t.Errorf("advance mode bị thiếu phải khởi tạo thành auto, nhận được %q", meta.AdvanceMode)
	}
}

func TestSetAndClearPendingSteer(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Thiết lập PendingSteer
	if err := store.RunMeta.SetPendingSteer("Đổi nhân vật chính thành nữ"); err != nil {
		t.Fatalf("SetPendingSteer: %v", err)
	}
	meta, _ := store.RunMeta.Load()
	if meta.PendingSteer != "Đổi nhân vật chính thành nữ" {
		t.Errorf("mong đợi pending steer, nhận được %s", meta.PendingSteer)
	}

	// Xóa
	if err := store.RunMeta.ClearPendingSteer(); err != nil {
		t.Fatalf("ClearPendingSteer: %v", err)
	}
	meta, _ = store.RunMeta.Load()
	if meta.PendingSteer != "" {
		t.Errorf("mong đợi pending steer rỗng, nhận được %s", meta.PendingSteer)
	}
}

func TestSetPlanningTier(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("mong đợi run meta tồn tại")
	}
	if meta.PlanningTier != domain.PlanningTierLong {
		t.Fatalf("mong đợi planning tier %q, nhận được %q", domain.PlanningTierLong, meta.PlanningTier)
	}
}

func TestClearPendingSteer_Noop(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	// Gọi trên meta rỗng không báo lỗi
	if err := store.RunMeta.ClearPendingSteer(); err != nil {
		t.Fatalf("ClearPendingSteer khi rỗng: %v", err)
	}
}

func TestAdvanceControlRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	if err := store.RunMeta.Init("fantasy", "openrouter", "m"); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview); err != nil {
		t.Fatalf("SetAdvanceMode: %v", err)
	}
	if err := store.RunMeta.GrantAdvancePermit(3); err != nil {
		t.Fatalf("GrantAdvancePermit: %v", err)
	}
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "Viết lại chương 3"}
	if err := store.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatalf("SetAdvanceHold: %v", err)
	}

	meta, _ := store.RunMeta.Load()
	if meta.AdvanceMode != domain.ChapterAdvanceReview || meta.AdvancePermitChapter != 3 {
		t.Fatalf("advance mode/permit vòng lưu-đọc: %+v", meta)
	}
	if meta.AdvanceHold == nil || *meta.AdvanceHold != hold {
		t.Fatalf("advance hold vòng lưu-đọc: %+v", meta.AdvanceHold)
	}

	if err := store.RunMeta.ClearAdvancePermit(3); err != nil {
		t.Fatalf("ClearAdvancePermit: %v", err)
	}
	if err := store.RunMeta.ClearAdvanceHold(hold); err != nil {
		t.Fatalf("ClearAdvanceHold: %v", err)
	}
	meta, _ = store.RunMeta.Load()
	if meta.AdvancePermitChapter != 0 || meta.AdvanceHold != nil {
		t.Fatalf("advance intent lẽ ra phải được tiêu thụ: %+v", meta)
	}
}

func TestInitRunMeta_PreservesAdvanceIntent(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.RunMeta.Init("fantasy", "openrouter", "m"); err != nil {
		t.Fatal(err)
	}
	_ = store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview)
	_ = store.RunMeta.GrantAdvancePermit(7)
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "nghiệm thu"}
	_ = store.RunMeta.SetAdvanceHold(hold)
	// Đường dẫn khởi động lại tiến trình: Host.New lần nào cũng gọi Init, ý định chạy của người dùng phải còn tồn tại.
	_ = store.RunMeta.Init("fantasy", "openrouter", "m")

	meta, _ := store.RunMeta.Load()
	if meta.AdvanceMode != domain.ChapterAdvanceReview || meta.AdvancePermitChapter != 7 {
		t.Fatalf("advance mode/permit phải tồn tại qua Init, nhận được %+v", meta)
	}
	if meta.AdvanceHold == nil || *meta.AdvanceHold != hold {
		t.Fatalf("advance hold phải tồn tại qua Init, nhận được %+v", meta.AdvanceHold)
	}
}

func TestAdvanceControlRejectsConflictingIntent(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.RunMeta.Init("fantasy", "openrouter", "m"); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.GrantAdvancePermit(1); err == nil {
		t.Fatal("chế độ auto phải từ chối permit")
	}
	if err := store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.GrantAdvancePermit(2); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.GrantAdvancePermit(3); err == nil {
		t.Fatal("permit xung đột phải thất bại")
	}
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "dừng"}
	if err := store.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatal(err)
	}
	if err := store.RunMeta.SetAdvanceHold(domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "một mục khác"}); err == nil {
		t.Fatal("hold xung đột phải thất bại")
	}
	if err := (domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, Reason: "thiếu mục tiêu"}).Validate(); err == nil {
		t.Fatal("chapter hold không có target phải thất bại")
	}
	if err := (domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, TargetChapter: 3, Reason: "mục tiêu sai"}).Validate(); err == nil {
		t.Fatal("hold không phải chapter mà có target phải thất bại")
	}
	if err := store.RunMeta.ClearAdvanceHold(domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "giá trị cũ"}); err == nil {
		t.Fatal("compare-and-clear phải từ chối hold đã thay đổi")
	}
	if err := store.RunMeta.SetAdvanceMode(domain.ChapterAdvanceAuto); err != nil {
		t.Fatal(err)
	}
	meta, _ := store.RunMeta.Load()
	if meta.AdvancePermitChapter != 0 || meta.AdvanceHold == nil {
		t.Fatalf("auto phải xóa permit nhưng giữ lại hold: %+v", meta)
	}
}

func TestInitRunMeta_UnknownAdvanceModeDoesNotWrite(t *testing.T) {
	store := NewStore(t.TempDir())
	original := domain.RunMeta{Style: "old", AdvanceMode: "future"}
	if err := store.RunMeta.Save(original); err != nil {
		t.Fatal(err)
	}
	err := store.RunMeta.Init("new", "openrouter", "m")
	var unsupported *domain.UnsupportedAdvanceModeError
	if !errors.As(err, &unsupported) {
		t.Fatalf("mong đợi UnsupportedAdvanceModeError, nhận được %v", err)
	}
	meta, loadErr := store.RunMeta.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if meta.Style != "old" || meta.AdvanceMode != "future" {
		t.Fatalf("Init thất bại không được ghi lại RunMeta: %+v", meta)
	}
}

// TestRunMetaInit_PreservesPlanStart giai đoạn lập kế hoạch (phán quyết đã ghi xuống đĩa, foundation đầu tiên chưa ghi xuống đĩa)
// Khi sập rồi khởi động lại, RunMeta.Init của Host.New không được xóa PlanStart — đó là
// căn cứ duy nhất để khôi phục danh tính planner (engine.planStartFallback).
func TestRunMetaInit_PreservesPlanStart(t *testing.T) {
	store := NewStore(t.TempDir())
	if err := store.RunMeta.SetStartPrompt("Viết một truyện ngắn trinh thám"); err != nil {
		t.Fatalf("thiết lập start prompt: %v", err)
	}
	rec := domain.PlanStartRecord{RawPrompt: "Viết một truyện ngắn trinh thám", Planner: "architect_short", PlannerTask: "toàn văn nhiệm vụ", DecisionID: "dec-x"}
	if err := store.RunMeta.SetPlanStart(rec); err != nil {
		t.Fatalf("thiết lập plan start: %v", err)
	}
	// Mô phỏng tiến trình khởi động lại: Host.New sẽ Init lần nữa
	if err := store.RunMeta.Init("default", "openrouter", "m"); err != nil {
		t.Fatalf("init: %v", err)
	}
	meta, err := store.RunMeta.Load()
	if err != nil || meta == nil {
		t.Fatalf("load: %v", err)
	}
	if meta.PlanStart == nil || meta.PlanStart.Planner != "architect_short" {
		t.Fatalf("Init phải giữ lại PlanStart, nhận được %+v", meta.PlanStart)
	}
	// StartPrompt cũng là sự kiện xuyên qua khởi động lại: sau khi phán quyết thất bại, nó là căn cứ duy nhất để engine phán quyết bổ sung.
	if meta.StartPrompt != "Viết một truyện ngắn trinh thám" {
		t.Fatalf("Init phải giữ lại StartPrompt, nhận được %q", meta.StartPrompt)
	}
}
