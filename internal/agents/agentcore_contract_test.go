package agents

// Kiểm thử contract agentcore: ghim hành vi framework mà dự án phụ thuộc thành assertion thực thi được.
// Mỗi kiểm thử ghi rõ bên phụ thuộc; trước khi nâng agentcore phải xanh toàn bộ — comment có thể lỗi thời, kiểm thử thì không.
// Tất cả đều chạy qua subagent.Runner.Run — đây là kênh dispatch thực tế của Engine.
//
// Các contract đã được ghim:
//  1. Thoát ở trạng thái cuối StopAfterTools/StopAfterToolResult sẽ đi qua StopGuard (StopTriggerAfterTool),
//     guard từ chối (InjectMessage) có thể kéo run quay lại tiếp tục — nhận biết nhiệm vụ trong guard/subagent_guards.go
//     EditorStopGuard dựa vào hành vi này để chặn thoát sớm kiểu "được giao tạo tóm tắt nhưng chỉ rà soát".
//  2. StopReasonError / StopReasonAborted kết thúc run trực tiếp, không chạm StopGuard —
//     vì vậy hardStopReasons trong guard/subagent_guards.go chỉ cần liệt kê safety/content_filter.
//  3. Provider từ chối (các stop không phải error như safety) sẽ chạm StopGuard qua đường end_turn,
//     và info.Message.StopReason giữ nguyên giá trị — hardStopReasons dựa vào đường này để escalate ngay.
//  4. Sau khi StopGuard trả InjectMessage, mô hình nhận lượt mới; trả Escalate thì kết thúc ngay,
//     và chuỗi lỗi có thể khớp bằng errors.Is(err, agentcore.ErrStopGuard) —
//     "không thể dừng vật lý" và escalate quá giới hạn trong guard/stop_guard.go dựa vào ngữ nghĩa này.
//  5. Lỗi của Runner.Run giữ chuỗi có kiểu: agent chưa đăng ký khớp subagent.ErrUnknownAgent —
//     isDeterministicWorkerError trong host/engine.go dựa vào phân loại này thay vì nội dung lỗi.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
)

// contractModel là mock mô hình trả phản hồi dựng sẵn theo số thứ tự gọi.
type contractModel struct {
	fn  func(i int, msgs []agentcore.Message) (*agentcore.LLMResponse, error)
	idx int64
}

func (m *contractModel) take(msgs []agentcore.Message) (*agentcore.LLMResponse, error) {
	i := int(atomic.AddInt64(&m.idx, 1) - 1)
	return m.fn(i, msgs)
}

func (m *contractModel) calls() int { return int(atomic.LoadInt64(&m.idx)) }

func (m *contractModel) Generate(_ context.Context, msgs []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return m.take(msgs)
}

func (m *contractModel) GenerateStream(_ context.Context, msgs []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	resp, err := m.take(msgs)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message, StopReason: resp.Message.StopReason}
	close(ch)
	return ch, nil
}

func (m *contractModel) SupportsTools() bool { return true }

func assistantText(text string, stop agentcore.StopReason) agentcore.Message {
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(text)},
		StopReason: stop,
	}
}

func assistantToolCall(name string, args string) agentcore.Message {
	return agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID: "tc-" + name, Name: name, Args: json.RawMessage(args),
		})},
		StopReason: agentcore.StopReasonToolUse,
	}
}

func okTool(name string) agentcore.Tool {
	return agentcore.NewFuncTool(name, "contract test tool", map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`"ok"`), nil
		})
}

// runSubagent chạy một dispatch đơn bằng cấu hình đã cho qua Runner.Run (kênh dispatch của Engine).
// Trả lỗi thực thi — kết thúc do StopGuard escalate sẽ nổi lên dưới dạng error (bản thân đây cũng là contract),
// case kỳ vọng kết thúc bình thường tự assert nil.
func runSubagent(t *testing.T, cfg subagent.Config) error {
	t.Helper()
	_, err := subagent.NewRunner(cfg).Run(context.Background(), cfg.Name, "contract")
	return err
}

// Contract 1: thoát bằng tool trạng thái cuối đi qua StopGuard; sau khi guard từ chối (InjectMessage), run tiếp tục.
// Bên phụ thuộc: EditorStopGuard — sau khi các tool trạng thái cuối như save_review khớp, guard nhận biết nhiệm vụ phải
// có cơ hội kéo lại trường hợp thoát sớm khi "artifact chưa được ghi xuống".
func TestContract_TerminalToolExitConsultsStopGuard(t *testing.T) {
	var guardCalls atomic.Int32
	var trigger atomic.Value

	model := &contractModel{fn: func(i int, _ []agentcore.Message) (*agentcore.LLMResponse, error) {
		switch i {
		case 0:
			return &agentcore.LLMResponse{Message: assistantToolCall("finish", `{}`)}, nil
		default:
			// Sau khi guard từ chối thoát trạng thái cuối, mô hình phải nhận lượt mới; lượt này kết thúc bình thường.
			return &agentcore.LLMResponse{Message: assistantText("done", agentcore.StopReasonStop)}, nil
		}
	}}

	if err := runSubagent(t, subagent.Config{
		Name:           "editorish",
		Description:    "contract",
		Model:          model,
		SystemPrompt:   "test",
		Tools:          []agentcore.Tool{okTool("finish")},
		MaxTurns:       5,
		StopAfterTools: []string{"finish"},
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
				n := guardCalls.Add(1)
				if n == 1 {
					trigger.Store(info.Trigger)
					return agentcore.StopDecision{Allow: false, InjectMessage: "Chưa ghi xuống, tiếp tục"}
				}
				return agentcore.StopDecision{Allow: true}
			}
		},
	}); err != nil {
		t.Fatalf("subagent execute: %v", err)
	}

	if guardCalls.Load() < 2 {
		t.Fatalf("Thoát bằng tool trạng thái cuối phải chạm StopGuard và tiếp tục sau khi bị từ chối (kỳ vọng ≥2 lần tham vấn), nhận %d", guardCalls.Load())
	}
	if got := trigger.Load(); got != agentcore.StopTriggerAfterTool {
		t.Fatalf("Trigger của thoát trạng thái cuối phải là StopTriggerAfterTool, nhận %v", got)
	}
	if model.calls() < 2 {
		t.Fatalf("Sau khi guard từ chối, mô hình phải nhận lượt mới, nhận %d calls", model.calls())
	}
}

// Contract 2: StopReasonError / StopReasonAborted kết thúc trực tiếp, không chạm StopGuard.
// Bên phụ thuộc: comment hardStopReasons — chỉ cần xử lý ngữ nghĩa từ chối thật sự đi tới guard.
func TestContract_ErrorAndAbortedStopSkipStopGuard(t *testing.T) {
	for _, stop := range []agentcore.StopReason{agentcore.StopReasonError, agentcore.StopReasonAborted} {
		t.Run(string(stop), func(t *testing.T) {
			var guardCalls atomic.Int32
			model := &contractModel{fn: func(int, []agentcore.Message) (*agentcore.LLMResponse, error) {
				return &agentcore.LLMResponse{Message: assistantText("dead", stop)}, nil
			}}
			_ = runSubagent(t, subagent.Config{
				Name: "dying", Description: "contract", Model: model,
				SystemPrompt: "test", MaxTurns: 5,
				StopGuardFactory: func(_, _ string) agentcore.StopGuard {
					return func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
						guardCalls.Add(1)
						return agentcore.StopDecision{Allow: true}
					}
				},
			}) // ngữ nghĩa lỗi của stop error/aborted do tầng subagent định nghĩa; ở đây chỉ quan tâm guard có bị chạm hay không
			if guardCalls.Load() != 0 {
				t.Fatalf("Stop %s không được chạm StopGuard, nhận %d lần tham vấn", stop, guardCalls.Load())
			}
		})
	}
}

// Contract 3: provider từ chối (như safety) đi theo đường end_turn để chạm StopGuard,
// và info.Message.StopReason giữ nguyên giá trị. Bên phụ thuộc: hardStopReasons escalate ngay.
func TestContract_SafetyStopReachesStopGuardWithReason(t *testing.T) {
	var seen atomic.Value
	model := &contractModel{fn: func(int, []agentcore.Message) (*agentcore.LLMResponse, error) {
		return &agentcore.LLMResponse{Message: assistantText("refused", agentcore.StopReason("safety"))}, nil
	}}
	err := runSubagent(t, subagent.Config{
		Name: "refused", Description: "contract", Model: model,
		SystemPrompt: "test", MaxTurns: 5,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
				seen.Store(info.Message.StopReason)
				return agentcore.StopDecision{Allow: false, Escalate: true}
			}
		},
	})
	if got := seen.Load(); got != agentcore.StopReason("safety") {
		t.Fatalf("StopGuard phải thấy stop reason gốc safety, nhận %v", got)
	}
	if !errors.Is(err, agentcore.ErrStopGuard) {
		t.Fatalf("Escalate phải nổi lên bằng lỗi khớp được errors.Is(agentcore.ErrStopGuard), nhận %v", err)
	}
}

// Contract 4: khi end_turn, InjectMessage cho mô hình nhận lượt mới và nội dung được inject có mặt;
// Escalate kết thúc ngay, mô hình không được gọi tiếp. Bên phụ thuộc: Worker StopGuard
// "không thể dừng vật lý + escalate quá giới hạn liên tiếp".
func TestContract_StopGuardInjectContinuesEscalateTerminates(t *testing.T) {
	var sawInject atomic.Bool
	model := &contractModel{fn: func(i int, msgs []agentcore.Message) (*agentcore.LLMResponse, error) {
		if i > 0 {
			for _, m := range msgs {
				if strings.Contains(m.TextContent(), "Cấm kết thúc-contract") {
					sawInject.Store(true)
				}
			}
		}
		return &agentcore.LLMResponse{Message: assistantText("try stop", agentcore.StopReasonStop)}, nil
	}}

	var guardCalls atomic.Int32
	err := runSubagent(t, subagent.Config{
		Name: "stubborn", Description: "contract", Model: model,
		SystemPrompt: "test", MaxTurns: 10,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
				switch guardCalls.Add(1) {
				case 1:
					return agentcore.StopDecision{Allow: false, InjectMessage: "Cấm kết thúc-contract"}
				default:
					return agentcore.StopDecision{Allow: false, Escalate: true}
				}
			}
		},
	})
	if !errors.Is(err, agentcore.ErrStopGuard) {
		t.Fatalf("Escalate phải nổi lên bằng lỗi khớp được errors.Is(agentcore.ErrStopGuard), nhận %v", err)
	}

	if !sawInject.Load() {
		t.Fatal("Sau InjectMessage, request lượt tiếp theo của mô hình phải chứa thông điệp inject")
	}
	if guardCalls.Load() != 2 {
		t.Fatalf("Kỳ vọng guard được tham vấn đúng 2 lần (1 inject + 1 escalate), nhận %d", guardCalls.Load())
	}
	if model.calls() != 2 {
		t.Fatalf("Sau Escalate, mô hình không được gọi tiếp; kỳ vọng đúng 2 lần, nhận %d", model.calls())
	}
}

// Contract 5: lỗi của Runner.Run giữ chuỗi có kiểu — agent chưa đăng ký nổi lên dưới dạng subagent.ErrUnknownAgent
// Bên phụ thuộc: isDeterministicWorkerError trong host/engine.go (phân loại "retry chắc chắn cùng lỗi →
// pause trực tiếp" dựa vào errors.Is, không khớp nội dung lỗi).
func TestContract_RunUnknownAgentIsTyped(t *testing.T) {
	runner := subagent.NewRunner(subagent.Config{
		Name: "writer", Description: "contract",
		Model: &contractModel{fn: func(int, []agentcore.Message) (*agentcore.LLMResponse, error) {
			return &agentcore.LLMResponse{Message: assistantText("ok", agentcore.StopReasonStop)}, nil
		}},
		SystemPrompt: "test", MaxTurns: 3,
	})
	_, err := runner.Run(context.Background(), "ghost", "contract")
	if !errors.Is(err, subagent.ErrUnknownAgent) {
		t.Fatalf("agent chưa đăng ký phải khớp subagent.ErrUnknownAgent, nhận %v", err)
	}
}
