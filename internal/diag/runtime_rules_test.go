package diag

import "testing"

// TestRuntimeFindings_Classify chứng minh chữ ký lặp được phân loại theo hình thái, nâng/hạ cấp ngưỡng chính xác,
// và mọi Finding thời gian chạy đều là AutoNone (kỷ luật quan sát viên: chỉ chẩn đoán, không sinh Action).
func TestRuntimeFindings_Classify(t *testing.T) {
	rc := RuntimeCapture{
		Repeats: []RepeatStat{
			{Sig: "writer-ch07 · err: InputValidationError", Count: 14}, // Vòng lặp lỗi critical
			{Sig: "writer-ch07 · novel_context", Count: 45},             // Công cụ tần suất cao bình thường → không sinh Finding
			{Sig: "writer · save_plan (args invalid)", Count: 4},        // Tham số vô hiệu warning
		},
		StuckStep:  "writing.commit_ch07",
		StuckCount: 9, // Bị kẹt critical
		LogKinds:   map[string]int{"stream_idle": 4},
		LogErrors:  270, // Cộng dồn trong chạy dài, không nên tự sinh Finding
	}

	fs := runtimeFindings(&rc)
	sev := map[string]Severity{}
	for _, f := range fs {
		sev[f.Rule] = f.Severity
		if f.AutoLevel != AutoNone {
			t.Errorf("%s phải là AutoNone (kỷ luật quan sát viên), got %s", f.Rule, f.AutoLevel)
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
	// Công cụ tần suất cao bình thường / error cộng dồn trong log không nên sinh Finding (tránh báo sai khi chạy dài).
	if _, ok := sev["RepeatedToolCall"]; ok {
		t.Error("Lặp công cụ thông thường không nên sinh Finding")
	}
	if _, ok := sev["LogErrorBurst"]; ok {
		t.Error("error cộng dồn trong log không nên tự sinh Finding")
	}
}

// TestRuntimeFindings_Quiet chứng minh khi không có tín hiệu bất thường thì không sinh Finding thời gian chạy nào (không báo sai).
func TestRuntimeFindings_Quiet(t *testing.T) {
	rc := RuntimeCapture{
		LogKinds:  map[string]int{"stream_idle": 1}, // Dưới ngưỡng
		LogErrors: 2,
	}
	if fs := runtimeFindings(&rc); len(fs) != 0 {
		t.Errorf("Trạng thái yên tĩnh không nên sinh Finding, got %d: %+v", len(fs), fs)
	}
}
