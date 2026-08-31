package domain

import (
	"fmt"
	"strings"
)

// Phase biểu thị giai đoạn sáng tác tiểu thuyết.
type Phase string

const (
	PhaseInit     Phase = "init"
	PhasePremise  Phase = "premise"
	PhaseOutline  Phase = "outline"
	PhaseWriting  Phase = "writing"
	PhaseComplete Phase = "complete"
)

// FlowState loại luồng hoạt động hiện tại, dùng để khôi phục checkpoint.
type FlowState string

const (
	FlowWriting   FlowState = "writing"
	FlowReviewing FlowState = "reviewing"
	FlowRewriting FlowState = "rewriting"
	FlowPolishing FlowState = "polishing"
	FlowSteering  FlowState = "steering"
)

// PlanningTier biểu thị cấp độ độ dài của kế hoạch tác phẩm.
type PlanningTier string

const (
	PlanningTierShort PlanningTier = "short"
	PlanningTierMid   PlanningTier = "mid"
	PlanningTierLong  PlanningTier = "long"
)

// Progress theo dõi tiến độ, được lưu bền vững vào meta/progress.json.
type Progress struct {
	Phase          Phase `json:"phase"`
	CurrentChapter int   `json:"current_chapter"`
	// TotalChapters trong chế độ không phân tầng là số chương của dàn ý chi tiết; trong chế độ phân tầng chỉ là
	// giá trị dung lượng nội bộ ước tính có chứa khung xương, dùng cho chiến lược ngữ cảnh, không đại diện cho
	// tổng số chương cố định của toàn sách.
	TotalChapters     int         `json:"total_chapters"`
	CompletedChapters []int       `json:"completed_chapters"`
	TotalWordCount    int         `json:"total_word_count"`
	ChapterWordCounts map[int]int `json:"chapter_word_counts,omitempty"` // số từ mỗi chương, hỗ trợ sửa tổng số từ khi viết lại
	InProgressChapter int         `json:"in_progress_chapter,omitempty"` // chương đang viết (khôi phục ở cấp cảnh)
	CompletedScenes   []int       `json:"completed_scenes,omitempty"`    // số hiệu các cảnh đã hoàn thành trong chương hiện tại
	Flow              FlowState   `json:"flow,omitempty"`                // luồng hiện tại
	PendingRewrites   []int       `json:"pending_rewrites,omitempty"`    // hàng đợi chương cần viết lại
	RewriteReason     string      `json:"rewrite_reason,omitempty"`      // lý do viết lại
	StrandHistory     []string    `json:"strand_history,omitempty"`      // ghi lại dominant_strand theo thứ tự chương
	HookHistory       []string    `json:"hook_history,omitempty"`        // ghi lại hook_type theo thứ tự chương
	// Theo dõi phân tầng cho truyện dài (chỉ dùng ở chế độ truyện dài, truyện ngắn/trung là giá trị zero)
	CurrentVolume int  `json:"current_volume,omitempty"`
	CurrentArc    int  `json:"current_arc,omitempty"`
	Layered       bool `json:"layered,omitempty"`
	// ReopenedFromComplete đánh dấu cuốn sách này đã được mở lại từ trạng thái hoàn tất bằng reopen để sửa.
	// Sửa chỉ thay đổi các chương hiện có, không tăng giảm cấu trúc, nên sau khi dọn trống phải cho phép hoàn tất
	// lại khi "cấu trúc đã đầy đủ" (tránh việc các manh mối cuối tập bị sửa làm kẹt trong vòng lặp chết viết
	// thêm vượt phạm vi writing →); viết hướng trước đó không đặt cờ này, và phán định hoàn tất vẫn giữ ngữ
	// nghĩa thận trọng về việc thu gọn các manh mối.
	ReopenedFromComplete bool `json:"reopened_from_complete,omitempty"`
	// ReopenCount ghi lại số lần tích lũy cuốn sách này được mở lại từ trạng thái hoàn tất (sự kiện kiểm toán /reopen).
	// Nó đồng thời bảo đảm progress.json sau khi hoàn tất lại từ lần mở lại sẽ khác với nội dung lần hoàn tất trước:
	// checkpoint loại trùng lặp theo cùng digest là idempotent, nên việc hoàn tất lại có byte giống hệt sẽ không tạo
	// checkpoint mới, và StopGuard sẽ hiểu nhầm complete_book thành quay vòng vô ích rồi nâng mức kết thúc.
	ReopenCount int `json:"reopen_count,omitempty"`
}

// IsResumable kiểm tra có thể khôi phục từ điểm ngắt hay không.
func (p *Progress) IsResumable() bool {
	return p.Phase == PhaseWriting && p.CurrentChapter > 0
}

// NextChapter trả về số chương kế tiếp cần viết.
func (p *Progress) NextChapter() int {
	return p.LatestCompleted() + 1
}

// LatestCompleted trả về số chương lớn nhất đã hoàn thành; trả về 0 nếu chưa có chương nào.
func (p *Progress) LatestCompleted() int {
	max := 0
	for _, ch := range p.CompletedChapters {
		if ch > max {
			max = ch
		}
	}
	return max
}

// ContextProfile chiến lược nạp ngữ cảnh, tự thích ứng theo tổng số chương.
type ContextProfile struct {
	SummaryWindow  int  // nạp tóm tắt của N chương gần nhất
	TimelineWindow int  // nạp dòng thời gian của N chương gần nhất
	Layered        bool // true = bật nạp tóm tắt phân tầng (tóm tắt tập + tóm tắt arc + tóm tắt chương)
}

// MemoryPolicy biểu thị chiến lược sử dụng bộ nhớ dùng chung ở thời gian chạy.
// Nó vừa dùng cho đầu ra ngữ cảnh, vừa dùng cho quyết định handoff / reminder ở tầng host.
type MemoryPolicy struct {
	Mode                string `json:"mode,omitempty"`
	SummaryWindow       int    `json:"summary_window,omitempty"`
	TimelineWindow      int    `json:"timeline_window,omitempty"`
	LayeredSummaries    bool   `json:"layered_summaries,omitempty"`
	SummaryStrategy     string `json:"summary_strategy,omitempty"`
	WorkingRefresh      string `json:"working_refresh,omitempty"`
	EpisodicRefresh     string `json:"episodic_refresh,omitempty"`
	PlanningRefresh     string `json:"planning_refresh,omitempty"`
	FoundationRefresh   string `json:"foundation_refresh,omitempty"`
	PlanningFocus       string `json:"planning_focus,omitempty"`
	FoundationFocus     string `json:"foundation_focus,omitempty"`
	PreviousTailChars   int    `json:"previous_tail_chars,omitempty"`
	ChapterPlanEnabled  bool   `json:"chapter_plan_enabled,omitempty"`
	RelatedLookup       bool   `json:"related_chapter_lookup,omitempty"`
	CurrentOutlineBound bool   `json:"current_outline_bound,omitempty"`
	HandoffPreferred    bool   `json:"handoff_preferred,omitempty"`
	ReadOnlyThreshold   int    `json:"read_only_threshold,omitempty"`
}

// NewContextProfile tính toán chiến lược ngữ cảnh theo tổng số chương.
func NewContextProfile(totalChapters int) ContextProfile {
	switch {
	case totalChapters <= 15:
		return ContextProfile{SummaryWindow: 10, TimelineWindow: 10}
	case totalChapters <= 50:
		return ContextProfile{SummaryWindow: 5, TimelineWindow: 8}
	default:
		return ContextProfile{SummaryWindow: 3, TimelineWindow: 5, Layered: true}
	}
}

// NewChapterMemoryPolicy tạo chiến lược bộ nhớ thời gian chạy cho chương dựa trên tiến độ và chiến lược ngữ cảnh.
func NewChapterMemoryPolicy(progress *Progress, profile ContextProfile, currentOutlineBound bool) MemoryPolicy {
	policy := MemoryPolicy{
		Mode:                "chapter",
		SummaryWindow:       profile.SummaryWindow,
		TimelineWindow:      profile.TimelineWindow,
		LayeredSummaries:    profile.Layered,
		WorkingRefresh:      "làm mới mỗi khi nạp theo chương",
		EpisodicRefresh:     "làm mới theo mỗi lần nộp chương, đánh giá và thay đổi trạng thái truyện dài",
		PreviousTailChars:   800,
		ChapterPlanEnabled:  true,
		CurrentOutlineBound: currentOutlineBound,
		ReadOnlyThreshold:   5,
	}
	if profile.Layered {
		policy.SummaryStrategy = "tóm tắt tập + tóm tắt arc + tóm tắt các chương gần nhất"
	} else {
		policy.SummaryStrategy = "tóm tắt các chương gần nhất"
	}
	if progress != nil {
		if progress.TotalChapters > 30 {
			policy.RelatedLookup = true
		}
		if progress.Flow == FlowReviewing || progress.Flow == FlowRewriting || progress.Flow == FlowPolishing {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.HandoffPreferred = true
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.HandoffPreferred = true
		}
		if progress.Layered && len(progress.CompletedChapters) >= 6 {
			policy.ReadOnlyThreshold = 4
		}
		if len(progress.CompletedChapters) >= 12 {
			policy.ReadOnlyThreshold = 4
		}
	}
	return policy
}

// NewArchitectMemoryPolicy trả về chiến lược bộ nhớ dùng ở giai đoạn lập kế hoạch.
func NewArchitectMemoryPolicy() MemoryPolicy {
	return MemoryPolicy{
		Mode:               "architect",
		PlanningRefresh:    "làm mới khi cập nhật cấu trúc tập, la bàn hoặc tóm tắt",
		FoundationRefresh:  "làm mới khi thay đổi nhân vật, manh mối hoặc bối cảnh",
		PlanningFocus:      "dàn ý phân tầng, la bàn, tóm tắt tập",
		FoundationFocus:    "thiết lập nhân vật, ảnh chụp nhân vật, sổ theo dõi manh mối",
		HandoffPreferred:   true,
		ChapterPlanEnabled: false,
		ReadOnlyThreshold:  4,
	}
}

// RunMeta thông tin siêu dữ liệu chạy, được lưu bền vững vào meta/run.json.
type RunMeta struct {
	StartedAt            string             `json:"started_at"`
	Provider             string             `json:"provider,omitempty"`
	Style                string             `json:"style"`
	Model                string             `json:"model"`
	PlanningTier         PlanningTier       `json:"planning_tier,omitempty"`
	StartPrompt          string             `json:"start_prompt,omitempty"`           // yêu cầu sáng tác gốc của người dùng (sự kiện đầu vào, ghi xuống trước khi phán định khởi động; sau khi phán định thất bại sẽ dựa vào đây để bổ sung phán định)
	PlanStart            *PlanStartRecord   `json:"plan_start,omitempty"`             // sự kiện phán định khởi động, là căn cứ duy nhất cho khôi phục khi giai đoạn lập kế hoạch bị sập
	PendingSteer         string             `json:"pending_steer,omitempty"`          // chỉ lệnh Steer chưa hoàn tất, sẽ được chèn lại khi khôi phục ngắt quãng
	AdvanceMode          ChapterAdvanceMode `json:"advance_mode"`                     // chế độ tiến chương: auto / review
	AdvancePermitChapter int                `json:"advance_permit_chapter,omitempty"` // chương phía trước được cho phép một lần trong chế độ review
	AdvanceHold          *AdvanceHold       `json:"advance_hold,omitempty"`           // ý định tạm dừng một lần đã được ký của can thiệp hiện tại
}

// ChapterAdvanceMode quyết định một chương mới có cần được cho phép theo từng chương hay không.
type ChapterAdvanceMode string

const (
	ChapterAdvanceAuto   ChapterAdvanceMode = "auto"
	ChapterAdvanceReview ChapterAdvanceMode = "review"
)

// Valid báo cáo chế độ tiến chương có được phiên bản hiện tại hỗ trợ hay không.
func (m ChapterAdvanceMode) Valid() bool {
	return m == ChapterAdvanceAuto || m == ChapterAdvanceReview
}

// UnsupportedAdvanceModeError biểu thị chế độ điều khiển của cuốn sách không được nhị phân hiện tại hỗ trợ.
// Bên gọi phải dừng tạo Host có thể ghi và nhắc người dùng dùng phiên bản khớp; cấm đoán suy đoán hạ cấp.
type UnsupportedAdvanceModeError struct {
	Mode ChapterAdvanceMode
}

func (e *UnsupportedAdvanceModeError) Error() string {
	return fmt.Sprintf("không hỗ trợ chế độ tiến chương %q, hãy dùng bản ainovel mới đã tạo dự án này", e.Mode)
}

// AdvanceHoldAfter là điều kiện kích hoạt xác định cho một lần tạm dừng dùng một lần.
type AdvanceHoldAfter string

const (
	AdvanceHoldAtBoundary           AdvanceHoldAfter = "boundary"
	AdvanceHoldAfterRewritesDrained AdvanceHoldAfter = "rewrites_drained"
	AdvanceHoldAtChapter            AdvanceHoldAfter = "chapter"
)

// Valid báo cáo điều kiện tạm dừng có được phiên bản hiện tại hỗ trợ hay không.
func (a AdvanceHoldAfter) Valid() bool {
	return a == AdvanceHoldAtBoundary || a == AdvanceHoldAfterRewritesDrained || a == AdvanceHoldAtChapter
}

// AdvanceHold là ý định tạm dừng một lần đã được ký của can thiệp hiện tại, do Host ở biên giới tiêu thụ.
type AdvanceHold struct {
	After         AdvanceHoldAfter `json:"after"`
	TargetChapter int              `json:"target_chapter,omitempty"`
	Reason        string           `json:"reason"`
}

// Validate kiểm tra các ràng buộc cấu trúc của ý định tạm dừng một lần.
func (h AdvanceHold) Validate() error {
	if !h.After.Valid() {
		return fmt.Errorf("không hỗ trợ điều kiện tạm dừng một lần %q", h.After)
	}
	if h.After == AdvanceHoldAtChapter {
		if h.TargetChapter <= 0 {
			return fmt.Errorf("chương mục tiêu phải lớn hơn 0")
		}
	} else if h.TargetChapter != 0 {
		return fmt.Errorf("điều kiện tạm dừng %q không thể đặt chương mục tiêu", h.After)
	}
	if strings.TrimSpace(h.Reason) == "" {
		return fmt.Errorf("lý do tạm dừng một lần không được để trống")
	}
	return nil
}

// PlanStartRecord là sự kiện bền vững của phán định khởi động (phán định phải được ghi thành sự thật trước, rồi mới bắt đầu thực thi; khi khôi phục sẽ không phán định lại).
// Sau khi save_foundation đầu tiên ghi xuống scale, việc khôi phục ở giai đoạn lập kế hoạch sẽ chuyển sang suy ra từ PlanningTier, bản ghi này
// chỉ bao phủ cửa sổ "từ khi phán định hoàn tất đến khi ghi xuống lần đầu". DecisionID liên kết với kiểm toán decisions.jsonl.
type PlanStartRecord struct {
	RawPrompt   string `json:"raw_prompt"`
	Planner     string `json:"planner"`
	PlannerTask string `json:"planner_task"`
	DecisionID  string `json:"decision_id,omitempty"`
}
