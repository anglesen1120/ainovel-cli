package utils

import "strings"

// JSONFieldExtractor trích giá trị chuỗi của một trường chỉ định từ các mảnh JSON stream.
//
// Khi LLM tạo tool call dạng stream, tham số đến theo từng mảnh với OpenAI/Anthropic
// hoặc đến một lần với Gemini. Bộ trích này dùng state machine quét từng ký tự,
// khi phát hiện key mục tiêu thì trích giá trị chuỗi và xử lý escape JSON.
type JSONFieldExtractor struct {
	key      string // Mục tiêu khớp, ví dụ `"content"` hoặc `"task"`
	state    extractState
	matchPos int
	escape   bool
	buf      strings.Builder
}

type extractState int

const (
	stateScan    extractState = iota // Quét để tìm key mục tiêu
	stateColon                       // Đã khớp key, chờ dấu hai chấm và dấu nháy mở
	stateExtract                     // Đang trích giá trị chuỗi
)

func NewFieldExtractor(fieldName string) *JSONFieldExtractor {
	return &JSONFieldExtractor{key: `"` + fieldName + `"`}
}

// Feed xử lý một đoạn delta và trả văn bản trích được, có thể rỗng.
func (e *JSONFieldExtractor) Feed(delta string) string {
	e.buf.Reset()
	for _, r := range delta {
		switch e.state {
		case stateScan:
			e.feedScan(r)
		case stateColon:
			e.feedColon(r)
		case stateExtract:
			e.feedExtract(r)
		}
	}
	return e.buf.String()
}

func (e *JSONFieldExtractor) feedScan(r rune) {
	if e.matchPos < len(e.key) && byte(r) == e.key[e.matchPos] {
		e.matchPos++
		if e.matchPos == len(e.key) {
			e.state = stateColon
			e.matchPos = 0
		}
		return
	}
	e.matchPos = 0
	if byte(r) == e.key[0] {
		e.matchPos = 1
	}
}

func (e *JSONFieldExtractor) feedColon(r rune) {
	switch r {
	case ':', ' ', '\t':
		// Bỏ qua
	case '"':
		e.state = stateExtract
		e.escape = false
	default:
		e.state = stateScan
		e.matchPos = 0
		if byte(r) == e.key[0] {
			e.matchPos = 1
		}
	}
}

func (e *JSONFieldExtractor) feedExtract(r rune) {
	if e.escape {
		e.escape = false
		switch r {
		case 'n':
			e.buf.WriteByte('\n')
		case 't':
			e.buf.WriteByte('\t')
		case 'r':
			e.buf.WriteByte('\r')
		case '"', '\\', '/':
			e.buf.WriteRune(r)
		default:
			e.buf.WriteByte('\\')
			e.buf.WriteRune(r)
		}
		return
	}
	switch r {
	case '\\':
		e.escape = true
	case '"':
		e.state = stateScan
		e.matchPos = 0
	default:
		e.buf.WriteRune(r)
	}
}

// Reset đặt lại trạng thái, dùng khi sang lượt tin nhắn LLM mới.
func (e *JSONFieldExtractor) Reset() {
	e.state = stateScan
	e.matchPos = 0
	e.escape = false
}

// ThinkingSep là marker phân tách văn bản suy nghĩ với nội dung chính.
// StreamFilter chèn marker này trước đoạn suy nghĩ để TUI đổi kiểu render.
const ThinkingSep = "\x02"

// StreamFilter phân biệt phản hồi văn bản của SubAgent với tool call JSON.
// Phản hồi văn bản được đánh dấu là nội dung suy nghĩ bằng tiền tố ThinkingSep; tool call JSON chỉ trích trường chỉ định.
//
// Cách nhận biết: gặp { thì vào chế độ JSON và theo dõi độ sâu dấu ngoặc nhọn;
// khi độ sâu về 0 thì quay lại chế độ văn bản.
type StreamFilter struct {
	fieldExt   *JSONFieldExtractor
	mode       filterMode
	braceDepth int
	inString   bool // Đang ở trong chuỗi JSON, dấu ngoặc nhọn không được tính
	escJSON    bool // Escape trong chuỗi JSON
	thinking   bool // Hiện đang ở đoạn văn bản suy nghĩ
	buf        strings.Builder
}

type filterMode int

const (
	filterText filterMode = iota // Phản hồi văn bản, chuyển thẳng qua
	filterJSON                   // Tool call JSON, trích trường mục tiêu
)

func NewStreamFilter(fieldName string) *StreamFilter {
	return &StreamFilter{fieldExt: NewFieldExtractor(fieldName)}
}

// Feed xử lý một đoạn delta và trả văn bản có thể hiển thị.
// Phản hồi văn bản được xuất trực tiếp; giá trị trường mục tiêu trong JSON được trích ra để xuất, còn cấu trúc JSON khác bị bỏ.
func (f *StreamFilter) Feed(delta string) string {
	f.buf.Reset()
	for _, r := range delta {
		switch f.mode {
		case filterText:
			if r == '{' {
				f.thinking = false
				f.mode = filterJSON
				f.braceDepth = 1
				f.inString = false
				f.escJSON = false
				f.fieldExt.Reset()
				f.feedExtractor(r)
			} else {
				if !f.thinking {
					f.thinking = true
					f.buf.WriteString(ThinkingSep)
				}
				f.buf.WriteRune(r)
			}
		case filterJSON:
			f.feedExtractor(r)
			f.trackBraces(r)
		}
	}
	return f.buf.String()
}

// feedExtractor đưa một ký tự vào fieldExt và ghi kết quả trích được vào buf.
func (f *StreamFilter) feedExtractor(r rune) {
	if text := f.fieldExt.Feed(string(r)); text != "" {
		f.buf.WriteString(text)
	}
}

// trackBraces theo dõi độ sâu dấu ngoặc nhọn JSON; khi độ sâu về 0 thì chuyển lại chế độ văn bản.
func (f *StreamFilter) trackBraces(r rune) {
	if f.escJSON {
		f.escJSON = false
		return
	}
	if f.inString {
		switch r {
		case '\\':
			f.escJSON = true
		case '"':
			f.inString = false
		}
		return
	}
	switch r {
	case '"':
		f.inString = true
	case '{':
		f.braceDepth++
	case '}':
		f.braceDepth--
		if f.braceDepth <= 0 {
			f.mode = filterText
		}
	}
}

// Reset đặt lại trạng thái.
func (f *StreamFilter) Reset() {
	f.mode = filterText
	f.braceDepth = 0
	f.inString = false
	f.escJSON = false
	f.thinking = false
	f.fieldExt.Reset()
}
