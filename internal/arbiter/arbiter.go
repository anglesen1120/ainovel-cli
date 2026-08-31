// Package arbiter là tầng phân xử ngữ nghĩa: LLM-as-function được đánh thức theo nhu cầu.
//
// Hai mặt phẳng đối xứng (docs/engine-arbiter.md §2):
//
//	Mặt phẳng xác định:  flow.LoadState   → flow.Route     → Instruction
//	Mặt phẳng ngữ nghĩa:    arbiter.Collect* → arbiter.Decide* → XxxDecision
//
// Kỷ luật: Collect gom IO (đọc đủ facts từ store); Decide không có IO ngoài request mô hình do executor thống nhất quản lý,
// có thể replay offline bằng facts lịch sử; thực thi thuộc Engine. Mỗi cảnh có một cặp hàm + kiểu Decision riêng,
// hành động không khớp cảnh không thể biểu đạt ở mức kiểu; phần hợp lệ còn lại do Validate của từng kiểu từ chối —
// Đầu ra Arbiter cũng không đáng tin như mọi đầu ra LLM; kiểm chứng facts là cửa cuối cùng.
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

// decideMaxTokens là giới hạn đầu ra cho một lần phân xử; JSON phân xử rất nhỏ, phần lớn dành cho ngân sách suy nghĩ của mô hình suy luận
// (tương tự userrules.normalizeMaxTokens).
const decideMaxTokens = 8192

// decide giao contract theo cảnh và kiểm tra nghiệp vụ cho executor cấu trúc thống nhất. Không có IO ngoài gọi mô hình.
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
				slog.Debug("Chọn giao thức phân xử", "module", "arbiter",
					"contract", contract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider,
					"model", res.Model, "schema_fingerprint", contract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("Tự phục hồi đầu ra phân xử", "module", "arbiter", "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return out, fmt.Errorf("arbiter: %w", err)
	}
	return out, nil
}

// DispatchOp là hành động dispatch dùng chung giữa các cảnh.
type DispatchOp struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

// workerNames là các mục tiêu dispatch hợp lệ (khớp đăng ký của agents.BuildWorkers). Slice có thứ tự:
// vừa làm enum schema (thứ tự ổn định giữ fingerprint ổn định), vừa làm whitelist kiểm tra.
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

// dispatchSchema là phần schema nullable của DispatchOp: chỉ hành động cần dispatch mới đưa object,
// các trường hợp còn lại là null (strict mode required mọi field, ngữ nghĩa tùy chọn biểu đạt bằng null).
func dispatchSchema(desc string) map[string]any {
	return llmcontract.Nullable(schema.Object(
		schema.Property("agent", schema.Enum(desc, workerNames...)).Required(),
		schema.Property("task", schema.String("Mô tả nhiệm vụ đầy đủ giao cho worker này")).Required(),
	))
}

// marshalPayload serialize gói facts; thất bại là lỗi chương trình, phải lộ ra — âm thầm giả facts rỗng
// sẽ khiến mô hình phán đoán sai dựa trên input giả.
func marshalPayload(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("arbiter: serialize gói facts thất bại: %w", err)
	}
	return string(data), nil
}
