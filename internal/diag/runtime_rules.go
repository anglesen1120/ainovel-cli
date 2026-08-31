package diag

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// Ngưỡng chẩn đoán runtime.
const (
	repeatCritical = 8 // Khi lặp gần đạt số lần này thì nâng lên critical
	streamIdleWarn = 3 // Ngưỡng cảnh báo tích lũy stream_idle
)

// RuntimeRuleFunc là chữ ký thống nhất của quy tắc chẩn đoán runtime (tương ứng RuleFunc phía sáng tác).
// Tham số vào là RuntimeCapture đã được tổng hợp và khử trùng, đầu ra là Finding dạng báo cáo — tất cả AutoNone,
// chỉ chẩn đoán, không sinh Action (kỷ luật quan sát, xem architecture.md §2.3).
type RuntimeRuleFunc func(rc *RuntimeCapture) []Finding

var runtimeRules = []RuntimeRuleFunc{
	repeatedErrors,
	stuckStep,
	streamIdleStorm,
}

// runtimeFindings chạy toàn bộ quy tắc runtime.
func runtimeFindings(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, rule := range runtimeRules {
		out = append(out, rule(rc)...)
	}
	return out
}

// Diagnose là cổng chẩn đoán đầy đủ của /diag: chẩn đoán sáng tác + tín hiệu runtime + kiểm tra runtime,
// trả về Report đã hợp nhất và RuntimeCapture gốc (dùng lại cho xuất, tránh chụp lặp).
// Finding runtime chỉ được gộp vào Findings để hiển thị, không đổi Actions — giữ thuần quan sát.
func Diagnose(s *store.Store) (Report, RuntimeCapture) {
	rep := Analyze(s)
	rc := CaptureRuntime(s)
	rep.Findings = append(rep.Findings, runtimeFindings(&rc)...)
	sortFindings(rep.Findings)
	return rep, rc
}

// repeatedErrors chỉ coi "lỗi / tham số không hợp lệ lặp lại ở vùng gần" là Finding.
// Không đụng tới việc lặp công cụ bình thường — subagent/novel_context/read_chapter trong chạy dài vốn
// có tần suất cao; số lần tích lũy không phải tín hiệu vòng lặp; "lặp mà không tiến" thật sự do stuckStep xử lý.
func repeatedErrors(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, r := range rc.Repeats {
		var rule, title, sugg string
		switch {
		case strings.Contains(r.Sig, " · err: "):
			rule = "RepeatedToolError"
			title = "lỗi"
            sugg = "Lỗi hoặc tham số không đúng với hợp đồng; hãy để agentcore xác thực tham số trong lời nhắc (xem #34)."
        case strings.Contains(r.Sig, "(args invalid)"):
            rule = "ArgsInvalidLoop"
            title = "tham số không thể phân tích"
            sugg = "Tham số không thể phân tích; hãy kiểm tra định dạng tham số mà agentcore yêu cầu (xem #34)."
		default:
			continue // Lặp công cụ bình thường không sinh Finding
		}
		sev := SevWarning
		if r.Count >= repeatCritical {
			sev = SevCritical
		}
		out = append(out, Finding{
			Rule:       rule,
			Category:   CatFlow,
			Severity:   sev,
			Confidence: ConfHigh,
			AutoLevel:  AutoNone,
			Target:     "runtime.flow",
			Title:      title,
			Evidence:   fmt.Sprintf("`%s` ×%d", r.Sig, r.Count),
			Suggestion: sugg,
		})
	}
	return out
}

// stuckStep phát hiện checkpoint liên tiếp dừng ở cùng một step.
func stuckStep(rc *RuntimeCapture) []Finding {
	if rc.StuckStep == "" {
		return nil
	}
	sev := SevWarning
	if rc.StuckCount >= repeatCritical {
		sev = SevCritical
	}
	return []Finding{{
		Rule:       "StuckStep",
		Category:   CatFlow,
		Severity:   sev,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.flow",
        Title:      "checkpoint đình trệ tại bước",
        Evidence:   fmt.Sprintf("`%s` ×%d", rc.StuckStep, rc.StuckCount),
        Suggestion: "Cùng một bước được ghi liên tục nhưng không có tiến triển; có thể quy trình đang bị kẹt.",
	}}
}

// streamIdleStorm phát hiện luồng dữ liệu thường xuyên bị gián đoạn (xem #32).
func streamIdleStorm(rc *RuntimeCapture) []Finding {
	n := rc.LogKinds["stream_idle"]
	if n < streamIdleWarn {
		return nil
	}
	return []Finding{{
		Rule:       "StreamIdleStorm",
		Category:   CatFlow,
		Severity:   SevWarning,
		Confidence: ConfHigh,
		AutoLevel:  AutoNone,
		Target:     "runtime.provider",
		Title:      "Luồng dữ liệu thường xuyên bị gián đoạn (stream_idle)",
		Evidence:   fmt.Sprintf("stream_idle ×%d", n),
		Suggestion: "Nguồn upstream im lặng quá lâu khiến watchdog dừng nhầm; hãy kiểm tra streamIdleTimeout và cấu hình provider (xem #32).",
	}}
}
