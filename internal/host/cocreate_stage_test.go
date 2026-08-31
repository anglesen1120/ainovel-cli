package host

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newFlagTestHost tạo một Host tối thiểu, chỉ đủ để drive máy trạng thái của cờ cocreating và bộ chặn đồng thời.
// emitEvent dùng channel không chặn, chỉ cần buffer events là đủ, không cần observer.
// Nhánh chạy của PauseForCoCreate sẽ gọi Engine Abort (tái sử dụng đường tạm dừng Esc đã được kiểm chứng),
// không kiểm trong unit test này; ở đây chỉ bao phủ trạng thái không chạy và logic cờ/bộ chặn.
func newFlagTestHost(lc lifecycle, cocreating bool) *Host {
	return &Host{
		lifecycle:  lc,
		cocreating: cocreating,
		engine:     &engine{}, // acquireExclusive kiểm tra engine.isRunning() (cửa sổ chờ dừng)
		events:     make(chan Event, 16),
	}
}

func TestPauseForCoCreate_NonRunningSetsFlag(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("trạng thái idle phải cho phép vào giai đoạn đồng sáng tạo")
	}
	if !h.cocreating {
		t.Error("sau khi vào, cocreating phải là true")
	}
	if h.lifecycle != lifecycleIdle {
		t.Errorf("vào ở trạng thái không chạy không được đổi lifecycle, nhận %s", h.lifecycle)
	}
}

func TestPauseForCoCreate_RejectsCompleted(t *testing.T) {
	h := newFlagTestHost(lifecycleCompleted, false)
	if h.PauseForCoCreate() {
		t.Error("sau khi hoàn thành toàn bộ sách thì không được cho phép vào giai đoạn đồng sáng tạo")
	}
	if h.cocreating {
		t.Error("sau khi từ chối không được đặt cờ cocreating")
	}
}

func TestPauseForCoCreate_RejectsReentrant(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if h.PauseForCoCreate() {
		t.Error("đã ở trong đồng sáng tạo thì phải từ chối vào lại")
	}
}

func TestCancelCoCreate_ClearsFlag(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	h.CancelCoCreate()
	if h.cocreating {
		t.Error("sau khi hủy, cocreating phải được xóa")
	}
	if h.lifecycle != lifecyclePaused {
		t.Errorf("hủy không được đổi lifecycle, nhận %s", h.lifecycle)
	}
}

func TestCancelCoCreate_NoopWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecycleRunning, false)
	h.CancelCoCreate() // không được panic, không được đổi trạng thái
	if h.cocreating || h.lifecycle != lifecycleRunning {
		t.Error("ở trạng thái không đồng sáng tạo, CancelCoCreate phải là no-op")
	}
}

func TestResumeFromCoCreate_RejectsEmptyDraft(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, true)
	if err := h.ResumeFromCoCreate("   "); err == nil {
		t.Fatal("draft trống phải báo lỗi")
	}
	if !h.cocreating {
		t.Error("draft trống trả về trước khi xóa cờ, cocreating phải giữ nguyên true")
	}
}

func TestResumeFromCoCreate_RejectsWhenNotCocreating(t *testing.T) {
	h := newFlagTestHost(lifecyclePaused, false)
	err := h.ResumeFromCoCreate("## Hướng đi tiếp theo\n- Bước vào quyển hai")
	if err == nil || !strings.Contains(err.Error(), "không ở chế độ đồng sáng tạo") {
		t.Fatalf("ở trạng thái không đồng sáng tạo phải báo lỗi phù hợp, nhận %v", err)
	}
}

func TestAcquireExclusive(t *testing.T) {
	cases := []struct {
		name       string
		lc         lifecycle
		cocreating bool
		exclusive  string
		wantErr    string // rỗng = kỳ vọng cho qua
	}{
		{"đang chạy", lifecycleRunning, false, "", "đang chạy"},
		{"đồng sáng tạo", lifecyclePaused, true, "", "đồng sáng tạo theo giai đoạn"},
		{"bận", lifecycleIdle, false, "nhập", "nhập đang diễn ra"},
		{"rảnh", lifecycleIdle, false, "", ""},
		{"tạm dừng rảnh", lifecyclePaused, false, "", ""},
	}
	// Cửa sổ dừng Abort: lifecycle đã ở paused nhưng goroutine của engine vẫn chưa thoát hẳn, vẫn phải từ chối——
	// nếu không import sẽ ghi đồng thời vào cùng một store với phần kết thúc của engine.
	drain := newFlagTestHost(lifecyclePaused, false)
	drain.engine.running = true
	if err := drain.acquireExclusive("nhập"); err == nil {
		t.Fatal("giai đoạn xả dừng của engine phải từ chối tác vụ độc quyền")
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newFlagTestHost(c.lc, c.cocreating)
			h.exclusive = c.exclusive
			err := h.acquireExclusive("nhập")
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("phải cho qua, nhận %v", err)
				}
				if h.exclusive != "nhập" {
					t.Fatalf("sau khi cho qua phải ghi nhận chiếm dụng, nhận %q", h.exclusive)
				}
				h.releaseExclusive()
				if h.exclusive != "" {
					t.Fatalf("sau khi giải phóng chiếm dụng phải rỗng, nhận %q", h.exclusive)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("phải chứa %q, nhận %v", c.wantErr, err)
			}
			if !strings.Contains(err.Error(), "nhập") {
				t.Errorf("văn bản lỗi phải mang action %q, nhận %v", "nhập", err)
			}
		})
	}
}

// TestExclusiveBlocksCreationEntries bảo vệ #2: khi tác vụ độc quyền nền (nhập/phỏng viết) đang chạy,
// không chỉ tác vụ nền thứ hai bị chặn, mà cả cổng ghi sáng tác (Continue/Resume) và tác vụ nền mới cũng phải bị chặn,
// nếu không Continue sẽ để Arbiter đổi trạng thái trước khi engine bị gate chặn, còn trong lúc Resume/next engine có thể chạy trước.
func TestExclusiveBlocksCreationEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	h.exclusive = "nhập"
	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("trong lúc tác vụ độc quyền đang chạy thì ImportFrom phải bị từ chối")
	}
	if err := h.Continue("tiếp tục viết"); err == nil {
		t.Error("trong lúc tác vụ độc quyền đang chạy thì Continue phải bị từ chối (phải chặn trước khi Arbiter ra quyết định)")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("trong lúc tác vụ độc quyền đang chạy thì Resume phải bị từ chối")
	}
}

// TestStageCoCreate_OccupancyBlocksConcurrentEntries xác minh mọi cổng độc quyền trong cửa sổ đồng sáng tạo đều bị chặn:
// import/start/resume/continue trong thời gian cocreating đều phải bị từ chối, bù cho lỗ hổng ở pha paused chỉ kiểm ==running.
func TestStageCoCreate_OccupancyBlocksConcurrentEntries(t *testing.T) {
	h := newFlagTestHost(lifecycleIdle, false)
	if !h.PauseForCoCreate() {
		t.Fatal("vào giai đoạn đồng sáng tạo thất bại")
	}

	if _, err := h.ImportFrom(context.Background(), imp.Options{}); err == nil {
		t.Error("trong cửa sổ đồng sáng tạo thì ImportFrom phải bị từ chối")
	}
	if err := h.StartPrepared("viết một câu chuyện mới"); err == nil {
		t.Error("trong cửa sổ đồng sáng tạo thì StartPrepared phải bị từ chối")
	}
	if _, err := h.Resume(); err == nil {
		t.Error("trong cửa sổ đồng sáng tạo thì Resume phải bị từ chối")
	}
	if err := h.Continue("tiếp tục viết"); err == nil {
		t.Error("trong cửa sổ đồng sáng tạo thì Continue phải bị từ chối")
	}

	// Sau khi thoát đồng sáng tạo thì giải phóng chiếm dụng (ở đây đi qua Cancel; đường can thiệp Resume để kiểm chứng qua tích hợp)
	h.CancelCoCreate()
	if h.cocreating {
		t.Fatal("sau khi thoát, cờ chiếm dụng phải được giải phóng")
	}
}

func TestBuildStoryStateSummary_NilStore(t *testing.T) {
	if got := buildStoryStateSummary(nil); got != "" {
		t.Errorf("nil store phải trả về chuỗi rỗng, nhận %q", got)
	}
}

func TestBuildStoryStateSummary_Populated(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(100); err != nil {
		t.Fatal(err)
	}
	if err := st.Book.Save(domain.BookMetadata{Title: "Bài ca bóng", Synopsis: "Cậu thiếu niên truy tìm chiếc bóng đã mất."}); err != nil {
		t.Fatal(err)
	}
	p, _ := st.Progress.Load()
	p.CompletedChapters = []int{1, 2, 3}
	p.TotalWordCount = 12000
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "Nhân vật chính lên tới đỉnh cao",
		OpenThreads:     []string{"Huyết thù của sư môn chưa báo"},
		EstimatedScale:  "Dự kiến 4-6 quyển",
	}); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	for _, want := range []string{"Bài ca bóng", "đã hoàn thành 3 chương", "chương tiếp theo là chương 4", "Nhân vật chính lên tới đỉnh cao", "Huyết thù của sư môn chưa báo", "Dự kiến 4-6 quyển"} {
		if !strings.Contains(got, want) {
			t.Errorf("bản tóm tắt phải chứa %q, thực tế:\n%s", want, got)
		}
	}
}

func TestBuildStoryStateSummaryUsesDynamicPlanningWording(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(66); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Title: "Quyển một", Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, EstimatedChapters: 64},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	p.Layered = true
	if err := st.Progress.Save(p); err != nil {
		t.Fatal(err)
	}

	got := buildStoryStateSummary(st)
	if !strings.Contains(got, "hiện đã chi tiết hóa 2 chương (về sau lập kế hoạch động theo arc)") {
		t.Fatalf("sai cách diễn đạt tóm tắt lập kế hoạch động:\n%s", got)
	}
	if strings.Contains(got, "66") || strings.Contains(got, "lập kế hoạch 2 chương") {
		t.Fatalf("tóm tắt lập kế hoạch động không được ám chỉ tổng số chương cố định:\n%s", got)
	}
}
