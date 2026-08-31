package host

import (
	"strings"
	"unicode/utf8"
)

// toolDisplays cấu hình chiến lược hiển thị của từng công cụ trên bảng luồng. Các công cụ
// không có trong bảng này sẽ không tham gia kết xuất theo luồng (observer sẽ bỏ qua
// DeltaToolCall).
//
// Chế độ chung (nakedKey rỗng): tokenizer sẽ render JSON args mà LLM xuất ra thành văn bản
// thụt dòng dạng "key: value", với object/array lồng nhau được thụt lề theo cấp, và string/
// number/bool được xuất theo luồng.
// Cách này tách rời hoàn toàn khỏi schema — LLM chỉ cần xuất thêm một trường là bảng sẽ có
// thêm một dòng, không cần chỉnh code.
//
// Chế độ luồng trần (nakedKey khác rỗng): chỉ phát nguyên trạng giá trị string của trường
// cấp cao nhất mục tiêu, còn các trường khác đều bị bỏ qua. Dùng cho draft_chapter để cả
// chương markdown không bị trang trí thành "content: # …".
// Header luôn bắt đầu bằng "✻ ": đây là tiền tố quy ước mà TUI renderStreamContent dùng để
// đi theo nhánh renderAgentBlock nổi bật (✻ vàng + label nền cyan gạch chân xanh + đường
// ngang dim), và phải nhất quán với header dự phòng (streamHeaderFallback); nếu đổi thành
// chữ thường, nó sẽ rơi vào đường dẫn thân bài và bị màu mặc định của terminal làm mờ, khiến
// title không còn nổi bật.
var toolDisplays = map[string]toolDisplay{
	"draft_chapter": {nakedKey: "content"},

	"plan_chapter":        {header: "✻ Lập kế hoạch"},
	"edit_chapter":        {header: "✻ Mài giũa"},
	"commit_chapter":      {header: "✻ Nộp chương"},
	"save_review":         {header: "✻ Duyệt"},
	"save_arc_summary":    {header: "✻ Tóm tắt cung"},
	"save_volume_summary": {header: "✻ Tóm tắt tập"},
	"save_foundation":     {header: "✻ Thiết lập"},
	"revise_outline":      {header: "✻ Sửa dàn ý"},
	"read_chapter":        {header: "✻ Đọc chương"},
	"check_consistency":   {header: "✻ Kiểm tra nhất quán"},
	"novel_context":       {header: "✻ Truy vấn ngữ cảnh"},
}

type toolDisplay struct {
	header   string
	nakedKey string
}

// jsonFieldExtractor là tokenizer JSON theo luồng. Nó điều khiển state machine từng byte,
// chuyển args của tool từ LLM thành văn bản có thể đọc được. Mỗi instance chỉ phục vụ một
// lần gọi tool; khi container cấp cao nhất đóng lại thì Done()=true.
type jsonFieldExtractor struct {
	cfg toolDisplay

	state pState
	stack []byte // ngăn container: 'O' obj / 'A' arr

	keyBuf strings.Builder

	escape bool
	uHex   []byte

	started bool // đã emit bất kỳ ký tự nào chưa (dùng cho khoảng xuống dòng giữa header và key đầu tiên)

	done bool
}

type pState int

const (
	psRoot         pState = iota
	psBeforeKey           // trong obj: chờ key kế tiếp hoặc }
	psInKey               // trong obj: đang phân tích key
	psAfterKey            // trong obj: chờ :
	psBeforeValue         // chờ ký tự bắt đầu của value
	psStringStream        // value kiểu string, emit từng ký tự đã giải mã
	psStringSkip          // value kiểu string, bỏ qua (ở chế độ luồng trần nếu không phải trường mục tiêu)
	psNumberStream        // số, emit theo luồng
	psNumberSkip          // số, bỏ qua
	psPrimStream          // true/false/null, emit theo luồng
	psPrimSkip            // true/false/null, bỏ qua
	psDone                // container cấp cao nhất đã đóng
)

func newToolExtractor(tool string) *jsonFieldExtractor {
	cfg, ok := toolDisplays[tool]
	if !ok {
		return nil
	}
	return &jsonFieldExtractor{cfg: cfg}
}

func (e *jsonFieldExtractor) Done() bool { return e.done }

func (e *jsonFieldExtractor) Feed(chunk string) string {
	if e.done || chunk == "" {
		return ""
	}
	var out strings.Builder
	for i := 0; i < len(chunk); i++ {
		e.step(chunk[i], &out)
		if e.done {
			break
		}
	}
	return out.String()
}

// ── ngăn container / thụt lề ──

func (e *jsonFieldExtractor) push(kind byte) {
	e.stack = append(e.stack, kind)
}

func (e *jsonFieldExtractor) pop() {
	if len(e.stack) == 0 {
		return
	}
	e.stack = e.stack[:len(e.stack)-1]
}

func (e *jsonFieldExtractor) parent() byte {
	if len(e.stack) == 0 {
		return 0
	}
	return e.stack[len(e.stack)-1]
}

// writeIndent ghi thụt lề hiện tại. Độ sâu = số cấp lồng nhau = len(stack)-1 (bên trong
// container root thì không thụt lề).
func (e *jsonFieldExtractor) writeIndent(out *strings.Builder) {
	depth := len(e.stack) - 1
	for range depth {
		out.WriteString("  ")
	}
}

// ── state machine ──

func (e *jsonFieldExtractor) step(c byte, out *strings.Builder) {
	switch e.state {
	case psRoot:
		switch c {
		case '{':
			e.push('O')
			e.state = psBeforeKey
		case '[':
			// Thực tế sẽ không xảy ra (tool args luôn là obj); chấp nhận: khi root là arr
			e.push('A')
			e.state = psBeforeValue
		}
	case psBeforeKey:
		switch c {
		case '"':
			e.keyBuf.Reset()
			e.escape = false
			e.state = psInKey
		case '}':
			e.closeContainer(out)
		case ' ', '\t', '\n', '\r', ',':
		}
	case psInKey:
		if e.escape {
			e.keyBuf.WriteByte(c)
			e.escape = false
			return
		}
		if c == '\\' {
			e.escape = true
			return
		}
		if c == '"' {
			e.emitKeyLine(out, e.keyBuf.String())
			e.state = psAfterKey
			return
		}
		e.keyBuf.WriteByte(c)
	case psAfterKey:
		if c == ':' {
			e.state = psBeforeValue
		}
	case psBeforeValue:
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == ',' {
			return
		}
		switch c {
		case '"':
			e.beginString(out)
		case '{':
			e.beginNested('O', out)
		case '[':
			e.beginNested('A', out)
		case ']', '}':
			e.closeContainer(out)
		case 't', 'f', 'n':
			e.beginPrim(c, out)
		default:
			if c == '-' || (c >= '0' && c <= '9') {
				e.beginNumber(c, out)
			}
		}
	case psStringStream:
		e.handleStringByte(c, out, false)
	case psStringSkip:
		e.handleStringByte(c, out, true)
	case psNumberStream:
		if isNumberByte(c) {
			out.WriteByte(c)
			return
		}
		e.afterValueChar(c, out)
	case psNumberSkip:
		if isNumberByte(c) {
			return
		}
		e.afterValueChar(c, out)
	case psPrimStream:
		if c >= 'a' && c <= 'z' {
			out.WriteByte(c)
			return
		}
		e.afterValueChar(c, out)
	case psPrimSkip:
		if c >= 'a' && c <= 'z' {
			return
		}
		e.afterValueChar(c, out)
	case psDone:
	}
}

// ── kết xuất dòng ──

// emitKeyLine được gọi khi key trong obj đã phân tích xong, để ghi tiền tố "<lf><indent>key:".
// Ở chế độ luồng trần, không ghi tiền tố key (key được ghi lại trong keyBuf để beginString
// kiểm tra).
func (e *jsonFieldExtractor) emitKeyLine(out *strings.Builder, key string) {
	if e.cfg.nakedKey != "" {
		return
	}
	if !e.started {
		if e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
		}
		e.started = true
	} else {
		out.WriteByte('\n')
	}
	e.writeIndent(out)
	out.WriteString(key)
	out.WriteByte(':')
}

// emitArrayItem được gọi khi bắt đầu mỗi phần tử trong arr, để ghi "<lf><indent>-". Phần tử
// primitive sẽ nối ngay một dấu cách rồi emit giá trị; phần tử struct sẽ được xử lý xuống
// dòng tự nhiên bởi lớp lồng tiếp theo.
func (e *jsonFieldExtractor) emitArrayItem(out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		return
	}
	if !e.started {
		if e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
		}
		e.started = true
	} else {
		out.WriteByte('\n')
	}
	e.writeIndent(out)
	out.WriteByte('-')
}

// ── bắt đầu value ──

func (e *jsonFieldExtractor) beginString(out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		// Luồng trần: chỉ xuất giá trị string của key mục tiêu ở obj cấp cao nhất
		if e.cfg.nakedKey == e.keyBuf.String() && len(e.stack) == 1 && e.stack[0] == 'O' {
			e.state = psStringStream
		} else {
			e.state = psStringSkip
		}
		e.escape = false
		e.uHex = nil
		return
	}
	// Chế độ chung: field của obj đi ngay sau "key: " (đã emit "key:", rồi thêm khoảng trắng);
	// phần tử arr đi ngay sau "- "
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	e.state = psStringStream
	e.escape = false
	e.uHex = nil
}

func (e *jsonFieldExtractor) beginNumber(first byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		e.state = psNumberSkip
		return
	}
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	out.WriteByte(first)
	e.state = psNumberStream
}

func (e *jsonFieldExtractor) beginPrim(first byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		e.state = psPrimSkip
		return
	}
	if e.parent() == 'A' {
		e.emitArrayItem(out)
		out.WriteByte(' ')
	} else {
		out.WriteByte(' ')
	}
	out.WriteByte(first)
	e.state = psPrimStream
}

func (e *jsonFieldExtractor) beginNested(kind byte, out *strings.Builder) {
	if e.cfg.nakedKey != "" {
		// Chế độ luồng trần không bung lồng; dùng độ sâu stack để theo dõi tới } / ]
		e.push(kind)
		if kind == 'O' {
			e.state = psBeforeKey
		} else {
			e.state = psBeforeValue
		}
		return
	}
	// Chế độ chung: khi phần tử arr là cấu trúc lồng nhau, trước hết emit một dòng riêng
	// "<indent>-"
	// (sau ":" của key trong obj thì không thêm khoảng trắng, để key con lồng vào tự xuống
	// dòng ở hàng kế tiếp)
	if e.parent() == 'A' {
		e.emitArrayItem(out)
	}
	e.push(kind)
	if kind == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// closeContainer xử lý } hoặc ].
func (e *jsonFieldExtractor) closeContainer(out *strings.Builder) {
	e.pop()
	if len(e.stack) == 0 {
		// Args rỗng (ví dụ novel_context không truyền tham số): dự phòng vì emitKeyLine không
		// có cơ hội xuất header, nên bổ sung ở đây để tránh rơi vào trạng thái "không có tiêu
		// đề cũng không có nội dung".
		if !e.started && e.cfg.nakedKey == "" && e.cfg.header != "" {
			out.WriteString(e.cfg.header)
			out.WriteByte('\n')
			e.started = true
		}
		// Kết thúc bằng một dòng trống để bảng có ranh giới rõ ràng với phần output kế tiếp
		if e.started {
			out.WriteByte('\n')
		}
		e.state = psDone
		e.done = true
		return
	}
	if e.parent() == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// ── string theo luồng ──

func (e *jsonFieldExtractor) handleStringByte(c byte, out *strings.Builder, skipping bool) {
	if e.uHex != nil {
		e.uHex = append(e.uHex, c)
		if len(e.uHex) == 4 {
			if r, ok := parseHex4(e.uHex); ok && !skipping {
				var buf [4]byte
				n := utf8.EncodeRune(buf[:], r)
				out.Write(buf[:n])
			}
			e.uHex = nil
		}
		return
	}
	if e.escape {
		e.escape = false
		if !skipping {
			writeEscapedByte(out, c)
		}
		if c == 'u' {
			e.uHex = make([]byte, 0, 4)
		}
		return
	}
	if c == '\\' {
		e.escape = true
		return
	}
	if c == '"' {
		e.afterValueDone()
		return
	}
	if !skipping {
		out.WriteByte(c)
	}
}

func writeEscapedByte(out *strings.Builder, c byte) {
	switch c {
	case 'n':
		out.WriteByte('\n')
	case 't':
		out.WriteByte('\t')
	case 'r':
		out.WriteByte('\r')
	case '"':
		out.WriteByte('"')
	case '\\':
		out.WriteByte('\\')
	case '/':
		out.WriteByte('/')
	case 'b', 'f':
		// backspace / form feed: bỏ qua
	case 'u':
		// do bên gọi tạo bộ đệm uHex; chỗ này không xuất
	default:
		out.WriteByte('\\')
		out.WriteByte(c)
	}
}

// ── kết thúc ──

// afterValueDone chuyển sang state tiếp theo sau khi string đóng (đọc tới dấu `"` kết thúc).
func (e *jsonFieldExtractor) afterValueDone() {
	e.escape = false
	e.uHex = nil
	if len(e.stack) == 0 {
		e.state = psDone
		e.done = true
		return
	}
	if e.parent() == 'O' {
		e.state = psBeforeKey
	} else {
		e.state = psBeforeValue
	}
}

// afterValueChar quyết định state tiếp theo khi đã đọc đến "ký tự kết thúc" của number /
// primitive. Ký tự này có thể là , / } / ] / khoảng trắng, và hàm này sẽ chuyển tiếp để
// phân phối nó.
func (e *jsonFieldExtractor) afterValueChar(c byte, out *strings.Builder) {
	switch c {
	case '}', ']':
		e.closeContainer(out)
	case ',', ' ', '\t', '\n', '\r':
		if len(e.stack) == 0 {
			e.state = psDone
			e.done = true
			return
		}
		if e.parent() == 'O' {
			e.state = psBeforeKey
		} else {
			e.state = psBeforeValue
		}
	}
}

// ── tiện ích ──

func isNumberByte(c byte) bool {
	switch c {
	case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
		'-', '+', '.', 'e', 'E':
		return true
	}
	return false
}

func parseHex4(b []byte) (rune, bool) {
	var r rune
	for _, d := range b {
		var v rune
		switch {
		case d >= '0' && d <= '9':
			v = rune(d - '0')
		case d >= 'a' && d <= 'f':
			v = rune(d-'a') + 10
		case d >= 'A' && d <= 'F':
			v = rune(d-'A') + 10
		default:
			return 0, false
		}
		r = r*16 + v
	}
	return r, true
}
