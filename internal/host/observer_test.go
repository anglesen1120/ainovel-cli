package host

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

func testObserver(events *[]Event) *observer {
	return &observer{
		emitEv: func(ev Event) {
			*events = append(*events, ev)
		},
		emitD:               func(string) {},
		emitC:               func() {},
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

func TestObserverSubagentRetryEventsUpdateSameLinePerAgent(t *testing.T) {
	var events []Event
	o := testObserver(&events)

	for i := 1; i <= 2; i++ {
		o.handleToolUpdate(agentcore.Event{
			Type: agentcore.EventToolExecUpdate,
			Progress: &agentcore.ProgressPayload{
				Kind:       agentcore.ProgressRetry,
				Agent:      "writer",
				Attempt:    i,
				MaxRetries: 7,
				Message:    "stream read error: INTERNAL_ERROR; received from peer [network, openai]",
				Meta:       json.RawMessage(`{"retry_delay_ms":2000}`),
			},
		})
	}

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 raw update events", len(events))
	}
	if events[0].ID == "" || events[1].ID != events[0].ID {
		t.Fatalf("writer retry events should share ID: %+v", events)
	}
	// Summary khong nhung do tre tinh; UI dua vao RetryAt de dem nguoc. Do tre duoc mang theo duoi dang thoi diem het han, anh chup tinh duoc giu trong Detail cho log.
	if events[1].Agent != "writer" || !strings.Contains(events[1].Summary, "Thử lại (2/7)") {
		t.Fatalf("event = %+v, mong đợi writer retry 2/7 không có độ trễ nội tuyến", events[1])
	}
	if events[1].RetryAt.IsZero() || !strings.Contains(events[1].Detail, "Thử lại (2/7, sau 2s)") {
		t.Fatalf("event = %+v, mong đợi RetryAt deadline + độ trễ tĩnh trong Detail", events[1])
	}
	if events[1].Kind != "network" {
		t.Fatalf("event kind = %q, mong đợi network", events[1].Kind)
	}
}

func TestRetryProgressDelayRequiresReportedDelay(t *testing.T) {
	p := &agentcore.ProgressPayload{Attempt: 3, MaxRetries: 7}
	if got := retryProgressDelay(p); got != 0 {
		t.Fatalf("unreported delay = %s, want 0", got)
	}
	p.Meta = json.RawMessage(`{"retry_delay_ms":4500}`)
	if got := retryProgressDelay(p); got.String() != "4.5s" {
		t.Fatalf("reported delay = %s, want 4.5s", got)
	}
}

func TestErrorKindFromFlattenedMessage(t *testing.T) {
	tests := []struct {
		message string
		want    string
	}{
		{message: "tool argument validation failed: invalid JSON", want: "tool_validation"},
		{message: "bad_response_status_code: Too many concurrent requests [provider, HTTP 500, openai]", want: "overloaded"},
	}
	for _, tt := range tests {
		if got := errorKind(nil, tt.message); got != tt.want {
			t.Fatalf("errorKind(%q) = %q, want %q", tt.message, got, tt.want)
		}
	}
}

func TestObserverDispatchErrorUpdatesSingleEventWithDetail(t *testing.T) {
	var events []Event
	o := testObserver(&events)

	o.dispatchStart("architect_long", "Lập kế hoạch tiểu thuyết dài", "Cần tạo dàn ý phân tầng")
	runErr := errors.New("stream read error: INTERNAL_ERROR; received from peer [network, openai]")
	o.dispatchFinish("architect_long", runErr)

	if len(events) != 2 {
		t.Fatalf("start + failed update chi nen co 2 su kien goc, got %d: %+v", len(events), events)
	}
	start, failed := events[0], events[1]
	if !strings.Contains(start.Detail, "Cần tạo dàn ý phân tầng") || !strings.Contains(start.Detail, "Lập kế hoạch tiểu thuyết dài") {
		t.Fatalf("log bắt đầu DISPATCH phải giữ lại đầy đủ lý do và tác vụ: %+v", start)
	}
	if failed.ID != start.ID || failed.Category != "DISPATCH" || !failed.Failed || failed.Level != "error" {
		t.Fatalf("loi phai cap nhat tai cho DISPATCH: start=%+v failed=%+v", start, failed)
	}
	if failed.Detail != runErr.Error() || failed.Kind != "network" || !strings.Contains(failed.Summary, "INTERNAL_ERROR") {
		t.Fatalf("DISPATCH phai mang theo day du loi va phan loai: %+v", failed)
	}
}

func TestObserverSubagentToolDeltaUpdatesSaveFoundationType(t *testing.T) {
	var events []Event
	o := testObserver(&events)

	o.handleSubagentDelta(&agentcore.ProgressPayload{
		Kind:      agentcore.ProgressToolDelta,
		Agent:     "architect_long",
		Tool:      "save_foundation",
		DeltaKind: agentcore.DeltaToolCall,
		Delta:     `{"type":"premise","content":"# Ten sach`,
	})

	if len(events) < 2 {
		t.Fatalf("events = %d, want start + summary update", len(events))
	}
	if events[0].Category != "TOOL" || events[0].Summary != "save_foundation" || events[0].Depth != 1 {
		t.Fatalf("start event = %+v", events[0])
	}
	if events[1].ID != events[0].ID || events[1].Summary != "save_foundation[premise]" {
		t.Fatalf("summary update = %+v, start = %+v", events[1], events[0])
	}
}

func TestObserverSubagentToolDeltaUpdatesSaveFoundationTypeAcrossChunks(t *testing.T) {
	var events []Event
	o := testObserver(&events)

	for _, delta := range []string{`{"ty`, `pe":"premise","content":"# Ten sach`} {
		o.handleSubagentDelta(&agentcore.ProgressPayload{
			Kind:      agentcore.ProgressToolDelta,
			Agent:     "architect_long",
			Tool:      "save_foundation",
			DeltaKind: agentcore.DeltaToolCall,
			Delta:     delta,
		})
	}

	var summaries []string
	for _, ev := range events {
		summaries = append(summaries, ev.Summary)
	}
	if !strings.Contains(strings.Join(summaries, "\n"), "save_foundation[premise]") {
		t.Fatalf("summaries = %v, want save_foundation[premise]", summaries)
	}
}

func TestObserverToolErrorUpdatesSingleToolEventWithFullDetail(t *testing.T) {
	var events []Event
	o := testObserver(&events)
	fullError := "tool argument validation failed: unexpected end of JSON input\nraw args: " +
		`{"chapter":1,"summary":"` + strings.Repeat("Tan Viet phat hien manh moi trong tai lieu", 30) + "<TAIL>"

	o.handleToolUpdate(agentcore.Event{
		Type: agentcore.EventToolExecUpdate,
		Progress: &agentcore.ProgressPayload{
			Kind:  agentcore.ProgressToolStart,
			Agent: "writer",
			Tool:  "edit_chapter",
		},
	})
	o.handleToolUpdate(agentcore.Event{
		Type: agentcore.EventToolExecUpdate,
		Progress: &agentcore.ProgressPayload{
			Kind:    agentcore.ProgressToolError,
			Agent:   "writer",
			Tool:    "edit_chapter",
			Message: fullError,
		},
	})

	if len(events) != 2 {
		t.Fatalf("start + failed update chi nen co 2 su kien goc, got %d: %+v", len(events), events)
	}
	start, failed := events[0], events[1]
	if failed.ID == "" || failed.ID != start.ID || !failed.Failed || failed.Category != "TOOL" || failed.Level != "error" {
		t.Fatalf("su kien loi phai cap nhat tai cho dong TOOL: start=%+v failed=%+v", start, failed)
	}
	if !strings.Contains(failed.Summary, "tool argument validation failed") ||
		!strings.Contains(failed.Detail, fullError) || !strings.Contains(failed.Detail, "<TAIL>") {
		t.Fatalf("su kien loi phai dong thoi giu lai tom tat UI va chi tiet log day du: %+v", failed)
	}
	if len(failed.Summary) >= len(failed.Detail) {
		t.Fatalf("UI Summary phai ngan hon Detail day du: summary=%d detail=%d", len(failed.Summary), len(failed.Detail))
	}
}
