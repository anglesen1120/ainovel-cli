package eval

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/host"
)

// fakeEngine  host：Abort  waitDone  done 。done  1 ，
//
//	drive  drain  Done（len(done)==0 ）—— send-on-closed-channel
//
// panic 。
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
	select { //  waitDone：abort  done
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

//	Abort  drain  Done lỗi—— RunCase  Close
//
// waitDone  done  panic（Codex review #1）。
func TestDriveTimeoutDrainsToDone(t *testing.T) {
	f := newFakeEngine()
	err := drive(f, 1, RunOptions{Timeout: 30 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "") {
		t.Fatalf("cầnlỗi，nhận được %v", err)
	}
	if !f.wasAborted() {
		t.Fatal("cần Abort")
	}
	if len(f.done) != 0 {
		t.Fatal("drive  drain Done （ Close  panic）")
	}
}

// giới hạn số chương：Abort  drain  Done， nil（bình thường，）。
func TestDriveCapStopsAndDrains(t *testing.T) {
	f := newFakeEngine()
	f.mu.Lock()
	f.snap = host.UISnapshot{CompletedCount: 1}
	f.mu.Unlock()
	f.events <- host.Event{Category: "SYSTEM", Summary: "committed"} //  cap kiểm tra

	err := drive(f, 1, RunOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("bình thườngcần nil，nhận được %v", err)
	}
	if !f.wasAborted() {
		t.Fatal("giới hạn số chươngcần Abort")
	}
	if len(f.done) != 0 {
		t.Fatal("cần drain Done ")
	}
}

// Done（）：không cần Abort， nil。
func TestDriveNaturalDoneReturnsNil(t *testing.T) {
	f := newFakeEngine()
	f.done <- struct{}{}

	err := drive(f, 1, RunOptions{Timeout: time.Second})
	if err != nil {
		t.Fatalf("hoàn tất tự nhiêncần nil，nhận được %v", err)
	}
	if f.wasAborted() {
		t.Fatal("hoàn tất tự nhiêncần Abort")
	}
}
