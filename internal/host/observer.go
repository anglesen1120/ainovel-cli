package host

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// errorKind classifies a runtime error into a stable, short label for log
// filtering and alert routing. Returns "" when no special tag applies.
//
// err is the live error chain (may be nil after JSON serialization); msg is
// the rendered string fallback used when the chain has been flattened
// (e.g. inside sub-agent JSON results).
func errorKind(err error, msg string) string {
	if kind := agentcore.ErrorKind(err); kind != "" && kind != "unknown" {
		return kind
	}
	if msg == "" {
		return ""
	}
	if kind := agentcore.ErrorKind(errors.New(msg)); kind != "unknown" {
		return kind
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "tool argument validation failed"):
		return "tool_validation"
	case strings.Contains(lower, "too many concurrent requests"):
		return "overloaded"
	// providerError se đính kiểu có cấu trúc của litellm vào cuối chuỗi văn bản.
	// Bản thân HTTP/2 INTERNAL_ERROR không có từ khóa phân loại nào, cứ giữ nhãn network rõ ràng này là đủ.
	case strings.Contains(lower, "[network,"):
		return "network"
	}
	return ""
}

// Bộ đếm ID sự kiện tăng đơn điệu; kết hợp với dấu thời gian để tạo ID ổn định.
var eventIDCounter uint64

func nextEventID() string {
	return fmt.Sprintf("e%d", atomic.AddUint64(&eventIDCounter, 1))
}

// activeCall ghi lại ID, thời điểm bắt đầu và summary của một lần gọi đang diễn ra
// (TOOL / DISPATCH). summary sẽ được điền lại vào finish Event khi hoàn tất,
// bảo đảm replay (runtime queue) có thể khôi phục nội dung dòng.
type activeCall struct {
	id      string
	start   time.Time
	summary string
	depth   int
}

// observer chiếu tiến trình của Engine dispatch và Worker lên kênh đầu ra của Host.
// Nó chỉ quan sát, không tham gia vào bất kỳ quyết định điều khiển nào.
type observer struct {
	emitEv  func(Event)
	emitD   func(string)
	emitC   func()
	store   *storepkg.Store // dùng cho lưu bền vững runtime queue (ReplayQueue tiêu thụ)
	agents  map[string]*agentState
	agentMu sync.Mutex

	// aborting được Host đặt trong các điểm vào Abort()/Close(), và xóa trong Start/Resume/Continue.
	// Khi được đặt, mọi sự kiện lỗi phát sinh từ context-cancel đều bị chặn (vừa đúng kỳ vọng của người dùng,
	// vừa tránh trùng với sự kiện "người dùng tạm dừng thủ công"). Lỗi thật sự (không phải cancel) vẫn được báo bình thường.
	aborting atomic.Bool

	streamThinking      bool
	lastThinkingByAgent map[string]string          // agent → văn bản thinking tích lũy gần nhất (dùng để trích delta gia tăng)
	dispatchStarts      map[string]*activeCall     // agent đã dispatch → một lời gọi DISPATCH đang diễn ra
	toolStarts          map[string]*activeCall     // agent → một lời gọi TOOL đang diễn ra
	streamExtractors    map[string]*agentExtractor // agent → bộ trích xuất nội dung hiện tại cho tham số JSON của lời gọi tool
	streamArgPrefixes   map[string]string          // agent/tool → tiền tố luồng tham số, dùng để nhận diện sớm nhãn nhẹ
	streamArgLabels     map[string]string          // agent/tool → tên hiển thị đã nhận diện sớm từ luồng tham số
	retryEvents         map[string]string          // retry scope → event ID, cập nhật tại chỗ trên cùng một dòng (2/7)
	streamHasContent    bool                       // streamRound hiện tại đã xuất nội dung hay chưa (để quyết định có cần ngăn cách đoạn hay không)
	streamLastByte      byte                       // byte cuối của lần xuất stream gần nhất (dùng để bù chính xác ký tự xuống dòng)
}

// agentExtractor ghi lại tên tool đang được trích xuất của một agent cùng với
// thể hiện bộ trích xuất. Tên tool dùng để phát hiện "đã bắt đầu một lời gọi tool mới",
// tránh bộ nhớ đệm bị nhiễm bởi phần dư của vòng trước.
type agentExtractor struct {
	tool       string
	ext        *jsonFieldExtractor
	emittedAny bool // extractor này đã xuất ra bất kỳ nội dung nào hay chưa; dùng để bù ngăn cách đoạn trước lần xuất đầu tiên
}

type agentState struct {
	name    string
	state   string
	tool    string
	summary string
	turn    int
	context AgentContextSnapshot
	updated time.Time
}

func newObserver(s *storepkg.Store, emitEv func(Event), emitD func(string), emitC func()) *observer {
	return &observer{
		emitEv:              emitEv,
		emitD:               emitD,
		emitC:               emitC,
		store:               s,
		agents:              make(map[string]*agentState),
		lastThinkingByAgent: make(map[string]string),
		dispatchStarts:      make(map[string]*activeCall),
		toolStarts:          make(map[string]*activeCall),
		streamExtractors:    make(map[string]*agentExtractor),
		streamArgPrefixes:   make(map[string]string),
		streamArgLabels:     make(map[string]string),
		retryEvents:         make(map[string]string),
	}
}

// ── Cổng vào điều khiển trực tiếp của Engine ──
//
// Engine chạy Worker trực tiếp, nguồn sự kiện được chia thành hai nhánh:
//  1. dispatchStart/dispatchFinish —— Engine gọi trực tiếp tại ranh giới dispatch (dòng DISPATCH)
//  2. workerProgress —— cầu nối tiến trình của Worker (ctx ToolProgress),
//     được handleToolUpdate xử lý thống nhất cho TOOL / nội dung stream / thinking / retry / context
//     (dòng TOOL / nội dung stream / thinking / retry / context).

// dispatchStart ghi lại thời điểm bắt đầu một lần dispatch Worker và phát dòng DISPATCH.
func (o *observer) dispatchStart(agent, task, reason string) {
	summary := dispatchSummary(agent, task)
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = ""
		a.summary = fmt.Sprintf("engine → %s", summary)
	})
	id := nextEventID()
	o.dispatchStarts[agent] = &activeCall{id: id, start: time.Now(), summary: summary}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "DISPATCH",
		Agent:    agent,
		Summary:  summary,
		Detail:   dispatchDetail(task, reason),
		Level:    "info",
	})
}

// dispatchFinish chuyển dòng DISPATCH sang trạng thái hoàn tất và đặt lại trạng thái Worker;
// đồng thời dọn các dòng TOOL mồ côi theo tên Worker đó (các đường abort/lỗi có thể thiếu ProgressToolEnd).
func (o *observer) dispatchFinish(agent string, runErr error) {
	o.updateAgent(agent, func(a *agentState) {
		a.state = "idle"
		a.tool = ""
	})
	delete(o.lastThinkingByAgent, agent)
	if call, ok := o.toolStarts[agent]; ok {
		delete(o.toolStarts, agent)
		delete(o.streamExtractors, agent)
		o.emitCallFinish(call, "TOOL", agent, runErr)
	}
	if call, ok := o.dispatchStarts[agent]; ok {
		delete(o.dispatchStarts, agent)
		o.emitCallFinish(call, "DISPATCH", agent, runErr)
	}
	o.streamClear()
}

// workerProgress thích ứng cầu nối tiến trình của Worker thành xử lý ToolExecUpdate sẵn có.
func (o *observer) workerProgress(p agentcore.ProgressPayload) {
	payload := p
	o.handleToolUpdate(agentcore.Event{Type: agentcore.EventToolExecUpdate, Progress: &payload})
}

func (o *observer) finalize() {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	for _, a := range o.agents {
		a.state = "idle"
		a.tool = ""
	}
}

// setAborting được Host gọi ở các điểm chuyển vòng đời như Abort/Close/Start, để kiểm soát
// việc có cần chặn các sự kiện phát sinh từ loại "context canceled" hay không (tránh trùng với "người dùng tạm dừng thủ công").
func (o *observer) setAborting(v bool) { o.aborting.Store(v) }

func (o *observer) retryEventID(scope string, attempt int) string {
	if strings.TrimSpace(scope) == "" {
		scope = "engine"
	}
	if o.retryEvents == nil {
		o.retryEvents = make(map[string]string)
	}
	if attempt <= 1 || o.retryEvents[scope] == "" {
		o.retryEvents[scope] = nextEventID()
	}
	return o.retryEvents[scope]
}

// emitAndLog dùng cho trạng thái "bắt đầu" của các sự kiện kiểu lời gọi: gửi cho TUI nhưng không ghi vào runtime queue,
// để tránh replay bị lặp "một dòng bắt đầu, một dòng hoàn tất". slog được host.emitEvent ghi log thống nhất.
func (o *observer) emitAndLog(ev Event) {
	o.emitEv(ev)
}

// persistEvent ghi sự kiện vào runtime queue (slog được host.emitEvent ghi log thống nhất).
func (o *observer) persistEvent(ev Event) {
	if o.store == nil || o.store.Runtime == nil {
		return
	}
	priority := domain.RuntimePriorityBackground
	switch {
	case ev.Level == "error":
		priority = domain.RuntimePriorityControl
	case ev.Category == "SYSTEM" || ev.Category == "ERROR":
		priority = domain.RuntimePriorityControl
	}
	if _, err := o.store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Time:     ev.Time,
		Priority: priority,
		Category: ev.Category,
		Summary:  ev.Summary,
		Payload:  ev,
	}); err != nil {
		slog.Warn("không thể lưu bền vững sự kiện runtime", "module", "observer", "category", ev.Category, "err", err)
	}
}

func (o *observer) updateAgent(name string, fn func(*agentState)) {
	if name == "" {
		return
	}
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	a, ok := o.agents[name]
	if !ok {
		a = &agentState{name: name, state: "idle"}
		o.agents[name] = a
	}
	fn(a)
	a.updated = time.Now()
}

func (o *observer) agentSnapshots() []AgentSnapshot {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	snaps := make([]AgentSnapshot, 0, len(o.agents))
	for _, a := range o.agents {
		snaps = append(snaps, AgentSnapshot{
			Name:      a.name,
			State:     a.state,
			Summary:   a.summary,
			Tool:      a.tool,
			Turn:      a.turn,
			Context:   a.context,
			UpdatedAt: a.updated,
		})
	}
	return snaps
}
