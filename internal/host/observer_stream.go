package host

import (
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/utils"
)

// handleSubagentDelta phân luồng văn bản và tham số gọi công cụ của subagent:
// - DeltaText được xuất trực tiếp dưới dạng markdown
// - DeltaToolCall chỉ trích xuất và xuất các trường đối với những công cụ nội dung dài đã biết (như draft_chapter.content); toàn bộ JSON tham số của các công cụ khác bị bỏ qua
func (o *observer) handleSubagentDelta(p *agentcore.ProgressPayload) {
	if p.DeltaKind != agentcore.DeltaToolCall {
		o.emitStreamDelta(p.Delta, false)
		return
	}
	if p.Tool == "" {
		return // Tên công cụ chưa sẵn sàng, thử lại ở delta tiếp theo
	}

	// Khi nhận diện được tên công cụ theo kiểu stream, phát sớm sự kiện TOOL đang chạy để spinner bao phủ toàn bộ giai đoạn LLM tạo nội dung
	// (nếu không, trạng thái "đang chạy" của các công cụ như draft_chapter chỉ hiển thị trong vài chục mili giây Execute thật sự).
	// Khi ProgressToolStart thật sự đến, nếu nhận ra toolStarts đã có bản ghi thì chỉ bổ sung summary.
	o.ensureSubagentToolStarted(p.Agent, p.Tool)
	o.updateToolCallSummaryFromDelta(p.Agent, p.Tool, p.Delta)

	cur, ok := o.streamExtractors[p.Agent]
	// Sau khi args của cùng một lần gọi công cụ đã đóng (khớp } ở tầng trên cùng), vẫn có thể nhận trailing delta:
	// một số provider (đã kiểm chứng với deepseek-v4-flash) sẽ tách args của một lần gọi thành nhiều chunk,
	// và chunk cuối cùng sau `}` còn kèm khoảng trắng hoặc ký tự lặp. Lúc này nếu xử lý theo kiểu "khớp tên công cụ +
	// Done thì dựng lại", extractor mới sẽ lại emit một header ✻ và coi đoạn token cuối
	// là args mới để phân tích. Các delta này là phần đuôi dư thừa, chỉ cần bỏ qua.
	if ok && cur.tool == p.Tool && cur.ext.Done() {
		return
	}
	// Tên công cụ đã đổi hoặc chưa từng tạo: tạo mới.
	if !ok || cur.tool != p.Tool {
		ext := newToolExtractor(p.Tool)
		if ext == nil {
			delete(o.streamExtractors, p.Agent)
			return
		}
		cur = &agentExtractor{tool: p.Tool, ext: ext}
		o.streamExtractors[p.Agent] = cur
	}
	if emitted := cur.ext.Feed(p.Delta); emitted != "" {
		if !cur.emittedAny {
			cur.emittedAny = true
			// streamClear giúp header ✻ của extractor nằm ở điểm bắt đầu round mới, phối hợp với
			// kiểm tra HasPrefix("✻") của renderStreamContent để đi theo đường renderAgentBlock có tô sáng;
			// nếu dùng ensureStreamParagraphBreak thì chỉ chèn dòng trống chứ không mở round, ✻ vẫn sẽ bị
			// phần thinking/nội dung phía trước bọc lại, rơi vào renderChapterBlock và bị vẽ bằng màu mặc định.
			o.streamClear()
			// streamClear đã dọn sạch streamExtractors theo kiểu phòng vệ. cur hiện tại vẫn cần tiếp tục Feed
			// các delta tiếp theo của lần gọi công cụ này, nên phải đăng ký lại nó ngay; nếu không, khi đoạn delta
			// tiếp theo đến sẽ tạo extractor mới và bắt đầu phân tích từ giữa args (chỉ đến dấu `{` của đối tượng lồng
			// nhau mới vào psBeforeKey), khiến timeline_events.time / foreshadow_updates.id
			// v.v. bị coi là trường tầng trên cùng, làm header ✻ xuất hiện lặp lại trên TUI.
			o.streamExtractors[p.Agent] = cur
		}
		o.emitStreamDelta(emitted, false)
	}
}

func (o *observer) emitStreamDelta(delta string, thinking bool) {
	if delta == "" {
		return
	}
	if thinking != o.streamThinking {
		o.emitD(utils.ThinkingSep)
		o.streamThinking = thinking
	}
	o.emitD(delta)
	o.streamHasContent = true
	o.streamLastByte = delta[len(delta)-1]
}

// ensureSubagentToolStarted khi nhận diện theo stream rằng tool_call xuất hiện lần đầu,
// đăng ký trước một lần gọi TOOL đang chạy cho agent đó, để spinner của luồng sự kiện bao phủ
// khoảng thời gian "LLM tạo tham số tool_call theo stream" (thường chiếm 99% tổng thời gian gọi).
// args lúc này chưa hoàn chỉnh, tạm dùng tên công cụ thuần làm summary;
// khi ProgressToolStart thật sự đến sẽ bổ sung summary có tham số.
func (o *observer) ensureSubagentToolStarted(agent, tool string) {
	if agent == "" || tool == "" {
		return
	}
	if _, ok := o.toolStarts[agent]; ok {
		return // Đã có lần gọi đang chạy, đảm bảo idempotent
	}
	o.resetStreamArgLabel(agent, tool)
	id := nextEventID()
	o.toolStarts[agent] = &activeCall{
		id:      id,
		start:   time.Now(),
		summary: tool, // Tạm dùng tên công cụ thuần, khi ProgressToolStart đến có thể cập nhật thành tool(chương N)
		depth:   1,
	}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "TOOL",
		Agent:    agent,
		Summary:  tool,
		Level:    "info",
		Depth:    1,
	})
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = tool
	})
	o.emitFallbackStreamHeader(tool)
}

func (o *observer) resetStreamArgLabel(agent, tool string) {
	key := streamArgKey(agent, tool)
	delete(o.streamArgPrefixes, key)
	delete(o.streamArgLabels, key)
}

// emitFallbackStreamHeader bổ sung một dòng tiêu đề ✻ vào panel stream cho các công cụ chưa cấu hình extractor.
// Cả hai đường đều phải gọi để bảo đảm nhất quán:
//  1. ensureSubagentToolStarted —— subagent stream tool args (DeltaToolCall)
//  2. handleToolUpdate ProgressToolStart —— subagent non-stream tool args
//
// Thiếu bất kỳ đường nào, tiêu đề công cụ của mô hình stream và non-stream sẽ biểu hiện không nhất quán.
func (o *observer) emitFallbackStreamHeader(tool string) {
	if _, has := toolDisplays[tool]; has {
		return // Có extractor, header do extractor tự xuất
	}
	o.streamClear()
	o.emitStreamDelta(streamHeaderFallback(tool)+"\n", false)
}

// streamHeaderFallback tạo văn bản header dạng stream cho các công cụ chưa cấu hình extractor,
// để người dùng vẫn thấy "đang gọi gì" ngay cả với các công cụ đọc nhẹ.
//
// Tiền tố "✻ " là dấu hiệu quy ước của "khối điều phối agent" — khi TUI renderStreamContent thấy
// tiền tố này sẽ đi theo đường renderAgentBlock để render (biểu tượng + label tô sáng + đường phân cách),
// nếu không sẽ rơi vào đường khối nội dung chính dùng màu mặc định của terminal, header trông như nội dung thường và không nổi bật.
func streamHeaderFallback(tool string) string {
	return "✻ " + tool
}

// streamClear thông báo TUI mở một streamRound mới, đồng thời đặt lại trạng thái liên quan đến phân tách đoạn.
// Về mặt logic, round mới là "stream rỗng", nếu không lần emit đầu tiên tiếp theo của extractor sẽ chèn nhầm dòng trống phía trước.
//
// streamThinking bắt buộc cũng phải được đặt lại: emitStreamDelta dùng streamThinking để theo dõi xuyên các lần gọi
// xem đoạn trước có phải là suy nghĩ hay không. Trong round mới chưa xuất bất kỳ nội dung nào,
// lần emit(thinking=false) tiếp theo không nên chèn ThinkingSep nữa. Nếu không, fallback header (như ✻ đọc chương) sẽ bị \x02
// chiếm đầu trước, HasPrefix("✻") của renderStreamContent không khớp, cả đoạn rơi vào đường nội dung chính
// rồi lại bị ThinkingSep tách thành đoạn suy nghĩ, khiến màu title bị vẽ thành màu suy nghĩ.
func (o *observer) streamClear() {
	o.emitC()
	o.streamHasContent = false
	o.streamLastByte = 0
	o.streamThinking = false
	// Trước khi subagent của round trước kết thúc, ProgressToolEnd đã delete; ở đây dọn sạch theo kiểu phòng vệ.
	if len(o.streamExtractors) > 0 {
		o.streamExtractors = make(map[string]*agentExtractor)
	}
}
