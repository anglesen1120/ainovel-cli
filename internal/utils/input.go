package utils

import (
	"strings"
	"unicode"
)

// CleanInputText xóa ký tự điều khiển không có ý nghĩa nghiệp vụ khỏi input terminal, giữ lại văn bản người dùng thấy được.
// Trong ngữ cảnh nhập một dòng, newline và tab từ nội dung dán sẽ được chuẩn hóa thành khoảng trắng.
func CleanInputText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// CleanInputLine làm sạch input thủ công một dòng và cắt khoảng trắng hai đầu.
func CleanInputLine(s string) string {
	return strings.TrimSpace(CleanInputText(s))
}

func CleanInputRunes(runes []rune) string {
	var b strings.Builder
	for _, r := range runes {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
			continue
		}
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func ContainsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
