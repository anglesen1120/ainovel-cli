package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/utils"
)

const maxEvents = 500

// maxStreamRounds giới hạn số vòng được giữ lại của bảng streaming. Mỗi lần LLM call kết thúc sẽ kích hoạt một lần streamClear
// Mỗi vòng mới, writer cho một chương khoảng 3~5 vòng (agent header / suy nghĩ / draft / commit), 32 vòng xấp xỉ
// tương đương việc xem lại luồng xuất của 6~10 chương gần nhất. Phần nội dung chương đã commit được ghi xuống store/drafts, vượt quá thì loại bỏ để tránh
// mỗi token delta đều kích hoạt render lại O(toàn bộ). Ổn định bộ nhớ tối đa khoảng 512KB, thấp hơn nhiều so với ngưỡng gây giật.
const maxStreamRounds = 32

type focusPane int

const (
	focusEvents focusPane = iota
	focusStream
	focusDetail
	focusState // thanh bên trạng thái bên trái (có thể cuộn)

	focusPaneCount // tổng số focus, dùng để xoay vòng Tab
)

type appMode int

const (
	modeNew     appMode = iota // chờ người dùng nhập yêu cầu tiểu thuyết
	modeRunning                // đang sáng tác (bao gồm dừng do lỗi, nhập có thể khôi phục)
	modeDone                   // sáng tác hoàn tất
)

// Dãy khung spinner dùng chung cho thanh trên cùng / hoạt động streaming (bubbles.Spinner.MiniDot).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Dãy khung spinner riêng cho dòng "đang xử lý" của luồng sự kiện (bubbles.Spinner.Dot).
// 7 chấm + 1 khe khuyết quay theo chiều kim đồng hồ trên lưới 3×3, trông như một vòng tải hoàn chỉnh.
// Dùng chỉ số khung riêng + tick nhanh hơn, không ảnh hưởng nhịp của thanh trên cùng và hoạt ảnh ngôi sao.
var toolSpinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Model là trạng thái cấp cao nhất của TUI.
type Model struct {
	runtime        *host.Host
	cocreate       *cocreateState
	help           *helpState
	modelSwitch    *modelSwitchState
	modelConfig    *modelConfigState
	report         *reportState
	version        string
	importer       *importState
	importSeq      int
	simulator      *simulationState
	simSeq         int
	compItems      []commandPaletteItem
	compIdx        int
	compActive     bool
	commandToken   string // command token hiện đã đăng ký; chỉ render đoạn đó, không tô màu tham số
	snapshot       host.UISnapshot
	events         []host.Event
	eventIndex     map[string]int   // event.ID → chỉ số m.events; cập nhật tại chỗ khi sự kiện kiểu gọi đến
	viewport       viewport.Model   // Luồng sự kiện viewport
	streamVP       viewport.Model   // viewport luồng xuất
	detailVP       viewport.Model   // viewport chi tiết bên phải
	stateVP        viewport.Model   // viewport thanh bên trạng thái bên trái (có thể cuộn)
	streamBuf      *strings.Builder // bộ đệm tích lũy văn bản streaming
	streamRounds   []string
	textarea       textarea.Model
	width          int
	height         int
	autoScroll     bool
	streamScroll   bool      // tự theo dõi ở bảng streaming
	streamDirty    bool      // streamRounds có delta chưa được làm mới
	flushPending   bool      // đã lên lịch một lần làm mới streaming, tránh khởi động timer lặp lại cho mỗi delta
	lastKeyAt      time.Time // thời điểm phím không phải Enter trước đó; KeyEnter throttle để tránh dán \n kích hoạt submit nhầm
	inputHistory   []string  // lịch sử Nhập đã submit (khử trùng lặp: không lặp liền kề)
	historyIdx     int       // chỉ số duyệt hiện tại; == len(inputHistory) nghĩa là "chưa duyệt, đang chỉnh sửa bản nháp"
	historyDraft   string    // bản nháp đã lưu trước khi vào duyệt lịch sử, sẽ khôi phục khi quay lại cuối
	focusPane      focusPane
	hoverPane      focusPane
	hoverActive    bool
	mode           appMode
	starting       bool // UI đã vào workspace, Host đang thực hiện quá trình khởi động
	startupMode    startupMode
	importHint     string // gợi ý phát hiện lúc khởi động có import chưa hoàn tất (hiển thị ở màn hình chào; xong import thì xóa)
	cocreateSeq    int
	reportSeq      int
	err            error
	spinnerIdx     int
	toolSpinnerIdx int  // chỉ số khung độc lập của dòng "đang xử lý" ở luồng sự kiện (tick 150ms, không ảnh hưởng thanh trên cùng/ngôi sao)
	toolTicking    bool // timer animation tool đã khởi động; tự dừng khi không có event đang chạy
	cursorIdx      int  // chỉ số khung con trỏ streaming (tiến cùng hoạt ảnh chính)
	streamRound    int  // bộ đếm vòng luồng xuất
	quitPending    bool // xác nhận thoát bằng Ctrl+C hai lần
	abortPending   bool // đang chờ Done trở về cho thao tác tạm dừng thủ công
	mouseOff       bool // khi true đã tắt báo cáo chuột, cho phép người dùng kéo thả nguyên bản để chọn/copy; chuyển lại lần nữa để khôi phục
}

// NewModel tạo Model của TUI.
func NewModel(rt *host.Host, version string) Model {
	ta := textarea.New()
	ta.Placeholder = placeholderForNewMode(startupModeQuick)
	ta.CharLimit = 5000
	ta.SetHeight(1)
	// MaxHeight=6 cho phép Nhập quá dài tự wrap theo chiều rộng và hiển thị thành nhiều dòng (giới hạn trực quan 6 dòng).
	ta.MaxHeight = 6
	ta.ShowLineNumbers = false
	ta.Focus()

	// mặc định Enter không xuống dòng (do handleEnterKey submit);
	// chuyển xuống dòng chủ động được bind lại thành ctrl+j (unix \n) và alt+enter (thói quen GUI).
	// tầng giao thức của terminal không phân biệt Shift+Enter với Enter, nên không hỗ trợ Shift+Enter.
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")

	vp := viewport.New(80, 20)
	vp.SetContent("")

	svp := viewport.New(80, 10)
	svp.SetContent("")

	dvp := viewport.New(40, 20)
	dvp.SetContent("")

	stvp := viewport.New(32, 20)
	stvp.SetContent("")

	// lúc khởi động kiểm tra một lần import chưa hoàn tất (LoadState tính lại digest của artifact, không vào vòng poll snapshot);
	// nếu không báo trước, người dùng chỉ phát hiện khi việc sáng tác bị chặn bởi cửa kiểm soát (RFC §18.2).
	importHint := ""
	if rt != nil {
		importHint = rt.ImportResumeHint()
	}

	return Model{
		runtime:      rt,
		version:      strings.TrimSpace(version),
		autoScroll:   true,
		streamScroll: true,
		mode:         modeNew,
		startupMode:  startupModeQuick,
		importHint:   importHint,
		textarea:     ta,
		viewport:     vp,
		streamVP:     svp,
		detailVP:     dvp,
		stateVP:      stvp,
		streamBuf:    &strings.Builder{},
		eventIndex:   make(map[string]int),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		listenEvents(m.runtime),
		listenDone(m.runtime),
		listenStream(m.runtime),
		tickSnapshot(m.runtime),
		bootstrapRuntime(m.runtime),
		tickSpinner(),
	)
}

func (m *Model) paneAtMouse(x, y int) (focusPane, bool) {
	if m.width == 0 || m.height == 0 {
		return focusEvents, false
	}

	topH, _, bodyH := m.layoutHeights()
	if bodyH < 1 {
		return focusEvents, false
	}

	bodyStartY := topH
	bodyEndY := topH + bodyH
	if y < bodyStartY || y >= bodyEndY {
		return focusEvents, false
	}

	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	centerStartX := leftW
	rightStartX := m.width - rightW

	if x >= rightStartX {
		return focusDetail, true
	}
	if x < centerStartX {
		return focusState, true
	}

	eventH, _ := m.splitHeights(bodyH)
	if y-bodyStartY < eventH {
		return focusEvents, true
	}
	return focusStream, true
}

func (m *Model) paneHighlighted(pane focusPane) bool {
	if m.focusPane == pane {
		return true
	}
	return m.hoverActive && m.hoverPane == pane
}

// hasRunningEvent có tồn tại event kiểu gọi chưa hoàn tất (spinner vẫn đang quay) hay không.
// toolSpinnerTick dùng điều này để quyết định có đáng render lại hay không: khi không có running event thì khung spinner không ảnh hưởng Xuất,
// toàn bộ refreshEventViewport là công việc vô ích mang tính tất định.
func (m *Model) hasRunningEvent() bool {
	for i := range m.events {
		if m.events[i].Running() {
			return true
		}
	}
	return false
}

// flushStreamIfDirty render streamRounds đã tích lũy ra viewport; đánh dấu là đã flush.
// trả về xem có flush thật hay không, để phía gọi quyết định có nên GotoBottom hay không.
func (m *Model) flushStreamIfDirty() bool {
	if !m.streamDirty {
		return false
	}
	m.refreshStreamViewport()
	m.streamDirty = false
	return true
}

// refreshEventViewport render lại nội dung Luồng sự kiện và thiết lập viewport.
func (m *Model) refreshEventViewport() {
	centerW := m.eventFlowWidth()
	content := renderEventContent(m.events, centerW, m.toolSpinnerIdx)
	snap := m.snapshot
	if m.starting {
		snap.IsRunning = true
	}
	if activity := renderEventActivity(snap, m.spinnerIdx, centerW); activity != "" {
		if strings.TrimSpace(content) != "" {
			content += "\n" + activity
		} else {
			content = activity
		}
	}
	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) refreshStreamViewport() {
	cursor := ""
	if m.snapshot.IsRunning {
		cursor = renderStreamCursor(m.cursorIdx)
	}
	m.streamVP.SetContent(renderStreamContent(m.streamRounds, m.streamVP.Width, cursor))
}

func (m *Model) refreshDetailViewport() {
	rightW := m.detailWidth()
	if rightW <= 4 {
		return
	}
	m.detailVP.SetContent(renderDetailContent(m.snapshot, rightW-4))
}

// refreshStateViewport đưa nội dung thanh bên trạng thái bên trái vào viewport.
// Nội dung thanh bên chỉ được suy ra từ snapshot, nên khi snapshot hoặc kích thước thay đổi đều phải render lại.
func (m *Model) refreshStateViewport() {
	leftW := m.sidebarWidth()
	if leftW <= 4 {
		return
	}
	m.stateVP.SetContent(renderStateContent(m.snapshot, leftW-4))
}

// updateViewportSize cập nhật kích thước viewport theo kích thước cửa sổ hiện tại.
func (m *Model) updateViewportSize() {
	centerW := m.eventFlowWidth()
	rightW := m.detailWidth()
	bodyH := m.bodyHeight()
	eventH, streamH := m.splitHeights(bodyH)
	m.viewport.Width = centerW - 2
	m.viewport.Height = eventH - 1 // -1 là dòng tiêu đề của event panel
	m.streamVP.Width = centerW - 2
	m.streamVP.Height = streamH - 1 // -1 là dòng tiêu đề của stream panel
	m.detailVP.Width = rightW - 2
	m.detailVP.Height = bodyH
	leftW := m.sidebarWidth()
	m.stateVP.Width = max(1, leftW-2)
	m.stateVP.Height = max(1, bodyH-1) // -1 chừa khoảng trống ở trên, dòng dưới cùng hiển thị nội dung trực tiếp
	// Sau khi chiều cao hoặc nội dung ngắn lại, hai cột trái/phải cuộn tự do có thể dừng ở offset vượt biên (của bubbles
	// SetContent chỉ ngăn vượt quá dòng cuối), viewport sẽ dùng dòng trống để lấp đầy phần đáy. SetYOffset tự clamp.
	m.stateVP.SetYOffset(m.stateVP.YOffset)
	m.detailVP.SetYOffset(m.detailVP.YOffset)
}

// splitHeights tính phân bổ chiều cao cho Luồng sự kiện và Xuất streaming.
func (m *Model) splitHeights(bodyH int) (eventH, streamH int) {
	eventH = bodyH * 40 / 100
	if eventH < 3 {
		eventH = 3
	}
	streamH = bodyH - eventH - 1 // -1 cho đường phân cách
	if streamH < 3 {
		streamH = 3
	}
	return
}

func (m *Model) inputWidth() int {
	if m.width == 0 {
		return 60
	}
	return m.width - 6 // border + padding + prompt "❯ "
}

func (m *Model) currentInputWidth() int {
	if m.cocreate != nil {
		return coCreateInputWidth(m.width, m.height)
	}
	return m.inputWidth()
}

// refitTextareaHeight ước tính số dòng hiển thị theo nội dung hiện tại, SetHeight động.
// Dòng hiển thị = tổng của từng đoạn trong dòng logic (tách bằng \n) sau khi wrap theo chiều rộng. Phối hợp với MaxHeight=6
// để đạt "nội dung quá dài/xuống dòng chủ động tự động hiển thị nhiều dòng, tối đa 6 dòng".
func (m *Model) refitTextareaHeight() {
	w := m.textarea.Width()
	if w <= 0 {
		return
	}
	// Trong chế độ đồng sáng tạo, input cố định 1 dòng: nội dung nhiều dòng của textarea sẽ được chính textarea hiển thị cuộn theo con trỏ
	//. Nếu không, chiều cao inputBox thay đổi theo nội dung, sẽ làm conversation cột trái co lại,
	// input trôi theo chiều dọc, phá vỡ tính ổn định của layout.
	if m.cocreate != nil {
		m.textarea.SetHeight(1)
		return
	}
	text := m.textarea.Value()
	if text == "" {
		m.textarea.SetHeight(1)
		return
	}
	// Trừ dư 2 cột (prompt symbol + cursor bên trong textarea), dư thêm 1 dòng là chấp nhận được.
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}
	total := 0
	for line := range strings.SplitSeq(text, "\n") {
		lw := lipgloss.Width(line)
		if lw == 0 {
			total++
			continue
		}
		total += (lw + contentW - 1) / contentW
	}
	if total < 1 {
		total = 1
	}
	m.textarea.SetHeight(total) // bên trong SetHeight clamp theo MaxHeight
}

// resizeTextarea đồng bộ thiết lập chiều rộng và chiều cao dựa trên nội dung.
// Thay thế các lệnh gọi SetWidth(currentInputWidth()) rải rác, đảm bảo khi chiều rộng thay đổi thì chiều cao thay đổi theo.
func (m *Model) resizeTextarea() {
	m.textarea.SetWidth(m.currentInputWidth())
	m.refitTextareaHeight()
}

// maxInputHistory giới hạn độ dài lịch sử, tránh tăng bộ nhớ trong hội thoại dài.
const maxInputHistory = 200

// pushInputHistory thêm nội dung đã gửi thành công vào lịch sử, khử trùng lặp các mục liền kề. Đồng thời reset chỉ mục duyệt.
func (m *Model) pushInputHistory(text string) {
	if text == "" {
		return
	}
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
		if len(m.inputHistory) > maxInputHistory {
			m.inputHistory = m.inputHistory[len(m.inputHistory)-maxInputHistory:]
		}
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

// tryHistoryUp đi tới một mục lịch sử cũ hơn; trả về có xử lý phím hay không.
// Khi lần đầu vào duyệt lịch sử, lưu nội dung textarea hiện tại làm draft, khi quay về cuối thì khôi phục.
// Bên gọi cần tự xác định trong tình huống nhiều dòng có nên bỏ qua hay không (để textarea xử lý di chuyển con trỏ trong dòng).
func (m *Model) tryHistoryUp() bool {
	if len(m.inputHistory) == 0 || m.historyIdx <= 0 {
		return false
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyDraft = m.textarea.Value()
	}
	m.historyIdx--
	m.textarea.SetValue(m.inputHistory[m.historyIdx])
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()
	m.refitTextareaHeight()
	return true
}

// tryHistoryDown đi tới một mục lịch sử mới hơn; đến cuối thì khôi phục draft.
func (m *Model) tryHistoryDown() bool {
	if m.historyIdx >= len(m.inputHistory) {
		return false
	}
	m.historyIdx++
	if m.historyIdx == len(m.inputHistory) {
		m.textarea.SetValue(m.historyDraft)
		m.historyDraft = ""
	} else {
		m.textarea.SetValue(m.inputHistory[m.historyIdx])
	}
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()
	m.refitTextareaHeight()
	return true
}

// textareaIsMultiline nội dung textarea hiện tại có chứa xuống dòng chủ động hay không; dùng để quyết định ↑↓ đi theo lịch sử hay di chuyển trong dòng.
func (m *Model) textareaIsMultiline() bool {
	return strings.Contains(m.textarea.Value(), "\n")
}

// inputHints tạo văn bản gợi ý ở đáy theo trạng thái hiện tại.
// Cuối cùng thêm thống nhất copySuffix, để người dùng ở mọi trạng thái không khẩn cấp đều thấy cách sao chép bằng chọn văn bản;
// Khi chuột đã tắt, hiển thị gợi ý chữ đỏ nổi bật, nhắc nhấn phím lần nữa để khôi phục tương tác chuột.
func (m *Model) inputHints() string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	if m.quitPending {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Bold(true).Render("Nhấn Ctrl+C một lần nữa để thoát")
	}
	limitHint := m.inputLimitHint()
	// Trang chào mừng (modeNew) không bật mouse reporting, có thể sao chép bằng kéo thả native của terminal, không cần gợi ý Ctrl+R;
	// Chỉ workspace mới bật reporting, sao chép cần tạm thời Tắt bằng Ctrl+R.
	suffix := limitHint + " · Ctrl+R chuyển sang chế độ sao chép vùng chọn"
	if m.mode == modeNew {
		suffix = limitHint
	}
	if m.mouseOff && m.mode != modeNew {
		// Workspace chuyển thủ công sang sao chép bằng chọn văn bản: dùng màu nhấn để cho biết đang ở trạng thái "tự do kéo chọn văn bản", nhấn Ctrl+R để khôi phục
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render("✂ Chế độ sao chép vùng chọn: kéo thả để sao chép văn bản · Ctrl+R thoát và khôi phục chuột")
	}
	if m.cocreate != nil {
		scrollHint := " · Tab cuộn:hội thoại"
		if m.cocreate.focusPrompt {
			scrollHint = " · Tab cuộn:chỉ dẫn sáng tác"
		}
		switch {
		case m.cocreate.awaiting:
			return dimStyle.Render("Đang chờ AI trả lời · Esc thoát đồng sáng tạo" + scrollHint + suffix)
		case m.cocreate.canStart():
			startLabel := "Ctrl+S bắt đầu sáng tác"
			if m.cocreate.stage {
				startLabel = "Ctrl+S áp dụng và tiếp tục"
			}
			return dimStyle.Render("Enter gửi · " + startLabel + " · Esc thoát đồng sáng tạo" + scrollHint + suffix)
		default:
			return dimStyle.Render("Enter gửi · Esc thoát đồng sáng tạo" + scrollHint + suffix)
		}
	}
	if m.mode == modeNew {
		if m.startupMode == startupModeQuick {
			return dimStyle.Render("Tab đổi chế độ khởi động · Nhập / để tìm lệnh · Enter bắt đầu sáng tác ngay · Esc xóa nội dung" + suffix)
		}
		return dimStyle.Render("Tab đổi chế độ khởi động · Nhập / để tìm lệnh · Enter vào hội thoại đồng sáng tạo · Esc xóa nội dung" + suffix)
	}
	switch m.snapshot.RuntimeState {
	case "pausing":
		return dimStyle.Render("Đang tạm dừng sáng tác · Vui lòng đợi vòng hiện tại kết thúc" + suffix)
	case "paused":
		return dimStyle.Render("Nhập / để tìm lệnh · Enter tiếp tục sáng tác · Esc xóa nội dung" + suffix)
	}
	return dimStyle.Render("Nhập / để tìm lệnh · Bấm/Tab đổi khung · ↑↓ cuộn · End nhảy xuống đáy · Ctrl+L xóa màn hình · Esc tạm dừng · Enter gửi" + suffix)
}

func (m *Model) inputLimitHint() string {
	limit := m.textarea.CharLimit
	if limit <= 0 {
		return ""
	}
	used := m.textarea.Length()
	if used < limit*4/5 {
		return ""
	}
	return fmt.Sprintf(" · Nhập %d/%d", used, limit)
}

func (m *Model) eventFlowWidth() int {
	if m.width == 0 {
		return 80
	}
	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	return m.width - leftW - rightW
}

func (m *Model) sidebarWidth() int {
	if m.width == 0 {
		return 32
	}
	return m.width * 23 / 100
}

func (m *Model) detailWidth() int {
	if m.width == 0 {
		return 40
	}
	return m.width * 27 / 100
}

func (m *Model) bodyHeight() int {
	_, _, bodyH := m.layoutHeights()
	return bodyH
}

func (m *Model) currentSpinnerFrame() string {
	if !m.snapshot.IsRunning && !m.starting {
		return ""
	}
	return spinnerFrames[m.spinnerIdx%len(spinnerFrames)]
}

func (m *Model) outputDir() string {
	if m.runtime == nil {
		return ""
	}
	return m.runtime.Dir()
}

func defaultSteerPlaceholder() string {
	return "Nhập can thiệp cốt truyện, ví dụ: đưa tuyến tình cảm lên Chương 4"
}

func (m *Model) syncRuntimePlaceholder() {
	if m.mode != modeRunning || m.cocreate != nil {
		return
	}
	if m.starting {
		m.textarea.Placeholder = "Đang khởi tạo sáng tác..."
		return
	}
	switch m.snapshot.RuntimeState {
	case "completed":
		m.textarea.Placeholder = donePlaceholder
	case "pausing":
		m.textarea.Placeholder = "Đang tạm dừng sáng tác..."
	case "paused":
		if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
			m.textarea.Placeholder = "Đang chờ duyệt từng chương: nhập ý kiến chỉnh sửa hoặc /next để cho qua chương tiếp theo"
		} else {
			m.textarea.Placeholder = "Sáng tác đã tạm dừng, nhập bất kỳ nội dung nào để tiếp tục"
		}
	default:
		if !m.snapshot.IsRunning {
			if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
				m.textarea.Placeholder = "Đang chờ duyệt từng chương: nhập ý kiến chỉnh sửa hoặc /next để cho qua chương tiếp theo"
			} else {
				m.textarea.Placeholder = "Quá trình bị gián đoạn, nhập bất kỳ nội dung nào để khôi phục sáng tác"
			}
		} else {
			m.textarea.Placeholder = defaultSteerPlaceholder()
		}
	}
}

func (m *Model) renderBottomBar() string {
	inputView := highlightCommandToken(m.textarea.View(), m.textarea.Value(), m.commandToken)
	inputBox := renderInputBox(
		inputView,
		m.inputHints(),
		m.snapshot,
		m.outputDir(),
		m.width,
	)
	if m.mode != modeNew || m.cocreate != nil {
		return inputBox
	}
	return renderStartupModeBar(m.width, m.startupMode) + "\n" + inputBox
}

func (m *Model) layoutHeights() (topH, inputH, bodyH int) {
	if m.width == 0 || m.height == 0 {
		return 1, 4, 20
	}
	topH = lipgloss.Height(renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version))
	inputH = lipgloss.Height(m.renderBottomBar())
	bodyH = m.height - topH - inputH
	if bodyH < 3 {
		bodyH = 3
	}
	return
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Đang tải..."
	}
	if m.width < 100 {
		return lipgloss.NewStyle().
			Width(m.width).Height(m.height).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render("Bề rộng terminal không đủ, hãy mở rộng ít nhất đến 100 cột")
	}
	if m.cocreate != nil {
		return renderCoCreateModal(m.width, m.height, m.cocreate, errorText(m.err), m.textarea.View(), m.spinnerIdx, m.quitPending)
	}
	if m.help != nil {
		return renderHelpModal(m.width, m.height, m.help)
	}
	if m.report != nil {
		return renderReportModal(m.width, m.height, m.report)
	}
	if m.importer != nil {
		// Import không phụ thuộc Engine Trạng thái chạy, frame animation lấy trực tiếp spinnerIdx (currentSpinnerFrame trả về rỗng khi engine dừng).
		return renderImportModal(m.width, m.height, m.importer, m.spinnerIdx)
	}
	if m.simulator != nil {
		return renderSimulationModal(m.width, m.height, m.simulator)
	}

	topBar := renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version)
	inputBox := m.renderBottomBar()
	_, inputH, bodyH := m.layoutHeights()

	var body string
	if m.mode == modeNew {
		errMsg := ""
		if m.err != nil {
			errMsg = m.err.Error()
		}
		body = renderWelcome(m.width, bodyH, errMsg, m.startupMode, m.importHint)
	} else {
		leftW := m.sidebarWidth()
		rightW := m.detailWidth()
		centerW := m.width - leftW - rightW
		eventH, streamH := m.splitHeights(bodyH)

		if m.viewport.Width != centerW-2 || m.viewport.Height != eventH-1 {
			m.viewport.Width = centerW - 2
			m.viewport.Height = eventH - 1 // -1 cho dòng header của event panel
		}
		if m.streamVP.Width != centerW-2 || m.streamVP.Height != streamH-1 {
			m.streamVP.Width = centerW - 2
			m.streamVP.Height = streamH - 1 // -1 cho dòng header của stream panel
		}

		eventFlow := renderEventFlowViewport(m.viewport, centerW, eventH, m.paneHighlighted(focusEvents))
		streamPanel := renderStreamPanel(m.streamVP, centerW, streamH, m.paneHighlighted(focusStream), m.snapshot.IsRunning || m.starting, m.spinnerIdx)
		center := lipgloss.JoinVertical(lipgloss.Left, eventFlow, streamPanel)

		left := renderStatePanel(m.stateVP, leftW, bodyH, m.paneHighlighted(focusState))
		right := renderDetailPanel(m.detailVP, rightW, bodyH, m.paneHighlighted(focusDetail))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	}

	view := lipgloss.JoinVertical(lipgloss.Left, topBar, body, inputBox)

	// Lớp phủ popup: nổi phía trên đáy body, không ảnh hưởng layout
	if m.modelSwitch != nil {
		commandBar := renderModelSwitchBar(m.width, m.modelSwitch)
		view = overlayAboveInput(view, commandBar, inputH)
	} else if m.modelConfig != nil {
		view = overlayAboveInput(view, renderModelConfigModal(m.width, m.modelConfig), inputH)
	} else if m.compActive {
		commandBar := renderCommandPalette(m.width, m.compItems, m.compIdx)
		view = overlayAboveInput(view, commandBar, inputH)
	}
	return view
}

// sendCoCreate khởi tạo một lượt yêu cầu đồng sáng tạo, xử lý thống nhất reqID, textarea, placeholder.
func (m *Model) sendCoCreate() tea.Cmd {
	m.cocreateSeq++
	m.cocreate.reqID = m.cocreateSeq
	m.cocreate.awaiting = true
	m.resizeTextarea()
	m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
	m.textarea.Blur()
	return runCoCreate(m.runtime, m.cocreate)
}

func (m Model) handleCoCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cocreate == nil {
		return m, nil
	}
	state := m.cocreate

	// Bàn phím ↑↓/PgUp/PgDn/Home/End để cuộn; Tab chuyển focus cuộn giữa cột hội thoại trái ↔ cột chỉ dẫn sáng tác phải
	// (mặc định cột trái, người dùng xem lại phần chính). Trang chào mừng đã tắt mouse reporting để giữ sao chép native, khi cột phải tràn thì dùng Tab
	// chuyển focus rồi dùng bàn phím cuộn. Cột trái: cuộn lên thì tắt follow, cuộn tới đáy thì bật lại follow (theo dõi streaming).
	switch msg.Type {
	case tea.KeyTab:
		state.focusPrompt = !state.focusPrompt
		return m, nil
	case tea.KeyUp, tea.KeyPgUp:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		state.convFollow = false
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		return m, cmd
	case tea.KeyDown, tea.KeyPgDown:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		if state.convVP.AtBottom() {
			state.convFollow = true
		}
		return m, cmd
	case tea.KeyHome:
		if state.focusPrompt {
			state.promptVP.GotoTop()
			return m, nil
		}
		state.convFollow = false
		state.convVP.GotoTop()
		return m, nil
	case tea.KeyEnd:
		if state.focusPrompt {
			state.promptVP.GotoBottom()
			return m, nil
		}
		state.convFollow = true
		state.convVP.GotoBottom()
		return m, nil
	case tea.KeyEsc:
		return m.exitCoCreate()
	}

	// Khi Đang đợi AI trả lời, cho phép các thao tác chỉnh sửa (ký tự Nhập/backspace/con trỏ/Ctrl+U/xuống dòng nhiều dòng) ——
	// người dùng có thể nhập trước câu tiếp theo trong lúc AI suy nghĩ. Chặn thao tác gửi được hạ xuống trong từng case,
	// để throttle Enter xảy ra trước chặn awaiting —— như vậy mảnh \n dán vào vẫn có thể được bổ sung khoảng trắng.

	switch msg.Type {
	case tea.KeyCtrlS:
		if state.awaiting {
			return m, nil
		}
		if !state.canStart() {
			return m, nil
		}
		// Giai đoạn đồng sáng tạo: inject "brief hướng tiếp theo" rồi khôi phục sáng tác, quay về bàn chạy.
		if state.stage {
			draft := state.draftPrompt()
			m.cocreate = nil
			m.err = nil
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			return m, tea.Batch(resumeFromCoCreate(m.runtime, draft), m.textarea.Focus())
		}
		// Khởi động lạnh đồng sáng tạo: dùng chỉ dẫn sáng tác đã được sắp xếp để bắt đầu sáng tác.
		prompt, err := state.buildPrompt()
		if err != nil {
			m.err = err
			return m, nil
		}
		cmd := m.enterStarting(prompt)
		return m, tea.Batch(startRuntime(m.runtime, prompt), cmd)
	case tea.KeyEnter:
		// Alt+Enter → xuống dòng chủ động, để textarea.Update tiếp quản (KeyMap.InsertNewline đã bind phím này)
		if msg.Alt {
			break
		}
		// Khoảng cách với lần nhấn ký tự trước quá ngắn → xem là mảnh sót \n của luồng paste: chèn khoảng trắng thay cho submit.
		// Phải kiểm tra trước khi chặn awaiting — nếu không, trong lúc awaiting mảnh sót \n được paste sẽ bị chặn,
		// khiến "abc\ndef" bị nuốt thành "abcdef", không nhất quán với ngữ nghĩa của đường dẫn base.
		if !m.lastKeyAt.IsZero() && time.Since(m.lastKeyAt) < 50*time.Millisecond {
			var cmd tea.Cmd
			state.resetSuggestionInput()
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			m.refitTextareaHeight()
			return m, cmd
		}
		// Ý định submit thực sự: chặn trong lúc awaiting (không thể gửi request đồng thời)
		if state.awaiting {
			return m, nil
		}
		text := utils.CleanInputLine(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.err = nil
		state.appendUser(text)
		m.textarea.Reset()
		m.refitTextareaHeight()
		cmd := m.sendCoCreate()
		return m, cmd
	case tea.KeyCtrlU:
		state.resetSuggestionInput()
		m.textarea.Reset()
		m.refitTextareaHeight()
		return m, nil
	}

	// Các phím số 1/2/3 có thể kết hợp suggestion liên tiếp: lần đầu điền vào, các lần sau append bằng dấu chấm phẩy, bỏ qua lựa chọn trùng lặp.
	// Mọi chỉnh sửa thủ công đều sẽ thoát trạng thái kết hợp shortcut, sau đó các phím số giữ ngữ nghĩa Nhập thông thường.
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && !state.awaiting {
		if r := msg.Runes[0]; r >= '1' && r <= '3' {
			if value, handled := state.appendSuggestion(int(r-'1'), m.textarea.Value()); handled {
				m.textarea.SetValue(value)
				m.textarea.CursorEnd()
				m.refitTextareaHeight()
				return m, nil
			}
		}
	}

	// Chuyển tiếp Nhập thông thường cho textarea
	if msg.Type == tea.KeyRunes && (containsSGRFragment(string(msg.Runes)) || isCSILeak(msg.Runes)) {
		return m, nil
	}
	var ok bool
	if msg, ok = cleanHumanKeyRunes(msg); !ok {
		return m, nil
	}
	state.resetSuggestionInput()
	if msg.Type == tea.KeyRunes {
		m.lastKeyAt = time.Now()
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	return m, cmd
}

// exitCoCreate thoát chế độ đồng sáng tạo, hủy request LLM đang chạy, khôi phục trạng thái input box.
func (m Model) exitCoCreate() (tea.Model, tea.Cmd) {
	if m.cocreate.cancel != nil {
		m.cocreate.cancel()
	}
	stage := m.cocreate.stage
	initial := m.cocreate.initialInput()
	m.cocreate = nil
	m.resizeTextarea()
	// Hủy giai đoạn đồng sáng tạo: xóa dấu chiếm dụng, giữ tạm dừng, quay về trạng thái Nhập của run console (không điền lại phần mở đầu đã tổng hợp).
	if stage {
		m.textarea.SetValue("")
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(cancelCoCreate(m.runtime), fetchSnapshot(m.runtime), m.textarea.Focus())
	}
	m.textarea.SetValue(initial)
	m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
	return m, m.textarea.Focus()
}

// overlayAboveInput xếp chồng nổi overlay ở đáy của view base (phía trên inputBox),
// không thay đổi chiều cao layout tổng thể. Chỉ phủ chiều rộng của chính thẻ overlay, bên phải lộ nội dung nền bên dưới.
func overlayAboveInput(base, overlay string, inputLineCount int) string {
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(strings.TrimRight(overlay, "\n"), "\n")

	endY := len(baseLines) - inputLineCount
	startY := endY - len(overLines)
	if startY < 0 {
		startY = 0
	}

	for i, ol := range overLines {
		y := startY + i
		if y >= 0 && y < endY {
			olW := lipgloss.Width(ol)
			// Cắt olW ký tự hiển thị ở bên trái baseline, ghép overlay + phần nội dung còn lại bên phải
			right := ansi.TruncateLeft(baseLines[y], olW, "")
			baseLines[y] = ol + right
		}
	}
	return strings.Join(baseLines, "\n")
}

// isCSILeak phát hiện KeyRunes có phải là mảnh sót bị rò rỉ của chuỗi escape CSI hay không.
// Khi terminal gửi phím mũi tên \x1b[A, nhấn phím nhanh có thể khiến chuỗi bị tách:
// \x1b được phân tích thành Escape, "[" hoặc "[A" rò rỉ vào textarea dưới dạng KeyRunes.
func isCSILeak(runes []rune) bool {
	if len(runes) == 0 || runes[0] != '[' {
		return false
	}
	for _, r := range runes[1:] {
		if (r >= '0' && r <= '9') || r == ';' ||
			(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
			continue
		}
		return false
	}
	return true
}

// containsSGRFragment phát hiện text có chứa mảnh sót của chuỗi chuột SGR hay không (mẫu "<số;số;").
func containsSGRFragment(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		j := i + 1
		if j >= len(s) || s[j] < '0' || s[j] > '9' {
			continue
		}
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == ';' {
			return true
		}
	}
	return false
}

func cleanHumanKeyRunes(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if msg.Type != tea.KeyRunes {
		return msg, true
	}
	cleaned := utils.CleanInputRunes(msg.Runes)
	if cleaned == "" {
		return msg, false
	}
	msg.Runes = []rune(cleaned)
	return msg, true
}
