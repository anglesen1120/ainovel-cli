package agents

// Xác minh end-to-end hành vi kết hợp giữa hard stop save_review và StopGuard nhận biết nhiệm vụ (dây nối thật của editor trong build.go
// cấu hình: StopAfterToolResult khớp save_review/save_*_summary,
// StopGuardFactory dùng guard thật, tool ghi checkpoint thật).
//
// Kịch bản một (nhiệm vụ tóm tắt nhưng rà soát trước): editor được giao tạo tóm tắt arc, nhưng lại gọi save_review trước —
// hard stop kích hoạt nhưng guard từ chối; sau khi inject nhắc nhở, editor tới save_arc_summary mới thật sự thoát.
// Đây là tiền đề an toàn để khôi phục hard stop save_review, ngăn hồi quy vòng lặp vô hạn khiến tóm tắt arc không bao giờ ghi xuống.
//
// Kịch bản hai (nhiệm vụ rà soát kết thúc một bước): editor được giao rà soát, save_review ghi xuống là hard stop được cho qua,
// không chạy thêm một lượt LLM để kết thúc.

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
	"github.com/voocel/ainovel-cli/internal/agents/guard"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// editorStopAfterToolResult giữ cùng tiêu chí với cấu hình editor trong build.go.
func editorStopAfterToolResult(toolName string, _ json.RawMessage) bool {
	return toolName == "save_review" || toolName == "save_arc_summary" || toolName == "save_volume_summary"
}

func checkpointTool(t *testing.T, st *store.Store, name, step string) agentcore.Tool {
	t.Helper()
	return agentcore.NewFuncTool(name, "fake "+name, map[string]any{"type": "object"},
		func(context.Context, json.RawMessage) (json.RawMessage, error) {
			if _, err := st.Checkpoints.Append(domain.ArcScope(1, 1), step, "artifact", "digest"); err != nil {
				t.Fatalf("append checkpoint %s: %v", step, err)
			}
			return json.RawMessage(`"saved"`), nil
		})
}

func runEditorLike(t *testing.T, st *store.Store, task string, model agentcore.ChatModel, tools []agentcore.Tool) {
	t.Helper()
	cfg := subagent.Config{
		Name:                "editor",
		Description:         "test editor",
		Model:               model,
		SystemPrompt:        "test",
		Tools:               tools,
		MaxTurns:            10,
		StopAfterToolResult: editorStopAfterToolResult,
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return guard.NewEditorStopGuard(st, task, nil)
		},
	}
	tool := subagent.NewRunner(cfg).AsTool()
	args, _ := json.Marshal(map[string]string{"agent": "editor", "task": task})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("subagent execute: %v", err)
	}
}

func TestEditorFlow_SummaryTaskSurvivesEarlyReview(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}

	var calls atomic.Int32
	model := &contractModel{fn: func(i int, _ []agentcore.Message) (*agentcore.LLMResponse, error) {
		switch i {
		case 0:
			// Lệch hướng: nhiệm vụ tóm tắt nhưng lại rà soát trước.
			return &agentcore.LLMResponse{Message: assistantToolCall("save_review", `{}`)}, nil
		default:
			// Sau khi guard từ chối hard stop và inject nhắc nhở, lượt này mới sinh tóm tắt.
			calls.Add(1)
			return &agentcore.LLMResponse{Message: assistantToolCall("save_arc_summary", `{}`)}, nil
		}
	}}

	runEditorLike(t, st, "Tạo tóm tắt arc 1 quyển 1 (save_arc_summary)", model, []agentcore.Tool{
		checkpointTool(t, st, "save_review", "review"),
		checkpointTool(t, st, "save_arc_summary", "arc_summary"),
	})

	if calls.Load() == 0 {
		t.Fatal("Sau khi hard stop save_review bị guard từ chối, editor phải tiếp tục tới save_arc_summary — nếu run kết thúc ngay sau rà soát, nghĩa là thoát trạng thái cuối đã đi vòng qua guard và vòng lặp vô hạn của tóm tắt arc sẽ hồi quy")
	}
	all := st.Checkpoints.All()
	var hasSummary bool
	for _, cp := range all {
		if cp.Step == "arc_summary" {
			hasSummary = true
		}
	}
	if !hasSummary {
		t.Fatal("Tóm tắt arc cuối cùng phải được ghi xuống")
	}
}

func TestEditorFlow_ReviewTaskStopsAtSaveReview(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}

	model := &contractModel{fn: func(i int, _ []agentcore.Message) (*agentcore.LLMResponse, error) {
		if i == 0 {
			return &agentcore.LLMResponse{Message: assistantToolCall("save_review", `{}`)}, nil
		}
		t.Fatal("Nhiệm vụ rà soát phải hard stop sau khi save_review ghi xuống; mô hình không được nhận lượt bổ sung")
		return nil, nil
	}}

	runEditorLike(t, st, "Rà soát cấp arc cho arc 1 quyển 1 (scope=arc)", model, []agentcore.Tool{
		checkpointTool(t, st, "save_review", "review"),
	})

	if got := model.calls(); got != 1 {
		t.Fatalf("Nhiệm vụ rà soát phải kết thúc sau đúng một lần gọi mô hình, got %d", got)
	}
}
