package domain

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/errs"
)

// Quy tắc chuyển trạng thái (phiên bản tối thiểu)
//
// Phase biểu thị giai đoạn lớn, áp dụng ràng buộc “chỉ tiến không lùi”:
//
//	init -> premise -> outline -> writing -> complete
//	  \---------> outline ------^
//	  \-----------------> writing
//
// Flow biểu thị luồng đang hoạt động hiện tại, cho phép chuyển đổi trong giai đoạn viết, nhưng không cho phép nhảy bất thường rõ rệt:
//
//	writing   -> reviewing / rewriting / polishing / steering / writing
//	reviewing -> writing / rewriting / polishing / steering / reviewing
//	rewriting -> writing / steering / rewriting
//	polishing -> writing / steering / polishing
//	steering  -> writing / reviewing / rewriting / polishing / steering
//
// Trạng thái rỗng (giá trị zero) được xem là “chưa khởi tạo”, cho phép chuyển sang bất kỳ trạng thái không rỗng hợp lệ nào.

var phaseOrder = map[Phase]int{
	PhaseInit:     1,
	PhasePremise:  2,
	PhaseOutline:  3,
	PhaseWriting:  4,
	PhaseComplete: 5,
}

// CanTransitionPhase xác định Phase có được phép chuyển đổi hay không.
// Quy tắc giữ đơn giản: cho phép chuyển cùng trạng thái, cho phép tiến lên, không cho phép lùi.
func CanTransitionPhase(from, to Phase) bool {
	if to == "" {
		return false
	}
	if from == "" || from == to {
		return true
	}
	fromOrder, fromOK := phaseOrder[from]
	toOrder, toOK := phaseOrder[to]
	if !fromOK || !toOK {
		return false
	}
	return toOrder >= fromOrder
}

// ValidatePhaseTransition kiểm tra chuyển đổi Phase có hợp lệ hay không.
func ValidatePhaseTransition(from, to Phase) error {
	if CanTransitionPhase(from, to) {
		return nil
	}
	return fmt.Errorf("chuyển đổi phase không hợp lệ: %q -> %q: %w", from, to, errs.ErrPhaseTransition)
}

// CanTransitionFlow xác định FlowState có được phép chuyển đổi hay không.
func CanTransitionFlow(from, to FlowState) bool {
	if to == "" {
		return false
	}
	if from == "" || from == to {
		return true
	}

	switch from {
	case FlowWriting:
		return to == FlowReviewing || to == FlowRewriting || to == FlowPolishing || to == FlowSteering
	case FlowReviewing:
		return to == FlowWriting || to == FlowRewriting || to == FlowPolishing || to == FlowSteering
	case FlowRewriting:
		return to == FlowWriting || to == FlowSteering
	case FlowPolishing:
		return to == FlowWriting || to == FlowSteering
	case FlowSteering:
		return to == FlowWriting || to == FlowReviewing || to == FlowRewriting || to == FlowPolishing
	default:
		return false
	}
}

// ValidateFlowTransition kiểm tra chuyển đổi FlowState có hợp lệ hay không.
func ValidateFlowTransition(from, to FlowState) error {
	if CanTransitionFlow(from, to) {
		return nil
	}
	return fmt.Errorf("chuyển đổi flow không hợp lệ: %q -> %q: %w", from, to, errs.ErrFlowTransition)
}
