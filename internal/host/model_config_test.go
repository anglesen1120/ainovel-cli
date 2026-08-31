package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

func newModelConfigTestHost(t *testing.T) (*Host, string) {
	t.Helper()
	pc := bootstrap.ProviderConfig{
		Type: "openai", APIKey: "old-secret", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "old", ContextWindow: 128000}, {Name: "writer-model"}},
	}
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "old", Providers: map[string]bootstrap.ProviderConfig{"proxy": pc},
		Roles: map[string]bootstrap.RoleConfig{"writer": {
			Provider: "proxy", Model: "writer-model",
			Fallbacks: []bootstrap.ModelRef{{Provider: "proxy", Model: "old"}},
		}},
	}
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatalf("new model set: %v", err)
	}
	// Ghi một bản cấu hình ban đầu: trong môi trường production, configPath phải trỏ tới lớp cấu hình đã tồn tại, SaveProviderConfig
	// chỉ bổ sung phần providers và giữ nguyên phần còn lại; chỉ sau khi seed xong mới có thể kiểm tra thật sự rằng "lựa chọn ở tầng trên không bị đổi".
	path := filepath.Join(t.TempDir(), "config.json")
	if err := bootstrap.SaveConfig(path, cfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	return &Host{
		cfg: cfg, models: models, events: make(chan Event, 4),
		configPath: path,
	}, path
}

// Độ mạnh suy luận được lưu giữ đúng ý định gốc: sau khi đặt rõ ràng, đổi model không được hạ cấp và ghi đè lại.
func TestSetRoleThinkingPreservesIntentAcrossModelSwitch(t *testing.T) {
	h, _ := newModelConfigTestHost(t)
	if err := h.SetRoleThinking("writer", "high"); err != nil {
		t.Fatalf("set thinking: %v", err)
	}
	if got := h.cfg.Roles["writer"].ReasoningEffort; got != "high" {
		t.Fatalf("SetRoleThinking phải lưu nguyên high, nhận được %q", got)
	}
	// Đổi model của writer: ý định độ mạnh đã lưu phải giữ high, việc hạ cấp chỉ được xảy ra ở đường đẩy xuống.
	if err := h.SwitchModel("writer", "proxy", "old"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if got := h.cfg.Roles["writer"].ReasoningEffort; got != "high" {
		t.Fatalf("sau khi đổi model, thinking của writer bị ghi lại thành %q, đáng lẽ vẫn phải là high", got)
	}
}

func TestConfigureModelsRejectsDeletingReferencedModel(t *testing.T) {
	h, _ := newModelConfigTestHost(t)
	// Xóa "writer-model" đang được role writer tham chiếu (giữ lại "old" đang dùng ở tầng trên) phải bị từ chối.
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider: "proxy", Type: "openai", BaseURL: "https://example.com/v1",
		Models:       []bootstrap.ModelConfig{{Name: "old"}, {Name: "new"}},
		APIKeyAction: APIKeyKeep,
	})
	if err == nil || !strings.Contains(err.Error(), "writer") {
		t.Fatalf("mong đợi lỗi tham chiếu writer, nhận được %v", err)
	}
	provider, model, _ := h.models.CurrentSelection("default")
	if provider != "proxy" || model != "old" {
		t.Fatalf("runtime đã bị thay đổi sau khi thất bại: %s/%s", provider, model)
	}
}

// /config không còn thay default: xóa model đang dùng ở tầng trên phải bị từ chối, để người dùng chuyển đi bằng /model trước.
func TestConfigureModelsRejectsDeletingCurrentModel(t *testing.T) {
	h, _ := newModelConfigTestHost(t)
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider: "proxy", Type: "openai", BaseURL: "https://example.com/v1",
		Models:       []bootstrap.ModelConfig{{Name: "writer-model"}, {Name: "new"}},
		APIKeyAction: APIKeyKeep,
	})
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("mong đợi lỗi tham chiếu default, nhận được %v", err)
	}
}

func TestConfigureModelsPersistsAndHotApplies(t *testing.T) {
	h, path := newModelConfigTestHost(t)
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider: "proxy", Type: "openai", API: "responses", BaseURL: "https://new.example/v1",
		Models:       []bootstrap.ModelConfig{{Name: "old", ContextWindow: 640000}, {Name: "writer-model"}},
		APIKeyAction: APIKeyKeep,
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	// Lựa chọn ở tầng trên không bị /config thay đổi: vẫn là proxy/old.
	provider, model, _ := h.models.CurrentSelection("default")
	if provider != "proxy" || model != "old" {
		t.Fatalf("lựa chọn runtime đã bị thay đổi = %s/%s", provider, model)
	}
	// Áp dụng nóng cho phần provider: cửa sổ context của old được cập nhật thành 640000.
	if window, source := h.models.ResolveContextWindow("proxy", "old"); window != 640000 || source != bootstrap.CtxWindowModelConfig {
		t.Fatalf("cửa sổ runtime = %d %s", window, source)
	}
	saved, err := bootstrap.LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load saved: %v", err)
	}
	if saved.Provider != "proxy" || saved.ModelName != "old" || saved.Providers["proxy"].APIKey != "old-secret" {
		t.Fatalf("config đã lưu = %#v", saved)
	}
	if saved.Providers["proxy"].API != "responses" || saved.Providers["proxy"].BaseURL != "https://new.example/v1" {
		t.Fatalf("provider đã lưu chưa được vá = %#v", saved.Providers["proxy"])
	}
	if len(saved.Providers["proxy"].Models) != 2 || saved.Providers["proxy"].Models[0].ContextWindow != 640000 {
		t.Fatalf("models đã lưu = %#v", saved.Providers["proxy"].Models)
	}
}

// Lưu bản nháp TUI không được làm mất trạng thái ba ngã của json_schema (khóa hồi quy cho việc round-trip cả struct qua prepareProviderDraftLocked).
func TestConfigureModelsPreservesJSONSchemaTriState(t *testing.T) {
	h, path := newModelConfigTestHost(t)
	tr := true
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider: "proxy", Type: "openai", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{
			{Name: "old", ContextWindow: 128000, JSONSchema: &tr},
			{Name: "writer-model"},
		},
		APIKeyAction: APIKeyKeep,
	})
	if err != nil {
		t.Fatalf("configure: %v", err)
	}
	saved, err := bootstrap.LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load saved: %v", err)
	}
	models := saved.Providers["proxy"].Models
	if len(models) != 2 || models[0].JSONSchema == nil || !*models[0].JSONSchema {
		t.Fatalf("json_schema bị mất: %#v", models)
	}
	if models[1].JSONSchema != nil {
		t.Fatalf("model chưa cấu hình không được tự bịa ra trạng thái ba ngã: %#v", models[1])
	}
}

func TestConfigureModelsRenamesModelAndReferencesAtomically(t *testing.T) {
	h, path := newModelConfigTestHost(t)
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider: "proxy", Type: "openai", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "renamed", ContextWindow: 256000}, {Name: "writer-renamed"}},
		Renames: []ModelRename{
			{From: "old", To: "renamed"},
			{From: "writer-model", To: "writer-renamed"},
		}, APIKeyAction: APIKeyKeep,
	})
	if err != nil {
		t.Fatalf("rename model: %v", err)
	}
	if h.cfg.ModelName != "renamed" || h.cfg.Roles["writer"].Model != "writer-renamed" ||
		h.cfg.Roles["writer"].Fallbacks[0].Model != "renamed" {
		t.Fatalf("tham chiếu runtime chưa được di chuyển: default=%q writer=%#v", h.cfg.ModelName, h.cfg.Roles["writer"])
	}
	provider, model, ok := h.models.CurrentSelection("default")
	if !ok || provider != "proxy" || model != "renamed" {
		t.Fatalf("bộ model runtime chưa được di chuyển: %s/%s ok=%v", provider, model, ok)
	}
	saved, err := bootstrap.LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load saved: %v", err)
	}
	if saved.ModelName != "renamed" || saved.Roles["writer"].Model != "writer-renamed" ||
		saved.Roles["writer"].Fallbacks[0].Model != "renamed" {
		t.Fatalf("tham chiếu đã lưu chưa được di chuyển: default=%q writer=%#v", saved.ModelName, saved.Roles["writer"])
	}
	if _, ok := saved.Providers["proxy"].ModelConfig("renamed"); !ok {
		t.Fatalf("provider đã lưu thiếu model được đổi tên: %#v", saved.Providers["proxy"].Models)
	}
}

func TestConfigureModelsDoesNotGuessRenameFromDeleteAndAdd(t *testing.T) {
	h, _ := newModelConfigTestHost(t)
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider: "proxy", Type: "openai", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "renamed"}, {Name: "writer-model"}}, APIKeyAction: APIKeyKeep,
	})
	if err == nil || !strings.Contains(err.Error(), "default") {
		t.Fatalf("khi không khai báo đổi tên rõ ràng thì vẫn phải bảo vệ theo xóa, nhận được %v", err)
	}
}

func TestModelConfigurationsIncludesReferencedUnlistedModel(t *testing.T) {
	pc := bootstrap.ProviderConfig{Models: []bootstrap.ModelConfig{{Name: "listed"}}}
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "listed", Providers: map[string]bootstrap.ProviderConfig{"proxy": pc},
		Roles: map[string]bootstrap.RoleConfig{"writer": {Provider: "proxy", Model: "referenced-only"}},
	}
	models := modelConfigurations(cfg, "proxy", pc)
	if len(models) != 2 || models[0].Name != "listed" || models[1].Name != "referenced-only" {
		t.Fatalf("giao diện và kiểm tra đổi tên phải dùng chung danh sách model ứng viên đầy đủ, nhận được %#v", models)
	}
}

func TestMaskAPIKeyAndSnapshotNeverExposeFullValue(t *testing.T) {
	if got := MaskAPIKey("  sk-1234567890abcdef  "); got != "sk-1******cdef" {
		t.Fatalf("MaskAPIKey = %q", got)
	}
	if got := MaskAPIKey("short-secret"); got != "******" {
		t.Fatalf("Key ngắn phải được ẩn hoàn toàn, nhận được %q", got)
	}

	h, _ := newModelConfigTestHost(t)
	snapshot := h.ModelConfiguration()
	if len(snapshot.Providers) != 1 {
		t.Fatalf("providers = %#v", snapshot.Providers)
	}
	provider := snapshot.Providers[0]
	if provider.APIKeyHint != "******" || strings.Contains(provider.APIKeyHint, "old-secret") {
		t.Fatalf("snapshot đã lộ API Key đầy đủ: %#v", provider)
	}
}

func TestConfigureModelsRejectsMissingRequiredAPIKeyForUnusedProvider(t *testing.T) {
	h, _ := newModelConfigTestHost(t)
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider:     "anthropic",
		Models:       []bootstrap.ModelConfig{{Name: "claude-test"}},
		APIKeyAction: APIKeyKeep,
	})
	if err == nil || !strings.Contains(err.Error(), "phải cấu hình API Key") {
		t.Fatalf("Provider không dùng nhưng vẫn cần chứng thực cũng phải từ chối Key rỗng, nhận được %v", err)
	}
	if _, exists := h.cfg.Providers["anthropic"]; exists {
		t.Fatal("sau khi kiểm tra thất bại không được sửa cấu hình runtime")
	}
}

func TestModelConnectionUsesDraftWithoutSaving(t *testing.T) {
	var requestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"test","object":"chat.completion","created":1,"model":"old",
			"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()

	h, path := newModelConfigTestHost(t)
	originalURL := h.cfg.Providers["proxy"].BaseURL
	err := h.TestModelConnection(context.Background(), ModelConfigurationDraft{
		Provider: "proxy", Type: "openai", BaseURL: server.URL + "/v1",
		Models: []bootstrap.ModelConfig{{Name: "old"}}, APIKeyAction: APIKeyKeep,
	}, "old")
	if err != nil {
		t.Fatalf("test connection: %v", err)
	}
	if requestPath != "/v1/chat/completions" {
		t.Fatalf("đường dẫn request = %q", requestPath)
	}
	if got := h.cfg.Providers["proxy"].BaseURL; got != originalURL {
		t.Fatalf("kiểm tra kết nối đã sửa cấu hình runtime: %q", got)
	}
	saved, err := bootstrap.LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if got := saved.Providers["proxy"].BaseURL; got != originalURL {
		t.Fatalf("kiểm tra kết nối đã ghi vào file cấu hình: %q", got)
	}
}

func TestConfigureModelsSuggestsSwitchForNewProvider(t *testing.T) {
	h, _ := newModelConfigTestHost(t)
	err := h.ConfigureModels(ModelConfigurationDraft{
		Provider: "backup", Type: "openai", BaseURL: "https://backup.example/v1",
		Models: []bootstrap.ModelConfig{{Name: "backup-model"}}, APIKeyAction: APIKeyKeep,
	})
	if err != nil {
		t.Fatalf("configure backup: %v", err)
	}
	event := <-h.events
	if !strings.Contains(event.Summary, "dùng /model để chuyển") {
		t.Fatalf("sau khi thêm Provider không phải hiện tại thì phải nhắc chuyển, event=%q", event.Summary)
	}
}
