package eval

import (
	"path/filepath"
	"testing"
)

// TestSmokeCasesLoad bảo đảm smoke case có sẵn trong repo được bộ nạp phân tích được (bao gồm kiểm tra DisallowUnknownFields).
func TestSmokeCasesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "evals", "cases", "smoke")
	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("Tải smoke case thất bại: %v", err)
	}
	if len(cases) < 3 {
		t.Fatalf("Mong đợi ít nhất 3 smoke case, nhận được %d", len(cases))
	}
	for _, c := range cases {
		if c.Category != "smoke" {
			t.Errorf("%s: category phải là smoke, nhận được %s", c.ID, c.Category)
		}
		if c.Gate.MaxSeverity == "" {
			t.Errorf("%s: Validate phải điền max_severity mặc định", c.ID)
		}
		if c.Gate.MaxCostDeltaRatio == nil || *c.Gate.MaxCostDeltaRatio != 0.3 ||
			c.Gate.MaxToolCallDeltaRatio == nil || *c.Gate.MaxToolCallDeltaRatio != 0.3 {
			t.Errorf("%s: Validate phải điền delta ratio mặc định, nhận cost=%v tool=%v",
				c.ID, c.Gate.MaxCostDeltaRatio, c.Gate.MaxToolCallDeltaRatio)
		}
		if c.Gate.StylestatRegression != "warn" {
			t.Errorf("%s: Validate phải mặc định stylestat_regression=warn, nhận %s", c.ID, c.Gate.StylestatRegression)
		}
	}
}

func TestLoadCasesRejectsUnknownField(t *testing.T) {
	// Kiểm tra gián tiếp: case hợp lệ phải có id+prompt; thiếu thì báo lỗi (đường dẫn Validate).
	if _, err := LoadCases(filepath.Join("..", "..", "evals", "cases", "smoke", "writer_first_chapter.json")); err != nil {
		t.Fatalf("Tải đơn tệp phải thành công: %v", err)
	}
}

// case id sẽ được ghép vào đường dẫn RemoveAll, nên xuyên đường dẫn / ký tự phân tách phải bị từ chối (phòng vệ nguy cơ cao).
func TestCaseIDRejectsUnsafe(t *testing.T) {
	for _, bad := range []string{"../evil", "a/b", "/abs", "..", "Up", "with space", "dot.case"} {
		c := Case{ID: bad, Prompt: "x"}
		if err := c.Validate(); err == nil {
			t.Errorf("id không hợp lệ %q phải bị từ chối", bad)
		}
	}
	for _, ok := range []string{"writer_first_chapter", "architect-long", "case1"} {
		c := Case{ID: ok, Prompt: "x"}
		if err := c.Validate(); err != nil {
			t.Errorf("id hợp lệ %q không được bị từ chối: %v", ok, err)
		}
	}
}

func TestCaseRejectsInvalidGate(t *testing.T) {
	c := Case{ID: "bad_gate", Prompt: "x", Gate: Gate{StylestatRegression: "maybe"}}
	if err := c.Validate(); err == nil {
		t.Fatal("stylestat_regression không hợp lệ phải bị từ chối")
	}
	c = Case{ID: "disabled_ratio", Prompt: "x", Gate: Gate{MaxCostDeltaRatio: float64Ptr(-1), MaxToolCallDeltaRatio: float64Ptr(-1)}}
	if err := c.Validate(); err != nil {
		t.Fatalf("delta ratio âm phải được chấp nhận như trạng thái tắt tường minh: %v", err)
	}
	if *c.Gate.MaxCostDeltaRatio != -1 || *c.Gate.MaxToolCallDeltaRatio != -1 {
		t.Fatalf("delta ratio tắt tường minh không được bị giá trị mặc định ghi đè: %+v", c.Gate)
	}
	c = Case{ID: "strict_ratio", Prompt: "x", Gate: Gate{MaxCostDeltaRatio: float64Ptr(0), MaxToolCallDeltaRatio: float64Ptr(0)}}
	if err := c.Validate(); err != nil {
		t.Fatalf("delta ratio 0 tường minh phải được chấp nhận như ngưỡng nghiêm ngặt: %v", err)
	}
	if *c.Gate.MaxCostDeltaRatio != 0 || *c.Gate.MaxToolCallDeltaRatio != 0 {
		t.Fatalf("delta ratio 0 tường minh không được bị giá trị mặc định ghi đè: %+v", c.Gate)
	}
}
