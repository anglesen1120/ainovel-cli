package host

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// TestHostReopen bảo vệ cổng mở lại cấp người dùng của /reopen: hoàn bản là một quyết định
// lại, việc mở lại chỉ do người dùng chủ động khởi xướng — chưa hoàn kết thì từ chối, đang chạy
// thì từ chối; mở lại thành công sẽ lùi phase về writing, và hướng viết tiếp đi kèm sẽ được ghi
// là can thiệp chờ xử lý (PendingSteer), khi khôi phục sẽ qua Arbiter quyết định đưa vào rồi
// mới chạy tiếp.
func TestHostReopen(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	h := &Host{store: st, events: make(chan Event, 8)}

	if err := st.Progress.Init(2); err != nil {
		t.Fatal(err)
	}
	if err := h.Reopen(""); err == nil {
		t.Fatal("Sách chưa hoàn kết phải từ chối mở lại")
	}

	_ = st.Progress.UpdatePhase(domain.PhaseWriting)
	if err := st.Progress.MarkComplete(); err != nil {
		t.Fatal(err)
	}
	if err := h.Reopen("Với tám mươi năm đại hạn, mở một cuốn mới"); err != nil {
		t.Fatalf("Mở lại sách đã hoàn kết phải thành công: %v", err)
	}
	p, _ := st.Progress.Load()
	if p.Phase != domain.PhaseWriting {
		t.Fatalf("Sau khi mở lại, phase phải là writing, nhận %s", p.Phase)
	}
	if len(p.PendingRewrites) != 0 || p.ReopenedFromComplete {
		t.Fatalf("Mở lại để viết tiếp không được mang theo ngữ nghĩa làm lại: %+v", p)
	}
	// Số lần mở lại phải được ghi xuống đĩa: digest của progress sau khi hoàn tất lại
	// mới có thể khác với lần trước — checkpoint trùng digest thì sẽ bị khử trùng lặp
	// theo idempotent, lần hoàn tất lại có cùng bytes sẽ không sinh checkpoint mới,
	// StopGuard sẽ hiểu nhầm là kết thúc rỗng do chạy vô ích.
	if p.ReopenCount != 1 {
		t.Fatalf("Số lần mở lại phải là 1, nhận %d", p.ReopenCount)
	}
	meta, _ := st.RunMeta.Load()
	if meta == nil || !strings.Contains(meta.PendingSteer, "tám mươi năm đại hạn") {
		t.Fatalf("Định hướng viết tiếp phải được ghi là can thiệp chờ xử lý, nhận %+v", meta)
	}

	running := &Host{store: st, lifecycle: lifecycleRunning, events: make(chan Event, 1)}
	if err := running.Reopen(""); err == nil {
		t.Fatal("Hệ thống đang chạy phải từ chối mở lại")
	}
}
