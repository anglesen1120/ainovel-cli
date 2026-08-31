package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestBookStorePersistsCanonicalDataAndReadableProjection(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: " Đêm dài sắp sáng ", Synopsis: " Chàng thiếu niên giữ lấy ngọn đèn cuối cùng. "}); err != nil {
		t.Fatal(err)
	}
	book, err := s.Book.Load()
	if err != nil {
		t.Fatal(err)
	}
	if book.Title != "Đêm dài sắp sáng" || book.Synopsis != "Chàng thiếu niên giữ lấy ngọn đèn cuối cùng." {
		t.Fatalf("metadata sách không như mong đợi: %+v", book)
	}
	projection, err := os.ReadFile(filepath.Join(dir, "book.md"))
	if err != nil {
		t.Fatal(err)
	}
	if text := string(projection); !strings.Contains(text, "《Đêm dài sắp sáng》") || !strings.Contains(text, "Chàng thiếu niên giữ lấy ngọn đèn cuối cùng.") {
		t.Fatalf("projection sách không như mong đợi: %s", text)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: "Tóm tắt trống"}); err == nil {
		t.Fatal("không được chấp nhận synopsis trống")
	}
}
