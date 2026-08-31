package tools

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/store"
)

// requireAggregateTarget ràng buộc bản ghi tổng hợp mới của Editor vào hạng mục duy nhất Router đang chờ.
// Mục tiêu được suy ra hoàn toàn từ dữ kiện đã lưu, không phụ thuộc mô tả nhiệm vụ và không tin số chương/tập/cung do mô hình tự điền;
// việc hoàn tất idempotent với cùng nội dung đã lưu được mỗi công cụ nhận diện trước khi gọi hàm này.
func requireAggregateTarget(st *store.Store, kind flow.AggregateKind, volume, arc, endChapter int) error {
	state, err := flow.LoadState(st)
	if err != nil {
		return fmt.Errorf("Không thể tải trạng thái tổng hợp: %w: %w", errs.ErrStoreRead, err)
	}
	due := state.AggregateRefresh
	if due == nil {
		return fmt.Errorf("Hiện không có hạng mục %s đang chờ xử lý: %w", kind, errs.ErrToolPrecondition)
	}
	targetMismatch := due.Kind != kind
	switch kind {
	case flow.AggregateArcReview, flow.AggregateArcSummary:
		targetMismatch = targetMismatch || due.Volume != volume || due.Arc != arc
	case flow.AggregateVolumeSummary:
		targetMismatch = targetMismatch || due.Volume != volume
	case flow.AggregateGlobalReview:
		// Đánh giá toàn cục không có tọa độ tập/cung, chỉ được định vị bằng kind và chương kết thúc.
	}
	endMismatch := endChapter > 0 && due.EndChapter != endChapter
	if targetMismatch || endMismatch {
		return fmt.Errorf(
			"Mục tiêu ghi tổng hợp không khớp: hiện phải xử lý kind=%s volume=%d arc=%d end_chapter=%d, nhưng nhận kind=%s volume=%d arc=%d end_chapter=%d: %w",
			due.Kind, due.Volume, due.Arc, due.EndChapter,
			kind, volume, arc, endChapter, errs.ErrToolConflict,
		)
	}
	return nil
}
