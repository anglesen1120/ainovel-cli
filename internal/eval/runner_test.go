package eval

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/host"
)

// fakeEngine mô phỏng host: sau Abort sẽ gửi một lần vào done như waitDone. done có buffer 1,
// kiểm thử dựa vào đó để khẳng định drive đã drain Done chưa (len(done)==0 tức là đã tiêu thụ) — đây là bất biến chống send-on-closed-channel
// panic là bất biến then chốt.
type fakeEngine struct {
	events chan host.Event
	stream chan string
	done   chan struct{}

	mu      sync.Mutex
	snap    host.UISnapshot
	aborted bool
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		events: make(chan host.Event, 4),
		stream: make(chan string),
		done:   make(chan struct{}, 1),
	}
}

func (f *fakeEngine) Events() <-chan host.Event { return f.events }
func (f *fakeEngine) Stream() <-chan string     { return f.stream }
func (f *fakeEngine) Done() <-chan struct{}     { return f.done }

func (f *fakeEngine) Snapshot() host.UISnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeEngine) Abort() bool {
	f.mu.Lock()
	f.aborted = true
	f.mu.Unlock()
	select { // Mô phỏng waitDone: sau khi abort sẽ gửi một lần vào done
	case f.done <- struct{}{}:
	default:
	}
	return true
}

func (f *fakeEngine) wasAborted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.aborted
}

// Nhánh quá giờ phải Abort rồi drain tới Done mới trả lỗi quá thời gian — nếu không Close của RunCase sẽ
// cạnh tranh đóng kênh done với waitDone rồi panic (Codex review #1).
func TestDriveTimeoutDrainsToDone(t *testing.T) {
	f := newFakeEngine()
	err := drive(f, 1, RunOptions{Timeout: 30 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "quá thời gian") {
		t.Fatalf("Quá thời gian phải trả về lỗi quá thời gian, nhận %v", err)
	}
	if !f.wasAborted() {
		t.Fatal("Quá thời gian phải kích hoạt Abort")
	}
	if len(f.done) != 0 {
		t.Fatal("drive phải drain Done rồi mới được trả về (nếu không sẽ cạnh tranh đóng kênh với Close và panic)")
	}
}

// Đạt giới hạn số chương: Abort rồi drain tới Done, trả nil (dừng bình thường, không phải quá thời gian).
func TestDriveCapStopsAndDrains(t *testing.T) {
	f := newFakeEngine()
	f.mu.Lock()
	f.snap = host.UISnapshot{CompletedCount: 1}
	f.mu.Unlock()
	f.events <- host.Event{Category: "SYSTEM", Summary: "committed"} // kích hoạt kiểm tra cap

	err := drive(f, 1, RunOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("Dừng bình thường phải trả nil, nhận %v", err)
	}
	if !f.wasAborted() {
		t.Fatal("Đạt giới hạn số chương phải Abort")
	}
	if len(f.done) != 0 {
		t.Fatal("Phải drain Done rồi mới trả về")
	}
}

// Engine tự Done (viết xong): không cần Abort, trả nil.
func TestDriveNaturalDoneReturnsNil(t *testing.T) {
	f := newFakeEngine()
	f.done <- struct{}{}

	err := drive(f, 1, RunOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("Hoàn thành tự nhiên phải trả nil, nhận %v", err)
	}
	if f.wasAborted() {
		t.Fatal("Hoàn thành tự nhiên không được Abort")
	}
}
