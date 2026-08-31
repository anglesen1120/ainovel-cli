package diag

import (
	"fmt"
	"sort"

	"github.com/voocel/ainovel-cli/internal/store"
)

// ── Ngưỡng chẩn đoán ─────────────────────────────────────────────

const (
	ThresholdDimScoreLow      = 70  // ChronicLowDimension: cảnh báo khi điểm trung bình của chiều dưới mức này
	ThresholdContractMissRate = 0.3 // ContractMissPattern: trần tỷ lệ chưa đạt hợp đồng
	ThresholdRewriteRate      = 0.5 // ExcessiveRewrites: trần tỷ lệ viết lại
	ThresholdWordShortRatio   = 0.4 // WordCountAnomaly: xem là bất thường khi số chữ thấp hơn tỷ lệ này so với trung bình
	ThresholdWordLongRatio    = 2.5 // WordCountAnomaly: xem là bất thường khi số chữ cao hơn tỷ lệ này so với trung bình
	ThresholdHookWeakScore    = 75  // HookWeakChain: hook dưới điểm này bị xem là yếu
	ThresholdHookWeakChain    = 3   // HookWeakChain: ngưỡng số chương yếu liên tiếp
	ThresholdPayoffMissRate   = 0.4 // PayoffMissPattern: trần tỷ lệ payoff chưa đượcđược thực hiện
	ThresholdCompassDrift     = 15  // CompassDrift: số chương tối đa không cập nhật compass
	ThresholdTimelineGapRate  = 0.3 // TimelineGaps: ngưỡng cho phép thiếu hụt
	ThresholdForeshadowMin    = 8   // StaleForeshadow: số chương tối thiểu để xétgợi ý đình trệ
)

// allRules được sắp theo flow → quality → planning → context.
var allRules = []RuleFunc{
	// Flow
	InvalidPendingRewrites,
	RewritePendingPressure,
	OrphanedSteer,
	PhaseFlowMismatch,
	ChapterGaps,
	// Quality
	ChronicLowDimension,
	ContractMissPattern,
	HookWeakChain,
	PayoffMissPattern,
	ExcessiveRewrites,
	WordCountAnomaly,
	// Planning
	StaleForeshadow,
	CompassDrift,
	OutlineExhausted,
	MissingSummaries,
	// Context
	GhostCharacter,
	TimelineGaps,
	RelationshipStagnation,
}

// Analyze là điểm vào duy nhất của hệ thống chẩn đoán.
func Analyze(s *store.Store) Report {
	snap := Load(s)

	var findings []Finding
	for _, e := range snap.LoadErrors {
		findings = append(findings, Finding{
			Rule:       "LoadError",
			Category:   CatFlow,
			Severity:   SevWarning,
			Confidence: ConfHigh,
			AutoLevel:  AutoNone,
			Target:     "runtime.flow",
			Title:      fmt.Sprintf("tải công cụ thất bại: %s", e),
			Suggestion: "Tệp có thể bị hỏng hoặc thiếu quyền; kết quả của các quy tắc chẩn đoán liên quan có thể chưa đầy đủ.",
		})
	}
	for _, rule := range allRules {
		findings = append(findings, rule(&snap)...)
	}
	sortFindings(findings)

	return Report{
		Stats:    buildStats(&snap),
		Findings: findings,
		Actions:  PlanActions(findings),
	}
}

func buildStats(snap *Snapshot) Stats {
	st := Stats{}
	if snap.Progress == nil {
		return st
	}
	p := snap.Progress
	st.CompletedChapters = len(p.CompletedChapters)
	st.TotalChapters = p.TotalChapters
	st.TotalWords = p.TotalWordCount
	st.Phase = string(p.Phase)
	st.Flow = string(p.Flow)

	if st.CompletedChapters > 0 {
		st.AvgWordsPerCh = st.TotalWords / st.CompletedChapters
	}

	if snap.RunMeta != nil {
		st.PlanningTier = string(snap.RunMeta.PlanningTier)
	}

	// Thống kê thẩm định
	st.ReviewCount = len(snap.Reviews)
	var totalScore float64
	var dimCount int
	for _, r := range snap.Reviews {
		if r.Verdict == "rewrite" {
			st.RewriteCount++
		}
		for _, d := range r.Dimensions {
			totalScore += float64(d.Score)
			dimCount++
		}
	}
	if dimCount > 0 {
		st.AvgReviewScore = totalScore / float64(dimCount)
	}

	// Thống kêgợi ý
	latest := snap.LatestCompleted()
	for _, f := range snap.Foreshadow {
		if f.Status == "planted" || f.Status == "advanced" {
			st.ForeshadowOpen++
			if f.Status == "planted" && latest-f.PlantedAt > staleForeshadowThreshold(st.CompletedChapters) {
				st.ForeshadowStale++
			}
		}
	}
	return st
}

// sortFindings sắp xếp theo mức độ nghiêm trọng: critical > warning > info.
func sortFindings(findings []Finding) {
	order := map[Severity]int{SevCritical: 0, SevWarning: 1, SevInfo: 2}
	sort.SliceStable(findings, func(i, j int) bool {
		return order[findings[i].Severity] < order[findings[j].Severity]
	})
}

// staleForeshadowThreshold tính ngưỡng đình trệgợi ý theo tổng số chương.
func staleForeshadowThreshold(completedChapters int) int {
	t := completedChapters / 3
	if t < ThresholdForeshadowMin {
		return ThresholdForeshadowMin
	}
	return t
}
