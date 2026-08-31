package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host/imp"
)

// TestImportHistoryCoalescesRetryLines bảo vệ việc cập nhật trực tiếp dòng thử lại: các sự kiện liên tiếp cùng Key chỉ chiếm một dòng
// ("lần thứ N" nhảy trên cùng một dòng), sau khi bị một dòng tiến độ thường ngắt quãng thì bắt đầu dòng mới, giữ nguyên thứ tự thời gian.
func TestImportHistoryCoalescesRetryLines(t *testing.T) {
	s := newImportState(1, "book.txt", 100, 40, nil)
	base := len(s.history)
	retry := func(msg string) imp.Event {
		return imp.Event{Time: time.Now(), Stage: imp.StageSegmenting, Message: msg, Level: "warn", Key: "retry:segmenting"}
	}
	s.appendEvent(retry("1s sau thử lại (lần thứ 1)"), 80)
	s.appendEvent(retry("2s sau thử lại (lần thứ 2)"), 80)
	s.appendEvent(retry("4s sau thử lại (lần thứ 3)"), 80)
	if got := len(s.history) - base; got != 1 {
		t.Fatalf("Các lần thử lại liên tiếp cùng Key phải gộp thành 1 dòng, được %d", got)
	}
	if last := s.history[len(s.history)-1]; last.message != "4s sau thử lại (lần thứ 3)" {
		t.Fatalf("Dòng đã gộp phải cập nhật thành thông điệp mới nhất, được %q", last.message)
	}
	// Sau khi bị một dòng tiến độ thường ngắt quãng, lần thử lại mới sẽ bắt đầu dòng riêng.
	s.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAnalyzing, Message: "Phân tích các lô liên tiếp bắt đầu từ chương 1..."}, 80)
	s.appendEvent(retry("1s sau thử lại (lần thứ 1)"), 80)
	if got := len(s.history) - base; got != 3 {
		t.Fatalf("Sau khi bị ngắt quãng, lần thử lại phải bắt đầu dòng mới, tổng cộng 3 dòng, được %d", got)
	}
}

// TestRenderImportLineWrapsWithoutClipping bảo vệ lỗi hiển thị đầy đủ: nội dung theo phần còn lại sau khi trừ tiền tố
// ngắt dòng theo phần rộng còn lại, các dòng tiếp nối thẳng hàng, không dòng nào được vượt quá contentW——viewport cắt cứng các dòng quá rộng,
// HTTP trạng thái/provider/Mô hình trong lỗi chính là căn cứ tra nguyên nhân, cắt mất là coi như báo lỗi vô ích.
func TestRenderImportLineWrapsWithoutClipping(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageSegmenting,
		message: "Phạm vi cắt L1..L171",
		err: errors.New("imp: gọi model thất bại (tham số yêu cầu không hợp lệ，HTTP 400，openrouter，deepseek/deepseek-chat）：" +
			"Provider returned error: invalid request payload with a very long gateway message tail"),
	}
	const contentW = 80
	out := renderImportLine(ln, contentW, time.Now())
	// Việc ngắt dòng có thể rơi vào bất kỳ ký tự nào, so sánh sau khi bỏ khoảng trắng, chỉ xác nhận nội dung không mất một chữ nào.
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' {
				return -1
			}
			return r
		}, s)
	}
	for _, want := range []string{"HTTP 400", "openrouter", "gateway message tail"} {
		if !strings.Contains(norm(out), norm(want)) {
			t.Fatalf("Nội dung dòng thiếu %q：%q", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("Dòng %d có độ rộng %d vượt quá %d, sẽ bị viewport cắt bỏ：%q", i, w, contentW, line)
		}
	}
	// Thiết bị đầu cuối hẹp: tiền tố (dấu thời gian+biểu tượng+tên giai đoạn dài) có thể chiếm quá nửa độ rộng dòng, phần chính phải xuống dòng riêng thay vì cố ghép vượt quá giới hạn.
	ln.stage = imp.StageAwaitingConfirmation
	const narrowW = 40
	for i, line := range strings.Split(renderImportLine(ln, narrowW, time.Now()), "\n") {
		if w := lipgloss.Width(line); w > narrowW {
			t.Fatalf("Dòng %d trên terminal hẹp có độ rộng %d vượt quá %d：%q", i, w, narrowW, line)
		}
	}
}

// TestRenderImportLineMultilineBlock bảo vệ kiểu trình bày của khối thông điệp nhiều dòng (xem trước xác nhận cắt): các dòng tiếp nối toàn bộ
// thụt lề nhẹ (2 cột), không được canh theo độ rộng tiền tố——tiền tố 40+ cột sẽ ép toàn bộ danh sách chương sang nửa phải của bảng, nửa trái trống hoàn toàn.
func TestRenderImportLineMultilineBlock(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageAwaitingConfirmation,
		current: 157, total: 157,
		message: "Đã cắt 157 chương, vui lòng kiểm tra：\n  Chương 1 mở đầu\n  Chương 2 cố ý của tôi\n",
	}
	const contentW = 100
	out := strings.Split(renderImportLine(ln, contentW, time.Now()), "\n")
	if len(out) != 3 {
		t.Fatalf("Phải là dòng tiền tố + 2 dòng nội dung, được %d dòng：%q", len(out), out)
	}
	for i, line := range out[1:] {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("Dòng %d vượt độ rộng %d：%q", i+1, w, line)
		}
		if strings.HasPrefix(line, strings.Repeat(" ", 20)) {
			t.Fatalf("Các dòng tiếp nối của khối nhiều dòng không được canh theo độ rộng tiền tố：%q", line)
		}
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("Các dòng tiếp nối của khối nhiều dòng phải thụt lề nhẹ 2 cột：%q", line)
		}
	}
}

// TestWrapTextResetsAtNewlines bảo vệ việc ngắt dòng cho thông điệp nhiều dòng: tại '\n' phải đặt lại bộ đếm độ rộng dòng, nếu không chỉ cần
// một dòng bất kỳ kích hoạt ngắt dòng thì mọi dòng sau đó đều bị nhận sai là quá rộng và chèn ngắt dòng giả + thụt lề, làm vỡ toàn bộ phần xem trước xác nhận.
func TestWrapTextResetsAtNewlines(t *testing.T) {
	in := strings.Repeat("rộng", 30) + "\ndòng ngắn một\ndòng ngắn hai"
	out := wrapText(in, 20)
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 20 {
			t.Fatalf("Dòng %d có độ rộng %d vượt quá 20：%q", i, w, l)
		}
	}
	if !strings.Contains(out, "\ndòng ngắn một\ndòng ngắn hai") {
		t.Fatalf("Các dòng ngắn sẵn có không được bị làm vỡ：%q", out)
	}
}

// TestImportEscResumeGate bảo vệ điểm rơi của Esc ở bảng nhập: sau khi kết thúc thành công một lần nhập khởi phát từ trang chào mừng,
// đóng bảng phải chạy bù thêm một lần phục hồi (Resume của bootstrap chỉ chạy lúc khởi động), nếu không người dùng sẽ bị kẹt ở trang chào mừng không có
// lối vào tiếp tục viết; trạng thái kết thúc do lỗi và ngữ cảnh workspace chỉ đóng bảng; khi đang chạy, Esc vẫn là hủy chứ không phải tắt.
func TestImportEscResumeGate(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	// tea.Batch sau khi thực thi sẽ trả về BatchMsg (các lệnh con không được thực thi), nhờ đó phân biệt "tập trung+phục hồi" với chỉ tập trung.
	isBatch := func(cmd tea.Cmd) bool {
		_, ok := cmd().(tea.BatchMsg)
		return ok
	}
	newM := func(mode appMode, st *importState) Model {
		return Model{mode: mode, importer: st, textarea: textarea.New()}
	}

	m := newM(modeNew, &importState{done: true, stage: imp.StageDone})
	next, cmd := m.handleImportKey(esc)
	if next.(Model).importer != nil {
		t.Fatal("Esc ở trạng thái cuối phải đóng bảng")
	}
	if !isBatch(cmd) {
		t.Fatal("Đóng bảng sau khi nhập thành công từ trang chào mừng phải kèm lệnh phục hồi")
	}

	m = newM(modeNew, &importState{done: true, stage: imp.StageError, err: errors.New("boom")})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("Trạng thái cuối do lỗi không nên kích hoạt phục hồi (sách có thể vốn chưa nhập thành công)")
	}

	m = newM(modeRunning, &importState{done: true, stage: imp.StageDone})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("Workspace có cơ chế kiểm soát riêng, không nên kích hoạt phục hồi lặp lại")
	}

	canceled := false
	m = newM(modeNew, &importState{cancel: func() { canceled = true }})
	next, _ = m.handleImportKey(esc)
	if !canceled || next.(Model).importer == nil {
		t.Fatal("Khi đang chạy, Esc phải hủy nhập và giữ bảng cho đến khi runner kết thúc")
	}
}

// TestRetryCountdown bảo vệ hợp đồng hiển thị đếm ngược (dùng chung cho bảng sự kiện và bảng nhập):
// chưa đặt hạn chót hoặc đã đến điểm thì trả về rỗng (yêu cầu đã đang trên đường); thời gian còn lại làm tròn lên tới giây, giảm từng giây và không xuất hiện 0s.
func TestRetryCountdown(t *testing.T) {
	now := time.Now()
	if got := retryCountdown(time.Time{}, now); got != "" {
		t.Fatalf("Hạn chót giá trị 0 phải trả về rỗng, được %q", got)
	}
	if got := retryCountdown(now.Add(-time.Second), now); got != "" {
		t.Fatalf("Đã đến điểm phải trả về rỗng, được %q", got)
	}
	if got := retryCountdown(now.Add(7500*time.Millisecond), now); got != "8s nữa rồi thử lại" {
		t.Fatalf("7.5s phải làm tròn lên thành 8s, được %q", got)
	}
	if got := retryCountdown(now.Add(300*time.Millisecond), now); got != "1s nữa rồi thử lại" {
		t.Fatalf("Không đủ 1s phải hiển thị 1s, được %q", got)
	}
}

// TestParseImportArgsGuide bảo vệ phân tích --guide: hướng dẫn bằng ngôn ngữ tự nhiên có thể chứa khoảng trắng (các token phía sau đều được nhập chung),
// Có thể kết hợp với các tùy chọn khác (đặt ở cuối), nội dung rỗng sẽ báo lỗi.
func TestParseImportArgsGuide(t *testing.T) {
	opts, err := parseImportArgs([]string{"--guide=Màn chuyển·X", "cũng là", "Chương độc lập"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Guidance != "Màn chuyển·X cũng là Chương độc lập" {
		t.Fatalf("guidance có khoảng trắng phải được gộp nguyên khối, nhận được %q", opts.Guidance)
	}
	opts, err = parseImportArgs([]string{"book.txt", "--yes", "--guide=Gộp phần mở đầu vào chương một"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AutoConfirm || opts.SourcePath != "book.txt" || opts.Guidance != "Gộp phần mở đầu vào chương một" {
		t.Fatalf("phân tích khi kết hợp với các tùy chọn khác không khớp: %+v", opts)
	}
	if _, err := parseImportArgs([]string{"--guide="}); err == nil {
		t.Fatal("--guide rỗng phải báo lỗi")
	}
}
