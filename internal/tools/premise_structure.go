package tools

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

var premiseHeadingAliases = map[string]string{
	"Định vị thể loại":                                "Định vị thể loại",
	"Thể loại và tông điệu":                           "Thể loại và tông điệu",
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
