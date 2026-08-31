package flow

// kiểm thử vét cạn không gian trạng thái Route.
//
// expectedInstruction là bản phản chiếu độc lập của bảng quyết định (đặc tả có thể chạy, tương ứng luật thép hai trong architecture.md
// về độ ưu tiên 11 nhánh), cố ý không dùng lại bất kỳ code implementation nào: nếu hành vi lệch sau khi refactor implementation, nơi này lập tức
// bật đèn đỏ; muốn đổi hành vi phải đồng thời sửa đặc tả và để lại diff. Các case đơn nhánh trong router_test.go chịu trách nhiệm
// làm tài liệu ý định dễ đọc, file này chịu trách nhiệm về độ ưu tiên và tính bảo toàn trong toàn bộ không gian tổ hợp.

import (
	"reflect"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// expectKind là kết quả quyết định ở tầng đặc tả: route đến ai, làm loại việc gì.
type expectKind int

const (
	expectNil expectKind = iota
	expectRewrite
	expectArcReview
	expectArcSummary
	expectVolumeSummary
	expectExpandArc
	expectNewVolume
	expectNextChapter
	expectFoundationFill
	expectGlobalReview
	expectOutlineFeedback
)

// expectedInstruction tính quyết định mong đợi cho một State theo đặc tả kiến trúc.
// Độ ưu tiên (mục đầu tiên khớp từ trên xuống):
//  1. thiếu Progress / Phase cuối → LLM quyết định (nil)
//  2. giai đoạn lập kế hoạch (không phải viết): thiếu thiết lập và xác định được planner (save_foundation đã ghi scale)
//     → tiếp tục dispatch cùng planner theo mục thiếu; nếu không → LLM quyết định (nil, gồm chọn planner lần đầu)
//  3. hàng đợi viết lại/trau chuốt không rỗng → writer theo đầu hàng đợi (ưu tiên tuyệt đối, đè mọi việc cuối arc)
//  4. Flow=Reviewing / Steering → LLM quyết định (nil)
//  5. artifact tổng hợp bị thiếu → Editor dựng bổ sung
//  6. sửa đổi bên ngoài ảnh hưởng kế hoạch sau đó → Architect tiêu thụ
//  7. cuối arc chế độ phân tầng → review → tóm tắt arc → (cuối tập) tóm tắt tập → mở rộng arc tiếp → append tập mới
//  8. còn lại → writer viết tiếp chương sau
func expectedInstruction(s State) expectKind {
	p := s.Progress
	if p == nil || p.Phase == domain.PhaseComplete {
		return expectNil
	}
	if p.Phase != domain.PhaseWriting {
		if len(s.FoundationMissing) > 0 && s.PlanningTier != "" {
			return expectFoundationFill
		}
		return expectNil
	}
	if len(p.PendingRewrites) > 0 {
		return expectRewrite
	}
	if p.Flow == domain.FlowReviewing || p.Flow == domain.FlowSteering {
		return expectNil
	}
	if s.AggregateRefresh != nil {
		return expectArcSummary
	}
	if s.ImmediateFeedbackCount > 0 {
		return expectOutlineFeedback
	}
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return expectArcReview
		case !s.HasArcSummary:
			return expectArcSummary
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return expectVolumeSummary
		case b.NeedsExpansion && b.NextArc > 0:
			return expectExpandArc
		case b.NeedsNewVolume:
			return expectNewVolume
		}
	}
	// không phân tầng: mỗi ReviewInterval chương có một lần duyệt xét toàn cục (nếu chưa làm thì review trước rồi viết tiếp).
	if !p.Layered && s.LastCompleted > 0 {
		if due, _ := domain.ShouldReview(len(p.CompletedChapters)); due && !s.HasGlobalReview {
			return expectGlobalReview
		}
	}
	return expectNextChapter
}

// classify xếp Instruction implementation trả về vào loại đặc tả; tổ hợp không nhận ra thì fail ngay.
func classify(t *testing.T, inst *Instruction) expectKind {
	t.Helper()
	if inst == nil {
		return expectNil
	}
	switch inst.Agent {
	case "writer":
		switch {
		case contains(inst.Task, "viết lại") || contains(inst.Task, "trau chuốt"):
			return expectRewrite
		case contains(inst.Task, "Viết chương"):
			return expectNextChapter
		}
	case "editor":
		switch {
		case contains(inst.Task, "Duyệt arc"):
			return expectArcReview
		case contains(inst.Task, "Duyệt toàn cục"):
			return expectGlobalReview
		case contains(inst.Task, "save_arc_summary"):
			return expectArcSummary
		case contains(inst.Task, "save_volume_summary"):
			return expectVolumeSummary
		}
	case "architect_long":
		switch {
		case contains(inst.Task, "Bổ sung các mục còn thiếu"):
			return expectFoundationFill
		case contains(inst.Task, "writer_feedback"):
			return expectOutlineFeedback
		case contains(inst.Task, "expand_arc"):
			return expectExpandArc
		case contains(inst.Task, "append_volume"):
			return expectNewVolume
		}
	case "architect_short":
		if contains(inst.Task, "Bổ sung các mục còn thiếu") {
			return expectFoundationFill
		}
		if contains(inst.Task, "writer_feedback") {
			return expectOutlineFeedback
		}
	}
	t.Fatalf("instruction không thể phân loại: agent=%q task=%q", inst.Agent, inst.Task)
	return expectNil
}

// boundaryCase là một điểm enum của chiều ranh giới arc: hình dạng ranh giới + ba fact tóm tắt.
type boundaryCase struct {
	name             string
	boundary         *storepkg.ArcBoundary
	hasArcReview     bool
	hasArcSummary    bool
	hasVolumeSummary bool
}

func enumerateBoundaryCases() []boundaryCase {
	cases := []boundaryCase{
		{name: "no-boundary"},
		{name: "mid-arc", boundary: &storepkg.ArcBoundary{Volume: 1, Arc: 1}},
	}
	type volCase struct {
		name       string
		volumeEnd  bool
		volSummary bool
	}
	type followCase struct {
		name      string
		expansion bool
		nextArc   int
		newVolume bool
	}
	volCases := []volCase{
		{name: "vol-mid", volumeEnd: false},
		{name: "vol-end-nosum", volumeEnd: true, volSummary: false},
		{name: "vol-end-sum", volumeEnd: true, volSummary: true},
	}
	followCases := []followCase{
		{name: "settled"},
		{name: "expand", expansion: true, nextArc: 4},
		{name: "expand-no-nextarc", expansion: true, nextArc: 0}, // thiếu vị trí mở rộng → không thể mở rộng
		{name: "new-volume", newVolume: true},
	}
	for _, review := range []bool{false, true} {
		for _, summary := range []bool{false, true} {
			for _, vc := range volCases {
				for _, fc := range followCases {
					cases = append(cases, boundaryCase{
						name: fmtBool("rev", review) + fmtBool("+sum", summary) + "+" + vc.name + "+" + fc.name,
						boundary: &storepkg.ArcBoundary{
							IsArcEnd:       true,
							IsVolumeEnd:    vc.volumeEnd,
							Volume:         2,
							Arc:            3,
							NextVolume:     2,
							NextArc:        fc.nextArc,
							NeedsExpansion: fc.expansion,
							NeedsNewVolume: fc.newVolume,
						},
						hasArcReview:     review,
						hasArcSummary:    summary,
						hasVolumeSummary: vc.volSummary,
					})
				}
			}
		}
	}
	return cases
}

func fmtBool(label string, v bool) string {
	if v {
		return label
	}
	return label + "!"
}

func TestRoute_ExhaustiveAgainstSpec(t *testing.T) {
	phases := []domain.Phase{domain.PhaseInit, domain.PhasePremise, domain.PhaseOutline, domain.PhaseWriting, domain.PhaseComplete}
	flows := []domain.FlowState{domain.FlowWriting, domain.FlowReviewing, domain.FlowRewriting, domain.FlowPolishing, domain.FlowSteering}
	queues := [][]int{nil, {7, 9}}
	// {1..5} trúng điểm kích hoạt duyệt xét toàn cục ReviewInterval(=5)
	completedSets := [][]int{nil, {1, 2, 3}, {1, 2, 3, 4, 5}}
	missingSets := [][]string{nil, {"characters", "world_rules"}}
	tiers := []domain.PlanningTier{"", domain.PlanningTierShort, domain.PlanningTierLong}
	globalReviews := []bool{false, true}
	feedbackCounts := []int{0, 1}
	aggregates := []*AggregateRefresh{nil, {Kind: AggregateArcSummary, Volume: 1, Arc: 1, EndChapter: 5}}

	total := 0
	for _, phase := range phases {
		for _, fl := range flows {
			for _, queue := range queues {
				for _, layered := range []bool{false, true} {
					for _, completed := range completedSets {
						for _, missing := range missingSets {
							for _, tier := range tiers {
								for _, hasGlobal := range globalReviews {
									for _, feedbackCount := range feedbackCounts {
										for _, aggregate := range aggregates {
											for _, bc := range enumerateBoundaryCases() {
												total++
												p := &domain.Progress{
													Phase:             phase,
													Flow:              fl,
													Layered:           layered,
													CompletedChapters: append([]int(nil), completed...),
													PendingRewrites:   append([]int(nil), queue...),
												}
												last := 0
												if n := len(completed); n > 0 {
													last = completed[n-1]
												}
												s := State{
													Progress:               p,
													LastCompleted:          last,
													ArcBoundary:            bc.boundary,
													HasArcReview:           bc.hasArcReview,
													HasArcSummary:          bc.hasArcSummary,
													HasVolumeSummary:       bc.hasVolumeSummary,
													FoundationMissing:      append([]string(nil), missing...),
													PlanningTier:           tier,
													HasGlobalReview:        hasGlobal,
													ImmediateFeedbackCount: feedbackCount,
													AggregateRefresh:       aggregate,
												}

												before := snapshotState(s)
												inst := Route(s)
												want := expectedInstruction(s)
												got := classify(t, inst)
												if got != want {
													t.Fatalf("phase=%s flow=%s queue=%v layered=%v completed=%v missing=%v tier=%q global=%v boundary=%s:\nđặc tả muốn %d, implementation trả %d (inst=%+v)",
														phase, fl, queue, layered, completed, missing, tier, hasGlobal, bc.name, want, got, inst)
												}
												assertConservation(t, s, inst)
												if !reflect.DeepEqual(before, snapshotState(s)) {
													t.Fatalf("Route phải là pure function, không được sửa input State (boundary=%s)", bc.name)
												}
												if again := Route(s); !reflect.DeepEqual(inst, again) {
													t.Fatalf("Route phải tất định: hai lần gọi cho kết quả khác nhau (boundary=%s)", bc.name)
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if total < 5000 {
		t.Fatalf("không gian enum co lại ngoài dự kiến (%d tổ hợp), hãy kiểm tra enum chiều", total)
	}
}

// assertConservation là các tính chất bảo toàn không phụ thuộc nhánh cụ thể.
func assertConservation(t *testing.T, s State, inst *Instruction) {
	t.Helper()
	if inst == nil {
		return
	}
	p := s.Progress
	if p == nil || p.Phase == domain.PhaseComplete {
		t.Fatalf("không được sinh instruction khi trạng thái cuối hoặc không có tiến độ: %+v", inst)
	}
	if p.Phase != domain.PhaseWriting {
		// instruction hợp lệ duy nhất trong giai đoạn lập kế hoạch: dispatch bổ sung, và planner nhất quán với tier đã ghi
		wantPlanner := "architect_long"
		if s.PlanningTier == domain.PlanningTierShort {
			wantPlanner = "architect_short"
		}
		if inst.Agent != wantPlanner || !contains(inst.Task, "Bổ sung các mục còn thiếu") || inst.Chapter != 0 {
			t.Fatalf("instruction giai đoạn lập kế hoạch phải là dispatch bổ sung và planner khớp tier=%q: %+v", s.PlanningTier, inst)
		}
		return
	}
	switch inst.Agent {
	case "writer":
		if inst.Chapter <= 0 {
			t.Fatalf("instruction writer phải có số chương: %+v", inst)
		}
		if len(p.PendingRewrites) > 0 {
			if inst.Chapter != p.PendingRewrites[0] {
				t.Fatalf("khi hàng đợi viết lại không rỗng phải dispatch đầu hàng đợi %d, got %d", p.PendingRewrites[0], inst.Chapter)
			}
			wantVerb := "viết lại"
			if p.Flow == domain.FlowPolishing {
				wantVerb = "trau chuốt"
			}
			if !contains(inst.Task, wantVerb) {
				t.Fatalf("động từ nhiệm vụ hàng đợi phải là %q: %q", wantVerb, inst.Task)
			}
		} else if inst.Chapter != p.NextChapter() {
			t.Fatalf("số chương instruction viết tiếp phải là NextChapter=%d, got %d", p.NextChapter(), inst.Chapter)
		}
	case "editor", "architect_long", "architect_short":
		if inst.Chapter != 0 {
			t.Fatalf("%s instruction không được có số chương: %+v", inst.Agent, inst)
		}
	default:
		t.Fatalf("target routing không xác định %q", inst.Agent)
	}
	if inst.Task == "" || inst.Reason == "" {
		t.Fatalf("Task và Reason của instruction đều không được rỗng: %+v", inst)
	}
}

// snapshotState deep-copy State để assert pure function.
func snapshotState(s State) State {
	cp := s
	if s.Progress != nil {
		p := *s.Progress
		p.CompletedChapters = append([]int(nil), s.Progress.CompletedChapters...)
		p.PendingRewrites = append([]int(nil), s.Progress.PendingRewrites...)
		cp.Progress = &p
	}
	if s.ArcBoundary != nil {
		b := *s.ArcBoundary
		cp.ArcBoundary = &b
	}
	if s.AggregateRefresh != nil {
		refresh := *s.AggregateRefresh
		cp.AggregateRefresh = &refresh
	}
	cp.FoundationMissing = append([]string(nil), s.FoundationMissing...)
	return cp
}
