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
			t.Fatalf("quy tắc %q tạo ra thao tác ngoài dự kiến; chỉ PhaseFlowMismatch được tạo thao tác", a.SourceRule)
		}
	}
	if len(actions) == 0 {
		t.Fatal("PhaseFlowMismatch phải tạo ít nhất một thao tác")
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
		t.Fatalf("OrphanedSteer phải tạo 1 thao tác (loại trùng), nhận %d", count)
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
		t.Fatalf("phải có 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
		t.Fatalf("phải là high/safe, nhận %s/%s", f.Confidence, f.AutoLevel)
	}
	actions := PlanActions(findings)
	if len(actions) == 0 {
		t.Fatal("PhaseFlowMismatch phải tạo thao tác")
	}
	hasFollowUp := false
	for _, a := range actions {
		if a.Kind == ActionEnqueueFollowUp {
			hasFollowUp = true
		}
	}
	if !hasFollowUp {
		t.Fatal("phải có thao tác enqueue_follow_up")
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
		t.Fatalf("phải có 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Severity != SevCritical || f.Confidence != ConfHigh || f.AutoLevel != AutoSuggest {
		t.Fatalf("phải là critical/high/suggest, nhận %s/%s/%s", f.Severity, f.Confidence, f.AutoLevel)
	}
	if f.Rule != "InvalidPendingRewrites" {
		t.Fatalf("quy tắc ngoài dự kiến: %s", f.Rule)
	}
	if actions := PlanActions(findings); len(actions) != 0 {
		t.Fatalf("pending rewrites không hợp lệ chưa được tự động lập thao tác, nhận %+v", actions)
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
		t.Fatalf("phải có 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
		t.Fatalf("phải là high/safe, nhận %s/%s", f.Confidence, f.AutoLevel)
	}
	actions := PlanActions(findings)
	if len(actions) != 1 || actions[0].Kind != ActionEnqueueFollowUp {
		t.Fatalf("phải có 1 thao tác enqueue_follow_up, nhận %+v", actions)
	}
}

func TestOrphanedSteerMeta(t *testing.T) {
	snap := &Snapshot{
		RunMeta: &domain.RunMeta{
			PendingSteer: "Hãy chỉnh lại tính cách nhân vật chính",
		},
		Progress: &domain.Progress{
			Flow: domain.FlowWriting,
		},
	}
	findings := OrphanedSteer(snap)
	if len(findings) != 1 {
		t.Fatalf("phải có 1 phát hiện, nhận %d", len(findings))
	}
	f := findings[0]
	if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
		t.Fatalf("phải là high/safe, nhận %s/%s", f.Confidence, f.AutoLevel)
	}
	actions := PlanActions(findings)
	if len(actions) != 1 || actions[0].Kind != ActionEnqueueFollowUp {
		t.Fatalf("phải có 1 thao tác enqueue_follow_up, nhận %+v", actions)
	}
}
