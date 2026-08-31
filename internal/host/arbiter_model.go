package host

import (
	"context"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// usageTrackedModel gắn theo dõi mức dùng vào lời gọi model: token/chi phí phải đi vào hệ thống ngân sách và usage,
// nếu không giới hạn ngân sách sẽ mù với chi phí, và số liệu usage trên UI sẽ không chính xác. Bản ghi danh tính dùng agentName truyền vào—
// phần nhập thuộc architect, phần phán quyết thuộc arbiter (UsageTracker tính phí vai trò chưa biết theo bảng giá Default).
type usageTrackedModel struct {
	inner     agentcore.ChatModel
	agentName string
	record    func(agentName, task string, msg agentcore.AgentMessage)
}

func newUsageTrackedModel(inner agentcore.ChatModel, agentName string, record func(string, string, agentcore.AgentMessage)) agentcore.ChatModel {
	if record == nil {
		return inner
	}
	tracked := &usageTrackedModel{inner: inner, agentName: agentName, record: record}
	if capabilities, ok := inner.(llm.CapabilityProvider); ok {
		return &capabilityUsageTrackedModel{usageTrackedModel: tracked, capabilities: capabilities}
	}
	return tracked
}

// capabilityUsageTrackedModel giữ lại các interface năng lực tùy chọn của model nền. Wrapper không được biến
// "không hỗ trợ thinking" thành "không rõ năng lực", nếu không lớp trên sẽ tạo ra tham số provider không chấp nhận.
type capabilityUsageTrackedModel struct {
	*usageTrackedModel
	capabilities llm.CapabilityProvider
}

func (m *capabilityUsageTrackedModel) Capabilities() llm.Capabilities {
	return m.capabilities.Capabilities()
}

// JSONSchemaOverride chuyển tiếp khai báo ba trạng thái config json_schema của model nền; khi inner không có
// thì trả về nil ("chưa cấu hình"), không bịa đặt năng lực.
func (m *capabilityUsageTrackedModel) JSONSchemaOverride() *bool {
	if o, ok := m.usageTrackedModel.inner.(interface{ JSONSchemaOverride() *bool }); ok {
		return o.JSONSchemaOverride()
	}
	return nil
}

func (m *capabilityUsageTrackedModel) StructuredOutputFacts() llmcontract.ModelFacts {
	if provider, ok := m.usageTrackedModel.inner.(interface {
		StructuredOutputFacts() llmcontract.ModelFacts
	}); ok {
		return provider.StructuredOutputFacts()
	}
	return llmcontract.ModelFacts{
		Capabilities:       m.Capabilities(),
		JSONSchemaOverride: m.JSONSchemaOverride(),
	}
}

func (m *usageTrackedModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	resp, err := m.inner.Generate(ctx, msgs, tools, opts...)
	if err == nil && resp != nil {
		m.record(m.agentName, "", resp.Message)
	}
	return resp, err
}

func (m *usageTrackedModel) GenerateStream(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	// Arbiter chỉ đi qua Generate; đường dẫn streaming được chuyển thẳng (nếu sau này đi qua stream, usage sẽ do phía tiêu thụ ghi bổ sung).
	return m.inner.GenerateStream(ctx, msgs, tools, opts...)
}

func (m *usageTrackedModel) SupportsTools() bool { return m.inner.SupportsTools() }
