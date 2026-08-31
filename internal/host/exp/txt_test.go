package exp

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestStripChapterTitleHeader(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		title string
		want  string
	}{
		{"plain body untouched", "Anh nhìn ra ngoài cửa sổ.", "Người về trong mưa đêm", "Anh nhìn ra ngoài cửa sổ."},
		{"strip h1 Vietnamese chapter title", "# Chương 1  Người về trong mưa đêm\n\nAnh nhìn ra ngoài cửa sổ.", "Người về trong mưa đêm", "Anh nhìn ra ngoài cửa sổ."},
		{"strip h2 with chapter token", "## Chương 2\n\nAnh nhìn ra ngoài cửa sổ.", "", "Anh nhìn ra ngoài cửa sổ."},
		{"keep body even if no header", "Câu đầu tiên.\nCâu thứ hai.", "", "Câu đầu tiên.\nCâu thứ hai."},
		{"do not strip non-chapter heading", "# Lời mở đầu\nAnh nhìn ra ngoài cửa sổ.", "Đời nổi bên làng", "# Lời mở đầu\nAnh nhìn ra ngoài cửa sổ."},
		{"single line header only", "# Chương 1", "", ""},
		// Writer có thể đặt riêng tiêu đề chương ở dòng đầu, trùng với tiêu đề exporter tạo, nên cần bóc ra.
		{"strip h1 matching chapter title", "# Đời nổi bên làng\n\nTrời chưa sáng.", "Đời nổi bên làng", "Trời chưa sáng."},
		// Dòng đầu là h1 nhưng chữ không bằng tiêu đề chương, nên xem là nội dung thân bài và giữ lại.
		{"keep h1 not matching title", "# Tiểu đề khác\nNội dung.", "Đời nổi bên làng", "# Tiểu đề khác\nNội dung."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripChapterTitleHeader(c.in, c.title)
			if got != c.want {
				t.Fatalf("stripChapterTitleHeader\nin   = %q\ntitle= %q\nwant = %q\ngot  = %q", c.in, c.title, c.want, got)
			}
		})
	}
}

func TestBuildTitleIndex(t *testing.T) {
	outline := []domain.OutlineEntry{
		{Chapter: 1, Title: "Người về trong mưa đêm"},
		{Chapter: 2, Title: ""}, // Tiêu đề rỗng phải bị lọc.
		{Chapter: 3, Title: "Rạng đông"},
	}
	idx := buildTitleIndex(outline)
	if got := idx[1]; got != "Người về trong mưa đêm" {
		t.Errorf("ch1 title: got %q want Người về trong mưa đêm", got)
	}
	if _, ok := idx[2]; ok {
		t.Errorf("ch2 should be absent (empty title)")
	}
	if got := idx[3]; got != "Rạng đông" {
		t.Errorf("ch3 title: got %q want Rạng đông", got)
	}
}

func TestBuildLocations(t *testing.T) {
	volumes := []domain.VolumeOutline{
		{Index: 1, Title: "Khởi nguyên", Arcs: []domain.ArcOutline{
			{Index: 1, Title: "Thiếu niên xuất hiện", Chapters: []domain.OutlineEntry{{}, {}}}, // 2 chương
			{Index: 2, Title: "Thử luyện tông môn", Chapters: []domain.OutlineEntry{{}}},       // 1 chương
		}},
		{Index: 2, Title: "Trỗi dậy", Arcs: []domain.ArcOutline{
			{Index: 1, Title: "Trận đầu", Chapters: []domain.OutlineEntry{{}}},
		}},
	}
	locs := buildLocations(volumes)

	// Chỉ kiểm tra quyển: hồi không còn đi vào location, nhưng tầng hồi vẫn tham gia cộng số chương toàn cục.
	if loc := locs[1]; !loc.IsFirstOfVolume || loc.VolumeIdx != 1 {
		t.Errorf("ch1 should be first of volume 1: %+v", loc)
	}
	if loc := locs[2]; loc.IsFirstOfVolume || loc.VolumeIdx != 1 {
		t.Errorf("ch2 should be volume 1, not first: %+v", loc)
	}
	// ch3 là chương đầu của hồi 2, nhưng vẫn thuộc quyển 1 nên không phải đầu quyển.
	if loc := locs[3]; loc.IsFirstOfVolume || loc.VolumeIdx != 1 {
		t.Errorf("ch3 (arc 2, same volume) should not be first of volume: %+v", loc)
	}
	if loc := locs[4]; !loc.IsFirstOfVolume || loc.VolumeIdx != 2 {
		t.Errorf("ch4 should start volume 2: %+v", loc)
	}
}

func TestRenderTXT_TitleAndChapter(t *testing.T) {
	got := renderTXT(
		"Đốm sáng",
		[]int{1, 2},
		chapterTitleIndex{1: "Người về trong mưa đêm", 2: "Rạng đông"},
		nil,
		map[int]string{
			1: "# Chương 1 Người về trong mưa đêm\n\nAnh nhìn ra ngoài cửa sổ.",
			2: "Cô đẩy cửa bước vào.",
		},
	)
	if !strings.HasPrefix(got, "《Đốm sáng》\n\n") {
		t.Errorf("missing book title at start:\n%s", got)
	}
	// premise không xuất hiện trong bản xuất: sau tên sách phải tới chương ngay, không chèn tiền tình tóm lược.
	if !strings.Contains(got, "Chương 1  Người về trong mưa đêm") {
		t.Errorf("missing ch1 header")
	}
	if !strings.Contains(got, "Anh nhìn ra ngoài cửa sổ.") {
		t.Errorf("missing ch1 body")
	}
	if strings.Contains(got, "# Chương 1") {
		t.Errorf("body markdown header not stripped:\n%s", got)
	}
	if !strings.Contains(got, "Chương 2  Rạng đông") {
		t.Errorf("missing ch2 header")
	}
}

func TestRenderTXT_EmptyBookTitleNoTitleLine(t *testing.T) {
	got := renderTXT(
		"",
		[]int{1},
		chapterTitleIndex{1: "Người về trong mưa đêm"},
		nil,
		map[int]string{1: "Nội dung."},
	)
	if strings.Contains(got, "《") {
		t.Errorf("should not contain book title brackets: %s", got)
	}
	if !strings.HasPrefix(got, "Chương 1  Người về trong mưa đêm") {
		t.Errorf("expect chapter header at very start: %s", got)
	}
}

// TestRenderTXT_LayeredVolume xác nhận dàn ý phân tầng chỉ chèn vạch phân quyển ở đầu quyển,
// không bao giờ chèn vạch phân hồi (issue #27: bố cục là "《Tên sách》→ vạch phân quyển → thân chương").
func TestRenderTXT_LayeredVolume(t *testing.T) {
	locs := map[int]chapterLocation{
		1: {VolumeIdx: 1, VolumeTitle: "Khởi nguyên", IsFirstOfVolume: true},
		2: {VolumeIdx: 1, VolumeTitle: "Khởi nguyên"},
	}
	got := renderTXT(
		"X", []int{1, 2},
		chapterTitleIndex{1: "A", 2: "B"},
		locs,
		map[int]string{1: "Nội dung một.", 2: "Nội dung hai."},
	)
	if !strings.Contains(got, "Quyển 1  Khởi nguyên") {
		t.Errorf("missing volume header: %s", got)
	}
	if strings.Contains(got, "Hồi") {
		t.Errorf("arc divider should never appear: %s", got)
	}
	// Tiêu đề quyển chỉ xuất hiện một lần trước chương đầu tiên.
	if strings.Count(got, "Quyển 1") != 1 {
		t.Errorf("volume header should appear exactly once: %s", got)
	}
}

func TestRenderTXT_ChapterWithoutTitleFallsBackToNumberOnly(t *testing.T) {
	got := renderTXT(
		"", []int{5},
		chapterTitleIndex{}, // Không có tiêu đề.
		nil,
		map[int]string{5: "Nội dung."},
	)
	if !strings.Contains(got, "Chương 5\n\n") {
		t.Errorf("expect chapter-number-only fallback header: %s", got)
	}
}
