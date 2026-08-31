package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/imp"
)

// importState là trạng thái modal trong khi lệnh /import chạy.
//
// Modal được tạo khi bắt đầu nhập, tiến theo luồng sự kiện; sau khi hoàn tất hoặc gặp lỗi thì giữ lại trên màn hình để chờ người dùng nhấn Esc đóng.
// Esc khi đang chạy sẽ kích hoạt hủy (ctx.Cancel), để runner dọn dẹp tại điểm sự kiện kế tiếp.
type importState struct {
	reqID      int
	source     string
	stage      imp.Stage
	current    int
	total      int
	startedAt  time.Time
	finishedAt time.Time
	history    []importLine
	totalLines int // Tổng số dòng nhật ký lũy kế (vẫn tiếp tục đếm sau khi history đạt importHistoryMax)
	err        error
	done       bool // Trạng thái kết thúc (hoàn tất/gặp lỗi)
	paused     bool // Pipeline dừng tại awaiting, kênh sự kiện đã đóng: panel có thể đóng, chưa phải trạng thái kết thúc
	frame      int  // Khung đồng bộ của hoạt ảnh chính: dấu sao ở cuối và đếm ngược dựa vào nó để tính lại theo từng tick
	cancel     context.CancelFunc
	viewport   viewport.Model
}

type importLine struct {
	at      time.Time
	stage   imp.Stage
	current int
	total   int
	message string
	level   string    // "warn" cảnh báo thử lại/backoff
	key     string    // Khi không rỗng, các dòng liên tiếp cùng key được cập nhật tại chỗ (khớp cơ chế ID của panel sự kiện)
	retryAt time.Time // Khác zero = thời điểm hạn chót cho lần thử lại kế tiếp, khi render sẽ tính số giây còn lại để tạo đếm ngược
	err     error

	rendered  string // Kết quả render được cache theo renderedW; lịch sử có thể lên tới hàng nghìn dòng, sắp xếp lại toàn bộ mỗi tick sẽ làm panel đơ
	renderedW int
}

// importHistoryMax là giới hạn số dòng nhật ký giữ trong bộ nhớ của panel: sách cỡ nghìn chương echo từng chương + publish từng chương sẽ
// tăng không giới hạn, vừa tốn bộ nhớ vừa làm chậm render lại. Tệp nhật ký (logs/import.log) luôn giữ bản ghi đầy đủ.
const importHistoryMax = 1000

func newImportState(reqID int, source string, width, height int, cancel context.CancelFunc) *importState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	vp := viewport.New(contentW, boxH-4)
	s := &importState{
		reqID:     reqID,
		source:    source,
		startedAt: time.Now(),
		stage:     imp.StageIngesting,
		cancel:    cancel,
		viewport:  vp,
	}
	s.refresh(contentW)
	return s
}

func (s *importState) appendEvent(ev imp.Event, contentW int) {
	s.stage = ev.Stage
	s.current = ev.Current
	s.total = ev.Total
	if ev.Err != nil {
		s.err = ev.Err
	}
	line := importLine{
		at: ev.Time, stage: ev.Stage, current: ev.Current, total: ev.Total,
		message: ev.Message, level: ev.Level, key: ev.Key, retryAt: ev.RetryAt, err: ev.Err,
	}
	// Cùng Key và liền kề → cập nhật tại chỗ (7 lần backoff nhảy trên một dòng); nếu bị dòng tiến độ khác chen ngang thì mở dòng mới, giữ thứ tự thời gian.
	if ev.Key != "" && len(s.history) > 0 && s.history[len(s.history)-1].key == ev.Key {
		s.history[len(s.history)-1] = line
	} else {
		s.totalLines++
		s.history = append(s.history, line)
		if len(s.history) > importHistoryMax {
			s.history = append(s.history[:0], s.history[len(s.history)-importHistoryMax:]...)
		}
	}
	if ev.Stage == imp.StageDone || ev.Stage == imp.StageError {
		s.done = true
		s.finishedAt = ev.Time
	}
	s.refresh(contentW)
}

func (s *importState) refresh(contentW int) {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Nhập tiểu thuyết bên ngoài"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Tệp nguồn "))
	b.WriteString(s.source)
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Bắt đầu "))
	b.WriteString(formatReportTime(s.startedAt))
	if !s.finishedAt.IsZero() {
		b.WriteString(dimStyle.Render("  Hoàn tất "))
		b.WriteString(formatReportTime(s.finishedAt))
	}
	b.WriteString("\n\n")

	// Dòng giai đoạn hiện tại
	b.WriteString(mutedStyle.Render("Giai đoạn "))
	b.WriteString(stageStyle.Render(string(s.stage)))
	if s.total > 0 {
		b.WriteString(mutedStyle.Render("  Tiến độ "))
		if s.current > 0 {
			b.WriteString(fmt.Sprintf("%d/%d", s.current, s.total))
		} else {
			b.WriteString(fmt.Sprintf("0/%d", s.total))
		}
	}
	b.WriteString("\n\n")

	// Nhật ký lịch sử. Mỗi dòng có một cột biểu tượng ngữ nghĩa (căn theo dạng panel sự kiện):
	// ✗ đỏ=thất bại · ↻ cam=backoff thử lại/hỏi lại kiểm tra (cùng key nhảy tại chỗ) · ✓ xanh=hoàn tất · · xám=tiến độ thường.
	b.WriteString(titleStyle.Render("Nhật ký quy trình"))
	b.WriteString(" ")
	if s.totalLines > len(s.history) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d dòng, chỉ hiển thị %d dòng gần nhất, xem đầy đủ tại logs/import.log)", s.totalLines, len(s.history))))
	} else {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d dòng)", s.totalLines)))
	}
	b.WriteString("\n")
	now := time.Now()
	for i := range s.history {
		ln := &s.history[i]
		// Các dòng đã chốt được cache kết quả render theo chiều rộng: refresh chạy ở mỗi tick hoạt ảnh, lịch sử cỡ nghìn dòng mà render lại toàn bộ
		// (wrapText+tô màu từng dòng) là chi phí bậc hai, đến giai đoạn publish sẽ giật thấy rõ.
		// Chỉ những dòng có đếm ngược còn hoạt động mới cần tính lại từng tick (sau khi đến hạn vẫn tính thêm 2s để xóa badge).
		live := !ln.retryAt.IsZero() && now.Before(ln.retryAt.Add(2*time.Second))
		if ln.rendered == "" || ln.renderedW != contentW || live {
			ln.rendered = renderImportLine(*ln, contentW, now)
			ln.renderedW = contentW
		}
		b.WriteString("\n")
		b.WriteString(ln.rendered)
	}

	running := !s.done && !s.paused
	if running {
		// Con trỏ ở cuối: một ngôi sao đơn cùng kiểu với panel streaming đi theo bên dưới dòng nhật ký cuối cùng, nhảy theo từng frame của hoạt ảnh chính,
		// hô ứng với dòng chỉ báo "đang tiến hành" ở phía trên——có nó ở cuối nhật ký, trong thời gian chờ backoff cũng thấy ngay pipeline vẫn còn sống.
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[s.frame%len(streamCursorFrames)]))
	}

	// Gợi ý kết thúc
	b.WriteString("\n\n")
	switch {
	case s.err != nil:
		b.WriteString(errStyle.Render("Nhập thất bại"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc đóng panel"))
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		b.WriteString(okStyle.Render("Chia đoạn hoàn tất, chờ bạn kiểm tra"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("y xác nhận chia đoạn và tiếp tục; nếu cần điều chỉnh chia đoạn có thể nhấn Esc rồi dùng /import --guide=<mô tả bằng ngôn ngữ tự nhiên>; Esc đóng panel"))
	case s.paused:
		// Pipeline dừng tại chỗ chờ phân xử, kênh đã đóng: thao tác theo gợi ý trong panel rồi nhấn Esc để đóng.
		b.WriteString(okStyle.Render("Nhập đã tạm dừng, đang chờ thao tác của bạn"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Làm theo gợi ý phía trên để tiếp tục (ví dụ /import --story=open|closed); Esc đóng panel"))
	case s.done:
		b.WriteString(okStyle.Render("Nhập hoàn tất, Foundation và Chương đã sẵn sàng"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc đóng panel và nối lại cổng kiểm soát viết tiếp (engine dừng ở ranh giới chương kế tiếp, chờ bạn nghiệm thu rồi cho phép tiếp tục)"))
	default:
		b.WriteString(dimStyle.Render("Esc hủy nhập"))
	}

	// Chỉ bám đuôi khi người dùng đang ở đáy: refresh hiện chạy mỗi tick (hoạt ảnh/đếm ngược),
	// GotoBottom vô điều kiện sẽ kéo người dùng đang cuộn lên xem trong lúc chạy về đáy mỗi 350ms.
	atBottom := s.viewport.AtBottom()
	s.viewport.SetContent(b.String())
	if running && atBottom {
		s.viewport.GotoBottom()
	}
}

// renderImportLine render một dòng nhật ký quy trình: timestamp + cột biểu tượng ngữ nghĩa + giai đoạn (+tiến độ) + nội dung.
// Nội dung được wrap theo chiều rộng còn lại sau khi trừ tiền tố, dòng tiếp theo căn theo điểm bắt đầu nội dung; quá rộng chỉ wrap chứ tuyệt đối không cắt——
// viewport cắt cứng các dòng quá rộng; HTTP status/provider/model trong lỗi chính là căn cứ để điều tra, cắt mất thì chẳng khác nào báo lỗi vô ích.
func renderImportLine(ln importLine, contentW int, now time.Time) string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	warnStyle := lipgloss.NewStyle().Foreground(colorReview)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var p strings.Builder
	p.WriteString(dimStyle.Render(ln.at.Format("15:04:05")))
	p.WriteString(" ")
	switch {
	case ln.err != nil:
		p.WriteString(errStyle.Bold(true).Render("✗"))
	case ln.level == "warn":
		p.WriteString(warnStyle.Bold(true).Render("↻"))
	case ln.stage == imp.StageDone:
		p.WriteString(okStyle.Bold(true).Render("✓"))
	default:
		p.WriteString(dimStyle.Render("·"))
	}
	p.WriteString(" ")
	p.WriteString(stageStyle.Render(string(ln.stage)))
	if ln.total > 0 && ln.current > 0 {
		p.WriteString(mutedStyle.Render(fmt.Sprintf(" %d/%d", ln.current, ln.total)))
	}
	p.WriteString(" ")
	prefix := p.String()

	var text string
	style := lipgloss.NewStyle()
	switch {
	case ln.err != nil:
		text = ln.message + " — " + ln.err.Error()
		style = errStyle
	case ln.level == "warn":
		text = ln.message
		if cd := retryCountdown(ln.retryAt, now); cd != "" {
			text += " · " + cd
		}
		style = warnStyle
	default:
		text = ln.message
	}
	// Sau khi tô màu từng dòng thì tự ghép lại: lipgloss với chuỗi nhiều dòng sẽ đệm từng dòng tới dòng rộng nhất trong block,
	// tiền tố chỉ có ở dòng đầu, render cả block sẽ khiến dòng đầu vượt quá contentW và bị viewport cắt.
	prefixW := lipgloss.Width(prefix)
	wrapW := contentW - prefixW
	if wrapW < 20 {
		// Trong terminal hẹp, tiền tố (timestamp+biểu tượng+tên giai đoạn dài+tiến độ) đã chiếm phần lớn chiều rộng dòng: đưa nội dung sang dòng mới với thụt lề nhẹ,
		// Độ rộng xuống dòng luôn bị contentW ràng buộc——gò cứng theo mức sàn 20 cột sẽ làm dòng đầu vượt quá bị viewport cắt mất,
		// đúng lúc cắt mất phần đuôi sai của HTTP status/provider và các căn cứ để dò lỗi.
		var out strings.Builder
		out.WriteString(prefix)
		for _, l := range strings.Split(wrapText(text, max(10, contentW-4)), "\n") {
			out.WriteString("\n    ")
			out.WriteString(style.Render(l))
		}
		return out.String()
	}
	// Thông điệp khối nhiều dòng (như xem trước xác nhận cắt tách): dòng đầu theo sau tiền tố, các dòng còn lại lùi vào nhẹ toàn bộ——nếu theo độ rộng tiền tố
	// để canh dòng tiếp, tiền tố 40+ cột sẽ dồn cả khối sang nửa phải của panel, nửa trái trống hoàn toàn.
	head, body := text, ""
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		head, body = text[:i], strings.TrimRight(text[i+1:], "\n")
	}
	lines := strings.Split(wrapText(head, wrapW), "\n")
	var out strings.Builder
	out.WriteString(prefix)
	out.WriteString(style.Render(lines[0]))
	pad := strings.Repeat(" ", prefixW)
	for _, l := range lines[1:] {
		out.WriteString("\n")
		out.WriteString(pad)
		out.WriteString(style.Render(l))
	}
	if body != "" {
		for _, l := range strings.Split(wrapText(body, contentW-2), "\n") {
			out.WriteString("\n  ")
			out.WriteString(style.Render(l))
		}
	}
	return out.String()
}

func renderImportModal(width, height int, s *importState, frame int) string {
	if s == nil {
		return ""
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	running := !s.done && !s.paused
	if s.viewport.Width != contentW {
		s.viewport.Width = contentW
		s.refresh(contentW)
	}
	vpH := boxH - 4
	if running {
		vpH -= 2 // dòng chỉ báo hoạt động ở trên + dòng trống
	}
	if s.viewport.Height != vpH {
		s.viewport.Height = vpH
	}

	hint := "  ↑↓ cuộn · Esc hủy/Tắt"
	switch {
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		hint = "  ↑↓ cuộn · y xác nhận cắt tách · Esc Tắt"
	case running:
		hint = "  ↑↓ cuộn · Esc hủy"
	}

	body := strings.Split(s.viewport.View(), "\n")
	if running {
		// Chỉ báo hoạt động khi đang chạy: ngôi sao cùng kiểu với panel streaming một quả + thời gian đã dùng, cập nhật theo animation chính ở tần suất thấp.
		// Gắn ở dòng cố định ngoài viewport——nội dung viewport chỉ cập nhật theo sự kiện, nhét animation vào trong đó sẽ không chạy;
		// thiếu nó, trong lúc gọi Mô hình dài/ thử lại theo backoff, panel sẽ đứng im như tượng, người dùng dễ tưởng bị treo.
		star := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[frame%len(streamCursorFrames)])
		status := lipgloss.NewStyle().Foreground(colorMuted).
			Render(fmt.Sprintf(" Đang chạy · đã dùng %s", formatElapsed(time.Since(s.startedAt))))
		body = append([]string{star + status, ""}, body...)
	}
	modal := renderPaddedModalFrame(boxW, boxH, "Nhập tiểu thuyết bên ngoài", hint, body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// formatElapsed hiển thị thời gian đã dùng mm:ss (quá 1 giờ thì chuyển sang h:mm:ss).
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

func (m Model) handleImportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.importer == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		// Vẫn đang chạy (chưa ở trạng thái cuối, chưa tạm dừng) → Esc hủy, giao runner thu dọn; đã ở trạng thái cuối hoặc đã dừng ở awaiting
		// (kênh đóng) → Esc đóng panel. Thiếu nhánh paused sẽ làm panel không thể đóng sau khi awaiting dừng (kẹt chết).
		if !m.importer.done && !m.importer.paused && m.importer.cancel != nil {
			m.importer.cancel()
			return m, nil
		}
		succeeded := m.importer.stage == imp.StageDone && m.importer.err == nil
		m.importer = nil
		// Khâu kết thúc thành công của import khởi phát từ trang chào mừng: trang chào mừng không có lối vào để viết tiếp (Resume của bootstrap chỉ ở
		// lúc khởi động chạy một lần), khi đóng panel thì chạy bù khôi phục, để người dùng rơi vào cổng Hold hoàn tất import của workspace,
		// thay vì ở lại trang chào mừng nơi lỡ nhấn Enter là "mở sách mới".
		if succeeded && m.mode == modeNew {
			return m, tea.Batch(m.textarea.Focus(), resumeBook(m.runtime))
		}
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.importer.viewport.ScrollUp(1)
	case tea.KeyDown:
		m.importer.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		m.importer.viewport.HalfPageUp()
	case tea.KeyPgDown:
		m.importer.viewport.HalfPageDown()
	case tea.KeyRunes:
		// Tại chỗ tạm dừng xác nhận cắt tách, nhấn y = chạy lại /import --yes ngay tại chỗ (khôi phục không cần đường dẫn), cho qua lần này với cắt tách hiện tại.
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y') &&
			m.importer.paused && m.importer.stage == imp.StageAwaitingConfirmation {
			return m.confirmImportSegmentation()
		}
	}
	return m, nil
}

// confirmImportSegmentation nén "đã xem trước rồi cho qua" thành một phím: chạy lại import ngay tại chỗ và kèm theo
// AcceptSegmentation (khôi phục là không trạng thái, pipeline tiếp tục từ chỗ thiếu confirmation). Nó khác --yes ở
// chỗ là "phân xử rõ ràng sau khi đã xem trước"——cắt tách có Notes giải thích chịu lỗi thì --yes không cho qua, y cho qua;
// chỉ có hiệu lực theo Options lần này, không ghi intent.json, sau đó các cắt tách mới được cắt lại bằng --guide vẫn sẽ dừng để đối chiếu.
// Dùng lại tên file nguồn và nhật ký quy trình của panel cũ, để xem trước chương vẫn có thể quay lui xem lại khi tiếp tục phân tích.
func (m Model) confirmImportSegmentation() (tea.Model, tea.Cmd) {
	prev := m.importer
	m.importSeq++
	state, listenCmd, err := startImportRun(m.runtime, m.importSeq, imp.Options{AcceptSegmentation: true}, m.width, m.height)
	if err != nil {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: "xác nhận cắt tách thất bại: " + err.Error(), Level: "error",
		})
		return m, nil
	}
	state.source = prev.source
	state.history = append([]importLine(nil), prev.history...)
	state.totalLines = prev.totalLines
	boxW, _ := reportModalSize(m.width, m.height)
	state.refresh(paddedModalContentWidth(boxW))
	m.importer = state
	return m, listenCmd
}

// importEventMsg: một lần phân phát imp.Event.
type importEventMsg struct {
	reqID int
	ev    imp.Event
	ch    <-chan imp.Event // tiếp tục lắng nghe mục tiếp theo trên cùng kênh
}

// importClosedMsg là tín hiệu kênh sự kiện đã đóng (goroutine import dừng). Dù dừng ở trạng thái cuối hay ở awaiting,
// tín hiệu đóng kênh đều dựa vào nó để báo tin cậy rằng panel có thể đóng, tránh chỉ nhận trạng thái cuối khiến panel kẹt sau khi awaiting dừng.
type importClosedMsg struct {
	reqID int
}

// startImport khởi động một lần nhập tiểu thuyết bên ngoài: phân tích tham số → tạo modal state → lắng nghe Luồng sự kiện.
func startImport(rt *host.Host, reqID int, args []string, width, height int) (*importState, tea.Cmd, error) {
	opts, err := parseImportArgs(args)
	if err != nil {
		return nil, nil, err
	}
	return startImportRun(rt, reqID, opts, width, height)
}

// startImportRun khởi động import bằng Options đã định (các lần tái nhập nội bộ như y xác nhận không qua phân tích tham số).
// width/height dùng để khởi tạo viewport; hàm cancel được gắn lên state để Esc hủy.
func startImportRun(rt *host.Host, reqID int, opts imp.Options, width, height int) (*importState, tea.Cmd, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.ImportFrom(ctx, opts)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newImportState(reqID, opts.SourcePath, width, height, cancel)
	return state, listenImportEvent(reqID, ch), nil
}

func listenImportEvent(reqID int, ch <-chan imp.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return importClosedMsg{reqID: reqID}
		}
		return importEventMsg{reqID: reqID, ev: ev, ch: ch}
	}
}

// parseImportArgs phân tích `/import <path> [--yes] [--story=open|closed] [--continue] [--guide=<mô tả>]`.
// Không có tham số thì xem là “khôi phục từ workspace đang hoạt động”, đường dẫn nguồn không phải mục bắt buộc để khôi phục (RFC §18).
// --guide là hướng dẫn cắt tách bằng ngôn ngữ tự nhiên, có thể chứa khoảng trắng: từ sau --guide= trở đi toàn bộ nội dung được nhập vào văn bản hướng dẫn, phải đặt ở cuối.
func parseImportArgs(args []string) (imp.Options, error) {
	var opts imp.Options
	for i := range args {
		a := args[i]
		switch {
		case a == "--yes":
			opts.AutoConfirm = true
		case a == "--continue":
			opts.ContinueAfter = true
		case strings.HasPrefix(a, "--story="):
			v := strings.TrimPrefix(a, "--story=")
			if v != "open" && v != "closed" {
				return imp.Options{}, fmt.Errorf("--story chỉ có thể là open hoặc closed：%q", v)
			}
			opts.StoryResolution = v
		case strings.HasPrefix(a, "--guide="):
			parts := append([]string{strings.TrimPrefix(a, "--guide=")}, args[i+1:]...)
			g := strings.TrimSpace(strings.Join(parts, " "))
			if g == "" {
				return imp.Options{}, fmt.Errorf("--guide cần hướng dẫn cắt tách bằng ngôn ngữ tự nhiên, ví dụ --guide=Interlude·X cũng là một chương độc lập")
			}
			opts.Guidance = g
			return opts, nil
		case strings.HasPrefix(a, "--"):
			return imp.Options{}, fmt.Errorf("tùy chọn không xác định %q（hỗ trợ：--yes / --story=open|closed / --continue / --guide=<hướng dẫn cắt tách>）", a)
		default:
			if opts.SourcePath != "" {
				return imp.Options{}, fmt.Errorf("chỉ chấp nhận một đường dẫn tệp nguồn：thừa %q", a)
			}
			opts.SourcePath = a
		}
	}
	return opts, nil
}
