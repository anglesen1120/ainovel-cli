package diag

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestPlanActionsOnlyHighConfSafe(t *testing.T) {
	findings := []Finding{
		{Rule: "PhaseFlowMismatch", Severity: SevCritical, Confidence: ConfHigh, AutoLevel: AutoSafe},
		{Rule: "ChronicLowDimension", Severity: SevWarning, Confidence: ConfMedium, AutoLevel: AutoNone},
		{Rule: "WordCountAnomaly", Severity: SevInfo, Confidence: ConfLow, AutoLevel: AutoNone},
	}
	actions := PlanActions(findings)
	for _, a := range actions {
		if a.SourceRule != "PhaseFlowMismatch" {
			t.Fatalf("hành động không mong đợi từ quy tắc %q; chỉ PhaseFlowMismatch được tạo hành động", a.SourceRule)
		}
	}
	if len(actions) == 0 {
		t.Fatal("cần ít nhất một hành động từ PhaseFlowMismatch")
	}
}

func TestPlanActionsDedup(t *testing.T) {
	findings := []Finding{
		{Rule: "OrphanedSteer", Severity: SevWarning, Confidence: ConfHigh, AutoLevel: AutoSafe},
		{Rule: "OrphanedSteer", Severity: SevWarning, Confidence: ConfHigh, AutoLevel: AutoSafe},
	}
	actions := PlanActions(findings)
	count := 0
	for _, a := range actions {
		if a.SourceRule == "OrphanedSteer" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("cần 1 hành động từ OrphanedSteer (khử trùng lặp), nhận %d", count)
	}
}

func TestPhaseFlowMismatchMeta(t *testing.T) {
	snap := &Snapshot{
		Progress: &domain.Progress{
			Phase: domain.PhaseOutline,
			Flow:  domain.FlowRewriting,
		},
	}
	findings := PhaseFlowMismatch(snap)
	if len(findings) != 1 {
		t.Fatalf("cần 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
		t.Fatalf("cần độ tin cậy cao/an toàn, nhận %s/%s", f.Confidence, f.AutoLevel)
	}
	actions := PlanActions(findings)
	if len(actions) == 0 {
		t.Fatal("cần hành động từ PhaseFlowMismatch")
	}
	hasFollowUp := false
	for _, a := range actions {
		if a.Kind == ActionEnqueueFollowUp {
			hasFollowUp = true
		}
	}
	if !hasFollowUp {
		t.Fatal("cần hành động enqueue_follow_up")
	}
}

func TestInvalidPendingRewritesMeta(t *testing.T) {
	snap := &Snapshot{
		Progress: &domain.Progress{
			Phase:             domain.PhaseWriting,
			Flow:              domain.FlowPolishing,
			CompletedChapters: []int{1, 2, 58},
			PendingRewrites:   []int{65},
		},
	}
	findings := InvalidPendingRewrites(snap)
	if len(findings) != 1 {
		t.Fatalf("cần 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Severity != SevCritical || f.Confidence != ConfHigh || f.AutoLevel != AutoSuggest {
		t.Fatalf("cần mức nghiêm trọng/độ tin cậy cao/gợi ý, nhận %s/%s/%s", f.Severity, f.Confidence, f.AutoLevel)
	}
	if f.Rule != "InvalidPendingRewrites" {
		t.Fatalf("quy tắc không mong đợi: %s", f.Rule)
	}
	if actions := PlanActions(findings); len(actions) != 0 {
		t.Fatalf("các bản viết lại đang chờ không hợp lệ chưa được tự lập kế hoạch hành động, nhận %+v", actions)
	}
}

func TestOutlineExhaustedMeta(t *testing.T) {
	snap := &Snapshot{
		Progress: &domain.Progress{
			Phase:             domain.PhaseWriting,
			TotalChapters:     5,
			CompletedChapters: []int{1, 2, 3, 4, 5},
		},
	}
	findings := OutlineExhausted(snap)
	if len(findings) != 1 {
		t.Fatalf("cần 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
		t.Fatalf("cần độ tin cậy cao/an toàn, nhận %s/%s", f.Confidence, f.AutoLevel)
	}
	actions := PlanActions(findings)
	if len(actions) != 1 || actions[0].Kind != ActionEnqueueFollowUp {
		t.Fatalf("cần 1 hành động enqueue_follow_up, nhận %+v", actions)
	}
}

func TestOrphanedSteerMeta(t *testing.T) {
	snap := &Snapshot{
		RunMeta: &domain.RunMeta{
			PendingSteer: "Hãy tăng nhịp chương này",
		},
		Progress: &domain.Progress{
			Flow: domain.FlowWriting,
		},
	}
	findings := OrphanedSteer(snap)
	if len(findings) != 1 {
		t.Fatalf("cần 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
		t.Fatalf("cần độ tin cậy cao/an toàn, nhận %s/%s", f.Confidence, f.AutoLevel)
	}
	actions := PlanActions(findings)
	if len(actions) != 1 || actions[0].Kind != ActionEnqueueFollowUp {
		t.Fatalf("cần 1 hành động enqueue_follow_up, nhận %+v", actions)
	}
}
