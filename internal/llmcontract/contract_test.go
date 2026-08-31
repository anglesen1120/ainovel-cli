package llmcontract

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/agentcore/schema"
)

type baseModel struct{}

func (baseModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return nil, nil
}

func (baseModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (baseModel) SupportsTools() bool { return true }

type capsModel struct {
	baseModel
	caps llm.Capabilities
}

func (m capsModel) Capabilities() llm.Capabilities { return m.caps }

type overrideModel struct {
	capsModel
	override *bool
}

func (m overrideModel) JSONSchemaOverride() *bool { return m.override }

type infoModel struct {
	baseModel
	info llm.ModelInfo
}

func (m infoModel) Info() llm.ModelInfo { return m.info }

type factsModel struct {
	baseModel
	facts ModelFacts
}

func (m factsModel) StructuredOutputFacts() ModelFacts { return m.facts }

func structured(jsonSchema, strict llm.Support) llm.Capabilities {
	return llm.Capabilities{
		Provider:   "prov",
		Model:      "mod",
		Structured: llm.StructuredCapabilities{JSONSchema: jsonSchema, Strict: strict},
	}
}

func boolPtr(v bool) *bool { return &v }

// TestResolveMatrix bao phủ mọi tổ hợp config ba trạng thái × capability adapter.
func TestResolveMatrix(t *testing.T) {
	cases := []struct {
		name       string
		model      agentcore.ChatModel
		wantMode   Mode
		wantSource Source
		wantStrict bool
	}{
		{"Không có interface capability nào", baseModel{}, ModePromptContract, SourceUnknown, false},
		{"adapter yes + strict yes", capsModel{caps: structured(llm.SupportYes, llm.SupportYes)}, ModeNativeJSONSchema, SourceAdapter, true},
		{"adapter yes + strict no (dạng Gemini)", capsModel{caps: structured(llm.SupportYes, llm.SupportNo)}, ModeNativeJSONSchema, SourceAdapter, false},
		{"adapter yes + strict unknown", capsModel{caps: structured(llm.SupportYes, llm.SupportUnknown)}, ModeNativeJSONSchema, SourceAdapter, false},
		{"adapter no", capsModel{caps: structured(llm.SupportNo, llm.SupportNo)}, ModePromptContract, SourceAdapter, false},
		{"adapter unknown", capsModel{caps: structured(llm.SupportUnknown, llm.SupportUnknown)}, ModePromptContract, SourceUnknown, false},
		{"config true không có thông tin capability mặc định strict", overrideModel{override: boolPtr(true)}, ModeNativeJSONSchema, SourceConfig, true},
		{"config true + adapter strict no tắt strict", overrideModel{capsModel{caps: structured(llm.SupportUnknown, llm.SupportNo)}, boolPtr(true)}, ModeNativeJSONSchema, SourceConfig, false},
		{"config false override adapter yes", overrideModel{capsModel{caps: structured(llm.SupportYes, llm.SupportYes)}, boolPtr(false)}, ModePromptContract, SourceConfig, false},
		{"config chưa cấu hình fallback adapter", overrideModel{capsModel{caps: structured(llm.SupportYes, llm.SupportYes)}, nil}, ModeNativeJSONSchema, SourceAdapter, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := Resolve(tc.model)
			if res.Mode != tc.wantMode || res.Source != tc.wantSource || res.Strict != tc.wantStrict {
				t.Fatalf("Resolve = %+v, want mode=%s source=%s strict=%v", res, tc.wantMode, tc.wantSource, tc.wantStrict)
			}
		})
	}
}

func TestResolveIdentity(t *testing.T) {
	res := Resolve(capsModel{caps: structured(llm.SupportYes, llm.SupportYes)})
	if res.Provider != "prov" || res.Model != "mod" {
		t.Fatalf("Chưa lấy được định danh caps: %+v", res)
	}
	res = Resolve(infoModel{info: llm.ModelInfo{Provider: "p2", Name: "m2"}})
	if res.Provider != "p2" || res.Model != "m2" {
		t.Fatalf("Chưa lấy được định danh fallback Info: %+v", res)
	}
}

func TestResolveUsesAtomicFactsSnapshot(t *testing.T) {
	res := Resolve(factsModel{facts: ModelFacts{
		Capabilities:       structured(llm.SupportYes, llm.SupportYes),
		Info:               llm.ModelInfo{Provider: "snapshot-provider", Name: "snapshot-model"},
		JSONSchemaOverride: boolPtr(false),
	}})
	if res.Mode != ModePromptContract || res.Source != SourceConfig {
		t.Fatalf("snapshot config false phải tắt schema native: %+v", res)
	}
	if res.Provider != "prov" || res.Model != "mod" {
		t.Fatalf("Định danh capability phải đến từ cùng snapshot: %+v", res)
	}
}

func testContract() Contract {
	return Contract{
		Name:        "test_decision",
		Description: "Contract kiểm thử",
		Schema: schema.Object(
			schema.Property("action", schema.Enum("Hành động", "a", "b")).Required(),
			schema.Property("reason", schema.String("Lý do")).Required(),
		),
	}
}

func TestPlanNativeOptions(t *testing.T) {
	opts, res := Plan(capsModel{caps: structured(llm.SupportYes, llm.SupportYes)}, testContract())
	if res.Mode != ModeNativeJSONSchema || len(opts) == 0 {
		t.Fatalf("native mode phải sinh opts: res=%+v opts=%d", res, len(opts))
	}
	cfg := agentcore.ResolveCallConfig(opts)
	rf := cfg.ResponseFormat
	if rf == nil || rf.Type != agentcore.ResponseFormatJSONSchema || rf.JSONSchema == nil {
		t.Fatalf("ResponseFormat = %+v", rf)
	}
	if rf.JSONSchema.Name != "test_decision" || rf.JSONSchema.Strict == nil || !*rf.JSONSchema.Strict {
		t.Fatalf("JSONSchema = %+v", rf.JSONSchema)
	}
}

func TestPlanPromptContractNoOptions(t *testing.T) {
	opts, res := Plan(baseModel{}, testContract())
	if res.Mode != ModePromptContract || opts != nil {
		t.Fatalf("prompt mode không được sinh opts: res=%+v opts=%v", res, opts)
	}
}

func TestPreparePromptUsesSchemaOnlyForPromptContract(t *testing.T) {
	contract := testContract()
	base := "Chỉ chịu trách nhiệm phán định hành động và lý do."

	prompt, err := PreparePrompt(base, contract, Resolution{Mode: ModePromptContract})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{base, "<output-json-schema>", `"action"`, `"required"`} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt contract thiếu %q:\n%s", want, prompt)
		}
	}

	native, err := PreparePrompt(base, contract, Resolution{Mode: ModeNativeJSONSchema})
	if err != nil {
		t.Fatal(err)
	}
	if native != base {
		t.Fatalf("native mode không được viết lại prompt ngữ nghĩa: %q", native)
	}
}

func TestPreparePromptRejectsUnmarshalableSchema(t *testing.T) {
	_, err := PreparePrompt("semantic", Contract{
		Name:   "bad",
		Schema: map[string]any{"bad": make(chan int)},
	}, Resolution{Mode: ModePromptContract})
	if err == nil || !strings.Contains(err.Error(), "marshal bad prompt schema") {
		t.Fatalf("Phải lộ lỗi serialize schema, got %v", err)
	}
}

func TestNullableCopies(t *testing.T) {
	orig := schema.String("Field nullable")
	out := Nullable(orig)
	got, ok := out["type"].([]string)
	if !ok || len(got) != 2 || got[0] != "string" || got[1] != "null" {
		t.Fatalf("Nullable type = %v", out["type"])
	}
	if orig["type"] != "string" {
		t.Fatalf("Nullable đã sửa map đầu vào: %v", orig["type"])
	}
}

func TestNullableExtendsEnumWithNull(t *testing.T) {
	orig := schema.Enum("Enum nullable", "a", "b")
	out := Nullable(orig)
	enum, ok := out["enum"].([]any)
	if !ok || len(enum) != 3 || enum[2] != nil {
		t.Fatalf("Nullable enum = %#v", out["enum"])
	}
	if _, ok := orig["enum"].([]string); !ok {
		t.Fatalf("Nullable đã sửa enum đầu vào: %#v", orig["enum"])
	}
	if err := ValidateJSON(out, []byte("null")); err != nil {
		t.Fatalf("Enum nullable phải chấp nhận null: %v", err)
	}
	if err := ValidateJSON(out, []byte(`"other"`)); err == nil {
		t.Fatal("Enum nullable không được chấp nhận string ngoài enum")
	}
}

func TestFingerprintStable(t *testing.T) {
	a, b := testContract(), testContract()
	if a.Fingerprint() != b.Fingerprint() || len(a.Fingerprint()) != 12 {
		t.Fatalf("Fingerprint cùng contract phải ổn định: %s vs %s", a.Fingerprint(), b.Fingerprint())
	}
	c := testContract()
	c.Schema = schema.Object(schema.Property("other", schema.String("x")).Required())
	if a.Fingerprint() == c.Fingerprint() {
		t.Fatalf("Fingerprint schema khác nhau không được giống nhau")
	}
}

func TestValidateJSONEnforcesStrictContract(t *testing.T) {
	contract := testContract()
	if err := ValidateJSON(contract.Schema, []byte(`{"action":"a","reason":"ok"}`)); err != nil {
		t.Fatalf("JSON hợp lệ bị từ chối: %v", err)
	}
	for name, raw := range map[string]string{
		"Thiếu required":    `{"action":"a"}`,
		"không hợp lệ enum": `{"action":"other","reason":"x"}`,
		"Sai kiểu field":    `{"action":1,"reason":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateJSON(contract.Schema, []byte(raw)); err == nil {
				t.Fatalf("Phải từ chối %s", raw)
			}
		})
	}
}

func TestValidateJSONRejectsInvalidEnumContract(t *testing.T) {
	contract := testContract()
	contract.Schema["enum"] = []any{1}
	if err := ValidateJSON(contract.Schema, []byte(`{"action":"a","reason":"ok"}`)); err == nil || !strings.Contains(err.Error(), "Hợp đồng không hợp lệ") {
		t.Fatalf("Phải lộ contract enum không hợp lệ, err=%v", err)
	}
}

func TestExtractJSONObjectBalanced(t *testing.T) {
	raw := "Tiền tố ```json\n{\"a\":\"}\",\"nested\":{\"b\":1}}\n``` hậu tố"
	want := `{"a":"}","nested":{"b":1}}`
	if got := ExtractJSONObject(raw); got != want {
		t.Fatalf("ExtractJSONObject = %q, want %q", got, want)
	}
}

type executionModel struct {
	responses []string
	stops     []agentcore.StopReason
	calls     int
	messages  []agentcore.Message
	config    agentcore.CallConfig
}

func (m *executionModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.messages = messages
	m.config = agentcore.ResolveCallConfig(opts)
	response := m.responses[m.calls]
	var stop agentcore.StopReason
	if m.calls < len(m.stops) {
		stop = m.stops[m.calls]
	}
	m.calls++
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(response)},
		StopReason: stop,
	}}, nil
}

type nativeExecutionModel struct{ *executionModel }

func (m *nativeExecutionModel) Capabilities() llm.Capabilities {
	return structured(llm.SupportYes, llm.SupportYes)
}

func TestExecutePromptModeSelfHealsSchemaViolation(t *testing.T) {
	model := &executionModel{responses: []string{
		`{}`,
		`{"action":"a","reason":"fixed"}`,
	}}
	type output struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	out, err := Execute(t.Context(), model, Request[output]{
		Contract: testContract(), SystemPrompt: "Phán định.", Payload: "Nhập", Agent: "test",
	})
	if err != nil || out.Reason != "fixed" {
		t.Fatalf("Tự phục hồi thất bại: %+v %v", out, err)
	}
	if model.calls != 2 {
		t.Fatalf("Phải thành công ở lần thứ hai, calls=%d", model.calls)
	}
	if len(model.messages) != 4 || !strings.Contains(model.messages[3].TextContent(), "$.action") {
		t.Fatalf("Hỏi lại chưa kèm lỗi Schema chính xác: %+v", model.messages)
	}
}

func TestExecuteNativeSchemaViolationFailsImmediately(t *testing.T) {
	model := &nativeExecutionModel{executionModel: &executionModel{responses: []string{`{}`}}}
	_, err := Execute(t.Context(), model, Request[map[string]any]{
		Contract: testContract(), SystemPrompt: "Phán định.", Payload: "Nhập",
	})
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != FailureContract {
		t.Fatalf("Phải trả lỗi contract native, nhận %T %v", err, err)
	}
	if model.calls != 1 {
		t.Fatalf("Vi phạm contract native không được hỏi lại, calls=%d", model.calls)
	}
}

func TestExecuteExposesModelErrorStopReason(t *testing.T) {
	model := &executionModel{
		responses: []string{`{"action":"a","reason":"unused"}`},
		stops:     []agentcore.StopReason{agentcore.StopReasonError},
	}
	_, err := Execute(t.Context(), model, Request[map[string]any]{
		Contract: testContract(), SystemPrompt: "Phán định.", Payload: "Nhập",
	})
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != FailureProtocol || !strings.Contains(err.Error(), "stop_reason=error") {
		t.Fatalf("Phải lộ lý do kết thúc lỗi của mô hình, nhận %T %v", err, err)
	}
	if model.calls != 1 {
		t.Fatalf("Kết thúc lỗi không được xem là lỗi JSON để hỏi lại, calls=%d", model.calls)
	}
}
