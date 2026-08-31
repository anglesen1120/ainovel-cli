package arbiter

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// PlanStartDecision là quyết định khởi động: chọn người lập kế hoạch và tạo văn bản tác vụ (được mở rộng khi cần).
type PlanStartDecision struct {
	Planner string `json:"planner"` // architect_long | architect_short
	Task    string `json:"task"`    // Tác vụ đầy đủ giao cho người lập kế hoạch (bao gồm yêu cầu đã mở rộng)
	Reason  string `json:"reason"`
}

func (d *PlanStartDecision) Validate() error {
	if d.Planner != "architect_long" && d.Planner != "architect_short" {
		return fmt.Errorf("planner không hợp lệ: %q (chọn architect_long / architect_short)", d.Planner)
	}
	if strings.TrimSpace(d.Task) == "" {
		return fmt.Errorf("task không được để trống")
	}
	if strings.TrimSpace(d.Reason) == "" {
		return fmt.Errorf("reason không được để trống")
	}
	return nil
}

// planStartContract nằm sát PlanStartDecision: mọi trường đều required, planner là enum khép kín.
var planStartContract = llmcontract.Contract{
	Name:        "arbiter_plan_start",
	Description: "Phân xử khởi động: chọn người lập kế hoạch và tạo văn bản tác vụ đầy đủ",
	Schema: schema.Object(
		schema.Property("planner", schema.Enum("Người lập kế hoạch", "architect_long", "architect_short")).Required(),
		schema.Property("task", schema.String("Tác vụ đầy đủ giao cho người lập kế hoạch (bao gồm yêu cầu đã mở rộng)")).Required(),
		schema.Property("reason", schema.String("Lý do lựa chọn")).Required(),
	),
}

// planStartPayload là tải người dùng của plan_start (dữ kiện chính là đầu vào, không có trạng thái store — sách mới).
type planStartPayload struct {
	Requirement string `json:"requirement"`
	Style       string `json:"style,omitempty"`
}

// DecidePlanStart chọn người lập kế hoạch dựa trên yêu cầu người dùng; khi yêu cầu quá ngắn (<20 ký tự),
// tự bổ sung vào task hướng khác biệt hóa, độc giả mục tiêu, điểm hấp dẫn cốt lõi và ít nhất một móc câu khác thường.
// Ngữ nghĩa thất bại: trả về error → bên gọi dừng khởi động và báo lỗi rõ ràng (người dùng có mặt lúc khởi động, báo lỗi tốt hơn suy đoán).
func DecidePlanStart(ctx context.Context, model agentcore.ChatModel, systemPrompt, requirement, style string) (PlanStartDecision, error) {
	payload, err := marshalPayload(planStartPayload{Requirement: requirement, Style: style})
	if err != nil {
		return PlanStartDecision{}, err
	}
	return decide(ctx, model, planStartContract, systemPrompt, payload, (*PlanStartDecision).Validate)
}
