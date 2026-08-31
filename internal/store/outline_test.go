package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func setupLayered(t *testing.T, volumes []domain.VolumeOutline) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}
	return s
}

func TestSaveLayeredOutlineRebuildsFlatProjection(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Tiêu đề cũ"}}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}

	volumes := []domain.VolumeOutline{{
		Index: 1, Title: "Quyển một", Theme: "Khởi hành",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "Cung đầu", Goal: "Bước vào thế giới mới",
			Chapters: []domain.OutlineEntry{
				{Chapter: 99, Title: "Mới một", CoreEvent: "Khởi hành", Hook: "Phát hiện"},
				{Chapter: 100, Title: "Mới hai", CoreEvent: "Đi sâu", Hook: "Khủng hoảng"},
			},
		}},
	}}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}

	flat, err := s.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	if len(flat) != 2 || flat[0].Chapter != 1 || flat[0].Title != "Mới một" || flat[1].Chapter != 2 || flat[1].Title != "Mới hai" {
		t.Fatalf("bản chiếu phẳng = %+v", flat)
	}
	markdown, err := os.ReadFile(filepath.Join(s.Dir(), "outline.md"))
	if err != nil {
		t.Fatalf("đọc outline.md: %v", err)
	}
	if !strings.Contains(string(markdown), "Mới một") || strings.Contains(string(markdown), "Tiêu đề cũ") {
		t.Fatalf("outline.md chưa được dựng lại đồng bộ:\n%s", markdown)
	}
}

func TestCheckArcBoundaryNeedsNewVolume(t *testing.T) {
	// Chỉ có 1 quyển 1 cung 1 chương, và không phải Final → phải kích hoạt NeedsNewVolume
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Quyển một", Theme: "Khởi đầu",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "Cung đầu", Goal: "Mục tiêu",
			Chapters: []domain.OutlineEntry{{Title: "Chương một", CoreEvent: "Mở đầu", Hook: "Tiếp tục"}},
		}},
	}})

	b, err := s.Outline.CheckArcBoundary(1) // Chương 1 = chương cuối của cung/quyển
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if b == nil {
		t.Fatal("mong đợi boundary, nhưng nhận nil")
	}
	if !b.IsArcEnd || !b.IsVolumeEnd {
		t.Fatalf("mong đợi kết thúc cung+quyển, nhưng nhận arc=%v vol=%v", b.IsArcEnd, b.IsVolumeEnd)
	}
	if !b.NeedsNewVolume {
		t.Fatal("mong đợi NeedsNewVolume=true")
	}
	if b.NextVolume != 0 || b.NextArc != 0 {
		t.Fatalf("mong đợi không có phần tiếp theo, nhưng nhận vol=%d arc=%d", b.NextVolume, b.NextArc)
	}
}

func TestCheckArcBoundaryLastVolumeRequiresDecision(t *testing.T) {
	// Chương cuối của quyển duy nhất → kích hoạt NeedsNewVolume, để Router cho kiến trúc sư chọn một trong hai:
	// append_volume viết tiếp / complete_book khép lại.
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Quyển duy nhất", Theme: "Chủ đề",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "Cung duy nhất", Goal: "Khép lại",
			Chapters: []domain.OutlineEntry{{Title: "Chương kết", CoreEvent: "Kết cục", Hook: "Không"}},
		}},
	}})

	b, err := s.Outline.CheckArcBoundary(1)
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if !b.NeedsNewVolume {
		t.Fatal("mong đợi NeedsNewVolume=true ở chương mở rộng cuối cùng")
	}
	if b.HasNextArc() {
		t.Fatal("mong đợi không có cung tiếp theo")
	}
}

func TestCheckArcBoundaryNextArcInSameVolume(t *testing.T) {
	// 2 cung: cung 1 kết thúc phải chỉ sang cung 2, không kích hoạt NeedsNewVolume
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Quyển một", Theme: "Khởi đầu",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "Cung đầu", Goal: "Mục tiêu", Chapters: []domain.OutlineEntry{{Title: "Chương một", CoreEvent: "Sự kiện", Hook: "Móc"}}},
			{Index: 2, Title: "Cung hai", Goal: "Mục tiêu 2", EstimatedChapters: 10},
		},
	}})

	b, err := s.Outline.CheckArcBoundary(1)
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if !b.IsArcEnd {
		t.Fatal("mong đợi kết thúc cung")
	}
	if b.IsVolumeEnd {
		t.Fatal("mong đợi không kết thúc quyển (vì còn cung thứ hai)")
	}
	if b.NeedsNewVolume {
		t.Fatal("mong đợi NeedsNewVolume=false")
	}
	if b.NextVolume != 1 || b.NextArc != 2 {
		t.Fatalf("mong đợi vol=1 arc=2 tiếp theo, nhưng nhận vol=%d arc=%d", b.NextVolume, b.NextArc)
	}
	if !b.NeedsExpansion {
		t.Fatal("mong đợi NeedsExpansion=true cho cung khung sườn")
	}
}

func TestCheckArcBoundaryReportsExactArcSpan(t *testing.T) {
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, Chapters: []domain.OutlineEntry{{Title: "Ba"}, {Title: "Bốn"}, {Title: "Năm"}}},
		},
	}})

	b, err := s.Outline.CheckArcBoundary(5)
	if err != nil {
		t.Fatalf("CheckArcBoundary: %v", err)
	}
	if b == nil || !b.IsArcEnd || b.StartChapter != 3 || b.EndChapter != 5 {
		t.Fatalf("khoảng cung không như mong đợi: %+v", b)
	}
}

func TestExpandArcCalibratesUnwrittenPlan(t *testing.T) {
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Quyển một", Theme: "Khởi đầu",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "Cung cũ", Goal: "Gây ra sự chia rẽ ngoài kế hoạch", Chapters: []domain.OutlineEntry{{Title: "Rạn nứt", CoreEvent: "Đồng đội rời nhóm", Hook: "Hướng đi không rõ"}}},
			{Index: 2, Title: "Khung sườn gốc", Goal: "Đi cùng nhau theo kế hoạch ban đầu", EstimatedChapters: 8},
		},
	}})

	expansion := domain.ArcExpansion{
		Title: "Tách hướng truy tìm",
		Goal:  "Thừa nhận rằng sự chia rẽ đã xảy ra, để hai tuyến hành động riêng biệt cùng tiến gần một sự thật",
		Chapters: []domain.OutlineEntry{
			{Title: "Hai tấm bản đồ", CoreEvent: "Hai nhóm xuất phát từ những manh mối khác nhau", Hook: "Manh mối chỉ tới cùng một địa điểm"},
			{Title: "Dội vang sau tường", CoreEvent: "Hai bên tác động lẫn nhau từ xa qua lựa chọn của đối phương", Hook: "Cái giá của cuộc tái ngộ hiện ra"},
		},
	}
	if err := s.ExpandArc(1, 2, expansion); err != nil {
		t.Fatalf("ExpandArc: %v", err)
	}

	volumes, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatalf("LoadLayeredOutline: %v", err)
	}
	got := volumes[0].Arcs[1]
	if got.Title != expansion.Title || got.Goal != expansion.Goal {
		t.Fatalf("mong đợi tiêu đề/mục tiêu đã hiệu chỉnh, nhưng nhận title=%q goal=%q", got.Title, got.Goal)
	}
	if got.EstimatedChapters != 0 || len(got.Chapters) != 2 {
		t.Fatalf("mong đợi cung đã mở rộng, nhưng nhận estimated=%d chapters=%d", got.EstimatedChapters, len(got.Chapters))
	}
	flat, err := s.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	if len(flat) != 3 || flat[1].Chapter != 2 || flat[2].Chapter != 3 {
		t.Fatalf("mong đợi bản phác thảo phẳng liên tục, nhưng nhận %+v", flat)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if progress.TotalChapters != 3 {
		t.Fatalf("mong đợi tổng số chương là 3, nhưng nhận %d", progress.TotalChapters)
	}

	if err := s.ExpandArc(1, 2, expansion); err != nil {
		t.Fatalf("cùng một expansion phải là idempotent: %v", err)
	}
	// Mô phỏng lần trước chỉ ghi xong layered JSON, còn outline phẳng dẫn xuất và Progress chưa được bổ sung.
	if err := os.Remove(filepath.Join(s.Dir(), "outline.json")); err != nil {
		t.Fatalf("xóa outline phẳng: %v", err)
	}
	if err := s.Progress.SetTotalChapters(1); err != nil {
		t.Fatalf("đặt total cũ: %v", err)
	}
	if err := s.ExpandArc(1, 2, expansion); err != nil {
		t.Fatalf("lần thử lại idempotent phải sửa trạng thái dẫn xuất: %v", err)
	}
	flat, err = s.Outline.LoadOutline()
	if err != nil || len(flat) != 3 {
		t.Fatalf("outline phẳng chưa được sửa: len=%d err=%v", len(flat), err)
	}
	progress, err = s.Progress.Load()
	if err != nil || progress.TotalChapters != 3 {
		t.Fatalf("tổng progress chưa được sửa: progress=%+v err=%v", progress, err)
	}
	changed := expansion
	changed.Goal = "Viết lại sau khi cung đã mở rộng"
	if err := s.ExpandArc(1, 2, changed); err == nil {
		t.Fatal("mong đợi expansion khác phải từ chối ghi đè cung đã mở rộng")
	}
}

func TestAppendVolumeValidation(t *testing.T) {
	s := setupLayered(t, []domain.VolumeOutline{{
		Index: 1, Title: "Quyển một", Theme: "Khởi đầu",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "Cung đầu", Goal: "Mục tiêu",
			Chapters: []domain.OutlineEntry{{Title: "Chương", CoreEvent: "Sự kiện", Hook: "Móc"}},
		}},
	}})

	validVol := domain.VolumeOutline{
		Index: 2, Title: "Quyển hai", Theme: "Nâng cấp",
		Arcs: []domain.ArcOutline{{
			Index: 1, Title: "Cung một", Goal: "Mục tiêu",
			Chapters: []domain.OutlineEntry{{Title: "Chương mới", CoreEvent: "Tiến triển", Hook: "Móc"}},
		}},
	}

	// Gắn thêm hợp lệ phải thành công
	if err := s.AppendVolume(validVol); err != nil {
		t.Fatalf("AppendVolume hợp lệ: %v", err)
	}

	// Index không tăng dần → thất bại
	if err := s.AppendVolume(domain.VolumeOutline{
		Index: 1, Title: "Trùng", Theme: "x",
		Arcs: []domain.ArcOutline{{Index: 1, Title: "Cung", Goal: "g", Chapters: []domain.OutlineEntry{{Title: "ch", CoreEvent: "e", Hook: "h"}}}},
	}); err == nil {
		t.Fatal("mong đợi lỗi cho chỉ số không tăng dần")
	}

	// Không có cung → thất bại
	if err := s.AppendVolume(domain.VolumeOutline{Index: 3, Title: "Rỗng", Theme: "x"}); err == nil {
		t.Fatal("mong đợi lỗi cho quyển không có cung")
	}

	// Cung đầu không có chương → thất bại
	if err := s.AppendVolume(domain.VolumeOutline{
		Index: 3, Title: "Khung sườn", Theme: "x",
		Arcs: []domain.ArcOutline{{Index: 1, Title: "Cung", Goal: "g", EstimatedChapters: 10}},
	}); err == nil {
		t.Fatal("mong đợi lỗi cho cung đầu không có chương")
	}
}

// Ghi chú: ngữ nghĩa trước đây dùng quyển Final để từ chối append đã được đẩy xuống lớp save_foundation
// (Phase=Complete từ chối),
// xem save_foundation_test.go::TestSaveFoundationAppendVolumeRejectsAfterComplete.
// Lớp store chỉ giữ lại kiểm tra cấu trúc (Index tăng dần / cung đầu có chương, v.v.).

func TestSaveAndLoadCompass(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// direction rỗng phải thất bại
	if err := s.Outline.SaveCompass(domain.StoryCompass{EstimatedScale: "3 quyển"}); err == nil {
		t.Fatal("mong đợi lỗi cho ending_direction rỗng")
	}

	// Lưu bình thường
	compass := domain.StoryCompass{
		EndingDirection: "Nhân vật chính đối diện lựa chọn cuối cùng",
		OpenThreads:     []string{"Manh mối A", "Quan hệ B"},
		EstimatedScale:  "Dự kiến 4-6 quyển",
		LastUpdated:     12,
	}
	if err := s.Outline.SaveCompass(compass); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}

	loaded, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if loaded == nil {
		t.Fatal("mong đợi compass, nhưng nhận nil")
	}
	if loaded.EndingDirection != "Nhân vật chính đối diện lựa chọn cuối cùng" {
		t.Fatalf("mong đợi direction %q, nhưng nhận %q", "Nhân vật chính đối diện lựa chọn cuối cùng", loaded.EndingDirection)
	}
	if len(loaded.OpenThreads) != 2 {
		t.Fatalf("mong đợi 2 luồng mở, nhưng nhận %d", len(loaded.OpenThreads))
	}
}

// TestOutlineFeedbackPool: vòng khép kín feedback phác thảo: commit ghi xuống đĩa → có thể đọc qua khởi động lại → thao tác cấu trúc tiêu thụ và xóa sạch.
func TestOutlineFeedbackPool(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Outline.AppendOutlineFeedback(ChapterFeedback{Chapter: 3, Deviation: "Tuyến phụ phình to", Suggestion: "Cung sau thu tuyến"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Outline.AppendOutlineFeedback(ChapterFeedback{Chapter: 3, Deviation: "Tuyến phụ phình to", Suggestion: "Cung sau thu tuyến"}); err != nil {
		t.Fatalf("append duplicate: %v", err)
	}
	if err := s.Outline.AppendOutlineFeedback(ChapterFeedback{Chapter: 4, Suggestion: "Phản diện xuất hiện sớm"}); err != nil {
		t.Fatalf("append2: %v", err)
	}

	// Đọc được qua khởi động lại (thực thể Store mới) — không phải trạng thái trong bộ nhớ
	s2 := NewStore(dir)
	fbs, err := s2.Outline.LoadPendingOutlineFeedback()
	if err != nil {
		t.Fatalf("load feedback: %v", err)
	}
	if len(fbs) != 2 || fbs[0].Chapter != 3 || fbs[1].Suggestion != "Phản diện xuất hiện sớm" {
		t.Fatalf("đọc qua khởi động lại thất bại: %+v", fbs)
	}
	for _, fb := range fbs {
		if fb.At == "" {
			t.Fatal("At phải được bổ sung tự động")
		}
	}

	if err := s2.Outline.ClearOutlineFeedback(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if left, err := s2.Outline.LoadPendingOutlineFeedback(); err != nil || len(left) != 0 {
		t.Fatalf("sau khi tiêu thụ phải rỗng: %+v", left)
	}
	// Xóa lặp lại phải idempotent
	if err := s2.Outline.ClearOutlineFeedback(); err != nil {
		t.Fatalf("clear idempotent: %v", err)
	}
}

func TestOutlineFeedbackCorruptionIsNotSilentlyConsumed(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	path := filepath.Join(dir, outlineFeedbackFile)
	if err := os.WriteFile(path, []byte("{\n"), 0o644); err != nil {
		t.Fatalf("ghi feedback hỏng: %v", err)
	}
	if _, err := s.Outline.LoadPendingOutlineFeedback(); err == nil {
		t.Fatal("feedback hỏng phải trả về lỗi đọc")
	}
	if err := s.Outline.ClearOutlineFeedback(); err == nil {
		t.Fatal("feedback hỏng không được xóa như thể đã được tiêu thụ")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("feedback hỏng phải được giữ lại để chẩn đoán: %v", err)
	}
}
