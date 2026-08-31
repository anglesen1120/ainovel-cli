package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartCommandLoadsPromptFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "outline files")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "story outline.md")
	want := "Thiết lập thế giới\n\nDàn ý quyển một"
	if err := os.WriteFile(path, []byte("  "+want+"  "), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel(nil, "")
	cmd, ok := parseSlashCommand("/start " + path)
	if !ok {
		t.Fatal("/start phải parse thành slash command")
	}
	prompt, err := prepareFileStart(cmd.args)
	if err != nil {
		t.Fatal(err)
	}
	if prompt != want {
		t.Fatalf("prompt = %q, want full file content", prompt)
	}
	next, startCmd := m.handleSlashCommand(cmd)
	got := next.(Model)
	if startCmd == nil || !got.starting || got.mode != modeRunning {
		t.Fatalf("start state = mode %v, starting %v, cmd %v", got.mode, got.starting, startCmd)
	}
}

func TestEnterStartingSwitchesToWorkbenchImmediately(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("Viết một bộ truyện huyền huyễn phương Đông")

	if m.mode != modeRunning {
		t.Fatalf("mode = %v, want modeRunning", m.mode)
	}
	if !m.starting {
		t.Fatal("starting phải là true khi lệnh khởi động host đang chạy")
	}
	if !m.snapshot.IsRunning {
		t.Fatal("snapshot phải render là đang chạy trong lúc khởi động local")
	}
	if got := m.textarea.Placeholder; got != "Đang khởi tạo sáng tác..." {
		t.Fatalf("placeholder = %q", got)
	}
	if len(m.events) != 2 {
		t.Fatalf("events = %+v, want startup user + system events", m.events)
	}
	if m.events[0].Category != "USER" || !strings.HasPrefix(m.events[0].Summary, "Yêu cầu sáng tác: ") {
		t.Fatalf("first event = %+v, want USER prompt event", m.events[0])
	}
}

func TestStartupFailureStaysInWorkbench(t *testing.T) {
	m := NewModel(nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("Viết một bộ truyện huyền huyễn phương Đông")

	next, _ := m.handleStartResultMsg(startResultMsg{err: errors.New("Tài khoản mô hình chưa được kích hoạt")})
	got := next.(Model)
	if got.mode != modeRunning {
		t.Fatalf("sau khi khởi động thất bại mode = %v, want modeRunning", got.mode)
	}
	if got.starting {
		t.Fatal("sau khi khởi động thất bại starting phải được đặt lại")
	}
	if got.snapshot.IsRunning {
		t.Fatal("sau khi khởi động thất bại snapshot không nên vẫn hiển thị đang chạy")
	}
	if !strings.Contains(got.textarea.Placeholder, "Khởi động thất bại") {
		t.Fatalf("placeholder = %q", got.textarea.Placeholder)
	}
	if len(got.events) == 0 || got.events[len(got.events)-1].Category != "ERROR" {
		t.Fatalf("workbench phải giữ lại sự kiện lỗi khởi động: %+v", got.events)
	}
}

func TestApplyStartupPromptEventTruncatesSummaryButKeepsDetail(t *testing.T) {
	m := NewModel(nil, "")
	prompt := strings.Repeat("thiết lập", maxPromptEventCols+50)

	m.applyStartupPromptEvent(prompt)

	if len(m.events) != 1 {
		t.Fatalf("events = %+v, want one event", m.events)
	}
	ev := m.events[0]
	if ev.Detail != prompt {
		t.Fatalf("detail phải giữ prompt đầy đủ, nhận len=%d muốn %d", len([]rune(ev.Detail)), len([]rune(prompt)))
	}
	maxSummaryRunes := len([]rune("Yêu cầu sáng tác: ")) + maxPromptEventCols
	if got := len([]rune(ev.Summary)); got > maxSummaryRunes {
		t.Fatalf("summary runes = %d, want <= %d", got, maxSummaryRunes)
	}
	if !strings.HasSuffix(ev.Summary, "...") {
		t.Fatalf("summary phải được cắt bằng dấu lược, nhận %q", ev.Summary)
	}
}

func TestStreamFlushTimerRunsOnlyForPendingData(t *testing.T) {
	m := NewModel(nil, "")
	next, cmd, handled := m.handleRuntimeMsg(streamDeltaMsg("nội dung chính"))
	if !handled || cmd == nil {
		t.Fatal("streaming delta phải khởi động một lần refresh")
	}
	got := next.(Model)
	if !got.streamDirty || !got.flushPending {
		t.Fatal("streaming delta phải đánh dấu chờ refresh")
	}
	next, cmd, handled = got.handleRuntimeMsg(streamFlushTickMsg{})
	got = next.(Model)
	if !handled || cmd != nil || got.streamDirty || got.flushPending {
		t.Fatal("sau khi refresh hoàn tất timer phải dừng")
	}
}
