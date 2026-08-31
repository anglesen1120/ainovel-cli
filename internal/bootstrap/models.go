package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// FailoverEvent biểu thị một lần chuyển provider rõ ràng.
// Reason là nhãn ngắn (rate_limit / timeout / stream_idle / network), dùng cho log có cấu trúc.
type FailoverEvent struct {
	Role         string
	Reason       string
	FromProvider string
	FromModel    string
	ToProvider   string
	ToModel      string
	Err          error
}

// FailoverReporter được gọi khi xảy ra chuyển đổi rõ ràng.
type FailoverReporter func(FailoverEvent)

type modelTarget struct {
	provider   string
	name       string
	model      agentcore.ChatModel
	jsonSchema *bool
}

// SwappableModel là bộ bọc ChatModel có thể hot-swap.
// Các yêu cầu đã bắt đầu tiếp tục dùng instance cũ; các yêu cầu sau tự động chuyển sang instance mới.
type SwappableModel struct {
	*agentcore.SwappableModel
	mu       sync.RWMutex
	provider string
	name     string
	// jsonSchema là khai báo ba trạng thái json_schema của config cho mô hình đang được chọn, cùng với provider/name
	// được chuyển đổi nguyên tử dưới cùng một lock; llmcontract.Resolve đọc trực tiếp mỗi lần qua interface khớp theo cấu trúc.
	jsonSchema *bool
}

func NewSwappableModel(provider, name string, model agentcore.ChatModel, jsonSchema *bool) *SwappableModel {
	return &SwappableModel{
		SwappableModel: agentcore.NewSwappableModel(model),
		provider:       provider,
		name:           name,
		jsonSchema:     jsonSchema,
	}
}

func (m *SwappableModel) ProviderName() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider
}

func (m *SwappableModel) Info() llm.ModelInfo {
	return m.StructuredOutputFacts().Info
}

// StructuredOutputFacts đọc instance mô hình, danh tính và ghi đè cấu hình dưới cùng một lock, bảo đảm mỗi lần
// lựa chọn giao thức có cấu trúc chỉ quan sát được một phiên bản hoàn chỉnh.
func (m *SwappableModel) StructuredOutputFacts() llmcontract.ModelFacts {
	m.mu.RLock()
	defer m.mu.RUnlock()
	current := m.SwappableModel.Current()
	facts := llmcontract.ModelFacts{
		Info:               llm.ModelInfo{Name: m.name, Provider: m.provider},
		JSONSchemaOverride: cloneBoolPtr(m.jsonSchema),
	}
	if cp, ok := current.(llm.CapabilityProvider); ok {
		facts.Capabilities = cp.Capabilities()
	}
	if info, ok := current.(interface{ Info() llm.ModelInfo }); ok {
		modelInfo := info.Info()
		if modelInfo.Name == "" {
			modelInfo.Name = m.name
		}
		if modelInfo.Provider == "" {
			modelInfo.Provider = m.provider
		}
		facts.Info = modelInfo
	}
	return facts
}

func (m *SwappableModel) Capabilities() llm.Capabilities {
	return m.StructuredOutputFacts().Capabilities
}

func (m *SwappableModel) Swap(provider, name string, model agentcore.ChatModel, jsonSchema *bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.SwappableModel.Swap(model)
	m.provider = provider
	m.name = name
	m.jsonSchema = jsonSchema
}

// JSONSchemaOverride trả về khai báo ba trạng thái json_schema của config cho mô hình đang được chọn hiện tại.
func (m *SwappableModel) JSONSchemaOverride() *bool {
	return m.StructuredOutputFacts().JSONSchemaOverride
}

func cloneBoolPtr(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (m *SwappableModel) Current() (provider, name string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.provider, m.name
}

// ModelSet giữ các instance mô hình được phân theo vai trò, vai trò chưa cấu hình sẽ quay về mô hình mặc định.
type ModelSet struct {
	mu        sync.RWMutex
	Default   *SwappableModel
	models    map[string]*SwappableModel
	fallbacks map[string][]modelTarget
	config    Config
}

// ForRole trả về mô hình của vai trò được chỉ định, nếu chưa cấu hình thì trả về mô hình mặc định.
func (ms *ModelSet) ForRole(role string) agentcore.ChatModel {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if m, ok := ms.models[role]; ok {
		return m
	}
	return ms.Default
}

// ForRoleWithFailover trả về mô hình của vai trò với fallback ở mức một yêu cầu.
// Chỉ có hiệu lực khi vai trò đó được cấu hình fallbacks rõ ràng; nếu chưa cấu hình thì lui về mô hình thường.
func (ms *ModelSet) ForRoleWithFailover(role string, report FailoverReporter) agentcore.ChatModel {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	primary, ok := ms.models[role]
	if !ok {
		return ms.Default
	}
	targets := ms.fallbacks[role]
	if len(targets) == 0 {
		return primary
	}
	return &failoverModel{
		role: role, primary: primary, set: ms, report: report,
	}
}

// Summary trả về tóm tắt phân bổ mô hình (dùng cho log).
func (ms *ModelSet) Summary() string {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	var parts []string
	for role, m := range ms.models {
		provider, name := m.Current()
		parts = append(parts, fmt.Sprintf("%s=%s/%s", role, provider, name))
	}
	if len(parts) == 0 {
		provider, name := ms.Default.Current()
		return fmt.Sprintf("default=%s/%s", provider, name)
	}
	provider, name := ms.Default.Current()
	return fmt.Sprintf("default=%s/%s %s", provider, name, strings.Join(parts, " "))
}

// CurrentSelection trả về provider/model đang có hiệu lực hiện tại của vai trò.
// khi role rỗng hoặc "default" thì trả về mô hình mặc định.
func (ms *ModelSet) CurrentSelection(role string) (provider, model string, explicit bool) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	if role == "" || role == "default" {
		provider, model = ms.Default.Current()
		return provider, model, true
	}
	if sw, ok := ms.models[role]; ok {
		provider, model = sw.Current()
		return provider, model, true
	}
	provider, model = ms.Default.Current()
	return provider, model, false
}

// Swap chuyển đổi mô hình mặc định hoặc mô hình của vai trò được chỉ định.
// khi role rỗng hoặc "default" thì chuyển mô hình mặc định; các vai trò khác chuyển thành ghi đè rõ ràng.
func (ms *ModelSet) Swap(role, provider, model string) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	pc, ok := ms.config.Providers[provider]
	if !ok {
		return fmt.Errorf("provider %q chưa được cấu hình: %w", provider, errs.ErrConfig)
	}
	next, err := createModelFromConfig(provider, model, pc, make(map[string]agentcore.ChatModel))
	if err != nil {
		return fmt.Errorf("chuyển đổi mô hình thất bại: %w", err)
	}

	jsonSchema := ms.config.ModelJSONSchema(provider, model)
	if role == "" || role == "default" {
		ms.Default.Swap(provider, model, next, jsonSchema)
		ms.config.Provider = provider
		ms.config.ModelName = model
		return nil
	}

	if !knownRoles[role] {
		return fmt.Errorf("vai trò không xác định %q: %w", role, errs.ErrConfig)
	}

	if existing, ok := ms.models[role]; ok {
		existing.Swap(provider, model, next, jsonSchema)
	} else {
		ms.models[role] = NewSwappableModel(provider, model, next, jsonSchema)
	}
	if ms.config.Roles == nil {
		ms.config.Roles = make(map[string]RoleConfig)
	}
	rc := ms.config.Roles[role]
	rc.Provider = provider
	rc.Model = model
	ms.config.Roles[role] = rc
	return nil
}

// ResolveContextWindow sử dụng cấu hình mới nhất của ModelSet để phân tích cửa sổ, phục vụ cho
// ContextManagerFactory sử dụng, tránh bắt giữ bản sao Config lúc khởi động.
func (ms *ModelSet) ResolveContextWindow(provider, model string) (int, ContextWindowSource) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return ms.config.ResolveContextWindow(provider, model)
}

// ApplyPrepared gửi một ModelSet ứng viên đã được xây dựng thành công. Địa chỉ của SwappableModel hiện có
// được giữ nguyên, vì vậy Worker/Arbiter đã lắp đặt sẽ tự động dùng client mới ở lần yêu cầu tiếp theo.
func (ms *ModelSet) ApplyPrepared(candidate *ModelSet) {
	if candidate == nil {
		return
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()

	defaultProvider, defaultName := candidate.Default.Current()
	ms.Default.Swap(defaultProvider, defaultName, candidate.Default.SwappableModel.Current(), candidate.Default.JSONSchemaOverride())

	nextModels := make(map[string]*SwappableModel, len(candidate.models))
	for role, next := range candidate.models {
		provider, name := next.Current()
		if existing, ok := ms.models[role]; ok {
			existing.Swap(provider, name, next.SwappableModel.Current(), next.JSONSchemaOverride())
			nextModels[role] = existing
		} else {
			nextModels[role] = next
		}
	}
	ms.models = nextModels
	ms.fallbacks = candidate.fallbacks
	ms.config = CloneConfig(candidate.config)
}

func (ms *ModelSet) fallbackTargets(role string) []modelTarget {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return append([]modelTarget(nil), ms.fallbacks[role]...)
}

// ModelName trích xuất tên mô hình hiện tại từ ChatModel, nếu thất bại trả về chuỗi rỗng.
// Hỗ trợ hot-swap của SwappableModel: khi gọi luôn trả về giá trị mới nhất.
func ModelName(m agentcore.ChatModel) string {
	if info, ok := m.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info().Name
	}
	return ""
}

// ModelProvider trích xuất tên provider hiện tại từ ChatModel, nếu thất bại trả về chuỗi rỗng.
func ModelProvider(m agentcore.ChatModel) string {
	if info, ok := m.(interface{ Info() llm.ModelInfo }); ok {
		return info.Info().Provider
	}
	if provider, ok := m.(interface{ ProviderName() string }); ok {
		return provider.ProviderName()
	}
	return ""
}

// NewModelSet tạo tập hợp nhiều mô hình dựa trên cấu hình.
// Tổ hợp provider+model giống nhau sẽ tái sử dụng cùng một instance.
func NewModelSet(cfg Config) (*ModelSet, error) {
	cache := make(map[string]agentcore.ChatModel)

	// tạo mô hình mặc định
	defaultPC := cfg.DefaultProviderConfig()
	defaultModel, err := createModelFromConfig(cfg.Provider, cfg.ModelName, defaultPC, cache)
	if err != nil {
		return nil, fmt.Errorf("model mặc định: %w", err)
	}

	ms := &ModelSet{
		Default:   NewSwappableModel(cfg.Provider, cfg.ModelName, defaultModel, cfg.ModelJSONSchema(cfg.Provider, cfg.ModelName)),
		models:    make(map[string]*SwappableModel),
		fallbacks: make(map[string][]modelTarget),
		config:    cfg,
	}

	// tạo mô hình ghi đè theo vai trò
	for role, rc := range cfg.Roles {
		pc, ok := cfg.Providers[rc.Provider]
		if !ok {
			return nil, fmt.Errorf("vai trò %s tham chiếu provider không xác định %q: %w", role, rc.Provider, errs.ErrConfig)
		}
		m, err := createModelFromConfig(rc.Provider, rc.Model, pc, cache)
		if err != nil {
			return nil, fmt.Errorf("model của vai trò %s: %w", role, err)
		}
		ms.models[role] = NewSwappableModel(rc.Provider, rc.Model, m, cfg.ModelJSONSchema(rc.Provider, rc.Model))
		slog.Info("phân bổ mô hình theo vai trò", "module", "config", "role", role, "provider", rc.Provider, "model", rc.Model)
		if len(rc.Fallbacks) == 0 {
			continue
		}

		targets := make([]modelTarget, 0, len(rc.Fallbacks))
		for _, fallback := range rc.Fallbacks {
			fpc, ok := cfg.Providers[fallback.Provider]
			if !ok {
				return nil, fmt.Errorf("fallback của vai trò %s tham chiếu provider không xác định %q: %w", role, fallback.Provider, errs.ErrConfig)
			}
			fm, err := createModelFromConfig(fallback.Provider, fallback.Model, fpc, cache)
			if err != nil {
				return nil, fmt.Errorf("fallback của vai trò %s %s/%s: %w", role, fallback.Provider, fallback.Model, err)
			}
			targets = append(targets, modelTarget{
				provider:   fallback.Provider,
				name:       fallback.Model,
				model:      fm,
				jsonSchema: cfg.ModelJSONSchema(fallback.Provider, fallback.Model),
			})
		}
		ms.fallbacks[role] = targets
	}

	return ms, nil
}

// createModelFromConfig tạo hoặc tái sử dụng instance ChatModel.
func createModelFromConfig(providerKey, model string, pc ProviderConfig, cache map[string]agentcore.ChatModel) (agentcore.ChatModel, error) {
	cacheKey := providerKey + "|" + model
	if m, ok := cache[cacheKey]; ok {
		return m, nil
	}

	providerType, err := pc.ProviderType(providerKey)
	if err != nil {
		return nil, fmt.Errorf("phân tích kiểu provider thất bại: %w", err)
	}
	providerExtra := cloneMap(pc.Extra)
	if pc.API != "" {
		if providerExtra == nil {
			providerExtra = make(map[string]any, 1)
		}
		providerExtra["api"] = pc.API
	}

	streamIdle, err := pc.StreamIdleTimeoutValue()
	if err != nil {
		return nil, fmt.Errorf("provider %s stream_idle_timeout: %w: %w", providerKey, errs.ErrConfig, err)
	}

	m, err := llm.NewModel(providerType, model,
		llm.WithAPIKey(pc.APIKey),
		llm.WithBaseURL(pc.BaseURL),
		llm.WithStreamIdleTimeout(streamIdle),
		llm.WithProviderExtra(providerExtra),
		llm.WithExtra(pc.ExtraBody),
	)
	if err != nil {
		return nil, fmt.Errorf("provider %s (%s): %w: %w", providerKey, providerType, errs.ErrProvider, err)
	}
	cache[cacheKey] = m
	return m, nil
}

type failoverModel struct {
	role    string
	primary *SwappableModel
	set     *ModelSet
	report  FailoverReporter
}

func (m *failoverModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	current := m.currentTarget()
	resp, err := current.model.Generate(ctx, messages, tools, opts...)
	if err == nil {
		return resp, nil
	}

	next, reason, ok := m.pickFallback(current, err, requestsJSONSchema(opts))
	if !ok {
		return nil, err
	}
	m.reportFailover(current, next, reason, err)
	return next.model.Generate(ctx, messages, tools, opts...)
}

func (m *failoverModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	out := make(chan agentcore.StreamEvent, 100)

	go func() {
		defer close(out)

		current := m.currentTarget()
		fallbackUsed := false

	retry:
		source, resp, err := m.startAttempt(ctx, current, messages, tools, opts...)
		if err != nil {
			if !fallbackUsed {
				if next, reason, ok := m.pickFallback(current, err, requestsJSONSchema(opts)); ok {
					fallbackUsed = true
					m.reportFailover(current, next, reason, err)
					current = next
					goto retry
				}
			}
			out <- agentcore.StreamEvent{Type: agentcore.StreamEventError, Err: err}
			return
		}
		if resp != nil {
			out <- agentcore.StreamEvent{
				Type:       agentcore.StreamEventDone,
				Message:    resp.Message,
				StopReason: resp.Message.StopReason,
			}
			return
		}

		forwarded := false
		for ev := range source {
			switch ev.Type {
			case agentcore.StreamEventError:
				if ev.Err != nil && !forwarded && !fallbackUsed {
					if next, reason, ok := m.pickFallback(current, ev.Err, requestsJSONSchema(opts)); ok {
						fallbackUsed = true
						m.reportFailover(current, next, reason, ev.Err)
						current = next
						goto retry
					}
				}
				out <- ev
				return
			case agentcore.StreamEventDone:
				out <- ev
				return
			default:
				forwarded = true
				out <- ev
			}
		}
	}()

	return out, nil
}

func (m *failoverModel) SupportsTools() bool {
	return m.primary != nil && m.primary.SupportsTools()
}

func (m *failoverModel) ProviderName() string {
	if m.primary == nil {
		return ""
	}
	return m.primary.ProviderName()
}

func (m *failoverModel) Info() llm.ModelInfo {
	if m.primary == nil {
		return llm.ModelInfo{}
	}
	return m.primary.Info()
}

func (m *failoverModel) Capabilities() llm.Capabilities {
	return m.StructuredOutputFacts().Capabilities
}

func (m *failoverModel) JSONSchemaOverride() *bool {
	return m.StructuredOutputFacts().JSONSchemaOverride
}

func (m *failoverModel) StructuredOutputFacts() llmcontract.ModelFacts {
	if m.primary == nil {
		return llmcontract.ModelFacts{}
	}
	return m.primary.StructuredOutputFacts()
}

func (m *failoverModel) currentTarget() modelTarget {
	if m.primary == nil {
		return modelTarget{}
	}
	provider, name := m.primary.Current()
	return modelTarget{
		provider:   provider,
		name:       name,
		model:      m.primary,
		jsonSchema: m.primary.JSONSchemaOverride(),
	}
}

func (m *failoverModel) pickFallback(current modelTarget, err error, requireJSONSchema bool) (modelTarget, string, bool) {
	if err == nil || current.model == nil {
		return modelTarget{}, "", false
	}
	if errors.Is(err, context.Canceled) {
		return modelTarget{}, "", false
	}

	if !agentcore.IsFailoverEligible(err) {
		return modelTarget{}, agentcore.FailoverReason(err), false
	}
	reason := agentcore.FailoverReason(err)
	var targets []modelTarget
	if m.set != nil {
		targets = m.set.fallbackTargets(m.role)
	}
	for _, target := range targets {
		if target.provider == current.provider && target.name == current.name {
			continue
		}
		if target.model == nil {
			continue
		}
		if requireJSONSchema && !supportsJSONSchema(target) {
			continue
		}
		return target, reason, true
	}
	return modelTarget{}, reason, false
}

func requestsJSONSchema(opts []agentcore.CallOption) bool {
	format := agentcore.ResolveCallConfig(opts).ResponseFormat
	return format != nil && format.Type == agentcore.ResponseFormatJSONSchema
}

func supportsJSONSchema(target modelTarget) bool {
	if target.jsonSchema != nil {
		return *target.jsonSchema
	}
	cp, ok := target.model.(llm.CapabilityProvider)
	return ok && cp.Capabilities().Structured.JSONSchema == llm.SupportYes
}

func (m *failoverModel) reportFailover(from, to modelTarget, reason string, err error) {
	if m.report != nil {
		m.report(FailoverEvent{
			Role:         m.role,
			Reason:       reason,
			FromProvider: from.provider,
			FromModel:    from.name,
			ToProvider:   to.provider,
			ToModel:      to.name,
			Err:          err,
		})
	}
}

func (m *failoverModel) startAttempt(ctx context.Context, target modelTarget, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, *agentcore.LLMResponse, error) {
	if target.model == nil {
		return nil, nil, fmt.Errorf("chưa cấu hình model")
	}

	streamCh, err := target.model.GenerateStream(ctx, messages, tools, opts...)
	if err == nil {
		return streamCh, nil, nil
	}

	resp, genErr := target.model.Generate(ctx, messages, tools, opts...)
	if genErr != nil {
		return nil, nil, genErr
	}
	return nil, resp, nil
}
