package startup

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/host"
)

// CoCreateSession chứa trạng thái không thuộc UI của chế độ đồng sáng tạo.
type CoCreateSession struct {
	history        []host.CoCreateMessage
	draftPrompt    string
	ready          bool
	streamReply    string
	streamThinking string
	suggestions    []string
}

func NewCoCreateSession(initial string) *CoCreateSession {
	return &CoCreateSession{
		history: []host.CoCreateMessage{
			{Role: "user", Content: strings.TrimSpace(initial)},
		},
	}
}

func (s *CoCreateSession) History() []host.CoCreateMessage {
	if s == nil {
		return nil
	}
	return append([]host.CoCreateMessage(nil), s.history...)
}

func (s *CoCreateSession) ApplyReply(reply host.CoCreateReply) {
	if s == nil {
		return
	}
	s.streamReply = ""
	s.streamThinking = ""
	// history lưu Raw đầy đủ của assistant (gồm [DRAFT]), để model thấy được ở lượt sau
	// bản nháp do chính mình viết ở lượt trước và tiếp tục cập nhật; chỉ lưu Message sẽ khiến [DRAFT] hoàn toàn
	// không vào ngữ cảnh, model phải tự tổng hợp lại từ hội thoại mỗi lượt, dễ mất chi tiết ban đầu. Ở đường fallback
	// Raw == Message, tương đương.
	text := strings.TrimSpace(reply.Raw)
	if text == "" {
		text = strings.TrimSpace(reply.Message)
	}
	if text != "" {
		s.history = append(s.history, host.CoCreateMessage{Role: "assistant", Content: text})
	}
	// chỉ ghi đè draft khi Prompt không rỗng: đường fallback parse trả về Prompt="", khi đó
	// phải giữ draft lượt trước, nếu không “chỉ lệnh sáng tác hiện tại” người dùng tích lũy sẽ bị phản hồi bị cắt xóa sạch.
	if prompt := strings.TrimSpace(reply.Prompt); prompt != "" {
		s.draftPrompt = prompt
	}
	s.ready = reply.Ready
	// suggestions ghi đè trực tiếp (bao gồm ghi đè thành rỗng): phần hướng dẫn của mỗi vòng chỉ có ý nghĩa tại thời điểm đó.
	s.suggestions = append(s.suggestions[:0], reply.Suggestions...)
}

func (s *CoCreateSession) AppendUser(text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	// người dùng đã quyết định câu tiếp theo, suggestions lập tức hết hiệu lực để tránh khi AI chưa trả lời
	// gợi ý cũ treo trên ô nhập gây hiểu nhầm.
	s.suggestions = nil
	s.history = append(s.history, host.CoCreateMessage{Role: "user", Content: text})
}

// ApplyDelta nhận dữ liệu tích lũy từ stream; kind="thinking" ghi vào luồng suy luận, "reply" ghi vào bản xem trước phản hồi.
// hai luồng được tích lũy riêng; UI có thể tô màu theo khối để người dùng thấy LLM đang làm việc trong giai đoạn thinking.
func (s *CoCreateSession) ApplyDelta(kind, text string) {
	if s == nil {
		return
	}
	text = strings.TrimSpace(text)
	switch kind {
	case host.CoCreateProgressThinking:
		s.streamThinking = text
	case host.CoCreateProgressReply:
		s.streamReply = text
	}
}

func (s *CoCreateSession) StreamReply() string {
	if s == nil {
		return ""
	}
	return s.streamReply
}

func (s *CoCreateSession) StreamThinking() string {
	if s == nil {
		return ""
	}
	return s.streamThinking
}

func (s *CoCreateSession) DraftPrompt() string {
	if s == nil {
		return ""
	}
	return s.draftPrompt
}

func (s *CoCreateSession) Suggestions() []string {
	if s == nil {
		return nil
	}
	return s.suggestions
}

func (s *CoCreateSession) Ready() bool {
	if s == nil {
		return false
	}
	return s.ready
}

func (s *CoCreateSession) CanStart() bool {
	return strings.TrimSpace(s.DraftPrompt()) != ""
}

func (s *CoCreateSession) InitialInput() string {
	if s == nil || len(s.history) == 0 {
		return ""
	}
	return strings.TrimSpace(s.history[0].Content)
}

func (s *CoCreateSession) BuildPrompt() (string, error) {
	if s == nil || !s.CanStart() {
		return "", fmt.Errorf("prompt bản nháp co-create là bắt buộc")
	}
	return s.DraftPrompt(), nil
}
