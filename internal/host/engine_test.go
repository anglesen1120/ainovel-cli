package host

// Kiểm thử tích hợp đầu-cuối Engine (nghiệm thu nguyên mẫu trong engine-rfc.md §7):
// store thực + công cụ Worker thực + ChatModel theo kịch bản, xác minh
//  1. Chuỗi viết sách hoàn chỉnh do Route điều khiển: viết chương 1 → viết chương 2 → hoàn thành → Engine tự dừng
//  2. Đường lỗi Worker: thử lại một lần → Arbiter phán quyết worker_failure là abort → tạm dừng + ghi kiểm toán
//  3. Đường bế tắc: cùng chỉ dẫn không tiến triển ×3 → Arbiter phán quyết deadlock → ghi kiểm toán → dừng abort

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"
	"github.com/voocel/ainovel-cli/internal/arbiter"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/flow"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// scriptedChatModel là ChatModel tối thiểu tạo phản hồi bằng callback.
type scriptedChatModel struct {
	fn func(msgs []agentcore.Message) agentcore.Message
}

func TestFailureFactsKeepPartialStateAndWarnings(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "premise.md"), 0o755); err != nil {
		t.Fatal(err)
	}

	e := &engine{store: st}
	workerErr := fmt.Errorf("writer exhausted: %w", agentcore.ErrMaxTurns)
	facts := e.failureFacts("worker_failure", &flow.Instruction{Agent: "writer", Task: "Viết tiếp"}, workerErr)
	if facts.ErrorKind != "max_turns" || facts.Phase != string(domain.PhaseInit) {
		t.Fatalf("Phải giữ loại lỗi và dữ kiện tiến độ có thể đọc: %+v", facts)
	}
	if len(facts.FactWarnings) == 0 {
		t.Fatalf("Dữ kiện nền không đọc được phải được chuyển thành cảnh báo cho Arbiter: %+v", facts)
	}
}

func TestIsNonSemanticWorkerFailure(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "context overflow", err: agentcore.ErrContextOverflow, want: true},
		{name: "partial stream", err: agentcore.ErrStreamPartial, want: true},
		{name: "stream idle", err: agentcore.ErrProviderStreamIdle, want: true},
		{name: "quota", err: agentcore.ErrProviderQuota, want: true},
		{name: "rate limit", err: agentcore.ErrProviderRateLimit, want: true},
		{name: "timeout", err: agentcore.ErrProviderTimeout, want: true},
		{name: "auth", err: agentcore.ErrProviderAuth, want: true},
		{name: "network wrapped", err: fmt.Errorf("provider: %w", agentcore.ErrProviderNetwork), want: true},
		{name: "raw EOF", err: fmt.Errorf("upstream closed: EOF"), want: true},
		{name: "overloaded", err: agentcore.ErrProviderOverloaded, want: true},
		{name: "flattened overloaded", err: fmt.Errorf("bad_response_status_code: Too many concurrent requests [provider, HTTP 500, openai]"), want: true},
		{name: "content filter", err: agentcore.ErrProviderContentFilter, want: false},
		{name: "max turns", err: agentcore.ErrMaxTurns, want: false},
		{name: "stop guard", err: agentcore.ErrStopGuard, want: false},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "tool validation", err: agentcore.ErrToolValidation, want: false},
		{name: "unknown", err: fmt.Errorf("unknown failure"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNonSemanticWorkerFailure(tt.err); got != tt.want {
				t.Fatalf("isNonSemanticWorkerFailure(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestInterventionDispatchTaskPreservesOriginalAuthority(t *testing.T) {
	const task = "Kiểm tra nội dung lặp lại và bố trí viết lại khi cần"
	const original = "  Về sau không giải thích lặp lại nguồn gốc năng lực; không sửa nội dung không liên quan.\n"

	got := interventionDispatchTask(task, original)
	if !strings.Contains(got, task) {
		t.Fatalf("Mất nhiệm vụ điều phối: %q", got)
	}
	if !strings.Contains(got, original) {
		t.Fatalf("Can thiệp gốc của người dùng không được giữ nguyên từng chữ: %q", got)
	}
	if !strings.Contains(got, "nguồn ủy quyền duy nhất") {
		t.Fatalf("Thiếu mô tả ranh giới ủy quyền: %q", got)
	}
}

func (m *scriptedChatModel) Generate(_ context.Context, msgs []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return &agentcore.LLMResponse{Message: m.fn(msgs)}, nil
}

func (m *scriptedChatModel) GenerateStream(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	resp, _ := m.Generate(ctx, msgs, tools, opts...)
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message, StopReason: resp.Message.StopReason}
	close(ch)
	return ch, nil
}

func (m *scriptedChatModel) SupportsTools() bool { return true }

// editThenCancelModel tái hiện #84: mỗi Worker đều tạo một edit checkpoint
// có nội dung khác, rồi trả về context canceled trong cùng run, không bao giờ commit.
type editThenCancelModel struct {
	edits atomic.Int32
}

func (m *editThenCancelModel) Generate(_ context.Context, msgs []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == agentcore.RoleTool {
		return nil, context.Canceled
	}
	n := int(m.edits.Add(1))
	return &agentcore.LLMResponse{Message: testToolCallMsg("edit_chapter", map[string]any{
		"chapter":    1,
		"old_string": fmt.Sprintf("Phiên bản %d", n-1),
		"new_string": fmt.Sprintf("Phiên bản %d", n),
	})}, nil
}

func (m *editThenCancelModel) GenerateStream(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	resp, err := m.Generate(ctx, msgs, tools, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message, StopReason: resp.Message.StopReason}
	close(ch)
	return ch, nil
}

func (m *editThenCancelModel) SupportsTools() bool { return true }

// providerNetworkModel mô phỏng Worker gặp lỗi mạng tạm thời trước bất kỳ đầu ra mô hình nào.
// Khi MaxRetries=0, mỗi subagent.Run tương ứng một lần gọi, tiện kiểm tra số lần Engine thử lại.
type providerNetworkModel struct {
	calls atomic.Int32
}

func (m *providerNetworkModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls.Add(1)
	return nil, fmt.Errorf("test provider EOF: %w", agentcore.ErrProviderNetwork)
}

func (m *providerNetworkModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	m.calls.Add(1)
	return nil, fmt.Errorf("test provider EOF: %w", agentcore.ErrProviderNetwork)
}

func (m *providerNetworkModel) SupportsTools() bool { return true }

func testToolCallMsg(name string, args any) agentcore.Message {
	data, _ := json.Marshal(args)
	return agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{
			ID: "tc-" + name, Name: name, Args: data,
		})},
		StopReason: agentcore.StopReasonToolUse,
	}
}

func testTextMsg(text string) agentcore.Message {
	return agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(text)},
		StopReason: agentcore.StopReasonStop,
	}
}

var chapterRe = regexp.MustCompile(`(?i)(?:viết(?: lại)?|trau chuốt|viet(?: lai)?|trau chuot) chương (\d+)`)

// scriptedWriterModel chọn bước tiếp theo theo số kết quả tool đã có trong hội thoại,
// đi trọn chuỗi plan → draft → check → commit (công cụ và ghi đĩa thực).
func scriptedWriterModel() *scriptedChatModel {
	return &scriptedChatModel{fn: func(msgs []agentcore.Message) agentcore.Message {
		chapter := 0
		toolResults := 0
		for _, m := range msgs {
			if m.Role == agentcore.RoleUser {
				if match := chapterRe.FindStringSubmatch(m.TextContent()); match != nil {
					chapter, _ = strconv.Atoi(match[1])
				}
			}
			if m.Role == agentcore.RoleTool {
				toolResults++
			}
		}
		switch toolResults {
		case 0:
			return testToolCallMsg("plan_chapter", map[string]any{
				"chapter": chapter, "title": fmt.Sprintf("Chương %d", chapter),
				"goal": "Tiến triển tuyến chính", "conflict": "Nhân vật chính gặp trở ngại", "hook": "Kết thúc bằng nút thắt",
			})
		case 1:
			return testToolCallMsg("draft_chapter", map[string]any{
				"chapter": chapter, "mode": "write",
				"content": strings.Repeat(fmt.Sprintf("Nội dung chương %d, nhân vật chính lần bước trong bóng tối.", chapter), 20),
			})
		case 2:
			return testToolCallMsg("check_consistency", map[string]any{"chapter": chapter})
		default:
			return testToolCallMsg("commit_chapter", map[string]any{
				"chapter": chapter, "title": fmt.Sprintf("Chương %d", chapter), "summary": fmt.Sprintf("Tóm tắt chương %d", chapter),
				"characters": []string{"Nhân vật chính"}, "key_events": []string{"Tiến triển"},
				"timeline_events": []any{}, "foreshadow_updates": []any{},
				"relationship_changes": []any{}, "state_changes": []any{}, "cast_intros": []any{},
				"hook_type": "crisis", "dominant_strand": "quest", "feedback": nil,
			})
		}
	}}
}

// newTestEngine lắp Engine với store/observer thực; trả về Engine, bộ thu sự kiện và tín hiệu hoàn tất.
func newTestEngine(t *testing.T, st *storepkg.Store, workers *subagent.Runner, arbiterModel agentcore.ChatModel) (*engine, *[]Event, chan struct{}) {
	t.Helper()
	if err := st.RunMeta.Init("default", "test", "test"); err != nil {
		t.Fatalf("init run meta: %v", err)
	}
	var mu sync.Mutex
	events := &[]Event{}
	done := make(chan struct{}, 1)
	obs := newObserver(st, func(ev Event) {
		mu.Lock()
		*events = append(*events, ev)
		mu.Unlock()
	}, func(string) {}, func() {})
	e := &engine{
		store:           st,
		workers:         workers,
		arbiterModel:    arbiterModel,
		failurePrompt:   "sys",
		planStartPrompt: "sys",
		style:           "default",
		observer:        obs,
		refresh:         func() {},
		emitEvent: func(ev Event) {
			mu.Lock()
			*events = append(*events, ev)
			mu.Unlock()
		},
		notify: func(string, string, string, string) {},
		onDone: func() {
			select {
			case done <- struct{}{}:
			default:
			}
		},
	}
	e.gate = NewChapterAdvanceGate(st, func(string) { e.abort() }, func(string, string) {})
	return e, events, done
}

func waitEngineDone(t *testing.T, done chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Engine không dừng trong thời hạn")
	}
}

func mustInterventionFacts(t *testing.T, st *storepkg.Store) arbiter.InterventionFacts {
	t.Helper()
	facts, err := arbiter.CollectInterventionFacts(st)
	if err != nil {
		t.Fatalf("CollectInterventionFacts: %v", err)
	}
	return facts
}

func TestEngine_ReviewPermitWritesExactlyOneNewChapter(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Một", CoreEvent: "a"},
		{Chapter: 2, Title: "Hai", CoreEvent: "b"},
		{Chapter: 3, Title: "Ba", CoreEvent: "c"},
	}); err != nil {
		t.Fatal(err)
	}
	writer := subagent.Config{
		Name: "writer", Description: "test writer", Model: scriptedWriterModel(), SystemPrompt: "test",
		Tools: []agentcore.Tool{
			tools.NewPlanChapterTool(st), tools.NewDraftChapterTool(st),
			tools.NewCheckConsistencyTool(st), tools.NewCommitChapterTool(st, tools.NewStyleStatsIndex(st)),
		},
		MaxTurns: 10, StopAfterTools: []string{"commit_chapter"},
	}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), nil)
	if err := st.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview); err != nil {
		t.Fatal(err)
	}
	if err := st.RunMeta.GrantAdvancePermit(1); err != nil {
		t.Fatal(err)
	}
	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %v", err)
	}
	if len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 1 {
		t.Fatalf("Một giấy phép chỉ được ổn định đúng một chương mới: %v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvancePermitChapter != 0 {
		t.Fatalf("Giấy phép phải được dùng sau khi commit ổn định: %+v", meta)
	}
}

func TestEngine_StalePairedDispatchDoesNotBypassHold(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	e, _, _ := newTestEngine(t, st, subagent.NewRunner(), nil)
	e.pending = []controlOp{{
		hold:     &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, Reason: "Dừng lại trước"},
		dispatch: &arbiter.DispatchOp{Agent: "editor", Task: "Nhiệm vụ đã lỗi thời"},
		facts:    arbiter.InterventionFacts{Phase: string(domain.PhaseOutline)},
	}}

	if e.applyPendingOps(context.Background()) {
		t.Fatal("Điều phối ghép đôi có dữ kiện lỗi thời không được vượt Gate khi chưa vào next")
	}
	if e.next != nil || e.deferGateForNext {
		t.Fatalf("Điều phối lỗi thời không được để lại chỉ dẫn có thể thực thi: next=%+v defer=%v", e.next, e.deferGateForNext)
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvanceHold != nil {
		t.Fatalf("Điều phối ghép đôi lỗi thời không được để lại hold cô lập: %+v", meta.AdvanceHold)
	}
	if e.gate.HandleBoundary() {
		t.Fatal("Gate không được tạo tạm dừng giả khi không có hold cô lập")
	}
}

// TestEngine_WritesBookToCompletion kiểm tra chuỗi hoàn chỉnh: sách hai chương không phân tầng đi từ writing đến complete.
func TestEngine_WritesBookToCompletion(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(2); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Chương một", CoreEvent: "Mở đầu"},
		{Chapter: 2, Title: "Chương hai", CoreEvent: "Kết thúc"},
	}); err != nil {
		t.Fatalf("outline: %v", err)
	}

	writer := subagent.Config{
		Name: "writer", Description: "test writer",
		Model:        scriptedWriterModel(),
		SystemPrompt: "test",
		Tools: []agentcore.Tool{
			tools.NewPlanChapterTool(st),
			tools.NewDraftChapterTool(st),
			tools.NewCheckConsistencyTool(st),
			tools.NewCommitChapterTool(st, tools.NewStyleStatsIndex(st)),
		},
		MaxTurns:       10,
		StopAfterTools: []string{"commit_chapter"},
	}
	e, events, done := newTestEngine(t, st, subagent.NewRunner(writer), nil)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %v", err)
	}
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("Viết đủ hai chương phải hoàn thành, got phase=%s completed=%v", progress.Phase, progress.CompletedChapters)
	}
	if len(progress.CompletedChapters) != 2 {
		t.Fatalf("Phải hoàn thành 2 chương, got %v", progress.CompletedChapters)
	}
	// Hình dạng sự kiện: mỗi chương có một DISPATCH (do engine phát), dòng TOOL đến từ chuyển tiếp tiến độ.
	var dispatches, toolRows int
	for _, ev := range *events {
		switch ev.Category {
		case "DISPATCH":
			dispatches++
		case "TOOL":
			toolRows++
		}
	}
	if dispatches < 2 {
		t.Fatalf("Phải có ít nhất 2 sự kiện DISPATCH, got %d", dispatches)
	}
	if toolRows == 0 {
		t.Fatal("Tiến độ công cụ Worker không được chiếu qua bộ chuyển tiếp (thiếu dòng TOOL)")
	}
}

// TestEngine_WorkerFailureConsultsArbiterAndAborts kiểm tra đường lỗi:
// writer không hoạt động bị StopGuard nâng cấp → thử lại một lần → Arbiter phán quyết abort → tạm dừng + kiểm toán.
func TestEngine_WorkerFailureConsultsArbiterAndAborts(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(2); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Một", CoreEvent: "s"}}); err != nil {
		t.Fatalf("outline: %v", err)
	}

	var runs atomic.Int32
	// writer chỉ trả văn bản mà không ghi đĩa ở mỗi lượt → guard.NewWriterStopGuard liên tiếp chặn rồi nâng cấp → Execute báo lỗi
	idle := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg("Tôi đã viết xong (thật ra chưa làm gì)")
	}}
	writer := subagent.Config{
		Name: "writer", Description: "idle writer",
		Model: idle, SystemPrompt: "test", MaxTurns: 20,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			runs.Add(1)
			return failNTimesGuard()
		},
	}
	// Arbiter phán quyết abort.
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"writer liên tục không tạo tiến triển, đề nghị kiểm tra cấu hình mô hình thủ công"}`)
	}}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	if got := runs.Load(); got != 2 {
		t.Fatalf("Lỗi đầu phải thử lại một lần (tổng 2 spawn), got %d", got)
	}
	recs, err := st.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	var found bool
	for _, r := range recs {
		if r.Kind == "worker_failure" && r.Decider == "arbiter" {
			found = true
			if !strings.Contains(string(r.Decision), "abort") {
				t.Fatalf("Nội dung phán quyết phải chứa abort: %s", r.Decision)
			}
		}
	}
	if !found {
		t.Fatalf("Phán quyết worker_failure phải được ghi kiểm toán: %+v", recs)
	}
}

// seedStuckRewrite tạo hiện trường "chương 2 đã hoàn tất và được xếp vào hàng đợi viết lại".
func seedStuckRewrite(t *testing.T, st *storepkg.Store) {
	t.Helper()
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(5); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := st.Progress.SetPendingRewrites([]int{2}, "Đánh giá yêu cầu viết lại"); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := st.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("flow: %v", err)
	}
}

// TestEngine_DeadlockAbortDropsStuckRewrite kiểm tra mặt bế tắc của issue #110:
// khi ngắt mạch bế tắc, chương viết lại bị kẹt phải được lấy khỏi hàng đợi.
// PendingRewrites là dữ kiện bền vững; nếu chỉ tạm dừng mà không lấy ra, khởi động lại sẽ phát
// lại chỉ dẫn chết tương tự và khóa vĩnh viễn toàn bộ sách.
func TestEngine_DeadlockAbortDropsStuckRewrite(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	seedStuckRewrite(t, st)
	e, events, _ := newTestEngine(t, st, subagent.NewRunner(), nil)

	inst := &flow.Instruction{Agent: "writer", Task: "Viết lại chương 2", Chapter: 2}
	e.lastKey, e.repeats = instructionKey(inst), deadlockAbortAt-1

	if stop := e.trackDeadlock(context.Background(), &inst); !stop {
		t.Fatal("Ngắt mạch bế tắc vẫn phải dừng và chờ can thiệp thủ công")
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("Chương viết lại bị kẹt phải được lấy khỏi hàng đợi khi ngắt mạch: %v", p.PendingRewrites)
	}
	if p.Flow != domain.FlowWriting {
		t.Fatalf("Sau khi làm rỗng hàng đợi, flow phải trở về writing, thực tế %s", p.Flow)
	}
	var notified bool
	for _, ev := range *events {
		if strings.Contains(ev.Summary, "đã được lấy ra khỏi hàng đợi sửa lại") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("Bỏ qua chương viết lại phải thông báo rõ cho người dùng: %+v", *events)
	}
}

// TestEngine_DropStuckRewriteOnlyTouchesQueuedChapter kiểm tra hành động lấy khỏi hàng đợi là phá hủy:
// chỉ "chương đang nằm trong hàng đợi viết lại" được lấy ra, mọi chỉ dẫn khác không được đổi hàng đợi.
func TestEngine_DropStuckRewriteOnlyTouchesQueuedChapter(t *testing.T) {
	cases := []struct {
		name string
		inst *flow.Instruction
	}{
		{"Chỉ dẫn không phải writer", &flow.Instruction{Agent: "editor", Task: "Đánh giá cấp cung"}},
		{"Chỉ dẫn writer không liên quan chương", &flow.Instruction{Agent: "writer", Task: "Viết tiếp"}},
		{"Chương viết tiếp không nằm trong hàng đợi", &flow.Instruction{Agent: "writer", Task: "Viết chương 3", Chapter: 3}},
		{"Chỉ dẫn rỗng", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storepkg.NewStore(t.TempDir())
			seedStuckRewrite(t, st)
			e, _, _ := newTestEngine(t, st, subagent.NewRunner(), nil)
			if e.dropStuckRewrite(tc.inst) {
				t.Fatal("Không được lấy khỏi hàng đợi")
			}
			p, err := st.Progress.Load()
			if err != nil {
				t.Fatalf("progress: %v", err)
			}
			if len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 2 {
				t.Fatalf("Không được làm ảnh hưởng hàng đợi viết lại: %v", p.PendingRewrites)
			}
		})
	}
}

// TestEngine_TransientProviderFailuresDoNotBecomeDeadlock hồi quy chuỗi lỗi của chương 135:
// hai chu kỳ lỗi mạng với worker_failure=retry không được để trackDeadlock coi lượt kế tiếp là
// "cùng một nhiệm vụ viết liên tục không tiến triển" rồi điều phối lại thành deadlock.
func TestEngine_TransientProviderFailuresDoNotBecomeDeadlock(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(1); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Chương một", CoreEvent: "Mở đầu"}}); err != nil {
		t.Fatalf("outline: %v", err)
	}

	network := &providerNetworkModel{}
	writer := subagent.Config{
		Name: "writer", Description: "network failing writer",
		Model: network, SystemPrompt: "test", MaxTurns: 5, MaxRetries: 0,
	}
	var arbiterCalls atomic.Int32
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		if arbiterCalls.Add(1) == 1 {
			return testTextMsg(`{"action":"retry","dispatch":null,"reason":"Lỗi mạng tạm thời, thử lại nhiệm vụ gốc"}`)
		}
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"Mạng tiếp tục không khả dụng, tạm dừng chờ khôi phục"}`)
	}}
	e, events, done := newTestEngine(t, st, subagent.NewRunner(writer), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	if got := network.calls.Load(); got != 4 {
		t.Fatalf("Mỗi chu kỳ lỗi Engine gồm 2 lần thực thi Worker, got %d", got)
	}
	recs, err := st.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	var workerFailures, deadlocks int
	for _, rec := range recs {
		switch rec.Kind {
		case "worker_failure":
			workerFailures++
		case "deadlock":
			deadlocks++
		}
	}
	if workerFailures != 2 || deadlocks != 0 {
		t.Fatalf("Lỗi mạng chỉ được đi vào worker_failure, got worker_failure=%d deadlock=%d records=%+v", workerFailures, deadlocks, recs)
	}
	var failedDispatches, duplicateErrors int
	for _, ev := range *events {
		if ev.Category == "DISPATCH" && ev.Failed && ev.Kind == "network" && strings.Contains(ev.Detail, "test provider EOF") {
			failedDispatches++
		}
		if ev.Category == "ERROR" && strings.Contains(ev.Detail, "test provider EOF") {
			duplicateErrors++
		}
	}
	if failedDispatches != 4 || duplicateErrors != 0 {
		t.Fatalf("Mỗi lỗi Worker chỉ cập nhật DISPATCH, got dispatch=%d duplicate_error=%d events=%+v", failedDispatches, duplicateErrors, *events)
	}
}

// failNTimesGuard là StopGuard nâng cấp ngay (mô phỏng ngắt mạch do không hoạt động).
func failNTimesGuard() agentcore.StopGuard {
	return func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
		return agentcore.StopDecision{Allow: false, Escalate: true}
	}
}

// TestEngine_RetriesUnfinishedPlanStart kiểm tra tự phục hồi sau khi phán quyết khởi động thất bại:
// StartPrompt đã ghi đĩa, PlanStart còn thiếu (mô hình hỏng khi khởi động) → Engine bổ sung phán quyết tại chỗ
// → cố định PlanStartRecord → điều phối planner.
// Planner không ghi đĩa → đi theo đường bế tắc hiện có và dừng, chứng minh Engine trở lại luồng bình thường.
func TestEngine_RetriesUnfinishedPlanStart(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatalf("progress: %v", err)
	}
	// Mô phỏng hiện trường StartPrepared thất bại: dữ kiện đầu vào có, dữ kiện phán quyết còn thiếu.
	if err := st.RunMeta.SetStartPrompt("Tu tiên phàm nhân"); err != nil {
		t.Fatalf("start prompt: %v", err)
	}

	// Arbiter: lần gọi đầu là bổ sung phán quyết (plan_start), các lần sau là hỏi bế tắc (kết thúc bằng abort).
	var arbCalls atomic.Int32
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		if arbCalls.Add(1) == 1 {
			return testTextMsg(`{"planner":"architect_long","task":"Lập khung ba quyển xoay quanh tu tiên phàm nhân","reason":"Chủ đề tu tiên trường thiên"}`)
		}
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"Planner không hoạt động, dừng lại"}`)
	}}
	// Planner trả về thành công nhưng không ghi đĩa → Route luôn tạo cùng chỉ dẫn bổ sung → bế tắc.
	architect := subagent.Config{
		Name: "architect_long", Description: "idle planner",
		Model: &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
			return testTextMsg("Đã lập kế hoạch (thật ra chưa ghi đĩa)")
		}},
		SystemPrompt: "test", MaxTurns: 3,
	}
	e, events, done := newTestEngine(t, st, subagent.NewRunner(architect), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	meta, err := st.RunMeta.Load()
	if err != nil || meta == nil || meta.PlanStart == nil {
		t.Fatalf("Sau khi bổ sung phán quyết, PlanStart phải được cố định, meta=%+v err=%v", meta, err)
	}
	if meta.PlanStart.Planner != "architect_long" || meta.PlanStart.RawPrompt != "Tu tiên phàm nhân" || meta.PlanStart.DecisionID == "" {
		t.Fatalf("Các trường PlanStartRecord không đầy đủ: %+v", meta.PlanStart)
	}
	recs, err := st.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	var planStartRec bool
	for _, r := range recs {
		if r.Kind == "plan_start" && strings.Contains(string(r.Decision), "architect_long") {
			planStartRec = true
		}
	}
	if !planStartRec {
		t.Fatalf("Bổ sung phán quyết phải để lại kiểm toán plan_start: %+v", recs)
	}
	var dispatched, healed bool
	for _, ev := range *events {
		if ev.Category == "DISPATCH" {
			dispatched = true
		}
		if strings.Contains(ev.Summary, "Phán quyết khởi động đã được bổ sung") {
			healed = true
		}
	}
	if !dispatched || !healed {
		t.Fatalf("Sau khi bổ sung phán quyết phải dispatch planner và hiển thị sự kiện bổ sung, dispatched=%v healed=%v", dispatched, healed)
	}
}

// TestEngine_PlanStartRetryFailurePauses kiểm tra bổ sung phán quyết thất bại không được dừng im lặng:
// Arbiter liên tục không khả dụng → hiển thị tạm dừng rõ ràng + kiểm toán plan_start có error + không điều phối.
func TestEngine_PlanStartRetryFailurePauses(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.RunMeta.SetStartPrompt("Tu tiên phàm nhân"); err != nil {
		t.Fatalf("start prompt: %v", err)
	}

	var e *engine
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		e.abort() // Mô phỏng host hủy lời gọi thất bại liên tục; đường lỗi kết thúc rõ ràng bằng context.
		return testTextMsg("Đây không phải JSON")
	}}
	e, events, done := newTestEngine(t, st, subagent.NewRunner(), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	for _, ev := range *events {
		if ev.Category == "DISPATCH" {
			t.Fatal("Bổ sung phán quyết thất bại không được điều phối bất kỳ worker nào")
		}
	}
	var paused bool
	for _, ev := range *events {
		if strings.Contains(ev.Summary, "Phán quyết khởi động thất bại") {
			paused = true
		}
	}
	if !paused {
		t.Fatalf("Bổ sung phán quyết thất bại phải hiển thị rõ lý do tạm dừng, events=%+v", *events)
	}
	recs, err := st.Decisions.Recent(5)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	var errRec bool
	for _, r := range recs {
		if r.Kind == "plan_start" && r.Error != "" && len(r.Decision) == 0 {
			errRec = true
		}
	}
	if !errRec {
		t.Fatalf("Phán quyết thất bại phải được ghi đĩa kèm error: %+v", recs)
	}
}

// TestEngine_DeadlockConsultsArbiter kiểm tra đường bế tắc: chỉ dẫn bổ sung kế hoạch lặp lại liên tục
// → lần thứ 3 hỏi Arbiter → dừng abort + kiểm toán deadlock.
func TestEngine_DeadlockConsultsArbiter(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatalf("progress: %v", err)
	}
	// Giai đoạn lập kế hoạch + tier đã biết + mục thiếu luôn tồn tại → Route mỗi lượt tạo cùng chỉ dẫn bổ sung.
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("tier: %v", err)
	}

	// architect không có guard, trả về thành công nhưng không ghi đĩa → chỉ dẫn Route không đổi.
	lazy := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg("Đã biết (nhưng không làm gì)")
	}}
	architect := subagent.Config{
		Name: "architect_long", Description: "lazy architect",
		Model: lazy, SystemPrompt: "test", MaxTurns: 5,
	}
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"Planner nhiều lần không tạo đầu ra"}`)
	}}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(architect), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	recs, err := st.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	var found bool
	for _, r := range recs {
		if r.Kind == "deadlock" && r.Decider == "arbiter" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Phán quyết deadlock phải được ghi đĩa: %+v", recs)
	}
}

// TestEngine_IntermediateCheckpointsDoNotMaskDeadlock khóa #84: Writer liên tục sửa
// bản nháp sẽ tạo digest mới và edit checkpoint mới, nhưng nếu Route vẫn là cùng một lệnh
// "trau chuốt chương 1", nghĩa là hậu điều kiện cấp Engine (commit) chưa hoàn tất và vẫn phải cộng dồn bế tắc.
func TestEngine_IntermediateCheckpointsDoNotMaskDeadlock(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(1); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Chương một", CoreEvent: "Mở đầu"}}); err != nil {
		t.Fatalf("outline: %v", err)
	}
	if err := st.Drafts.SaveDraft(1, "Phiên bản 0, bản nháp đầu"); err != nil {
		t.Fatalf("draft: %v", err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune("Phiên bản 0, bản nháp đầu")), "mystery", "quest"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "Kiểm thử trau chuốt không commit"); err != nil {
		t.Fatalf("pending rewrite: %v", err)
	}
	if err := st.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("flow: %v", err)
	}

	writerModel := &editThenCancelModel{}
	writer := subagent.Config{
		Name: "writer", Description: "edit then cancel writer",
		Model: writerModel, SystemPrompt: "test",
		Tools:    []agentcore.Tool{tools.NewEditChapterTool(st)},
		MaxTurns: 5,
	}
	// Dù Arbiter luôn yêu cầu retry với worker_failure / deadlock, lần ngắt mạch cứng thứ 5 hiện có
	// vẫn phải chặn trước khi điều phối và không được bị edit checkpoint đặt lại.
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg(`{"action":"retry","dispatch":null,"reason":"Tiếp tục thử lại"}`)
	}}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	if got := writerModel.edits.Load(); got != deadlockAbortAt-1 {
		t.Fatalf("deadlock phải ngắt mạch cứng trước lần điều phối thứ %d, thực tế edit %d lần", deadlockAbortAt, got)
	}
	var edits int
	for _, cp := range st.Checkpoints.All() {
		if cp.Scope.Matches(domain.ChapterScope(1)) && cp.Step == "edit" {
			edits++
		}
	}
	if edits != deadlockAbortAt-1 {
		t.Fatalf("Phải giữ %d edit checkpoint khác nhau, thực tế %d", deadlockAbortAt-1, edits)
	}
	recs, err := st.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("decisions: %v", err)
	}
	var hasWorkerFailure, hasDeadlockWithCause bool
	for _, rec := range recs {
		switch rec.Kind {
		case "worker_failure":
			hasWorkerFailure = true
		case "deadlock":
			var facts arbiter.FailureFacts
			if err := json.Unmarshal(rec.Facts, &facts); err != nil {
				t.Fatalf("decode deadlock facts: %v", err)
			}
			if facts.ErrorKind == "canceled" && strings.Contains(facts.Error, "context canceled") {
				hasDeadlockWithCause = true
			}
		}
	}
	if !hasWorkerFailure || !hasDeadlockWithCause {
		t.Fatalf("Phải ghi worker_failure trước, deadlock phải giữ lỗi cuối cùng: %+v", recs)
	}
}

// TestEngine_PauseWithEditorDispatchWaitsForRewriteQueue xác minh sửa lỗi (chặn đánh giá 2):
// Phán quyết viết lại của Arbiter = điểm dừng + đưa editor vào hàng đợi. Điểm dừng phải đợi editor tạo hàng đợi viết lại,
// rồi đợi writer viết lại và làm rỗng hàng đợi mới được dùng—không được hiểu nhầm là "hàng đợi đã rỗng" trước khi editor chạy.
func TestEngine_PauseWithEditorDispatchWaitsForRewriteQueue(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Một", CoreEvent: "a"},
		{Chapter: 2, Title: "Hai", CoreEvent: "b"},
		{Chapter: 3, Title: "Ba", CoreEvent: "c"},
	}); err != nil {
		t.Fatalf("outline: %v", err)
	}
	// Chương 1 đã hoàn tất (sẽ bị viết lại); writer worker sẽ viết lại trước, rồi điểm dừng được dùng.
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatalf("start ch1: %v", err)
	}
	if err := st.Progress.MarkChapterComplete(1, 1200, "crisis", "quest"); err != nil {
		t.Fatalf("complete ch1: %v", err)
	}

	// editor: một lần save_review(verdict=rewrite, affected=[1]) đưa chương 1 vào hàng đợi.
	editorModel := &scriptedChatModel{fn: func(msgs []agentcore.Message) agentcore.Message {
		toolResults := 0
		for _, m := range msgs {
			if m.Role == agentcore.RoleTool {
				toolResults++
			}
		}
		if toolResults == 0 {
			return testToolCallMsg("save_review", map[string]any{
				"chapter": 1, "scope": "chapter",
				"dimensions": []map[string]any{
					{"dimension": "consistency", "score": 85, "comment": "Đạt yêu cầu (trích dẫn: nguyên văn)"},
					{"dimension": "character", "score": 85, "comment": "Đạt yêu cầu (trích dẫn: nguyên văn)"},
					{"dimension": "pacing", "score": 85, "comment": "Đạt yêu cầu (trích dẫn: nguyên văn)"},
					{"dimension": "continuity", "score": 85, "comment": "Đạt yêu cầu (trích dẫn: nguyên văn)"},
					{"dimension": "foreshadow", "score": 85, "comment": "Đạt yêu cầu (trích dẫn: nguyên văn)"},
					{"dimension": "hook", "score": 85, "comment": "Đạt yêu cầu (trích dẫn: nguyên văn)"},
					{"dimension": "aesthetic", "score": 55, "comment": "Giọng văn không phù hợp (trích dẫn: đoạn đầu nguyên văn)"},
				},
				"issues": []map[string]any{{
					"type": "aesthetic", "severity": "error", "description": "Giọng văn", "evidence": "Nguyên văn", "suggestion": "Viết lạnh hơn",
					"chapters": []int{1}, "requires_change": true,
				}},
				"contract_status": nil, "contract_misses": []string{}, "contract_notes": nil,
				"verdict": "rewrite", "summary": "Chương 1 cần viết lại giọng văn",
			})
		}
		return testTextMsg("done")
	}}
	editor := subagent.Config{
		Name: "editor", Description: "test editor", Model: editorModel,
		SystemPrompt: "test", MaxTurns: 6,
		Tools:          []agentcore.Tool{tools.NewSaveReviewTool(st)},
		StopAfterTools: []string{"save_review"},
	}
	writer := subagent.Config{
		Name: "writer", Description: "test writer", Model: scriptedWriterModel(),
		SystemPrompt: "test",
		Tools: []agentcore.Tool{
			tools.NewPlanChapterTool(st),
			tools.NewDraftChapterTool(st),
			tools.NewCheckConsistencyTool(st),
			tools.NewCommitChapterTool(st, tools.NewStyleStatsIndex(st)),
		},
		MaxTurns: 10, StopAfterTools: []string{"commit_chapter"},
	}

	e, _, done := newTestEngine(t, st, subagent.NewRunner(editor, writer), nil)
	// Mô phỏng phán quyết viết lại của Arbiter: hold + dispatch editor (Engine chưa chạy → áp dụng ngay).
	e.applyControlOp(context.Background(), controlOp{
		hold:     &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "Viết lại giọng văn chương 1, sửa xong tạm dừng để nghiệm thu"},
		dispatch: &arbiter.DispatchOp{Agent: "editor", Task: "Rà soát chương 1: làm giọng văn lạnh hơn, dùng issues[].chapters và requires_change để đưa vào hàng đợi"},
		facts:    mustInterventionFacts(t, st),
	})
	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %v", err)
	}
	// Khẳng định lõi ①: điểm dừng không được dùng trước khi editor đưa vào hàng đợi—chương 1 thật sự đã được viết lại
	// (commit viết lại sẽ drain nó khỏi hàng đợi).
	if len(progress.PendingRewrites) != 0 {
		t.Fatalf("Hàng đợi viết lại phải đã rỗng, got %v", progress.PendingRewrites)
	}
	if progress.ChapterWordCounts[1] == 1200 {
		t.Fatal("Chương 1 phải được viết lại thật (số chữ phải thay đổi)")
	}
	// Khẳng định lõi ②: điểm dừng được dùng sau khi hàng đợi rỗng, Engine tạm dừng—chương 2 không được viết tiếp.
	if len(progress.CompletedChapters) != 1 {
		t.Fatalf("Điểm dừng phải tạm dừng trước khi viết tiếp chương 2, completed=%v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta != nil && meta.AdvanceHold != nil {
		t.Fatalf("Tạm dừng một lần phải đã được dùng, got %+v", meta.AdvanceHold)
	}
}

// TestEngine_BoundaryHoldDoesNotDispatchAnotherWorker hồi quy:
// khi can thiệp người dùng chỉ phán ra boundary hold (không điều phối), Engine phải dùng hold
// và tạm dừng ngay tại biên hiện tại, không được viết thêm một chương.
func TestEngine_BoundaryHoldDoesNotDispatchAnotherWorker(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Một", CoreEvent: "a"},
		{Chapter: 2, Title: "Hai", CoreEvent: "b"},
		{Chapter: 3, Title: "Ba", CoreEvent: "c"},
	}); err != nil {
		t.Fatalf("outline: %v", err)
	}

	writer := subagent.Config{
		Name: "writer", Description: "test writer", Model: scriptedWriterModel(),
		SystemPrompt: "test",
		Tools: []agentcore.Tool{
			tools.NewPlanChapterTool(st),
			tools.NewDraftChapterTool(st),
			tools.NewCheckConsistencyTool(st),
			tools.NewCommitChapterTool(st, tools.NewStyleStatsIndex(st)),
		},
		MaxTurns: 10, StopAfterTools: []string{"commit_chapter"},
	}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), nil)
	if !e.start(nil) {
		t.Fatal("engine start")
	}
	// Can thiệp hold-only đến trong lúc viết chương 1 (khớp trình tự Steer thực tế).
	e.enqueue(controlOp{
		hold:  &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, Reason: "Dừng lại một chút để tôi xem"},
		facts: mustInterventionFacts(t, st),
	})
	waitEngineDone(t, done)

	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %v", err)
	}
	// Can thiệp đến khi chương 1 đang chạy → chương 1 viết xong; điểm dừng được dùng ngay tại biên → chương 2 không được bắt đầu.
	if n := len(progress.CompletedChapters); n > 1 {
		t.Fatalf("Sau boundary hold không được viết thêm chương nào, completed=%v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta != nil && meta.AdvanceHold != nil {
		t.Fatalf("Tạm dừng một lần phải đã được dùng, got %+v", meta.AdvanceHold)
	}
}

func TestEngine_TargetChapterHoldStopsAtRequestedChapter(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Một", CoreEvent: "a"},
		{Chapter: 2, Title: "Hai", CoreEvent: "b"},
		{Chapter: 3, Title: "Ba", CoreEvent: "c"},
	}); err != nil {
		t.Fatal(err)
	}

	writer := subagent.Config{
		Name: "writer", Description: "test writer", Model: scriptedWriterModel(), SystemPrompt: "test",
		Tools: []agentcore.Tool{
			tools.NewPlanChapterTool(st), tools.NewDraftChapterTool(st),
			tools.NewCheckConsistencyTool(st), tools.NewCommitChapterTool(st, tools.NewStyleStatsIndex(st)),
		},
		MaxTurns: 10, StopAfterTools: []string{"commit_chapter"},
	}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), nil)
	if err := st.RunMeta.SetAdvanceHold(domain.AdvanceHold{
		After: domain.AdvanceHoldAtChapter, TargetChapter: 2, Reason: "Viết đến chương 2",
	}); err != nil {
		t.Fatal(err)
	}
	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %v", err)
	}
	if !slices.Equal(progress.CompletedChapters, []int{1, 2}) {
		t.Fatalf("Phải dừng chính xác ở chương 2, completed=%v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvanceHold != nil {
		t.Fatalf("Hold theo chương mục tiêu phải đã được dùng: %+v", meta.AdvanceHold)
	}
}

// TestEngine_ExitRaceRestoresPendingDispatch hồi quy (chặn đánh giá 3):
// khi can thiệp vào hàng đợi đua với Engine thoát, điều phối phán quyết còn dư không được mất im lặng—
// PendingSteer phải được lưu lại, và hành động dữ kiện kiểu pause phải được thực thi bù.
func TestEngine_ExitRaceRestoresPendingDispatch(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(2); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}

	// worker treo đến khi ctx bị hủy: tạo cửa sổ "đã vào hàng đợi rồi Engine bị abort".
	blocked := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		time.Sleep(50 * time.Millisecond)
		return testTextMsg("...")
	}}
	writer := subagent.Config{Name: "writer", Description: "slow", Model: blocked, SystemPrompt: "t", MaxTurns: 100}
	// Cần outline để Route điều phối writer.
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Một", CoreEvent: "a"}, {Chapter: 2, Title: "Hai", CoreEvent: "b"}}); err != nil {
		t.Fatalf("outline: %v", err)
	}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), nil)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	// Khi worker đang chạy: đưa pause+dispatch vào hàng đợi rồi abort ngay (hành động sẽ không bao giờ chờ được biên kế tiếp).
	e.enqueue(controlOp{
		hold:     &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "Nghiệm thu"},
		dispatch: &arbiter.DispatchOp{Agent: "writer", Task: "Viết lại chương 1"},
		text:     "Viết lại chương 1 rồi dừng lại",
		facts:    mustInterventionFacts(t, st),
	})
	e.abort()
	waitEngineDone(t, done)

	meta, err := st.RunMeta.Load()
	if err != nil || meta == nil {
		t.Fatalf("load meta: %v", err)
	}
	if meta.PendingSteer != "Viết lại chương 1 rồi dừng lại" {
		t.Fatalf("Điều phối còn dư phải được lưu vào PendingSteer để phát lại khi khôi phục, got %q", meta.PendingSteer)
	}
	if meta.AdvanceHold == nil {
		t.Fatal("Hành động dữ kiện hold phải được thực thi bù trong cleanup khi thoát")
	}
}
