package tui

import (
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/host"
)

func TestAdvanceCommandsAreRegistered(t *testing.T) {
	registry := commandRegistryInstance()
	review, ok := registry.Find("review")
	if !ok || review.NeedsIdle {
		t.Fatalf("/review phải khả dụng khi đang chạy: %+v", review)
	}
	next, ok := registry.Find("next")
	if !ok || !next.NeedsIdle || !next.AutoExecute {
		t.Fatalf("/next phải là lệnh one-shot khi idle: %+v", next)
	}
	items := builtinCommandItems()
	if !hasPaletteItem(items, "review") || !hasPaletteItem(items, "next") {
		t.Fatalf("thiếu lệnh advance trong palette: %+v", items)
	}
}

func TestReviewWaitingPlaceholder(t *testing.T) {
	m := Model{
		mode: modeRunning,
		snapshot: host.UISnapshot{
			RuntimeState: "paused",
			Phase:        "writing",
			AdvanceMode:  "review",
		},
	}
	m.syncRuntimePlaceholder()
	if got := m.textarea.Placeholder; !strings.Contains(got, "/next") || !strings.Contains(got, "ý kiến chỉnh sửa") {
		t.Fatalf("placeholder review phải hiển thị cả hai lựa chọn, nhận %q", got)
	}
}
