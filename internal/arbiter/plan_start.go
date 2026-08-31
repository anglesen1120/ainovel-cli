package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// PlanStartDecision phân xử khởi động: chọn planner và sinh văn bản nhiệm vụ (đã mở rộng khi cần).
type PlanStartDecision struct {
	Planner string `json:"planner"` // architect_long | architect_short
	Task    string `json:"task"`    // Nhiệm vụ đầy đủ giao cho Planner (gồm yêu cầu đã mở rộng)
	Reason  string `json:"reason"`
}

func (d *PlanStartDecision) Validate() error {
	if d.Planner != "architect_long" && d.Planner != "architect_short" {
		return fmt.Errorf("planner không hợp lệ: %q (có thể chọn architect_long / architect_short)", d.Planner)
	}
	if strings.TrimSpace(d.Task) == "" {
		return fmt.Errorf("task không được để trống")
	}
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được để trống")
	}
	return nil
}

// planStartContract đặt cạnh PlanStartDecision: mọi field đều required, planner là enum đóng.
var planStartContract = llmcontract.Contract{
	Name:        "arbiter_plan_start",
	Description: "Phân xử khởi động: chọn planner và sinh văn bản nhiệm vụ đầy đủ",
	Schema: schema.Object(
		schema.Property("planner", schema.Enum("Planner", "architect_long", "architect_short")).Required(),
		schema.Property("task", schema.String("Nhiệm vụ đầy đủ giao cho Planner (gồm yêu cầu đã mở rộng)")).Required(),
		schema.Property("reason", schema.String("Lý do lựa chọn")).Required(),
	),
}

// planStartPayload là payload người dùng của plan_start (facts chính là input, không có trạng thái store — sách mới).
type planStartPayload struct {
	Requirement string `json:"requirement"`
	Style       string `json:"style,omitempty"`
}

// DecidePlanStart phân xử khởi động: chọn Planner theo yêu cầu người dùng; khi yêu cầu quá ngắn (<20 ký tự) thì trong task
// tự bổ sung hướng khác biệt hóa, độc giả mục tiêu, điểm tiêu thụ cốt lõi và ít nhất một hook phi thông thường.
// Ngữ nghĩa thất bại: trả error → bên gọi báo lỗi rõ ràng và dừng khởi động (giai đoạn khởi động có người dùng, báo lỗi tốt hơn đoán).
func DecidePlanStart(ctx context.Context, model agentcore.ChatModel, systemPrompt, requirement, style string) (PlanStartDecision, error) {
	payload, err := marshalPayload(planStartPayload{Requirement: requirement, Style: style})
	if err != nil {
		return PlanStartDecision{}, err
	}
	return decide(ctx, model, planStartContract, systemPrompt, payload, (*PlanStartDecision).Validate)
}
