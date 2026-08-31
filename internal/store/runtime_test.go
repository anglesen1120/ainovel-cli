package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestRuntimeStoreAppendQueueAssignsSeq(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	first, err := store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Priority: domain.RuntimePriorityBackground,
		Summary:  "first",
	})
	if err != nil {
		t.Fatalf("AppendQueue lần đầu: %v", err)
	}
	second, err := store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Priority: domain.RuntimePriorityControl,
		Summary:  "second",
	})
	if err != nil {
		t.Fatalf("AppendQueue lần hai: %v", err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("giá trị seq không mong đợi: %d %d", first.Seq, second.Seq)
	}

	items, err := store.Runtime.LoadQueue()
	if err != nil {
		t.Fatalf("LoadQueue: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("mong đợi 2 mục, nhận được %d", len(items))
	}
	if items[1].Summary != "second" {
		t.Fatalf("mong đợi mục thứ hai được lưu bền vững, nhận %+v", items[1])
	}
}

func TestRuntimeStoreAppendTaskLog(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	if err := store.Runtime.AppendTaskLog("task-1", domain.RuntimeTaskLogEntry{
		Agent:   "writer",
		Event:   "stream",
		Summary: "bắt đầu soạn thảo",
	}); err != nil {
		t.Fatalf("AppendTaskLog 1: %v", err)
	}
	if err := store.Runtime.AppendTaskLog("task-1", domain.RuntimeTaskLogEntry{
		Agent:   "writer",
		Event:   "tool",
		Tool:    "draft_chapter",
		Summary: "đã hoàn tất xuất nội dung chính",
	}); err != nil {
		t.Fatalf("AppendTaskLog 2: %v", err)
	}

	entries, err := store.Runtime.LoadTaskLog("task-1")
	if err != nil {
		t.Fatalf("LoadTaskLog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("mong đợi 2 mục nhật ký tác vụ, nhận được %d", len(entries))
	}
	if entries[1].Tool != "draft_chapter" {
		t.Fatalf("mong đợi tool được lưu bền vững, nhận %+v", entries[1])
	}
}

func TestRuntimeStoreLoadQueueAfter(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	for _, summary := range []string{"one", "two", "three"} {
		if _, err := store.Runtime.AppendQueue(domain.RuntimeQueueItem{
			Priority: domain.RuntimePriorityBackground,
			Summary:  summary,
		}); err != nil {
			t.Fatalf("AppendQueue %s: %v", summary, err)
		}
	}

	items, err := store.Runtime.LoadQueueAfter(1)
	if err != nil {
		t.Fatalf("LoadQueueAfter: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("mong đợi 2 mục sau seq 1, nhận được %d", len(items))
	}
	if items[0].Summary != "two" || items[1].Summary != "three" {
		t.Fatalf("các mục không mong đợi: %+v", items)
	}
}

func TestRuntimeStoreReset(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _ = store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Priority: domain.RuntimePriorityBackground,
		Summary:  "queued",
	})
	_ = store.Runtime.AppendTaskLog("task-1", domain.RuntimeTaskLogEntry{
		Event:   "stream_delta",
		Summary: "chênh lệch",
	})

	if err := store.Runtime.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	items, err := store.Runtime.LoadQueue()
	if err != nil {
		t.Fatalf("LoadQueue sau khi reset: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("mong đợi hàng đợi rỗng sau khi reset, nhận được %d", len(items))
	}

	logs, err := store.Runtime.LoadTaskLog("task-1")
	if err != nil {
		t.Fatalf("LoadTaskLog sau khi reset: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("mong đợi nhật ký tác vụ rỗng sau khi reset, nhận được %d", len(logs))
	}
}
