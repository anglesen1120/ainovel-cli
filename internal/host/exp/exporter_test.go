package exp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// newTestStore dựng store tối thiểu trên t.TempDir(), đã ghi bản thảo cuối và progress cho các chương hoàn tất.
func newTestStore(t *testing.T, novelName string, completed []int) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	if err := s.Progress.Init(len(completed)); err != nil {
		t.Fatalf("init progress: %v", err)
	}
	if novelName != "" {
		if err := s.Book.Save(domain.BookMetadata{Title: novelName, Synopsis: "Một đoạn giới thiệu thử nghiệm dành cho độc giả."}); err != nil {
			t.Fatalf("save book: %v", err)
		}
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("phase writing: %v", err)
	}
	for _, ch := range completed {
		if err := s.Drafts.SaveFinalChapter(ch, fmt.Sprintf("Nội dung ch %d.", ch)); err != nil {
			t.Fatalf("save chapter %d: %v", ch, err)
		}
		if err := s.Progress.StartChapter(ch); err != nil {
			t.Fatalf("start chapter %d: %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, 5, "cliff", "main"); err != nil {
			t.Fatalf("mark complete %d: %v", ch, err)
		}
	}
	return s, dir
}

func TestRun_HappyPath_DefaultsToNovelDir(t *testing.T) {
	s, dir := newTestStore(t, "Đốm sáng", []int{1, 2, 3})
	if err := s.Outline.SavePremise("Câu chuyện về ánh sáng và bóng tối."); err != nil {
		t.Fatalf("save premise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Người về trong mưa đêm"},
		{Chapter: 2, Title: "Rạng đông"},
		{Chapter: 3, Title: "Tro tàn"},
	}); err != nil {
		t.Fatalf("save outline: %v", err)
	}

	res, err := Run(context.Background(), Deps{Store: s}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Chapters != 3 {
		t.Errorf("Chapters = %d, want 3", res.Chapters)
	}
	if res.Path != filepath.Join(dir, "Đốm sáng.txt") {
		t.Errorf("Path = %q, want default {dir}/Đốm sáng.txt", res.Path)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(data)
	for _, want := range []string{"《Đốm sáng》", "Chương 1  Người về trong mưa đêm", "Chương 3  Tro tàn"} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, text)
		}
	}
	// premise không xuất hiện trong bản xuất (đó là bản thiết kế sáng tác, không phải nội dung độc giả).
	if strings.Contains(text, "Câu chuyện về ánh sáng và bóng tối.") {
		t.Errorf("premise must not appear in export:\n%s", text)
	}
}

func TestRun_UsesCommittedTitleForCompletedChapter(t *testing.T) {
	s, _ := newTestStore(t, "Đốm sáng", []int{1})
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Tiêu đề kế hoạch"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter: 1, Title: "Tiêu đề cuối", Summary: "Tóm tắt",
	}); err != nil {
		t.Fatal(err)
	}

	res, err := Run(context.Background(), Deps{Store: s}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Chương 1  Tiêu đề cuối") || strings.Contains(text, "Tiêu đề kế hoạch") {
		t.Fatalf("export title projection is wrong:\n%s", text)
	}
}

// TestRun_PremiseNotExported khóa end-to-end: dù premise.md tồn tại, nó không vào bản xuất và tên sách vẫn được giữ (issue #27).
func TestRun_PremiseNotExported(t *testing.T) {
	s, _ := newTestStore(t, "Đốm sáng", []int{1})
	if err := s.Outline.SavePremise("# Đốm sáng\n## Độc giả mục tiêu\nBản thiết kế sáng tác không nên xuất hiện."); err != nil {
		t.Fatalf("save premise: %v", err)
	}
	res, err := Run(context.Background(), Deps{Store: s}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	data, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "Bản thiết kế sáng tác không nên xuất hiện") || strings.Contains(text, "Độc giả mục tiêu") {
		t.Errorf("premise must not be exported, got:\n%s", text)
	}
	if !strings.Contains(text, "《Đốm sáng》") {
		t.Errorf("book title should remain: %s", text)
	}
}

func TestRun_NoCompletedChapters(t *testing.T) {
	s, _ := newTestStore(t, "X", nil)
	_, err := Run(context.Background(), Deps{Store: s}, Options{})
	if err == nil {
		t.Fatal("expect error when no completed chapters")
	}
}

func TestRun_ExistingFile_NoOverwrite(t *testing.T) {
	s, dir := newTestStore(t, "X", []int{1})
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target, []byte("preexisting"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	_, err := Run(context.Background(), Deps{Store: s}, Options{OutPath: target})
	if err == nil {
		t.Fatal("expect error when target exists and !Overwrite")
	}
	if !strings.Contains(err.Error(), "đã tồn tại") {
		t.Errorf("unexpected error: %v", err)
	}

	// Bật Overwrite thì phải thành công.
	res, err := Run(context.Background(), Deps{Store: s}, Options{OutPath: target, Overwrite: true})
	if err != nil {
		t.Fatalf("Overwrite Run: %v", err)
	}
	if res.Path != target {
		t.Errorf("Path = %q want %q", res.Path, target)
	}
	data, _ := os.ReadFile(target)
	if string(data) == "preexisting" {
		t.Error("file not overwritten")
	}
}

func TestRun_RangeWithSkipped(t *testing.T) {
	s, _ := newTestStore(t, "X", []int{1, 2, 3})
	res, err := Run(context.Background(), Deps{Store: s}, Options{From: 2, To: 5, Overwrite: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Chapters != 2 {
		t.Errorf("Chapters = %d want 2 (only 2,3 completed in range 2..5)", res.Chapters)
	}
	if got := res.Skipped; len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Errorf("Skipped = %v want [4 5]", got)
	}
}

func TestRun_FromGreaterThanTo(t *testing.T) {
	s, _ := newTestStore(t, "X", []int{1, 2})
	_, err := Run(context.Background(), Deps{Store: s}, Options{From: 5, To: 2})
	if err == nil {
		t.Fatal("expect error for invalid range")
	}
}

func TestRun_UnsupportedFormat(t *testing.T) {
	s, _ := newTestStore(t, "X", []int{1})
	_, err := Run(context.Background(), Deps{Store: s}, Options{Format: Format("pdf")})
	if err == nil {
		t.Fatal("expect error for unsupported format")
	}
}

func TestRunRejectsMissingBookMetadata(t *testing.T) {
	s, _ := newTestStore(t, "", []int{1})
	if _, err := Run(context.Background(), Deps{Store: s}, Options{}); err == nil {
		t.Fatal("must reject export when book metadata is missing")
	}
}

func TestInferFormat(t *testing.T) {
	cases := []struct {
		in      string
		want    Format
		wantErr bool
	}{
		{"", FormatTXT, false},
		{"book.txt", FormatTXT, false},
		{"book.TXT", FormatTXT, false},
		{"book.epub", FormatEPUB, false},
		{"book.EPUB", FormatEPUB, false},
		{"/abs/path/x.epub", FormatEPUB, false},
		{"book", FormatTXT, false}, // Không có hậu tố thì dùng TXT.
		{"book.dat", "", true},
		{"book.pdf", "", true},
	}
	for _, c := range cases {
		got, err := inferFormat(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("inferFormat(%q) want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("inferFormat(%q): unexpected err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("inferFormat(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestRun_EPUB_FromExtension(t *testing.T) {
	s, dir := newTestStore(t, "Đốm sáng", []int{1})
	if err := s.Outline.SavePremise("Ánh sáng và bóng tối."); err != nil {
		t.Fatalf("save premise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Đêm mưa"}}); err != nil {
		t.Fatalf("save outline: %v", err)
	}

	target := filepath.Join(dir, "out.epub")
	res, err := Run(context.Background(), Deps{Store: s}, Options{OutPath: target})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Path != target {
		t.Errorf("Path = %q want %q", res.Path, target)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// EPUB là zip, hai byte đầu là chữ ký PK.
	if len(data) < 4 || string(data[:2]) != "PK" {
		t.Errorf("output does not look like a zip: %x", data[:min(8, len(data))])
	}
}

func TestRun_DefaultPathFollowsFormat(t *testing.T) {
	s, dir := newTestStore(t, "Đốm sáng", []int{1})
	res, err := Run(context.Background(), Deps{Store: s}, Options{Format: FormatEPUB})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := filepath.Join(dir, "Đốm sáng.epub")
	if res.Path != want {
		t.Errorf("Path = %q want %q", res.Path, want)
	}
}

func TestRun_UnknownExtension(t *testing.T) {
	s, _ := newTestStore(t, "X", []int{1})
	_, err := Run(context.Background(), Deps{Store: s}, Options{OutPath: "/tmp/foo.dat"})
	if err == nil {
		t.Fatal("expect error for unknown extension")
	}
	if !strings.Contains(err.Error(), "phần mở rộng") {
		t.Errorf("error should mention extension: %v", err)
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"":                     "novel",
		"   ":                  "novel",
		"normal":               "normal",
		"a/b":                  "a_b",
		"a\\b":                 "a_b",
		"a:b*c?\"d<e>f|g\x00h": "a_b_c__d_e_f_g_h",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Errorf("sanitizeFileName(%q) = %q want %q", in, got, want)
		}
	}
}
