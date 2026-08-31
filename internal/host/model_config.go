package host

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

type APIKeyAction string

const (
	APIKeyKeep    APIKeyAction = "keep"
	APIKeyReplace APIKeyAction = "replace"
	APIKeyClear   APIKeyAction = "clear"
)

// ProviderSnapshot là cấu hình provider đã che giấu dùng cho TUI.
type ProviderSnapshot struct {
	Name           string
	Type           string
	API            string
	BaseURL        string
	Models         []bootstrap.ModelConfig
	HasAPIKey      bool
	APIKeyHint     string
	RequiresAPIKey bool
}

type ModelConfigurationSnapshot struct {
	Providers       []ProviderSnapshot
	DefaultProvider string
	DefaultModel    string
	ConfigPath      string
	References      map[string][]string
}

func (s ModelConfigurationSnapshot) ReferencesFor(provider, model string) []string {
	return append([]string(nil), s.References[modelReferenceKey(provider, model)]...)
}

// ModelConfiguration là một bản nháp cấu hình provider đơn lẻ được gửi từ /config tới Host.
// Nó chỉ mô tả định nghĩa của provider đó (giao thức/chứng thực/kho model), không gồm "hiện đang dùng cái nào" — việc chuyển đổi thuộc về /model.
type ModelConfigurationDraft struct {
	Provider     string
	Type         string
	API          string
	BaseURL      string
	Models       []bootstrap.ModelConfig
	Renames      []ModelRename
	APIKeyAction APIKeyAction
	APIKey       string
}

// ModelRename mô tả thay đổi ID của cùng một cấu hình model. Đây không phải là suy đoán kiểu "xóa cũ thêm mới",
// Host chỉ di chuyển tham chiếu default, role và fallback khi TUI gửi rõ ràng quan hệ này.
type ModelRename struct {
	From string
	To   string
}

type ConfiguredModel struct {
	Name          string
	ContextWindow int
	ContextSource bootstrap.ContextWindowSource
}

func modelReferenceKey(provider, model string) string {
	return strings.TrimSpace(provider) + "\x00" + strings.TrimSpace(model)
}

// MaskAPIKey chỉ giữ lại các đoạn đầu và cuối đủ để nhận diện chứng thực; chứng thực ngắn sẽ bị ẩn hoàn toàn.
// TUI chỉ nhận kết quả này, tuyệt đối không giữ API Key đầy đủ trong cấu hình.
func MaskAPIKey(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) == 0 {
		return ""
	}
	if len(runes) < 16 {
		return "******"
	}
	return string(runes[:4]) + "******" + string(runes[len(runes)-4:])
}

// ModelConfiguration trả về cấu hình đã che giấu, mục tiêu có thể ghi và tham chiếu model, tuyệt đối không lộ API Key hiện có.
func (h *Host) ModelConfiguration() ModelConfigurationSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()

	providers := make([]ProviderSnapshot, 0, len(h.cfg.Providers))
	for name, pc := range h.cfg.Providers {
		providers = append(providers, ProviderSnapshot{
			Name: name, Type: pc.Type, API: pc.API, BaseURL: pc.BaseURL,
			Models:    modelConfigurations(h.cfg, name, pc),
			HasAPIKey: pc.APIKey != "", APIKeyHint: MaskAPIKey(pc.APIKey),
			RequiresAPIKey: pc.RequiresAPIKey(name),
		})
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })

	refs := make(map[string][]string)
	refs[modelReferenceKey(h.cfg.Provider, h.cfg.ModelName)] = append(
		refs[modelReferenceKey(h.cfg.Provider, h.cfg.ModelName)], "default")
	for role, rc := range h.cfg.Roles {
		key := modelReferenceKey(rc.Provider, rc.Model)
		refs[key] = append(refs[key], role)
		for i, fallback := range rc.Fallbacks {
			key = modelReferenceKey(fallback.Provider, fallback.Model)
			refs[key] = append(refs[key], fmt.Sprintf("%s fallback[%d]", role, i))
		}
	}
	for key := range refs {
		sort.Strings(refs[key])
	}

	return ModelConfigurationSnapshot{
		Providers: providers, DefaultProvider: h.cfg.Provider, DefaultModel: h.cfg.ModelName,
		ConfigPath: h.configPath, References: refs,
	}
}

func modelConfigurations(cfg bootstrap.Config, provider string, pc bootstrap.ProviderConfig) []bootstrap.ModelConfig {
	models := make([]bootstrap.ModelConfig, 0)
	for _, modelName := range cfg.CandidateModels(provider) {
		model, ok := pc.ModelConfig(modelName)
		if !ok {
			model = bootstrap.ModelConfig{Name: modelName}
		}
		models = append(models, model)
	}
	return models
}

func (h *Host) ConfiguredModelOptions(provider string) []ConfiguredModel {
	h.mu.Lock()
	defer h.mu.Unlock()
	names := h.cfg.CandidateModels(provider)
	out := make([]ConfiguredModel, 0, len(names))
	for _, name := range names {
		window, source := h.cfg.ResolveContextWindow(provider, name)
		out = append(out, ConfiguredModel{Name: name, ContextWindow: window, ContextSource: source})
	}
	return out
}

type preparedProviderDraft struct {
	draft     ModelConfigurationDraft
	candidate bootstrap.Config
	provider  bootstrap.ProviderConfig
	oldModels []bootstrap.ModelConfig
}

// prepareProviderDraftLocked chuẩn hóa bản nháp từ TUI và hợp nhất nó vào bản sao cấu hình, để lưu và kiểm tra kết nối dùng chung một chuỗi kiểm tra.
func (h *Host) prepareProviderDraftLocked(draft ModelConfigurationDraft) (preparedProviderDraft, error) {
	draft.Provider = strings.TrimSpace(draft.Provider)
	draft.Type = strings.ToLower(strings.TrimSpace(draft.Type))
	draft.API = strings.ToLower(strings.TrimSpace(draft.API))
	draft.BaseURL = strings.TrimSpace(draft.BaseURL)
	draft.APIKey = strings.TrimSpace(draft.APIKey)
	if draft.Provider == "" {
		return preparedProviderDraft{}, fmt.Errorf("provider không được để trống")
	}
	if len(draft.Models) == 0 {
		return preparedProviderDraft{}, fmt.Errorf("hãy cấu hình ít nhất một model")
	}

	candidate := bootstrap.CloneConfig(h.cfg)
	pc := candidate.Providers[draft.Provider]
	oldModels := modelConfigurations(candidate, draft.Provider, pc)
	pc.Type = draft.Type
	pc.API = draft.API
	pc.BaseURL = draft.BaseURL
	configuredModels := make([]bootstrap.ModelConfig, 0, len(draft.Models))
	seen := make(map[string]bool, len(draft.Models))
	for _, model := range draft.Models {
		model.Name = strings.TrimSpace(model.Name)
		if model.Name == "" {
			return preparedProviderDraft{}, fmt.Errorf("tên model không được để trống")
		}
		if model.ContextWindow < 0 {
			return preparedProviderDraft{}, fmt.Errorf("cửa sổ ngữ cảnh của model %q không thể là số âm", model.Name)
		}
		if seen[model.Name] {
			return preparedProviderDraft{}, fmt.Errorf("model %q bị trùng", model.Name)
		}
		seen[model.Name] = true
		configuredModels = append(configuredModels, model)
	}
	pc.Models = configuredModels

	switch draft.APIKeyAction {
	case "", APIKeyKeep:
		// Giữ giá trị hiện có trong cấu hình ứng viên; với provider mới thì đương nhiên là rỗng.
	case APIKeyReplace:
		pc.APIKey = draft.APIKey
	case APIKeyClear:
		pc.APIKey = ""
	default:
		return preparedProviderDraft{}, fmt.Errorf("thao tác API Key không xác định %q", draft.APIKeyAction)
	}
	if pc.RequiresAPIKey(draft.Provider) && pc.APIKey == "" {
		return preparedProviderDraft{}, fmt.Errorf("Provider %q phải cấu hình API Key", draft.Provider)
	}

	if candidate.Providers == nil {
		candidate.Providers = make(map[string]bootstrap.ProviderConfig)
	}
	candidate.Providers[draft.Provider] = pc
	return preparedProviderDraft{draft: draft, candidate: candidate, provider: pc, oldModels: oldModels}, nil
}

// ConfigureModels kiểm tra, lưu bền vững và áp dụng nóng kho model của một provider.
func (h *Host) ConfigureModels(draft ModelConfigurationDraft) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	preparedDraft, err := h.prepareProviderDraftLocked(draft)
	if err != nil {
		return err
	}
	draft = preparedDraft.draft
	candidate := preparedDraft.candidate
	pc := preparedDraft.provider
	renames, err := validateModelRenames(draft.Renames, preparedDraft.oldModels, pc.Models)
	if err != nil {
		return err
	}
	renameModelReferences(&candidate, draft.Provider, renames)

	newNames := make(map[string]bool, len(pc.Models))
	for _, model := range pc.Models {
		newNames[model.Name] = true
	}
	// Trước khi xóa model thì kiểm tra tham chiếu: model được trỏ bởi default cấp cao nhất hoặc bất kỳ role/fallback nào thì không thể xóa,
	// hãy để người dùng chuyển đi trước trong /model — /config không còn tự chuyển default nữa.
	for _, old := range preparedDraft.oldModels {
		if newNames[old.Name] {
			continue
		}
		if _, renamed := renames[old.Name]; renamed {
			continue
		}
		if refs := h.modelReferencesLocked(draft.Provider, old.Name); len(refs) > 0 {
			return fmt.Errorf("model %q vẫn đang được %s tham chiếu, hãy chuyển trong /model trước rồi mới xóa", old.Name, strings.Join(refs, "、"))
		}
	}

	// Chỉnh sửa thông thường không làm đổi "đang dùng model nào"; chỉ đổi tên rõ ràng mới di chuyển danh tính tham chiếu của cùng một model.
	if err := candidate.ValidateBase(); err != nil {
		return err
	}
	prepared, err := bootstrap.NewModelSet(candidate)
	if err != nil {
		return fmt.Errorf("tạo client model thất bại: %w", err)
	}

	if h.configPath == "" {
		return fmt.Errorf("không thể xác định đường dẫn tệp cấu hình")
	}
	if err := h.saveModelConfigurationLocked(candidate, draft.Provider, pc, len(renames) > 0); err != nil {
		return fmt.Errorf("lưu cấu hình thất bại: %w", err)
	}

	h.models.ApplyPrepared(prepared)
	h.cfg = candidate
	// Sau khi client model được dựng lại, đẩy lại cường độ suy luận: applyThinkingLocked sẽ giới hạn giá trị có hiệu lực theo năng lực model mới của từng role,
	// còn ý định cường độ đã lưu thì giữ nguyên.
	h.applyThinkingLocked("default")
	summary := fmt.Sprintf("Cấu hình Provider đã được lưu: %s → %s", draft.Provider, h.configPath)
	if draft.Provider != h.cfg.Provider {
		summary += "；dùng /model để chuyển"
	}
	h.emitEvent(Event{
		Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: summary,
	})
	return nil
}

func validateModelRenames(requested []ModelRename, oldModels, newModels []bootstrap.ModelConfig) (map[string]string, error) {
	oldNames := make(map[string]bool, len(oldModels))
	newNames := make(map[string]bool, len(newModels))
	for _, model := range oldModels {
		oldNames[model.Name] = true
	}
	for _, model := range newModels {
		newNames[model.Name] = true
	}
	renames := make(map[string]string, len(requested))
	targets := make(map[string]bool, len(requested))
	for _, rename := range requested {
		from := strings.TrimSpace(rename.From)
		to := strings.TrimSpace(rename.To)
		if from == "" || to == "" {
			return nil, fmt.Errorf("tên cũ và tên mới khi đổi tên model không được để trống")
		}
		if from == to {
			continue
		}
		if !oldNames[from] {
			return nil, fmt.Errorf("không thể đổi tên model không tồn tại %q", from)
		}
		if !newNames[to] {
			return nil, fmt.Errorf("model đích khi đổi tên %q không nằm trong danh sách model hiện tại", to)
		}
		if _, exists := renames[from]; exists {
			return nil, fmt.Errorf("model %q bị đổi tên lặp", from)
		}
		if targets[to] {
			return nil, fmt.Errorf("không thể đồng thời đổi tên nhiều model thành %q", to)
		}
		renames[from] = to
		targets[to] = true
	}
	return renames, nil
}

func renameModelReferences(cfg *bootstrap.Config, provider string, renames map[string]string) {
	if len(renames) == 0 {
		return
	}
	if cfg.Provider == provider {
		if renamed, ok := renames[cfg.ModelName]; ok {
			cfg.ModelName = renamed
		}
	}
	for role, roleConfig := range cfg.Roles {
		changed := false
		if roleConfig.Provider == provider {
			if renamed, ok := renames[roleConfig.Model]; ok {
				roleConfig.Model = renamed
				changed = true
			}
		}
		for i := range roleConfig.Fallbacks {
			fallback := &roleConfig.Fallbacks[i]
			if fallback.Provider != provider {
				continue
			}
			if renamed, ok := renames[fallback.Model]; ok {
				fallback.Model = renamed
				changed = true
			}
		}
		if changed {
			cfg.Roles[role] = roleConfig
		}
	}
}

func (h *Host) saveModelConfigurationLocked(candidate bootstrap.Config, provider string, pc bootstrap.ProviderConfig, renamed bool) error {
	if renamed {
		// Tham chiếu và định nghĩa provider phải được ghi xuống trong cùng một lần thay thế tệp, nếu không sau khi tiến trình khởi động lại có thể chỉ thấy một nửa.
		// /model cũng dùng SaveConfig để ghi lại cấu hình hợp lệ; đổi tên kế thừa cùng một ngữ nghĩa.
		return bootstrap.SaveConfig(h.configPath, candidate)
	}
	return bootstrap.SaveProviderConfig(h.configPath, provider, pc)
}

// TestModelConnection dùng bản nháp hiện tại để tạo một client model thật và gửi yêu cầu tối thiểu.
// Nó không lưu cấu hình, không chuyển model lúc chạy, và cũng không hạ cấp sang provider khác khi thất bại.
func (h *Host) TestModelConnection(ctx context.Context, draft ModelConfigurationDraft, modelName string) error {
	h.mu.Lock()
	preparedDraft, err := h.prepareProviderDraftLocked(draft)
	h.mu.Unlock()
	if err != nil {
		return err
	}

	modelName = strings.TrimSpace(modelName)
	found := false
	for _, model := range preparedDraft.provider.Models {
		if model.Name == modelName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("model kiểm tra kết nối %q không nằm trong danh sách model hiện tại", modelName)
	}

	testConfig := preparedDraft.candidate
	testConfig.Provider = preparedDraft.draft.Provider
	testConfig.ModelName = modelName
	testConfig.Roles = nil
	if err := testConfig.ValidateBase(); err != nil {
		return err
	}
	models, err := bootstrap.NewModelSet(testConfig)
	if err != nil {
		return fmt.Errorf("tạo client model kiểm tra thất bại: %w", err)
	}
	if _, err := models.Default.Generate(ctx, []agentcore.Message{agentcore.UserMsg("Reply OK.")}, nil); err != nil {
		return fmt.Errorf("kiểm tra kết nối thất bại (%s/%s): %w", preparedDraft.draft.Provider, modelName, err)
	}
	return nil
}

func (h *Host) modelReferencesLocked(provider, model string) []string {
	var refs []string
	if h.cfg.Provider == provider && h.cfg.ModelName == model {
		refs = append(refs, "default")
	}
	for role, rc := range h.cfg.Roles {
		if rc.Provider == provider && rc.Model == model {
			refs = append(refs, role)
		}
		for i, fallback := range rc.Fallbacks {
			if fallback.Provider == provider && fallback.Model == model {
				refs = append(refs, fmt.Sprintf("%s fallback[%d]", role, i))
			}
		}
	}
	sort.Strings(refs)
	return refs
}
