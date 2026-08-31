package store

import (
	"fmt"
	"os"
	"slices"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// ProgressStore quản lý trạng thái tiến độ sáng tác.
type ProgressStore struct{ io *IO }

func NewProgressStore(io *IO) *ProgressStore { return &ProgressStore{io: io} }

// Load Đọc meta/progress.json. Nếu không tồn tại thì trả về nil.
func (s *ProgressStore) Load() (*domain.Progress, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	return s.loadUnlocked()
}

func (s *ProgressStore) loadUnlocked() (*domain.Progress, error) {
	var p domain.Progress
	if err := s.io.ReadJSONUnlocked("meta/progress.json", &p); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

// Save Lưu tiến độ.
func (s *ProgressStore) Save(p *domain.Progress) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.saveUnlocked(p)
}

func (s *ProgressStore) saveUnlocked(p *domain.Progress) error {
	return s.io.WriteJSONUnlocked("meta/progress.json", p)
}

// Init Tạo tiến độ ban đầu.
func (s *ProgressStore) Init(totalChapters int) error {
	return s.Save(&domain.Progress{
		Phase:         domain.PhaseInit,
		TotalChapters: totalChapters,
	})
}

// SetTotalChapters Cập nhật dung lượng dàn ý: ở chế độ không phân tầng là số chương chi tiết, ở chế độ phân tầng là ước tính nội bộ.
func (s *ProgressStore) SetTotalChapters(n int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		p.TotalChapters = n
		return s.saveUnlocked(p)
	})
}

// UpdatePhase Cập nhật giai đoạn sáng tác.
func (s *ProgressStore) UpdatePhase(phase domain.Phase) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, phase); err != nil {
			return err
		}
		p.Phase = phase
		return s.saveUnlocked(p)
	})
}

// AdvancePhase Đẩy giai đoạn sáng tác ít nhất tới phase; nếu đã ở giai đoạn sau hơn thì giữ nguyên.
// Phù hợp cho các hiện vật giai đoạn có thể lưu lặp lại, tránh việc sửa lại hiện vật cũ bị hiểu nhầm là lùi giai đoạn.
func (s *ProgressStore) AdvancePhase(phase domain.Phase) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if domain.CanTransitionPhase(phase, p.Phase) {
			return nil
		}
		if err := domain.ValidatePhaseTransition(p.Phase, phase); err != nil {
			return err
		}
		p.Phase = phase
		return s.saveUnlocked(p)
	})
}

// StartChapter Đánh dấu một chương vào trạng thái đang viết. Hàm này không chịu trách nhiệm chuyển giai đoạn; caller phải để flow foundation/import đẩy Progress rõ ràng sang writing trước, tránh giao nhầm việc đi vòng qua giai đoạn lập kế hoạch.
func (s *ProgressStore) StartChapter(chapter int) error {
	if chapter <= 0 {
		return fmt.Errorf("chapter phải > 0")
	}
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseWriting {
			return fmt.Errorf("viết chương chỉ được phép ở giai đoạn writing (phase hiện tại=%s): %w", p.Phase, errs.ErrToolPrecondition)
		}
		if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
			p.Flow = domain.FlowWriting
		}
		if p.CurrentChapter < chapter {
			p.CurrentChapter = chapter
		}
		p.InProgressChapter = chapter
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// IsChapterCompleted Kiểm tra xem chương đã được commit hoàn tất chưa. Nếu đọc thất bại thì phải trả về rõ ràng, không được coi progress hỏng là “chưa hoàn thành” rồi tiếp tục ghi đè chương.
func (s *ProgressStore) IsChapterCompleted(chapter int) (bool, error) {
	p, err := s.Load()
	if err != nil {
		return false, err
	}
	if p == nil {
		return false, nil
	}
	return slices.Contains(p.CompletedChapters, chapter), nil
}

// MarkChapterComplete Đánh dấu chương hoàn tất, cập nhật tiến độ nguyên tử.
func (s *ProgressStore) MarkChapterComplete(chapter, wordCount int, hookType, dominantStrand string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress chưa khởi tạo, hãy gọi Init trước")
		}
		if p.ChapterWordCounts == nil {
			p.ChapterWordCounts = make(map[int]int)
		}
		if oldWC, ok := p.ChapterWordCounts[chapter]; ok {
			p.TotalWordCount -= oldWC
		}
		p.ChapterWordCounts[chapter] = wordCount
		p.TotalWordCount += wordCount
		if !slices.Contains(p.CompletedChapters, chapter) {
			p.CompletedChapters = append(p.CompletedChapters, chapter)
		}
		if chapter+1 > p.CurrentChapter {
			p.CurrentChapter = chapter + 1
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseWriting); err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting

		if dominantStrand != "" {
			for len(p.StrandHistory) < chapter-1 {
				p.StrandHistory = append(p.StrandHistory, "")
			}
			if len(p.StrandHistory) < chapter {
				p.StrandHistory = append(p.StrandHistory, dominantStrand)
			} else {
				p.StrandHistory[chapter-1] = dominantStrand
			}
		}
		if hookType != "" {
			for len(p.HookHistory) < chapter-1 {
				p.HookHistory = append(p.HookHistory, "")
			}
			if len(p.HookHistory) < chapter {
				p.HookHistory = append(p.HookHistory, hookType)
			} else {
				p.HookHistory[chapter-1] = hookType
			}
		}

		return s.saveUnlocked(p)
	})
}

// MarkComplete Đánh dấu toàn bộ sách hoàn thành và xóa cờ mở lại làm lại (đã kết thúc thì không còn ở trạng thái làm lại).
func (s *ProgressStore) MarkComplete() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			p = &domain.Progress{}
		}
		if err := domain.ValidatePhaseTransition(p.Phase, domain.PhaseComplete); err != nil {
			return err
		}
		p.Phase = domain.PhaseComplete
		p.ReopenedFromComplete = false
		return s.saveUnlocked(p)
	})
}

// Reopen Mở lại sách đã hoàn tất vào trạng thái làm lại: phase complete→writing + thêm chương mục tiêu vào hàng đợi + flow=rewriting,
// được thực hiện nguyên tử trong một write lock. Đây là lối thoát ngoại lệ duy nhất cho ràng buộc phaseOrder “chỉ tiến” — cố ý không đi qua
// ValidatePhaseTransition; tính hợp lệ của bước lùi được gom vào chính phương thức này, và được bảo vệ bởi guard đầu vào phase=complete,
// tránh việc dùng sai khiến máy trạng thái mất kiểm soát. Sau khi xử lý xong hàng đợi, commit_chapter sẽ tự động hoàn tất lại ở cuối.
func (s *ProgressStore) Reopen(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseComplete {
			return fmt.Errorf("reopen chỉ áp dụng cho sách đã hoàn tất (phase hiện tại=%s): %w", p.Phase, errs.ErrToolPrecondition)
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.Phase = domain.PhaseWriting // bước lùi hợp lệ duy nhất, được bảo vệ bởi điều kiện complete ở trên
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		p.Flow = domain.FlowRewriting
		p.ReopenedFromComplete = true // sau khi hàng đợi rỗng thì sẽ hoàn tất lại theo cấu trúc đầy đủ, xem khối drain của commit_chapter
		return s.saveUnlocked(p)
	})
}

// ReopenContinue Chuyển sách đã hoàn tất sang trạng thái viết tiếp: chỉ phase complete→writing, không đưa vào hàng đợi làm lại,
// và không đặt ReopenedFromComplete (đó là ngữ nghĩa drain “làm lại xong rồi tự hoàn tất lại theo cấu trúc cũ”,
// còn mở lại để viết tiếp thì phải mở rộng cấu trúc). Cùng là lối thoát khỏi ràng buộc phaseOrder “chỉ tiến”,
// và cũng chịu guard đầu vào phase=complete; sau khi mở lại, bộ định tuyến cuối chương sẽ phân phát cho kiến trúc sư tiếp tục quyển.
func (s *ProgressStore) ReopenContinue() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if p.Phase != domain.PhaseComplete {
			return fmt.Errorf("reopen chỉ áp dụng cho sách đã hoàn tất (phase hiện tại=%s): %w", p.Phase, errs.ErrToolPrecondition)
		}
		p.Phase = domain.PhaseWriting
		p.ReopenCount++ // audit + bảo đảm digest tiến độ khi hoàn tất lại khác lần trước (xem chú thích trường)
		return s.saveUnlocked(p)
	})
}

// ClearInProgress Xóa trạng thái trung gian của tiến độ.
func (s *ProgressStore) ClearInProgress() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.InProgressChapter = 0
		p.CompletedScenes = nil
		return s.saveUnlocked(p)
	})
}

// UpdateVolumeArc Cập nhật vị trí quyển/cung hiện tại.
func (s *ProgressStore) UpdateVolumeArc(volume, arc int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.CurrentVolume = volume
		p.CurrentArc = arc
		return s.saveUnlocked(p)
	})
}

// SetLayered Thiết lập cờ chế độ phân tầng.
func (s *ProgressStore) SetLayered(layered bool) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.Layered = layered
		return s.saveUnlocked(p)
	})
}

// SetFlow Cập nhật trạng thái luồng hiện tại.
func (s *ProgressStore) SetFlow(flow domain.FlowState) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
			return err
		}
		p.Flow = flow
		return s.saveUnlocked(p)
	})
}

// SetPendingRewrites Thiết lập hàng đợi chương cần viết lại và lý do.
// PendingRewrites chỉ được chứa các chương đã hoàn thành; chương chưa hoàn thành còn chưa có bản cuối nên không được vào hàng đợi viết lại/đánh bóng.
func (s *ProgressStore) SetPendingRewrites(chapters []int, reason string) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
		if err != nil {
			return err
		}
		p.PendingRewrites = normalized
		p.RewriteReason = reason
		return s.saveUnlocked(p)
	})
}

// ApplyReviewOutcome Áp dụng nguyên tử trạng thái luồng do rà soát tạo ra. Ý nghĩa rà soát do tầng trên quyết định; Store chỉ chịu trách nhiệm
// kiểm tra chuyển đổi Flow và các chương cần làm lại, đồng thời bảo đảm Flow, PendingRewrites, RewriteReason không xuất hiện trạng thái trung gian.
func (s *ProgressStore) ApplyReviewOutcome(flow domain.FlowState, chapters []int, reason string) (*domain.Progress, error) {
	var latest *domain.Progress
	err := s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if len(chapters) > 0 {
			if flow == domain.FlowWriting {
				return fmt.Errorf("khi danh sách chương làm lại không rỗng thì flow không được là writing: %w", errs.ErrToolConflict)
			}
			if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
				return err
			}
			normalized, err := normalizePendingRewrites(chapters, p.CompletedChapters)
			if err != nil {
				return err
			}
			p.PendingRewrites = normalized
			p.RewriteReason = reason
			p.Flow = flow
		} else if len(p.PendingRewrites) == 0 {
			if err := domain.ValidateFlowTransition(p.Flow, flow); err != nil {
				return err
			}
			p.Flow = flow
		}
		if err := s.saveUnlocked(p); err != nil {
			return err
		}
		latest = p
		return nil
	})
	return latest, err
}

// ValidatePendingRewrites Kiểm tra danh sách chương có thể vào hàng đợi làm lại hay không, không thay đổi trạng thái.
func (s *ProgressStore) ValidatePendingRewrites(chapters []int) error {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()

	p, err := s.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		_, err := normalizePendingRewrites(chapters, nil)
		return err
	}
	_, err = normalizePendingRewrites(chapters, p.CompletedChapters)
	return err
}

// CompleteRewrite Loại bỏ chương đã hoàn thành khỏi hàng đợi viết lại.
func (s *ProgressStore) CompleteRewrite(chapter int) error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		var remaining []int
		for _, ch := range p.PendingRewrites {
			if ch != chapter {
				remaining = append(remaining, ch)
			}
		}
		p.PendingRewrites = remaining
		if len(remaining) == 0 {
			if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
				return err
			}
			p.Flow = domain.FlowWriting
			p.RewriteReason = ""
		}
		return s.saveUnlocked(p)
	})
}

// ClearPendingRewrites Buộc xóa sạch hàng đợi viết lại.
func (s *ProgressStore) ClearPendingRewrites() error {
	return s.io.WithWriteLock(func() error {
		p, err := s.loadUnlocked()
		if err != nil {
			return err
		}
		if p == nil {
			return nil
		}
		p.PendingRewrites = nil
		p.RewriteReason = ""
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		return s.saveUnlocked(p)
	})
}

// ValidateChapterWork Kiểm tra chương hiện tại có được phép được lập kế hoạch hoặc nộp hay không.
// Writer chỉ được làm việc ở giai đoạn writing; trong luồng đánh bóng/viết lại, chỉ được xử lý các chương nằm trong PendingRewrites.
// Ràng buộc giai đoạn được kiểm tra lại ở biên Store để tránh việc phân việc sai của Arbiter đi vòng qua Router.
func (s *ProgressStore) ValidateChapterWork(chapter int) error {
	p, err := s.Load()
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("progress chưa khởi tạo: %w", errs.ErrToolPrecondition)
	}
	if p.Phase != domain.PhaseWriting {
		return fmt.Errorf("viết chương chỉ được phép ở giai đoạn writing (phase hiện tại=%s): %w", p.Phase, errs.ErrToolPrecondition)
	}
	if p.Flow != domain.FlowRewriting && p.Flow != domain.FlowPolishing {
		return nil
	}
	if _, err := normalizePendingRewrites(p.PendingRewrites, p.CompletedChapters); err != nil {
		return err
	}
	if slices.Contains(p.PendingRewrites, chapter) {
		return nil
	}

	verb := "viết lại"
	if p.Flow == domain.FlowPolishing {
		verb = "trau chuốt"
	}
	return fmt.Errorf("chương %d không nằm trong hàng đợi chờ %s, hàng đợi hiện tại: %v. Hãy xử lý các chương trong hàng đợi trước, rồi mới động vào chương mới: %w", chapter, verb, p.PendingRewrites, errs.ErrToolConflict)
}

func normalizePendingRewrites(chapters, completed []int) ([]int, error) {
	if len(chapters) == 0 {
		return nil, nil
	}
	completedSet := make(map[int]struct{}, len(completed))
	for _, ch := range completed {
		completedSet[ch] = struct{}{}
	}

	seen := make(map[int]struct{}, len(chapters))
	normalized := make([]int, 0, len(chapters))
	var invalid []int
	for _, ch := range chapters {
		if ch <= 0 {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := completedSet[ch]; !ok {
			invalid = append(invalid, ch)
			continue
		}
		if _, ok := seen[ch]; ok {
			continue
		}
		seen[ch] = struct{}{}
		normalized = append(normalized, ch)
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("pending_rewrites chỉ được chứa các chương đã hoàn thành, chương không hợp lệ: %v, completed_chapters=%v: %w", invalid, completed, errs.ErrToolPrecondition)
	}
	return normalized, nil
}
