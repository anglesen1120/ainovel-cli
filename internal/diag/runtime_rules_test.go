package diag

import "testing"

// TestRuntimeFindings_Classify chữ ký lặp được phân loại đúng và ngưỡng nâng/hạ chính xác，
// mọi Finding runtime đều AutoNone (kỷ luật quan sát: chỉ chẩn đoán, không tạo Action)。
func TestRuntimeFindings_Classify(t *testing.T) {
	rc := RuntimeCapture{
		Repeats: []RepeatStat{
			{Sig: "writer-ch07 · err: InputValidationError", Count: 14}, // lỗivòng lặp critical
			{Sig: "writer-ch07 · novel_context", Count: 45},             // bình thường →  Finding
			{Sig: "writer · save_plan (args invalid)", Count: 4},        // tham số warning
		},
		StuckStep:  "writing.commit_ch07",
		StuckCount: 9, // bị kẹt critical
		LogKinds:   map[string]int{"stream_idle": 4},
		LogErrors:  270, // ，cần Finding
	}

	fs := runtimeFindings(&rc)
	sev := map[string]Severity{}
	for _, f := range fs {
		sev[f.Rule] = f.Severity
		if f.AutoLevel != AutoNone {
			t.Errorf("%s cần AutoNone（），got %s", f.Rule, f.AutoLevel)
		}
	}

	want := map[string]Severity{
		"RepeatedToolError": SevCritical,
		"ArgsInvalidLoop":   SevWarning,
		"StuckStep":         SevCritical,
		"StreamIdleStorm":   SevWarning,
	}
	for rule, w := range want {
		if sev[rule] != w {
			t.Errorf("%s: got %q want %q", rule, sev[rule], w)
		}
	}
	// bình thường /  error cần Finding（）。
	if _, ok := sev["RepeatedToolCall"]; ok {
		t.Error("trùngcần Finding")
	}
	if _, ok := sev["LogErrorBurst"]; ok {
		t.Error(" error cần Finding")
	}
}

// TestRuntimeFindings_Quiet  Finding（）。
func TestRuntimeFindings_Quiet(t *testing.T) {
	rc := RuntimeCapture{
		LogKinds:  map[string]int{"stream_idle": 1}, //
		LogErrors: 2,
	}
	if fs := runtimeFindings(&rc); len(fs) != 0 {
		t.Errorf("cần Finding，got %d: %+v", len(fs), fs)
	}
}
