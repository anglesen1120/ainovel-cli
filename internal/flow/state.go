package flow

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// LoadState đọc toàn bộ dữ kiện mà Route cần từ Store.
// Đây là "ranh giới IO" của bộ định tuyến: mọi thao tác đọc tập trung tại đây để Route luôn thuần.
// Bất kỳ lỗi đọc nào cũng được trả về; artifact hỏng và “chưa được tạo” là hai dữ kiện khác nhau, Router không được
// tiếp tục phân công khi snapshot không đầy đủ.
func LoadState(store *storepkg.Store) (State, error) {
	var s State
	missing, err := store.FoundationMissing()
	if err != nil {
		return s, fmt.Errorf("tải trạng thái foundation: %w", err)
	}
	s.FoundationMissing = missing
	// Cấp quy hoạch: save_foundation ghi scale vào RunMeta; nhánh bù dùng nó để suy ra planner.
	// Lỗi đọc được coi là chưa biết (tier rỗng → LLM quyết định việc bù), nhất quán với mặc định thận trọng cho các dữ kiện khác.
	meta, err := store.RunMeta.Load()
	if err != nil {
		return s, fmt.Errorf("tải metadata lần chạy: %w", err)
	}
	if meta != nil {
		s.PlanningTier = meta.PlanningTier
	}
	progress, err := store.Progress.Load()
	if err != nil {
		return s, fmt.Errorf("tải tiến độ: %w", err)
	}
	if progress == nil {
		return s, nil
	}
	s.Progress = progress
	feedback, err := store.Outline.LoadPendingOutlineFeedback()
	if err != nil {
		return s, fmt.Errorf("tải phản hồi dàn ý: %w", err)
	}
	for _, item := range feedback {
		if item.RequiresImmediateReview() {
			s.ImmediateFeedbackCount++
		}
	}

	s.LastCompleted = progress.LatestCompleted()

	// Ranh giới hồi chỉ được tính ở chế độ phân tầng khi có chương đã hoàn thành.
	if progress.Layered && s.LastCompleted > 0 {
		boundaries, err := store.Outline.CompletedArcBoundaries(s.LastCompleted)
		if err != nil {
			return s, fmt.Errorf("tải ranh giới hồi đã hoàn thành: %w", err)
		}
		for i := range boundaries {
			boundary := &boundaries[i]
			hasReview, err := store.World.HasArcReview(boundary.EndChapter)
			if err != nil {
				return s, fmt.Errorf("tải đánh giá hồi: %w", err)
			}
			if !hasReview {
				s.AggregateRefresh = aggregateRefresh(AggregateArcReview, boundary)
				break
			}
			hasArcSummary, err := store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
			if err != nil {
				return s, fmt.Errorf("tải tóm tắt hồi: %w", err)
			}
			if !hasArcSummary {
				s.AggregateRefresh = aggregateRefresh(AggregateArcSummary, boundary)
				break
			}
			if boundary.IsVolumeEnd {
				hasVolumeSummary, err := store.Summaries.HasVolumeSummary(boundary.Volume)
				if err != nil {
					return s, fmt.Errorf("tải tóm tắt quyển: %w", err)
				}
				if !hasVolumeSummary {
					s.AggregateRefresh = aggregateRefresh(AggregateVolumeSummary, boundary)
					break
				}
			}
		}

		boundary, err := store.Outline.CheckArcBoundary(s.LastCompleted)
		if err != nil {
			return s, fmt.Errorf("kiểm tra ranh giới hồi: %w", err)
		}
		if boundary != nil {
			s.ArcBoundary = boundary
			if boundary.IsArcEnd {
				s.HasArcReview, err = store.World.HasArcReview(s.LastCompleted)
				if err != nil {
					return s, fmt.Errorf("tải đánh giá hồi: %w", err)
				}
				s.HasArcSummary, err = store.Summaries.HasArcSummary(boundary.Volume, boundary.Arc)
				if err != nil {
					return s, fmt.Errorf("tải tóm tắt hồi: %w", err)
				}
				if boundary.IsVolumeEnd {
					s.HasVolumeSummary, err = store.Summaries.HasVolumeSummary(boundary.Volume)
					if err != nil {
						return s, fmt.Errorf("tải tóm tắt quyển: %w", err)
					}
				}
			}
		}
	}

	// Dữ kiện đánh giá toàn cục không phân tầng: chỉ đọc đĩa tại điểm kích hoạt (các tổ hợp Route khác không dùng trường này).
	if !progress.Layered && s.LastCompleted > 0 {
		for completed := domain.ReviewInterval; completed <= len(progress.CompletedChapters); completed += domain.ReviewInterval {
			chapter := progress.CompletedChapters[completed-1]
			hasReview, err := store.World.HasGlobalReview(chapter)
			if err != nil {
				return s, fmt.Errorf("tải đánh giá toàn cục: %w", err)
			}
			if !hasReview {
				s.AggregateRefresh = &AggregateRefresh{Kind: AggregateGlobalReview, EndChapter: chapter}
				break
			}
		}
		if due, _ := domain.ShouldReview(len(progress.CompletedChapters)); due {
			s.HasGlobalReview, err = store.World.HasGlobalReview(s.LastCompleted)
			if err != nil {
				return s, fmt.Errorf("tải đánh giá toàn cục: %w", err)
			}
		}
	}

	return s, nil
}

func aggregateRefresh(kind AggregateKind, boundary *storepkg.ArcBoundary) *AggregateRefresh {
	return &AggregateRefresh{
		Kind: kind, Volume: boundary.Volume, Arc: boundary.Arc,
		StartChapter: boundary.StartChapter, EndChapter: boundary.EndChapter,
	}
}
