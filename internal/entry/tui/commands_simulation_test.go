package tui

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/host"
)

func TestSimulationCommandsAreRegisteredAndNeedIdle(t *testing.T) {
	registry := commandRegistryInstance()
	for _, name := range []string{"simulate", "importsim"} {
		spec, ok := registry.Find(name)
		if !ok {
			t.Fatalf("/%s phải được đăng ký", name)
		}
		if !spec.NeedsIdle {
			t.Fatalf("/%s phải yêu cầu trạng thái idle", name)
		}
	}

	items := builtinCommandItems()
	if !hasPaletteItem(items, "simulate") || !hasPaletteItem(items, "importsim") {
		t.Fatalf("phải có lệnh simulate trong palette: %+v", items)
	}
}

func TestSimulationCommandsAreBlockedWhileRunning(t *testing.T) {
	m := Model{snapshot: host.UISnapshot{IsRunning: true}, eventIndex: map[string]int{}}
	next, _ := m.handleSlashCommand(slashCommand{name: "simulate"})
	got := next.(Model)
	if len(got.events) != 1 || got.events[0].Category != "ERROR" {
		t.Fatalf("NeedsIdle phải phát một lỗi, got %+v", got.events)
	}
	if got.simulator != nil {
		t.Fatal("modal simulate không được mở khi runtime đang chạy")
	}
}

func hasPaletteItem(items []commandPaletteItem, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
