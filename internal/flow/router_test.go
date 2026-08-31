package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

func TestLoadState_TraVeLoiDocTienDo(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(st); err == nil {
		t.Fatal("Progress hỏng phải ngăn định tuyến")
	}
}

func TestLoadState_ChiUuTienPhanHoiSuaDoiBenNgoai(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	progress := &domain.Progress{
		Phase: domain.PhaseWriting, Flow: domain.FlowWriting,
		TotalChapters: 10, CompletedChapters: []int{1},
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatalf("Lưu progress: %v", err)
	}
	if err := st.Outline.AppendOutlineFeedback(storepkg.ChapterFeedback{
		Chapter: 1, Deviation: "Không có sai lệch rõ ràng", Suggestion: "Tiếp tục phát triển ở chương sau",
	}); err != nil {
		t.Fatalf("Ghi nhận phản hồi thông thường: %v", err)
	}

	state, err := LoadState(st)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.ImmediateFeedbackCount != 0 {
		t.Fatalf("Phản hồi writer thông thường không được làm gián đoạn quá trình viết: %+v", state)
	}
	if got := Route(state); got == nil || got.Agent != "writer" {
		t.Fatalf("Phản hồi writer thông thường phải tiếp tục quá trình viết, nhận được %+v", got)
	}

	if err := st.Outline.AppendOutlineFeedback(storepkg.ChapterFeedback{
		Chapter: 1, StoryChanged: true, ChangeSummary: "Người dùng đã viết lại kết thúc chương này",
	}); err != nil {
		t.Fatalf("Ghi nhận phản hồi sửa đổi bên ngoài: %v", err)
	}
	state, err = LoadState(st)
	if err != nil {
		t.Fatalf("LoadState sau sửa đổi bên ngoài: %v", err)
	}
	if state.ImmediateFeedbackCount != 1 {
		t.Fatalf("Sửa đổi bên ngoài phải làm gián đoạn quá trình viết: %+v", state)
	}
	if got := Route(state); got == nil || !strings.HasPrefix(got.Agent, "architect_") {
		t.Fatalf("Sửa đổi bên ngoài phải giao cho architect, nhận được %+v", got)
	}
}

// Hàm trợ giúp tạo Progress ở giai đoạn Writing, chế độ phân tầng.
func writingProgress(completed []int, flow domain.FlowState) *domain.Progress {
	return &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              flow,
		Layered:           true,
		CompletedChapters: completed,
	}
}

func TestRoute_TienDoNil(t *testing.T) {
	if got := Route(State{Progress: nil}); got != nil {
		t.Fatalf("Mong đợi nil khi Progress là nil, nhận được %+v", got)
	}
}

func TestRoute_PhaseCompleteTraVeNil(t *testing.T) {
	s := State{Progress: &domain.Progress{Phase: domain.PhaseComplete}}
	if got := Route(s); got != nil {
		t.Fatalf("Mong đợi nil tại PhaseComplete, nhận được %+v", got)
	}
}

func TestRoute_CacPhaseKhongVietUyQuyenChoLLM(t *testing.T) {
	for _, phase := range []domain.Phase{domain.PhaseInit, domain.PhasePremise, domain.PhaseOutline} {
		s := State{Progress: &domain.Progress{Phase: phase}, FoundationMissing: []string{"premise"}}
		if got := Route(s); got != nil {
			t.Fatalf("Phase %s phải trả về nil, nhận được %+v", phase, got)
		}
	}
}

func TestRoute_UuTienPendingRewrites(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowRewriting)
	p.PendingRewrites = []int{3, 5}
	got := Route(State{Progress: p})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("Mong đợi writer cho PendingRewrites, nhận được %+v", got)
	}
	if got.Task != "Viết lại Chương 3" {
		t.Errorf("Mong đợi 'Viết lại Chương 3', nhận được %q", got.Task)
	}
	if got.Chapter != 3 {
		t.Errorf("Mong đợi Chapter=3, nhận được %d", got.Chapter)
	}
}

func TestRoute_PendingPolishingDungDongTuBienTap(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowPolishing)
	p.PendingRewrites = []int{2}
	got := Route(State{Progress: p})
	if got == nil || got.Task != "Biên tập Chương 2" {
		t.Fatalf("Mong đợi động từ biên tập, nhận được %+v", got)
	}
}

func TestRoute_FlowReviewingUyQuyenChoLLM(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowReviewing)
	if got := Route(State{Progress: p}); got != nil {
		t.Fatalf("Mong đợi nil trong Flow reviewing, nhận được %+v", got)
	}
}

func TestRoute_FlowSteeringUyQuyenChoLLM(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowSteering)
	if got := Route(State{Progress: p}); got != nil {
		t.Fatalf("Mong đợi nil trong Flow steering, nhận được %+v", got)
	}
}

func TestRoute_CuoiCungCanDanhGia(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:     true,
			Volume:       1,
			Arc:          2,
			StartChapter: 11,
			EndChapter:   22,
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" {
		t.Fatalf("Mong đợi editor cho đánh giá cung, nhận được %+v", got)
	}
	if got.Reason != "Chưa hoàn tất đánh giá cuối cung" {
		t.Errorf("Reason không khớp: %q", got.Reason)
	}
	if !strings.Contains(got.Task, "Chương 11-22") || !strings.Contains(got.Task, "chapter=22") {
		t.Fatalf("Task đánh giá cung phải mang đúng khoảng chương và điểm cuối: %q", got.Task)
	}
}

func TestRoute_CuoiCungDaDanhGiaCanTomTat(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd: true,
			Volume:   1,
			Arc:      2,
		},
		HasArcReview: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "editor" || got.Reason != "Chưa hoàn tất bản tóm tắt cung" {
		t.Fatalf("Mong đợi lời gọi editor tóm tắt cung, nhận được %+v", got)
	}
}

func TestRoute_CuoiQuyenCanTomTatQuyen(t *testing.T) {
	p := writingProgress([]int{20}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 20,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:    true,
			IsVolumeEnd: true,
			Volume:      1,
			Arc:         3,
		},
		HasArcReview:  true,
		HasArcSummary: true,
	}
	got := Route(s)
	if got == nil || got.Reason != "Chưa hoàn tất bản tóm tắt quyển" {
		t.Fatalf("Mong đợi yêu cầu tóm tắt quyển, nhận được %+v", got)
	}
}

func TestRoute_CanMoRongCung(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			Volume:         1,
			Arc:            2,
			NextVolume:     1,
			NextArc:        3,
			NeedsExpansion: true,
		},
		HasArcReview:  true,
		HasArcSummary: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" {
		t.Fatalf("Mong đợi architect_long cho mở rộng cung, nhận được %+v", got)
	}
	if got.Reason != "Khung cung tiếp theo đang chờ mở rộng" {
		t.Errorf("Reason không khớp: %q", got.Reason)
	}
}

func TestRoute_CanQuyenMoi(t *testing.T) {
	p := writingProgress([]int{30}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 30,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			IsVolumeEnd:    true,
			Volume:         2,
			Arc:            4,
			NeedsNewVolume: true,
		},
		HasArcReview:     true,
		HasArcSummary:    true,
		HasVolumeSummary: true,
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" || got.Reason != "Cuối quyển cần quyết định thêm quyển mới, tạo quyển kết thúc hoặc kết thúc toàn bộ tác phẩm" {
		t.Fatalf("Mong đợi điều phối append_volume/complete_book, nhận được %+v", got)
	}
}

func TestRoute_TiepTucBinhThuong(t *testing.T) {
	p := writingProgress([]int{1, 2, 3}, domain.FlowWriting)
	p.TotalChapters = 20
	got := Route(State{Progress: p, LastCompleted: 3})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("Mong đợi writer cho chương tiếp theo, nhận được %+v", got)
	}
	if got.Task != "Viết Chương 4" {
		t.Errorf("Mong đợi 'Viết Chương 4', nhận được %q", got.Task)
	}
	if got.Chapter != 4 {
		t.Errorf("Mong đợi Chapter=4, nhận được %d", got.Chapter)
	}
}

func TestRoute_SuaDoiBenNgoaiGiaoArchitectTruocWriter(t *testing.T) {
	p := writingProgress([]int{1, 2, 3}, domain.FlowWriting)
	p.TotalChapters = 20
	got := Route(State{Progress: p, LastCompleted: 3, PlanningTier: domain.PlanningTierShort, ImmediateFeedbackCount: 2})
	if got == nil || got.Agent != "architect_short" || !strings.Contains(got.Reason, "2 sửa đổi") {
		t.Fatalf("Mong đợi architect xử lý phản hồi, nhận được %+v", got)
	}
	for _, want := range []string{"novel_context", "writer_feedback", "revise_outline", "resolve_outline_feedback", "foundation_status"} {
		if !strings.Contains(got.Task, want) {
			t.Errorf("Task sửa đổi bên ngoài thiếu %q: %s", want, got.Task)
		}
	}
	if !strings.Contains(got.Task, "không xử lý foundation_status") {
		t.Errorf("Task sửa đổi bên ngoài phải cấm foundation_status: %s", got.Task)
	}
}

func TestRoute_LamMoiTongHopUuTienTruocSuaDoiBenNgoai(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowWriting)
	got := Route(State{
		Progress: p, ImmediateFeedbackCount: 1,
		AggregateRefresh: &AggregateRefresh{
			Kind: AggregateArcSummary, Volume: 1, Arc: 1, StartChapter: 1, EndChapter: 2,
		},
	})
	if got == nil || got.Agent != "editor" || !strings.Contains(got.Task, "save_arc_summary") {
		t.Fatalf("Mong đợi editor làm mới tổng hợp, nhận được %+v", got)
	}
}

func TestRoute_DaiCuongKhongPhanTangHetGiaoArchitect(t *testing.T) {
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		CompletedChapters: []int{1, 2, 3},
		TotalChapters:     3,
	}
	got := Route(State{Progress: p, LastCompleted: 3, PlanningTier: domain.PlanningTierShort})
	if got == nil || got.Agent != "architect_short" {
		t.Fatalf("Mong đợi architect_short khi đại cương đã dùng hết, nhận được %+v", got)
	}
	for _, want := range []string{"complete_book", "revise_outline", "Chương 4"} {
		if !strings.Contains(got.Task, want) {
			t.Errorf("Task thiếu %q: %s", want, got.Task)
		}
	}
}

func TestRoute_CuoiCungKhongPhanTangBoQuaRanhGioi(t *testing.T) {
	// Chế độ không phân tầng không đi qua nhánh cuối cung dù ArcBoundary khác nil.
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		CompletedChapters: []int{10},
		TotalChapters:     20,
	}
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary:   &storepkg.ArcBoundary{IsArcEnd: true, Volume: 1, Arc: 2},
	}
	got := Route(s)
	if got == nil || got.Agent != "writer" {
		t.Fatalf("Không phân tầng phải đi tiếp xuống writer, nhận được %+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Bổ sung trong giai đoạn lập kế hoạch: mục thiếu + planner đã xác định → tiếp tục giao cùng planner.
func TestRoute_BoSungLapKeHoachGiaoCungPlanner(t *testing.T) {
	base := State{Progress: &domain.Progress{Phase: domain.PhaseOutline}, FoundationMissing: []string{"characters", "world_rules"}}
	short := base
	short.PlanningTier = domain.PlanningTierShort
	if got := Route(short); got == nil || got.Agent != "architect_short" {
		t.Fatalf("Cấp short phải tiếp tục giao architect_short, nhận được %+v", got)
	}
	long := base
	long.PlanningTier = domain.PlanningTierLong
	got := Route(long)
	if got == nil || got.Agent != "architect_long" {
		t.Fatalf("Cấp long phải tiếp tục giao architect_long, nhận được %+v", got)
	}
	for _, want := range []string{"Bổ sung các mục thiếu", "characters", "world_rules", "save_foundation"} {
		if !contains(got.Task, want) {
			t.Errorf("Nhiệm vụ bổ sung thiếu %q: %s", want, got.Task)
		}
	}
	bookMissing := base
	bookMissing.PlanningTier = domain.PlanningTierLong
	bookMissing.FoundationMissing = []string{"book"}
	if got := Route(bookMissing); got == nil || !contains(got.Task, "save_book") {
		t.Fatalf("Khi thiếu book phải chỉ dẫn save_book, nhận được %+v", got)
	}
	unknown := base
	if got := Route(unknown); got != nil {
		t.Fatalf("Cấp lập kế hoạch không xác định phải giao LLM phân xử, nhận được %+v", got)
	}
	done := base
	done.PlanningTier = domain.PlanningTierLong
	done.FoundationMissing = nil
	if got := Route(done); got != nil {
		t.Fatalf("Khi đủ mục không được giao bổ sung, nhận được %+v", got)
	}
}
