package utils

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// DecodeText giải mã byte file văn bản người dùng cung cấp thành UTF-8. Nếu dữ liệu không phải UTF-8 hợp lệ thì chuyển mã theo GB18030,
// là superset của GBK, vì nhiều file tiểu thuyết txt lưu hành trên mạng dùng GBK và đọc thẳng như UTF-8 sẽ thành mojibake.
// Chuỗi byte không phải GBK sẽ được decoder thay bằng U+FFFD; đó vẫn là dữ liệu lỗi mã hóa để caller dùng cơ chế lỗi dự phòng hướng dẫn người dùng.
// Cuối cùng loại bỏ UTF-8 BOM để khớp đầu dòng không bị dính marker này.
func DecodeText(data []byte) string {
	if !utf8.Valid(data) {
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
			data = decoded
		}
	}
	return strings.TrimPrefix(string(data), "\uFEFF")
}
