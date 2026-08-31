package tools

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/chapterfacts"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// validateCommitArgs kiểm tra tải ngữ nghĩa hoàn chỉnh do mô hình nộp trước khi tạo PendingCommit.
// Trả lỗi trực tiếp để mô hình sửa; không tạo trạng thái dở dang hay suy đoán giá trị thiếu.
func (t *CommitChapterTool) validateCommitArgs(a commitArgs) error {
	if err := chapterfacts.Validate(a.ChapterFacts); err != nil {
		return fmt.Errorf("%v: %w", err, errs.ErrToolArgs)
	}

	if len(a.ForeshadowUpdates) > 0 {
		ledger, err := t.store.World.LoadForeshadowLedger()
		if err != nil {
			return fmt.Errorf("Không thể tải sổ tình tiết gieo mầm: %w: %w", errs.ErrStoreRead, err)
		}
		// Sổ là phép chiếu toàn truyện, còn Projector phát lại các bản ghi chương theo thứ tự chương. Khi viết lại chương trước,
		// sổ vẫn có thể chứa tình tiết chỉ được gieo ở chương sau — cho phép chúng sẽ khiến kiểm tra trước khi nộp trái với kết quả phát lại,
		// mô hình không thể sửa và hàng đợi viết lại bị khóa. Vì vậy chỉ chấp nhận tình tiết hiển thị ở chương này.
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
					return fmt.Errorf("foreshadow_updates[%d] tình tiết gieo mầm %q được gieo ở chương %d, không thể phát triển hoặc thu hồi ở chương %d: %w",
						i, update.ID, at, a.Chapter, errs.ErrToolPrecondition)
				}
			}
		}
	}
	return nil
}
