package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// outlineGridThreshold ngưỡng số chương để dàn ý chuyển sang nhiều cột。
// short tier giới hạn trên 25 chương; dưới 20 thì một cột một màn hình vẫn chứa đủ và vẫn giữ được huy hiệu "Đang tiến hành";
// Ở chế độ layered cho truyện dài, sau khi cuộn mở rộng thì n tự nhiên sẽ vượt 20, chuyển mượt sang nhiều cột.
const outlineGridThreshold = 20

// renderOutlineSection chọn layout theo số chương: ít thì một cột (kèm huy hiệu "Đang tiến hành"), nhiều thì lưới nhiều cột.
func renderOutlineSection(snap host.UISnapshot, contentW int) string {
	if len(snap.Outline) < outlineGridThreshold {
		return renderOutlineList(snap, contentW)
	}
	return renderOutlineGrid(snap, contentW)
}

// renderOutlineList danh sách chương một cột (dùng cho truyện ngắn). Cuối mỗi dòng có huy hiệu "Đang tiến hành", nhịp đọc dọc gần với mục lục hơn.
func renderOutlineList(snap host.UISnapshot, contentW int) string {
	var b strings.Builder
	for _, e := range snap.Outline {
		ch := fmt.Sprintf("%2d", e.Chapter)
		var marker, chStyle string
		titleStyle := cardContentStyle
		switch {
		case snap.CompletedCount >= e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
		case snap.InProgressChapter == e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
			chStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		default:
			marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}
		title := truncate(e.Title, contentW-6)
		line := marker + chStyle + " " + titleStyle.Render(title)
		if snap.InProgressChapter == e.Chapter {
			line += lipgloss.NewStyle().Foreground(colorAccent).Italic(true).Render(" Đang tiến hành")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineGrid đổ các chương dàn ý vào lưới nhiều cột theo “ưu tiên cột”, tránh chừa nhiều khoảng trống khi màn hình rộng mà chỉ có một cột.
// số cột thích ứng theo contentW（1-4），chương tăng liên tục trong mỗi cột（"đọc hết một cột rồi tới cột sau"）。
// Đánh đổi so với layout một cột: bỏ huy hiệu " Đang tiến hành" ở cuối — trong nhiều cột, huy hiệu sẽ phá vỡ căn chỉnh cột,
// đồng thời dấu ▸ + màu vàng + cột Tổng quan bên trái với "đang viết chương N" đã truyền đạt rõ thông tin Đang tiến hành.
func renderOutlineGrid(snap host.UISnapshot, contentW int) string {
	n := len(snap.Outline)
	if n == 0 {
		return ""
	}
	chNumW := 2
	titleW := 0
	for _, e := range snap.Outline {
		if w := len(strconv.Itoa(e.Chapter)); w > chNumW {
			chNumW = w
		}
		if w := lipgloss.Width(e.Title); w > titleW {
			titleW = w
		}
	}
	// giới hạn chiều rộng tiêu đề 14 (khoảng 7 ký tự CJK); các tiêu đề dài thỉnh thoảng xuất hiện sẽ bị cắt bớt, tránh một hai tiêu đề dài làm phình toàn bộ cell
	if titleW > 14 {
		titleW = 14
	} else if titleW < 4 {
		titleW = 4
	}
	cellW := 3 + chNumW + titleW // marker(1) + khoảng trắng(1) + số chương + khoảng trắng(1) + tiêu đề
	gutter := 4
	cols := (contentW + gutter) / (cellW + gutter)
	if cols < 1 {
		cols = 1
	} else if cols > 4 {
		cols = 4
	}
	rows := (n + cols - 1) / cols

	var b strings.Builder
	cellStyle := lipgloss.NewStyle().Width(cellW)
	gutterStr := strings.Repeat(" ", gutter)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := c*rows + r
			if idx >= n {
				break
			}
			cell := renderOutlineCell(snap.Outline[idx], snap, chNumW, titleW)
			// khi cột sau còn cell thì đệm theo cellW + gutter；nếu không, cell hiện tại ở cuối dòng thì không đệm
			if c < cols-1 && (c+1)*rows+r < n {
				b.WriteString(cellStyle.Render(cell))
				b.WriteString(gutterStr)
			} else {
				b.WriteString(cell)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineCell render một cell chương：hoàn tất (xanh ●) / đang tiến hành (vàng ▸) / chưa bắt đầu (mờ ○)。
func renderOutlineCell(e host.OutlineSnapshot, snap host.UISnapshot, chNumW, titleW int) string {
	chStr := fmt.Sprintf("%*d", chNumW, e.Chapter)
	title := truncateWidth(e.Title, titleW)
	var marker, chRendered, titleRendered string
	switch {
	case snap.CompletedCount >= e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = cardContentStyle.Render(title)
	case snap.InProgressChapter == e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
		chRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(title)
	default:
		marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorMuted).Render(title)
	}
	return marker + " " + chRendered + " " + titleRendered
}

// truncateWidth cắt theo “chiều rộng hiển thị” (ký tự CJK tính 2 cột), cùng nguồn với lipgloss.Width.
// không thêm dấu lược, dùng chung cho căn chỉnh cột cell trong lưới và truncate.
func truncateWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if cur+rw > maxW {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String()
}

// renderDetailContent xây dựng nội dung panel chi tiết bên phải。
// ưu tiên hiển thị thiết lập nền (Dàn ý, Nhân vật), sau đó là thông tin runtime (submit, review, v.v.).
func renderDetailContent(snap host.UISnapshot, contentW int) string {
	var b strings.Builder

	// Dàn ý
	if len(snap.Outline) > 0 {
		outlineHeader := ":: Dàn ý"
		if snap.Layered {
			outlineHeader = fmt.Sprintf(":: Dàn ý（%s · dàn ý quy hoạch động）", snap.CurrentVolumeArc)
		}
		b.WriteString(panelTitleStyle.Render(outlineHeader))
		b.WriteString("\n")
		b.WriteString(renderOutlineSection(snap, contentW))
		// gợi ý quy hoạch cuộn
		compassStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		if snap.Layered {
			if snap.NextVolumeTitle != "" {
				b.WriteString(compassStyle.Render("  ┄ Quyển tiếp: " + snap.NextVolumeTitle))
				b.WriteString("\n")
			}
			b.WriteString(compassStyle.Render("  ··· Các chương tiếp theo sẽ tự sinh theo tiến trình sáng tác"))
			b.WriteString("\n")
			if snap.CompassDirection != "" {
				direction := fmt.Sprintf("  → Kết cục: %s", snap.CompassDirection)
				if snap.CompassScale != "" {
					direction += "（" + snap.CompassScale + "）"
				}
				b.WriteString(compassStyle.Render(truncate(direction, contentW)))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	// Nhân vật
	if len(snap.Characters) > 0 {
		b.WriteString(panelTitleStyle.Render(":: Nhân vật"))
		b.WriteString("\n")
		for _, c := range snap.Characters {
			writeBulletWrapped(&b, c, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	// Hệ sinh thái nhân vật phụ：tổng số nhân vật phụ đã xuất hiện + 5 nhân vật hoạt động gần đây nhất
	if snap.SupportingCount > 0 {
		b.WriteString(panelTitleStyle.Render(":: Hệ sinh thái nhân vật phụ"))
		b.WriteString("\n")
		b.WriteString(cardContentStyle.Render(truncate(fmt.Sprintf("Đã xuất hiện: %d người", snap.SupportingCount), contentW)))
		b.WriteString("\n")
		for _, name := range snap.RecentSupporting {
			writeBulletWrapped(&b, name, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	if snap.Synopsis != "" {
		b.WriteString(panelTitleStyle.Render(":: Giới thiệu"))
		b.WriteString("\n")
		for _, line := range wrapStreamText(snap.Synopsis, contentW) {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n\n")
	}

	// Tiền đề
	if snap.Premise != "" {
		b.WriteString(panelTitleStyle.Render(":: Tiền đề"))
		b.WriteString("\n")
		for _, line := range wrapStreamText(snap.Premise, contentW) {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n\n")
	}

	if snap.LastCommitSummary != "" {
		b.WriteString(cardTitleStyle.Render("~ Gần đây gửi ~"))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastCommitSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if snap.LastReviewSummary != "" {
		b.WriteString(cardTitleStyle.Render("~ Gần đây duyệt ~"))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastReviewSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if len(snap.RecentSummaries) > 0 {
		b.WriteString(cardTitleStyle.Render("~ Tóm tắt ~"))
		b.WriteString("\n")
		for _, s := range snap.RecentSummaries {
			writeWrapped(&b, s, contentW, cardContentStyle)
		}
	}

	return b.String()
}

// writeWrapped xuống dòng một đoạn văn bản theo chiều rộng thị giác, mỗi dòng được render style riêng.
func writeWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for _, line := range wrapStreamText(text, max(8, contentW)) {
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
}

// writeBulletWrapped ghi một mục "· ": xuống dòng theo chiều rộng thị giác, dòng tiếp theo thụt treo bằng hai cột khoảng trắng.
func writeBulletWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for i, line := range wrapStreamText(text, max(8, contentW-2)) {
		prefix := "· "
		if i > 0 {
			prefix = "  "
		}
		b.WriteString(style.Render(prefix + line))
		b.WriteString("\n")
	}
}
