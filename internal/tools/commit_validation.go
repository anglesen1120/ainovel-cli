package tools

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/chapterfacts"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// validateCommitArgs kiểm tra tải trọng ngữ nghĩa đầy đủ do mô hình gửi lên trước khi tạo PendingCommit.
// Lỗi được trả thẳng cho mô hình để sửa; không tạo trạng thái dở dang và cũng không đoán giá trị còn thiếu.
func (t *CommitChapterTool) validateCommitArgs(a commitArgs) error {
	if err := chapterfacts.Validate(a.ChapterFacts); err != nil {
		return fmt.Errorf("%v: %w", err, errs.ErrToolArgs)
	}

	if len(a.ForeshadowUpdates) > 0 {
		ledger, err := t.store.World.LoadForeshadowLedger()
		if err != nil {
			return fmt.Errorf("tải sổ cái foreshadow: %w: %w", errs.ErrStoreRead, err)
		}
		// Sổ cái là hình chiếu của toàn bộ sách, còn Projector phát lại ghi chép chương theo thứ tự chương.
		// Khi viết lại các chương đầu, trong sổ cái vẫn còn các chi tiết báo trước được gieo ở những chương sau;
		// nếu cho qua chúng, kiểm tra trước khi commit sẽ trái ngược với kết luận phát lại,
		// mô hình không có cách nào sửa và hàng đợi làm lại sẽ bị khóa cứng. Vì vậy luôn lấy "những gì chương này thấy được" làm chuẩn.
		plantedAt := make(map[string]int, len(ledger))
		for _, entry := range ledger {
			plantedAt[entry.ID] = entry.PlantedAt
		}
		declared := make(map[string]struct{}, len(a.ForeshadowUpdates))
		for i, update := range a.ForeshadowUpdates {
			switch update.Action {
			case "plant":
				declared[update.ID] = struct{}{}
			case "advance", "resolve":
				if _, ok := declared[update.ID]; ok {
					continue
				}
				at, known := plantedAt[update.ID]
				if !known {
					return fmt.Errorf("foreshadow_updates[%d] tham chiếu id không xác định %q: %w", i, update.ID, errs.ErrToolPrecondition)
				}
				if at > a.Chapter {
					return fmt.Errorf("foreshadow_updates[%d] chi tiết báo trước %q được gieo ở chương %d, không thể được đẩy tiến hoặc thu hồi ở chương %d: %w",
						i, update.ID, at, a.Chapter, errs.ErrToolPrecondition)
				}
			}
		}
	}
	return nil
}
