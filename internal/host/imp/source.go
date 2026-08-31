package imp

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Nhãn mã hóa nguồn được hỗ trợ, ghi vào Manifest và sự kiện tiến độ, không dùng cơ chế dự phòng im lặng (RFC §7.1).
const (
	encodingUTF8    = "utf-8"
	encodingUTF8BOM = "utf-8-bom"
	encodingGB18030 = "gb18030"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// decoded là kết quả của một lần giải mã: văn bản + mã hóa thực sự được chọn.
type decoded struct {
	text     string
	encoding string
}

// decodeSource giải mã theo thứ tự UTF-8 / UTF-8 BOM / GB18030, rồi trả về mã hóa đã chọn.
// Nếu không thể giải mã tin cậy hoặc có ký tự thay thế thì thất bại ngay, lỗi sẽ chứa kết quả phát hiện, không che giấu việc "thử GB18030" thành dự phòng im lặng.
func decodeSource(raw []byte) (decoded, error) {
	if bytes.HasPrefix(raw, utf8BOM) {
		body := raw[len(utf8BOM):]
		if !utf8.Valid(body) {
			return decoded{}, fmt.Errorf("Khai báo UTF-8 BOM nhưng nội dung không phải UTF-8 hợp lệ")
		}
		return decoded{text: string(body), encoding: encodingUTF8BOM}, nil
	}
	if utf8.Valid(raw) {
		return decoded{text: string(raw), encoding: encodingUTF8}, nil
	}
	out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err != nil {
		return decoded{}, fmt.Errorf("Không phải UTF-8 hợp lệ, và giải mã GB18030 cũng thất bại: %w", err)
	}
	if !utf8.Valid(out) {
		return decoded{}, fmt.Errorf("Kết quả giải mã GB18030 vẫn không phải UTF-8 hợp lệ, không thể giải mã một cách tin cậy")
	}
	if i := bytes.IndexRune(out, utf8.RuneError); i >= 0 {
		return decoded{}, fmt.Errorf("Giải mã GB18030 xuất hiện ký tự thay thế (U+FFFD @ byte %d), không thể giải mã một cách tin cậy; vui lòng xác nhận mã hóa tệp", i)
	}
	return decoded{text: string(out), encoding: encodingGB18030}, nil
}

// normalize chỉ thực hiện chuyển đổi không làm thay đổi nội dung văn học: CRLF/CR được thống nhất thành LF.
// Giữ nguyên dòng trống, thụt lề, dòng tiêu đề và ký tự phần thân; không xóa văn bản đầu, chương rỗng, quảng cáo hay cái gọi là nhiễu ở cuối (RFC §7.2).
// BOM đã được tách ra ở giai đoạn decodeSource.
func normalize(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// Ingest đọc tệp nguồn, giải mã, chuẩn hóa, rồi tạo snapshot workspace meta/import/ nguyên tử bằng rename thư mục.
// Trả về tay cầm workspace và Manifest; phía gọi sẽ dựa vào đó để phát sự kiện tiến độ.
func Ingest(bookDir, sourcePath string, in Intent) (*Workspace, *Manifest, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("Đọc tệp nguồn: %w", err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil, fmt.Errorf("Tệp nguồn trống: %s", sourcePath)
	}
	dec, err := decodeSource(raw)
	if err != nil {
		return nil, nil, err
	}
	normBytes := []byte(normalize(dec.text))

	m := Manifest{
		Version:          workspaceSchemaVersion,
		SourceName:       filepath.Base(sourcePath),
		RawSHA256:        Digest(raw),
		NormalizedSHA256: Digest(normBytes),
		Encoding:         dec.encoding,
		SizeBytes:        int64(len(raw)),
		CreatedAt:        time.Now().UTC().Format(time.RFC3339),
	}
	if in.Version == 0 {
		in.Version = workspaceSchemaVersion
	}

	ws, err := createWorkspace(bookDir, m, in, normBytes)
	if err != nil {
		return nil, nil, err
	}
	return ws, &m, nil
}

// SourceUnit là tọa độ ổn định mà mô hình có thể tham chiếu (RFC §7.3).
// ID chỉ dùng cho hiển thị và tham chiếu của mô hình; mọi kiểm tra thứ tự/bao hàm/tăng dần đều phải theo thứ tự số của (Line, Part), nghiêm cấm so sánh chuỗi ID theo thứ tự từ điển.
type SourceUnit struct {
	ID        string `json:"id"`   // L1257; dòng vượt ngân sách được tách thành L1257.1, L1257.2
	Line      int    `json:"line"` // bắt đầu từ 1
	Part      int    `json:"part"` // 0=toàn dòng; phân mảnh ảo 1..N
	StartByte int    `json:"start_byte"`
	EndByte   int    `json:"end_byte"`
	Text      string `json:"text"`
}

// unitLess định nghĩa thứ tự toàn phần của SourceUnit: trước hết theo Line rồi theo Part, đều so sánh số học (bản sửa A1).
func unitLess(a, b SourceUnit) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Part < b.Part
}

// buildSourceUnits xây dựng bảng tọa độ ổn định từ văn bản đã chuẩn hóa.
// Mỗi dòng thông thường một unit; khi số byte của một dòng vượt maxUnitBytes thì chỉ tạo nhiều unit ảo tại ranh giới ký tự UTF-8,
// không ghi lại source.txt, không chèn ngắt dòng mềm, không thay đổi bất kỳ ký tự nguồn nào (RFC §7.3). maxUnitBytes<=0 nghĩa là không phân mảnh.
func buildSourceUnits(normalized []byte, maxUnitBytes int) []SourceUnit {
	var units []SourceUnit
	n := len(normalized)
	line := 0
	offset := 0
	for offset < n {
		nl := bytes.IndexByte(normalized[offset:], '\n')
		lineEnd := n
		if nl >= 0 {
			lineEnd = offset + nl
		}
		line++
		if maxUnitBytes > 0 && lineEnd-offset > maxUnitBytes {
			part := 0
			s := offset
			for s < lineEnd {
				e := s + maxUnitBytes
				if e >= lineEnd {
					e = lineEnd
				} else {
					for e > s && !utf8.RuneStart(normalized[e]) {
						e--
					}
					if e == s { // phương án dự phòng cực đoan cho một rune đơn lẻ quá dài
						e = s + maxUnitBytes
					}
				}
				part++
				units = append(units, SourceUnit{
					ID: fmt.Sprintf("L%d.%d", line, part), Line: line, Part: part,
					StartByte: s, EndByte: e, Text: string(normalized[s:e]),
				})
				s = e
			}
		} else {
			units = append(units, SourceUnit{
				ID: fmt.Sprintf("L%d", line), Line: line, Part: 0,
				StartByte: offset, EndByte: lineEnd, Text: string(normalized[offset:lineEnd]),
			})
		}
		if nl < 0 {
			break
		}
		offset = lineEnd + 1
	}
	return units
}

// resolveBoundaryByte ánh xạ một quyết định ranh giới thành vị trí byte chính xác:
// không có anchor thì lấy điểm bắt đầu của unit; có anchor thì yêu cầu khớp nguyên văn duy nhất trong unit đó, rồi ánh xạ thành độ lệch byte (RFC §8.3).
func resolveBoundaryByte(unitByID map[string]SourceUnit, unitID, anchor string) (int, error) {
	u, ok := unitByID[unitID]
	if !ok {
		return 0, fmt.Errorf("Tham chiếu ranh giới tới unit không tồn tại: %s", unitID)
	}
	if anchor == "" {
		return u.StartByte, nil
	}
	switch strings.Count(u.Text, anchor) {
	case 0:
		return 0, fmt.Errorf("Anchor %q không nằm trong unit %s", anchor, unitID)
	case 1:
		return u.StartByte + strings.Index(u.Text, anchor), nil
	default:
		return 0, fmt.Errorf("Anchor %q không duy nhất trong unit %s", anchor, unitID)
	}
}
