package bootstrap

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/voocel/agentcore/llm"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// DefaultContextWindow là kích thước cửa sổ dự phòng khi model chưa được đăng ký trong registry.
const DefaultContextWindow = 200000

// CompactRatio là ngưỡng tương đối để kích hoạt nén ngữ cảnh: tokens >= window * CompactRatio thì nén.
// 0.85 là giá trị kinh nghiệm, chừa 15% headroom cho "prompt lượt tiếp theo + kết quả công cụ lớn", đồng thời để model cửa sổ lớn
// cũng chủ động nén ở 85%, tránh chờ tới lúc chạm đầy trong cửa sổ danh nghĩa 1M (vùng suy giảm chú ý).
//
// Tỷ lệ nén không mở cho người dùng cấu hình; người dùng chỉ cấu hình context_window thực của từng model.
const CompactRatio = 0.85

// MinCompactReserve là mức sàn của ReserveTokens. Với model cửa sổ nhỏ (như qwen3:8b cục bộ 32k),
// tính reserve theo tỷ lệ 0.15 chỉ ra 4800; riêng một phản hồi công cụ commit_chapter đã có thể nhét 5-8k,
// trong khi một chương có thể 8-15k — sẽ xảy ra "nén xong lại vượt" ngay. Mốc 8000 làm dự phòng để còn nửa lượt đệm trong kịch bản xấu nhất.
const MinCompactReserve = 8000

// CompactReserveTokens tính ngược ReserveTokens từ CompactRatio và áp dụng mức sàn MinCompactReserve:
//
//	threshold = window - reserve = window * CompactRatio
//	reserve   = max(MinCompactReserve, window * (1 - CompactRatio))
//
// Dùng cho EngineConfig.ReserveTokens của agentcore.context.Engine.
func CompactReserveTokens(window int) int {
	if window <= 0 {
		return 0
	}
	reserve := window - int(float64(window)*CompactRatio)
	if reserve < MinCompactReserve {
		return MinCompactReserve
	}
	return reserve
}

// ProviderConfig định nghĩa thông tin xác thực của một nhà cung cấp LLM.
type ProviderConfig struct {
	Type    string        `json:"type,omitempty"`     // kiểu giao thức API (openai/anthropic/gemini), chỉ định khi dùng proxy tùy biến
	API     string        `json:"api,omitempty"`      // endpoint theo giao thức OpenAI: chat (mặc định) / responses
	APIKey  string        `json:"api_key,omitempty"`  // API Key
	BaseURL string        `json:"base_url,omitempty"` // API Base URL
	Models  []ModelConfig `json:"models,omitempty"`   // Danh sách model tùy chọn, dùng để hiển thị khi TUI chuyển đổi
	// ExtraBody Truyền nguyên văn tham số bổ sung cho mỗi request tới provider này(như temperature/top_p/min_p/
	// presence_penalty, hoặc key riêng nhà cung cấp như chat_template_kwargs của nvidia để bật think).
	// Endpoint tương thích OpenAI gộp nguyên văn vào request body (quy ước extra_body); người dùng tự chịu trách nhiệm về giá trị.
	ExtraBody map[string]any `json:"extra_body,omitempty"`
	// Extra Truyền nguyên văn tới cấu hình cấp provider(litellm.ProviderConfig.Extra), dùng cho HTTP
	// headers, user_agent, anthropic_beta và các tùy chọn tầng client/truyền tải.
	Extra map[string]any `json:"extra,omitempty"`
	// StreamIdleTimeout là watchdog cho trạng thái stream nhàn rỗi: quá thời gian này mà không nhận được chunk nào thì cắt luồng
	// (chuỗi Go duration, như "900s" / "15m"). Để trống mặc định 5m —giới hạn trên hợp lý cho dịch vụ cloud；
	// block đầu tiên của LocalAI/ollama và các dịch vụ của dịch vụ tự dựng suy luận chậm có thể vượt xa 5 phút，có thể nới theo provider，
	// không làm chậm kiểm tra treo của kênh khác（#79）。
	StreamIdleTimeout string `json:"stream_idle_timeout,omitempty"`
}

// ModelConfig mô tả model có thể chuyển dưới một provider và cửa sổ ngữ cảnh tùy chọn。
// để tương thích với cấu hình cũ, có thể đọc từ chuỗi JSON ("model-name") hoặc từ object;
// khi ghi lại luôn chuẩn hóa thành object。
type ModelConfig struct {
	Name          string `json:"name"`
	ContextWindow int    `json:"context_window,omitempty"`
	// JSONSchema là khai báo ba trạng thái cho đầu ra có cấu trúc native (response_format json_schema):
	// chưa cấu hình = phán đoán theo khả năng cấp model của provider adapter；true=người dùng khai báo endpoint/model này
	// hỗ trợ (request bị từ chối thì hiển thị nguyên lỗi, không âm thầm hạ cấp); false = buộc dùng prompt contract.
	// Khả năng của proxy tùy biến và gateway tổng hợp lấy theo khai báo người dùng, chương trình không dò.
	JSONSchema *bool `json:"json_schema,omitempty"`
}

func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		m.Name = legacy
		m.ContextWindow = 0
		m.JSONSchema = nil
		return nil
	}
	type modelConfigAlias ModelConfig
	var decoded modelConfigAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return fmt.Errorf("cấu hình model phải là chuỗi hoặc đối tượng: %w", err)
	}
	*m = ModelConfig(decoded)
	return nil
}

// ModelConfig trả về cấu hình rõ ràng của model chỉ định。
func (pc ProviderConfig) ModelConfig(name string) (ModelConfig, bool) {
	name = strings.TrimSpace(name)
	for _, model := range pc.Models {
		if strings.TrimSpace(model.Name) == name {
			return model, true
		}
	}
	return ModelConfig{}, false
}

// ModelJSONSchema trả về khai báo json_schema ba trạng thái của mô hình; khi chưa nằm trong models hoặc chưa được cấu hình
// thì trả về nil (phán đoán theo khả năng của adapter).
func (c Config) ModelJSONSchema(provider, model string) *bool {
	if pc, ok := c.Providers[provider]; ok {
		if mc, ok := pc.ModelConfig(model); ok {
			return mc.JSONSchema
		}
	}
	return nil
}

// defaultStreamIdleTimeout：trong trường hợp output dài + ctx dài，reasoning-aware provider
// (mimo / deepseek-r1, v.v.) ở giai đoạn suy nghĩ, nếu server phía cuối không stream reasoning delta,
// cả SSE sẽ im lặng. Watchdog mặc định của litellm là 2 phút, với 8000 chữ viết Chương thường xuyên
// kích hoạt loại bỏ nhầm; 5 phút bao phủ hầu hết các ca kiểm thử thực tế (xem thống kê thời lượng suy nghĩ plan→draft trong tasks/todo.md).
const defaultStreamIdleTimeout = 5 * time.Minute

// StreamIdleTimeoutValue phân tích thời gian chờ nhàn rỗi khi stream của provider này; để trống thì rơi về giá trị mặc định.
func (pc ProviderConfig) StreamIdleTimeoutValue() (time.Duration, error) {
	s := strings.TrimSpace(pc.StreamIdleTimeout)
	if s == "" {
		return defaultStreamIdleTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("thời lượng không hợp lệ %q (dùng thời lượng Go như \"900s\" / \"15m\")", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("phải là số dương, nhận %q", s)
	}
	return d, nil
}

// RequiresAPIKey trả về việc provider này có bắt buộc phải cấu hình api_key rõ ràng hay không.
// Quy ước:
// 1. ollama / bedrock cho phép không có key;
// 2. cấu hình có Type chỉ định rõ được xem là proxy tùy chỉnh, cho phép không có key;
// 3. các provider khác mặc định yêu cầu key, giữ kiểm tra thận trọng với giao diện được host chính thức.
func (pc ProviderConfig) RequiresAPIKey(name string) bool {
	switch name {
	case "ollama", "bedrock":
		return false
	}
	return pc.Type == ""
}

// ProviderType trả về kiểu giao thức API hợp lệ.
// Ưu tiên dùng Type rõ ràng; nếu không thì yêu cầu chính tên provider đã nằm trong registry của litellm.
func (pc ProviderConfig) ProviderType(name string) (string, error) {
	if pc.Type != "" {
		return pc.Type, nil
	}
	if llm.IsProviderRegistered(name) {
		return name, nil
	}
	return "", fmt.Errorf("provider %q thiếu type, và không nằm trong danh sách provider đã biết của litellm: %w", name, errs.ErrConfig)
}

// ModelRef biểu thị một tổ hợp provider/model.
type ModelRef struct {
	Provider string `json:"provider"` // tên provider (khóa của map Providers)
	Model    string `json:"model"`    // tên mô hình (truyền nguyên dạng, không phân tích gì cả)
}

// RoleConfig định nghĩa ghi đè mô hình cho từng vai trò.
type RoleConfig struct {
	Provider  string     `json:"provider"`            // tên provider chính (khóa của map Providers)
	Model     string     `json:"model"`               // tên model chính (truyền nguyên dạng, không phân tích)
	Fallbacks []ModelRef `json:"fallbacks,omitempty"` // danh sách provider/model dự phòng rõ ràng
	// ReasoningEffort mức suy luận của vai trò này (off/low/medium/high/xhigh/max), trống = kế thừa mặc định cấp trên.
	// được agents.ParseThinkingLevel kiểm tra rồi áp dụng, giá trị vượt cấp xem như rỗng.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// knownRoles là các tên vai trò có thể cấu hình được. Arbiter hiện không mở cấu hình theo vai trò,
// thống nhất dùng mô hình mặc định ở cấp trên (host.arbiterModel dùng models.Default).
// import_* là các núm chọn mức mô hình cho các hàm ngữ nghĩa nhập vào (docs/import-pipeline.md §13.1):
// khi chưa cấu hình thì rơi về architect, sau khi cấu hình có thể chuyển các hàm mang tính cơ học hơn sang mức rẻ hơn.
var knownRoles = map[string]bool{
	"architect":         true,
	"writer":            true,
	"editor":            true,
	"import_segment":    true,
	"import_analyze":    true,
	"import_synthesize": true,
}

// Config cấu hình ứng dụng novel.
type Config struct {
	// Trường lúc chạy (không tuần tự hóa sang JSON)
	OutputDir string `json:"-"` // thư mục gốc đầu ra

	// cấu hình LLM mặc định
	Provider  string `json:"provider"` // provider mặc định (khóa của map Providers)
	ModelName string `json:"model"`    // tên mô hình mặc định
	// ReasoningEffort mức suy luận mặc định ở cấp trên (off/low/medium/high/xhigh/max), trống = không ghi đè (giữ nguyên mặc định của mô hình/provider).
	// khi vai trò không cấu hình riêng reasoning_effort thì lùi về giá trị này.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`

	// Kho thông tin xác thực Provider
	Providers map[string]ProviderConfig `json:"providers,omitempty"`

	// Ghi đè model cấp vai trò
	Roles map[string]RoleConfig `json:"roles,omitempty"`

	// Tham số sáng tác
	Style string `json:"style,omitempty"`

	// ContextWindow là cửa sổ ngữ cảnh toàn cục bản cũ, giữ làm fallback sau context_window riêng của model
	// tương thích. Chỉ ảnh hưởng ngưỡng nén, không đổi độ dài request thực tế tới LLM API.
	ContextWindow int `json:"context_window,omitempty"`

	// chính sách ngân sách Budget cho một cuốn sách; chỉ bật khi book_usd > 0.
	Budget BudgetConfig `json:"budget,omitzero"`

	// cấu hình cảnh báo không cần người trực; mặc định bật (kênh system dùng làm phương án dự phòng).
	Notify NotifyConfig `json:"notify,omitzero"`
}

// BudgetConfig là khai báo chính sách ví tiền cho một cuốn sách của người dùng. Khi vượt ngưỡng thì dừng giống như người dùng ở thời điểm đó
// tự tay Abort——Host chỉ thay mặt thực thi, không đánh giá hành vi của mô hình (ranh giới hợp hiến của kiến trúc §10).
type BudgetConfig struct {
	BookUSD   float64 `json:"book_usd,omitempty"`   // chỉ bật khi có giá trị; 0/thiếu = không giới hạn
	WarnRatio float64 `json:"warn_ratio,omitempty"` // mức cảnh báo, mặc định 0.8
	HardStop  bool    `json:"hard_stop,omitempty"`  // true = vượt ngưỡng thì dừng ngay; mặc định đợi nhiệm vụ của sub-agent hiện tại kết thúc
}

// Enabled trả về việc chính sách ngân sách có được bật hay không.
func (b BudgetConfig) Enabled() bool { return b.BookUSD > 0 }

// NotifyConfig cấu hình kênh cảnh báo không cần người trực.
type NotifyConfig struct {
	Enabled *bool    `json:"enabled,omitempty"` // mặc định true (kênh system có thể dùng mà không cần cấu hình)
	Command string   `json:"command,omitempty"` // tùy chọn, cấu hình xong sẽ thay thế kênh system (thông báo điện thoại đi qua đây)
	Events  []string `json:"events,omitempty"`  // tùy chọn, lọc theo notify.Kinds; mặc định bật tất cả
}

// IsEnabled trả về việc cảnh báo có được bật hay không (mặc định true).
func (n NotifyConfig) IsEnabled() bool { return n.Enabled == nil || *n.Enabled }

// ValidateBase kiểm tra cấu hình cơ bản.
func (c *Config) ValidateBase() error {
	if err := validateConfigText("provider", c.Provider); err != nil {
		return err
	}
	if err := validateConfigText("model", c.ModelName); err != nil {
		return err
	}

	if c.Provider == "" {
		return fmt.Errorf("bắt buộc phải có provider: %w", errs.ErrConfig)
	}
	if c.ModelName == "" {
		return fmt.Errorf("bắt buộc phải có model: %w", errs.ErrConfig)
	}

	// provider mặc định phải có thông tin xác thực
	pc, ok := c.Providers[c.Provider]
	if !ok {
		return fmt.Errorf("provider %q chưa cấu hình thông tin xác thực trong providers; nếu trong ./.ainovel/config.json đã ghi đè provider, thì cũng phải khai báo providers.%s (gồm api_key/base_url), không được chỉ sửa provider cấp trên: %w", c.Provider, c.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(c.Provider) && pc.APIKey == "" {
		return fmt.Errorf("provider %q chưa cấu hình api_key: %w", c.Provider, errs.ErrConfig)
	}
	if err := validateProviderConfigText(c.Provider, pc); err != nil {
		return err
	}
	if err := c.validateProviderAPI("default", c.Provider, pc); err != nil {
		return err
	}
	for name, provider := range c.Providers {
		if err := validateConfigText("provider name", name); err != nil {
			return err
		}
		if err := validateProviderConfigText(name, provider); err != nil {
			return err
		}
		if err := c.validateProviderAPI(fmt.Sprintf("provider %q", name), name, provider); err != nil {
			return err
		}
	}

	// kiểm tra ghi đè vai trò
	for role, rc := range c.Roles {
		if err := validateConfigText("tên vai trò", role); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("provider của vai trò %q", role), rc.Provider); err != nil {
			return err
		}
		if err := validateConfigText(fmt.Sprintf("model của vai trò %q", role), rc.Model); err != nil {
			return err
		}
		if !knownRoles[role] {
			return fmt.Errorf("vai trò không xác định %q trong cấu hình roles (hợp lệ: architect/writer/editor/import_segment/import_analyze/import_synthesize): %w", role, errs.ErrConfig)
		}
		if rc.Provider == "" || rc.Model == "" {
			return fmt.Errorf("vai trò %q phải có cả provider và model: %w", role, errs.ErrConfig)
		}
		if err := c.validateModelRef(
			fmt.Sprintf("vai trò %q", role),
			ModelRef{Provider: rc.Provider, Model: rc.Model},
		); err != nil {
			return err
		}
		for i, fallback := range rc.Fallbacks {
			if err := validateConfigText(fmt.Sprintf("provider fallback[%d] của vai trò %q", i, role), fallback.Provider); err != nil {
				return err
			}
			if err := validateConfigText(fmt.Sprintf("model fallback[%d] của vai trò %q", i, role), fallback.Model); err != nil {
				return err
			}
			if err := c.validateModelRef(
				fmt.Sprintf("fallback[%d] của vai trò %q", i, role),
				fallback,
			); err != nil {
				return err
			}
		}
	}

	// kiểm tra chính sách ngân sách
	if c.Budget.BookUSD < 0 {
		return fmt.Errorf("budget.book_usd phải >= 0: %w", errs.ErrConfig)
	}
	if c.Budget.Enabled() && (c.Budget.WarnRatio <= 0 || c.Budget.WarnRatio >= 1) {
		return fmt.Errorf("budget.warn_ratio phải thuộc (0, 1): %w", errs.ErrConfig)
	}

	// kiểm tra cấu hình cảnh báo
	if err := validateConfigText("notify.command", c.Notify.Command); err != nil {
		return err
	}
	for _, ev := range c.Notify.Events {
		if !notify.IsKnownKind(ev) {
			return fmt.Errorf("sự kiện notify không xác định %q (hợp lệ: %s): %w", ev, strings.Join(notify.Kinds(), "/"), errs.ErrConfig)
		}
	}

	return nil
}

func validateProviderConfigText(name string, pc ProviderConfig) error {
	fields := []struct {
		label string
		value string
	}{
		{label: fmt.Sprintf("type của provider %q", name), value: pc.Type},
		{label: fmt.Sprintf("api của provider %q", name), value: pc.API},
		{label: fmt.Sprintf("api_key của provider %q", name), value: pc.APIKey},
		{label: fmt.Sprintf("base_url của provider %q", name), value: pc.BaseURL},
	}
	for _, field := range fields {
		if err := validateConfigText(field.label, field.value); err != nil {
			return err
		}
	}
	seenModels := make(map[string]bool, len(pc.Models))
	for i, model := range pc.Models {
		modelName := strings.TrimSpace(model.Name)
		if err := validateConfigText(fmt.Sprintf("name của provider %q models[%d]", name, i), model.Name); err != nil {
			return err
		}
		if modelName == "" {
			return fmt.Errorf("provider %q models[%d].name là bắt buộc: %w", name, i, errs.ErrConfig)
		}
		if seenModels[modelName] {
			return fmt.Errorf("provider %q có model trùng lặp %q: %w", name, modelName, errs.ErrConfig)
		}
		seenModels[modelName] = true
		if model.ContextWindow < 0 {
			return fmt.Errorf("provider %q model %q context_window phải >= 0: %w", name, modelName, errs.ErrConfig)
		}
	}
	switch pc.API {
	case "", "chat", "responses":
	default:
		return fmt.Errorf("provider %q api phải là chat hoặc responses: %w", name, errs.ErrConfig)
	}
	if _, err := pc.StreamIdleTimeoutValue(); err != nil {
		return fmt.Errorf("provider %q stream_idle_timeout: %w: %w", name, err, errs.ErrConfig)
	}
	return nil
}

func validateConfigText(name, value string) error {
	if utils.ContainsControl(value) {
		return fmt.Errorf("%s chứa ký tự điều khiển: %w", name, errs.ErrConfig)
	}
	return nil
}

// DefaultProviderConfig trả về cấu hình thông tin xác thực mặc định của provider.
func (c *Config) DefaultProviderConfig() ProviderConfig {
	if c.Providers == nil {
		return ProviderConfig{}
	}
	return c.Providers[c.Provider]
}

// FillDefaults điền các giá trị mặc định.
func (c *Config) FillDefaults() {
	if c.OutputDir == "" {
		c.OutputDir = filepath.Join("output", "novel")
	}
	if c.Providers == nil {
		c.Providers = make(map[string]ProviderConfig)
	}
	if c.Roles == nil {
		c.Roles = make(map[string]RoleConfig)
	}
	if c.Style == "" {
		c.Style = "default"
	}
	if c.Budget.Enabled() && c.Budget.WarnRatio == 0 {
		c.Budget.WarnRatio = 0.8
	}
}

// ContextWindowSource đánh dấu nguồn lấy giá trị cửa sổ, dùng cho log/chẩn đoán.
type ContextWindowSource string

const (
	CtxWindowModelConfig ContextWindowSource = "model_config" // mục model của provider chỉ định tường minh
	CtxWindowConfig      ContextWindowSource = "config"       // context_window cấp cao nhất cũ chỉ định tường minh
	CtxWindowRegistry    ContextWindowSource = "registry"     // khớp baseline OpenRouter
	CtxWindowDefault     ContextWindowSource = "default"      // giá trị dự phòng (proxy tùy chỉnh/model không xác định)
)

// ResolveContextWindow phân tích cửa sổ hiệu dụng dùng cho nén ngữ cảnh, theo thứ tự ưu tiên:
//  1. providers.<provider>.models[].context_window
//  2. ContextWindow cấp cao nhất cũ (tương thích cấu hình hiện có)
//  3. models.DefaultRegistry tra cứu theo tên model (baseline OpenRouter + làm mới 24h)
//  4. DefaultContextWindow dự phòng (proxy tùy chỉnh / model không xác định)
//
// Lưu ý: giá trị trả về chỉ dùng để tính ngưỡng nén, không thu nhỏ độ dài yêu cầu thực sự có thể gửi tới LLM API.
func (c Config) ResolveContextWindow(provider, modelName string) (int, ContextWindowSource) {
	if pc, ok := c.Providers[strings.TrimSpace(provider)]; ok {
		if model, found := pc.ModelConfig(modelName); found && model.ContextWindow > 0 {
			return model.ContextWindow, CtxWindowModelConfig
		}
	}
	if c.ContextWindow > 0 {
		return c.ContextWindow, CtxWindowConfig
	}
	if rw := models.DefaultRegistry().ResolveContextWindow(modelName); rw > 0 {
		return rw, CtxWindowRegistry
	}
	return DefaultContextWindow, CtxWindowDefault
}

// ResolveReasoningEffort trả về chuỗi gốc reasoning effort có hiệu lực cho một vai trò (off/low/medium/high/xhigh/max hoặc rỗng).
// Thứ tự ưu tiên: Roles[role].ReasoningEffort cấp vai trò → ReasoningEffort mặc định cấp cao nhất → "" (không ghi đè, tiếp tục dùng mặc định của model/provider).
// Khi role rỗng hoặc là "default" thì lấy trực tiếp mặc định cấp cao nhất. Tính hợp lệ của giá trị do agents.ParseThinkingLevel kiểm soát.
func (c Config) ResolveReasoningEffort(role string) string {
	if role != "" && role != "default" {
		if rc, ok := c.Roles[role]; ok && rc.ReasoningEffort != "" {
			return rc.ReasoningEffort
		}
	}
	return c.ReasoningEffort
}

// LogContextWindowChoice in ra quyết định cửa sổ của một vai trò. Khi source=default thì phát cảnh báo Warn
// model này không khớp trong registry (OpenRouter cũng chưa ghi nhận), nén ngữ cảnh về sau sẽ theo cửa sổ dự phòng
// để kích hoạt —— nếu cửa sổ thực tế của model lớn hơn, có thể chỉ định tường minh bằng context_window trong tệp cấu hình để tránh bị nén sớm và mất lịch sử.
func LogContextWindowChoice(role, model string, window int, source ContextWindowSource) {
	attrs := []any{"module", "context", "role", role, "model", model, "window", window, "source", source}
	switch source {
	case CtxWindowModelConfig:
		slog.Info("Cửa sổ ngữ cảnh (từ cấu hình model của provider)", attrs...)
	case CtxWindowDefault:
		slog.Warn("Model không nhận diện được, sử dụng cửa sổ dự phòng (có thể chỉ định tường minh tại providers.<name>.models[].context_window)", attrs...)
	case CtxWindowConfig:
		slog.Info("Cửa sổ ngữ cảnh (từ tệp cấu hình context_window)", attrs...)
	default:
		slog.Info("Cửa sổ ngữ cảnh", attrs...)
	}
}

// CandidateModels trả về danh sách model có thể chuyển đổi dưới một provider.
// Ưu tiên dùng models do provider khai báo tường minh; đồng thời bổ sung các model của provider này đã từng xuất hiện trong cấu hình hiện tại.
func (c Config) CandidateModels(provider string) []string {
	if provider == "" {
		return nil
	}

	seen := make(map[string]bool)
	models := make([]string, 0, 4)
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			return
		}
		seen[model] = true
		models = append(models, model)
	}

	if pc, ok := c.Providers[provider]; ok {
		for _, model := range pc.Models {
			add(model.Name)
		}
	}
	if c.Provider == provider {
		add(c.ModelName)
	}
	for _, rc := range c.Roles {
		if rc.Provider == provider {
			add(rc.Model)
		}
		for _, fallback := range rc.Fallbacks {
			if fallback.Provider == provider {
				add(fallback.Model)
			}
		}
	}
	return models
}

func (c Config) validateModelRef(owner string, ref ModelRef) error {
	if ref.Provider == "" || ref.Model == "" {
		return fmt.Errorf("%s phải có cả provider và model: %w", owner, errs.ErrConfig)
	}

	pc, ok := c.Providers[ref.Provider]
	if !ok {
		return fmt.Errorf("%s tham chiếu provider %q chưa được cấu hình: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if pc.RequiresAPIKey(ref.Provider) && pc.APIKey == "" {
		return fmt.Errorf("%s tham chiếu provider %q chưa có api_key: %w", owner, ref.Provider, errs.ErrConfig)
	}
	if err := c.validateProviderAPI(owner, ref.Provider, pc); err != nil {
		return err
	}
	return nil
}

func (c Config) validateProviderAPI(owner, providerName string, pc ProviderConfig) error {
	if pc.API == "" {
		return nil
	}
	providerType, err := pc.ProviderType(providerName)
	if err != nil {
		return fmt.Errorf("%s provider %q cấu hình api không thể phân tích kiểu protocol: %w", owner, providerName, err)
	}
	if strings.ToLower(strings.TrimSpace(providerType)) != "openai" {
		return fmt.Errorf("%s provider %q api chỉ hỗ trợ provider protocol OpenAI: %w", owner, providerName, errs.ErrConfig)
	}
	return nil
}
