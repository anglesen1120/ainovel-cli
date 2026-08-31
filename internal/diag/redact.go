package diag

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SkelEvent là khung hành vi đã khử nhạy cảm của một tin nhắn phiên: giữ lại tín hiệu cấu trúc (vai trò / công cụ / lỗi /
// dấu vân tay lặp), còn mọi văn bản tự do (nội dung, prompt, suy nghĩ) đều bị che. Đây là lớp chiếu còn
// nghiêm hơn store.compactMessage — bên kia nén theo dung lượng (>4KB), còn ở đây không xét dung lượng,
// không để bất kỳ văn bản nào lộ ra.
type SkelEvent struct {
	Agent    string     // Phiên nguồn: writer-ch07 / architect-arc02 …
	Role     string     // assistant / tool / user
	Tools    []SkelTool // Lệnh công cụ bên trong tin nhắn này
	ErrClass string     // role=tool và is_error: dòng đầu của lỗi (chuỗi lỗi khung, không gồm nội dung)
	TextSha  string     // Băm ngắn của phần văn bản bị che; cùng sha = lặp lại cùng một đoạn (tín hiệu vòng lặp)
	Redacted int        // Số khối văn bản/suy nghĩ bị che ở mục này (dùng cho tự kiểm tra khử nhạy cảm)
}

// SkelTool là phép chiếu đã khử nhạy cảm của một lần gọi công cụ.
type SkelTool struct {
	Name     string            // Tên công cụ (tín hiệu cấu trúc, không gồm nội dung)
	Args     map[string]string // key → giá trị vô hướng gốc / chuỗi ngắn có dấu ngoặc kép / "<redacted len sha>"
	Invalid  bool              // ArgsInvalid: tham số do mô hình gửi tới không thể phân tích (tín hiệu #34)
	ParseErr string            // ArgsParseError: nguyên nhân phân tích thất bại
}

// redactMessage chiếu một agentcore.Message thành khung hành vi.
func redactMessage(agent string, m agentcore.Message) SkelEvent {
	ev := SkelEvent{Agent: agent, Role: string(m.Role)}
	isErr, _ := m.Metadata["is_error"].(bool)

	var text strings.Builder
	for _, b := range m.Content {
		switch b.Type {
		case agentcore.ContentText:
			// Kết quả lỗi của tool giữ lại dòng đầu: đây là chuỗi lỗi của chính chúng ta (ví dụ InputValidationError),
			// không chứa nội dung, và là chìa khóa để định vị vòng lặp. Phần còn lại đều đưa vào vùng che.
			if m.Role == agentcore.RoleTool && isErr && ev.ErrClass == "" {
				ev.ErrClass = firstLine(b.Text, 160)
				continue
			}
			if strings.TrimSpace(b.Text) != "" {
				text.WriteString(b.Text)
				ev.Redacted++
			}
		case agentcore.ContentThinking:
			if strings.TrimSpace(b.Thinking) != "" {
				text.WriteString(b.Thinking)
				ev.Redacted++
			}
		case agentcore.ContentToolCall:
			if b.ToolCall != nil {
				ev.Tools = append(ev.Tools, redactToolCall(b.ToolCall))
			}
		}
	}
	if t := text.String(); t != "" {
		ev.TextSha = shortHash(t)
	}
	return ev
}

// redactToolCall chiếu một lần gọi công cụ: tên công cụ + tham số (giá trị đã che) + cờ lỗi phân tích.
func redactToolCall(tc *agentcore.ToolCall) SkelTool {
	return SkelTool{
		Name:     tc.Name,
		Args:     redactArgs(tc.Args),
		Invalid:  tc.ArgsInvalid,
		ParseErr: tc.ArgsParseError,
	}
}

// redactArgs chiếu đối tượng tham số công cụ thành key → giá trị đã che. Tham số không phải đối tượng trả về nil
// (ArgsInvalid/ParseErr đã được ghi riêng trong SkelTool).
func redactArgs(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = projectValue(v)
	}
	return out
}

// projectValue chiếu từng giá trị tham số theo kiểu JSON:
//   - Vô hướng (số / bool / null): chính giá trị gốc đã là tín hiệu cấu trúc, nên giữ lại (chapter: 7)
//   - Chuỗi ngắn dạng định danh: giữ lại có dấu ngoặc kép để lộ kiểu (chapter: "7" ← tín hiệu số bị chuyển thành chuỗi trong #34)
//   - Chuỗi chứa chữ Hán / khoảng trắng / văn bản dài, đối tượng, mảng: che thành <redacted …> (không lộ nội dung)
//   - Đã là placeholder [session_compact: …]: an toàn và có thông tin, giữ nguyên
func projectValue(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return ""
	}
	switch s[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return redactPlaceholder(s)
		}
		if strings.HasPrefix(str, store.CompactTag) {
			return str
		}
		// Chỉ giữ lại các giá trị ngắn trông như "định danh/số/enum" (chapter:"7", type:"premise", agent:"writer");
		// Bất kỳ chuỗi nào có chữ Hán, khoảng trắng hoặc ký hiệu khác đều được xem là nội dung và sẽ bị che.
		if utf8.RuneCountInString(str) <= 32 && isStructuralToken(str) {
			return strconv.Quote(str)
		}
		return redactPlaceholder(str)
	case '{':
		return fmt.Sprintf("<redacted object len=%d>", len(raw))
	case '[':
		return fmt.Sprintf("<redacted array len=%d>", len(raw))
	default:
		return s
	}
}

// isStructuralToken kiểm tra chuỗi có trông như một "định danh" hay không — chỉ gồm ASCII chữ / số / `_-.:/`,
// không có khoảng trắng, không có chữ Hán. Dùng để phân biệt tín hiệu cấu trúc (giữ lại) với mảnh nội dung (che đi).
func isStructuralToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '.' || r == ':' || r == '/':
		default:
			return false
		}
	}
	return true
}

func redactPlaceholder(s string) string {
	return fmt.Sprintf("<redacted len=%d sha=%s>", utf8.RuneCountInString(s), shortHash(s))
}

// shortHash lấy hash ngắn của văn bản; chỉ dùng để kiểm tra cùng một đoạn văn bản có lặp lại hay không, không dùng cho mã hóa.
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// firstLine lấy dòng đầu và cắt theo rune để tóm tắt chuỗi lỗi.
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		s = string(r[:max]) + "…"
	}
	return s
}
