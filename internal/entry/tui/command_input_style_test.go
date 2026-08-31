package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestCommandInputHighlightsOnlyRegisteredCommands(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := Model{textarea: textarea.New()}
	m.textarea.Focus()

	for _, input := range []string{"/config", "/model writer", "/plan"} { // /plan là bí danh của /cocreate
		m.textarea.SetValue(input)
		m.syncCommandInputHighlight()
		if m.commandToken == "" {
			t.Errorf("Lệnh đã đăng ký %q phải được nhận diện", input)
		}
		plain := m.textarea.View()
		if colored := highlightCommandToken(plain, input, m.commandToken); colored == plain {
			t.Errorf("Kết xuất thực tế của lệnh đã đăng ký %q không đổi màu", input)
		}
	}

	for _, input := range []string{"Nhập thông thường", "/con", "/unknown"} {
		m.textarea.SetValue(input)
		m.syncCommandInputHighlight()
		if m.commandToken != "" {
			t.Errorf("Lệnh chưa hoàn chỉnh %q không nên được tô sáng, token=%q", input, m.commandToken)
		}
	}
}

func TestCommandInputDoesNotHighlightArguments(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })

	m := Model{textarea: textarea.New()}
	m.textarea.Focus()
	m.textarea.SetValue("/reopen tiếp tục sáng tác")
	m.textarea.CursorEnd()
	m.syncCommandInputHighlight()

	plainView := m.textarea.View()
	view := highlightCommandToken(plainView, m.textarea.Value(), m.commandToken)
	if stripped := ansi.Strip(view); stripped != ansi.Strip(plainView) {
		t.Fatalf("Tô sáng không nên thay đổi nội dung Nhập: %q", stripped)
	}
	if !strings.Contains(view, "/reopen"+resetForeground+" tiếp tục sáng tác") {
		t.Fatalf("Tham số sau lệnh không khôi phục màu văn bản chính: %q", view)
	}
}
