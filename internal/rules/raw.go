package rules

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RawSource là một nguồn thô chờ chuẩn hóa (toàn bộ văn bản của tệp rules).
//
// Sau khi bỏ YAML, tệp rules chỉ là prompt ngôn ngữ tự nhiên; chuẩn hóa chỉ cần văn bản gốc, không còn phân tích front matter.
type RawSource struct {
	Label string     // Nhãn nguồn, đi vào Snapshot.Sources (ví dụ global:my-style.md)
	Kind  SourceKind // Tầng ưu tiên
	Text  string     // Nội dung gốc của tệp
}

// RawFileSources liệt kê tệp .md trong thư mục rules theo thứ tự Global -> Project và trả về văn bản gốc.
//
// Dùng cùng quy ước quét với readDirFromDisk (md cấp đỉnh, thứ tự từ điển, bỏ qua tệp ẩn), nhưng không phân tích YAML,
// toàn bộ văn bản được chuyển nguyên dạng cho bộ chuẩn hóa. System defaults / khởi động prompt / yêu cầu trong lúc chạy do service cung cấp riêng.
func RawFileSources(opts LoadOptions) []RawSource {
	var out []RawSource
	out = append(out, rawDir(opts.HomeRulesDir, SourceGlobal)...)
	out = append(out, rawDir(opts.ProjectRulesDir, SourceProject)...)
	return out
}

func rawDir(dir string, kind SourceKind) []RawSource {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Thư mục không tồn tại là bình thường nên âm thầm bỏ qua; nhưng lỗi quyền hoặc đường dẫn thực ra là tệp phải được ghi dấu:
		// nếu không, người dùng đã viết rules nhưng chúng hoàn toàn không có hiệu lực và không có phản hồi, chi phí điều tra rất cao (xem known_rules_path_stale_readme).
		if !os.IsNotExist(err) {
			slog.Warn("Đọc thư mục rules thất bại, đã bỏ qua", "module", "rules", "dir", dir, "err", err)
		}
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var out []RawSource
	for _, name := range names {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("Đọc tệp rules thất bại, đã bỏ qua", "module", "rules", "file", path, "err", err)
			continue
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			continue
		}
		out = append(out, RawSource{
			Label: kind.String() + ":" + name,
			Kind:  kind,
			Text:  text,
		})
	}
	return out
}
