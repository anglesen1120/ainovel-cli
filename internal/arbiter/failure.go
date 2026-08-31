package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// FailureFacts là gói facts dùng chung cho hai cảnh worker_failure / deadlock:
// Engine đã phân loại xác định (retry/lỗi tham số... không tới đây), những gì gửi tới Arbiter đều là
// phần còn lại mà "mã xác định không tìm được lối ra".
type FailureFacts struct {
	Kind          string   `json:"kind"` // worker_failure | deadlock
	Agent         string   `json:"agent,omitempty"`
	Task          string   `json:"task,omitempty"`
	Error         string   `json:"error,omitempty"` // worker_failure: văn bản lỗi
	ErrorKind     string   `json:"error_kind,omitempty"`
	Repeats       int      `json:"repeats,omitempty"` // deadlock: số lần cùng instruction đã dispatch
	Phase         string   `json:"phase,omitempty"`
	NextChapter   int      `json:"next_chapter,omitempty"`
	PendingQueue  []int    `json:"pending_rewrites,omitempty"`
	FoundationGap []string `json:"foundation_missing,omitempty"`
	FactWarnings  []string `json:"fact_warnings,omitempty"`
}

// FailureDecision phân xử thất bại/bế tắc.
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
		return fmt.Errorf("action không hợp lệ: %q (có thể chọn retry / reroute / abort)", d.Action)
	}
}

// failureContract đặt cạnh FailureDecision: action là enum đóng, dispatch là object nullable
// (chỉ non-null khi reroute); tổ hợp liên field vẫn do ValidateAgainst kiểm tra theo facts.
var failureContract = llmcontract.Contract{
	Name:        "arbiter_failure",
	Description: "Phân xử thất bại/bế tắc: đưa ra lối ra",
	Schema: schema.Object(
		schema.Property("action", schema.Enum("Lối ra", "retry", "reroute", "abort")).Required(),
		schema.Property("dispatch", dispatchSchema("Mục tiêu dispatch (chỉ đưa khi reroute, nếu không là null)")).Required(),
		schema.Property("reason", schema.String("Lý do phân xử")).Required(),
	),
}

// DecideFailure tham vấn thất bại/bế tắc. Ngữ nghĩa thất bại: trả error → Engine xử lý theo đường bảo thủ nhất
// (pause + notify), tuyệt đối không tham vấn vô hạn.
func DecideFailure(ctx context.Context, model agentcore.ChatModel, systemPrompt string, facts FailureFacts) (FailureDecision, error) {
	payload, err := marshalPayload(facts)
	if err != nil {
		return FailureDecision{}, err
	}
	return decide(ctx, model, failureContract, systemPrompt, payload, func(d *FailureDecision) error {
		return d.ValidateAgainst(facts)
	})
}
