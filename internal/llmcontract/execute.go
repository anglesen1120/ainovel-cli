package llmcontract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/llmretry"
)

// FailureKind phân biệt biên thất bại không thể sửa bằng phản hồi cấu trúc trong cùng lần.
type FailureKind string

const (
	FailureRequest  FailureKind = "request"
	FailureProtocol FailureKind = "protocol"
	FailureLength   FailureKind = "length"
	FailureSafety   FailureKind = "safety"
	FailureContract FailureKind = "contract"
)

// Failure giữ loại thất bại và đầu ra gốc của mô hình để bên gọi quyết định log, artifact và biểu đạt UI.
type Failure struct {
	Kind     FailureKind
	Contract string
	Raw      string
	Err      error
}

func (e *Failure) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return e.Contract
	}
	if e.Contract == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %v", e.Contract, e.Err)
}

func (e *Failure) Unwrap() error { return e.Err }

// Correction mô tả một lỗi đầu ra mà mô hình có thể sửa. Attempt là số thứ tự lần gọi vừa thất bại.
type Correction struct {
	Attempt int
	Layer   string
	Mode    Mode
	Raw     string
	Err     error
}

// Hooks chỉ phụ trách quan sát, không đổi ngữ nghĩa thực thi.
type Hooks struct {
	Resolved     func(Resolution)
	RequestRetry func(llmretry.Event)
	Correction   func(Correction)
}

// Request định nghĩa một lần trả về cấu trúc trực tiếp. Contract là nguồn duy nhất của cấu trúc, Validate chỉ xử lý
// các ràng buộc nghiệp vụ JSON Schema không biểu đạt được.
type Request[T any] struct {
	Contract     Contract
	SystemPrompt string
	Payload      string
	Options      []agentcore.CallOption
	Validate     func(*T) error
	Agent        string
	Hooks        Hooks
}

const promptCorrection = "Đầu ra phía trên không phù hợp JSON Schema. Hãy sửa theo lỗi và chỉ xuất object JSON hoàn chỉnh, không giải thích hoặc dùng Markdown fence."
const semanticCorrection = "Cấu trúc JSON phía trên hợp lệ nhưng giá trị field không qua kiểm tra nghiệp vụ. Hãy sửa theo lỗi và xuất lại object JSON hoàn chỉnh."

// Execute thống nhất chọn giao thức, chuẩn bị prompt, retry request, phân loại stop reason, Schema/DTO
// decode và tự phục hồi bằng phản hồi nghiệp vụ. Lỗi format/Schema ở prompt mode và lỗi nghiệp vụ ở cả hai mode sẽ
// liên tục phản hồi cho mô hình cho tới khi thành công hoặc context kết thúc; vi phạm contract native sẽ lộ ngay.
func Execute[T any](ctx context.Context, model llmretry.Generator, req Request[T]) (T, error) {
	var zero T
	if model == nil {
		return zero, &Failure{Kind: FailureProtocol, Contract: req.Contract.Name, Err: errors.New("Chưa cấu hình mô hình")}
	}

	schemaOptions, resolution := Plan(model, req.Contract)
	systemPrompt, err := PreparePrompt(req.SystemPrompt, req.Contract, resolution)
	if err != nil {
		return zero, &Failure{Kind: FailureContract, Contract: req.Contract.Name, Err: fmt.Errorf("Chuẩn bị contract đầu ra: %w", err)}
	}
	if req.Hooks.Resolved != nil {
		req.Hooks.Resolved(resolution)
	}

	messages := []agentcore.Message{
		agentcore.SystemMsg(systemPrompt),
		agentcore.UserMsg(req.Payload),
	}
	options := append(schemaOptions, req.Options...)
	native := resolution.Mode == ModeNativeJSONSchema

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		resp, err := llmretry.Generate(ctx, model, llmretry.Config{
			Agent:   req.Agent,
			OnRetry: req.Hooks.RequestRetry,
		}, messages, options...)
		if err != nil {
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			return zero, &Failure{Kind: FailureRequest, Contract: req.Contract.Name, Err: err}
		}
		if resp == nil {
			return zero, &Failure{Kind: FailureProtocol, Contract: req.Contract.Name, Err: errors.New("Mô hình trả phản hồi rỗng")}
		}

		raw := resp.Message.TextContent()
		switch resp.Message.StopReason {
		case agentcore.StopReasonLength:
			return zero, &Failure{Kind: FailureLength, Contract: req.Contract.Name, Raw: raw, Err: errors.New("Đầu ra của mô hình bị cắt do vượt quá giới hạn độ dài (stop_reason=length)")}
		case agentcore.StopReasonSafety:
			return zero, &Failure{Kind: FailureSafety, Contract: req.Contract.Name, Raw: raw, Err: errors.New("Mô hình từ chối hoặc kích hoạt bộ lọc nội dung (stop_reason=safety)")}
		case agentcore.StopReasonError:
			return zero, &Failure{Kind: FailureProtocol, Contract: req.Contract.Name, Raw: raw, Err: errors.New("Mô hình kết thúc với trạng thái lỗi (stop_reason=error)")}
		case agentcore.StopReasonToolUse:
			return zero, &Failure{Kind: FailureProtocol, Contract: req.Contract.Name, Raw: raw, Err: errors.New("Lệnh gọi cấu trúc bất ngờ trả tool call (stop_reason=tool_use)")}
		case agentcore.StopReasonAborted:
			return zero, &Failure{Kind: FailureProtocol, Contract: req.Contract.Name, Raw: raw, Err: errors.New("Lệnh gọi mô hình bị dừng (stop_reason=aborted)")}
		}

		body := strings.TrimSpace(raw)
		if native {
			if body == "" {
				return zero, &Failure{Kind: FailureContract, Contract: req.Contract.Name, Raw: raw, Err: errors.New("Schema native trả nội dung rỗng")}
			}
		} else {
			body = ExtractJSONObject(raw)
		}

		layer := "schema"
		var cause error
		if body == "" {
			layer, cause = "decode", errors.New("Không tìm thấy object JSON trong đầu ra")
		} else if err := ValidateJSON(req.Contract.Schema, []byte(body)); err != nil {
			cause = err
		} else {
			var out T
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				// Schema đã pass nhưng DTO không decode được, nghĩa là contract tĩnh không khớp kiểu Go,
				// tiếp tục yêu cầu mô hình viết lại không thể sửa lỗi code.
				return zero, &Failure{Kind: FailureContract, Contract: req.Contract.Name, Raw: raw, Err: fmt.Errorf("schema không khớp DTO: %w", err)}
			}
			if req.Validate == nil {
				return out, nil
			}
			if err := req.Validate(&out); err == nil {
				return out, nil
			} else {
				layer, cause = "semantic", err
			}
		}

		if native && layer != "semantic" {
			return zero, &Failure{Kind: FailureContract, Contract: req.Contract.Name, Raw: raw, Err: fmt.Errorf("vi phạm contract schema native: %w", cause)}
		}
		correction := Correction{Attempt: attempt, Layer: layer, Mode: resolution.Mode, Raw: raw, Err: cause}
		if req.Hooks.Correction != nil {
			req.Hooks.Correction(correction)
		}
		hint := promptCorrection
		if layer == "semantic" {
			hint = semanticCorrection
		}
		messages = append(messages,
			agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock(raw)}},
			agentcore.UserMsg(hint+"\nlỗi："+cause.Error()),
		)
	}
}

// ExtractJSONObject trả object JSON cân bằng đầu tiên trong văn bản; dấu ngoặc nhọn trong string không tính vào cấp.
func ExtractJSONObject(raw string) string {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return ""
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(raw); i++ {
		switch c := raw[i]; {
		case inString && escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && c == '{':
			depth++
		case !inString && c == '}':
			depth--
			if depth == 0 {
				return raw[start : i+1]
			}
		}
	}
	return ""
}
