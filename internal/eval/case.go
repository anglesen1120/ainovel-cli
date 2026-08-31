// Package eval là harness đánh giá ngoại tuyến của ainovel-cli.
//
// cơ sở thiết kế：bộ đánh giá（ diag、 stylestat、 rubric）
// đã tồn tại，eval ——điều khiển case、、 diag Finding  case hợp đồng
// 、báo cáo。，tầng đánh giá。 docs/evaluation-system.md。
//
// hiện đã bao phủ luồng xác định chính：、baseline/variant A/B delta、repeat  stylestat 。
// LLM Judge vẫn là lớp tùy chọn về sau，。
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

// caseIDPattern giới hạn case id ký tự an toàn：id đầu ra RunCase  RemoveAll ，
//
//	. / ， "../" 。
var caseIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

const defaultDeltaRatio = 0.3

// Case mẫu đánh giá：yêu cầu sáng tác + khẳng định tầng dữ kiện。
type Case struct {
	ID            string   `json:"id"`
	Category      string   `json:"category"`       // tầng đánh giá：smoke/workflow/quality/longform/recovery/steering
	Role          string   `json:"role,omitempty"` // vai trò được kiểm thử：writer/architect/editor（ Category ）
	Description   string   `json:"description,omitempty"`
	Prompt        string   `json:"prompt"`                   // yêu cầu sáng tác
	Style         string   `json:"style,omitempty"`          // ghi đè phong cách cấu hình
	MaxChapters   int      `json:"max_chapters"`             // giới hạn số chương；0 hoàn tất lập kế hoạch（ writing）
	TargetPrompts []string `json:"target_prompts,omitempty"` //  case  prompt （）
	Rubric        string   `json:"rubric,omitempty"`         // LLM Judge （Phase 3 ）
	Expect        Expect   `json:"expect"`
	Gate          Gate     `json:"gate"`
}

// Expect  case hợp đồng—— diag 、 case 。
type Expect struct {
	Phase                string   `json:"phase,omitempty"`                  // mong đợi phase
	MinCompletedChapters int      `json:"min_completed_chapters,omitempty"` // số chương hoàn tất tối thiểu
	RequiredCheckpoints  []string `json:"required_checkpoints,omitempty"`   //  "chapter:1:commit" / "arc:1:1:arc_summary" / "global:layered_outline"
	NoPending            []string `json:"no_pending,omitempty"`             // cần：pending_commit/pending_steer/last_commit/last_review
}

// Gate  case ngưỡng cổng kiểm tra。 MaxSeverity；trường A/B（regression），
// phân tích nhưng không tham gia cổng kiểm tra——giữ lại case  docs/evaluation-system.md  schema 。
type Gate struct {
	MaxSeverity string `json:"max_severity,omitempty"` // diag Finding （ warning）： hard fail

	MaxCostDeltaRatio     *float64 `json:"max_cost_delta_ratio,omitempty"`
	MaxToolCallDeltaRatio *float64 `json:"max_tool_call_delta_ratio,omitempty"`
	StylestatRegression   string   `json:"stylestat_regression,omitempty"`
}

// Validate xác thực case trường。
func (c *Case) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("case thiếu id")
	}
	if !caseIDPattern.MatchString(c.ID) {
		return fmt.Errorf("case id không hợp lệ %q：///，", c.ID)
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return fmt.Errorf("case %q thiếu prompt", c.ID)
	}
	if c.Gate.MaxSeverity == "" {
		c.Gate.MaxSeverity = "warning"
	}
	if !validSeverity(c.Gate.MaxSeverity) {
		return fmt.Errorf("case %q  gate.max_severity không hợp lệ: %s", c.ID, c.Gate.MaxSeverity)
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
		return fmt.Errorf("case %q  gate.stylestat_regression không hợp lệ: %s", c.ID, c.Gate.StylestatRegression)
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

// LoadCases  .json tải case。 *.json tải， id 。
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
			return nil, fmt.Errorf("case id trùng: %q（%s  %s）", c.ID, prev, f)
		}
		seen[c.ID] = f
		cases = append(cases, c)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("không tìm thấy case: %s", path)
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
	dec.DisallowUnknownFields() // trườngbáo lỗi ngay，
	if err := dec.Decode(&c); err != nil {
		return Case{}, fmt.Errorf(" case %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return Case{}, err
	}
	return c, nil
}
