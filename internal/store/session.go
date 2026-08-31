package store

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/voocel/agentcore"
)

// SessionStore ghi nối tiếp lịch sử đối thoại LLM vào file JSONL.
// Nội dung dung lượng lớn (phần thân tiểu thuyết, ngữ cảnh đầy đủ) được thay bằng nhãn giữ chỗ [session_compact: ...].
type SessionStore struct {
	io      *IO
	mu      sync.Mutex
	seq     map[string]int    // Số thứ tự chạy của agent (dùng khi không thể trích xuất số chương)
	taskKey map[string]string // "agentName|task" → suffix, cùng một run dùng lại cùng một file
}

func NewSessionStore(io *IO) *SessionStore {
	return &SessionStore{io: io, seq: make(map[string]int), taskKey: make(map[string]string)}
}

// ModelLookup khi logger ghi sẽ tra provider/model "đang có hiệu lực tại thời điểm đó" theo tên agent.
// Dùng kiểu func thay vì interface để phía gọi có thể tiêm quy tắc chuẩn hóa bằng closure (như architect_short → architect).
// Trả về chuỗi rỗng nghĩa là không rõ, phía gọi vẫn ghi bình thường nhưng không kèm _meta; khi replay sẽ rơi về fallback của ModelSet.
type ModelLookup func(agentName string) (provider, model string)

// SubAgentLogger trả về callback OnMessage của sub-agent.
func (s *SessionStore) SubAgentLogger(lookup ModelLookup) func(agentName, task string, msg agentcore.AgentMessage) {
	return func(agentName, task string, msg agentcore.AgentMessage) {
		rel, err := s.subAgentPath(agentName, task)
		if err != nil {
			slog.Warn("ghi log phiên thất bại", "agent", agentName, "err", err)
			return
		}
		var meta *sessionLogMeta
		if lookup != nil {
			meta = lookupMeta(lookup, agentName)
		}
		if err := s.logEntry(rel, msg, meta); err != nil {
			slog.Warn("ghi log phiên thất bại", "agent", agentName, "err", err)
		}
	}
}

func lookupMeta(lookup ModelLookup, agentName string) *sessionLogMeta {
	provider, model := lookup(agentName)
	if provider == "" && model == "" {
		return nil
	}
	return &sessionLogMeta{Provider: provider, Model: model}
}

// LogCoCreate ghi nối thêm một bản ghi nhật ký đối thoại đồng sáng tạo vào meta/sessions/cocreate.jsonl.
// Giai đoạn đồng sáng tạo chưa gắn với tiểu thuyết cụ thể, nên thống nhất ghi về gốc mặc định của OutputDir (output/novel),
// cùng cấp với sáng tác chính thức agents/*, để tiện điều tra.
func (s *SessionStore) LogCoCreate(entry any) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal cocreate session: %w", err)
	}
	data = append(data, '\n')
	return s.io.AppendLine("meta/sessions/cocreate.jsonl", data)
}

// Log ghi thêm một message vào đường dẫn chỉ định, tự động nén nội dung lớn.
// Không kèm _meta; chỉ dùng cho cocreate và các đường dẫn không có vai trò.
func (s *SessionStore) Log(rel string, msg agentcore.AgentMessage) error {
	return s.logEntry(rel, msg, nil)
}

// sessionLogEntry nhúng agentcore.Message + _meta tùy chọn.
// agentcore.Message là struct thuần (không có MarshalJSON), nên khi nhúng vào json marshal
// sẽ tự bung ra ở tầng gốc; _meta được kiểm soát bằng omitempty — chỉ khi assistant + Usage != nil
// thì mới chèn, user/tool message không mang _meta, khi parse jsonl cũ _meta=nil là noop.
type sessionLogEntry struct {
	agentcore.Message
	Meta *sessionLogMeta `json:"_meta,omitempty"`
}

type sessionLogMeta struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// logEntry tuần tự hóa message và đính kèm _meta khi cần. meta đã tính sẵn bởi lookupMeta được truyền vào;
// bên trong hàm sẽ quyết định chỉ ghi meta cho message "đã phát sinh usage LLM" (assistant + Usage != nil),
// các message khác giữ nguyên hình thái tuần tự hóa sạch của agentcore.Message.
func (s *SessionStore) logEntry(rel string, msg agentcore.AgentMessage, meta *sessionLogMeta) error {
	m, ok := msg.(agentcore.Message)
	if !ok {
		return nil // Bỏ qua message không phải LLM (ví dụ kiểu tùy biến)
	}
	compacted := compactMessage(m)
	entry := sessionLogEntry{Message: compacted}
	if compacted.Role == agentcore.RoleAssistant && compacted.Usage != nil {
		entry.Meta = usageMeta(compacted.Usage)
		if entry.Meta == nil {
			entry.Meta = meta
		}
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal session message: %w", err)
	}
	data = append(data, '\n')
	return s.io.AppendLine(rel, data)
}

func usageMeta(usage *agentcore.Usage) *sessionLogMeta {
	if usage == nil || (usage.Provider == "" && usage.Model == "") {
		return nil
	}
	return &sessionLogMeta{
		Provider: usage.Provider,
		Model:    usage.Model,
	}
}

// subAgentPath tạo đường dẫn file theo agentName+task.
func (s *SessionStore) subAgentPath(agentName, task string) (string, error) {
	suffix := extractChapter(task)
	if suffix != "" {
		return fmt.Sprintf("meta/sessions/agents/%s-%s.jsonl", agentName, suffix), nil
	}
	key := agentName + "|" + task
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.taskKey[key]; ok {
		return fmt.Sprintf("meta/sessions/agents/%s-%s.jsonl", agentName, cached), nil
	}
	if _, ok := s.seq[agentName]; !ok {
		seq, err := s.maxAgentSequence(agentName)
		if err != nil {
			return "", err
		}
		s.seq[agentName] = seq
	}
	s.seq[agentName]++
	suffix = fmt.Sprintf("%03d", s.seq[agentName])
	s.taskKey[key] = suffix
	return fmt.Sprintf("meta/sessions/agents/%s-%s.jsonl", agentName, suffix), nil
}

func (s *SessionStore) maxAgentSequence(agentName string) (int, error) {
	entries, err := os.ReadDir(s.io.path("meta/sessions/agents"))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read agent sessions: %w", err)
	}

	prefix := agentName + "-"
	maxSeq := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		seq, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".jsonl"))
		if err == nil && seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq, nil
}

var chapterRe = regexp.MustCompile(`(?:chương|Chương)\s*(\d+)`)

func extractChapter(task string) string {
	m := chapterRe.FindStringSubmatch(task)
	if len(m) < 2 {
		return ""
	}
	n, _ := strconv.Atoi(m[1])
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("ch%02d", n)
}

// compactMessage sao chép message và thay thế nội dung lớn.
func compactMessage(m agentcore.Message) agentcore.Message {
	if len(m.Content) == 0 {
		return m
	}
	blocks := make([]agentcore.ContentBlock, len(m.Content))
	copy(blocks, m.Content)

	toolName := toolNameFromMeta(m.Metadata)

	for i := range blocks {
		switch blocks[i].Type {
		case agentcore.ContentText:
			blocks[i].Text = compactText(m.Role, toolName, blocks[i].Text)
		case agentcore.ContentToolCall:
			if blocks[i].ToolCall != nil {
				blocks[i].ToolCall = compactToolCall(blocks[i].ToolCall)
			}
		}
	}
	m.Content = blocks
	return m
}

func toolNameFromMeta(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if v, ok := meta["tool_name"].(string); ok {
		return v
	}
	return ""
}

// compactText nén text content của tool result.
func compactText(role agentcore.Role, toolName, text string) string {
	if role != agentcore.RoleTool || len(text) < 4096 {
		return text
	}
	switch toolName {
	case "novel_context":
		summary := extractJSONField(text, "_loading_summary")
		return fmt.Sprintf("[session_compact: novel_context %dB | %s]", len(text), summary)
	case "read_chapter":
		chars := utf8.RuneCountInString(text)
		return fmt.Sprintf("[session_compact: read_chapter %dký tự | xem chapters/]", chars)
	default:
		if len(text) > 8192 {
			chars := utf8.RuneCountInString(text)
			return fmt.Sprintf("[session_compact: %s %dký tự]", toolName, chars)
		}
		return text
	}
}

// compactToolCall nén các trường nội dung lớn trong args của tool call.
func compactToolCall(tc *agentcore.ToolCall) *agentcore.ToolCall {
	switch tc.Name {
	case "draft_chapter":
		return compactArgsContent(tc, "phần thân Chương N", "drafts/")
	case "save_foundation":
		return compactFoundationArgs(tc)
	default:
		return tc
	}
}

func compactArgsContent(tc *agentcore.ToolCall, label, ref string) *agentcore.ToolCall {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return tc
	}
	contentRaw, ok := args["content"]
	if !ok || len(contentRaw) < 4096 {
		return tc
	}
	var content string
	if err := json.Unmarshal(contentRaw, &content); err != nil {
		// content không phải chuỗi (có thể là object JSON), dùng số byte
		placeholder := fmt.Sprintf("[session_compact: %s %dB | xem %s]", label, len(contentRaw), ref)
		args["content"], _ = json.Marshal(placeholder)
	} else {
		chars := utf8.RuneCountInString(content)
		ch := extractJSONFieldInt(tc.Args, "chapter")
		if ch > 0 {
			label = fmt.Sprintf("phần thân Chương %d", ch)
			ref = fmt.Sprintf("drafts/%02d.draft.md", ch)
		}
		placeholder := fmt.Sprintf("[session_compact: %s %dký tự | xem %s]", label, chars, ref)
		args["content"], _ = json.Marshal(placeholder)
	}
	clone := *tc
	clone.Args, _ = json.Marshal(args)
	return &clone
}

func compactFoundationArgs(tc *agentcore.ToolCall) *agentcore.ToolCall {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(tc.Args, &args); err != nil {
		return tc
	}
	contentRaw, ok := args["content"]
	if !ok || len(contentRaw) < 4096 {
		return tc
	}
	typeName := "foundation"
	var t string
	if json.Unmarshal(args["type"], &t) == nil && t != "" {
		typeName = t
	}
	placeholder := fmt.Sprintf("[session_compact: %s %dB | xem store]", typeName, len(contentRaw))
	args["content"], _ = json.Marshal(placeholder)
	clone := *tc
	clone.Args, _ = json.Marshal(args)
	return &clone
}

// extractJSONField trích xuất giá trị chuỗi của field chỉ định từ chuỗi JSON.
func extractJSONField(jsonStr, field string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return ""
	}
	raw, ok := m[field]
	if !ok {
		return ""
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return string(raw)
	}
	return val
}

func extractJSONFieldInt(data json.RawMessage, field string) int {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return 0
	}
	raw, ok := m[field]
	if !ok {
		return 0
	}
	var val int
	if err := json.Unmarshal(raw, &val); err != nil {
		return 0
	}
	return val
}

// CompactTag là tiền tố nhãn giữ chỗ, tiện cho việc tìm kiếm và khôi phục.
const CompactTag = "[session_compact:"

// IsCompacted kiểm tra xem văn bản đã được nén hay chưa.
func IsCompacted(text string) bool {
	return strings.HasPrefix(text, CompactTag)
}
