package eval

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
)

// RunOptions điều khiển một lần chạy case.
type RunOptions struct {
	OutputDir string        // Thư mục output cách ly (bắt buộc)
	Timeout   time.Duration // Giới hạn thời gian thực cho một case; 0 nghĩa là không giới hạn
	Progress  io.Writer     // Đầu ra dòng tiến độ (tùy chọn, nil thì không in)
}

// RunCase điều khiển một case: lắp host → khởi động → tiến tới giới hạn số chương → Abort đúng lúc.
// bundle đã được bên gọi ghi đè variant (nếu có). error trả về chính là "lỗi thời gian chạy" (căn cứ hard fail);
// Viết xong bình thường hoặc dừng đúng cách đều trả về nil.
//
// RunCase độc quyền và đặt lại OutputDir: StartPrepared chỉ đặt lại progress/checkpoints, không xóa chapters/
// foundation và các tài sản khác, dùng lại thư mục cũ sẽ khiến sản phẩm sót làm nhiễm diag và novel_context. Vì vậy phải dọn trước khi chạy để bảo đảm cách ly.
func RunCase(cfg bootstrap.Config, bundle assets.Bundle, c Case, opts RunOptions) error {
	if strings.TrimSpace(opts.OutputDir) == "" {
		return fmt.Errorf("RunCase: thiếu OutputDir")
	}
	if err := os.RemoveAll(opts.OutputDir); err != nil {
		return fmt.Errorf("Dọn thư mục output: %w", err)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return fmt.Errorf("Tạo thư mục output: %w", err)
	}
	cfg.OutputDir = opts.OutputDir
	if c.Style != "" {
		cfg.Style = c.Style
	}

	eng, err := host.New(cfg, bundle, host.WithFileLog("headless.log", false))
	if err != nil {
		return fmt.Errorf("Lắp host: %w", err)
	}
	defer eng.Close()
	if logErr := eng.FileLogError(); logErr != nil {
		return fmt.Errorf("Nhật ký tệp đánh giá không khả dụng: %w", logErr)
	}

	prompt, err := startup.PrepareQuick(c.Prompt)
	if err != nil {
		return err
	}
	if err := eng.PrepareUserRules(prompt); err != nil {
		return fmt.Errorf("Chuẩn bị quy tắc người dùng: %w", err)
	}
	if err := eng.StartPrepared(prompt); err != nil {
		return fmt.Errorf("Khởi động: %w", err)
	}

	return drive(eng, c.MaxChapters, opts)
}

// driveEngine là interface engine tối thiểu mà drive tiêu thụ (*host.Host tự nhiên đáp ứng). Tách ra để
// viết kiểm thử xác định cho kỷ luật drain-to-Done — đoạn logic đồng thời này từng dính bẫy send-on-closed-channel.
type driveEngine interface {
	Events() <-chan host.Event
	Stream() <-chan string
	Done() <-chan struct{}
	Snapshot() host.UISnapshot
	Abort() bool
}

// drive tiêu thụ luồng sự kiện của engine, tới giới hạn số chương hoặc hết giờ thì Abort, rồi chờ Done kết thúc.
//
// Kỷ luật then chốt: dù hoàn thành bình thường, dừng do giới hạn chương hay quá giờ, đều phải drain tới Done rồi mới trả về. host phía sau waitDone
// sẽ gửi một lần vào done, còn eng.Close() (defer của RunCase) sẽ close(done) — trả về sớm sẽ khiến Close
// cạnh tranh với lần gửi của waitDone khi đóng kênh mà panic (send on closed channel). headless cũng dựa vào "Done trước
// rồi Close". Đồng thời phải xả hết Events và Stream để tránh làm nghẽn engine.
func drive(eng driveEngine, maxChapters int, opts RunOptions) error {
	var timeoutCh <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		timeoutCh = t.C
	}

	aborted, timedOut := false, false
	// finish được gọi sau khi drain tới Done (hoặc kênh đóng): nếu quá giờ thì trả error, nếu không thì kết thúc bình thường.
	finish := func() error {
		if timedOut {
			return fmt.Errorf("Chạy quá thời gian (%s)", opts.Timeout)
		}
		return nil
	}
	for {
		select {
		case ev, ok := <-eng.Events():
			if !ok {
				return finish()
			}
			if opts.Progress != nil && strings.TrimSpace(ev.Summary) != "" {
				fmt.Fprintf(opts.Progress, "    [%s] %s\n", ev.Category, ev.Summary)
			}
			if !aborted && capReached(eng.Snapshot(), maxChapters) {
				eng.Abort()
				aborted = true
				timeoutCh = nil // đã đạt điều kiện dừng, chuyển sang thu dọn bình thường, không còn chịu ràng buộc timeout (tránh nhầm dừng thành công là quá thời gian)
			}
		case <-eng.Stream():
			// Xả các mảnh streaming tăng dần, không tiêu thụ nội dung — eval không quan tâm luồng thân bài, chỉ nhìn sự thật đã ghi đĩa.
		case _, ok := <-eng.Done():
			if !ok {
				return finish()
			}
			return finish()
		case <-timeoutCh:
			eng.Abort() // ở đây aborted bắt buộc là false (cap dừng sẽ đặt timeoutCh thành nil)
			aborted, timedOut = true, true
			timeoutCh = nil // tắt bộ đếm thời gian, tiếp tục drain cho tới Done, rồi để finish trả lỗi quá thời gian
		}
	}
}

// capReached kiểm tra đã đạt điều kiện dừng hay chưa. maxChapters>0 thì tính theo số chương đã hoàn thành; <=0 thì xem là "loại lập kế hoạch",
// hễ hoàn tất lập kế hoạch (vào writing hoặc đã complete) thì dừng.
func capReached(snap host.UISnapshot, maxChapters int) bool {
	if maxChapters <= 0 {
		return snap.Phase == string(domain.PhaseWriting) || snap.Phase == string(domain.PhaseComplete)
	}
	return snap.CompletedCount >= maxChapters
}
