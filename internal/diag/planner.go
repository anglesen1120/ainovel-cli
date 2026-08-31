package diag

import "fmt"

// PlanActions tạo hành động có thể thực thi từ các Finding có độ tin cậy cao.
// Chỉ những Finding có Confidence==high && AutoLevel==safe mới tạo Action.
func PlanActions(findings []Finding) []Action {
	var actions []Action
	seen := make(map[string]struct{})

	for _, f := range findings {
		if f.Confidence != ConfHigh || f.AutoLevel != AutoSafe {
			continue
		}
		if _, ok := seen[f.Rule]; ok {
			continue
		}
		seen[f.Rule] = struct{}{}

		actions = append(actions, planRule(f)...)
	}
	return actions
}

func planRule(f Finding) []Action {
	key := findingFingerprint(f)

	switch f.Rule {
	case "PhaseFlowMismatch":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEmitNotice, Severity: f.Severity, Summary: f.Title, Message: f.Title, Fingerprint: key},
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "Sửa lỗi trạng thái máy", Message: "Bất thường trạng thái máy: " + f.Evidence + "。Hãy kiểm tra và sửa trạng thái phase/flow của progress trước khi tiếp tục chạy.", Fingerprint: key},
		}
	case "OutlineExhausted":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "Xử lý khi dàn ý cạn", Message: "Số chương đã hoàn thành đã chạm ngưỡng dàn ý. Hãy ưu tiên gọi Architect để mở rộng nhánh tiếp theo hoặc thêm cuốn mới rồi mới viết tiếp.", Fingerprint: key},
		}
	case "OrphanedSteer":
		return []Action{
			{SourceRule: f.Rule, Kind: ActionEnqueueFollowUp, Severity: f.Severity, Summary: "Xử lý can thiệp người dùng chưa được tiêu thụ", Message: "Tồn tại chỉ thị can thiệp của người dùng chưa được tiêu thụ, hãy ưu tiên xử lý pending steer trước khi tiếp tục tác vụ hiện tại.", Fingerprint: key},
		}
	default:
		return nil
	}
}

func findingFingerprint(f Finding) string {
	return fmt.Sprintf("%s|%s|%s|%s", f.Rule, f.Target, f.Title, f.Evidence)
}
