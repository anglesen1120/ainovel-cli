package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// InterventionFacts là gói facts cho phân loại can thiệp (snapshot tại thời điểm Collect).
// Trước khi Engine thực thi Dispatch tại biên, dùng Phase/QueueHead để đối chiếu (giữa tham vấn và thực thi có
// worker chạy, facts có thể đã tiến lên; không khớp → bỏ và hỏi lại bằng facts mới).
type InterventionFacts struct {
	Phase                    string           `json:"phase,omitempty"`
	Flow                     string           `json:"flow,omitempty"`
	Title                    string           `json:"title,omitempty"`
	CompletedChapters        int              `json:"completed_chapters"`
	OutlinedChapters         int              `json:"outlined_chapters,omitempty"`
	DynamicPlanning          bool             `json:"dynamic_planning"`
	NextChapter              int              `json:"next_chapter,omitempty"`
	PendingRewrites          []int            `json:"pending_rewrites,omitempty"`
	ReopenCount              int              `json:"reopen_count,omitempty"` // số lần tích lũy người dùng dùng /reopen rõ ràng để mở lại sách đã hoàn tất
	FoundationMissing        []string         `json:"foundation_missing,omitempty"`
	PlanningTier             string           `json:"planning_tier,omitempty"`
	AdvanceMode              string           `json:"advance_mode,omitempty"`
	HasAdvanceHold           bool             `json:"has_advance_hold"`
	AdvanceHoldAfter         string           `json:"advance_hold_after,omitempty"`
	AdvanceHoldTargetChapter int              `json:"advance_hold_target_chapter,omitempty"`
	AdvanceHoldReason        string           `json:"advance_hold_reason,omitempty"`
	Running                  bool             `json:"running"`                  // khi can thiệp tới có run đang diễn ra hay không
	CheckpointSeq            int64            `json:"checkpoint_seq,omitempty"` // checkpoint mới nhất tại thời điểm Collect; Engine dùng để đối chiếu
	RecentDecisions          []RecentDecision `json:"recent_decisions,omitempty"`
}

// RecentDecision là bộ nhớ can thiệp: tóm tắt vài lần phân xử gần nhất, bao phủ tham chiếu xuyên can thiệp kiểu "lần trước sửa thế nào rồi".
type RecentDecision struct {
	At     string `json:"at"`
	Input  string `json:"input"`
	Reason string `json:"reason,omitempty"`
}

// QueueHead trả đầu hàng đợi rewrite (không có thì 0), Engine dùng để đối chiếu.
func (f InterventionFacts) QueueHead() int {
	if len(f.PendingRewrites) > 0 {
		return f.PendingRewrites[0]
	}
	return 0
}

// CollectInterventionFacts đọc đủ facts phân loại từ store. Mọi lỗi đọc facts điều khiển đều được
// trả lỗi rõ ràng, cấm Arbiter ra quyết định ngữ nghĩa trên snapshot không đầy đủ ghép từ giá trị zero.
func CollectInterventionFacts(st *storepkg.Store) (InterventionFacts, error) {
	var f InterventionFacts
	if st == nil {
		return f, fmt.Errorf("store không được để trống")
	}
	missing, err := st.FoundationMissing()
	if err != nil {
		return f, fmt.Errorf("đọc trạng thái thiết lập nền tảng: %w", err)
	}
	f.FoundationMissing = missing
	book, err := st.Book.Load()
	if err != nil {
		return f, fmt.Errorf("đọc thông tin tác phẩm: %w", err)
	}
	if book != nil {
		f.Title = book.Title
	}
	p, err := st.Progress.Load()
	if err != nil {
		return f, fmt.Errorf("đọc tiến độ: %w", err)
	}
	if p != nil {
		f.Phase = string(p.Phase)
		f.Flow = string(p.Flow)
		f.CompletedChapters = len(p.CompletedChapters)
		f.DynamicPlanning = p.Layered
		if p.Layered {
			outline, outlineErr := st.Outline.LoadOutline()
			if outlineErr != nil {
				return f, fmt.Errorf("đọc dàn ý chi tiết hiện tại: %w", outlineErr)
			}
			f.OutlinedChapters = len(outline)
		} else {
			f.OutlinedChapters = p.TotalChapters
		}
		f.NextChapter = p.NextChapter()
		f.PendingRewrites = append([]int(nil), p.PendingRewrites...)
		f.ReopenCount = p.ReopenCount
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		return f, fmt.Errorf("đọc metadata chạy: %w", err)
	}
	if meta != nil {
		f.PlanningTier = string(meta.PlanningTier)
		f.AdvanceMode = string(meta.AdvanceMode)
		if meta.AdvanceHold != nil {
			f.HasAdvanceHold = true
			f.AdvanceHoldAfter = string(meta.AdvanceHold.After)
			f.AdvanceHoldTargetChapter = meta.AdvanceHold.TargetChapter
			f.AdvanceHoldReason = meta.AdvanceHold.Reason
		}
	}
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		f.CheckpointSeq = cp.Seq
	}
	recent, err := st.Decisions.Recent(5)
	if err != nil {
		return f, fmt.Errorf("đọc phân xử gần đây: %w", err)
	}
	for _, r := range recent {
		if r.Kind != "intervention" {
			continue
		}
		f.RecentDecisions = append(f.RecentDecisions, RecentDecision{
			At: r.At, Input: truncateRunes(r.Input, 80), Reason: r.Reason,
		})
	}
	return f, nil
}

// AdvanceHoldOp là hành động pause một lần: pause tại biên công việc, khi hàng đợi rewrite rỗng hoặc khi chương mục tiêu hoàn tất; cũng có thể hủy.
type AdvanceHoldOp struct {
	Cancel        bool                    `json:"cancel,omitempty"`
	After         domain.AdvanceHoldAfter `json:"after,omitempty"`
	TargetChapter int                     `json:"target_chapter,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}

// ReopenOp rewrite sách đã hoàn tất: mở lại toàn sách vào trạng thái rewrite và đưa chương mục tiêu vào hàng đợi (chỉ hợp lệ khi phase=complete).
type ReopenOp struct {
	Chapters []int  `json:"chapters"`
	Reason   string `json:"reason,omitempty"`
}

// InterventionDecision phân xử can thiệp. Tổ hợp hành động tự do, thứ tự thực thi do Engine cố định:
// answer → rules → hold → reopen → dispatch; tối đa một dispatch (fact kiểu).
type InterventionDecision struct {
	Answer   string         `json:"answer,omitempty"`
	Rules    string         `json:"rules,omitempty"`
	Hold     *AdvanceHoldOp `json:"hold,omitempty"`
	Reopen   *ReopenOp      `json:"reopen,omitempty"`
	Dispatch *DispatchOp    `json:"dispatch,omitempty"`
	Reason   string         `json:"reason"`
}

var interventionContract = llmcontract.Contract{
	Name:        "arbiter_intervention",
	Description: "Phân xử can thiệp người dùng: trả lời, quy tắc, pause, mở lại và dispatch",
	Schema: schema.Object(
		schema.Property("answer", llmcontract.Nullable(schema.String("Văn bản echo cho người dùng; không có thì null"))).Required(),
		schema.Property("rules", llmcontract.Nullable(schema.String("Nguyên văn quy tắc viết dài hạn cần ghi xuống; không có thì null"))).Required(),
		schema.Property("hold", llmcontract.Nullable(schema.Object(
			schema.Property("cancel", schema.Bool("Có hủy pause một lần hiện có hay không")).Required(),
			schema.Property("after", llmcontract.Nullable(schema.Enum("Điểm kích hoạt pause; khi hủy thì null", string(domain.AdvanceHoldAtBoundary), string(domain.AdvanceHoldAfterRewritesDrained), string(domain.AdvanceHoldAtChapter)))).Required(),
			schema.Property("target_chapter", llmcontract.Nullable(schema.Int("Chương mục tiêu khi after=chapter; trường hợp khác là null"))).Required(),
			schema.Property("reason", llmcontract.Nullable(schema.String("Tóm tắt yêu cầu người dùng; khi hủy có thể là null"))).Required(),
		))).Required(),
		schema.Property("reopen", llmcontract.Nullable(schema.Object(
			schema.Property("chapters", schema.Array("Số chương cần mở lại", schema.Int("Số chương"))).Required(),
			schema.Property("reason", llmcontract.Nullable(schema.String("Lý do mở lại"))).Required(),
		))).Required(),
		schema.Property("dispatch", dispatchSchema("Mục tiêu dispatch; khi không cần dispatch thì null")).Required(),
		schema.Property("reason", schema.String("Lý do phân xử một câu")).Required(),
	),
}

// ValidateAgainst kiểm tra cơ học theo facts (tính hợp lệ trong cảnh; kiểu đã loại trừ hành động xuyên cảnh).
func (d *InterventionDecision) ValidateAgainst(f InterventionFacts) error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được để trống")
	}
	if d.Answer == "" && d.Rules == "" && d.Hold == nil && d.Reopen == nil && d.Dispatch == nil {
		return fmt.Errorf("Quyết định rỗng: ít nhất phải có một hành động hoặc answer")
	}
	if err := d.Dispatch.validate(); err != nil {
		return err
	}
	if err := validateDispatchAgainst(d.Dispatch, f.Phase); err != nil {
		return err
	}
	complete := f.Phase == string(domain.PhaseComplete)
	if d.Reopen != nil {
		if !complete {
			return fmt.Errorf("reopen chỉ giới hạn ở giai đoạn hoàn tất (phase hiện tại=%s)", f.Phase)
		}
		if len(d.Reopen.Chapters) == 0 {
			return fmt.Errorf("reopen.chapters không được để trống")
		}
		for _, ch := range d.Reopen.Chapters {
			if ch < 1 || ch > f.CompletedChapters {
				return fmt.Errorf("reopen chương %d vượt biên (đã hoàn thành %d chương)", ch, f.CompletedChapters)
			}
		}
	}
	if complete && d.Dispatch != nil {
		return fmt.Errorf("Giai đoạn hoàn tất cấm dispatch trực tiếp; rewrite dùng reopen (sau khi vào hàng đợi Router sẽ tự dispatch)")
	}
	if d.Hold != nil && !d.Hold.Cancel {
		if f.Phase != string(domain.PhaseWriting) {
			return fmt.Errorf("pause một lần chỉ giới hạn ở giai đoạn viết (phase hiện tại=%s)", f.Phase)
		}
		hold := domain.AdvanceHold{After: d.Hold.After, TargetChapter: d.Hold.TargetChapter, Reason: d.Hold.Reason}
		if err := hold.Validate(); err != nil {
			return fmt.Errorf("hold không hợp lệ: %w", err)
		}
		nextChapter := f.NextChapter
		if nextChapter == 0 {
			nextChapter = f.CompletedChapters + 1
		}
		if hold.After == domain.AdvanceHoldAtChapter && hold.TargetChapter < nextChapter {
			return fmt.Errorf("Chương mục tiêu %d sớm hơn chương kế tiếp hiện tại %d", hold.TargetChapter, nextChapter)
		}
	}
	return nil
}

// validateDispatchAgainst biến kỷ luật giai đoạn trong prompt thành phòng tuyến cơ học. Architect có thể ở giai đoạn lập kế hoạch
// và giai đoạn viết để bảo trì cấu trúc; Writer/Editor chỉ được tiêu thụ facts tác phẩm đã đầy đủ và vào writing.
func validateDispatchAgainst(dispatch *DispatchOp, phase string) error {
	if dispatch == nil {
		return nil
	}
	if phase == "" {
		return fmt.Errorf("Thiếu phase, cấm thực thi dispatch")
	}
	if phase == string(domain.PhaseComplete) {
		return fmt.Errorf("Giai đoạn hoàn tất cấm dispatch trực tiếp")
	}
	switch dispatch.Agent {
	case "writer", "editor":
		if phase != string(domain.PhaseWriting) {
			return fmt.Errorf("%s chỉ có thể dispatch trong giai đoạn writing (phase hiện tại=%s)", dispatch.Agent, phase)
		}
	}
	return nil
}

// DecideIntervention phân loại can thiệp. Ngữ nghĩa thất bại: trả error → bên gọi echo rõ ràng
// lý do thất bại thật, và không sinh bất kỳ ghi nào (thà không động, không được động sai).
func DecideIntervention(ctx context.Context, model agentcore.ChatModel, systemPrompt string, facts InterventionFacts, text string) (InterventionDecision, error) {
	payload, err := marshalPayload(struct {
		Intervention string            `json:"intervention"`
		Facts        InterventionFacts `json:"facts"`
	}{Intervention: text, Facts: facts})
	if err != nil {
		return InterventionDecision{}, err
	}
	return decide(ctx, model, interventionContract, systemPrompt, payload, func(d *InterventionDecision) error {
		return d.ValidateAgainst(facts)
	})
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
