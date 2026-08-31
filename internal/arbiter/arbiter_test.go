package arbiter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// scriptedModel trả văn bản dựng sẵn theo số thứ tự gọi.
type scriptedModel struct {
	outputs        []string
	idx            int64
	lastCfg        agentcore.CallConfig
	lastMsgs       []agentcore.Message
	rejectThinking bool
	cancel         context.CancelFunc
	cancelAt       int
}

func (m *scriptedModel) take() string {
	i := int(atomic.AddInt64(&m.idx, 1) - 1)
	if m.cancel != nil && m.cancelAt > 0 && i+1 >= m.cancelAt {
		m.cancel()
	}
	if i >= len(m.outputs) {
		return m.outputs[len(m.outputs)-1]
	}
	return m.outputs[i]
}

func (m *scriptedModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.lastCfg = agentcore.ResolveCallConfig(opts)
	m.lastMsgs = messages
	if m.rejectThinking && m.lastCfg.ThinkingLevel != agentcore.ThinkingAuto {
		return nil, errors.New("thinking chỉ được hỗ trợ bởi mô hình chat suy luận")
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.take())},
	}}, nil
}

func TestDecidePlanStartDoesNotSendThinkingToChatModel(t *testing.T) {
	m := &scriptedModel{outputs: []string{
		`{"planner":"architect_short","task":"Lập kế hoạch truyện ngắn","reason":"Dung lượng khá ngắn"}`,
	}, rejectThinking: true}
	if _, err := DecidePlanStart(t.Context(), m, "sys", "Viết một truyện ngắn", ""); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if m.lastCfg.ThinkingLevel != agentcore.ThinkingAuto {
		t.Fatalf("Arbiter không nên gửi tham số thinking cho mô hình chat thường, nhận %q", m.lastCfg.ThinkingLevel)
	}
	if m.lastCfg.MaxTokens != decideMaxTokens {
		t.Fatalf("max_tokens = %d, cần %d", m.lastCfg.MaxTokens, decideMaxTokens)
	}
}

func TestDecidePromptContractAppendsSchema(t *testing.T) {
	m := &scriptedModel{outputs: []string{
		`{"planner":"architect_short","task":"Lập kế hoạch truyện ngắn","reason":"Dung lượng khá ngắn"}`,
	}}
	const semanticPrompt = "Chỉ dựa vào yêu cầu để quyết định cách lập kế hoạch."
	if _, err := DecidePlanStart(t.Context(), m, semanticPrompt, "Viết một truyện ngắn", ""); err != nil {
		t.Fatal(err)
	}
	got := m.lastMsgs[0].TextContent()
	for _, want := range []string{semanticPrompt, "<output-json-schema>", `"planner"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt contract thiếu %q:\n%s", want, got)
		}
	}
}

func (m *scriptedModel) GenerateStream(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	resp, _ := m.Generate(ctx, msgs, tools, opts...)
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message}
	close(ch)
	return ch, nil
}

func (m *scriptedModel) SupportsTools() bool { return true }

type retryableTestError struct {
	retryable bool
}

func (e retryableTestError) Error() string             { return "provider không khả dụng" }
func (e retryableTestError) Retryable() bool           { return e.retryable }
func (e retryableTestError) RetryAfter() time.Duration { return time.Millisecond }

type failingThenValidModel struct {
	failures int64
	calls    int64
}

func (m *failingThenValidModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	call := atomic.AddInt64(&m.calls, 1)
	if call <= m.failures {
		return nil, retryableTestError{retryable: true}
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(
			`{"planner":"architect_short","task":"Lập kế hoạch truyện ngắn","reason":"Dung lượng khá ngắn"}`)},
	}}, nil
}

func (m *failingThenValidModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, errors.New("không sử dụng")
}

func (m *failingThenValidModel) SupportsTools() bool { return true }

func TestDecidePlanStart_ValidAndFeedbackRetry(t *testing.T) {
	// Lần đầu đầu ra không hợp lệ (planner sai), lần hai có fence nhưng hợp lệ — retry phản hồi + trích JSON đều phải hoạt động.
	m := &scriptedModel{outputs: []string{
		`{"planner":"writer","task":"x","reason":"r"}`,
		"```json\n{\"planner\":\"architect_short\",\"task\":\"Viết một truyện ngắn trinh thám 20 chương...\",\"reason\":\"Người dùng yêu cầu rõ truyện ngắn\"}\n```",
	}}
	d, err := DecidePlanStart(context.Background(), m, "sys", "truyện ngắn trinh thám 20 chương", "suspense")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if d.Planner != "architect_short" || !strings.Contains(d.Task, "trinh thám") {
		t.Fatalf("Lỗi phân xử: %+v", d)
	}
	if got := atomic.LoadInt64(&m.idx); got != 2 {
		t.Fatalf("Phải gọi đúng 2 lần (1 không hợp lệ + 1 retry phản hồi thành công), nhận %d", got)
	}
}

func TestDecide_InvalidOutputContinuesUntilContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &scriptedModel{outputs: []string{"Hoàn toàn không phải JSON"}, cancel: cancel, cancelAt: 4}
	if _, err := DecidePlanStart(ctx, m, "sys", "yêu cầu", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("ngữ cảnh phải kết thúc vòng tự phục hồi, nhận %v", err)
	}
	if got := atomic.LoadInt64(&m.idx); got != 4 {
		t.Fatalf("Phải tiếp tục gọi trước khi ngữ cảnh hủy, nhận %d", got)
	}
}

func TestDecide_RetryableModelErrorReportsSharedProgress(t *testing.T) {
	m := &failingThenValidModel{failures: 8}
	var progress []agentcore.ProgressPayload
	ctx := agentcore.WithToolProgress(context.Background(), func(p agentcore.ProgressPayload) {
		progress = append(progress, p)
	})

	if _, err := DecidePlanStart(ctx, m, "sys", "yêu cầu", ""); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got := atomic.LoadInt64(&m.calls); got != 9 {
		t.Fatalf("số lần gọi mô hình = %d, cần 9", got)
	}
	if len(progress) != 8 || progress[7].Kind != agentcore.ProgressRetry || progress[7].Agent != "arbiter" || progress[7].Attempt != 8 || progress[7].MaxRetries != 0 {
		t.Fatalf("tiến trình = %+v", progress)
	}
}

func TestDecide_NonRetryableModelErrorFailsImmediately(t *testing.T) {
	m := &errorModel{err: retryableTestError{retryable: false}}
	if _, err := DecidePlanStart(context.Background(), m, "sys", "yêu cầu", ""); err == nil {
		t.Fatal("lỗi mô hình không thể thử lại phải thất bại")
	}
	if got := atomic.LoadInt64(&m.calls); got != 1 {
		t.Fatalf("số lần gọi mô hình = %d, cần 1", got)
	}
}

type errorModel struct {
	err   error
	calls int64
}

func (m *errorModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	atomic.AddInt64(&m.calls, 1)
	return nil, m.err
}

func (m *errorModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, m.err
}

func (m *errorModel) SupportsTools() bool { return true }

func TestInterventionDecision_ValidateAgainst(t *testing.T) {
	writing := InterventionFacts{Phase: string(domain.PhaseWriting), CompletedChapters: 10}
	complete := InterventionFacts{Phase: string(domain.PhaseComplete), CompletedChapters: 10}

	cases := []struct {
		name    string
		d       InterventionDecision
		f       InterventionFacts
		wantErr bool
	}{
		{"Quyết định rỗng", InterventionDecision{Reason: "r"}, writing, true},
		{"Thiếu reason", InterventionDecision{Answer: "Được"}, writing, true},
		{"Loại truy vấn", InterventionDecision{Answer: "Đã hoàn thành 10 chương", Reason: "Truy vấn"}, writing, false},
		{"Tổ hợp rewrite", InterventionDecision{
			Hold:     &AdvanceHoldOp{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "Viết lại giọng điệu chương 3"},
			Dispatch: &DispatchOp{Agent: "editor", Task: "Viết lại chương 3: đổi giọng lạnh hơn"},
			Reason:   "Sửa chương đã viết",
		}, writing, false},
		{"Mục tiêu dispatch không hợp lệ", InterventionDecision{Dispatch: &DispatchOp{Agent: "coordinator", Task: "x"}, Reason: "r"}, writing, true},
		{"reopen trong giai đoạn viết", InterventionDecision{Reopen: &ReopenOp{Chapters: []int{3}}, Reason: "r"}, writing, true},
		{"reopen trong giai đoạn hoàn tất", InterventionDecision{Reopen: &ReopenOp{Chapters: []int{3}}, Reason: "rewrite"}, complete, false},
		{"reopen vượt biên trong giai đoạn hoàn tất", InterventionDecision{Reopen: &ReopenOp{Chapters: []int{99}}, Reason: "r"}, complete, true},
		{"dispatch trực tiếp trong giai đoạn hoàn tất", InterventionDecision{Dispatch: &DispatchOp{Agent: "writer", Task: "x"}, Reason: "r"}, complete, true},
		{"cấm writer trong giai đoạn lập kế hoạch", InterventionDecision{Dispatch: &DispatchOp{Agent: "writer", Task: "Viết chương 1"}, Reason: "r"}, InterventionFacts{Phase: string(domain.PhaseOutline)}, true},
		{"cho phép architect trong giai đoạn lập kế hoạch", InterventionDecision{Dispatch: &DispatchOp{Agent: "architect_long", Task: "Bổ sung dàn ý"}, Reason: "r"}, InterventionFacts{Phase: string(domain.PhaseOutline)}, false},
		{"pause một lần thiếu điều kiện", InterventionDecision{Hold: &AdvanceHoldOp{Reason: "dừng"}, Reason: "r"}, writing, true},
		{"pause một lần thiếu tóm tắt", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary}, Reason: "r"}, writing, true},
		{"pause theo chương mục tiêu", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtChapter, TargetChapter: 15, Reason: "viết tới chương 15"}, Reason: "r"}, writing, false},
		{"chưa điền chương mục tiêu", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtChapter, Reason: "viết tới chương mục tiêu"}, Reason: "r"}, writing, true},
		{"chương mục tiêu đã hoàn thành", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtChapter, TargetChapter: 10, Reason: "viết tới chương 10"}, Reason: "r"}, writing, true},
		{"pause không theo mục tiêu nhưng mang chương", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, TargetChapter: 15, Reason: "dừng"}, Reason: "r"}, writing, true},
		{"hủy pause một lần", InterventionDecision{Hold: &AdvanceHoldOp{Cancel: true}, Answer: "tiếp tục", Reason: "r"}, writing, false},
		{"đặt pause một lần trong giai đoạn hoàn tất", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, Reason: "dừng"}, Reason: "r"}, complete, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.d.ValidateAgainst(tc.f)
			if (err != nil) != tc.wantErr {
				t.Fatalf("cần lỗi=%v, nhận %v", tc.wantErr, err)
			}
		})
	}
}

func TestDecideInterventionAcceptsTargetChapterHold(t *testing.T) {
	m := &scriptedModel{outputs: []string{`{
		"answer":"Sẽ viết liên tục tới chương 15 rồi pause",
		"rules":null,
		"hold":{"cancel":false,"after":"chapter","target_chapter":15,"reason":"viết tới chương 15 rồi pause"},
		"reopen":null,
		"dispatch":null,
		"reason":"Người dùng chỉ định điểm kết thúc chạy một lần"
	}`}}
	d, err := DecideIntervention(t.Context(), m, "sys", InterventionFacts{
		Phase: string(domain.PhaseWriting), CompletedChapters: 10, NextChapter: 11,
	}, "viết tới chương 15")
	if err != nil {
		t.Fatal(err)
	}
	if d.Hold == nil || d.Hold.After != domain.AdvanceHoldAtChapter || d.Hold.TargetChapter != 15 {
		t.Fatalf("Decode hold chương mục tiêu lỗi: %+v", d.Hold)
	}
}

func TestFailureDecision_Validate(t *testing.T) {
	facts := FailureFacts{Kind: "worker_failure", Phase: string(domain.PhaseWriting)}
	ok := FailureDecision{Action: "reroute", Dispatch: &DispatchOp{Agent: "architect_long", Task: "chạy expand_arc trước"}, Reason: "lỗi chỉ rõ lối ra"}
	if err := ok.ValidateAgainst(facts); err != nil {
		t.Fatalf("reroute hợp lệ bị từ chối: %v", err)
	}
	bad := FailureDecision{Action: "reroute", Reason: "r"}
	if err := bad.ValidateAgainst(facts); err == nil {
		t.Fatal("reroute không có dispatch phải bị từ chối")
	}
	if err := (&FailureDecision{Action: "escalate", Reason: "r"}).ValidateAgainst(facts); err == nil {
		t.Fatal("action không hợp lệ phải bị từ chối")
	}
	planning := FailureFacts{Kind: "worker_failure", Phase: string(domain.PhaseOutline)}
	writer := FailureDecision{Action: "reroute", Dispatch: &DispatchOp{Agent: "writer", Task: "Viết chương 1"}, Reason: "thử đi vòng qua lập kế hoạch"}
	if err := writer.ValidateAgainst(planning); err == nil {
		t.Fatal("phân xử thất bại không được dispatch writer trong giai đoạn lập kế hoạch")
	}
}

func TestCollectInterventionFacts(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("init: %v", err)
	}
	if err := st.Progress.Init(30); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.Book.Save(domain.BookMetadata{Title: "Sách kiểm thử", Synopsis: "Tóm tắt kiểm thử"}); err != nil {
		t.Fatalf("book: %v", err)
	}
	if err := st.RunMeta.Init("default", "openrouter", "m"); err != nil {
		t.Fatalf("run meta: %v", err)
	}
	if err := st.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview); err != nil {
		t.Fatalf("advance mode: %v", err)
	}
	if err := st.RunMeta.SetAdvanceHold(domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 20, Reason: "viết tới chương 20"}); err != nil {
		t.Fatalf("advance hold: %v", err)
	}
	if _, err := st.Decisions.Append(storepkg.DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "can thiệp trước", Reason: "đã vào hàng đợi"}); err != nil {
		t.Fatalf("append decision: %v", err)
	}

	f, err := CollectInterventionFacts(st)
	if err != nil {
		t.Fatalf("CollectInterventionFacts: %v", err)
	}
	if f.Title != "Sách kiểm thử" {
		t.Fatalf("facts phải chứa tên sách, nhận %+v", f)
	}
	if len(f.RecentDecisions) != 1 || f.RecentDecisions[0].Input != "can thiệp trước" {
		t.Fatalf("Thiếu bộ nhớ can thiệp: %+v", f.RecentDecisions)
	}
	if f.AdvanceMode != string(domain.ChapterAdvanceReview) || !f.HasAdvanceHold || f.AdvanceHoldAfter != string(domain.AdvanceHoldAtChapter) || f.AdvanceHoldTargetChapter != 20 {
		t.Fatalf("Thiếu facts điều khiển tiến triển: %+v", f)
	}
	if len(f.FoundationMissing) == 0 {
		t.Fatal("Sách mới phải có mục thiết lập nền tảng còn thiếu")
	}

	// /reopen là fact có thể đếm, bắt buộc vào facts: sách sau khi mở lại đã viết đủ số chương; thiếu nó mô hình sẽ
	// dựa vào completed=total để suy ra "đã hoàn tất" và bỏ qua phase=writing (sự cố thực đo).
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase: %v", err)
	}
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if err := st.Progress.ReopenContinue(); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	f, err = CollectInterventionFacts(st)
	if err != nil {
		t.Fatalf("CollectInterventionFacts after reopen: %v", err)
	}
	if f.ReopenCount != 1 || f.Phase != string(domain.PhaseWriting) {
		t.Fatalf("Thiếu facts mở lại: phase=%s reopen_count=%d", f.Phase, f.ReopenCount)
	}
}

func TestCollectInterventionFactsDoesNotExposeLayeredEstimateAsTotal(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(66); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1, Title: "Quyển một", Arcs: []domain.ArcOutline{
			{Index: 1, Title: "Arc hiện tại", Chapters: []domain.OutlineEntry{{Title: "một"}, {Title: "hai"}}},
			{Index: 2, Title: "Arc khung", EstimatedChapters: 64},
		},
	}}
	if err := st.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	p.Layered = true
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}

	facts, err := CollectInterventionFacts(st)
	if err != nil {
		t.Fatal(err)
	}
	if !facts.DynamicPlanning || facts.OutlinedChapters != 2 {
		t.Fatalf("Facts lập kế hoạch động lỗi: %+v", facts)
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "total_chapters") || strings.Contains(string(raw), `:66`) {
		t.Fatalf("Ước tính nội bộ không được đi vào Arbiter như tổng số chương: %s", raw)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`: `{"a":1}`,
		"Tiền tố ```json\n{\"a\":\"}\"}\n```": `{"a":"}"}`, // dấu ngoặc nhọn trong string không ảnh hưởng cân bằng
		"không có object":                     "",
		`{"nested":{"b":2},"c":3} hậu tố`:     `{"nested":{"b":2},"c":3}`,
	}
	for in, want := range cases {
		if got := llmcontract.ExtractJSONObject(in); got != want {
			t.Errorf("extractJSON(%q) = %q, cần %q", in, got, want)
		}
	}
}

// nativeModel khai báo hỗ trợ JSON Schema native: decide phải đi nhánh native.
type nativeModel struct {
	*scriptedModel
	stop agentcore.StopReason
}

func (m *nativeModel) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		Provider:   "openai",
		Model:      "gpt-test",
		Structured: llm.StructuredCapabilities{JSONSchema: llm.SupportYes, Strict: llm.SupportYes},
	}
}

func (m *nativeModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	resp, err := m.scriptedModel.Generate(ctx, msgs, tools, opts...)
	if resp != nil && m.stop != "" {
		resp.Message.StopReason = m.stop
	}
	return resp, err
}

// Kiểm thử contract (RFC §11.1): root là object, mọi thuộc tính (kể cả lồng nhau) đều bắt buộc, dispatch là object nullable.
func TestContractSchemasAreStrictReady(t *testing.T) {
	for _, c := range []llmcontract.Contract{planStartContract, failureContract, interventionContract} {
		if c.Schema["type"] != "object" {
			t.Fatalf("%s: root phải là object", c.Name)
		}
		if err := llmcontract.ValidateStrictReady(c.Schema); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		if len(c.Fingerprint()) != 12 {
			t.Fatalf("%s: fingerprint bất thường: %q", c.Name, c.Fingerprint())
		}
	}
	dispatch := failureContract.Schema["properties"].(map[string]any)["dispatch"].(map[string]any)
	types, ok := dispatch["type"].([]string)
	if !ok || !slices.Contains(types, "null") || !slices.Contains(types, "object") {
		t.Fatalf("dispatch phải là object nullable: %v", dispatch["type"])
	}
	var d FailureDecision
	if err := json.Unmarshal([]byte(`{"action":"retry","dispatch":null,"reason":"lỗi tạm thời"}`), &d); err != nil {
		t.Fatalf("Mẫu chứa dispatch:null phải decode được: %v", err)
	}
	if err := d.ValidateAgainst(FailureFacts{Phase: "writing"}); err != nil {
		t.Fatalf("Mẫu phải qua kiểm tra: %v", err)
	}
}

func TestDecideNativeSendsSchemaAndDecodesFullOutput(t *testing.T) {
	m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{
		`{"planner":"architect_short","task":"Lập kế hoạch truyện ngắn","reason":"Dung lượng khá ngắn"}`,
	}}}
	const semanticPrompt = "Chỉ dựa vào yêu cầu để quyết định cách lập kế hoạch."
	d, err := DecidePlanStart(t.Context(), m, semanticPrompt, "Viết một truyện ngắn", "")
	if err != nil || d.Planner != "architect_short" {
		t.Fatalf("phân xử native thất bại: %+v %v", d, err)
	}
	rf := m.lastCfg.ResponseFormat
	if rf == nil || rf.Type != agentcore.ResponseFormatJSONSchema || rf.JSONSchema == nil {
		t.Fatalf("chế độ native phải gửi response_format: %+v", rf)
	}
	if rf.JSONSchema.Name != "arbiter_plan_start" || rf.JSONSchema.Strict == nil || !*rf.JSONSchema.Strict {
		t.Fatalf("Tham số schema không khớp: %+v", rf.JSONSchema)
	}
	if got := m.lastMsgs[0].TextContent(); got != semanticPrompt {
		t.Fatalf("chế độ native không được chèn schema lặp vào prompt:\n%s", got)
	}
}

// Trong chế độ native, decode thất bại = provider vi phạm contract: báo lỗi ngay, không fallback extractJSON, không hỏi lại.
func TestDecideNativeFencedOutputIsContractViolation(t *testing.T) {
	m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{
		"```json\n{\"planner\":\"architect_short\",\"task\":\"x\",\"reason\":\"y\"}\n```",
	}}}
	_, err := DecidePlanStart(t.Context(), m, "sys", "Viết một truyện ngắn", "")
	if err == nil || !strings.Contains(err.Error(), "vi phạm contract") {
		t.Fatalf("Kỳ vọng lỗi vi phạm contract, nhận %v", err)
	}
	if m.idx != 1 {
		t.Fatalf("Vi phạm contract không được hỏi lại, đã gọi %d lần", m.idx)
	}
}

// Trong chế độ native, kiểm tra nghiệp vụ thất bại vẫn phản hồi hỏi lại, và request hỏi lại giữ schema.
func TestDecideNativeValidateFailureFeedbackKeepsSchema(t *testing.T) {
	m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{
		`{"action":"reroute","dispatch":null,"reason":"cần đổi tuyến"}`,
		`{"action":"retry","dispatch":null,"reason":"lỗi tạm thời có thể retry"}`,
	}}}
	d, err := DecideFailure(t.Context(), m, "sys", FailureFacts{Kind: "worker_failure", Phase: "writing"})
	if err != nil || d.Action != "retry" {
		t.Fatalf("Phải thành công sau hỏi lại bằng phản hồi: %+v %v", d, err)
	}
	if m.idx != 2 {
		t.Fatalf("Phải gọi đúng hai lần, nhận %d", m.idx)
	}
	if m.lastCfg.ResponseFormat == nil {
		t.Fatal("Yêu cầu hỏi lại đã mất schema")
	}
}

// chế độ native phân loại stop reason trước: cắt ngắn/từ chối/phản hồi rỗng là facts lỗi độc lập, không đi vào vòng hỏi lại.
func TestDecideNativeStopReasonClassification(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		stop    agentcore.StopReason
		wantErr string
	}{
		{"length cắt ngắn", `{"planner":`, agentcore.StopReasonLength, "Đầu ra của mô hình bị cắt do vượt quá giới hạn độ dài (stop_reason=length)"},
		{"safety từ chối", `không thể hỗ trợ`, agentcore.StopReasonSafety, "Mô hình từ chối hoặc kích hoạt bộ lọc nội dung (stop_reason=safety)"},
		{"phản hồi rỗng", ``, agentcore.StopReasonStop, "Schema native trả nội dung rỗng"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{tc.output}}, stop: tc.stop}
			_, err := DecidePlanStart(t.Context(), m, "sys", "Viết một truyện ngắn", "")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Kỳ vọng lỗi %q, nhận %v", tc.wantErr, err)
			}
			if m.idx != 1 {
				t.Fatalf("Lý do dừng không được hỏi lại, đã gọi %d lần", m.idx)
			}
		})
	}
}

// marshalPayload thất bại phải lộ ra: âm thầm giả {} sẽ khiến mô hình phán đoán sai dựa trên facts giả.
func TestMarshalPayloadErrors(t *testing.T) {
	if _, err := marshalPayload(func() {}); err == nil {
		t.Fatal("Payload không serialize được phải báo lỗi")
	}
	s, err := marshalPayload(map[string]int{"a": 1})
	if err != nil || !strings.Contains(s, `"a"`) {
		t.Fatalf("Payload bình thường: %q %v", s, err)
	}
}
