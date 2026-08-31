package tools

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

var premiseHeadingAliases = map[string]string{
	"Thể loại và tông điệu":                           "Thể loại và tông điệu",
	"Định vị thể loại":                                "Định vị thể loại",
	"Xung đột cốt lõi":                                "Xung đột cốt lõi",
	"Mục tiêu nhân vật chính":                         "Mục tiêu nhân vật chính",
	"Hướng kết cục":                                   "Hướng kết cục",
	"Vùng cấm viết":                                   "Vùng cấm viết",
	"Điểm bán khác biệt":                              "Điểm bán khác biệt",
	"Móc câu khác biệt":                               "Móc câu khác biệt",
	"Cam kết thực hiện cốt lõi":                       "Cam kết thực hiện cốt lõi",
	"Động cơ câu chuyện":                              "Động cơ câu chuyện",
	"Tuyến quan hệ/trưởng thành":                      "Tuyến quan hệ/trưởng thành",
	"Lộ trình nâng cấp":                               "Lộ trình nâng cấp",
	"Bước ngoặt giữa chặng":                           "Bước ngoặt giữa chặng",
	"Luận đề kết cục":                                 "Luận đề kết cục",
	"Khả năng phù hợp truyện ngắn":                    "Khả năng phù hợp truyện ngắn",
	"Vì sao tác phẩm phù hợp với truyện ngắn/một tập": "Khả năng phù hợp truyện ngắn",
	"Câu chuyện vận hành":                             "Động cơ câu chuyện",
	"Tuyến phát triển quan hệ/tăng trưởng":            "Tuyến quan hệ/trưởng thành",
	"Chuyển hướng giữa chặng":                         "Bước ngoặt giữa chặng",
	"Đề tài kết cục":                                  "Luận đề kết cục",
	"Bản đồ ngắn tập":                                 "Khả năng phù hợp truyện ngắn",
	"Mục tiêu của nhân vật chính":                     "Mục tiêu nhân vật chính",
	"Vùng cấm khi viết":                               "Vùng cấm viết",
	"Điểm bán khác biệt hóa":                          "Điểm bán khác biệt",
	"Móc khác biệt hóa":                               "Móc câu khác biệt",
	"Vì sao tác phẩm này phù hợp khép lại truyện ngắn/một tập":                                             "Khả năng phù hợp truyện ngắn",
	"Mức phù hợp truyện ngắn":                                                                              "Khả năng phù hợp truyện ngắn",
	"Động cơ câu chuyện：động lực bên ngoài và bên trong lần lượt là gì":                                    "Động cơ câu chuyện",
	"Tuyến quan hệ/trưởng thành chính":                                                                     "Tuyến quan hệ/trưởng thành",
	"Chủ đề kết cục":                                                                                       "Luận đề kết cục",
	"Định vị thể loại(độc giả mục tiêu / điểm hấp dẫn cốt lõi)":                                            "Định vị thể loại",
	"Hướng kết cục(hướng chủ đề，không phải tên quyển hoặc số chương cụ thể)":                               "Hướng kết cục",
	"Điểm bán khác biệt(ít nhất 3 mục)":                                                                    "Điểm bán khác biệt",
	"Móc câu khác biệt：điểm độc đáo đáng theo dõi tiếp nhất của cuốn sách này":                             "Móc câu khác biệt",
	"Cam kết thực hiện cốt lõi：cuốn sách này liên tục đem lại điều gì cho độc giả":                         "Cam kết thực hiện cốt lõi",
	"Tuyến quan hệ/trưởng thành chính：quan hệ nhân vật và trưởng thành tiến triển xuyên quyển thế nào":     "Tuyến quan hệ/trưởng thành",
	"Lộ trình nâng cấp：giai đoạn đầu / giữa / sau dựa vào gì để nâng cấp":                                  "Lộ trình nâng cấp",
	"Chuyển hướng giữa chặng：phương pháp giai đoạn đầu khi nào mất hiệu lực, câu chuyện chuyển số thế nào": "Bước ngoặt giữa chặng",
	"Chủ đề kết cục：vấn đề cuối cùng thật sự cần trả lời ở giai đoạn sau":                                  "Luận đề kết cục",
}

func parsePremiseSections(premise string) map[string]string {
	lines := strings.Split(premise, "\n")
	sections := make(map[string]string)
	var current string
	var body []string

	flush := func() {
		if current == "" {
			return
		}
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text != "" {
			sections[current] = text
		}
		body = body[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heading, ok := canonicalPremiseHeading(trimmed); ok {
			flush()
			current = heading
			continue
		}
		if current != "" {
			body = append(body, line)
		}
	}
	flush()
	return sections
}

func canonicalPremiseHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(line, "#"))
	if title == "" {
		return "", false
	}
	canonical, ok := premiseHeadingAliases[title]
	return canonical, ok
}

func premiseStructure(premise string, tier domain.PlanningTier) map[string]any {
	sections := parsePremiseSections(premise)
	required := requiredPremiseHeadings(tier)
	found := make([]string, 0, len(required))
	var missing []string
	for _, heading := range required {
		if _, ok := sections[heading]; ok {
			found = append(found, heading)
			continue
		}
		missing = append(missing, heading)
	}

	structure := map[string]any{
		"template_ready": len(missing) == 0,
		"found":          found,
		"missing":        missing,
	}
	if len(sections) > 0 {
		structure["section_count"] = len(sections)
	}
	return structure
}

func requiredPremiseHeadings(tier domain.PlanningTier) []string {
	common := []string{
		"Thể loại và tông điệu",
		"Định vị thể loại",
		"Xung đột cốt lõi",
		"Mục tiêu nhân vật chính",
		"Hướng kết cục",
		"Vùng cấm viết",
		"Điểm bán khác biệt",
		"Móc câu khác biệt",
		"Cam kết thực hiện cốt lõi",
	}

	switch tier {
	case domain.PlanningTierLong:
		return append(common,
			"Động cơ câu chuyện",
			"Tuyến quan hệ/trưởng thành",
			"Lộ trình nâng cấp",
			"Bước ngoặt giữa chặng",
			"Luận đề kết cục",
		)
	case domain.PlanningTierMid:
		return append(common,
			"Động cơ câu chuyện",
			"Bước ngoặt giữa chặng",
		)
	case domain.PlanningTierShort:
		return append(common,
			"Khả năng phù hợp truyện ngắn",
		)
	default:
		return common
	}
}
