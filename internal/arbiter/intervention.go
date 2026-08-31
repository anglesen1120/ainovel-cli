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

// InterventionFacts là gói dữ kiện phân loại can thiệp (ảnh chụp tại thời điểm Collect).
// Trước khi thực thi Dispatch ở ranh giới, Engine đối chiếu bằng Phase/QueueHead (worker có thể
// chạy giữa lúc tư vấn và thực thi, khiến dữ kiện tiến triển; nếu không khớp thì loại bỏ và hỏi lại bằng dữ kiện mới).
type InterventionFacts struct {
	Phase                    string           `json:"phase,omitempty"`
	Flow                     string           `json:"flow,omitempty"`
	Title                    string           `json:"title,omitempty"`
	CompletedChapters        int              `json:"completed_chapters"`
	OutlinedChapters         int              `json:"outlined_chapters,omitempty"`
	DynamicPlanning          bool             `json:"dynamic_planning"`
	NextChapter              int              `json:"next_chapter,omitempty"`
	PendingRewrites          []int            `json:"pending_rewrites,omitempty"`
	ReopenCount              int              `json:"reopen_count,omitempty"` // Số lần người dùng chủ động mở lại sách đã hoàn thành qua /reopen
	FoundationMissing        []string         `json:"foundation_missing,omitempty"`
	PlanningTier             string           `json:"planning_tier,omitempty"`
	AdvanceMode              string           `json:"advance_mode,omitempty"`
	HasAdvanceHold           bool             `json:"has_advance_hold"`
	AdvanceHoldAfter         string           `json:"advance_hold_after,omitempty"`
	AdvanceHoldTargetChapter int              `json:"advance_hold_target_chapter,omitempty"`
	AdvanceHoldReason        string           `json:"advance_hold_reason,omitempty"`
	Running                  bool             `json:"running"`                  // Có run đang thực thi tại thời điểm can thiệp
	CheckpointSeq            int64            `json:"checkpoint_seq,omitempty"` // Checkpoint mới nhất tại thời điểm Collect; Engine dùng để đối chiếu
	RecentDecisions          []RecentDecision `json:"recent_decisions,omitempty"`
}

// RecentDecision là bộ nhớ can thiệp: tóm tắt vài phân xử gần nhất, hỗ trợ tham chiếu xuyên can thiệp như “lần trước đã sửa thế nào”.
type RecentDecision struct {
	At     string `json:"at"`
	Input  string `json:"input"`
	Reason string `json:"reason,omitempty"`
}

// QueueHead trả về đầu hàng đợi viết lại (0 nếu không có); Engine dùng để đối chiếu.
func (f InterventionFacts) QueueHead() int {
	if len(f.PendingRewrites) > 0 {
		return f.PendingRewrites[0]
	}
	return 0
}

// CollectInterventionFacts đọc đủ dữ kiện phân loại từ store. Mọi lỗi đọc dữ kiện điều khiển đều
// được trả về rõ ràng; Arbiter không được quyết định ngữ nghĩa dựa trên ảnh chụp không đầy đủ ghép từ giá trị không.
func CollectInterventionFacts(st *storepkg.Store) (InterventionFacts, error) {
	var f InterventionFacts
	if st == nil {
		return f, fmt.Errorf("store không được để trống")
	}
	missing, err := st.FoundationMissing()
	if err != nil {
		return f, fmt.Errorf("không thể đọc trạng thái thiết lập nền tảng: %w", err)
	}
	f.FoundationMissing = missing
	book, err := st.Book.Load()
	if err != nil {
		return f, fmt.Errorf("không thể đọc thông tin tác phẩm: %w", err)
	}
	if book != nil {
		f.Title = book.Title
	}
	p, err := st.Progress.Load()
	if err != nil {
		return f, fmt.Errorf("không thể đọc tiến độ: %w", err)
	}
	if p != nil {
		f.Phase = string(p.Phase)
		f.Flow = string(p.Flow)
		f.CompletedChapters = len(p.CompletedChapters)
		f.DynamicPlanning = p.Layered
		if p.Layered {
			outline, outlineErr := st.Outline.LoadOutline()
			if outlineErr != nil {
				return f, fmt.Errorf("không thể đọc dàn ý chi tiết hiện tại: %w", outlineErr)
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
		return f, fmt.Errorf("không thể đọc siêu dữ liệu chạy: %w", err)
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
		return f, fmt.Errorf("không thể đọc các quyết định gần đây: %w", err)
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

// AdvanceHoldOp là thao tác tạm dừng một lần: dừng ở ranh giới công việc, khi viết lại đã cạn hàng đợi hoặc khi hoàn thành chương mục tiêu; cũng có thể hủy.
type AdvanceHoldOp struct {
	Cancel        bool                    `json:"cancel,omitempty"`
	After         domain.AdvanceHoldAfter `json:"after,omitempty"`
	TargetChapter int                     `json:"target_chapter,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}

// ReopenOp là thao tác làm lại sau khi hoàn thành: mở lại toàn bộ sách vào trạng thái làm lại và đưa chương mục tiêu vào hàng đợi (chỉ hợp lệ khi phase=complete).
type ReopenOp struct {
	Chapters []int  `json:"chapters"`
	Reason   string `json:"reason,omitempty"`
}

// InterventionDecision là quyết định phân xử can thiệp. Các thao tác được kết hợp tự do, với thứ tự thực thi cố định trong Engine:
// answer → rules → hold → reopen → dispatch; tối đa một dispatch (ràng buộc kiểu dữ kiện).
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
	Description: "Phân xử can thiệp người dùng: phản hồi, quy tắc, tạm dừng, mở lại và phân công",
	Schema: schema.Object(
		schema.Property("answer", llmcontract.Nullable(schema.String("Văn bản phản hồi cho người dùng; null nếu không có"))).Required(),
		schema.Property("rules", llmcontract.Nullable(schema.String("Nguyên văn quy tắc viết lâu dài cần lưu; null nếu không có"))).Required(),
		schema.Property("hold", llmcontract.Nullable(schema.Object(
			schema.Property("cancel", schema.Bool("Có hủy lần tạm dừng một lần hiện có không")).Required(),
			schema.Property("after", llmcontract.Nullable(schema.Enum("Điểm kích hoạt tạm dừng; null khi hủy", string(domain.AdvanceHoldAtBoundary), string(domain.AdvanceHoldAfterRewritesDrained), string(domain.AdvanceHoldAtChapter)))).Required(),
			schema.Property("target_chapter", llmcontract.Nullable(schema.Int("Chương mục tiêu khi after=chapter; null trong các trường hợp khác"))).Required(),
			schema.Property("reason", llmcontract.Nullable(schema.String("Tóm tắt yêu cầu người dùng; có thể null khi hủy"))).Required(),
		))).Required(),
		schema.Property("reopen", llmcontract.Nullable(schema.Object(
			schema.Property("chapters", schema.Array("Số chương cần mở lại", schema.Int("Số chương"))).Required(),
			schema.Property("reason", llmcontract.Nullable(schema.String("Lý do mở lại"))).Required(),
		))).Required(),
		schema.Property("dispatch", dispatchSchema("Đích phân công; null nếu không cần phân công")).Required(),
		schema.Property("reason", schema.String("Lý do phân xử trong một câu")).Required(),
	),
}

// ValidateAgainst thực hiện kiểm chứng cơ học theo dữ kiện (tính hợp lệ trong tình huống; kiểu đã loại trừ thao tác xuyên tình huống).
func (d *InterventionDecision) ValidateAgainst(f InterventionFacts) error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được để trống")
	}
	if d.Answer == "" && d.Rules == "" && d.Hold == nil && d.Reopen == nil && d.Dispatch == nil {
		return fmt.Errorf("quyết định rỗng: phải có ít nhất một thao tác hoặc answer")
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
			return fmt.Errorf("reopen chỉ dành cho giai đoạn hoàn thành (phase hiện tại=%s)", f.Phase)
		}
		if len(d.Reopen.Chapters) == 0 {
			return fmt.Errorf("reopen.chapters không được để trống")
		}
		for _, ch := range d.Reopen.Chapters {
			if ch < 1 || ch > f.CompletedChapters {
				return fmt.Errorf("chương reopen %d vượt phạm vi (đã hoàn thành %d chương)", ch, f.CompletedChapters)
			}
		}
	}
	if complete && d.Dispatch != nil {
		return fmt.Errorf("giai đoạn hoàn thành cấm phân công trực tiếp; hãy dùng reopen để làm lại (Router tự động phân công sau khi vào hàng đợi)")
	}
	if d.Hold != nil && !d.Hold.Cancel {
		if f.Phase != string(domain.PhaseWriting) {
			return fmt.Errorf("tạm dừng một lần chỉ dành cho giai đoạn viết (phase hiện tại=%s)", f.Phase)
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
			return fmt.Errorf("chương mục tiêu %d đứng trước chương kế tiếp hiện tại %d", hold.TargetChapter, nextChapter)
		}
	}
	return nil
}

// validateDispatchAgainst biến kỷ luật giai đoạn trong prompt thành tuyến phòng thủ cơ học. Architect có thể
// duy trì cấu trúc trong giai đoạn lập kế hoạch và viết; Writer/Editor chỉ dùng dữ kiện tác phẩm đã hoàn chỉnh và đang ở writing.
func validateDispatchAgainst(dispatch *DispatchOp, phase string) error {
	if dispatch == nil {
		return nil
	}
	if phase == "" {
		return fmt.Errorf("thiếu phase, cấm thực thi phân công")
	}
	if phase == string(domain.PhaseComplete) {
		return fmt.Errorf("giai đoạn hoàn thành cấm phân công trực tiếp")
	}
	switch dispatch.Agent {
	case "writer", "editor":
		if phase != string(domain.PhaseWriting) {
			return fmt.Errorf("%s chỉ có thể được phân công trong giai đoạn writing (phase hiện tại=%s)", dispatch.Agent, phase)
		}
	}
	return nil
}

// DecideIntervention phân loại can thiệp. Ngữ nghĩa thất bại: trả về error → bên gọi phản hồi rõ
// nguyên nhân thất bại thực tế và không tạo bất kỳ thao tác ghi nào (thà không làm còn hơn làm sai).
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
