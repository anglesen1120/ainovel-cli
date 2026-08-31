package bootstrap

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/errs"
)

const validGlobal = `{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "providers": { "openrouter": { "api_key": "sk-test-123456" } }
}`

// Ghi cấu hình toàn cục trong HOME cô lập và trả về HOME đó.
func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Trên Windows, os.UserHomeDir đọc USERPROFILE; nếu không đặt biến này sẽ đọc ~/.ainovel thật trên máy.
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ainovel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("tạo thư mục: %v", err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("ghi cấu hình toàn cục: %v", err)
		}
	}
	return home
}

// writeProjectConfig ghi cấu hình cấp dự án vào ./.ainovel/ trong thư mục làm việc hiện tại.
// Phải gọi t.Chdir đến thư mục đích trước.
func writeProjectConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(".ainovel", 0o755); err != nil {
		t.Fatalf("tạo thư mục .ainovel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".ainovel", "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("ghi cấu hình dự án: %v", err)
	}
}

// Nguyên nhân 3: ./.ainovel/config.json cấp dự án tồn tại nhưng là JSON hỏng, phải báo lỗi thay vì âm thầm quay về toàn cục.
func TestLoadConfig_CorruptProjectFailsLoud(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	// Sao chép ví dụ có thêm dấu phẩy ở cuối — dạng JSON hỏng phổ biến nhất.
	writeProjectConfig(t, `{ "model": "x", }`)

	if _, err := LoadConfig(); err == nil {
		t.Fatal(".ainovel/config.json bị hỏng phải báo lỗi nhưng đã bị âm thầm bỏ qua")
	}
}

// Toàn cục là nền ưu tiên thấp nhất: tệp hỏng không được chặn lớp ghi đè cấp dự án có ưu tiên cao hơn (bảo vệ hồi quy —
// phiên bản trước xử lý toàn cục theo fail-loud, khiến người dùng có “toàn cục hỏng + cấu hình dự án hợp lệ” bị tệp không liên quan cản trở).
func TestLoadConfig_CorruptGlobalDoesNotBlockProjectOverride(t *testing.T) {
	writeGlobal(t, `{ not json`)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("toàn cục hỏng không được chặn cấu hình dự án hợp lệ, nhận được: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("phải dùng giá trị cấu hình cấp dự án, nhận provider=%q", cfg.Provider)
	}
}

// Chỉnh sửa gần nơi dùng: khi thư mục dự án có ./.ainovel/config.json, EffectiveConfigPath trỏ đến đó (đường dẫn tuyệt đối),
// nếu không thì quay về toàn cục — /config và /model đều dựa vào đây để quyết định vị trí ghi tệp.
func TestEffectiveConfigPathPrefersProject(t *testing.T) {
	writeGlobal(t, validGlobal)

	t.Chdir(t.TempDir()) // không có cấu hình dự án
	if got := EffectiveConfigPath(); got != DefaultConfigPath() {
		t.Fatalf("khi không có cấu hình dự án phải quay về cấu hình toàn cục, nhận được %q, mong đợi %q", got, DefaultConfigPath())
	}

	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)
	wantAbs, err := filepath.Abs(filepath.Join(".ainovel", "config.json"))
	if err != nil {
		t.Fatalf("lấy đường dẫn tuyệt đối: %v", err)
	}
	if got := EffectiveConfigPath(); got != wantAbs {
		t.Fatalf("khi có cấu hình dự án phải ghi vào dự án, nhận được %q, mong đợi %q", got, wantAbs)
	}
}

// Tệp không tồn tại là trường hợp bình thường (di động/lần đầu), không được báo lỗi.
func TestLoadConfig_MissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // ~/.ainovel/config.json không tồn tại
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir()) // cũng không có ./.ainovel/config.json

	if _, err := LoadConfig(); err != nil {
		t.Fatalf("tệp cấu hình bị thiếu không được báo lỗi, nhận được: %v", err)
	}
}

// Đường đi bình thường: cấu hình toàn cục và cấp dự án được hợp nhất.
func TestLoadConfig_ValidMergeWorks(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, `{
  "model": "google/gemini-2.5-pro",
  "reasoning_effort": "high",
  "roles": {
    "writer": {
      "provider": "openrouter",
      "model": "google/gemini-2.5-flash",
      "reasoning_effort": "low"
    }
  }
}`)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("cấu hình hợp lệ không được báo lỗi: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("provider phải giữ giá trị toàn cục openrouter, nhận được %q", cfg.Provider)
	}
	if cfg.ModelName != "google/gemini-2.5-pro" {
		t.Errorf("model phải được cấp dự án ghi đè, nhận được %q", cfg.ModelName)
	}
	if cfg.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort phải được cấp dự án ghi đè, nhận được %q", cfg.ReasoningEffort)
	}
	if got := cfg.Roles["writer"].ReasoningEffort; got != "low" {
		t.Errorf("roles.writer.reasoning_effort phải được cấp dự án ghi đè, nhận được %q", got)
	}
}

func TestMergeConfig_ProviderExtraFields(t *testing.T) {
	base := Config{
		Provider:  "openrouter",
		ModelName: "google/gemini-2.5-flash",
		Providers: map[string]ProviderConfig{
			"openrouter": {
				API:    "chat",
				APIKey: "sk-test-123456",
				ExtraBody: map[string]any{
					"temperature": 0.8,
				},
				Extra: map[string]any{
					"user_agent": "base-client/1.0",
				},
			},
		},
	}
	overlay := Config{
		Providers: map[string]ProviderConfig{
			"openrouter": {
				API:     "responses",
				BaseURL: "https://proxy.example.com/v1",
				ExtraBody: map[string]any{
					"min_p": 0.05,
				},
				Extra: map[string]any{
					"user_agent": "override-client/1.0",
					"headers": map[string]any{
						"X-Custom-Client": "ainovel",
					},
				},
			},
		},
	}

	cfg := mergeConfig(base, overlay)
	pc := cfg.Providers["openrouter"]
	if pc.APIKey != "sk-test-123456" {
		t.Fatalf("APIKey = %q, phải là khóa được kế thừa", pc.APIKey)
	}
	if pc.API != "responses" {
		t.Fatalf("API = %q, phải là responses", pc.API)
	}
	if pc.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("BaseURL = %q, phải là URL bị ghi đè", pc.BaseURL)
	}
	if _, ok := pc.ExtraBody["temperature"]; ok {
		t.Fatalf("ExtraBody phải được lớp ghi đè thay thế, nhận được %#v", pc.ExtraBody)
	}
	if got := pc.ExtraBody["min_p"]; got != 0.05 {
		t.Fatalf("ExtraBody[min_p] = %#v, mong đợi 0.05", got)
	}
	if got := pc.Extra["user_agent"]; got != "override-client/1.0" {
		t.Fatalf("Extra[user_agent] = %#v, mong đợi override-client/1.0", got)
	}
	headers, ok := pc.Extra["headers"].(map[string]any)
	if !ok {
		t.Fatalf("Extra[headers] bị thiếu hoặc không hợp lệ: %#v", pc.Extra["headers"])
	}
	if got := headers["X-Custom-Client"]; got != "ainovel" {
		t.Fatalf("Extra.headers[X-Custom-Client] = %#v, mong đợi ainovel", got)
	}
}

// Nguyên nhân 2 (tái hiện cốt lõi issue #37): cấp dự án ghi đè provider nhưng không khai báo thông tin xác thực trong providers,
// ValidateBase phải báo lỗi cấu hình (thay vì cho qua rồi đổ vỡ ở tầng sâu hơn).
func TestValidateBase_ProviderOverrideWithoutCredentials(t *testing.T) {
	cfg := Config{
		Provider:  "mimo",
		ModelName: "mimo-v2.5-pro",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "sk-test-123456"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("provider thiếu thông tin xác thực phải báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("phải bọc errs.ErrConfig, nhận được: %v", err)
	}
}

func TestValidateBaseRejectsInvalidProviderAPI(t *testing.T) {
	cfg := Config{
		Provider:  "openai",
		ModelName: "gpt-5.1",
		Providers: map[string]ProviderConfig{
			"openai": {APIKey: "sk-test-123456", API: "legacy"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("API của provider không hợp lệ phải báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("phải bọc errs.ErrConfig, nhận được: %v", err)
	}
}

func TestValidateBaseRejectsProviderAPIOnNonOpenAIProvider(t *testing.T) {
	cfg := Config{
		Provider:  "anthropic",
		ModelName: "claude-sonnet-4",
		Providers: map[string]ProviderConfig{
			"anthropic": {APIKey: "sk-test-123456", API: "responses"},
		},
	}
	cfg.FillDefaults()
	err := cfg.ValidateBase()
	if err == nil {
		t.Fatal("API trong cấu hình provider không phải OpenAI phải báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("phải bọc errs.ErrConfig, nhận được: %v", err)
	}
}

// Cấu hình ví dụ phải nhất quán: sau khi bỏ chú thích vẫn là JSON hợp lệ,
// con trỏ provider cấp cao nhất không bị treo, đồng thời làm rõ “con trỏ” — đây là mẫu người dùng sao chép, tự hỏng sẽ gây rắc rối.
func TestExampleConfigIsValidAndSelfConsistent(t *testing.T) {
	if exampleConfig == "" {
		t.Fatal("go:embed không hoạt động, exampleConfig rỗng")
	}
	rootExample, err := os.ReadFile(filepath.Join("..", "..", "config.example.jsonc"))
	if err != nil {
		t.Fatalf("đọc config.example.jsonc ở thư mục gốc: %v", err)
	}
	if string(rootExample) != exampleConfig {
		t.Fatal("config.example.jsonc ở thư mục gốc không nhất quán với internal/bootstrap/config.example.jsonc")
	}
	var cfg Config
	if err := json.Unmarshal(stripJSONComments([]byte(exampleConfig)), &cfg); err != nil {
		t.Fatalf("ví dụ tích hợp sau khi bỏ chú thích không phải JSON hợp lệ (sao chép là gặp lỗi): %v", err)
	}
	if cfg.Provider == "" || cfg.ModelName == "" {
		t.Fatal("ví dụ phải cung cấp provider/model mặc định")
	}
	if _, ok := cfg.Providers[cfg.Provider]; !ok {
		t.Errorf("provider %q ở cấp cao nhất của ví dụ không trỏ đến mục trong providers — mẫu con trỏ đã bị treo", cfg.Provider)
	}
	if !contains(exampleConfig, "\u6307\u9488") {
		t.Error("ví dụ phải làm rõ “provider là con trỏ” — đừng để bẫy nhận thức của #37 quay lại")
	}
}

func TestWriteStartupError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := WriteStartupError("boom: provider not configured")
	if path == "" {
		t.Fatal("phải trả về đường dẫn tệp")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("đọc last-error.log: %v", err)
	}
	if want := "boom: provider not configured"; !contains(string(data), want) {
		t.Errorf("nhật ký phải chứa %q, thực tế: %s", want, data)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
