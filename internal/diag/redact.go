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

// SkelEvent là khung hành vi đã khử nhạy của một thông điệp phiên: giữ các tín hiệu cấu trúc (vai trò, công cụ, lỗi và dấu vân tay lặp lại), còn mọi văn bản tự do (nội dung truyện, prompt, suy nghĩ) đều bị che. Đây là lớp chiếu chặt hơn
// store.compactMessage: phía sau chỉ nén theo dung lượng (>4KB), còn ở đây không xét dung lượng; không văn bản nào được lọt ra ngoài.
type SkelEvent struct {
	Agent    string     // Phiên nguồn: writer-ch07 / architect-arc02 …
	Role     string     // assistant / tool / user
	Tools    []SkelTool // Các lần gọi công cụ trong thông điệp này
	ErrClass string     // role=tool và is_error: dòng đầu của lỗi (chuỗi lỗi framework, không gồm nội dung truyện)
	TextSha  string     // Băm ngắn của nội dung truyện bị che; cùng sha nghĩa là cùng một đoạn được tạo lại (tín hiệu vòng lặp)
	Redacted int        // Số khối nội dung/suy nghĩ bị che của mục này (dùng để tự kiểm tra việc khử nhạy)
}

// SkelTool là phép chiếu đã khử nhạy của một lần gọi công cụ.
type SkelTool struct {
	Name     string            // Tên công cụ (tín hiệu cấu trúc, không chứa nội dung truyện)
	Args     map[string]string // key → giá trị vô hướng gốc / chuỗi ngắn có dấu ngoặc kép / "<redacted len sha>"
	Invalid  bool              // ArgsInvalid: tham số model gửi lên không thể phân tích (#34 signal)
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
			// Với kết quả lỗi của tool, giữ lại dòng đầu: đây là chuỗi lỗi của chính framework (ví dụ InputValidationError),
			// không chứa nội dung truyện và là chìa khóa để định vị vòng lặp. Phần văn bản còn lại đều đưa vào vùng che.
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

// redactToolCall chiếu một lần gọi công cụ: tên công cụ, tham số (giá trị đã che) và cờ lỗi phân tích.
func redactToolCall(tc *agentcore.ToolCall) SkelTool {
	return SkelTool{
		Name:     tc.Name,
		Args:     redactArgs(tc.Args),
		Invalid:  tc.ArgsInvalid,
		ParseErr: tc.ArgsParseError,
	}
}

// redactArgs chiếu đối tượng tham số công cụ thành key → giá trị đã che. Tham số không phải object thì trả nil
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

// projectValue chiếu một giá trị tham số theo kiểu JSON:
//   - vô hướng (số / bool / null): giữ nguyên vì là tín hiệu cấu trúc (chapter: 7)
//   - chuỗi định danh ngắn: giữ trong dấu ngoặc kép để biểu lộ kiểu (chapter: "7" ← #34)
//   - chuỗi có chữ ngoài ASCII / khoảng trắng / văn bản dài, đối tượng, mảng: che thành <redacted …> (nội dung truyện)
//   - chuỗi đã có tiền tố [session_compact: …]: giữ lại
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
		// Chỉ giữ chuỗi "giống định danh/số/enum" (chapter: "7", type: "premise", agent: "writer");
		// các chuỗi khác có thể chứa nội dung truyện nên được che.
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

// isStructuralToken xác định chuỗi chỉ gồm chữ và số ASCII cùng các ký tự _-.:/;
// những chuỗi này được giữ lại vì là tín hiệu cấu trúc, không phải nội dung truyện.
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

// shortHash lấy băm ngắn của văn bản; chỉ dùng để nhận diện các nội dung giống nhau.
func shortHash(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", h.Sum32())
}

// firstLine lấy dòng đầu và cắt theo rune để tóm tắt lỗi.
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
