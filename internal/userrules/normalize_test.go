package userrules

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

func TestExtractJSON_StripsCodeFences(t *testing.T) {
	cases := []struct{ in, wantHas string }{
		{"```json\n{\"a\":1}\n```", `"a":1`},
		{"```\n{\"a\":1}\n```", `"a":1`},
		{"Giải thích tiền tố\n{\"a\":1}\nHậu tố", `"a":1`},
		{"{\"a\":1}", `"a":1`},
	}
	for _, c := range cases {
		got := llmcontract.ExtractJSONObject(c.in)
		if got == "" {
			t.Fatalf("extractJSON(%q) trả về rỗng", c.in)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(got), &m); err != nil {
			t.Fatalf("extractJSON(%q)=%q không phải JSON hợp lệ: %v", c.in, got, err)
		}
	}
	if llmcontract.ExtractJSONObject("Không có JSON nào") != "" {
		t.Fatal("khi không có JSON phải trả về chuỗi rỗng")
	}
}

func TestParseNormalizerJSON_FullOutput(t *testing.T) {
	raw := "```json\n" + `{
  "structured": {
    "genre": "đô thị",
    "forbidden_chars": [],
    "forbidden_phrases": ["ở mức độ nào đó"],
    "fatigue_words": [{"word": "bất ngờ", "max_per_chapter": 2}]
  },
  "preferences": "Nhân vật chính bình tĩnh kiềm chế",
  "uncertain": ["Ít dùng ẩn dụ: không có ngưỡng"]
}` + "\n```"
	body := llmcontract.ExtractJSONObject(raw)
	if err := llmcontract.ValidateJSON(normalizeContract.Schema, []byte(body)); err != nil {
		t.Fatalf("phải phân tích thành công: %v", err)
	}
	var out normalizerOutput
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("phải decode thành công: %v", err)
	}
	cand, err := out.toCandidate("startup_prompt")
	if err != nil {
		t.Fatalf("toCandidate: %v", err)
	}
	if cand.Structured.Genre != "đô thị" {
		t.Fatalf("genre phân tích sai: %+v", cand.Structured)
	}
	if len(cand.Structured.ForbiddenPhrases) != 1 || cand.Structured.ForbiddenPhrases[0] != "ở mức độ nào đó" {
		t.Fatalf("forbidden_phrases phân tích sai: %v", cand.Structured.ForbiddenPhrases)
	}
	if cand.Structured.FatigueWords["bất ngờ"] != 2 {
		t.Fatalf("mảng fatigue_words phải chuyển thành map: %v", cand.Structured.FatigueWords)
	}
	if cand.Preferences != "Nhân vật chính bình tĩnh kiềm chế" {
		t.Fatalf("preferences phân tích sai: %q", cand.Preferences)
	}
	if len(cand.Uncertain) != 1 {
		t.Fatalf("uncertain phải có 1 mục, got %v", cand.Uncertain)
	}
}

// Kiểm tra mục fatigue: từ rỗng và ngưỡng không phải số nguyên dương đều là lỗi nghiệp vụ có thể phản hồi để sửa.
func TestToCandidateRejectsInvalidFatigueEntries(t *testing.T) {
	bad := normalizerOutput{Structured: normalizerStructured{
		FatigueWords: []fatigueWordEntry{{Word: " ", MaxPerChapter: 2}},
	}}
	if _, err := bad.toCandidate("x"); err == nil {
		t.Fatal("mục từ rỗng phải báo lỗi")
	}
	bad = normalizerOutput{Structured: normalizerStructured{
		FatigueWords: []fatigueWordEntry{{Word: "bất ngờ", MaxPerChapter: 0}},
	}}
	if _, err := bad.toCandidate("x"); err == nil {
		t.Fatal("ngưỡng không phải số nguyên dương phải báo lỗi")
	}
}

func TestParseNormalizerJSON_GarbageFails(t *testing.T) {
	if body := llmcontract.ExtractJSONObject("Mô hình chỉ trả về một câu, không có JSON"); body != "" {
		t.Fatal("không có JSON thì phải phân tích thất bại (kích hoạt hạ cấp)")
	}
	if body := llmcontract.ExtractJSONObject("{ không hoàn chỉnh"); body != "" {
		t.Fatal("JSON thiếu hụt phải phân tích thất bại")
	}
}

// Test contract (RFC §11.1): root là object, mọi thuộc tính (gồm structured/fatigue_words lồng nhau) đều required.
func TestNormalizeContractIsStrictReady(t *testing.T) {
	if normalizeContract.Schema["type"] != "object" {
		t.Fatal("root phải là object")
	}
	if err := llmcontract.ValidateStrictReady(normalizeContract.Schema); err != nil {
		t.Fatal(err)
	}
}

func TestNormalize_NilModelErrors(t *testing.T) {
	// Không có mô hình khả dụng: trả về lỗi rõ ràng, tầng Service hạ cấp thành raw preferences.
	var n *Normalizer = NewNormalizer(nil)
	if _, err := n.Normalize(t.Context(), "startup_prompt", "Mỗi chương 1200 chữ, nhân vật chính bình tĩnh"); err == nil {
		t.Fatal("không có mô hình phải trả về lỗi")
	}
}

// scriptedModel là fake ChatModel tối thiểu: trả lời định sẵn theo thứ tự gọi và ghi lại vòng messages cuối cùng nhận được,
// để assertion kiểm tra retry có phản hồi có đưa gợi ý sửa vào lượt đối thoại tiếp theo không. Khi hết reply thì lặp lại reply cuối.
type scriptedModel struct {
	replies  []string
	calls    int
	lastMsgs []agentcore.Message
	lastCfg  agentcore.CallConfig
	err      error // Khi khác nil, Generate luôn trả về lỗi này
	cancel   context.CancelFunc
	cancelAt int
}

func (m *scriptedModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	var cfg agentcore.CallConfig
	for _, o := range opts {
		o(&cfg)
	}
	m.lastCfg = cfg
	m.lastMsgs = messages
	m.calls++
	if m.cancel != nil && m.cancelAt > 0 && m.calls >= m.cancelAt {
		m.cancel()
	}
	if m.err != nil {
		return nil, m.err
	}
	i := m.calls - 1
	if i >= len(m.replies) {
		i = len(m.replies) - 1
	}
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.replies[i])},
	}}, nil
}

func (m *scriptedModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (m *scriptedModel) SupportsTools() bool { return false }

// Retry có phản hồi: lượt đầu trả JSON hỏng, lượt hai mới hợp lệ. Normalize phải thành công, và lượt đối thoại thứ hai mang theo
// đầu ra hỏng và gợi ý sửa của lượt trước (có phản hồi, không retry mù nguyên dạng).
func TestNormalize_FeedbackRetryRecovers(t *testing.T) {
	model := &scriptedModel{replies: []string{
		"Đây không phải JSON",
		`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":["ở mức độ nào đó"],"fatigue_words":[]},"preferences":"","uncertain":[]}`,
	}}
	n := NewNormalizer(model)

	cand, err := n.Normalize(t.Context(), "startup_prompt", "Đừng để xuất hiện ở mức độ nào đó")
	if err != nil {
		t.Fatalf("lượt hai đã trả JSON hợp lệ, không nên thất bại: %v", err)
	}
	if len(cand.Structured.ForbiddenPhrases) != 1 {
		t.Fatalf("phải phân tích ra forbidden_phrases, got %+v", cand.Structured)
	}
	if model.calls != 2 {
		t.Fatalf("phải thành công ở lần thứ 2, thực tế gọi %d lần", model.calls)
	}

	var sawBad, sawHint bool
	for _, msg := range model.lastMsgs {
		text := msg.TextContent()
		if text == "Đây không phải JSON" {
			sawBad = true
		}
		if strings.Contains(text, "JSON Schema") && strings.Contains(text, "lỗi：") {
			sawHint = true
		}
	}
	if !sawBad || !sawHint {
		t.Errorf("lượt hai phải gộp đầu ra hỏng và gợi ý sửa từ lượt trước, sawBad=%v sawHint=%v", sawBad, sawHint)
	}
	system := model.lastMsgs[0].TextContent()
	if !strings.Contains(system, "<output-json-schema>") || !strings.Contains(system, `"fatigue_words"`) {
		t.Fatalf("prompt contract phải tự động đính kèm schema từ Contract:\n%s", system)
	}
}

// Chuẩn hóa không override thinking mặc định của mô hình; mô hình chat thông thường sẽ từ chối off tường minh.
func TestNormalize_LeavesThinkingUnspecifiedAndReservesTokens(t *testing.T) {
	model := &scriptedModel{replies: []string{`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`}}
	n := NewNormalizer(model)

	if _, err := n.Normalize(t.Context(), "startup_prompt", "Một rule bất kỳ"); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if model.lastCfg.ThinkingLevel != agentcore.ThinkingAuto {
		t.Errorf("không nên gửi tham số thinking, got %q", model.lastCfg.ThinkingLevel)
	}
	if model.lastCfg.MaxTokens != normalizeMaxTokens {
		t.Errorf("max_tokens phải là %d, got %d", normalizeMaxTokens, model.lastCfg.MaxTokens)
	}
}

// JSON hỏng xuyên suốt: không có giới hạn số lần cố định, tiếp tục hỏi lại có phản hồi đến khi context bị hủy.
func TestNormalize_FeedbackRetryContinuesUntilContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &scriptedModel{replies: []string{"hỏng"}, cancel: cancel, cancelAt: 4}
	n := NewNormalizer(model)

	_, err := n.Normalize(ctx, "startup_prompt", "Mỗi chương 1200 chữ")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context phải kết thúc vòng tự chữa, got %v", err)
	}
	if model.calls != 4 {
		t.Fatalf("phải tiếp tục gọi trước khi context bị hủy, thực tế %d", model.calls)
	}
}

type terminalTestError struct{}

func (terminalTestError) Error() string   { return "401 authentication failed" }
func (terminalTestError) Retryable() bool { return false }

type retryableTestError struct{}

func (retryableTestError) Error() string             { return "provider unavailable" }
func (retryableTestError) Retryable() bool           { return true }
func (retryableTestError) RetryAfter() time.Duration { return time.Millisecond }

// Lỗi kết thúc (401, v.v.) không được retry mù: gọi đúng 1 lần rồi trả lỗi.
func TestNormalize_TerminalErrorStopsImmediately(t *testing.T) {
	model := &scriptedModel{err: terminalTestError{}}
	n := NewNormalizer(model)

	_, err := n.Normalize(t.Context(), "startup_prompt", "Rule")
	if err == nil || !errors.As(err, &terminalTestError{}) {
		t.Fatalf("phải truyền ra lỗi kết thúc: %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("lỗi kết thúc không được retry, thực tế gọi %d lần", model.calls)
	}
}

// Lỗi request có thể retry được llmretry retry với backoff.
type flakyModel struct {
	scriptedModel
	failures int
}

func (m *flakyModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if m.scriptedModel.calls < m.failures {
		m.scriptedModel.calls++
		return nil, retryableTestError{}
	}
	return m.scriptedModel.Generate(ctx, msgs, tools, opts...)
}

func TestNormalize_RetryableErrorRecovers(t *testing.T) {
	model := &flakyModel{
		scriptedModel: scriptedModel{replies: []string{`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`}},
		failures:      2,
	}
	n := NewNormalizer(model)
	cand, err := n.Normalize(t.Context(), "startup_prompt", "Rule")
	if err != nil || cand.Preferences != "x" {
		t.Fatalf("phải thành công sau backoff: %+v %v", cand, err)
	}
}

// nativeRulesModel khai báo hỗ trợ JSON Schema native.
type nativeRulesModel struct {
	*scriptedModel
}

func (m *nativeRulesModel) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		Provider:   "openai",
		Model:      "gpt-test",
		Structured: llm.StructuredCapabilities{JSONSchema: llm.SupportYes, Strict: llm.SupportYes},
	}
}

func TestNormalize_NativeSendsSchemaAndRejectsFences(t *testing.T) {
	// Chế độ native: schema đi vào request; JSON trần thành công.
	model := &nativeRulesModel{&scriptedModel{replies: []string{
		`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`,
	}}}
	n := NewNormalizer(model)
	cand, err := n.Normalize(t.Context(), "startup_prompt", "Rule")
	if err != nil || cand.Preferences != "x" {
		t.Fatalf("chuẩn hóa native thất bại: %+v %v", cand, err)
	}
	rf := model.lastCfg.ResponseFormat
	if rf == nil || rf.JSONSchema == nil || rf.JSONSchema.Name != "userrules_normalize" {
		t.Fatalf("chế độ native phải gửi schema: %+v", rf)
	}
	if got := model.lastMsgs[0].TextContent(); got != normalizerSystemPrompt {
		t.Fatalf("chế độ native không được inject schema lặp lại vào prompt:\n%s", got)
	}

	// Đầu ra có fence = vi phạm contract: báo lỗi ngay, không đi qua extractJSON, không hỏi lại.
	fenced := &nativeRulesModel{&scriptedModel{replies: []string{
		"```json\n{\"structured\":{},\"preferences\":\"x\",\"uncertain\":[]}\n```",
	}}}
	n = NewNormalizer(fenced)
	_, err = n.Normalize(t.Context(), "startup_prompt", "Rule")
	if err == nil || !strings.Contains(err.Error(), "vi phạm contract") {
		t.Fatalf("mong đợi lỗi vi phạm contract, got %v", err)
	}
	if fenced.calls != 1 {
		t.Fatalf("vi phạm contract không nên hỏi lại, thực tế %d lần", fenced.calls)
	}
}
