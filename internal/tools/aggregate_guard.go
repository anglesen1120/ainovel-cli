package tools

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/store"
)

// requireAggregateTarget gắn lượt ghi aggregate mới của Editor với artifact duy nhất mà Router đang chờ bổ sung.
// Mục tiêu được suy ra hoàn toàn từ dữ kiện đã ghi, không dựa vào mô tả tác vụ và không tin chương/volume/arc do model tự điền;
// phần hoàn tất lũy đẳng cho nội dung đã ghi giống nhau được từng công cụ nhận diện trước khi gọi hàm này.
func requireAggregateTarget(st *store.Store, kind flow.AggregateKind, volume, arc, endChapter int) error {
	state, err := flow.LoadState(st)
	if err != nil {
		return fmt.Errorf("load aggregate state: %w: %w", errs.ErrStoreRead, err)
	}
	due := state.AggregateRefresh
	if due == nil {
		return fmt.Errorf("hiện không có artifact %s nào đang chờ xử lý: %w", kind, errs.ErrToolPrecondition)
	}
	targetMismatch := due.Kind != kind
	switch kind {
	case flow.AggregateArcReview, flow.AggregateArcSummary:
		targetMismatch = targetMismatch || due.Volume != volume || due.Arc != arc
	case flow.AggregateVolumeSummary:
		targetMismatch = targetMismatch || due.Volume != volume
	case flow.AggregateGlobalReview:
		// Đánh giá toàn cục không có tọa độ volume/arc, chỉ được định vị bằng kind và chương kết thúc.
	}
	endMismatch := endChapter > 0 && due.EndChapter != endChapter
	if targetMismatch || endMismatch {
		return fmt.Errorf(
			"mục tiêu ghi aggregate không khớp: hiện phải xử lý kind=%s volume=%d arc=%d end_chapter=%d, nhận kind=%s volume=%d arc=%d end_chapter=%d: %w",
			due.Kind, due.Volume, due.Arc, due.EndChapter,
			kind, volume, arc, endChapter, errs.ErrToolConflict,
		)
	}
	return nil
}
