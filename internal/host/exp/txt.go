package exp

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// chapterTitleIndex tra tiêu đề theo số chương; trả về chuỗi rỗng khi thiếu.
type chapterTitleIndex map[int]string

func buildTitleIndex(outline []domain.OutlineEntry) chapterTitleIndex {
	idx := make(chapterTitleIndex, len(outline))
	for _, e := range outline {
		if e.Title != "" {
			idx[e.Chapter] = e.Title
		}
	}
	return idx
}

// chapterLocation mô tả chương thuộc vị trí nào trong dàn ý phân tầng. Chỉ giữ thông tin quyển
// cần cho bố cục xuất bản; hồi không xuất hiện trong bản xuất vì quá chi tiết với độc giả.
type chapterLocation struct {
	VolumeIdx       int
	VolumeTitle     string
	IsFirstOfVolume bool
}

// buildLocations dựng {chapter -> location} theo thứ tự chương toàn cục của dàn ý phân tầng.
// Số chương được tái tạo theo cùng quy tắc với FlattenOutline (tăng dần qua từng hồi trong từng quyển)
// để khớp với Progress.CompletedChapters. Vẫn phải duyệt tầng hồi để tính số chương toàn cục,
// nhưng không ghi hồi vào location; bản xuất chỉ chèn vạch phân quyển ở đầu quyển.
func buildLocations(volumes []domain.VolumeOutline) map[int]chapterLocation {
	if len(volumes) == 0 {
		return nil
	}
	locs := make(map[int]chapterLocation)
	ch := 0
	for _, v := range volumes {
		firstOfVol := true
		for _, a := range v.Arcs {
			for range a.Chapters {
				ch++
				locs[ch] = chapterLocation{
					VolumeIdx:       v.Index,
					VolumeTitle:     v.Title,
					IsFirstOfVolume: firstOfVol,
				}
				firstOfVol = false
			}
		}
	}
	return locs
}

// chapterHeaderRe khớp dòng tiêu đề Markdown đầu tiên có số chương. Nhánh tiếng Trung được giữ
// chỉ để bóc tiêu đề trùng trong bản thảo cũ đã tạo trước khi cắt sang tiếng Việt.
var chapterHeaderRe = regexp.MustCompile(`^#+\s+(?:第.+?章|(?:Chương|Chuong)\s+\d+)`)

// atxTitleRe trích phần chữ của tiêu đề ATX (ví dụ: # Tiêu đề).
var atxTitleRe = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)

// stripChapterTitleHeader bóc dòng đầu nếu đó là tiêu đề chương sẽ bị trùng với tiêu đề do exporter tạo.
// Hai trường hợp: (1) tiêu đề có số chương (kể cả mẫu tiếng Trung cũ để tương thích bản thảo đã lưu);
// (2) tiêu đề Markdown có phần chữ đúng bằng tiêu đề chương, ví dụ "# Đời nổi bên làng".
// Các h1 khác, như "# Lời mở đầu", được xem là nội dung thân bài và giữ nguyên.
// Bên gọi đã TrimSpace trước, nên dòng trống đầu không được xét ở đây.
func stripChapterTitleHeader(content, title string) string {
	first, rest, hasNewline := strings.Cut(content, "\n")
	if !isChapterTitleLine(first, title) {
		return content
	}
	if !hasNewline {
		return ""
	}
	return strings.TrimLeft(rest, "\n")
}

func isChapterTitleLine(line, title string) bool {
	if chapterHeaderRe.MatchString(line) {
		return true
	}
	if title = strings.TrimSpace(title); title == "" {
		return false
	}
	m := atxTitleRe.FindStringSubmatch(line)
	return len(m) == 2 && strings.TrimSpace(m[1]) == title
}

// renderTXT ghép văn bản cuối cùng.
//
// Thứ tự chương do chapters quyết định (bên gọi đã sắp tăng dần và khử trùng lặp). bodies/titleIdx/locations
// đều xử lý theo hướng "thiếu thì hạ cấp": thiếu tiêu đề chỉ xuất "Chương N"; thiếu vị trí phân tầng thì xem như dàn ý phẳng.
func renderTXT(
	novelName string,
	chapters []int,
	titleIdx chapterTitleIndex,
	locations map[int]chapterLocation,
	bodies map[int]string,
) string {
	var b strings.Builder

	if name := strings.TrimSpace(novelName); name != "" {
		b.WriteString("《")
		b.WriteString(name)
		b.WriteString("》\n\n")
	}

	useLayered := len(locations) > 0

	for i, ch := range chapters {
		if useLayered {
			if loc, ok := locations[ch]; ok && loc.IsFirstOfVolume {
				b.WriteString("\n═══════════════════════════════════════════\n")
				fmt.Fprintf(&b, "           Quyển %d  %s\n", loc.VolumeIdx, strings.TrimSpace(loc.VolumeTitle))
				b.WriteString("═══════════════════════════════════════════\n\n")
			}
		}

		title := strings.TrimSpace(titleIdx[ch])
		if title != "" {
			fmt.Fprintf(&b, "Chương %d  %s\n\n", ch, title)
		} else {
			fmt.Fprintf(&b, "Chương %d\n\n", ch)
		}

		body := stripChapterTitleHeader(strings.TrimSpace(bodies[ch]), title)
		b.WriteString(body)
		b.WriteString("\n")
		if i < len(chapters)-1 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}
