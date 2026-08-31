package eval

import (
	"path/filepath"
	"testing"
)

// TestSmokeCasesLoad  smoke case tải（ DisallowUnknownFields xác thực）。
func TestSmokeCasesLoad(t *testing.T) {
	dir := filepath.Join("..", "..", "evals", "cases", "smoke")
	cases, err := LoadCases(dir)
	if err != nil {
		t.Fatalf("tải smoke case thất bại: %v", err)
	}
	if len(cases) < 3 {
		t.Fatalf("mong đợi 3  smoke case，nhận được %d", len(cases))
	}
	for _, c := range cases {
		if c.Category != "smoke" {
			t.Errorf("%s: category cần smoke，nhận được %s", c.ID, c.Category)
		}
		if c.Gate.MaxSeverity == "" {
			t.Errorf("%s: Validate cần max_severity", c.ID)
		}
		if c.Gate.MaxCostDeltaRatio == nil || *c.Gate.MaxCostDeltaRatio != 0.3 ||
			c.Gate.MaxToolCallDeltaRatio == nil || *c.Gate.MaxToolCallDeltaRatio != 0.3 {
			t.Errorf("%s: Validate cần delta ratio，nhận được cost=%v tool=%v",
				c.ID, c.Gate.MaxCostDeltaRatio, c.Gate.MaxToolCallDeltaRatio)
		}
		if c.Gate.StylestatRegression != "warn" {
			t.Errorf("%s: Validate cần stylestat_regression=warn，nhận được %s", c.ID, c.Gate.StylestatRegression)
		}
	}
}

func TestLoadCasesRejectsUnknownField(t *testing.T) {
	// ：hợp lệ case  id+prompt；thiếu（Validate ）。
	if _, err := LoadCases(filepath.Join("..", "..", "evals", "cases", "smoke", "writer_first_chapter.json")); err != nil {
		t.Fatalf("tảicần: %v", err)
	}
}

// case id  RemoveAll ，/（）。
func TestCaseIDRejectsUnsafe(t *testing.T) {
	for _, bad := range []string{"../evil", "a/b", "/abs", "..", "Up", "with space", "dot.case"} {
		c := Case{ID: bad, Prompt: "x"}
		if err := c.Validate(); err == nil {
			t.Errorf("không hợp lệ id %q cần", bad)
		}
	}
	for _, ok := range []string{"writer_first_chapter", "architect-long", "case1"} {
		c := Case{ID: ok, Prompt: "x"}
		if err := c.Validate(); err != nil {
			t.Errorf("hợp lệ id %q cần: %v", ok, err)
		}
	}
}

func TestCaseRejectsInvalidGate(t *testing.T) {
	c := Case{ID: "bad_gate", Prompt: "x", Gate: Gate{StylestatRegression: "maybe"}}
	if err := c.Validate(); err == nil {
		t.Fatal("không hợp lệ stylestat_regression cần")
	}
	c = Case{ID: "disabled_ratio", Prompt: "x", Gate: Gate{MaxCostDeltaRatio: float64Ptr(-1), MaxToolCallDeltaRatio: float64Ptr(-1)}}
	if err := c.Validate(); err != nil {
		t.Fatalf(" delta ratio cần: %v", err)
	}
	if *c.Gate.MaxCostDeltaRatio != -1 || *c.Gate.MaxToolCallDeltaRatio != -1 {
		t.Fatalf(" delta ratio cần: %+v", c.Gate)
	}
	c = Case{ID: "strict_ratio", Prompt: "x", Gate: Gate{MaxCostDeltaRatio: float64Ptr(0), MaxToolCallDeltaRatio: float64Ptr(0)}}
	if err := c.Validate(); err != nil {
		t.Fatalf(" 0 delta ratio cần: %v", err)
	}
	if *c.Gate.MaxCostDeltaRatio != 0 || *c.Gate.MaxToolCallDeltaRatio != 0 {
		t.Fatalf(" 0 delta ratio cần: %+v", c.Gate)
	}
}
