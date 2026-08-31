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

// scriptedModel trả về văn bản định sẵn theo thứ tự gọi.
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
		return nil, errors.New("thinking is only supported for reasoning chat models")
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.take())},
	}}, nil
}

func TestDecidePlanStartDoesNotSendThinkingToChatModel(t *testing.T) {
	m := &scriptedModel{outputs: []string{
		`{"planner":"architect_short","task":"lập kế hoạch truyện ngắn","reason":"độ dài khá ngắn"}`,
	}, rejectThinking: true}
	if _, err := DecidePlanStart(t.Context(), m, "sys", "viết một truyện ngắn", ""); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if m.lastCfg.ThinkingLevel != agentcore.ThinkingAuto {
		t.Fatalf("Arbiter không được gửi tham số thinking cho mô hình chat thông thường, nhận %q", m.lastCfg.ThinkingLevel)
	}
	if m.lastCfg.MaxTokens != decideMaxTokens {
		t.Fatalf("max_tokens = %d, cần là %d", m.lastCfg.MaxTokens, decideMaxTokens)
	}
}

func TestDecidePromptContractAppendsSchema(t *testing.T) {
	m := &scriptedModel{outputs: []string{
		`{"planner":"architect_short","task":"lập kế hoạch truyện ngắn","reason":"độ dài khá ngắn"}`,
	}}
	const semanticPrompt = "chỉ dựa vào yêu cầu để xác định cách lập kế hoạch."
	if _, err := DecidePlanStart(t.Context(), m, semanticPrompt, "viết một truyện ngắn", ""); err != nil {
		t.Fatal(err)
	}
	got := m.lastMsgs[0].TextContent()
	for _, want := range []string{semanticPrompt, "<output-json-schema>", `"planner"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("hợp đồng prompt thiếu %q:\n%s", want, got)
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

func (e retryableTestError) Error() string             { return "provider unavailable" }
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
			`{"planner":"architect_short","task":"lập kế hoạch truyện ngắn","reason":"độ dài khá ngắn"}`)},
	}}, nil
}

func (m *failingThenValidModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, errors.New("unused")
}

func (m *failingThenValidModel) SupportsTools() bool { return true }

func TestDecidePlanStart_ValidAndFeedbackRetry(t *testing.T) {
	// Lần đầu đầu ra không hợp lệ (sai planner), lần thứ hai có hàng rào nhưng hợp lệ — phản hồi thử lại và trích xuất JSON đều phải hoạt động.
	m := &scriptedModel{outputs: []string{
		`{"planner":"writer","task":"x","reason":"r"}`,
		"```json\n{\"planner\":\"architect_short\",\"task\":\"viết truyện trinh thám ngắn 20 chương…\",\"reason\":\"người dùng yêu cầu rõ truyện ngắn\"}\n```",
	}}
	d, err := DecidePlanStart(context.Background(), m, "sys", "truyện trinh thám ngắn 20 chương", "suspense")
	if err != nil {
		t.Fatalf("quyết định: %v", err)
	}
	if d.Planner != "architect_short" || !strings.Contains(d.Task, "trinh thám") {
		t.Fatalf("quyết định sai: %+v", d)
	}
	if got := atomic.LoadInt64(&m.idx); got != 2 {
		t.Fatalf("phải gọi đúng 2 lần (1 lần không hợp lệ + 1 lần phản hồi thử lại thành công), nhận %d", got)
	}
}

func TestDecide_InvalidOutputContinuesUntilContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &scriptedModel{outputs: []string{"hoàn toàn không phải JSON"}, cancel: cancel, cancelAt: 4}
	if _, err := DecidePlanStart(ctx, m, "sys", "yêu cầu", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("vòng tự sửa phải kết thúc bởi context, nhận %v", err)
	}
	if got := atomic.LoadInt64(&m.idx); got != 4 {
		t.Fatalf("phải tiếp tục gọi trước khi hủy context, nhận %d", got)
	}
}

func TestDecide_RetryableModelErrorReportsSharedProgress(t *testing.T) {
	m := &failingThenValidModel{failures: 8}
	var progress []agentcore.ProgressPayload
	ctx := agentcore.WithToolProgress(context.Background(), func(p agentcore.ProgressPayload) {
		progress = append(progress, p)
	})

	if _, err := DecidePlanStart(ctx, m, "sys", "yêu cầu", ""); err != nil {
		t.Fatalf("quyết định: %v", err)
	}
	if got := atomic.LoadInt64(&m.calls); got != 9 {
		t.Fatalf("số lần gọi mô hình = %d, cần là 9", got)
	}
	if len(progress) != 8 || progress[7].Kind != agentcore.ProgressRetry || progress[7].Agent != "arbiter" || progress[7].Attempt != 8 || progress[7].MaxRetries != 0 {
		t.Fatalf("tiến độ = %+v", progress)
	}
}

func TestDecide_NonRetryableModelErrorFailsImmediately(t *testing.T) {
	m := &errorModel{err: retryableTestError{retryable: false}}
	if _, err := DecidePlanStart(context.Background(), m, "sys", "yêu cầu", ""); err == nil {
		t.Fatal("lỗi mô hình không thể thử lại phải thất bại ngay")
	}
	if got := atomic.LoadInt64(&m.calls); got != 1 {
		t.Fatalf("số lần gọi mô hình = %d, cần là 1", got)
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
		{"quyết định rỗng", InterventionDecision{Reason: "r"}, writing, true},
		{"thiếu reason", InterventionDecision{Answer: "được rồi"}, writing, true},
		{"dạng truy vấn", InterventionDecision{Answer: "đã hoàn thành 10 chương", Reason: "truy vấn"}, writing, false},
		{"tổ hợp làm lại", InterventionDecision{
			Hold:     &AdvanceHoldOp{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "viết lại giọng điệu chương 3"},
			Dispatch: &DispatchOp{Agent: "editor", Task: "viết lại chương 3: giọng điệu lạnh hơn"},
			Reason:   "chỉnh sửa chương đã viết",
		}, writing, false},
		{"mục tiêu giao việc không hợp lệ", InterventionDecision{Dispatch: &DispatchOp{Agent: "coordinator", Task: "x"}, Reason: "r"}, writing, true},
		{"reopen trong giai đoạn viết", InterventionDecision{Reopen: &ReopenOp{Chapters: []int{3}}, Reason: "r"}, writing, true},
		{"reopen trong giai đoạn hoàn tất", InterventionDecision{Reopen: &ReopenOp{Chapters: []int{3}}, Reason: "làm lại"}, complete, false},
		{"reopen vượt giới hạn khi hoàn tất", InterventionDecision{Reopen: &ReopenOp{Chapters: []int{99}}, Reason: "r"}, complete, true},
		{"giao việc trực tiếp khi hoàn tất", InterventionDecision{Dispatch: &DispatchOp{Agent: "writer", Task: "x"}, Reason: "r"}, complete, true},
		{"cấm writer trong giai đoạn lập kế hoạch", InterventionDecision{Dispatch: &DispatchOp{Agent: "writer", Task: "viết chương 1"}, Reason: "r"}, InterventionFacts{Phase: string(domain.PhaseOutline)}, true},
		{"cho phép architect trong giai đoạn lập kế hoạch", InterventionDecision{Dispatch: &DispatchOp{Agent: "architect_long", Task: "hoàn thiện dàn ý"}, Reason: "r"}, InterventionFacts{Phase: string(domain.PhaseOutline)}, false},
		{"tạm dừng một lần thiếu điều kiện", InterventionDecision{Hold: &AdvanceHoldOp{Reason: "dừng"}, Reason: "r"}, writing, true},
		{"tạm dừng một lần thiếu tóm tắt", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary}, Reason: "r"}, writing, true},
		{"tạm dừng tại chương mục tiêu", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtChapter, TargetChapter: 15, Reason: "viết đến chương 15"}, Reason: "r"}, writing, false},
		{"chưa điền chương mục tiêu", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtChapter, Reason: "viết đến chương mục tiêu"}, Reason: "r"}, writing, true},
		{"chương mục tiêu đã hoàn tất", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtChapter, TargetChapter: 10, Reason: "viết đến chương 10"}, Reason: "r"}, writing, true},
		{"tạm dừng không theo mục tiêu nhưng kèm chương", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, TargetChapter: 15, Reason: "dừng"}, Reason: "r"}, writing, true},
		{"hủy tạm dừng một lần", InterventionDecision{Hold: &AdvanceHoldOp{Cancel: true}, Answer: "tiếp tục", Reason: "r"}, writing, false},
		{"đặt tạm dừng một lần khi hoàn tất", InterventionDecision{Hold: &AdvanceHoldOp{After: domain.AdvanceHoldAtBoundary, Reason: "dừng"}, Reason: "r"}, complete, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.d.ValidateAgainst(tc.f)
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, nhận %v", tc.wantErr, err)
			}
		})
	}
}

func TestDecideInterventionAcceptsTargetChapterHold(t *testing.T) {
	m := &scriptedModel{outputs: []string{`{
        "answer":"sẽ viết liên tục đến chương 15 rồi tạm dừng",
        "rules":null,
        "hold":{"cancel":false,"after":"chapter","target_chapter":15,"reason":"viết đến chương 15 rồi tạm dừng"},
        "reopen":null,
        "dispatch":null,
        "reason":"người dùng chỉ định điểm kết thúc chạy một lần"
    }`}}
	d, err := DecideIntervention(t.Context(), m, "sys", InterventionFacts{
		Phase: string(domain.PhaseWriting), CompletedChapters: 10, NextChapter: 11,
	}, "viết đến chương 15")
	if err != nil {
		t.Fatal(err)
	}
	if d.Hold == nil || d.Hold.After != domain.AdvanceHoldAtChapter || d.Hold.TargetChapter != 15 {
		t.Fatalf("giải mã hold chương mục tiêu sai: %+v", d.Hold)
	}
}

func TestFailureDecision_Validate(t *testing.T) {
	facts := FailureFacts{Kind: "worker_failure", Phase: string(domain.PhaseWriting)}
	ok := FailureDecision{Action: "reroute", Dispatch: &DispatchOp{Agent: "architect_long", Task: "trước hết expand_arc"}, Reason: "lỗi chỉ ra hướng xử lý"}
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
	writer := FailureDecision{Action: "reroute", Dispatch: &DispatchOp{Agent: "writer", Task: "viết chương 1"}, Reason: "thử bỏ qua lập kế hoạch"}
	if err := writer.ValidateAgainst(planning); err == nil {
		t.Fatal("quyết định lỗi không được giao writer trong giai đoạn lập kế hoạch")
	}
}

func TestCollectInterventionFacts(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("khởi tạo: %v", err)
	}
	if err := st.Progress.Init(30); err != nil {
		t.Fatalf("khởi tạo tiến độ: %v", err)
	}
	if err := st.Book.Save(domain.BookMetadata{Title: "sách thử nghiệm", Synopsis: "tóm tắt thử nghiệm"}); err != nil {
		t.Fatalf("lưu thông tin sách: %v", err)
	}
	if err := st.RunMeta.Init("default", "openrouter", "m"); err != nil {
		t.Fatalf("khởi tạo siêu dữ liệu chạy: %v", err)
	}
	if err := st.RunMeta.SetAdvanceMode(domain.ChapterAdvanceReview); err != nil {
		t.Fatalf("thiết lập chế độ tiến triển: %v", err)
	}
	if err := st.RunMeta.SetAdvanceHold(domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 20, Reason: "viết đến chương 20"}); err != nil {
		t.Fatalf("thiết lập tạm dừng tiến triển: %v", err)
	}
	if _, err := st.Decisions.Append(storepkg.DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "can thiệp trước đó", Reason: "đã xếp hàng"}); err != nil {
		t.Fatalf("thêm quyết định: %v", err)
	}

	f, err := CollectInterventionFacts(st)
	if err != nil {
		t.Fatalf("CollectInterventionFacts: %v", err)
	}
	if f.Title != "sách thử nghiệm" {
		t.Fatalf("facts phải chứa tên sách, nhận %+v", f)
	}
	if len(f.RecentDecisions) != 1 || f.RecentDecisions[0].Input != "can thiệp trước đó" {
		t.Fatalf("thiếu bộ nhớ can thiệp: %+v", f.RecentDecisions)
	}
	if f.AdvanceMode != string(domain.ChapterAdvanceReview) || !f.HasAdvanceHold || f.AdvanceHoldAfter != string(domain.AdvanceHoldAtChapter) || f.AdvanceHoldTargetChapter != 20 {
		t.Fatalf("thiếu facts điều khiển tiến triển: %+v", f)
	}
	if len(f.FoundationMissing) == 0 {
		t.Fatal("sách mới phải thiếu một số thiết lập nền tảng")
	}

	// /reopen là fact liệt kê được, bắt buộc phải vào facts: sau khi mở lại,
	// sách đã viết đủ số chương; thiếu fact này, mô hình sẽ suy ra "đã hoàn tất"
	// theo completed=total và bỏ qua phase=writing (sự cố đã xảy ra thực tế).
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("cập nhật giai đoạn: %v", err)
	}
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatalf("đánh dấu hoàn tất: %v", err)
	}
	if err := st.Progress.ReopenContinue(); err != nil {
		t.Fatalf("mở lại và tiếp tục: %v", err)
	}
	f, err = CollectInterventionFacts(st)
	if err != nil {
		t.Fatalf("CollectInterventionFacts sau khi mở lại: %v", err)
	}
	if f.ReopenCount != 1 || f.Phase != string(domain.PhaseWriting) {
		t.Fatalf("thiếu fact mở lại: phase=%s reopen_count=%d", f.Phase, f.ReopenCount)
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
		Index: 1, Title: "quyển một", Arcs: []domain.ArcOutline{
			{Index: 1, Title: "cung hiện tại", Chapters: []domain.OutlineEntry{{Title: "một"}, {Title: "hai"}}},
			{Index: 2, Title: "cung khung", EstimatedChapters: 64},
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
		t.Fatalf("facts lập kế hoạch động sai: %+v", facts)
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "total_chapters") || strings.Contains(string(raw), `:66`) {
		t.Fatalf("ước tính nội bộ không được đưa làm tổng số chương vào Arbiter: %s", raw)
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`{"a":1}`: `{"a":1}`,
		"tiền tố ```json\n{\"a\":\"}\"}\n```": `{"a":"}"}`, // dấu ngoặc nhọn trong chuỗi không ảnh hưởng đến cân bằng
		"không có đối tượng":                  "",
		`{"nested":{"b":2},"c":3} phần đuôi`:  `{"nested":{"b":2},"c":3}`,
	}
	for in, want := range cases {
		if got := llmcontract.ExtractJSONObject(in); got != want {
			t.Errorf("extractJSON(%q) = %q, mong đợi %q", in, got, want)
		}
	}
}

// nativeModel khai báo hỗ trợ JSON Schema gốc; decide phải đi theo nhánh native.
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

// Kiểm thử hợp đồng (RFC §11.1): gốc là object, mọi thuộc tính (kể cả lồng nhau) đều required, dispatch là đối tượng có thể rỗng.
func TestContractSchemasAreStrictReady(t *testing.T) {
	for _, c := range []llmcontract.Contract{planStartContract, failureContract, interventionContract} {
		if c.Schema["type"] != "object" {
			t.Fatalf("%s: gốc phải là object", c.Name)
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
		t.Fatalf("dispatch phải là đối tượng có thể rỗng: %v", dispatch["type"])
	}
	var d FailureDecision
	if err := json.Unmarshal([]byte(`{"action":"retry","dispatch":null,"reason":"lỗi tạm thời"}`), &d); err != nil {
		t.Fatalf("mẫu có dispatch:null phải giải mã được: %v", err)
	}
	if err := d.ValidateAgainst(FailureFacts{Phase: "writing"}); err != nil {
		t.Fatalf("mẫu phải vượt qua kiểm tra: %v", err)
	}
}

func TestDecideNativeSendsSchemaAndDecodesFullOutput(t *testing.T) {
	m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{
		`{"planner":"architect_short","task":"lập kế hoạch truyện ngắn","reason":"độ dài khá ngắn"}`,
	}}}
	const semanticPrompt = "chỉ dựa vào yêu cầu để xác định cách lập kế hoạch."
	d, err := DecidePlanStart(t.Context(), m, semanticPrompt, "viết một truyện ngắn", "")
	if err != nil || d.Planner != "architect_short" {
		t.Fatalf("native quyết định thất bại: %+v %v", d, err)
	}
	rf := m.lastCfg.ResponseFormat
	if rf == nil || rf.Type != agentcore.ResponseFormatJSONSchema || rf.JSONSchema == nil {
		t.Fatalf("chế độ native phải gửi response_format: %+v", rf)
	}
	if rf.JSONSchema.Name != "arbiter_plan_start" || rf.JSONSchema.Strict == nil || !*rf.JSONSchema.Strict {
		t.Fatalf("tham số schema không đúng: %+v", rf.JSONSchema)
	}
	if got := m.lastMsgs[0].TextContent(); got != semanticPrompt {
		t.Fatalf("chế độ native không được lặp lại schema trong prompt:\n%s", got)
	}
}

// Ở chế độ native, giải mã thất bại là vi phạm hợp đồng provider: báo lỗi ngay, không dùng extractJSON dự phòng và không hỏi lại.
func TestDecideNativeFencedOutputIsContractViolation(t *testing.T) {
	m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{
		"```json\n{\"planner\":\"architect_short\",\"task\":\"x\",\"reason\":\"y\"}\n```",
	}}}
	_, err := DecidePlanStart(t.Context(), m, "sys", "viết một truyện ngắn", "")
	if err == nil || !strings.Contains(err.Error(), "Vi phạm hợp đồng Schema gốc") {
		t.Fatalf("mong đợi lỗi vi phạm hợp đồng Schema gốc, nhận %v", err)
	}
	if m.idx != 1 {
		t.Fatalf("vi phạm hợp đồng không được hỏi lại, đã gọi %d lần", m.idx)
	}
}

// Ở chế độ native, lỗi kiểm tra nghiệp vụ vẫn phản hồi để hỏi lại và yêu cầu hỏi lại giữ schema.
func TestDecideNativeValidateFailureFeedbackKeepsSchema(t *testing.T) {
	m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{
		`{"action":"reroute","dispatch":null,"reason":"cần đổi hướng"}`,
		`{"action":"retry","dispatch":null,"reason":"lỗi tạm thời có thể thử lại"}`,
	}}}
	d, err := DecideFailure(t.Context(), m, "sys", FailureFacts{Kind: "worker_failure", Phase: "writing"})
	if err != nil || d.Action != "retry" {
		t.Fatalf("phải thành công sau khi phản hồi hỏi lại: %+v %v", d, err)
	}
	if m.idx != 2 {
		t.Fatalf("phải gọi đúng hai lần, nhận %d", m.idx)
	}
	if m.lastCfg.ResponseFormat == nil {
		t.Fatal("yêu cầu hỏi lại bị mất schema")
	}
}

// Ở chế độ native, trước hết phân loại lý do kết thúc: bị cắt, từ chối và phản hồi rỗng là các sự kiện lỗi độc lập, không vào vòng hỏi lại.
func TestDecideNativeStopReasonClassification(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		stop    agentcore.StopReason
		wantErr string
	}{
		{"length bị cắt", `{"planner":`, agentcore.StopReasonLength, "bị cắt do giới hạn độ dài"},
		{"safety từ chối", `không thể hỗ trợ`, agentcore.StopReasonSafety, "từ chối trả lời"},
		{"phản hồi rỗng", ``, agentcore.StopReasonStop, "nội dung trống"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &nativeModel{scriptedModel: &scriptedModel{outputs: []string{tc.output}}, stop: tc.stop}
			_, err := DecidePlanStart(t.Context(), m, "sys", "viết một truyện ngắn", "")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("mong đợi lỗi %q, nhận %v", tc.wantErr, err)
			}
			if m.idx != 1 {
				t.Fatalf("lý do kết thúc không được hỏi lại, đã gọi %d lần", m.idx)
			}
		})
	}
}

// Lỗi marshalPayload phải được lộ ra: âm thầm giả tạo {} khiến mô hình phán đoán dựa trên dữ kiện giả.
func TestMarshalPayloadErrors(t *testing.T) {
	if _, err := marshalPayload(func() {}); err == nil {
		t.Fatal("payload không thể tuần tự hóa phải báo lỗi")
	}
	s, err := marshalPayload(map[string]int{"a": 1})
	if err != nil || !strings.Contains(s, `"a"`) {
		t.Fatalf("payload hợp lệ: %q %v", s, err)
	}
}
