package host

// Kiểm thử tích hợp đầu-cuối của Engine (engine-rfc.md §7 nghiệm thu prototype):
// store thật + công cụ Worker thật + ChatModel kịch bản hóa, xác minh
//  1. Chuỗi viết sách hoàn chỉnh do Route điều phối: viết chương 1 → viết chương 2 → hoàn tất sách → engine tự dừng tự nhiên
//  2. Nhánh Worker thất bại: thử lại một lần → Arbiter phán quyết worker_failure là abort → tạm dừng + ghi audit xuống đĩa
//  3. Nhánh bế tắc: cùng chỉ thị không có tiến triển ×3 → Arbiter phán quyết deadlock → ghi audit xuống đĩa → abort dừng máy

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

// scriptedChatModel là ChatModel tối thiểu sinh phản hồi theo callback.
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
		t.Fatalf("phải giữ lại loại lỗi và các sự kiện tiến độ có thể đọc được: %+v", facts)
	}
	if len(facts.FactWarnings) == 0 {
		t.Fatalf("các sự kiện nền tảng không đọc được phải được chuyển cho Arbiter dưới dạng cảnh báo: %+v", facts)
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
	const task = "Kiểm tra nội dung lặp lại và sắp xếp làm lại cần thiết"
	const original = "  Về sau không lặp lại giải thích nguồn gốc năng lực; không sửa nội dung không liên quan.\n"

	got := interventionDispatchTask(task, original)
	if !strings.Contains(got, task) {
		t.Fatalf("mất nhiệm vụ dispatch: %q", got)
	}
	if !strings.Contains(got, original) {
		t.Fatalf("can thiệp gốc của người dùng chưa được giữ nguyên từng chữ: %q", got)
	}
	if !strings.Contains(got, "nguồn ủy quyền duy nhất cho lần sửa đổi này") {
		t.Fatalf("thiếu mô tả ranh giới thẩm quyền: %q", got)
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

type editThenCancelModel struct{ edits atomic.Int32 }

func (m *editThenCancelModel) Generate(_ context.Context, msgs []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if len(msgs) > 0 && msgs[len(msgs)-1].Role == agentcore.RoleTool {
		return nil, context.Canceled
	}
	n := int(m.edits.Add(1))
	return &agentcore.LLMResponse{Message: testToolCallMsg("edit_chapter", map[string]any{"chapter": 1, "old_string": fmt.Sprintf("Bản%d", n-1), "new_string": fmt.Sprintf("Bản%d", n)})}, nil
}

func (m *editThenCancelModel) GenerateStream(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	r, e := m.Generate(ctx, msgs, tools, opts...)
	if e != nil {
		return nil, e
	}
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: r.Message, StopReason: r.Message.StopReason}
	close(ch)
	return ch, nil
}

func (m *editThenCancelModel) SupportsTools() bool { return true }

type providerNetworkModel struct{ calls atomic.Int32 }

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
	return agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{ID: "tc-" + name, Name: name, Args: data})}, StopReason: agentcore.StopReasonToolUse}
}

func testTextMsg(text string) agentcore.Message {
	return agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock(text)}, StopReason: agentcore.StopReasonStop}
}

var chapterRe = regexp.MustCompile(`(?i)(?:viết(?: lại)?|viet(?: lai)?|trau chuốt|trau chuot) ch(?:ương|uong) (\d+)`)

func scriptedWriterModel() *scriptedChatModel {
	return &scriptedChatModel{fn: func(msgs []agentcore.Message) agentcore.Message {
		chapter, toolResults := 0, 0
		for _, msg := range msgs {
			if msg.Role == agentcore.RoleUser {
				if match := chapterRe.FindStringSubmatch(msg.TextContent()); match != nil {
					chapter, _ = strconv.Atoi(match[1])
				}
			}
			if msg.Role == agentcore.RoleTool {
				toolResults++
			}
		}
		switch toolResults {
		case 0:
			return testToolCallMsg("plan_chapter", map[string]any{
				"chapter":  chapter,
				"title":    fmt.Sprintf("Chương %d", chapter),
				"goal":     "Đẩy mạch chính",
				"conflict": "Nhân vật chính gặp trở ngại",
				"hook":     "Khép lại bằng nút thắt",
			})
		case 1:
			return testToolCallMsg("draft_chapter", map[string]any{
				"chapter": chapter,
				"mode":    "write",
				"content": strings.Repeat(fmt.Sprintf("Đoạn văn Chương %d, nhân vật chính lần bước trong bóng tối.", chapter), 20),
			})
		case 2:
			return testToolCallMsg("check_consistency", map[string]any{"chapter": chapter})
		default:
			return testToolCallMsg("commit_chapter", map[string]any{
				"chapter":              chapter,
				"title":                fmt.Sprintf("Chương %d", chapter),
				"summary":              fmt.Sprintf("Tóm tắt Chương %d", chapter),
				"characters":           []string{"Nhân vật chính"},
				"key_events":           []string{"Đẩy mạch truyện"},
				"timeline_events":      []any{},
				"foreshadow_updates":   []any{},
				"relationship_changes": []any{},
				"state_changes":        []any{},
				"cast_intros":          []any{},
				"hook_type":            "crisis",
				"dominant_strand":      "quest",
				"feedback":             nil,
			})
		}
	}}
}

func newTestEngine(t *testing.T, st *storepkg.Store, workers *subagent.Runner, arbiterModel agentcore.ChatModel) (*engine, *[]Event, chan struct{}) {
	t.Helper()
	if err := st.RunMeta.Init("default", "test", "test"); err != nil {
		t.Fatalf("init run meta: %v", err)
	}
	var mu sync.Mutex
	events := &[]Event{}
	done := make(chan struct{}, 1)
	obs := newObserver(st, func(ev Event) { mu.Lock(); *events = append(*events, ev); mu.Unlock() }, func(string) {}, func() {})
	e := &engine{
		store: st, workers: workers, arbiterModel: arbiterModel,
		failurePrompt: "sys", planStartPrompt: "sys", style: "default", observer: obs,
		refresh:   func() {},
		emitEvent: func(ev Event) { mu.Lock(); *events = append(*events, ev); mu.Unlock() },
		notify:    func(string, string, string, string) {},
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
		t.Fatal("engine không dừng trong thời hạn")
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
		{Chapter: 1, Title: "Mot", CoreEvent: "a"},
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
		t.Fatalf("một giấy phép phải ổn định đúng một chương mới: %v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvancePermitChapter != 0 {
		t.Fatalf("sau khi commit ổn định, giấy phép phải được tiêu thụ: %+v", meta)
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
		hold:     &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, Reason: "tạm dừng trước"},
		dispatch: &arbiter.DispatchOp{Agent: "editor", Task: "nhiệm vụ quá hạn"},
		facts:    arbiter.InterventionFacts{Phase: string(domain.PhaseOutline)},
	}}

	if e.applyPendingOps(context.Background()) {
		t.Fatal("dispatch ghép cặp có facts quá hạn không được vượt qua Gate khi chưa rơi vào next")
	}
	if e.next != nil || e.deferGateForNext {
		t.Fatalf("dispatch quá hạn không được để lại chỉ thị có thể thực thi: next=%+v defer=%v", e.next, e.deferGateForNext)
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvanceHold != nil {
		t.Fatalf("khi dispatch ghép cặp quá hạn không được để lại hold đơn lẻ: %+v", meta.AdvanceHold)
	}
	if e.gate.HandleBoundary() {
		t.Fatal("khi không có hold đơn lẻ, Gate không nên giả tạo tạm dừng")
	}
}

// TestEngine_WritesBookToCompletion chuỗi hoàn chỉnh: sách hai chương không phân tầng viết từ writing đến complete.
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
		{Chapter: 1, Title: "Chuong mot", CoreEvent: "Khoi dau"},
		{Chapter: 2, Title: "Chuong hai", CoreEvent: "Ket cuc"},
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
		t.Fatalf("viết đủ hai chương thì phải hoàn tất sách, got phase=%s completed=%v", progress.Phase, progress.CompletedChapters)
	}
	if len(progress.CompletedChapters) != 2 {
		t.Fatalf("phải hoàn thành 2 chương, got %v", progress.CompletedChapters)
	}
	// Hình dạng sự kiện: mỗi chương một DISPATCH (do engine khởi phát), các dòng TOOL đến từ relay tiến độ
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
		t.Fatalf("phải có ít nhất 2 sự kiện DISPATCH, got %d", dispatches)
	}
	if toolRows == 0 {
		t.Fatal("tiến độ công cụ Worker chưa được relay chiếu ra (thiếu dòng TOOL)")
	}
}

// TestEngine_WorkerFailureConsultsArbiterAndAborts nhánh thất bại:
// writer chạy rỗng được StopGuard nâng cấp → thử lại một lần → Arbiter phán quyết abort → tạm dừng + audit.
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
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Mot", CoreEvent: "s"}}); err != nil {
		t.Fatalf("outline: %v", err)
	}

	var runs atomic.Int32
	// writer mỗi vòng chỉ trả văn bản mà không ghi đĩa → guard.NewWriterStopGuard chặn liên tục rồi nâng cấp → Execute báo lỗi
	idle := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg("Tôi đã viết xong (thực ra chưa làm gì)")
	}}
	writer := subagent.Config{
		Name: "writer", Description: "idle writer",
		Model: idle, SystemPrompt: "test", MaxTurns: 20,
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			runs.Add(1)
			return failNTimesGuard()
		},
	}
	// Arbiter phán quyết abort
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"writer liên tục chạy rỗng, nên kiểm tra thủ công cấu hình model"}`)
	}}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	if got := runs.Load(); got != 2 {
		t.Fatalf("thất bại đầu tiên phải thử lại một lần (tổng cộng 2 lần spawn), got %d", got)
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
				t.Fatalf("nội dung phán quyết phải chứa abort: %s", r.Decision)
			}
		}
	}
	if !found {
		t.Fatalf("phán quyết worker_failure phải được ghi xuống đĩa: %+v", recs)
	}
}

// seedStuckRewrite tạo hiện trường "chương 2 đã hoàn tất và được xếp vào hàng đợi làm lại".
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
	if err := st.Progress.SetPendingRewrites([]int{2}, "đánh giá yêu cầu viết lại"); err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := st.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("flow: %v", err)
	}
}

// TestEngine_DeadlockAbortDropsStuckRewrite khóa chặt bề mặt deadlock của issue #110:
// khi bế tắc bị cầu chì ngắt, chương làm lại bị kẹt phải ra khỏi hàng đợi. PendingRewrites là sự kiện được bền vững hóa;
// nếu chỉ tạm dừng mà không ra khỏi hàng đợi, khởi động lại sẽ lập tức phát lại cùng một chỉ thị chết,
// khóa vĩnh viễn cả cuốn sách.
func TestEngine_DeadlockAbortDropsStuckRewrite(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	seedStuckRewrite(t, st)
	e, events, _ := newTestEngine(t, st, subagent.NewRunner(), nil)

	inst := &flow.Instruction{Agent: "writer", Task: "viết lại chương 2", Chapter: 2}
	e.lastKey, e.repeats = instructionKey(inst), deadlockAbortAt-1

	if stop := e.trackDeadlock(context.Background(), &inst); !stop {
		t.Fatal("cầu chì deadlock vẫn phải dừng máy chờ can thiệp thủ công")
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("progress: %v", err)
	}
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("khi cầu chì ngắt, chương làm lại bị kẹt phải ra khỏi hàng đợi: %v", p.PendingRewrites)
	}
	if p.Flow != domain.FlowWriting {
		t.Fatalf("sau khi hàng đợi trống, flow phải trở về writing, thực tế %s", p.Flow)
	}
	var notified bool
	for _, ev := range *events {
		if strings.Contains(ev.Summary, "chương 2 đã được lấy ra khỏi hàng đợi sửa lại") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("bỏ qua làm lại phải thông báo rõ cho người dùng: %+v", *events)
	}
}

// TestEngine_DropStuckRewriteOnlyTouchesQueuedChapter ra khỏi hàng đợi là thao tác phá hủy, phạm vi gây hại nhầm phải được đóng đinh:
// chỉ "chương đang nằm trong hàng đợi làm lại" mới được đưa ra, các chỉ thị khác tuyệt đối không động vào hàng đợi.
func TestEngine_DropStuckRewriteOnlyTouchesQueuedChapter(t *testing.T) {
	cases := []struct {
		name string
		inst *flow.Instruction
	}{
		{"chỉ thị không phải writer", &flow.Instruction{Agent: "editor", Task: "đánh giá cấp arc"}},
		{"chỉ thị writer không liên quan chương", &flow.Instruction{Agent: "writer", Task: "viết tiếp"}},
		{"chương viết tiếp không nằm trong hàng đợi", &flow.Instruction{Agent: "writer", Task: "viết chương 3", Chapter: 3}},
		{"chỉ thị rỗng", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := storepkg.NewStore(t.TempDir())
			seedStuckRewrite(t, st)
			e, _, _ := newTestEngine(t, st, subagent.NewRunner(), nil)
			if e.dropStuckRewrite(tc.inst) {
				t.Fatal("không nên ra khỏi hàng đợi")
			}
			p, err := st.Progress.Load()
			if err != nil {
				t.Fatalf("progress: %v", err)
			}
			if len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 2 {
				t.Fatalf("hàng đợi làm lại không được bị tác động nhầm: %v", p.PendingRewrites)
			}
		})
	}
}

// TestEngine_TransientProviderFailuresDoNotBecomeDeadlock hồi quy chuỗi lỗi chương 135:
// worker_failure=retry sau hai vòng lỗi mạng không được bị trackDeadlock ở vòng kế tiếp xem là
// "cùng một nhiệm vụ viết liên tục không có tiến triển" và kích hoạt deadlock để đổi dispatch.
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
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Chương một", CoreEvent: "Khởi đầu"}}); err != nil {
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
			return testTextMsg(`{"action":"retry","dispatch":null,"reason":"lỗi mạng nhất thời, thử lại nhiệm vụ gốc"}`)
		}
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"mạng tiếp tục không khả dụng, tạm dừng chờ khôi phục"}`)
	}}
	e, events, done := newTestEngine(t, st, subagent.NewRunner(writer), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	if got := network.calls.Load(); got != 4 {
		t.Fatalf("hai chu kỳ thất bại Engine, mỗi chu kỳ thực thi Worker 2 lần, got %d", got)
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
		t.Fatalf("lỗi mạng chỉ được đi vào worker_failure, got worker_failure=%d deadlock=%d records=%+v", workerFailures, deadlocks, recs)
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
		t.Fatalf("mỗi lần Worker thất bại chỉ nên cập nhật DISPATCH, got dispatch=%d duplicate_error=%d events=%+v", failedDispatches, duplicateErrors, *events)
	}
}

// failNTimesGuard StopGuard nâng cấp ngay lập tức (mô phỏng cầu chì chạy rỗng).
func failNTimesGuard() agentcore.StopGuard {
	return func(context.Context, agentcore.StopInfo) agentcore.StopDecision {
		return agentcore.StopDecision{Allow: false, Escalate: true}
	}
}

// TestEngine_RetriesUnfinishedPlanStart nhánh tự phục hồi sau khi phán quyết khởi động thất bại:
// StartPrompt đã ghi đĩa, PlanStart vắng mặt (model lỗi lúc khởi động) → engine khởi động thì bổ sung phán quyết tại hiện trường
// → cố định PlanStartRecord → dispatch planner.
// Planner không ghi đĩa → đi theo nhánh deadlock sẵn có để dừng máy, chứng minh engine trở về quỹ đạo bình thường sau bổ sung phán quyết.
func TestEngine_RetriesUnfinishedPlanStart(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatalf("progress: %v", err)
	}
	// Mô phỏng hiện trường StartPrepared thất bại: sự kiện đầu vào có, sự kiện phán quyết vắng mặt.
	if err := st.RunMeta.SetStartPrompt("người thường tu tiên"); err != nil {
		t.Fatalf("start prompt: %v", err)
	}

	// Arbiter: lần gọi đầu là bổ sung phán quyết (plan_start), sau đó là tư vấn deadlock (abort kết thúc).
	var arbCalls atomic.Int32
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		if arbCalls.Add(1) == 1 {
			return testTextMsg(`{"planner":"architect_long","task":"lập khung ba quyển xoay quanh người thường tu tiên","reason":"đề tài tu tiên dài tập"}`)
		}
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"planner chạy rỗng, dừng máy"}`)
	}}
	// Planner trả về thành công nhưng không ghi bất kỳ thứ gì xuống đĩa → Route luôn trả cùng một chỉ thị bổ sung → deadlock.
	architect := subagent.Config{
		Name: "architect_long", Description: "idle planner",
		Model: &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
			return testTextMsg("Đã lập kế hoạch (thực ra chưa ghi đĩa)")
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
		t.Fatalf("sau khi bổ sung phán quyết, PlanStart phải được cố định, meta=%+v err=%v", meta, err)
	}
	if meta.PlanStart.Planner != "architect_long" || meta.PlanStart.RawPrompt != "người thường tu tiên" || meta.PlanStart.DecisionID == "" {
		t.Fatalf("các trường PlanStartRecord không đầy đủ: %+v", meta.PlanStart)
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
		t.Fatalf("bổ sung phán quyết phải để lại audit plan_start: %+v", recs)
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
		t.Fatalf("sau khi bổ sung phán quyết phải dispatch planner và hiển thị sự kiện bổ sung, dispatched=%v healed=%v", dispatched, healed)
	}
}

// TestEngine_PlanStartRetryFailurePauses bổ sung phán quyết thất bại không được phép dừng máy âm thầm:
// Arbiter liên tục không khả dụng → hiển thị tạm dừng rõ ràng + audit plan_start có error + không dispatch.
func TestEngine_PlanStartRetryFailurePauses(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.RunMeta.SetStartPrompt("người thường tu tiên"); err != nil {
		t.Fatalf("start prompt: %v", err)
	}

	var e *engine
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		e.abort() // Mô phỏng host hủy lời gọi liên tục thất bại, nhánh thất bại kết thúc rõ ràng bằng context.
		return testTextMsg("Đây không phải JSON")
	}}
	e, events, done := newTestEngine(t, st, subagent.NewRunner(), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	for _, ev := range *events {
		if ev.Category == "DISPATCH" {
			t.Fatal("bổ sung phán quyết thất bại không được dispatch bất kỳ worker nào")
		}
	}
	var paused bool
	for _, ev := range *events {
		if strings.Contains(ev.Summary, "Phán quyết khởi động thất bại, đã tạm dừng") {
			paused = true
		}
	}
	if !paused {
		t.Fatalf("bổ sung phán quyết thất bại phải hiển thị rõ lý do tạm dừng, events=%+v", *events)
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
		t.Fatalf("phán quyết thất bại phải ghi xuống đĩa kèm error: %+v", recs)
	}
}

// TestEngine_DeadlockConsultsArbiter nhánh bế tắc: chỉ thị bổ sung quy hoạch lặp lại liên tục
// → lần thứ 3 tư vấn Arbiter → abort dừng máy + audit deadlock.
func TestEngine_DeadlockConsultsArbiter(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(3); err != nil {
		t.Fatalf("progress: %v", err)
	}
	// Giai đoạn quy hoạch + tier đã biết + hạng mục thiếu luôn tồn tại → Route mỗi vòng sinh cùng một chỉ thị bổ sung
	if err := st.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("tier: %v", err)
	}

	// architect không có guard, trả về thành công nhưng không ghi gì xuống đĩa → chỉ thị Route không đổi
	lazy := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg("Đã biết (không làm gì)")
	}}
	architect := subagent.Config{
		Name: "architect_long", Description: "lazy architect",
		Model: lazy, SystemPrompt: "test", MaxTurns: 5,
	}
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg(`{"action":"abort","dispatch":null,"reason":"planner lặp lại nhiều lần không có đầu ra"}`)
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
		t.Fatalf("phán quyết deadlock phải được ghi xuống đĩa: %+v", recs)
	}
}

// TestEngine_IntermediateCheckpointsDoNotMaskDeadlock khóa #84: Writer liên tục sửa
// bản nháp sẽ sinh digest mới và edit checkpoint mới, nhưng chỉ cần Route vẫn là cùng một
// "trau chuốt chương 1" thì nghĩa là hậu điều kiện cấp Engine (commit) chưa hoàn tất, phải tiếp tục tích lũy deadlock.
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
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Chuong mot", CoreEvent: "Khoi dau"}}); err != nil {
		t.Fatalf("outline: %v", err)
	}
	if err := st.Drafts.SaveDraft(1, "Bản0"); err != nil {
		t.Fatalf("draft: %v", err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune("Bản0")), "mystery", "quest"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := st.Progress.SetPendingRewrites([]int{1}, "kiểm thử trau chuốt không submit"); err != nil {
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
	// Ngay cả khi Arbiter luôn yêu cầu retry cho worker_failure / deadlock, lần cầu chì cứng thứ 5 hiện có
	// cũng phải chặn trước khi dispatch, không được bị edit checkpoint reset.
	arb := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		return testTextMsg(`{"action":"retry","dispatch":null,"reason":"tiếp tục thử lại"}`)
	}}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), arb)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	waitEngineDone(t, done)

	if got := writerModel.edits.Load(); got != deadlockAbortAt-1 {
		t.Fatalf("deadlock phải cầu chì cứng trước lần dispatch thứ %d, thực tế edit %d lần", deadlockAbortAt, got)
	}
	var edits int
	for _, cp := range st.Checkpoints.All() {
		if cp.Scope.Matches(domain.ChapterScope(1)) && cp.Step == "edit" {
			edits++
		}
	}
	if edits != deadlockAbortAt-1 {
		t.Fatalf("phải giữ lại %d edit checkpoint khác nhau, thực tế %d", deadlockAbortAt-1, edits)
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
		t.Fatalf("phải ghi worker_failure trước, deadlock phải giữ lỗi cuối cùng: %+v", recs)
	}
}

// TestEngine_PauseWithEditorDispatchWaitsForRewriteQueue xác minh sửa lỗi (chặn đánh giá 2):
// Phán quyết làm lại của Arbiter = điểm dừng + dispatch editor vào hàng đợi. Điểm dừng phải chờ editor thiết lập hàng đợi làm lại,
// rồi writer viết lại xả hết hàng đợi mới tiêu thụ -- không được tiêu thụ nhầm trước khi editor chạy vì "hàng đợi đã trống".
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
		{Chapter: 1, Title: "Mot", CoreEvent: "a"},
		{Chapter: 2, Title: "Hai", CoreEvent: "b"},
		{Chapter: 3, Title: "Ba", CoreEvent: "c"},
	}); err != nil {
		t.Fatalf("outline: %v", err)
	}
	// Chương 1 đã hoàn tất (sẽ bị làm lại); writer worker sẽ viết lại nó trước, rồi điểm dừng được tiêu thụ.
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
					{"dimension": "consistency", "score": 85, "comment": "dat yeu cau (trich dan: van ban goc)"},
					{"dimension": "character", "score": 85, "comment": "dat yeu cau (trich dan: van ban goc)"},
					{"dimension": "pacing", "score": 85, "comment": "dat yeu cau (trich dan: van ban goc)"},
					{"dimension": "continuity", "score": 85, "comment": "dat yeu cau (trich dan: van ban goc)"},
					{"dimension": "foreshadow", "score": 85, "comment": "dat yeu cau (trich dan: van ban goc)"},
					{"dimension": "hook", "score": 85, "comment": "dat yeu cau (trich dan: van ban goc)"},
					{"dimension": "aesthetic", "score": 55, "comment": "giong dieu khong phu hop (trich dan: doan dau van ban goc)"},
				},
				"issues": []map[string]any{{
					"type": "aesthetic", "severity": "error", "description": "giong dieu", "evidence": "van ban goc", "suggestion": "doi sang lanh hon",
					"chapters": []int{1}, "requires_change": true,
				}},
				"contract_status": nil, "contract_misses": []string{}, "contract_notes": nil,
				"verdict": "rewrite", "summary": "chuong 1 can viet lai giong dieu",
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
	// Mô phỏng Arbiter phán quyết làm lại: hold + dispatch editor (engine chưa chạy → áp dụng ngay).
	e.applyControlOp(context.Background(), controlOp{
		hold:     &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "viết lại giọng điệu chương 1, sửa xong tạm dừng nghiệm thu"},
		dispatch: &arbiter.DispatchOp{Agent: "editor", Task: "kiểm tra lại chương 1: đổi giọng điệu sang lạnh hơn, dùng issues[].chapters và requires_change để đưa vào hàng đợi"},
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
	// Khẳng định lõi 1: điểm dừng không bị tiêu thụ trước khi editor đưa vào hàng đợi -- chương 1 thật sự đã trải qua viết lại
	// (rewrite commit sẽ drain nó khỏi hàng đợi).
	if len(progress.PendingRewrites) != 0 {
		t.Fatalf("hàng đợi làm lại phải đã trống, got %v", progress.PendingRewrites)
	}
	if progress.ChapterWordCounts[1] == 1200 {
		t.Fatal("chương 1 phải được viết lại thật (số từ phải thay đổi)")
	}
	// Khẳng định lõi 2: sau khi xả hết hàng đợi thì tiêu thụ điểm dừng, engine tạm dừng -- chương 2 không nên được viết tiếp.
	if len(progress.CompletedChapters) != 1 {
		t.Fatalf("điểm dừng phải tạm dừng trước khi viết tiếp chương 2, completed=%v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta != nil && meta.AdvanceHold != nil {
		t.Fatalf("tạm dừng một lần phải đã được tiêu thụ, got %+v", meta.AdvanceHold)
	}
}

// TestEngine_BoundaryHoldDoesNotDispatchAnotherWorker hồi quy:
// Khi can thiệp người dùng chỉ phán quyết ra boundary hold (không dispatch), engine phải lập tức
// tiêu thụ hold ở ranh giới hiện tại và tạm dừng, không được viết thêm một chương nữa.
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
		{Chapter: 1, Title: "Mot", CoreEvent: "a"},
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
	// hold-only intervention đến trong khi đang viết chương 1 (khớp thứ tự thời gian Steer thật).
	e.enqueue(controlOp{
		hold:  &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, Reason: "tạm dừng một chút để tôi xem"},
		facts: mustInterventionFacts(t, st),
	})
	waitEngineDone(t, done)

	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		t.Fatalf("load progress: %v", err)
	}
	// Can thiệp đến trong lúc chương 1 đang chạy → chương 1 viết xong; điểm dừng tiêu thụ ngay tại ranh giới → chương 2 không được bắt đầu viết.
	if n := len(progress.CompletedChapters); n > 1 {
		t.Fatalf("sau boundary hold không được viết thêm một chương, completed=%v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta != nil && meta.AdvanceHold != nil {
		t.Fatalf("tạm dừng một lần phải đã được tiêu thụ, got %+v", meta.AdvanceHold)
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
		{Chapter: 1, Title: "Mot", CoreEvent: "a"},
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
		After: domain.AdvanceHoldAtChapter, TargetChapter: 2, Reason: "viết đến chương 2",
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
		t.Fatalf("phải dừng chính xác ở chương 2, completed=%v", progress.CompletedChapters)
	}
	meta, _ := st.RunMeta.Load()
	if meta.AdvanceHold != nil {
		t.Fatalf("hold chương mục tiêu phải đã được tiêu thụ: %+v", meta.AdvanceHold)
	}
}

// TestEngine_ExitRaceRestoresPendingDispatch hồi quy (chặn đánh giá 3):
// Khi can thiệp vào hàng đợi và engine thoát xảy ra race, dispatch phán quyết còn sót không được bị âm thầm bỏ mất -- PendingSteer phải được ghi lại,
// các hành động sự kiện kiểu pause phải được thực thi bù.
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

	// worker treo cho đến khi ctx bị hủy: tạo cửa sổ "sau khi vào hàng đợi thì engine bị abort".
	blocked := &scriptedChatModel{fn: func([]agentcore.Message) agentcore.Message {
		time.Sleep(50 * time.Millisecond)
		return testTextMsg("...")
	}}
	writer := subagent.Config{Name: "writer", Description: "slow", Model: blocked, SystemPrompt: "t", MaxTurns: 100}
	// Cần outline để Route dispatch writer
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Mot", CoreEvent: "a"}, {Chapter: 2, Title: "Hai", CoreEvent: "b"}}); err != nil {
		t.Fatalf("outline: %v", err)
	}
	e, _, done := newTestEngine(t, st, subagent.NewRunner(writer), nil)

	if !e.start(nil) {
		t.Fatal("engine start")
	}
	// worker đang chạy: enqueue pause+dispatch, ngay sau đó abort (hành động sẽ không bao giờ đợi được ranh giới tiếp theo).
	e.enqueue(controlOp{
		hold:     &arbiter.AdvanceHoldOp{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "nghiem thu"},
		dispatch: &arbiter.DispatchOp{Agent: "writer", Task: "viết lại chương 1"},
		text:     "viết lại chương 1 rồi dừng lại",
		facts:    mustInterventionFacts(t, st),
	})
	e.abort()
	waitEngineDone(t, done)

	meta, err := st.RunMeta.Load()
	if err != nil || meta == nil {
		t.Fatalf("load meta: %v", err)
	}
	if meta.PendingSteer != "viết lại chương 1 rồi dừng lại" {
		t.Fatalf("dispatch còn sót phải ghi lại PendingSteer để khôi phục phát lại, got %q", meta.PendingSteer)
	}
	if meta.AdvanceHold == nil {
		t.Fatal("hành động facts của hold phải được thực thi bù trong dọn dẹp khi thoát")
	}
}
