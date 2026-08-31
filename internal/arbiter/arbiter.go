// Package arbiter là tầng phân xử ngữ nghĩa: LLM-as-function được kích hoạt khi cần.
//
// Hai mặt phẳng đối xứng (docs/engine-arbiter.md §2):
//
//	Mặt phẳng xác định: flow.LoadState   → flow.Route     → Instruction
//	Mặt phẳng ngữ nghĩa: arbiter.Collect* → arbiter.Decide* → XxxDecision
//
// Kỷ luật: Collect tập trung IO (đọc đủ dữ kiện từ store); ngoài yêu cầu mô hình do trình
// thực thi thống nhất quản lý, Decide không có IO; có thể phát lại dữ kiện lịch sử ngoại tuyến;
// Engine chịu trách nhiệm thực thi. Mỗi tình huống có một cặp hàm và kiểu Decision riêng;
// hành động không phù hợp với tình huống không thể biểu diễn bằng kiểu; từng kiểu từ chối phần
// hợp lệ còn lại qua Validate — đầu ra Arbiter cũng không đáng tin như mọi đầu ra LLM, nên
// kiểm chứng dữ kiện là tuyến phòng thủ cuối cùng.
package arbiter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// decideMaxTokens là giới hạn đầu ra cho một lần phân xử; JSON phân xử nhỏ nên phần lớn
// ngân sách được dành cho suy luận của mô hình (tương tự userrules.normalizeMaxTokens).
const decideMaxTokens = 8192

// decide giao hợp đồng tình huống và kiểm chứng nghiệp vụ cho trình thực thi có cấu trúc thống nhất. Ngoài lệnh gọi mô hình, hàm không có IO.
func decide[T any](ctx context.Context, model agentcore.ChatModel, contract llmcontract.Contract, systemPrompt, payload string, validate func(*T) error) (T, error) {
	out, err := llmcontract.Execute(ctx, model, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: systemPrompt,
		Payload:      payload,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(decideMaxTokens)},
		Validate:     validate,
		Agent:        "arbiter",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("Đã chọn giao thức phân xử", "module", "arbiter",
					"contract", contract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider,
					"model", res.Model, "schema_fingerprint", contract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("Đầu ra phân xử tự khắc phục", "module", "arbiter", "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return out, fmt.Errorf("arbiter: %w", err)
	}
	return out, nil
}

// DispatchOp là thao tác phân công dùng chung cho các tình huống.
type DispatchOp struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

// workerNames là các đích phân công hợp lệ (khớp với đăng ký của agents.BuildWorkers). Slice
// có thứ tự đồng thời là enum schema (thứ tự cố định giữ fingerprint ổn định) và danh sách cho phép kiểm chứng.
var workerNames = []string{"architect_long", "architect_short", "writer", "editor"}

func (d *DispatchOp) validate() error {
	if d == nil {
		return nil
	}
	if !slices.Contains(workerNames, d.Agent) {
		return fmt.Errorf("dispatch.agent không hợp lệ: %q", d.Agent)
	}
	if strings.TrimSpace(d.Task) == "" {
		return fmt.Errorf("dispatch.task không được để trống")
	}
	return nil
}

// dispatchSchema là vị trí schema có thể null của DispatchOp: chỉ hành động cần phân công mới
// cung cấp đối tượng; các trường hợp còn lại là null (chế độ strict yêu cầu toàn bộ trường; null biểu đạt tính tùy chọn).
func dispatchSchema(desc string) map[string]any {
	return llmcontract.Nullable(schema.Object(
		schema.Property("agent", schema.Enum(desc, workerNames...)).Required(),
		schema.Property("task", schema.String("Mô tả đầy đủ tác vụ giao cho worker này")).Required(),
	))
}

// marshalPayload tuần tự hóa gói dữ kiện; thất bại là lỗi chương trình và phải được hiển thị —
// âm thầm tạo dữ kiện rỗng sẽ khiến mô hình phán đoán sai dựa trên đầu vào giả.
func marshalPayload(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("arbiter: không thể tuần tự hóa gói dữ kiện: %w", err)
	}
	return string(data), nil
}
