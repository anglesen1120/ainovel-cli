package tui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
)

func TestBootstrapExistingBookFailureStaysInWorkbench(t *testing.T) {
	m := Model{mode: modeNew, textarea: textarea.New()}
	next, cmd, handled := m.handleRuntimeMsg(bootstrapMsg{existing: true, err: errors.New("di chuyển thất bại")})
	if !handled || cmd == nil {
		t.Fatal("khôi phục tác phẩm có sẵn thất bại vẫn phải làm mới workspace")
	}
	got := next.(Model)
	if got.mode != modeRunning {
		t.Fatalf("sau khi khôi phục tác phẩm có sẵn thất bại phải ở lại workspace, nhận mode=%v", got.mode)
	}
	if got.err == nil || got.err.Error() != "di chuyển thất bại" {
		t.Fatalf("workspace phải hiển thị lỗi gốc, nhận %v", got.err)
	}
}

// TestBootstrapCompletedBookLandsOnDoneWorkbench bảo vệ đích khởi động của sách đã hoàn tất: resumeLabel đúng
// complete trả về nhãn rỗng, hành vi cũ rơi vào trang chào mừng——trang chào mừng không nhắc tới sách có sẵn, người dùng sẽ tưởng sách đã mất,
// và vị trí tự nhiên của /reopen、/export và thao tác làm lại đều ở workbench trạng thái hoàn tất.
func TestBootstrapCompletedBookLandsOnDoneWorkbench(t *testing.T) {
	m := Model{mode: modeNew, textarea: textarea.New()}
	next, cmd, handled := m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if !handled || cmd == nil {
		t.Fatal("completed bootstrap phải được xử lý và trả về lệnh")
	}
	got := next.(Model)
	if got.mode != modeDone {
		t.Fatalf("sách đã hoàn tất phải vào workspace trạng thái hoàn tất, nhận mode=%v", got.mode)
	}
	if got.textarea.Placeholder != donePlaceholder {
		t.Fatalf("phải đưa gợi ý trạng thái hoàn tất (gồm /reopen), nhận %q", got.textarea.Placeholder)
	}

	// đã ở workspace（như sau khi phiên kết thúc rồi lại nhận bootstrap）không được đổi trạng thái lặp lại。
	m = Model{mode: modeRunning, textarea: textarea.New()}
	next, _, _ = m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if next.(Model).mode != modeRunning {
		t.Fatal("không ở trang chào mừng thì completed bootstrap không được đổi trạng thái")
	}
}
