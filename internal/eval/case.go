// Package eval là bộ harness đánh giá ngoại tuyến của ainovel-cli.
//
// Nền tảng thiết kế: bộ đánh giá (chẩn đoán xác định diag, stylestat toàn sách, rubric bảy chiều) vốn đã
// tồn tại trong dự án, eval chỉ làm một lớp mỏng — điều khiển hàng loạt case, thu thập đầu ra, ánh xạ diag Finding và hợp đồng case
// thành cổng chặn, tổng hợp báo cáo. Một định nghĩa sự thật duy nhất, không viết lại phần phán định ở tầng đánh giá. Xem docs/evaluation-system.md.
//
// Hiện đã bao phủ tuyến xác định chính: cổng chặn đơn lộ trình, delta baseline/variant A/B, tổng hợp repeat và hồi quy stylestat.
// LLM Judge vẫn là lớp tuỳ chọn về sau, không được làm nhiễm cổng chặn xác định.
package eval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// caseIDPattern giới hạn case id chỉ gồm ký tự an toàn: id sẽ được ghép vào thư mục output và bị RunCase dùng RemoveAll dọn sạch,
// cấm các ký tự đường dẫn như . /, ngăn tuyệt đối việc "../" xuyên đường dẫn xóa ra ngoài workspace.
var caseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

const defaultDeltaRatio = 0.3

// Case là một mẫu đánh giá: một nhu cầu sáng tác + một nhóm khẳng định ở tầng sự thật.
type Case struct {
	ID            string   `json:"id"`
	Category      string   `json:"category"`       // Tầng đánh giá: smoke/workflow/quality/longform/recovery/steering
	Role          string   `json:"role,omitempty"` // Vai trò được kiểm thử: writer/architect/editor (trực giao với Category)
	Description   string   `json:"description,omitempty"`
	Prompt        string   `json:"prompt"`                   // Nhu cầu sáng tác của người dùng
	Style         string   `json:"style,omitempty"`          // Ghi đè phong cách cấu hình
	MaxChapters   int      `json:"max_chapters"`             // Giới hạn số chương; 0 nghĩa là chỉ chạy tới khi hoàn tất lập kế hoạch (vào writing)
	TargetPrompts []string `json:"target_prompts,omitempty"` // Tệp prompt mà case này chủ yếu xác minh (chỉ mang tính thông tin)
	Rubric        string   `json:"rubric,omitempty"`         // Bảng chấm điểm của LLM Judge (bật ở Phase 3)
	Expect        Expect   `json:"expect"`
	Gate          Gate     `json:"gate"`
}

// Expect là khẳng định hợp đồng cấp case — chỉ khai báo các kỳ vọng gắn chặt với case này mà quy tắc chung của diag không bao phủ được.
type Expect struct {
	Phase                string   `json:"phase,omitempty"`                  // phase cuối cùng mong đợi
	MinCompletedChapters int      `json:"min_completed_chapters,omitempty"` // Số chương tối thiểu phải hoàn thành
	RequiredCheckpoints  []string `json:"required_checkpoints,omitempty"`   // Dạng "chapter:1:commit" / "arc:1:1:arc_summary" / "global:layered_outline"
	NoPending            []string `json:"no_pending,omitempty"`             // Các tín hiệu phải được xóa khi kết thúc: pending_commit/pending_steer/last_commit/last_review
}

// Gate là ngưỡng cổng chặn của case này. Phiên bản hiện tại chỉ dùng MaxSeverity; các trường còn lại được giữ chỗ cho giai đoạn A/B (regression),
// vẫn phân tích nhưng không tham gia cổng chặn — giữ lại để tệp case có thể viết theo schema đầy đủ trong docs/evaluation-system.md.
type Gate struct {
	MaxSeverity string `json:"max_severity,omitempty"` // Mức nghiêm trọng cao nhất được phép của diag Finding (mặc định warning): vượt quá thì hard fail

	MaxCostDeltaRatio     *float64 `json:"max_cost_delta_ratio,omitempty"`
	MaxToolCallDeltaRatio *float64 `json:"max_tool_call_delta_ratio,omitempty"`
	StylestatRegression   string   `json:"stylestat_regression,omitempty"`
}

// Validate kiểm tra các trường bắt buộc của case.
func (c *Case) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("case thiếu id")
	}
	if !caseIDPattern.MatchString(c.ID) {
		return fmt.Errorf("case id không hợp lệ %q: chỉ cho phép chữ thường/số/dấu gạch dưới/dấu gạch nối và không chứa ký tự đường dẫn", c.ID)
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("case %q thiếu prompt", c.ID)
	}
	if c.Gate.MaxSeverity == "" {
		c.Gate.MaxSeverity = "warning"
	}
	if !validSeverity(c.Gate.MaxSeverity) {
		return fmt.Errorf("case %q có gate.max_severity không hợp lệ: %s", c.ID, c.Gate.MaxSeverity)
	}
	if c.Gate.MaxCostDeltaRatio == nil {
		c.Gate.MaxCostDeltaRatio = float64Ptr(defaultDeltaRatio)
	}
	if c.Gate.MaxToolCallDeltaRatio == nil {
		c.Gate.MaxToolCallDeltaRatio = float64Ptr(defaultDeltaRatio)
	}
	if c.Gate.StylestatRegression == "" {
		c.Gate.StylestatRegression = "warn"
	}
	if !validStylestatGate(c.Gate.StylestatRegression) {
		return fmt.Errorf("case %q có gate.stylestat_regression không hợp lệ: %s", c.ID, c.Gate.StylestatRegression)
	}
	return nil
}

func float64Ptr(v float64) *float64 { return &v }

func validStylestatGate(s string) bool {
	switch s {
	case "warn", "block", "off":
		return true
	default:
		return false
	}
}

// LoadCases tải case từ một tệp .json hoặc từ thư mục. Mọi *.json bên trong thư mục sẽ được tải đệ quy và sắp theo id.
func LoadCases(path string) ([]Case, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	var files []string
	if info.IsDir() {
		err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".json") {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	} else {
		files = []string{path}
	}

	var cases []Case
	seen := map[string]string{}
	for _, f := range files {
		c, err := loadCaseFile(f)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[c.ID]; dup {
			return nil, fmt.Errorf("case id bị trùng: %q (%s và %s)", c.ID, prev, f)
		}
		seen[c.ID] = f
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("Không tìm thấy case nào: %s", path)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func loadCaseFile(path string) (Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Case{}, err
	}
	var c Case
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields() // Gõ sai trường thì báo lỗi ngay, tránh bị bỏ qua âm thầm
	if err := dec.Decode(&c); err != nil {
		return Case{}, fmt.Errorf("Phân tích case %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Case{}, err
	}
	return c, nil
}
