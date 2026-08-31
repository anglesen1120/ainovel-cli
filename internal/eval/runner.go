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

// RunOptions  case 。
type RunOptions struct {
	OutputDir string        // đầu ra（）
	Timeout   time.Duration //  case ；0
	Progress  io.Writer     // đầu ra（，nil ）
}

// RunCase điều khiển case：lắp ráp host → khởi động → giới hạn số chương →  Abort。
// bundle  variant （）。 error "lỗi"（hard fail ）；
// bình thườngbình thường nil。
//
// RunCase  OutputDir：StartPrepared  progress/checkpoints， chapters/
// foundation ， diag  novel_context。，。
func RunCase(cfg bootstrap.Config, bundle assets.Bundle, c Case, opts RunOptions) error {
	if strings.TrimSpace(opts.OutputDir) == "" {
		return fmt.Errorf("RunCase: thiếu OutputDir")
	}
	if err := os.RemoveAll(opts.OutputDir); err != nil {
		return fmt.Errorf("đầu ra: %w", err)
	}
	if err := os.MkdirAll(opts.OutputDir, 0o755); err != nil {
		return fmt.Errorf("đầu ra: %w", err)
	}
	cfg.OutputDir = opts.OutputDir
	if c.Style != "" {
		cfg.Style = c.Style
	}

	eng, err := host.New(cfg, bundle, host.WithFileLog("headless.log", false))
	if err != nil {
		return fmt.Errorf("lắp ráp host: %w", err)
	}
	defer eng.Close()
	if logErr := eng.FileLogError(); logErr != nil {
		return fmt.Errorf(": %w", logErr)
	}

	prompt, err := startup.PrepareQuick(c.Prompt)
	if err != nil {
		return err
	}
	if err := eng.PrepareUserRules(prompt); err != nil {
		return fmt.Errorf("chuẩn bị quy tắc người dùng: %w", err)
	}
	if err := eng.StartPrepared(prompt); err != nil {
		return fmt.Errorf("khởi động: %w", err)
	}

	return drive(eng, c.MaxChapters, opts)
}

// driveEngine  drive （*host.Host ）。
// drain-to-Done —— send-on-closed-channel 。
type driveEngine interface {
	Events() <-chan host.Event
	Stream() <-chan string
	Done() <-chan struct{}
	Snapshot() host.UISnapshot
	Abort() bool
}

// drive ，giới hạn số chương Abort， Done 。
//
// ：bình thường、， drain  Done 。host  waitDone
//
//	done ， eng.Close()（RunCase  defer） close(done)—— Close
//	waitDone  panic（send on closed channel）。headless " Done
//	Close"。 Events  Stream，。
func drive(eng driveEngine, maxChapters int, opts RunOptions) error {
	var timeoutCh <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		timeoutCh = t.C
	}

	aborted, timedOut := false, false
	// finish  drain  Done（）： error，bình thường。
	finish := func() error {
		if timedOut {
			return fmt.Errorf("runtime quá giờ（%s）", opts.Timeout)
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
				timeoutCh = nil // ，bình thường，（）
			}
		case <-eng.Stream():
			// xả phần gia tăng của luồng，nội dung——eval văn bản truyện，。
		case _, ok := <-eng.Done():
			if !ok {
				return finish()
			}
			return finish()
		case <-timeoutCh:
			eng.Abort() //  aborted  false（cap  timeoutCh  nil）
			aborted, timedOut = true, true
			timeoutCh = nil // ， drain  Done， finish lỗi
		}
	}
}

// capReached 。maxChapters>0 ；<=0 ""，
// hoàn tất lập kế hoạch（ writing  complete）。
func capReached(snap host.UISnapshot, maxChapters int) bool {
	if maxChapters <= 0 {
		return snap.Phase == string(domain.PhaseWriting) || snap.Phase == string(domain.PhaseComplete)
	}
	return snap.CompletedCount >= maxChapters
}
