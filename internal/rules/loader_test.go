package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureRulesDirAt xác minh việc chuẩn bị thư mục + README.txt: ghi hướng dẫn và luôn ghi đè bằng mẫu mới nhất,
// đồng thời README.txt (không phải .md) không bị quét như quy tắc.
func TestEnsureRulesDirAt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "rules")
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(dir, "README.txt")
	data, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("README.txt should be written: %v", err)
	}
	// Sau khi bỏ YAML, hướng dẫn chuyển sang "ngôn ngữ tự nhiên + chuẩn hóa tự động", không còn dạy front matter.
	if !strings.Contains(string(data), "chuẩn hóa") {
		t.Errorf("README.txt phải nói rằng ngôn ngữ tự nhiên sẽ được chuẩn hóa, got %q", data)
	}
	if strings.Contains(string(data), "front matter") {
		t.Errorf("README.txt không nên dạy YAML front matter nữa, got %q", data)
	}

	// Luôn ghi đè bằng mẫu mới nhất: nội dung cũ từ phiên bản trước sẽ được làm mới khi ensure lại
	if err := os.WriteFile(readme, []byte("Nội dung cũ từ phiên bản trước"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureRulesDirAt(dir); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(readme); string(again) != homeRulesReadme {
		t.Errorf("README.txt should be refreshed to latest template, got %q", again)
	}

	// README.txt không bị xem là quy tắc (trình quét chỉ nhận .md)
	if srcs := RawFileSources(LoadOptions{HomeRulesDir: dir}); len(srcs) != 0 {
		t.Errorf("README.txt must not be scanned as a rule, got %d sources", len(srcs))
	}
}

// TestDefaultProjectRulesDir cố định thư mục rules cấp project đối xứng global: ./.ainovel/rules/.
func TestDefaultProjectRulesDir(t *testing.T) {
	proj := filepath.Join("/tmp", "demo-book")
	want := filepath.Join(proj, ".ainovel", "rules")
	if got := DefaultProjectRulesDir(proj); got != want {
		t.Errorf("DefaultProjectRulesDir=%q, want %q", got, want)
	}
	if got := DefaultProjectRulesDir(""); got != "" {
		t.Errorf("project root rỗng phải trả về chuỗi rỗng, got %q", got)
	}
}

// TestDefaultOptions_ScansProjectRulesFromDotAinovel xác minh end-to-end:
// DefaultOptions đưa ./.ainovel/rules/ dưới cwd vào nguồn SourceProject.
func TestDefaultOptions_ScansProjectRulesFromDotAinovel(t *testing.T) {
	proj := t.TempDir()
	t.Chdir(proj)
	rulesDir := filepath.Join(proj, ".ainovel", "rules")
	if err := os.MkdirAll(rulesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "book.md"), []byte("# Sở thích của sách này\nMỗi chương 4000 chữ"), 0o644); err != nil {
		t.Fatal(err)
	}

	srcs := RawFileSources(DefaultOptions())
	var got *RawSource
	for i := range srcs {
		if srcs[i].Kind == SourceProject {
			got = &srcs[i]
		}
	}
	if got == nil {
		t.Fatalf("phải quét được nguồn rules project từ ./.ainovel/rules/, got %+v", srcs)
	}
	if !strings.Contains(got.Text, "Sở thích của sách này") {
		t.Errorf("nội dung gốc của rules project phải được trả nguyên dạng, got %q", got.Text)
	}
	if got.Label != "project:book.md" {
		t.Errorf("nhãn nguồn phải là project:book.md, got %q", got.Label)
	}
}
