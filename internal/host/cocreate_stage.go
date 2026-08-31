package host

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

// buildStoryStateSummary ghép một bản tóm tắt ngắn gọn về trạng thái câu chuyện hiện tại, để trợ lý cộng tác theo giai đoạn hiểu "đã viết tới đâu".
// Tái sử dụng các điểm truy cập store, chỉ lấy các факт cấp cao cần cho hướng lập kế hoạch (tiến độ / la bàn / quyển gần nhất / nhân vật chính / chi tiết gài trước đang hoạt động);
// không kéo phần nội dung chính, không nạp toàn bộ JSON novel_context — cộng tác là đối thoại, cần cái nhìn tổng quan dễ đọc, không phải ngữ cảnh biên soạn.
// Mục nào thiếu thì bỏ qua (best-effort), trả về chuỗi rỗng nếu hiện chưa có tiến độ khả dụng.
func buildStoryStateSummary(s *store.Store) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	var warnings []string
	warn := func(scope string, err error) {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s đọc thất bại: %v", scope, err))
		}
	}

	if book, err := s.Book.Load(); book != nil {
		fmt.Fprintf(&b, "- Tên sách：《%s》\n", book.Title)
	} else {
		warn("book", err)
	}

	if progress, err := s.Progress.Load(); progress != nil {
		fmt.Fprintf(&b, "- Tiến độ: đã hoàn thành %d chương", len(progress.CompletedChapters))
		if progress.Layered {
			outline, outlineErr := s.Outline.LoadOutline()
			if outlineErr != nil {
				warn("outline", outlineErr)
			} else if len(outline) > 0 {
				fmt.Fprintf(&b, " / hiện đã chi tiết hóa %d chương (về sau lập kế hoạch động theo arc)", len(outline))
			}
		} else if progress.TotalChapters > 0 {
			fmt.Fprintf(&b, " / kế hoạch %d chương", progress.TotalChapters)
		}
		fmt.Fprintf(&b, "，khoảng %d chữ, chương tiếp theo là chương %d\n", progress.TotalWordCount, progress.NextChapter())
		if progress.Layered && progress.CurrentVolume > 0 {
			fmt.Fprintf(&b, "- Vị trí hiện tại: quyển %d, arc %d\n", progress.CurrentVolume, progress.CurrentArc)
		}
	} else {
		warn("progress", err)
	}

	if compass, err := s.Outline.LoadCompass(); compass != nil {
		if dir := strings.TrimSpace(compass.EndingDirection); dir != "" {
			fmt.Fprintf(&b, "- Hướng kết cục: %s\n", dir)
		}
		if compass.EstimatedScale != "" {
			fmt.Fprintf(&b, "- Quy mô ước tính: %s\n", compass.EstimatedScale)
		}
		if len(compass.OpenThreads) > 0 {
			fmt.Fprintf(&b, "- Tuyến dài đang hoạt động: %s\n", strings.Join(compass.OpenThreads, "；"))
		}
	} else {
		warn("story_compass", err)
	}

	// Tóm tắt quyển gần nhất, để trợ lý biết câu chuyện vừa đi tới đâu
	if vols, err := s.Summaries.LoadAllVolumeSummaries(); len(vols) > 0 {
		last := vols[len(vols)-1]
		fmt.Fprintf(&b, "- Quyển gần nhất《%s》: %s\n", last.Title, truncate(last.Summary, 200))
	} else {
		warn("volume_summaries", err)
	}

	// Nhân vật chính (core/important), tối đa 8 người
	if chars, err := s.Characters.Load(); len(chars) > 0 {
		var names []string
		for _, c := range chars {
			if c.Tier == "secondary" || c.Tier == "decorative" {
				continue
			}
			line := c.Name
			if role := strings.TrimSpace(c.Role); role != "" {
				line += "（" + role + "）"
			}
			names = append(names, line)
			if len(names) >= 8 {
				break
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&b, "- Nhân vật chính: %s\n", strings.Join(names, "、"))
		}
	} else {
		warn("characters", err)
	}

	// Chi tiết gài trước chưa thu, tối đa 6 mục
	if fs, err := s.World.LoadActiveForeshadow(); len(fs) > 0 {
		var items []string
		for _, f := range fs {
			items = append(items, truncate(f.Description, 40))
			if len(items) >= 6 {
				break
			}
		}
		fmt.Fprintf(&b, "- Chi tiết gài trước chưa thu: %s\n", strings.Join(items, "；"))
	} else {
		warn("foreshadow", err)
	}

	if len(warnings) > 0 {
		fmt.Fprintf(&b, "- Cảnh báo dữ liệu: %s\n", strings.Join(warnings, "；"))
	}

	return strings.TrimSpace(b.String())
}

// stageSystemPrompt ghép hoàn chỉnh hệ thống cộng tác theo giai đoạn: prompt của giai đoạn + bản tóm tắt trạng thái câu chuyện hiện tại.
// Phần tóm tắt được gắn ở cuối như một phụ lục dữ liệu (ngăn cách bằng dòng phân cách và định dạng chuẩn), hưởng ứng chỉ dẫn trong prompt rằng "tiến độ xem bên dưới".
func stageSystemPrompt(s *store.Store) string {
	prompt := stageCoCreateSystemPrompt
	if summary := buildStoryStateSummary(s); summary != "" {
		prompt += "\n\n---\n## Trạng thái câu chuyện hiện tại\n(Phần dưới đây là bản tóm tắt khách quan của nội dung đã viết, dùng để tham chiếu khi bạn lập kế hoạch tiếp theo, đừng chép nguyên văn vào <draft>)\n" + summary
	}
	return prompt
}
