package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

const configDirName = ".ainovel"

// DefaultConfigPath trả về đường dẫn tới file cấu hình toàn cục ~/.ainovel/config.json.
func DefaultConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName, "config.json")
}

// DefaultConfigDir trả về đường dẫn thư mục ~/.ainovel; nếu không lấy được thư mục home thì trả về chuỗi rỗng.
// Chỉ dùng cho các file không bắt buộc phải tồn tại (như cache model), sẽ không tự tạo thư mục.
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName)
}

// configDir trả về đường dẫn thư mục ~/.ainovel, nếu chưa tồn tại thì tạo mới.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("thư mục home: %w", err)
	}
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("tạo thư mục cấu hình: %w", err)
	}
	return dir, nil
}

// projectConfigPath trả về đường dẫn tương đối của file cấu hình cấp dự án ./.ainovel/config.json.
// dotdir cấp dự án phản chiếu ~/.ainovel/ toàn cục, dùng chung configDirName; được phân giải tương đối theo cwd.
func projectConfigPath() string {
	return filepath.Join(configDirName, "config.json")
}

// EffectiveConfigPath trả về file cấu hình mà thay đổi từ TUI (/config, /model) cần ghi lại:
// nếu thư mục dự án có ./.ainovel/config.json thì ghi vào đó — cùng hướng với việc đọc là dự án ghi đè toàn cục,
// bảo đảm "chỉnh đúng bản đang có hiệu lực" và sửa xong là áp dụng ngay; nếu không thì ghi vào ~/.ainovel/config.json toàn cục.
// Chỉ chỉnh file cấu hình dự án đã tồn tại, sẽ không tự tạo mới (tạo file dự án là hành động do người dùng chủ động).
func EffectiveConfigPath() string {
	rel := projectConfigPath()
	if _, err := os.Stat(rel); err == nil {
		if abs, err := filepath.Abs(rel); err == nil {
			return abs
		}
		return rel
	}
	return DefaultConfigPath()
}

// LoadConfig tải và hợp nhất cấu hình theo thứ tự ưu tiên:
//  1. ~/.ainovel/config.json (toàn cục)
//  2. ./.ainovel/config.json (ghi đè cấp dự án)
func LoadConfig() (Config, error) {
	var cfg Config

	// 1. Cấu hình toàn cục. Đây là nền tảng có mức ưu tiên thấp nhất; file lỗi được hạ cấp thành cảnh báo thay vì chặn — có thể bị cấu hình cấp dự án ghi đè;
	//    Thất bại cứng sẽ chặn người dùng có "cấu hình toàn cục lỗi + cấu hình dự án hợp lệ" ở ngoài cửa.
	if p := DefaultConfigPath(); p != "" {
		global, found, err := loadOptionalJSON(p)
		switch {
		case err != nil:
			slog.Warn("phân tích cấu hình toàn cục thất bại, đã bỏ qua (có thể bị ghi đè bởi cấp dự án)", "module", "config", "path", p, "err", err)
		case found:
			cfg = global
		}
	}

	// 2. Ghi đè cấp dự án. File lỗi thì fail loud: cấu hình người dùng chủ động đặt trong thư mục hiện tại, nếu âm thầm nuốt đi sẽ khiến
	//    "đã cấu hình nhưng không có hiệu lực" không thể điều tra (issue #37).
	project, found, err := loadOptionalJSON(projectConfigPath())
	if err != nil {
		return cfg, fmt.Errorf("phân tích cấu hình cấp dự án ./.ainovel/config.json thất bại (hãy kiểm tra cú pháp JSON): %w", err)
	}
	if found {
		cfg = mergeConfig(cfg, project)
	}

	return cfg, nil
}

// loadOptionalJSON đọc một file cấu hình tùy chọn:
//   - File không tồn tại → (zero, false, nil), để bên gọi quyết định dùng mặc định/giá trị tầng trên
//   - File tồn tại nhưng phân tích thất bại → trả về lỗi (không còn âm thầm nuốt đi nữa — nếu không cấu hình của người dùng "đã cấu hình nhưng không có hiệu lực"
//     lại không thể điều tra, chính là căn nguyên của issue #37)
func loadOptionalJSON(path string) (Config, bool, error) {
	cfg, err := loadJSONFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	return cfg, true, nil
}

// LoadConfigFile đọc một file cấu hình JSON, hỗ trợ chú thích dòng //.
// Không thực hiện bất kỳ merge nào, chỉ trả về cấu hình của chính file đó. Khi file không tồn tại thì trả về lỗi.
func LoadConfigFile(path string) (Config, error) {
	return loadJSONFile(path)
}

// loadJSONFile đọc file cấu hình JSON, hỗ trợ chú thích dòng //.
// Khi file không tồn tại thì trả về lỗi (do bên gọi quyết định có bỏ qua hay không).
func loadJSONFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cleaned := stripJSONComments(data)
	var cfg Config
	if err := json.Unmarshal(cleaned, &cfg); err != nil {
		return Config{}, fmt.Errorf("phân tích %s: %w", path, err)
	}
	return cfg, nil
}

// mergeConfig merge overlay vào base. Các trường có giá trị khác zero sẽ ghi đè, map được merge theo key.
func mergeConfig(base, overlay Config) Config {
	if overlay.Provider != "" {
		base.Provider = overlay.Provider
	}
	if overlay.ModelName != "" {
		base.ModelName = overlay.ModelName
	}
	if overlay.ReasoningEffort != "" {
		base.ReasoningEffort = overlay.ReasoningEffort
	}
	if overlay.Style != "" {
		base.Style = overlay.Style
	}
	if overlay.ContextWindow > 0 {
		base.ContextWindow = overlay.ContextWindow
	}

	// Providers: key của overlay ghi đè key cùng tên trong base
	if len(overlay.Providers) > 0 {
		if base.Providers == nil {
			base.Providers = make(map[string]ProviderConfig)
		}
		for k, v := range overlay.Providers {
			existing := base.Providers[k]
			if v.Type != "" {
				existing.Type = v.Type
			}
			if v.API != "" {
				existing.API = v.API
			}
			if v.APIKey != "" {
				existing.APIKey = v.APIKey
			}
			if v.BaseURL != "" {
				existing.BaseURL = v.BaseURL
			}
			if len(v.Models) > 0 {
				existing.Models = append([]ModelConfig(nil), v.Models...)
			}
			if len(v.ExtraBody) > 0 {
				existing.ExtraBody = cloneMap(v.ExtraBody)
			}
			if len(v.Extra) > 0 {
				existing.Extra = cloneMap(v.Extra)
			}
			base.Providers[k] = existing
		}
	}

	// Roles: key của overlay ghi đè key cùng tên trong base
	if len(overlay.Roles) > 0 {
		if base.Roles == nil {
			base.Roles = make(map[string]RoleConfig)
		}
		for k, v := range overlay.Roles {
			existing := base.Roles[k]
			if v.Provider != "" {
				existing.Provider = v.Provider
			}
			if v.Model != "" {
				existing.Model = v.Model
			}
			if len(v.Fallbacks) > 0 {
				existing.Fallbacks = append([]ModelRef(nil), v.Fallbacks...)
			}
			if v.ReasoningEffort != "" {
				existing.ReasoningEffort = v.ReasoningEffort
			}
			base.Roles[k] = existing
		}
	}

	// Budget / Notify: ghi đè nguyên khối (ngân sách/cảnh báo cấp dự án là tuyên bố chính sách độc lập, không ghép từng trường với toàn cục)
	if overlay.Budget != (BudgetConfig{}) {
		base.Budget = overlay.Budget
	}
	if overlay.Notify.Enabled != nil || overlay.Notify.Command != "" || len(overlay.Notify.Events) > 0 {
		base.Notify = overlay.Notify
	}

	return base
}

func cloneMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// CloneConfig sao chép sâu các map/slice mà cấu hình có thể sửa đổi trong runtime, tránh cấu hình ứng viên làm bẩn cấu hình hiện tại.
func CloneConfig(cfg Config) Config {
	clone := cfg
	clone.Providers = make(map[string]ProviderConfig, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		pc.Models = append([]ModelConfig(nil), pc.Models...)
		pc.Extra = cloneMap(pc.Extra)
		pc.ExtraBody = cloneMap(pc.ExtraBody)
		clone.Providers[name] = pc
	}
	clone.Roles = make(map[string]RoleConfig, len(cfg.Roles))
	for role, rc := range cfg.Roles {
		rc.Fallbacks = append([]ModelRef(nil), rc.Fallbacks...)
		clone.Roles[role] = rc
	}
	clone.Notify.Events = append([]string(nil), cfg.Notify.Events...)
	return clone
}

// SaveProviderConfig cập nhật kiểu patch thông tin xác thực và kho model của một provider trong tầng cấu hình đích.
// Chỉ động đến đoạn providers, tuyệt đối không chạm vào lựa chọn provider/model cấp đỉnh — “hiện đang dùng cái nào” thuộc về /model.
// Khi đích không tồn tại thì tạo cấu hình tối thiểu; khi đích bị hỏng thì từ chối ghi đè.
func SaveProviderConfig(path string, provider string, pc ProviderConfig) error {
	target, found, err := loadOptionalJSON(path)
	if err != nil {
		return err
	}
	if !found {
		target = Config{}
	}
	if target.Providers == nil {
		target.Providers = make(map[string]ProviderConfig)
	}
	target.Providers[provider] = pc
	return SaveConfig(path, target)
}

// stripJSONComments loại bỏ chú thích dòng // trong JSON, theo dõi trạng thái dấu nháy để tránh xóa nhầm nội dung chuỗi.
func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		if escaped {
			out = append(out, b)
			escaped = false
			continue
		}

		if inString {
			out = append(out, b)
			if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		// Không ở trong chuỗi
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}

		// Phát hiện chú thích //
		if b == '/' && i+1 < len(data) && data[i+1] == '/' {
			// Nhảy tới cuối dòng
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
			continue
		}

		out = append(out, b)
	}

	return out
}

// WriteStartupError ghi nối thêm lỗi nghiêm trọng trong giai đoạn khởi động vào ~/.ainovel/last-error.log, và trả về
// đường dẫn file đó (best-effort, khi thất bại thì trả về chuỗi rỗng). Khi nhấp đúp để khởi động, cửa sổ console sẽ theo tiến trình
// thoát mà tắt ngay, lỗi chỉ lóe qua; ghi xuống đĩa là cách duy nhất để nhóm người dùng này truy vết sau đó.
func WriteStartupError(msg string) string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "last-error.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "[%s] %s\n", time.Now().Format(time.RFC3339), msg); err != nil {
		return ""
	}
	return path
}

// SaveConfig ghi cấu hình vào đường dẫn chỉ định (định dạng JSON, thụt lề làm đẹp).
func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
