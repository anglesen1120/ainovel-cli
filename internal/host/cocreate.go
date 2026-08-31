package host

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Đồng sáng tạo khi khởi động nguội: làm rõ yêu cầu từ con số không, tạo ra chỉ dẫn sáng tác cho cả cuốn sách.
const coCreateSystemPrompt = `Bạn là một trợ lý đồng sáng tạo tiểu thuyết. Nhiệm vụ của bạn không phải là bắt đầu viết tiểu thuyết ngay, mà là thông qua nhiều lượt đối thoại ngắn để giúp người dùng làm rõ nhu cầu sáng tác, đồng thời liên tục tổng hợp một đoạn chỉ dẫn sáng tác bằng tiếng Việt có thể giao trực tiếp cho công cụ sáng tác.

Mỗi lượt trả lời phải xuất đúng theo định dạng XML sau, gồm bốn thẻ, xuất hiện lần lượt; mỗi thẻ đều phải có thẻ mở và thẻ đóng chính xác:

<reply>
Phần trả lời tự nhiên bằng tiếng Việt cho người dùng: trước hết phản hồi đầu vào của người dùng, sau đó đặt tối đa 1 đến 2 câu hỏi quan trọng nhất ở thời điểm hiện tại. Nếu thông tin đã đủ để bắt đầu sáng tác, hãy nói với người dùng rằng có thể nhấn Ctrl+S để bắt đầu.
</reply>

<draft>
Bản nháp chỉ dẫn sáng tác hoàn chỉnh hiện tại, dùng Markdown: bắt đầu trực tiếp từ tiêu đề cấp hai, ví dụ "## Chủ đề", "## Yếu tố then chốt", "## Thông tin cần làm rõ"; dùng gạch đầu dòng để liệt kê ý chính. Mỗi lượt đều phải **cập nhật tích lũy** trên các kết luận đã có, hấp thụ ý định mới nhất của người dùng; ngay cả khi lượt này không có nội dung mới, cũng phải viết lại nguyên văn toàn bộ bản nháp hoàn chỉnh, không được lược bỏ, không được viết các chỗ giữ chỗ kiểu "(giữ nguyên lượt trước)".
</draft>
` + coCreateProtocolTail

// Đồng sáng tạo theo giai đoạn: tiểu thuyết đã viết một phần, lập kế hoạch hướng đi cho "giai đoạn tiếp theo". Bên gọi cần nối phần tóm tắt trạng thái câu chuyện hiện tại
// vào sau prompt này (đoạn "## Trạng thái câu chuyện hiện tại"), để mô hình lập kế hoạch dựa trên nội dung đã viết.
const stageCoCreateSystemPrompt = `Bạn là một trợ lý "đồng sáng tạo theo giai đoạn" cho tiểu thuyết. Cuốn tiểu thuyết này đã được viết một phần (tiến độ xem trong "Trạng thái câu chuyện hiện tại" bên dưới). Người dùng tạm dừng lại, muốn cùng bạn lập kế hoạch cho hướng đi của "giai đoạn tiếp theo", rồi mới tiếp tục sáng tác.

Nhiệm vụ của bạn không phải là viết tiếp phần nội dung chính, mà là thông qua nhiều lượt đối thoại ngắn để giúp người dùng suy nghĩ rõ phần sau này (một số chương tiếp theo / hồi tiếp theo / quyển tiếp theo) nên đi theo hướng nào, đồng thời liên tục tổng hợp một đoạn "brief hướng đi tiếp theo" để công cụ sáng tác dựa vào đó tiếp tục triển khai.

Luật bắt buộc: mọi đề xuất phải nhất quán với tình tiết, nhân vật và chi tiết gài trước đã xảy ra trong "Trạng thái câu chuyện hiện tại"; tuyệt đối không lật ngược hoặc bỏ qua nội dung đã viết; chỉ lập kế hoạch "tiếp theo đi như thế nào", không thiết kế lại cả cuốn sách.

Mỗi lượt trả lời phải xuất đúng theo định dạng XML sau, gồm bốn thẻ, xuất hiện lần lượt; mỗi thẻ đều phải có thẻ mở và thẻ đóng chính xác:

<reply>
Phần trả lời tự nhiên bằng tiếng Việt cho người dùng: trước hết phản hồi đầu vào của người dùng, sau đó đặt tối đa 1 đến 2 câu hỏi quan trọng nhất ở thời điểm hiện tại. Nếu hướng đi tiếp theo đã đủ rõ, hãy nói với người dùng rằng có thể nhấn Ctrl+S để giao hướng đi cho công cụ sáng tác và tiếp tục sáng tác.
</reply>

<draft>
"Brief hướng đi tiếp theo" hoàn chỉnh hiện tại, dùng Markdown: bắt đầu trực tiếp từ tiêu đề cấp hai, ví dụ "## Hướng đi tiếp theo", "## Bước ngoặt then chốt", "## Chi tiết gài trước cần thu lại", "## Nhịp độ và dung lượng"; dùng gạch đầu dòng để liệt kê ý chính. Mỗi lượt đều phải **cập nhật tích lũy** trên các kết luận đã có, hấp thụ ý định mới nhất của người dùng; ngay cả khi lượt này không có nội dung mới, cũng phải viết lại nguyên văn toàn bộ brief hoàn chỉnh, không được lược bỏ, không được viết các chỗ giữ chỗ kiểu "(giữ nguyên lượt trước)".
</draft>
` + coCreateProtocolTail

// coCreateProtocolTail là phần đuôi giao thức đầu ra dùng chung cho hai chế độ đồng sáng tạo (<ready> / <suggestions> + quy chuẩn đầu ra).
// Hai chế độ chỉ khác nhau ở ngữ cảnh mở đầu và ngữ nghĩa của <draft>; giao thức hoàn toàn giống nhau.
const coCreateProtocolTail = `
<ready>false</ready>

<suggestions>
1-3 câu "những điều người dùng có thể muốn nói tiếp theo", mỗi dòng một câu bắt đầu bằng "- ". Đây là gợi ý dẫn dắt khi người dùng bí ý,
nhấn phím số sẽ điền vào ô nhập, người dùng có thể chỉnh sửa rồi gửi.

Yêu cầu:
- Đứng trên giọng điệu của người dùng, giống như lời người dùng nói với bạn, không viết thành câu hỏi ngược của trợ lý.
- Mỗi câu không quá 25 chữ, đa dạng cách diễn đạt, tránh rập khuôn.
- Đưa ra khuynh hướng / lựa chọn / ý định bổ sung, không viết thay người dùng một thiết lập hoàn chỉnh trong một câu.
</suggestions>

Quy chuẩn đầu ra:
- Bắt buộc sử dụng bốn thẻ XML: <reply> / <draft> / <ready> / <suggestions>, thẻ nào cũng phải có mở và đóng hoàn chỉnh.
- Tên thẻ chỉ được dùng chữ cái tiếng Anh viết thường, không đổi thành <REPLY> / <REWRITE> / <phan_hoi> hay bất kỳ biến thể nào.
- Không thêm bất kỳ giải thích, suy nghĩ hay hàng rào mã nào bên ngoài thẻ.
- Trong <draft> được phép có Markdown nhiều dòng, viết xuống dòng trực tiếp, không cần escape.
- <ready> chỉ viết true hoặc false. Khi thông tin đã đủ thì điền true.
- Khi <ready>true</ready>, <suggestions> có thể để trống (giữ thẻ rỗng <suggestions></suggestions> là được).`

// CoCreateProgressKind đánh dấu kiểu nội dung của callback dạng streaming.
const (
	CoCreateProgressThinking = "thinking"
	CoCreateProgressReply    = "reply"
)

// Đầu ra thẻ XML bốn đoạn. Phong cách XML vững hơn marker bằng ngoặc vuông: trong dữ liệu huấn luyện của Claude/GPT
// có rất nhiều định dạng kiểu , nên mô hình hầu như sẽ không đổi <reply> thành <REWRITE>
// hoặc biến thể khác; thẻ đóng cũng giúp cắt đoạn giữa luồng chính xác hơn (không phụ thuộc vào việc tìm marker kế tiếp để cắt đuôi).
const (
	tagReply       = "reply"
	tagDraft       = "draft"
	tagReady       = "ready"
	tagSuggestions = "suggestions"
)

func coCreateStream(ctx context.Context, models *bootstrap.ModelSet, sessions *store.SessionStore, sysPrompt string, history []CoCreateMessage, onProgress func(kind, text string)) (reply CoCreateReply, err error) {
	if len(history) == 0 {
		return CoCreateReply{}, fmt.Errorf("lịch sử đồng sáng tạo rỗng")
	}

	model := models.ForRole("thinking")

	msgs := []agentcore.Message{agentcore.SystemMsg(sysPrompt)}
	for _, item := range history {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Role)) {
		case "assistant":
			msgs = append(msgs, assistantMsg(content))
		default:
			msgs = append(msgs, agentcore.UserMsg(content))
		}
	}

	var raw, thinking strings.Builder

	// Để điều tra các vấn đề ngẫu nhiên như "phản hồi đồng sáng tạo rỗng", cần thấy mô hình thực sự trả về gì.
	// Mỗi lượt được ghi toàn bộ xuống <output>/meta/sessions/cocreate.jsonl, cùng vị trí với nhật ký session của quá trình sáng tác chính thức.
	start := time.Now()
	defer func() {
		if sessions == nil {
			return
		}
		if logErr := sessions.LogCoCreate(coCreateLogEntry{
			Time:         time.Now(),
			DurationMS:   time.Since(start).Milliseconds(),
			InputHistory: history,
			RawResponse:  raw.String(),
			RawLen:       len([]rune(raw.String())),
			Thinking:     thinking.String(),
			ParsedReply:  reply.Message,
			ParsedDraft:  reply.Prompt,
			ParsedReady:  reply.Ready,
			ParsedSugs:   reply.Suggestions,
			Error:        errString(err),
		}); logErr != nil {
			slog.Warn("ghi nhat ky phien dong sang tao xuong dia that bai", "module", "cocreate", "err", logErr)
		}
	}()

	streamCh, err := model.GenerateStream(ctx, msgs, nil, agentcore.WithMaxTokens(2048))
	if err != nil {
		return CoCreateReply{}, fmt.Errorf("tạo phản hồi đồng sáng tạo: %w", err)
	}

	var streamed bool
	for ev := range streamCh {
		switch ev.Type {
		case agentcore.StreamEventThinkingDelta:
			thinking.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressThinking, thinking.String())
			}
		case agentcore.StreamEventTextDelta:
			streamed = true
			raw.WriteString(ev.Delta)
			if onProgress != nil {
				onProgress(CoCreateProgressReply, extractReplyPreview(raw.String()))
			}
		case agentcore.StreamEventDone:
			if !streamed {
				raw.WriteString(ev.Message.TextContent())
			}
		case agentcore.StreamEventError:
			if ev.Err != nil {
				return CoCreateReply{}, fmt.Errorf("tạo phản hồi đồng sáng tạo: %w", ev.Err)
			}
			return CoCreateReply{}, fmt.Errorf("tạo phản hồi đồng sáng tạo thất bại")
		}
	}

	// Dự phòng kênh: các mô hình thiên về suy luận (R1/GLM-Z1/QwQ v.v.) đôi khi ghi toàn bộ câu trả lời vào
	// reasoning_content rồi không chuyển lại kênh final answer, khiến raw rỗng nhưng thinking chứa
	// đầy đủ bốn đoạn. Khi đó, xem meta/sessions/cocreate.jsonl để lấy thẳng thinking làm raw phân tích;
	// tầng giao thức đã có xử lý hạ cấp (khi không có marker [REPLY] thì coi cả đoạn là reply), nên trải nghiệm UI không đổi.
	rawText := raw.String()
	if strings.TrimSpace(rawText) == "" {
		if t := strings.TrimSpace(thinking.String()); t != "" {
			rawText = t
		}
	}
	reply, err = parseCoCreateResponse(rawText)
	return reply, err
}

// coCreateLogEntry là cấu trúc một dòng ghi vào meta/sessions/cocreate.jsonl.
// Cách đặt tên trường gần với thói quen tra cứu trực tiếp jsonl (snake_case), thuận tiện lọc bằng jq.
type coCreateLogEntry struct {
	Time         time.Time         `json:"time"`
	DurationMS   int64             `json:"duration_ms"`
	InputHistory []CoCreateMessage `json:"input_history"`
	RawResponse  string            `json:"raw_response"`
	RawLen       int               `json:"raw_len"`
	Thinking     string            `json:"thinking,omitempty"`
	ParsedReply  string            `json:"parsed_reply"`
	ParsedDraft  string            `json:"parsed_draft"`
	ParsedReady  bool              `json:"parsed_ready"`
	ParsedSugs   []string          `json:"parsed_sugs,omitempty"`
	Error        string            `json:"error,omitempty"`
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func assistantMsg(text string) agentcore.Message {
	return agentcore.Message{
		Role:      agentcore.RoleAssistant,
		Content:   []agentcore.ContentBlock{agentcore.TextBlock(text)},
		Timestamp: time.Now(),
	}
}

// parseCoCreateResponse phân tích đầu ra thẻ XML. Nếu mô hình không tuân thủ giao thức (nói trực tiếp bằng ngôn ngữ tự nhiên),
// cả đoạn sẽ hiển thị như reply, draft để trống để session giữ lại lượt trước.
func parseCoCreateResponse(raw string) (CoCreateReply, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return CoCreateReply{}, fmt.Errorf("phản hồi đồng sáng tạo rỗng")
	}

	reply, draft, ready, suggestions := splitCoCreateMarkers(raw)
	if reply == "" {
		// Mô hình không tuân thủ giao thức XML: coi cả đoạn là reply.
		return CoCreateReply{Message: raw, Prompt: "", Ready: false, Raw: raw}, nil
	}
	return CoCreateReply{
		Message:     reply,
		Prompt:      draft,
		Ready:       ready,
		Suggestions: suggestions,
		Raw:         raw,
	}, nil
}

// splitCoCreateMarkers tách văn bản theo bốn thẻ XML.
// Thẻ có thể bị thiếu (giữa luồng streaming hoặc mô hình bỏ sót); phần thiếu tương ứng sẽ là rỗng / false / nil.
// Khi thiếu thẻ đóng, extractTagContent sẽ lấy đến cuối chuỗi và vẫn cố gắng phân tích.
func splitCoCreateMarkers(s string) (reply, draft string, ready bool, suggestions []string) {
	reply = extractTagContent(s, tagReply)
	draft = extractTagContent(s, tagDraft)
	readyStr := strings.ToLower(extractTagContent(s, tagReady))
	ready = readyStr == "true" || readyStr == "yes"
	suggestions = parseSuggestions(extractTagContent(s, tagSuggestions))
	return
}

// extractTagContent móc phần văn bản giữa <tag>...</tag> từ s.
// Xử lý dự phòng cho ba tình huống lỗi ngẫu nhiên, tránh đi thẳng vào hạ cấp làm mất trường:
//  1. Có mở không đóng (giữa luồng streaming) -> cắt trước thẻ mở đã biết kế tiếp
//  2. Không mở có đóng (mô hình typo, như viết <suggestions> thành <uggestions>) -> bắt đầu từ vị trí kết thúc của
//     thẻ đóng hoàn chỉnh đã biết gần nhất, đến trước </tag>
//  3. reply hoàn toàn không có thẻ mở (mô hình mở đầu trực tiếp bằng ngôn ngữ tự nhiên, cuối dán </reply>) -> từ đầu đến </reply>
func extractTagContent(s, tag string) string {
	open := "<" + tag + ">"
	closeTag := "</" + tag + ">"
	oIdx := strings.Index(s, open)
	if oIdx >= 0 {
		rest := s[oIdx+len(open):]
		if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
			return strings.TrimSpace(rest[:cIdx])
		}
		// Có mở không đóng -> cắt trước thẻ mở đã biết kế tiếp
		for _, other := range []string{"<reply>", "<draft>", "<ready>", "<suggestions>"} {
			if other == open {
				continue
			}
			if idx := strings.Index(rest, other); idx >= 0 {
				rest = rest[:idx]
			}
		}
		return strings.TrimSpace(rest)
	}

	// Không mở có đóng -> bắt đầu từ vị trí kết thúc của thẻ đóng hoàn chỉnh đã biết gần nhất, đến </tag>.
	if cIdx := strings.Index(s, closeTag); cIdx >= 0 {
		prefix := s[:cIdx]
		start := 0
		for _, t := range []string{"</reply>", "</draft>", "</ready>", "</suggestions>"} {
			if t == closeTag {
				continue
			}
			if i := strings.LastIndex(prefix, t); i >= 0 {
				if end := i + len(t); end > start {
					start = end
				}
			}
		}
		return strings.TrimSpace(prefix[start:])
	}
	return ""
}

// parseSuggestions móc từng dòng trong đoạn <suggestions>, loại bỏ tiền tố danh sách "- " / "* " / "1. " v.v.
// Giữ tối đa 3 câu; bỏ qua dòng trống, quá ngắn (<2 ký tự), hoặc cả dòng trông giống thẻ XML (phần sót lại do typo thẻ mở,
// ví dụ <uggestions>).
func parseSuggestions(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Cả dòng trông giống thẻ XML -> bỏ qua (tránh typo thẻ mở làm bẩn dữ liệu)
		if strings.HasPrefix(line, "<") && strings.HasSuffix(line, ">") {
			continue
		}
		// Bóc tiền tố danh sách
		switch {
		case strings.HasPrefix(line, "- "):
			line = strings.TrimSpace(line[2:])
		case strings.HasPrefix(line, "* "):
			line = strings.TrimSpace(line[2:])
		case isOrderedSuggestion(line):
			line = stripOrderedPrefix(line)
		}
		if len([]rune(line)) < 2 {
			continue
		}
		out = append(out, line)
		if len(out) >= 3 {
			break
		}
	}
	return out
}

// isOrderedSuggestion kiểm tra đầu dòng có dạng "1. " / "12. " (chữ số + dấu chấm + khoảng trắng) hay không.
func isOrderedSuggestion(line string) bool {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	return i > 0 && i+1 < len(line) && line[i] == '.' && line[i+1] == ' '
}

func stripOrderedPrefix(line string) string {
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i+1 >= len(line) {
		return line
	}
	return strings.TrimSpace(line[i+2:])
}

// extractReplyPreview xem trước dạng streaming: khi raw vẫn đang tăng, cung cấp cho UI một đoạn văn bản có thể hiển thị.
// Tìm nội dung sau <reply>, cắt ở </reply> hoặc trước thẻ mở kế tiếp <draft>.
// Khi mô hình tuân thủ một nửa (thiếu thẻ mở <reply>), phần từ đầu đến </reply> hoặc <draft> đều được tính là reply.
func extractReplyPreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	open := "<" + tagReply + ">"
	closeTag := "</" + tagReply + ">"
	draftOpen := "<" + tagDraft + ">"

	rest := trimmed
	if rIdx := strings.Index(trimmed, open); rIdx >= 0 {
		rest = trimmed[rIdx+len(open):]
	}
	if cIdx := strings.Index(rest, closeTag); cIdx >= 0 {
		return strings.TrimSpace(rest[:cIdx])
	}
	if dIdx := strings.Index(rest, draftOpen); dIdx >= 0 {
		rest = rest[:dIdx]
	}
	return strings.TrimSpace(rest)
}
