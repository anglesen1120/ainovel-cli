package imp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// spyCommitter ghi lại số lần gọi Execute, dùng cho kiểm thử đường dẫn idempotent/khôi phục khi phát hành.
type spyCommitter struct{ calls int }

func (s *spyCommitter) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	s.calls++
	return json.RawMessage(`{}`), nil
}

func TestCheckFoundationConflictsNormalizesBookMetadata(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Book.Save(domain.BookMetadata{Title: "Sách kiểm thử", Synopsis: "Tóm tắt kiểm thử"}); err != nil {
		t.Fatal(err)
	}
	f := &Foundation{Book: domain.BookMetadata{Title: " Sách kiểm thử ", Synopsis: " Tóm tắt kiểm thử "}}
	if err := checkFoundationConflicts(st, f); err != nil {
		t.Fatalf("Thông tin tác phẩm giống nhau sau khi chuẩn hóa không nên xung đột: %v", err)
	}
}

// TestPublishChapterHandlesStalePendingCommit bảo vệ quá trình khôi phục của cửa sổ sự cố khi phát hành:
// nếu sự cố xảy ra giữa MarkChapterComplete và ClearPendingCommit thì sẽ còn lại pending_commit trỏ tới chương này.
// Nếu chương đã hoàn tất bị bỏ qua trực tiếp thì sẽ né nhánh dọn dẹp của công cụ commit, và Execute của chương kế tiếp sẽ
// bị ErrToolConflict từ chối, khiến mỗi lần nhập lại đều chết ở cùng một chỗ — khi chạm vào phần còn sót, vẫn phải đi qua
// đúng một đường dẫn idempotent của công cụ.
func TestPublishChapterHandlesStalePendingCommit(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 100, "mystery", "quest"); err != nil {
		t.Fatal(err)
	}
	f := ImportedChapterFacts{Chapter: 1, Summary: "s", CoreEvent: "c", HookType: "mystery", DominantStrand: "quest"}

	// Không có phần còn sót: chương đã hoàn tất sẽ bị bỏ qua với chi phí bằng không, không kích hoạt commit.
	spy := &spyCommitter{}
	if err := publishChapter(context.Background(), st, spy, 1, "phần thân", f); err != nil {
		t.Fatalf("Chương đã hoàn tất nên được bỏ qua một cách idempotent: %v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("Không có phần còn sót thì không nên gọi commit, nhận %d lần", spy.calls)
	}

	// Phần còn sót trỏ tới chương này: phải đi qua đúng một đường dẫn commit idempotent để hoàn tất dọn dẹp.
	if err := st.Signals.SavePendingCommit(domain.PendingCommit{Chapter: 1}); err != nil {
		t.Fatal(err)
	}
	if err := publishChapter(context.Background(), st, spy, 1, "phần thân", f); err != nil {
		t.Fatalf("Đường dẫn dọn dẹp phần còn sót không nên thất bại: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("Chạm vào phần còn sót phải gọi commit đúng một lần, nhận %d lần", spy.calls)
	}
}
