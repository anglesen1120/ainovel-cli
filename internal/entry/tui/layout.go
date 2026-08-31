package tui

import (
	"fmt"
	"math"

	"github.com/charmbracelet/lipgloss"
)

// --- Hàm phụ trợ ---

func renderField(label, value string) string {
	if value == "" {
		value = "-"
	}
	return fieldLabelStyle.Render(label) + fieldValueStyle.Render(value) + "\n"
}

func renderHighlightField(label, value string) string {
	return fieldLabelStyle.Render(label) + highlightValueStyle.Render(value) + "\n"
}

// contextPercentColor trả về màu chuyển sắc theo mức độ sử dụng ngữ cảnh.
// Phản chiếu khái niệm calculateTokenWarningState của Claude Code:
//   - < 70%: xanh lá (còn nhiều dung lượng)
//   - 70-85%: vàng (gần ngưỡng nén)
//   - > 85%: đỏ (sắp hoặc đang nén)
func contextPercentColor(percent float64) lipgloss.AdaptiveColor {
	switch {
	case percent >= 85:
		return colorError
	case percent >= 70:
		return colorReview
	default:
		return colorSuccess
	}
}

// formatContextWindow định dạng số token thành nhãn cửa sổ gọn: "128K" / "200K" / "1M" / "2M".
// 1048576 (2^20) của Gemini, tức 1M theo nghĩa kỹ thuật, sẽ hiển thị là "1M" chứ không phải "1.0M".
// n<=0 trả về chuỗi rỗng; bên gọi nên dựa vào đó để quyết định có hiển thị hay không.
func formatContextWindow(n int) string {
	if n <= 0 {
		return ""
	}
	if n >= 1_000_000 {
		m := float64(n) / 1_000_000
		rounded := math.Round(m)
		if rounded > 0 && math.Abs(m-rounded)/rounded < 0.05 {
			return fmt.Sprintf("%dM", int(rounded))
		}
		return fmt.Sprintf("%.1fM", m)
	}
	if n >= 1000 {
		return fmt.Sprintf("%dK", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

// formatCostUSD định dạng chi phí USD. <$0.01 dùng 4 chữ số thập phân, ngược lại dùng 2 chữ số. 0 trả về rỗng.
func formatCostUSD(usd float64) string {
	if usd <= 0 {
		return ""
	}
	if usd < 0.01 {
		return fmt.Sprintf("$%.4f", usd)
	}
	return fmt.Sprintf("$%.2f", usd)
}

func formatNumber(n int) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	result := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// truncate cắt ngắn theo độ rộng hiển thị (tiếng Trung tính 2 cột); khi quá rộng thì kết thúc bằng "...".
// Không thể cắt theo số rune: dòng toàn tiếng Trung sẽ tràn gần gấp đôi độ rộng cột, bị viewport bên ngoài cắt cứng sát mép,
// cắt luôn cả dấu ba chấm, thứ người dùng thấy sẽ là "văn bản bị cắt sát mép, không xuống dòng".
func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= max {
		return s
	}
	if max < 4 {
		return truncateWidth(s, max)
	}
	return truncateWidth(s, max-3) + "..."
}
