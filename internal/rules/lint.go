package rules

import (
	"regexp"
	"strings"
)

// Lint kiểm tra các giới hạn tích hợp sẵn của sản phẩm: quét phần văn bản để tìm dấu vết cơ chế, không liên quan đến quy tắc người dùng; luôn chạy khi commit.
// Cùng hợp đồng với Check — chỉ trả về dữ kiện (nguyên tắc sắt một), không chặn luồng; để người đánh giá/người dùng quyết định.
//
// Hiện có ba loại (đều là các khiếm khuyết đã được kiểm chứng từ sản phẩm chạy dài thực tế):
//   - markdown_residue: phần văn bản còn ** in đậm, hoặc dòng tiêu đề # nằm sau dòng đầu (khi xuất txt sẽ làm lộ ký hiệu)
//   - non_cjk_fragments: các đoạn chữ cái Latin liên tiếp (ngôn ngữ bị trộn bởi model, chẳng hạn phần văn bản tiếng Trung lẫn "pattern")
func Lint(text string) []Violation {
	var vs []Violation
	vs = appendMarkdownResidue(vs, text)
	vs = appendNonCJKFragments(vs, text)
	return vs
}

func appendMarkdownResidue(vs []Violation, text string) []Violation {
	if n := strings.Count(text, "**"); n > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "**",
			Actual:   n,
			Severity: SeverityWarning,
		})
	}
	headings := 0
	seenContent := false
	for line := range strings.SplitSeq(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		// Tiêu đề # ở dòng không trống đầu tiên là định dạng hợp lệ của tệp chương (không cố định theo số dòng, cho phép các dòng trống ở đầu)
		first := !seenContent
		seenContent = true
		if !first && strings.HasPrefix(t, "#") {
			headings++
		}
	}
	if headings > 0 {
		vs = append(vs, Violation{
			Rule:     "markdown_residue",
			Target:   "#",
			Actual:   headings,
			Severity: SeverityWarning,
		})
	}
	return vs
}

var latinFragmentRe = regexp.MustCompile(`[A-Za-z]{2,}`)

// appendNonCJKFragments báo cáo tổng số lần xuất hiện của các đoạn chữ cái Latin và các ví dụ không trùng lặp.
// Tiếng Anh hợp lệ trong chủ đề hiện đại (tên thương hiệu/viết tắt) cũng sẽ khớp — đây là dữ kiện cấp warning, người đánh giá quyết định theo chủ đề.
func appendNonCJKFragments(vs []Violation, text string) []Violation {
	matches := latinFragmentRe.FindAllString(text, -1)
	if len(matches) == 0 {
		return vs
	}
	seen := make(map[string]struct{})
	var examples []string
	for _, m := range matches {
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		if len(examples) < 3 {
			examples = append(examples, m)
		}
	}
	return append(vs, Violation{
		Rule:     "non_cjk_fragments",
		Target:   strings.Join(examples, "、"),
		Actual:   len(matches),
		Severity: SeverityWarning,
	})
}
