package diag

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// Ngưỡng phát hiện thời gian chạy.
const (
	repeatCritical = 8 // Lặp ở đoạn gần đạt số lần này thì nâng lên critical
	streamIdleWarn = 3 // Ngưỡng cảnh báo tích lũy của stream_idle
)

// RuntimeRuleFunc là chữ ký thống nhất của quy tắc chẩn đoán thời gian chạy (tương ứng RuleFunc phía sáng tác).
// Tham số vào là RuntimeCapture đã được tổng hợp và khử nhạy cảm, đầu ra là Finding kiểu báo cáo — tất cả AutoNone,
// chỉ chẩn đoán, không sinh Action (kỷ luật quan sát viên, xem architecture.md §2.3).
type RuntimeRuleFunc func(rc *RuntimeCapture) []Finding

var runtimeRules = []RuntimeRuleFunc{
	repeatedErrors,
	stuckStep,
	streamIdleStorm,
}

// runtimeFindings chạy toàn bộ quy tắc thời gian chạy.
func runtimeFindings(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, rule := range runtimeRules {
		out = append(out, rule(rc)...)
	}
	return out
}

// Diagnose là đầu vào chẩn đoán đầy đủ của /diag: chẩn đoán sáng tác + tín hiệu thời gian chạy + phát hiện thời gian chạy,
// trả về Report đã gộp cùng RuntimeCapture gốc (để tái sử dụng khi xuất, tránh thu thập lại).
// Finding thời gian chạy chỉ được gộp vào Findings để hiển thị, không sửa Actions — giữ nguyên tính quan sát thuần túy.
func Diagnose(s *store.Store) (Report, RuntimeCapture) {
	rep := Analyze(s)
	rc := CaptureRuntime(s)
	rep.Findings = append(rep.Findings, runtimeFindings(&rc)...)
	sortFindings(rep.Findings)
	return rep, rc
}

// repeatedErrors chỉ coi "lỗi / tham số vô hiệu xuất hiện lặp lại gần đây" là Finding.
// Không đụng tới các lặp công cụ thông thường — subagent/novel_context/read_chapter v.v. trong chạy dài vốn
// có tần suất cao; số lần cộng dồn không phải tín hiệu vòng lặp; trạng thái "lặp mà không tiến" thật sự do stuckStep đảm nhiệm.
func repeatedErrors(rc *RuntimeCapture) []Finding {
	var out []Finding
	for _, r := range rc.Repeats {
		var rule, title, sugg string
		switch {
		case strings.Contains(r.Sig, " · err: "):
			rule = "RepeatedToolError"
			title = "Công cụ lặp lại cùng một lỗi"
			sugg = "Cùng một công cụ ở vùng gần liên tiếp trả về cùng lỗi, thường do tham số mô hình không hợp lệ hoặc không khớp hợp đồng công cụ; hãy kiểm tra cơ chế xác thực công cụ của agentcore và quy ước tham số trong lời nhắc (xem #34)."
		case strings.Contains(r.Sig, "(args invalid)"):
			rule = "ArgsInvalidLoop"
			title = "Tham số lặp lại không thể phân tích"
			sugg = "Tham số do mô hình gửi tới không thể phân tích nhưng vẫn liên tục được thử lại; hãy kiểm tra xem agentcore có đang chấp nhận kiểu dữ liệu này quá lỏng hay không (xem #34)."
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
		Title:      "Checkpoint bị kẹt ở cùng một step",
		Evidence:   fmt.Sprintf("Liên tiếp dừng ở `%s` ×%d", rc.StuckStep, rc.StuckCount),
		Suggestion: "Cùng một bước được ghi lặp lại mà không tiến triển; hãy đối chiếu các chữ ký lặp ở trên để xác định tác nhân phụ đang bị kẹt.",
	}}
}

// streamIdleStorm phát hiện ngắt dòng streaming diễn ra thường xuyên (#32).
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
		Title:      "Ngắt streaming diễn ra thường xuyên (stream_idle)",
		Evidence:   fmt.Sprintf("stream_idle ×%d", n),
		Suggestion: "Nguồn cung cấp không gửi mã thông báo trong thời gian dài nên watchdog có thể dừng nhầm; với mô hình suy nghĩ chậm, hãy tăng streamIdleTimeout hoặc kiểm tra độ ổn định kết nối của provider (xem #32).",
	}}
}
