package tui

import "testing"

func TestRevisionCommandIsRegisteredAndNeedsIdle(t *testing.T) {
	spec, ok := commandRegistryInstance().Find("sync")
	if !ok {
		t.Fatal("/sync phải được đăng ký")
	}
	if !spec.NeedsIdle || !spec.AutoExecute {
		t.Fatalf("policy /sync không đúng: %+v", spec)
	}
	if !hasPaletteItem(builtinCommandItems(), "sync") {
		t.Fatal("/sync phải có trong command palette")
	}
}
