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

// writeGlobal ghi cấu hình toàn cục trong HOME đã cô lập, rồi trả về HOME đó.
func writeGlobal(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Trên Windows, os.UserHomeDir đọc USERPROFILE; nếu không đặt nó sẽ đọc ~/.ainovel thật trên máy hiện tại.
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(home, ".ainovel")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("tạo thư mục: %v", err)
	}
	if content != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(content), 0o644); err != nil {
			t.Fatalf("write global: %v", err)
		}
	}
	return home
}

// writeProjectConfig ghi cấu hình cấp dự án vào ./.ainovel/ dưới thư mục làm việc hiện tại.
// Trước khi gọi cần t.Chdir tới thư mục đích.
func writeProjectConfig(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(".ainovel", 0o755); err != nil {
		t.Fatalf("mkdir .ainovel: %v", err)
	}
	if err := os.WriteFile(filepath.Join(".ainovel", "config.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}
}

// Nguyên nhân gốc 3: ./.ainovel/config.json cấp dự án tồn tại nhưng là JSON hỏng, phải báo lỗi, không được im lặng nuốt lỗi rồi fallback về global.
func TestLoadConfig_CorruptProjectFailsLoud(t *testing.T) {
	writeGlobal(t, validGlobal)
	proj := t.TempDir()
	t.Chdir(proj)
	// Ví dụ chép tay có thừa dấu phẩy cuối dòng -- JSON hỏng thường gặp nhất.
	writeProjectConfig(t, `{ "model": "x", }`)

	if _, err := LoadConfig(); err == nil {
		t.Fatal("./.ainovel/config.json hỏng lẽ ra phải báo lỗi, nhưng lại bị im lặng bỏ qua")
	}
}

// Global là nền tảng có độ ưu tiên thấp nhất: file hỏng không được chặn override cấp dự án có độ ưu tiên cao hơn (regression guard --
// phiên bản trước đã nhầm làm global cũng fail-loud, khiến người dùng có "global hỏng + cấu hình dự án hợp lệ" bị file không liên quan chặn lại).
func TestLoadConfig_CorruptGlobalDoesNotBlockProjectOverride(t *testing.T) {
	writeGlobal(t, `{ not json`)
	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("global hỏng không nên chặn cấu hình cấp dự án hợp lệ, nhận được: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("nên dùng giá trị của cấu hình cấp dự án, nhận được provider=%q", cfg.Provider)
	}
}

// Chỉnh sửa gần nhất: khi thư mục dự án có ./.ainovel/config.json thì EffectiveConfigPath trỏ tới nó (đường dẫn tuyệt đối),
// nếu không thì fallback về global -- /config và /model đều dựa vào đây để quyết định vị trí ghi xuống đĩa.
func TestEffectiveConfigPathPrefersProject(t *testing.T) {
	writeGlobal(t, validGlobal)

	t.Chdir(t.TempDir()) // không có cấu hình dự án
	if got := EffectiveConfigPath(); got != DefaultConfigPath() {
		t.Fatalf("không có cấu hình dự án thì nên dùng cấu hình toàn cục, nhận %q, cần %q", got, DefaultConfigPath())
	}

	proj := t.TempDir()
	t.Chdir(proj)
	writeProjectConfig(t, validGlobal)
	wantAbs, err := filepath.Abs(filepath.Join(".ainovel", "config.json"))
	if err != nil {
		t.Fatalf("đường dẫn tuyệt đối: %v", err)
	}
	if got := EffectiveConfigPath(); got != wantAbs {
		t.Fatalf("có cấu hình dự án thì nên ghi vào dự án, nhận %q, cần %q", got, wantAbs)
	}
}

// File không tồn tại là tình huống bình thường (portable/lần đầu), không được báo lỗi.
func TestLoadConfig_MissingFilesNoError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // ~/.ainovel/config.json không tồn tại
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir()) // cũng không có ./.ainovel/config.json

	if _, err := LoadConfig(); err != nil {
		t.Fatalf("thiếu file cấu hình không nên báo lỗi, nhận được: %v", err)
	}
}

// Đường dẫn bình thường: global + cấp dự án được merge và có hiệu lực.
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
		t.Fatalf("cấu hình hợp lệ không nên báo lỗi: %v", err)
	}
	if cfg.Provider != "openrouter" {
		t.Errorf("provider nên giữ giá trị global openrouter, nhận được %q", cfg.Provider)
	}
	if cfg.ModelName != "google/gemini-2.5-pro" {
		t.Errorf("model nên được cấp dự án override, nhận được %q", cfg.ModelName)
	}
	if cfg.ReasoningEffort != "high" {
		t.Errorf("reasoning_effort nên được cấp dự án override, nhận được %q", cfg.ReasoningEffort)
	}
	if got := cfg.Roles["writer"].ReasoningEffort; got != "low" {
		t.Errorf("roles.writer.reasoning_effort nên được cấp dự án override, nhận được %q", got)
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
		t.Fatalf("APIKey = %q, cần khóa được kế thừa", pc.APIKey)
	}
	if pc.API != "responses" {
		t.Fatalf("API = %q, cần responses", pc.API)
	}
	if pc.BaseURL != "https://proxy.example.com/v1" {
		t.Fatalf("BaseURL = %q, cần URL ghi đè", pc.BaseURL)
	}
	if _, ok := pc.ExtraBody["temperature"]; ok {
		t.Fatalf("ExtraBody phải được thay bằng cấu hình ghi đè, nhận %#v", pc.ExtraBody)
	}
	if got := pc.ExtraBody["min_p"]; got != 0.05 {
		t.Fatalf("ExtraBody[min_p] = %#v, cần 0.05", got)
	}
	if got := pc.Extra["user_agent"]; got != "override-client/1.0" {
		t.Fatalf("Extra[user_agent] = %#v, cần override-client/1.0", got)
	}
	headers, ok := pc.Extra["headers"].(map[string]any)
	if !ok {
		t.Fatalf("Extra[headers] thiếu hoặc không hợp lệ: %#v", pc.Extra["headers"])
	}
	if got := headers["X-Custom-Client"]; got != "ainovel" {
		t.Fatalf("Extra.headers[X-Custom-Client] = %#v, cần ainovel", got)
	}
}

// Nguyên nhân gốc 2 (tái hiện cốt lõi issue #37): cấp dự án override provider nhưng không khai báo credentials providers tương ứng,
// ValidateBase phải báo lỗi config (chứ không phải cho qua rồi crash ở sâu hơn).
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
		t.Fatal("provider thiếu credentials nên báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("nên wrap errs.ErrConfig, nhận được: %v", err)
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
		t.Fatal("provider api không hợp lệ nên báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("nên wrap errs.ErrConfig, nhận được: %v", err)
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
		t.Fatal("provider không phải OpenAI mà cấu hình api thì nên báo lỗi")
	}
	if !errors.Is(err, errs.ErrConfig) {
		t.Errorf("nên wrap errs.ErrConfig, nhận được: %v", err)
	}
}

// Cấu hình ví dụ phải tự nhất quán: sau khi bỏ comment phải là JSON hợp lệ,
// con trỏ provider ở top-level không được treo, và phải nói rõ mô hình tư duy "con trỏ" -- đây là mẫu để người dùng chép theo, tự nó hỏng thì sẽ hại người dùng.
func TestExampleConfigIsValidAndSelfConsistent(t *testing.T) {
	if exampleConfig == "" {
		t.Fatal("go:embed chưa có hiệu lực, exampleConfig rỗng")
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
		t.Fatalf("ví dụ tích hợp sau khi bỏ comment không phải JSON hợp lệ (người dùng chép theo là dính bẫy ngay): %v", err)
	}
	if cfg.Provider == "" || cfg.ModelName == "" {
		t.Fatal("ví dụ nên cung cấp provider/model mặc định")
	}
	if _, ok := cfg.Providers[cfg.Provider]; !ok {
		t.Errorf("provider top-level %q trong ví dụ không trỏ tới entry trong providers -- mẫu chính diện về con trỏ lại tự bị treo", cfg.Provider)
	}
	if !contains(exampleConfig, "con trỏ") {
		t.Error("ví dụ nên nói rõ 'provider là con trỏ' -- đừng để bẫy nhận thức của #37 quay lại")
	}
}

func TestWriteStartupError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := WriteStartupError("boom: provider chưa được cấu hình")
	if path == "" {
		t.Fatal("nên trả về đường dẫn ghi xuống đĩa")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("đọc last-error.log: %v", err)
	}
	if want := "boom: provider chưa được cấu hình"; !contains(string(data), want) {
		t.Errorf("log nên chứa %q, thực tế: %s", want, data)
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
