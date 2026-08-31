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
		{"Giải thích tiền tố\n{\"a\":1}\nhậu tố", `"a":1`},
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
    "genre": "Đô thị",
    "forbidden_chars": [],
    "forbidden_phrases": ["ở một mức độ nào đó"],
    "fatigue_words": [{"word": "thật ra", "max_per_chapter": 2}]
  },
  "preferences": "Nhân vật chính điềm tĩnh, kiềm chế",
  "uncertain": ["Hạn chế dùng ẩn dụ: không có ngưỡng"]
}` + "\n```"
	body := llmcontract.ExtractJSONObject(raw)
	if err := llmcontract.ValidateJSON(normalizeContract.Schema, []byte(body)); err != nil {
		t.Fatalf("phải phân tích thành công: %v", err)
	}
	var out normalizerOutput
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("phải giải mã thành công: %v", err)
	}
	cand, err := out.toCandidate("startup_prompt")
	if err != nil {
		t.Fatalf("toCandidate: %v", err)
	}
	if cand.Structured.Genre != "Đô thị" {
		t.Fatalf("phân tích genre lỗi: %+v", cand.Structured)
	}
	if len(cand.Structured.ForbiddenPhrases) != 1 || cand.Structured.ForbiddenPhrases[0] != "ở một mức độ nào đó" {
		t.Fatalf("phân tích forbidden_phrases lỗi: %v", cand.Structured.ForbiddenPhrases)
	}
	if cand.Structured.FatigueWords["thật ra"] != 2 {
		t.Fatalf("mảng fatigue_words phải chuyển thành map: %v", cand.Structured.FatigueWords)
	}
	if cand.Preferences != "Nhân vật chính điềm tĩnh, kiềm chế" {
		t.Fatalf("phân tích preferences lỗi: %q", cand.Preferences)
	}
	if len(cand.Uncertain) != 1 {
		t.Fatalf("uncertain phải có 1 mục, nhận được %v", cand.Uncertain)
	}
}

// Kiểm tra mục fatigue: từ trống và ngưỡng không dương đều là lỗi nghiệp vụ có thể phản hồi để sửa.
func TestToCandidateRejectsInvalidFatigueEntries(t *testing.T) {
	bad := normalizerOutput{Structured: normalizerStructured{
		FatigueWords: []fatigueWordEntry{{Word: " ", MaxPerChapter: 2}},
	}}
	if _, err := bad.toCandidate("x"); err == nil {
		t.Fatal("mục có từ trống phải báo lỗi")
	}
	bad = normalizerOutput{Structured: normalizerStructured{
		FatigueWords: []fatigueWordEntry{{Word: "thật ra", MaxPerChapter: 0}},
	}}
	if _, err := bad.toCandidate("x"); err == nil {
		t.Fatal("ngưỡng không phải số nguyên dương phải báo lỗi")
	}
}

func TestParseNormalizerJSON_GarbageFails(t *testing.T) {
	if body := llmcontract.ExtractJSONObject("Mô hình chỉ trả về một câu, không có JSON"); body != "" {
		t.Fatal("không có JSON phải phân tích thất bại (kích hoạt hạ cấp)")
	}
	if body := llmcontract.ExtractJSONObject("{ không hoàn chỉnh"); body != "" {
		t.Fatal("JSON không hoàn chỉnh phải phân tích thất bại")
	}
}

// Kiểm tra hợp đồng (RFC §11.1): gốc là object; mọi thuộc tính, kể cả structured lồng nhau
// và các mục fatigue_words, đều bắt buộc.
func TestNormalizeContractIsStrictReady(t *testing.T) {
	if normalizeContract.Schema["type"] != "object" {
		t.Fatal("gốc phải là object")
	}
	if err := llmcontract.ValidateStrictReady(normalizeContract.Schema); err != nil {
		t.Fatal(err)
	}
}

func TestNormalize_NilModelErrors(t *testing.T) {
	// Không có mô hình: trả về lỗi rõ ràng để tầng Service hạ cấp thành raw preferences.
	var n *Normalizer = NewNormalizer(nil)
	if _, err := n.Normalize(t.Context(), "startup_prompt", "Mỗi chương 1.200 từ, nhân vật chính điềm tĩnh"); err == nil {
		t.Fatal("không có mô hình phải trả về lỗi")
	}
}

// scriptedModel là ChatModel giả tối thiểu: trả các hồi đáp định sẵn theo thứ tự gọi và ghi lại
// messages của lượt cuối để kiểm tra lần thử lại có đưa gợi ý sửa vào hội thoại kế tiếp hay không.
// Sau khi hết hồi đáp, nó lặp lại hồi đáp cuối.
type scriptedModel struct {
	replies  []string
	calls    int
	lastMsgs []agentcore.Message
	lastCfg  agentcore.CallConfig
	err      error // Khi khác nil, Generate luôn trả lỗi này.
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

// Thử lại có phản hồi: lượt đầu trả JSON hỏng, lượt hai mới hợp lệ. Normalize phải thành công,
// đồng thời hội thoại lượt hai phải có đầu ra hỏng trước đó và gợi ý sửa, không phải thử lại mù.
func TestNormalize_FeedbackRetryRecovers(t *testing.T) {
	model := &scriptedModel{replies: []string{
		"Đây không phải JSON",
		`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":["ở một mức độ nào đó"],"fatigue_words":[]},"preferences":"","uncertain":[]}`,
	}}
	n := NewNormalizer(model)

	cand, err := n.Normalize(t.Context(), "startup_prompt", "Không được xuất hiện cụm ở một mức độ nào đó")
	if err != nil {
		t.Fatalf("lượt hai đã trả JSON hợp lệ, không được thất bại: %v", err)
	}
	if len(cand.Structured.ForbiddenPhrases) != 1 {
		t.Fatalf("phải phân tích được forbidden_phrases, nhận %+v", cand.Structured)
	}
	if model.calls != 2 {
		t.Fatalf("phải thành công ở lần gọi thứ 2, thực tế gọi %d lần", model.calls)
	}

	var sawBad, sawHint bool
	for _, msg := range model.lastMsgs {
		text := msg.TextContent()
		if text == "Đây không phải JSON" {
			sawBad = true
		}
		if strings.Contains(text, "JSON Schema") && strings.Contains(text, "Lỗi:") {
			sawHint = true
		}
	}
	if !sawBad || !sawHint {
		t.Errorf("lượt hai phải có đầu ra hỏng và gợi ý sửa, sawBad=%v sawHint=%v", sawBad, sawHint)
	}
	system := model.lastMsgs[0].TextContent()
	if !strings.Contains(system, "<output-json-schema>") || !strings.Contains(system, `"fatigue_words"`) {
		t.Fatalf("prompt contract phải tự động thêm schema từ Contract:\n%s", system)
	}
}

// Chuẩn hóa không ghi đè mặc định thinking của mô hình; mô hình chat thông thường sẽ từ chối off tường minh.
func TestNormalize_LeavesThinkingUnspecifiedAndReservesTokens(t *testing.T) {
	model := &scriptedModel{replies: []string{`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`}}
	n := NewNormalizer(model)

	if _, err := n.Normalize(t.Context(), "startup_prompt", "một quy tắc bất kỳ"); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if model.lastCfg.ThinkingLevel != agentcore.ThinkingAuto {
		t.Errorf("không được gửi tham số thinking, nhận %q", model.lastCfg.ThinkingLevel)
	}
	if model.lastCfg.MaxTokens != normalizeMaxTokens {
		t.Errorf("max_tokens phải là %d, nhận %d", normalizeMaxTokens, model.lastCfg.MaxTokens)
	}
}

// JSON hỏng xuyên suốt: không giới hạn số lần cố định; tiếp tục phản hồi hỏi lại đến khi context bị hủy.
func TestNormalize_FeedbackRetryContinuesUntilContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	model := &scriptedModel{replies: []string{"hỏng"}, cancel: cancel, cancelAt: 4}
	n := NewNormalizer(model)

	_, err := n.Normalize(ctx, "startup_prompt", "Mỗi chương 1.200 từ")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context phải kết thúc vòng tự sửa, nhận %v", err)
	}
	if model.calls != 4 {
		t.Fatalf("phải tiếp tục gọi đến trước khi context bị hủy, thực tế %d", model.calls)
	}
}

type terminalTestError struct{}

func (terminalTestError) Error() string   { return "401 authentication failed" }
func (terminalTestError) Retryable() bool { return false }

type retryableTestError struct{}

func (retryableTestError) Error() string             { return "provider unavailable" }
func (retryableTestError) Retryable() bool           { return true }
func (retryableTestError) RetryAfter() time.Duration { return time.Millisecond }

// Lỗi dừng (như 401) không được thử lại mù: chỉ đúng một lần gọi rồi trả về lỗi.
func TestNormalize_TerminalErrorStopsImmediately(t *testing.T) {
	model := &scriptedModel{err: terminalTestError{}}
	n := NewNormalizer(model)

	_, err := n.Normalize(t.Context(), "startup_prompt", "quy tắc")
	if err == nil || !errors.As(err, &terminalTestError{}) {
		t.Fatalf("phải trả lỗi dừng: %v", err)
	}
	if model.calls != 1 {
		t.Fatalf("lỗi dừng không được thử lại, thực tế gọi %d lần", model.calls)
	}
}

// Lỗi yêu cầu có thể thử lại được llmretry thử lại với cơ chế lùi thời gian.
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
	cand, err := n.Normalize(t.Context(), "startup_prompt", "quy tắc")
	if err != nil || cand.Preferences != "x" {
		t.Fatalf("phải thành công sau khi lùi thời gian: %+v %v", cand, err)
	}
}

// nativeRulesModel khai báo hỗ trợ JSON Schema gốc.
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
	// Chế độ native: gửi schema theo yêu cầu; JSON trần thành công.
	model := &nativeRulesModel{&scriptedModel{replies: []string{
		`{"structured":{"genre":"","forbidden_chars":[],"forbidden_phrases":[],"fatigue_words":[]},"preferences":"x","uncertain":[]}`,
	}}}
	n := NewNormalizer(model)
	cand, err := n.Normalize(t.Context(), "startup_prompt", "quy tắc")
	if err != nil || cand.Preferences != "x" {
		t.Fatalf("chuẩn hóa native thất bại: %+v %v", cand, err)
	}
	rf := model.lastCfg.ResponseFormat
	if rf == nil || rf.JSONSchema == nil || rf.JSONSchema.Name != "userrules_normalize" {
		t.Fatalf("chế độ native phải gửi schema: %+v", rf)
	}
	if got := model.lastMsgs[0].TextContent(); got != normalizerSystemPrompt {
		t.Fatalf("chế độ native không được chèn lại schema vào prompt:\n%s", got)
	}

	// Đầu ra có hàng rào là vi phạm hợp đồng: báo lỗi ngay, không extractJSON hoặc hỏi lại.
	fenced := &nativeRulesModel{&scriptedModel{replies: []string{
		"```json\n{\"structured\":{},\"preferences\":\"x\",\"uncertain\":[]}\n```",
	}}}
	n = NewNormalizer(fenced)
	_, err = n.Normalize(t.Context(), "startup_prompt", "quy tắc")
	if err == nil || !strings.Contains(err.Error(), "Vi phạm hợp đồng Schema gốc") {
		t.Fatalf("mong đợi lỗi vi phạm hợp đồng Schema gốc, nhận %v", err)
	}
	if fenced.calls != 1 {
		t.Fatalf("vi phạm hợp đồng không được hỏi lại, thực tế gọi %d lần", fenced.calls)
	}
}
