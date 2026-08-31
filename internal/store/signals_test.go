package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestPendingCommitLifecycle(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	pending := domain.PendingCommit{
		Chapter:   3,
		Stage:     domain.CommitStageProgressMarked,
		Summary:   "Tóm tắt chương 3",
		StartedAt: "2026-03-27T10:00:00Z",
		UpdatedAt: "2026-03-27T10:01:00Z",
		Result: &domain.CommitResult{
			Chapter:     3,
			Committed:   true,
			WordCount:   2400,
			NextChapter: 4,
		},
	}
	if err := s.Signals.SavePendingCommit(pending); err != nil {
		t.Fatalf("SavePendingCommit: %v", err)
	}

	got, err := s.Signals.LoadPendingCommit()
	if err != nil {
		t.Fatalf("LoadPendingCommit: %v", err)
	}
	if got == nil {
		t.Fatal("mong đợi pending commit, nhận được nil")
	}
	if got.Chapter != 3 || got.Stage != domain.CommitStageProgressMarked {
		t.Fatalf("pending commit không mong đợi: %+v", got)
	}
	if got.Result == nil || got.Result.NextChapter != 4 {
		t.Fatalf("kết quả pending không mong đợi: %+v", got.Result)
	}

	if err := s.Signals.ClearPendingCommit(); err != nil {
		t.Fatalf("ClearPendingCommit: %v", err)
	}
	got, err = s.Signals.LoadPendingCommit()
	if err != nil {
		t.Fatalf("LoadPendingCommit sau khi xóa: %v", err)
	}
	if got != nil {
		t.Fatalf("mong đợi pending commit đã được xóa, nhận được %+v", got)
	}
}
