package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
)

type startupMode int

const (
	startupModeQuick startupMode = iota
	startupModeCoCreate
)

func (m startupMode) label() string {
	switch m {
	case startupModeCoCreate:
		return "Kế hoạch cộng tác"
	default:
		return "Bắt đầu nhanh"
	}
}

func (m startupMode) subtitle() string {
	switch m {
	case startupModeCoCreate:
		return "Trò chuyện với AI để làm rõ trước rồi mới bắt đầu sáng tác"
	default:
		return "Chỉ cần một câu là bắt đầu viết ngay"
	}
}

func placeholderForNewMode(mode startupMode) string {
	switch mode {
	case startupModeCoCreate:
		return "Nhập ý tưởng cốt lõi của bạn trước, Enter để cùng AI cộng tác"
	default:
		return "Nhập một câu yêu cầu tiểu thuyết, Enter để bắt đầu sáng tác ngay"
	}
}

func placeholderForCoCreate(state *cocreateState) string {
	if state == nil {
		return placeholderForNewMode(startupModeCoCreate)
	}
	switch {
	case state.awaiting:
		return "AI đang sắp xếp yêu cầu của bạn..."
	case state.canStart():
		if state.stage {
			return "Tiếp tục bổ sung, hoặc nhấn Ctrl+S để áp dụng hướng và tiếp tục sáng tác"
		}
		return "Tiếp tục bổ sung, hoặc nhấn Ctrl+S để bắt đầu sáng tác"
	default:
		return "Tiếp tục bổ sung yêu cầu của bạn, Enter để gửi cho AI"
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

type cocreateState struct {
	session             *startup.CoCreateSession
	stage               bool // true=cộng tác giai đoạn; false=cộng tác khởi động
	awaiting            bool
	reqID               int
	cancel              context.CancelFunc // Hủy yêu cầu LLM hiện tại
	deltaCh             chan cocreateStreamItem
	doneCh              chan cocreateDoneMsg
	convVP              viewport.Model
	promptVP            viewport.Model
	convFollow          bool // true: nội dung mới tự cuộn xuống đáy; người dùng cuộn lên sẽ tắt theo dõi
	selectedSuggestions []string
	// focusPrompt quyết định ↑↓/PgUp/PgDn/Home/End cuộn cột nào: false=cột hội thoại bên trái (mặc định),
	// true=cột chỉ lệnh sáng tác bên phải. Trang chào mừng đã tắt báo cáo chuột (giữ lại sao chép nguyên sinh), khi cột phải tràn thì dùng Tab chuyển focus rồi cuộn bằng bàn phím.
	focusPrompt bool
}

func newCoCreateState(initial string) *cocreateState {
	makeVP := func() viewport.Model {
		vp := viewport.New(0, 0)
		vp.MouseWheelEnabled = true
		vp.MouseWheelDelta = 3
		return vp
	}
	return &cocreateState{
		session:    startup.NewCoCreateSession(strings.TrimSpace(initial)),
		awaiting:   true,
		convVP:     makeVP(),
		promptVP:   makeVP(),
		convFollow: true,
	}
}

// stageCoCreateOpener là lời mở đầu người dùng được tổng hợp cho giai đoạn đồng sáng tạo, được gửi cho LLM như lượt người dùng khởi động,
// để trợ lý chủ động mở đầu dựa trên "trạng thái câu chuyện hiện tại", thay vì để hội thoại trống chờ người dùng nói trước.
const stageCoCreateOpener = "Tôi tạm dừng một chút, muốn cùng bạn lên kế hoạch cho hướng đi tiếp theo."

// stageCoCreateSystemLine là cách trình bày trung tính của lời mở đầu này trong UI: câu mở đầu về bản chất là do hệ thống tổng hợp,
// người dùng chưa thật sự nhập, nên không giả làm lời nói của “Bạn”, mà dùng dòng hệ thống để trình bày ngữ cảnh (nó vẫn được gửi dưới dạng stageCoCreateOpener
// cho LLM, xem xử lý đặc biệt i==0 của renderCoCreateConversationPanel).
const stageCoCreateSystemLine = "Đã tạm dừng sáng tác, chuyển sang cộng tác giai đoạn — AI sẽ kết hợp tiến độ câu chuyện hiện tại để cùng bạn lên kế hoạch hướng đi tiếp theo."

// newStageCoCreateState tạo trạng thái giai đoạn đồng sáng tạo: seed lời mở đầu và đánh dấu stage, để runCoCreate đi theo
// StageCoCreateStream, Ctrl+S đi theo ResumeFromCoCreate.
func newStageCoCreateState() *cocreateState {
	s := newCoCreateState(stageCoCreateOpener)
	s.stage = true
	return s
}

func (s *cocreateState) appendUser(text string) {
	s.resetSuggestionInput()
	s.session.AppendUser(text)
}

func (s *cocreateState) apply(reply host.CoCreateReply) {
	s.awaiting = false
	s.resetSuggestionInput()
	s.session.ApplyReply(reply)
}

func (s *cocreateState) applyDelta(kind, text string) {
	s.session.ApplyDelta(kind, text)
}

func (s *cocreateState) canStart() bool {
	return s.session.CanStart()
}

func (s *cocreateState) initialInput() string {
	return s.session.InitialInput()
}

func (s *cocreateState) streamReply() string {
	return s.session.StreamReply()
}

func (s *cocreateState) draftPrompt() string {
	return s.session.DraftPrompt()
}

func (s *cocreateState) ready() bool {
	return s.session.Ready()
}

func (s *cocreateState) suggestions() []string {
	return s.session.Suggestions()
}

// appendSuggestion thêm gợi ý tương ứng với phím số vào ô nhập do phím tắt tạo.
// khi người dùng sửa ô nhập thủ công, current và tổ hợp gợi ý đã chọn không còn bằng nhau, phím số sẽ khôi phục thành nhập thông thường.
func (s *cocreateState) appendSuggestion(index int, current string) (string, bool) {
	suggestions := s.suggestions()
	if index < 0 || index >= len(suggestions) {
		return "", false
	}
	if len(s.selectedSuggestions) == 0 {
		if strings.TrimSpace(current) != "" {
			return "", false
		}
	} else if current != strings.Join(s.selectedSuggestions, "；") {
		s.resetSuggestionInput()
		return "", false
	}

	suggestion := strings.TrimSpace(suggestions[index])
	for _, selected := range s.selectedSuggestions {
		if selected == suggestion {
			return current, true
		}
	}

	s.selectedSuggestions = append(s.selectedSuggestions, suggestion)
	return strings.Join(s.selectedSuggestions, "；"), true
}

func (s *cocreateState) resetSuggestionInput() {
	s.selectedSuggestions = nil
}

func (s *cocreateState) buildPrompt() (string, error) {
	return s.session.BuildPrompt()
}

func renderStartupModeBar(width int, mode startupMode) string {
	quick := renderStartupModePill(mode == startupModeQuick, "Bắt đầu nhanh")
	cocreate := renderStartupModePill(mode == startupModeCoCreate, "Kế hoạch cộng tác")
	title := lipgloss.NewStyle().
		Foreground(colorAccent).
		Bold(true).
		Render("Chế độ khởi động")
	divider := lipgloss.NewStyle().
		Foreground(colorDim).
		Render("·")
	line := title + " " + divider + " " + quick + "  " + cocreate
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Render(line)
}

func renderStartupModePill(active bool, label string) string {
	style := lipgloss.NewStyle().Padding(0, 1)
	if active {
		style = style.Foreground(lipgloss.Color("#1c1a14")).Background(colorAccent).Bold(true)
	} else {
		style = style.Foreground(colorMuted)
	}
	return style.Render(label)
}

// coCreateColumns chia vùng nội dung modal thành chiều rộng hai cột trái phải.
// cột trái chứa hội thoại và ô nhập (xếp trên dưới), cột phải chứa bản nháp chỉ lệnh sáng tác; tổng bằng chiều rộng nội dung modal.
func coCreateColumns(bodyW int) (leftW, rightW int) {
	leftW = bodyW * 58 / 100
	if leftW < 42 {
		leftW = bodyW / 2
	}
	rightW = bodyW - leftW
	if rightW < 28 {
		rightW = 28
		leftW = bodyW - rightW
	}
	return leftW, rightW
}

func renderCoCreateBody(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int) string {
	if state == nil {
		return ""
	}
	leftW, rightW := coCreateColumns(width)

	// Viền phải do vùng chứa leftCol bên ngoài vẽ, chạy xuyên suốt phần thân; hội thoại / gợi ý /
	// ô nhập không tự vẽ viền phải. Ô nhập vẫn là hộp bo tròn hoàn chỉnh, có lề 1 cột ở hai bên để
	// thẳng hàng với phần đệm hội thoại, nhờ đó khoảng cách đến hai đường biên khớp nhau.
	// Ở chế độ cộng tác, textarea cố định 1 dòng (xem nhánh model.refitTextareaHeight),
	// nên chiều cao ô nhập = 1 (textarea) + 2 (viền trên/dưới) = 3 dòng và không bao giờ xê dịch.
	innerW := leftW - 1 // chừa 1 cột cho đường dọc bên phải lớp ngoài

	inputBox := lipgloss.NewStyle().
		Width(innerW-6). // -2 lề -2 phần đệm -2 viền
		Border(baseBorder).
		BorderForeground(colorDim).
		Padding(0, 1).
		Margin(0, 1).
		Render(inputView)

	suggestionsBox := renderCoCreateSuggestions(innerW, state)
	suggestionsH := 0
	if suggestionsBox != "" {
		suggestionsH = lipgloss.Height(suggestionsBox)
	}

	convH := height - lipgloss.Height(inputBox) - suggestionsH
	if convH < 4 {
		convH = 4
	}

	convPanel := renderCoCreateConversationPanel(innerW, convH, state, errMsg, spinnerFrame)

	var stack string
	if suggestionsBox == "" {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, inputBox)
	} else {
		stack = lipgloss.JoinVertical(lipgloss.Left, convPanel, suggestionsBox, inputBox)
	}

	leftCol := lipgloss.NewStyle().
		Border(baseBorder, false, true, false, false).
		BorderForeground(colorDim).
		Render(stack)

	rightPanel := renderCoCreatePromptPanel(rightW, height, state)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightPanel)
}

// extractReplyForDisplay cắt đoạn <reply>...</reply> khỏi lịch sử trợ lý.
// Các tag khác (<draft>/<ready>/<suggestions>) là trường giao thức cho lượt model tiếp theo và không nên hiển thị thô cho người dùng.
// Khi model chỉ tuân thủ một phần (thiếu tag mở <reply>), văn bản từ đầu đến </reply> hoặc tag mở tiếp theo được tính là phản hồi.
// Khi hoàn toàn không có tag (đường dự phòng), trả nội dung nguyên dạng.
func extractReplyForDisplay(content string) string {
	rest := content
	if rIdx := strings.Index(content, "<reply>"); rIdx >= 0 {
		rest = content[rIdx+len("<reply>"):]
	}
	if cIdx := strings.Index(rest, "</reply>"); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	cut := len(rest)
	for _, mark := range []string{"<draft>", "<ready>", "<suggestions>"} {
		if idx := strings.Index(rest, mark); idx >= 0 && idx < cut {
			cut = idx
		}
	}
	if cut == len(rest) && !strings.Contains(content, "<") {
		return content
	}
	return strings.TrimSpace(rest[:cut])
}

// renderCoCreateSuggestions render các dòng gợi ý AI phía trên ô nhập. Khi đang chờ hoặc không có gợi ý,
// hàm trả chuỗi rỗng để layout tự co lại không để dòng trống. Hiển thị tối đa 3 gợi ý và chọn bằng 1/2/3.
func renderCoCreateSuggestions(width int, state *cocreateState) string {
	if state == nil || state.awaiting {
		return ""
	}
	sugs := state.suggestions()
	if len(sugs) == 0 {
		return ""
	}
	if len(sugs) > 3 {
		sugs = sugs[:3]
	}

	digits := []string{"❶", "❷", "❸"}
	digitStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(colorMuted)
	hintStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	lines := []string{hintStyle.Render("Gợi ý AI (ghép 1/2/3, có thể sửa rồi gửi):")}
	for i, s := range sugs {
		lines = append(lines, digitStyle.Render(digits[i]+" ")+bodyStyle.Render(strings.TrimSpace(s)))
	}

	// Căn theo lề và phần đệm trái/phải của inputBox: bên trái 2 cột (lề 1 + đệm 1), bên phải cũng vậy.
	return lipgloss.NewStyle().
		Width(width-2).
		Padding(0, 2).
		Render(strings.Join(lines, "\n"))
}

func coCreateModalSize(width, height int) (boxW, boxH int) {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 24
	}
	boxW = min(max(width*76/100, 88), width-4)
	boxH = min(max(height*72/100, 22), height-4)
	if boxW < 64 {
		boxW = max(width-2, 42)
	}
	if boxH < 14 {
		boxH = max(height-2, 12)
	}
	return boxW, boxH
}

// coCreateInputWidth tính chiều rộng ký tự dùng được của textarea.
// Phần trang trí cột trái gồm: đường dọc phải ngoài 1 + lề trái/phải ô nhập 2 + viền 2 + phần đệm 2 = 7 cột;
// dấu nhắc + con trỏ của textarea chiếm 2 cột, do đó textareaW = leftW - 9.
func coCreateInputWidth(width, height int) int {
	boxW, _ := coCreateModalSize(width, height)
	bodyW := boxW - 4
	leftW, _ := coCreateColumns(bodyW)
	inputW := leftW - 9
	if inputW < 20 {
		inputW = 20
	}
	return inputW
}

func renderCoCreateModal(width, height int, state *cocreateState, errMsg, inputView string, spinnerFrame int, quitPending bool) string {
	if state == nil {
		return ""
	}

	boxW, boxH := coCreateModalSize(width, height)

	// Tiêu đề / phụ đề / gợi ý được đặt ngoài modal (căn giữa phía trên và dưới), dành toàn bộ thân modal
	// cho phần nội dung — viền phải cột trái và panel phải chạy từ đầu đến cuối modal.
	// Modal thực tế chiếm = boxH (nội dung) + 2 (phần đệm 1*2) + 2 (viền) = boxH+4 dòng;
	// toàn bộ khối = tiêu đề(1) + phụ đề(1) + dòng trống(1) + modal(boxH+4) + dòng trống(1) + gợi ý(1) = boxH+9.
	// Vì vậy trừ 5 dòng khỏi boxH để dành chỗ cho phần trang trí ngoài modal và tránh tràn terminal.
	contentH := boxH - 5
	if contentH < 10 {
		contentH = 10
	}

	titleText, subtitleText := "Kế hoạch cộng tác", "Trò chuyện để làm rõ yêu cầu trước rồi bắt đầu sáng tác"
	if state.stage {
		titleText, subtitleText = "Kế hoạch cộng tác giai đoạn", "Lên hướng tiếp theo rồi tiếp tục sáng tác"
	}
	headerStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	title := headerStyle.Foreground(colorMuted).Bold(true).Render(titleText)
	subtitle := headerStyle.Foreground(colorDim).Italic(true).Render(subtitleText)

	var hintLine string
	hintStyle := lipgloss.NewStyle().Width(boxW).AlignHorizontal(lipgloss.Center)
	if quitPending {
		// quitPending khớp với inputHints(); nếu không, modal cộng tác che thanh dưới và người dùng không bao giờ thấy gợi ý “nhấn Ctrl+C thêm lần nữa”.
		hintLine = hintStyle.Foreground(lipgloss.Color("243")).Bold(true).Render("Nhấn Ctrl+C thêm lần nữa để thoát")
	} else {
		hintLine = hintStyle.Foreground(colorDim).Italic(true).Render(coCreateHint(state))
	}

	body := renderCoCreateBody(boxW-4, contentH, state, errMsg, inputView, spinnerFrame)
	box := lipgloss.NewStyle().
		Width(boxW).
		Height(contentH).
		Border(baseBorder).
		BorderForeground(colorAccent).
		Padding(1, 2).
		Render(body)

	stack := lipgloss.JoinVertical(lipgloss.Center, title, subtitle, "", box, "", hintLine)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, stack)
}

// coCreateHint tạo chuỗi gợi ý phím ngắn, tránh lặp lại ý nghĩa của placeholder.
func coCreateHint(state *cocreateState) string {
	switch {
	case state == nil:
		return "Enter gửi · Esc thoát"
	case state.awaiting:
		return "AI đang trả lời · ↑↓ cuộn hội thoại · cuộn chuột cho chỉ dẫn · Esc thoát"
	case state.canStart():
		action := "Ctrl+S bắt đầu sáng tác"
		if state.stage {
			action = "Ctrl+S áp dụng và tiếp tục"
		}
		return "Enter tiếp tục bổ sung · " + action + " · ↑↓ cuộn hội thoại · cuộn chuột cho chỉ dẫn · Esc thoát"
	default:
		return "Enter gửi · ↑↓ cuộn hội thoại · cuộn chuột cho chỉ dẫn · Esc thoát"
	}
}

func renderCoCreateConversationPanel(width, height int, state *cocreateState, errMsg string, spinnerFrame int) string {
	// Không tự vẽ viền — đường dọc phải do vùng chứa leftCol bên ngoài vẽ.
	// Tổng chiều rộng cột = width; style.Width = contentW = width-2; sau Padding(0,1), vùng nội dung = contentW-2.
	// Các dòng cũng phải tính đến tiền tố 2 cột như "▌ " / "  "; nếu không sau khi xuống dòng, mỗi dòng cộng tiền tố sẽ tràn vùng nội dung 2 cột,
	// khiến terminal tự xuống dòng — lipgloss vẫn cho rằng chiều cao modal cố định, nhưng terminal thực tế vẽ thêm dòng,
	// và khi suy nghĩ luồng lặp lại, khung ngoài sẽ bị “rung” chiều cao. Do đó wrapW = contentW - 4.
	contentW := width - 2
	if contentW < 12 {
		contentW = 12
	}
	wrapW := max(12, contentW-4)

	userRole := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Render("Bạn")
	aiRole := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("AI")
	userBody := lipgloss.NewStyle().Foreground(colorAccent2)
	aiBody := lipgloss.NewStyle().Foreground(bodyTextColor)
	thinkingStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
	thinkingTag := lipgloss.NewStyle().Foreground(colorDim).Bold(true).Render("AI đang suy nghĩ")

	sysStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)

	var lines []string
	for i, item := range state.session.History() {
		isUser := item.Role != "assistant"
		// Lời mở đầu cộng tác tổng hợp ở chế độ giai đoạn (luôn là thông điệp người dùng tại history[0]) được hiển thị như một dòng hệ thống trung tính,
		// không ngụy trang thành nội dung người dùng; lời này vẫn được gửi tới LLM dưới dạng lượt người dùng khởi đầu.
		if isUser && state.stage && i == 0 {
			for j, line := range wrapStreamText(stageCoCreateSystemLine, wrapW) {
				prefix := "· "
				if j > 0 {
					prefix = "  "
				}
				lines = append(lines, sysStyle.Render(prefix+line))
			}
			lines = append(lines, "")
			continue
		}
		if isUser {
			lines = append(lines, userRole)
			for _, line := range wrapStreamText(strings.TrimSpace(item.Content), wrapW) {
				// Hiển thị toàn bộ dòng một lần để tránh rò mã điều khiển ANSI khi phần đặt lại của tiền tố gặp màu phần thân.
				lines = append(lines, userBody.Render("▌ "+line))
			}
		} else {
			lines = append(lines, aiRole)
			// Lịch sử trợ lý lưu toàn bộ nội dung thô gồm bốn phần (cho ngữ cảnh mô hình); UI chỉ hiển thị đoạn [REPLY].
			display := extractReplyForDisplay(item.Content)
			for _, line := range wrapStreamText(strings.TrimSpace(display), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
		}
		lines = append(lines, "")
	}

	if state.awaiting {
		if t := state.session.StreamThinking(); t != "" {
			lines = append(lines, thinkingTag)
			for _, line := range wrapStreamText(t, wrapW) {
				lines = append(lines, thinkingStyle.Render("  "+line))
			}
			lines = append(lines, "")
		}
		if state.streamReply() != "" {
			lines = append(lines, aiRole)
			for _, line := range wrapStreamText(state.streamReply(), wrapW) {
				lines = append(lines, aiBody.Render("  "+line))
			}
			lines = append(lines, "")
		}
		// Trang trí lấp lánh: luôn cho người dùng biết rằng “AI đang làm việc”.
		lines = append(lines, strings.TrimLeft(renderEventSparkle(spinnerFrame, contentW), " "))
	}
	if errMsg != "" {
		lines = append(lines, "")
		lines = append(lines, lipgloss.NewStyle().Foreground(colorError).Render("! "+errMsg))
	}

	// Dùng viewport thay vì cắt thủ công để người dùng có thể cuộn ngược.
	// Chiều cao vp = chiều cao panel - 1 dòng tiêu đề. Sau SetContent, nếu người dùng đã ở đáy,
	// tự cuộn tới nội dung mới nhất (theo luồng); nếu người dùng cuộn lên, tắt convFollow sẽ ngừng theo dõi.
	vpH := height - 1
	if vpH < 1 {
		vpH = 1
	}
	if state.convVP.Width != contentW || state.convVP.Height != vpH {
		state.convVP.Width = contentW
		state.convVP.Height = vpH
	}
	state.convVP.SetContent(strings.Join(lines, "\n"))
	if state.convFollow {
		state.convVP.GotoBottom()
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)
	return style.Render(panelTitleStyle.Render(":: Hội thoại cộng tác") + "\n" + state.convVP.View())
}

func renderCoCreatePromptPanel(width, height int, state *cocreateState) string {
	readyLabel := "Đã có thể bắt đầu sáng tác"
	if state.stage {
		readyLabel = "Có thể áp dụng và tiếp tục"
	}
	status := lipgloss.NewStyle().Foreground(colorDim).Render("Đang tiếp tục hội thoại")
	if state.ready() {
		status = lipgloss.NewStyle().Foreground(colorAccent).Render(readyLabel)
	}
	if state.awaiting {
		status = lipgloss.NewStyle().Foreground(colorMuted).Italic(true).Render("AI đang sắp xếp")
	}

	// Chiều rộng nội dung = tổng chiều rộng cột - 2 (Padding(0,1) dùng 2 cột, không có viền).
	contentW := width - 2
	if contentW < 8 {
		contentW = 8
	}

	emptyHint := "AI sẽ liên tục sắp xếp thành một chỉ dẫn cuối cùng có thể đưa thẳng vào sáng tác."
	panelTitle := ":: Chỉ dẫn sáng tác hiện tại"
	if state.stage {
		emptyHint = "AI sẽ liên tục sắp xếp brief định hướng cho giai đoạn tiếp theo ở đây."
		panelTitle = ":: Hướng tiếp theo"
	}
	text := strings.TrimSpace(state.draftPrompt())
	if text == "" {
		text = lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render(emptyHint)
	} else {
		text = renderMarkdownPreview(text, max(12, contentW-2))
	}
	vpHeight := height - 5
	if vpHeight < 3 {
		vpHeight = 3
	}
	if state.promptVP.Width != contentW || state.promptVP.Height != vpHeight {
		state.promptVP.Width = contentW
		state.promptVP.Height = vpHeight
	}
	state.promptVP.MouseWheelEnabled = true
	state.promptVP.SetContent(text)

	hint := ""
	if state.promptVP.TotalLineCount() > state.promptVP.VisibleLineCount() {
		switch {
		case state.promptVP.AtTop():
			hint = "↓ Bên dưới còn nội dung, có thể cuộn hoặc PgDn để xem"
		case state.promptVP.AtBottom():
			hint = "↑ Bên trên còn nội dung, có thể cuộn hoặc PgUp để xem"
		default:
			hint = "↑↓ có thể tiếp tục cuộn để xem"
		}
	}

	style := lipgloss.NewStyle().
		Width(contentW).
		Height(height).
		Padding(0, 1)

	body := panelTitleStyle.Render(panelTitle) + "\n" + status + "\n\n" + state.promptVP.View()
	if hint != "" {
		body += "\n\n" + lipgloss.NewStyle().
			Width(contentW).
			AlignHorizontal(lipgloss.Center).
			Foreground(colorDim).
			Italic(true).
			Render(hint)
	}
	return style.Render(body)
}

func renderMarkdownPreview(text string, width int) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}

	h1Style := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	h2Style := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	h3Style := lipgloss.NewStyle().Foreground(colorMuted).Bold(true)
	bulletStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true)
	codeStyle := lipgloss.NewStyle().Foreground(colorMuted).Italic(true)

	var out []string
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			out = append(out, "")
			continue
		}

		switch {
		case strings.HasPrefix(line, "# "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "# "))
			out = append(out, h1Style.Render(title))
		case strings.HasPrefix(line, "## "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "## "))
			out = append(out, h2Style.Render(title))
		case strings.HasPrefix(line, "### "):
			title := strings.TrimSpace(strings.TrimPrefix(line, "### "))
			out = append(out, h3Style.Render(title))
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			body := strings.TrimSpace(line[2:])
			wrapped := wrapStreamText(body, max(8, width-4))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render("• ")+cardContentStyle.Render(item))
				} else {
					out = append(out, "  "+cardContentStyle.Render(item))
				}
			}
		case isOrderedMarkdownItem(line):
			prefix, body := splitOrderedMarkdownItem(line)
			wrapped := wrapStreamText(body, max(8, width-len(prefix)-2))
			for i, item := range wrapped {
				if i == 0 {
					out = append(out, bulletStyle.Render(prefix+" ")+cardContentStyle.Render(item))
				} else {
					out = append(out, strings.Repeat(" ", len(prefix)+1)+cardContentStyle.Render(item))
				}
			}
		case strings.HasPrefix(line, "> "):
			body := strings.TrimSpace(strings.TrimPrefix(line, "> "))
			for _, item := range wrapStreamText(body, max(8, width-4)) {
				out = append(out, codeStyle.Render("│ "+item))
			}
		default:
			for _, item := range wrapStreamText(line, width) {
				out = append(out, cardContentStyle.Render(item))
			}
		}
	}
	return strings.Join(out, "\n")
}

func isOrderedMarkdownItem(line string) bool {
	if len(line) < 3 {
		return false
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func splitOrderedMarkdownItem(line string) (prefix, body string) {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return "", strings.TrimSpace(line)
	}
	return line[:i+1], strings.TrimSpace(line[i+2:])
}
