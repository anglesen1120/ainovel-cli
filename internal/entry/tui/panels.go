package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderTopBar render thanh trạng thái phía trên.
// Bên trái：provider/model，ở giữa：tên sách，bên phải：huy hiệu trạng thái。
func renderTopBar(snap host.UISnapshot, width int, spinnerFrame, version string) string {
	bookTitle := snap.BookTitle
	if bookTitle == "" {
		bookTitle = "Chưa đặt tên sách"
	}

	var infoParts []string
	if version != "" {
		infoParts = append(infoParts, "ainovel-cli "+version)
	}
	if snap.Provider != "" {
		infoParts = append(infoParts, snap.Provider)
	}
	if snap.ModelName != "" {
		if w := formatContextWindow(snap.ModelContextWindow); w != "" {
			infoParts = append(infoParts, snap.ModelName+"("+w+")")
		} else {
			infoParts = append(infoParts, snap.ModelName)
		}
	}
	if snap.Style != "" && snap.Style != "default" {
		infoParts = append(infoParts, snap.Style)
	}
	leftText := strings.Join(infoParts, " · ")

	label := snap.StatusLabel
	if label == "" {
		label = "SẴN SÀNG"
	}
	color, ok := statusColors[label]
	if !ok {
		color = colorDim
	}
	disp, ok := statusDisplay[label]
	if !ok {
		disp = struct {
			icon  string
			label string
		}{"○", strings.ToLower(label)}
	}
	icon := disp.icon
	if snap.IsRunning && spinnerFrame != "" {
		icon = spinnerFrame
	}
	var status string
	if icon != "" {
		status = statusIconStyle.Foreground(color).Render(icon) + " " + statusLabelStyle.Render(disp.label)
	} else {
		status = statusLabelStyle.Render(disp.label)
	}

	innerW := max(12, width-2)
	titleText := truncate(bookTitle, max(8, innerW/3))
	centerW := max(16, lipgloss.Width(titleText)+6)
	if centerW > innerW-24 {
		centerW = max(8, innerW-24)
	}
	sideTotal := innerW - centerW
	if sideTotal < 0 {
		sideTotal = 0
		centerW = innerW
	}
	leftW := sideTotal / 2
	rightW := innerW - centerW - leftW

	leftCell := lipgloss.NewStyle().
		Width(leftW).
		AlignHorizontal(lipgloss.Left).
		Foreground(colorDim).
		Render(truncate(leftText, leftW))
	centerCell := lipgloss.NewStyle().
		Width(centerW).
		AlignHorizontal(lipgloss.Center).
		Bold(true).
		Foreground(bodyTextColor).
		Render(titleText)
	rightCell := lipgloss.NewStyle().
		Width(rightW).
		AlignHorizontal(lipgloss.Right).
		Render(status)

	content := leftCell + centerCell + rightCell
	return topBarStyle.Width(width).
		Border(baseBorder, false, false, true, false).
		BorderForeground(colorDim).
		Render(content)
}

// renderStatePanel đưa nội dung sidebar trạng thái (đã nằm trong stateVP) vào hộp bên trái có viền phải.
// Đối xứng với renderDetailPanel: nội dung do renderStateContent tạo ra và đẩy vào viewport, ở đây chỉ lo khung.
// MaxHeight giới hạn chiều cao, tránh tràn cao hơn cột phải khi cửa sổ thu nhỏ (xem ràng buộc chiều cao trong panels_test.go).
func renderStatePanel(vp viewport.Model, width, height int, focused bool) string {
	borderColor := colorDim
	if focused {
		borderColor = colorAccent
	}
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(baseBorder, false, true, false, false).
		BorderForeground(borderColor).
		Padding(1, 1, 0, 1)
	return style.Render(vp.View())
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// renderDetailPanel render panel Chi tiết có thể cuộn ở bên phải.
func renderDetailPanel(vp viewport.Model, width, height int, focused bool) string {
	borderColor := colorDim
	if focused {
		borderColor = colorAccent
	}
	style := lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxHeight(height).
		Border(baseBorder, false, false, false, true).
		BorderForeground(borderColor).
		Padding(0, 1)

	return style.Render(vp.View())
}

// renderWelcome render màn hình đầu cho trạng thái tạo mới.
func renderWelcome(width, height int, errMsg string, mode startupMode, importHint string) string {
	// tiêu đề gọn
	title := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render("A I N O V E L")

	// phụ đề
	subtitle := lipgloss.NewStyle().
		Foreground(colorMuted).
		Italic(true).
		Render("Công cụ sáng tác tiểu thuyết hỗ trợ bởi AI")

	// đường phân cách
	divW := 44
	if divW > width-8 {
		divW = width - 8
	}
	divider := lipgloss.NewStyle().Foreground(colorDim).
		Render(strings.Repeat("~", divW))

	// điểm nổi bật
	features := []struct{ icon, label, desc string }{
		{">>", "Phối hợp đa mô hình", "Architect lập kế hoạch / Writer sáng tác / Editor duyệt"},
		{"::", "Khôi phục điểm dừng", "Tự động nối tiếp từ tiến độ trước sau khi sập hoặc bị ngắt"},
		{"<>", "Can thiệp tức thời", "Có thể chỉnh hướng cốt truyện bất cứ lúc nào"},
		{"##", "Dài tập phân tầng", "Hỗ trợ sáng tác dài theo cấu trúc quyển - cung - chương"},
	}
	iconStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	featLabelStyle := lipgloss.NewStyle().Foreground(bodyTextColor)
	descStyle := lipgloss.NewStyle().Foreground(colorDim)
	var featLines []string
	for _, f := range features {
		line := iconStyle.Render(f.icon) + " " +
			featLabelStyle.Render(f.label) + "  " +
			descStyle.Render(f.desc)
		featLines = append(featLines, line)
	}
	feats := strings.Join(featLines, "\n")

	// Nhập gợi ý
	prompt := lipgloss.NewStyle().Foreground(bodyTextColor).Render("Nhập yêu cầu tiểu thuyết của bạn bên dưới để bắt đầu sáng tác")

	modeLine := lipgloss.NewStyle().
		Foreground(colorMuted).
		Render("Chế độ hiện tại: " + mode.label() + " · " + mode.subtitle())

	// ví dụ
	examples := []string{
		"Viết một tiểu thuyết huyền bí đô thị 12 chương, nhân vật chính là nữ pháp y",
		"Sáng tác một truyện tiên hiệp dài, nhân vật chính tu luyện từ phàm nhân đến phi thăng",
		"Viết một truyện ngắn khoa học viễn tưởng về tình thế đạo đức sau khi AI thức tỉnh",
	}
	exStyle := lipgloss.NewStyle().Foreground(colorAccent)
	dotStyle := lipgloss.NewStyle().Foreground(colorDim)
	var exLines []string
	for _, ex := range examples {
		exLines = append(exLines, dotStyle.Render("  . ")+exStyle.Render(ex))
	}
	exBlock := strings.Join(exLines, "\n")

	// lắp ghép
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString(subtitle)
	b.WriteString("\n\n")
	b.WriteString(divider)
	b.WriteString("\n\n")
	b.WriteString(feats)
	b.WriteString("\n\n")
	b.WriteString(divider)
	b.WriteString("\n\n")
	b.WriteString(modeLine)
	b.WriteString("\n\n")
	b.WriteString(prompt)
	b.WriteString("\n\n")
	b.WriteString(exBlock)
	b.WriteString("\n\n")
	if importHint != "" {
		// cuốn sách này dừng giữa quá trình nhập：hiển thị rõ lối vào khôi phục thay cho gợi ý nhập thông thường。
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).
			Render("! " + importHint))
	} else {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).
			Render("Có thiết lập/dàn ý? /start <đường dẫn> tạo sách mới · Có bản thảo truyện? /import <đường dẫn> để nhập và nối tiếp"))
	}
	b.WriteString("\n\n")
	b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Italic(true).
		Render("Tab đổi chế độ · Ở bắt đầu nhanh Enter sáng tác ngay · Ở Kế hoạch đồng sáng tạo Enter vào hội thoại"))

	if errMsg != "" {
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorError).Bold(true).Render("! " + errMsg))
	}

	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		AlignHorizontal(lipgloss.Center).
		AlignVertical(lipgloss.Center).
		Render(b.String())
}
