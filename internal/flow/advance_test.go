package flow

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestStartsForwardChapter(t *testing.T) {
	base := &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}}
	tests := []struct {
		name    string
		inst    *Instruction
		p       *domain.Progress
		pending *domain.PendingCommit
		want    bool
	}{
		{"chương kế tiếp bình thường", &Instruction{Agent: "writer", Chapter: 2}, base, nil, true},
		{"suy ra chương số không theo facts", &Instruction{Agent: "writer"}, base, nil, true},
		{"văn bản không tham gia", &Instruction{Agent: "writer", Chapter: 2, Task: "bất kỳ", Reason: "bất kỳ"}, base, nil, true},
		{"Writer làm lại", &Instruction{Agent: "writer", Chapter: 1}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}, PendingRewrites: []int{1}}, nil, false},
		{"khôi phục chương", &Instruction{Agent: "writer", Chapter: 2}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1}, InProgressChapter: 2}, nil, false},
		{"khôi phục commit", &Instruction{Agent: "writer", Chapter: 2}, base, &domain.PendingCommit{Chapter: 2}, false},
		{"không phải chương kế", &Instruction{Agent: "writer", Chapter: 3}, base, nil, false},
		{"Editor", &Instruction{Agent: "editor"}, base, nil, false},
		{"lệnh rỗng", nil, base, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StartsForwardChapter(tc.inst, tc.p, tc.pending); got != tc.want {
				t.Fatalf("StartsForwardChapter() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveAdvanceHold(t *testing.T) {
	tests := []struct {
		name    string
		hold    *domain.AdvanceHold
		p       *domain.Progress
		want    AdvanceHoldResolution
		wantErr bool
	}{
		{"không có hold", nil, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldKeep, false},
		{"tạm dừng ở ranh giới", &domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "dừng"}, &domain.Progress{Phase: domain.PhaseWriting, PendingRewrites: []int{1}}, AdvanceHoldConsumeAndStop, false},
		{"hàng đợi làm lại chưa rỗng", &domain.AdvanceHold{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "nghiệm thu"}, &domain.Progress{Phase: domain.PhaseWriting, PendingRewrites: []int{1}}, AdvanceHoldKeep, false},
		{"hàng đợi làm lại đã rỗng", &domain.AdvanceHold{After: domain.AdvanceHoldAfterRewritesDrained, Reason: "nghiệm thu"}, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldConsumeAndStop, false},
		{"chương mục tiêu chưa hoàn tất", &domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 3, Reason: "viết đến chương 3"}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1, 2}}, AdvanceHoldKeep, false},
		{"chương mục tiêu đã hoàn tất", &domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, TargetChapter: 3, Reason: "viết đến chương 3"}, &domain.Progress{Phase: domain.PhaseWriting, CompletedChapters: []int{1, 2, 3}}, AdvanceHoldConsumeAndStop, false},
		{"thiếu chương mục tiêu", &domain.AdvanceHold{After: domain.AdvanceHoldAtChapter, Reason: "viết đến chương mục tiêu"}, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldKeep, true},
		{"hoàn tất sách chỉ consume", &domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "dừng"}, &domain.Progress{Phase: domain.PhaseComplete}, AdvanceHoldConsume, false},
		{"điều kiện không xác định", &domain.AdvanceHold{After: "unknown", Reason: "dừng"}, &domain.Progress{Phase: domain.PhaseWriting}, AdvanceHoldKeep, true},
		{"thiếu tiến độ", &domain.AdvanceHold{After: domain.AdvanceHoldAtBoundary, Reason: "dừng"}, nil, AdvanceHoldKeep, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAdvanceHold(tc.hold, tc.p)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("resolution = %v, want %v", got, tc.want)
			}
		})
	}
}
