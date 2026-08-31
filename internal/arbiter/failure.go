package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// FailureFacts là gói dữ kiện chung cho hai tình huống worker_failure / deadlock:
// Engine đã phân loại xác định (retry, lỗi tham số, v.v. không đến đây); chỉ phần còn lại
// mà mã xác định không tìm được lối ra mới được gửi đến Arbiter.
type FailureFacts struct {
	Kind          string   `json:"kind"` // worker_failure | deadlock
	Agent         string   `json:"agent,omitempty"`
	Task          string   `json:"task,omitempty"`
	Error         string   `json:"error,omitempty"` // worker_failure: văn bản lỗi
	ErrorKind     string   `json:"error_kind,omitempty"`
	Repeats       int      `json:"repeats,omitempty"` // deadlock: số lần đã phân công cùng chỉ thị
	Phase         string   `json:"phase,omitempty"`
	NextChapter   int      `json:"next_chapter,omitempty"`
	PendingQueue  []int    `json:"pending_rewrites,omitempty"`
	FoundationGap []string `json:"foundation_missing,omitempty"`
	FactWarnings  []string `json:"fact_warnings,omitempty"`
}

// FailureDecision là quyết định phân xử lỗi/bế tắc.
type FailureDecision struct {
	Action   string      `json:"action"` // retry | reroute | abort
	Dispatch *DispatchOp `json:"dispatch,omitempty"`
	Reason   string      `json:"reason"`
}

func (d *FailureDecision) ValidateAgainst(f FailureFacts) error {
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được để trống")
	}
	switch d.Action {
	case "retry", "abort":
		return nil
	case "reroute":
		if d.Dispatch == nil {
			return fmt.Errorf("reroute phải kèm dispatch")
		}
		if err := d.Dispatch.validate(); err != nil {
			return err
		}
		return validateDispatchAgainst(d.Dispatch, f.Phase)
	default:
		return fmt.Errorf("action không hợp lệ: %q (chọn retry / reroute / abort)", d.Action)
	}
}

// failureContract nằm sát FailureDecision: action là enum khép kín, dispatch là đối tượng có thể null
// (chỉ có khi reroute); ValidateAgainst vẫn kiểm chứng tổ hợp liên trường dựa trên dữ kiện.
var failureContract = llmcontract.Contract{
	Name:        "arbiter_failure",
	Description: "Phân xử lỗi/bế tắc: đưa ra lối xử lý",
	Schema: schema.Object(
		schema.Property("action", schema.Enum("Lối xử lý", "retry", "reroute", "abort")).Required(),
		schema.Property("dispatch", dispatchSchema("Đích phân công (chỉ cung cấp khi reroute, nếu không là null)")).Required(),
		schema.Property("reason", schema.String("Lý do phân xử")).Required(),
	),
}

// DecideFailure tư vấn khi lỗi/bế tắc. Ngữ nghĩa thất bại: trả về error → Engine xử lý
// theo lối thận trọng nhất (tạm dừng + notify), tuyệt đối không tư vấn vô hạn.
func DecideFailure(ctx context.Context, model agentcore.ChatModel, systemPrompt string, facts FailureFacts) (FailureDecision, error) {
	payload, err := marshalPayload(facts)
	if err != nil {
		return FailureDecision{}, err
	}
	return decide(ctx, model, failureContract, systemPrompt, payload, func(d *FailureDecision) error {
		return d.ValidateAgainst(facts)
	})
}
