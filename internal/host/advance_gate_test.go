package host

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/flow"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

type gateRecorder struct {
	paused  int
	reasons []string
}

func newAdvanceGateTest(t *testing.T, mode domain.ChapterAdvanceMode) (*storepkg.Store, *ChapterAdvanceGate, *gateRecorder) {
	t.Helper()
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("store init: %v", err)
	}
	if err := st.RunMeta.Init("default", "test", "test"); err != nil {
		t.Fatalf("run meta init: %v", err)
	}
	if err := st.RunMeta.SetAdvanceMode(mode); err != nil {
		t.Fatalf("advance mode: %v", err)
	}
	if err := st.Progress.Init(10); err != nil {
		t.Fatalf("progress init: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	recorder := &gateRecorder{}
	gate := NewChapterAdvanceGate(st, func(reason string) {
		recorder.paused++
		recorder.reasons = append(recorder.reasons, reason)
	}, func(_ string, summary string) {
		recorder.reasons = append(recorder.reasons, summary)
	})
	return st, gate, recorder
}

func TestChapterAdvanceGateReviewRequiresExactPermit(t *testing.T) {
	st, gate, recorder := newAdvanceGateTest(t, domain.ChapterAdvanceReview)
	forward := &flow.Instruction{Agent: "writer", Chapter: 1, Task: "Viết chương 1"}

	allowed, err := gate.Allow(forward)
	if err != nil {
		t.Fatal(err)
	}
	if allowed || recorder.paused != 1 {
		t.Fatalf("Chương mới chưa được cấp quyền bắt buộc phải tạm dừng: allowed=%v paused=%d", allowed, recorder.paused)
	}
	if len(recorder.reasons) == 0 || !strings.Contains(recorder.reasons[len(recorder.reasons)-1], "/next") {
		t.Fatalf("Nội dung tạm dừng phải đưa ra cách cho phép tiếp tục rõ ràng: %v", recorder.reasons)
	}

	if err := st.RunMeta.GrantAdvancePermit(1); err != nil {
		t.Fatal(err)
	}
	allowed, err = gate.Allow(forward)
	if err != nil || !allowed {
		t.Fatalf("Quyền khớp phải được cho phép tiếp tục: allowed=%v err=%v", allowed, err)
	}
	if err := st.RunMeta.ClearAdvancePermit(1); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.GrantAdvancePermit(2); err != nil {
		t.Fatal(err)
	}
	allowed, err = gate.Allow(forward)
	if err == nil || allowed {
		t.Fatalf("Quyền không khớp bắt buộc phải thất bại rõ ràng: allowed=%v err=%v", allowed, err)
	}
}

func TestChapterAdvanceGateDoesNotGateRewriteOrRecovery(t *testing.T) {
	st, gate, _ := newAdvanceGateTest(t, domain.ChapterAdvanceReview)
	if err := st.Progress.MarkChapterComplete(1, 1000, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "làm lại"); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.GrantAdvancePermit(2); err != nil {
		t.Fatal(err)
	}

	allowed, err := gate.Allow(&flow.Instruction{Agent: "writer", Chapter: 1, Task: "Viết lại chương 1"})
	if err != nil || !allowed {
		t.Fatalf("Làm lại không tiêu thụ quyền chương tiến tới: allowed=%v err=%v", allowed, err)
	}
	if gate.HandleBoundary() {
		t.Fatal("Khi tồn tại hàng đợi làm lại, sự đan xen bình thường giữa permit và NextChapter không nên bị báo nhầm là hỏng")
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvancePermitChapter != 2 {
		t.Fatalf("Trong lúc làm lại, quyền phải được giữ nguyên: %+v", meta)
	}

	if err := st.Signals.SavePendingCommit(domain.PendingCommit{Chapter: 2, Stage: domain.CommitStageStarted}); err != nil {
		t.Fatal(err)
	}
	allowed, err = gate.Allow(&flow.Instruction{Agent: "writer", Chapter: 2, Task: "Khôi phục commit chương 2"})
	if err != nil || !allowed {
		t.Fatalf("Khôi phục commit không được bị xem là chương mới: allowed=%v err=%v", allowed, err)
	}
}

func TestChapterAdvanceGateConsumesPermitOnlyAfterStableCommit(t *testing.T) {
	st, gate, recorder := newAdvanceGateTest(t, domain.ChapterAdvanceReview)
	if err := st.RunMeta.GrantAdvancePermit(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 1000, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Signals.SavePendingCommit(domain.PendingCommit{Chapter: 1, Stage: domain.CommitStageProgressMarked}); err != nil {
		t.Fatal(err)
	}
	if gate.HandleBoundary() {
		t.Fatal("Khi commit saga chưa hoàn tất thì không thể tiêu thụ quyền hoặc dừng máy")
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvancePermitChapter != 1 {
		t.Fatalf("Trong thời gian pending commit, quyền phải được giữ lại: %+v", meta)
	}

	if err := st.Signals.ClearPendingCommit(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "commit", "", ""); err != nil {
		t.Fatal(err)
	}
	if gate.HandleBoundary() {
		t.Fatal("Commit ổn định chỉ tiêu thụ quyền; trước lượt điều phối tiếp theo mới vào trạng thái chờ")
	}
	meta, _ = st.RunMeta.Load()
	if meta.AdvancePermitChapter != 0 {
		t.Fatalf("Sau commit ổn định, quyền bắt buộc phải được tiêu thụ: %+v", meta)
	}
	allowed, err := gate.Allow(&flow.Instruction{Agent: "writer", Chapter: 2})
	if err != nil || allowed || recorder.paused != 1 {
		t.Fatalf("Sau khi tiêu thụ, chương tiếp theo bắt buộc phải chờ cấp quyền lại: allowed=%v paused=%d err=%v", allowed, recorder.paused, err)
	}
}

func TestChapterAdvanceGateRejectsCorruptPermitState(t *testing.T) {
	st, gate, recorder := newAdvanceGateTest(t, domain.ChapterAdvanceReview)
	if err := st.RunMeta.GrantAdvancePermit(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 1000, "", ""); err != nil {
		t.Fatal(err)
	}
	if !gate.HandleBoundary() || recorder.paused != 1 {
		t.Fatal("Đã hoàn thành nhưng thiếu commit checkpoint thì bắt buộc phải báo lỗi rõ ràng và tạm dừng")
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvancePermitChapter != 1 {
		t.Fatal("Trong trạng thái hỏng, không được suy đoán để tiêu thụ quyền")
	}
}

func TestChapterAdvanceGateHoldLifecycle(t *testing.T) {
	st, gate, recorder := newAdvanceGateTest(t, domain.ChapterAdvanceAuto)
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "Sửa xong thì để tôi nghiệm thu"}
	if err := st.Progress.MarkChapterComplete(1, 1000, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "làm lại"); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatal(err)
	}
	if gate.HandleBoundary() {
		t.Fatal("Khi làm lại chưa được xả hết thì không thể tạm dừng sớm")
	}
	if err := st.Progress.CompleteRewrite(1); err != nil {
		t.Fatal(err)
	}
	if !gate.HandleBoundary() || recorder.paused != 1 {
		t.Fatal("Sau khi làm lại được xả hết, bắt buộc phải tiêu thụ hold và tạm dừng")
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvanceHold != nil {
		t.Fatalf("Trước khi tạm dừng, hold bắt buộc phải được tiêu thụ nguyên tử: %+v", meta.AdvanceHold)
	}
}

func TestChapterAdvanceGateStopsAfterTargetChapterCommit(t *testing.T) {
	st, gate, recorder := newAdvanceGateTest(t, domain.ChapterAdvanceAuto)
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 2, Reason: "Viết đến chương 2"}
	if err := st.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 1000, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(1), "commit", "", ""); err != nil {
		t.Fatal(err)
	}
	if gate.HandleBoundary() {
		t.Fatal("Khi chương mục tiêu chưa hoàn thành thì không thể tạm dừng")
	}
	if err := st.Progress.MarkChapterComplete(2, 1000, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Checkpoints.Append(domain.ChapterScope(2), "commit", "", ""); err != nil {
		t.Fatal(err)
	}
	if !gate.HandleBoundary() || recorder.paused != 1 {
		t.Fatal("Sau khi chương mục tiêu commit ổn định, bắt buộc phải tạm dừng")
	}
	if len(recorder.reasons) == 0 || !strings.Contains(recorder.reasons[len(recorder.reasons)-1], "chương 2") {
		t.Fatalf("Sự kiện tạm dừng thiếu chương mục tiêu: %v", recorder.reasons)
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvanceHold != nil {
		t.Fatalf("Trước khi tạm dừng ở chương mục tiêu, bắt buộc phải tiêu thụ hold: %+v", meta.AdvanceHold)
	}
}

func TestChapterAdvanceGateTargetHoldWaitsForCommitRecovery(t *testing.T) {
	st, gate, recorder := newAdvanceGateTest(t, domain.ChapterAdvanceAuto)
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 1, Reason: "Viết đến chương 1"}
	if err := st.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 1000, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	if err := st.Signals.SavePendingCommit(domain.PendingCommit{Chapter: 1, Stage: domain.CommitStageProgressMarked}); err != nil {
		t.Fatal(err)
	}
	if gate.HandleBoundary() || recorder.paused != 0 {
		t.Fatal("Khi khôi phục commit chưa hoàn tất thì không thể tiêu thụ hold của chương mục tiêu")
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvanceHold == nil {
		t.Fatal("Trong thời gian khôi phục commit, bắt buộc phải giữ lại hold của chương mục tiêu")
	}
	if err := st.Signals.ClearPendingCommit(); err != nil {
		t.Fatal(err)
	}
	if !gate.HandleBoundary() || recorder.paused != 1 {
		t.Fatal("Khi bản ghi khôi phục commit biến mất nhưng thiếu checkpoint, bắt buộc phải tạm dừng rõ ràng")
	}
	meta, _ = st.RunMeta.Load()
	if meta.AdvanceHold == nil {
		t.Fatal("Trong trạng thái hỏng, không được tiêu thụ hold của chương mục tiêu")
	}
}

func TestChapterAdvanceGateTargetHoldTemporarilyAuthorizesReviewMode(t *testing.T) {
	st, gate, recorder := newAdvanceGateTest(t, domain.ChapterAdvanceReview)
	hold := domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 2, Reason: "Viết đến chương 2"}
	if err := st.RunMeta.SetAdvanceHold(hold); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 2; chapter++ {
		allowed, err := gate.Allow(&flow.Instruction{Agent: "writer", Chapter: chapter})
		if err != nil || !allowed {
			t.Fatalf("Hold của chương mục tiêu nên tạm thời cho phép chương %d tiếp tục: allowed=%v err=%v", chapter, allowed, err)
		}
		if err := st.Progress.MarkChapterComplete(chapter, 1000, "", ""); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Checkpoints.Append(domain.ChapterScope(chapter), "commit", "", ""); err != nil {
			t.Fatal(err)
		}
		stopped := gate.HandleBoundary()
		if chapter < 2 && stopped {
			t.Fatal("Trước khi tới chương mục tiêu thì không thể tạm dừng")
		}
		if chapter == 2 && !stopped {
			t.Fatal("Sau khi tới chương mục tiêu thì bắt buộc phải tạm dừng")
		}
	}
	meta, _ := st.RunMeta.Load()
	if recorder.paused != 1 || meta.AdvanceMode != domain.ChapterAdvanceReview || meta.AdvanceHold != nil {
		t.Fatalf("Sau khi tạm dừng, nên khôi phục chính sách review vốn có: paused=%d meta=%+v", recorder.paused, meta)
	}
}
