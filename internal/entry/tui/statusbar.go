package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStatusBar hiển thị thanh trạng thái sử dụng ở đáy màn hình, chiếm dòng trống cuối vốn có của vùng Nhập (không thêm chiều cao):
//
//	◆ provider model(cửa sổ,suy nghĩ) │ ↑Nhập ↓Xuất ⚡cache gần đây trúng trung bình │ chi phí(/ngân sách) tiết kiệm X    ./thư mục sách
//
// Định vị là "nhìn thoáng biết chi phí": danh tính Mô hình được trả phí, token lũy kế của phiên, chi phí và cảnh báo tiệm cận ngân sách.
// Dữ liệu đến từ UISnapshot được thăm dò mỗi 3s (mỗi lần gọi Mô hình hoàn tất, usage sẽ được cộng dồn vào sổ);
// Chi tiết per-role/per-model và chẩn đoán cache vẫn do thanh bên trái đảm nhiệm, không lặp lại ở đây.
func renderStatusBar(snap host.UISnapshot, outputDir string, width int) string {
	dim := lipgloss.NewStyle().Foreground(colorDim)
	val := lipgloss.NewStyle().Foreground(colorMuted)

	var segs []string
	if snap.ModelName != "" {
		s := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("◆") + " "
		if snap.Provider != "" {
			s += dim.Render(snap.Provider) + " "
		}
		s += val.Render(snap.ModelName)
		if suffix := modelInfoSuffix(snap); suffix != "" {
			s += dim.Render("(" + suffix + ")")
		}
		segs = append(segs, s)
	}
	if snap.TotalInputTokens > 0 || snap.TotalOutputTokens > 0 {
		s := dim.Render("↑") + val.Render(formatTokensCompact(snap.TotalInputTokens)) +
			" " + dim.Render("↓") + val.Render(formatTokensCompact(snap.TotalOutputTokens))
		// Chỉ hiển thị tỷ lệ hit gần đây khi model thật sự hỗ trợ prompt cache và có mẫu, để tránh hiểu lầm “0% cần điều tra”.
		if snap.OverallCacheCapable && snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
			rate := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
			s += " " + lipgloss.NewStyle().Foreground(cacheHitColor(rate)).Render("⚡"+formatPercent(rate))
		}
		segs = append(segs, s)
	}
	if snap.TotalCostUSD > 0 || snap.BudgetLimitUSD > 0 {
		cost := formatCostUSD(snap.TotalCostUSD)
		if cost == "" {
			cost = "$0"
		}
		style := val
		if snap.BudgetLimitUSD > 0 {
			// ngân sách tiến gần/vượt giới hạn dùng màu cảnh báo——thanh trạng thái luôn hiển thị, là vị trí ngân sách nên được nhìn thấy nhất.
			switch ratio := snap.TotalCostUSD / snap.BudgetLimitUSD; {
			case ratio >= 1:
				style = lipgloss.NewStyle().Foreground(colorError).Bold(true)
			case ratio >= 0.8:
				style = lipgloss.NewStyle().Foreground(colorReview)
			}
		}
		s := style.Render(cost)
		if snap.BudgetLimitUSD > 0 {
			s += dim.Render("/" + formatCostUSD(snap.BudgetLimitUSD))
		}
		if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
			s += dim.Render(" tiết kiệm " + saved)
		}
		segs = append(segs, s)
	}

	left := strings.Join(segs, dim.Render(" │ "))
	var right string
	if outputDir != "" {
		right = dim.Render("./" + filepath.Base(outputDir))
	}
	if left == "" && right == "" {
		return dim.Render("SẴN SÀNG")
	}
	return joinInlineSides(left, right, width)
}

// modelInfoSuffix ghép chú thích cho Mô hình: cửa sổ ngữ cảnh + mức suy nghĩ, ví dụ "200K,med".
func modelInfoSuffix(snap host.UISnapshot) string {
	var parts []string
	if w := formatContextWindow(snap.ModelContextWindow); w != "" {
		parts = append(parts, w)
	}
	if t := formatThinkingLevel(snap.ThinkingLevel); t != "" {
		parts = append(parts, t)
	}
	return strings.Join(parts, ",")
}

func formatThinkingLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "":
		return "auto"
	case "medium":
		return "med"
	default:
		return strings.ToLower(strings.TrimSpace(level))
	}
}
