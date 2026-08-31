package imp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func mustLoadState(t *testing.T, w *Workspace) Facts {
	t.Helper()
	f, err := LoadState(w)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	return f
}

func TestNextActionChain(t *testing.T) {
	cases := []struct {
		name string
		f    Facts
		want Action
	}{
		{"Trống", Facts{}, ActionIngest},
		{"Đã tạo vùng, chờ phân đoạn", Facts{WorkspaceReady: true}, ActionSegment},
		{"Đã phân đoạn, chờ xác nhận", Facts{WorkspaceReady: true, Segmented: true}, ActionAwaitConfirmation},
		{"Đã xác nhận, chờ phân tích", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3}, ActionAnalyze},
		{"Phân tích chưa đủ", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 2}, ActionAnalyze},
		{"Phân tích đủ, chờ tổng hợp", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3}, ActionSynthesize},
		{"Sau tổng hợp uncertain, chờ phân xử", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, StoryUncertain: true}, ActionAwaitStoryResolution},
		{"uncertain đã phân xử, chờ phát hành", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, StoryUncertain: true, StoryResolved: true}, ActionPublish},
		{"Trạng thái rõ ràng, chờ phát hành", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true}, ActionPublish},
		{"Tất cả nhất quán", Facts{WorkspaceReady: true, Segmented: true, Confirmed: true, ExpectedChapters: 3, AnalyzedChapters: 3, Synthesized: true, Published: true}, ActionDone},
		{"Trạng thái cuối phát hành đi tắt qua thượng nguồn hết mới", Facts{Published: true}, ActionDone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextAction(c.f)
			if got != c.want {
				t.Fatalf("NextAction=%s want=%s", got, c.want)
			}
			// Bất biến với cùng một ảnh chụp Facts.
			if NextAction(c.f) != got {
				t.Fatal("NextAction không bất biến với cùng một Facts")
			}
		})
	}
}

func TestLoadStateReflectsWorkspace(t *testing.T) {
	book := t.TempDir()
	// Chưa tạo vùng: không hoạt động -> ingest.
	w := OpenWorkspace(book)
	if NextAction(mustLoadState(t, w)) != ActionIngest {
		t.Fatal("Sách trống phải ingest trước")
	}
	// Sau khi tạo vùng: workspace ready, chưa phân đoạn -> segment.
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	f := mustLoadState(t, ws)
	if !f.WorkspaceReady || f.Segmented {
		t.Fatalf("Facts sau khi tạo vùng không khớp: %+v", f)
	}
	if NextAction(f) != ActionSegment {
		t.Fatal("Sau khi tạo vùng phải segment")
	}
}

func TestLoadStateReportsCorruptArtifact(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.writeAtomic(fileSegmentation, []byte("{")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(ws); err == nil || !strings.Contains(err.Error(), "đọc artifact chia đoạn") {
		t.Fatalf("Tạo tác hỏng không được giả vờ là chưa phân đoạn: %v", err)
	}
}

func TestIngestSnapshotConsistent(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	content := "Chương một\r\nNội dung một\r\n\r\nChương hai\r\nNội dung hai"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, m, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if m.Encoding != encodingUTF8 || m.SourceName != "book.txt" {
		t.Fatalf("manifest không khớp: %+v", m)
	}
	snap, err := ws.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	// Ảnh chụp nguồn phải đã được chuẩn hóa, và digest phải nhất quán với manifest.
	if string(snap) != "Chương một\nNội dung một\n\nChương hai\nNội dung hai" {
		t.Fatalf("Ảnh chụp nguồn chưa được chuẩn hóa: %q", snap)
	}
	if Digest(snap) != m.NormalizedSHA256 {
		t.Fatal("Digest của ảnh chụp nguồn không nhất quán với manifest")
	}
}

// TestGuidanceChangeInvalidatesSegmentation bảo vệ mục 18.3: hướng dẫn phân đoạn là đầu vào ngữ nghĩa của segmentation,
// thay đổi hướng dẫn khiến phân đoạn cũ (và toàn bộ hạ nguồn của nó) tự nhiên mất khớp và phải làm lại, không cần quy tắc vô hiệu hóa thủ công.
func TestGuidanceChangeInvalidatesSegmentation(t *testing.T) {
	book := t.TempDir()
	src := filepath.Join(book, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(book, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	norm, err := ws.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "Chương một", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", segmentPromptVersion), seg); err != nil {
		t.Fatal(err)
	}
	if !mustLoadState(t, ws).Segmented {
		t.Fatal("Khi không có hướng dẫn, phân đoạn phải hợp lệ")
	}
	if err := ws.writeAtomic(fileGuidance, []byte("Đoạn xen giữa cũng là chương độc lập")); err != nil {
		t.Fatal(err)
	}
	if mustLoadState(t, ws).Segmented {
		t.Fatal("Sau khi hướng dẫn thay đổi, phân đoạn cũ phải mất hiệu lực (cần nhận diện lại)")
	}
}

// TestResumeSummary bảo vệ gợi ý khởi động ở mục 18.2: không có workspace thì trả về chuỗi rỗng; khi dừng giữa chừng thì đưa ra mô tả theo giai đoạn,
// để người dùng không phải đợi đến lúc sáng tác bị chặn bởi cổng kiểm soát mới phát hiện cuốn sách này đang dừng giữa quá trình nhập.
func TestResumeSummary(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if got := ResumeSummary(st); got != "" {
		t.Fatalf("Không có workspace đã nhập thì phải trả về chuỗi rỗng, nhận được %q", got)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(dir, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if got := ResumeSummary(st); !strings.Contains(got, "chưa hoàn tất chia đoạn") {
		t.Fatalf("Vừa tạo vùng phải gợi ý chưa hoàn tất phân đoạn, nhận được %q", got)
	}
	// Phân đoạn + xác nhận đã sẵn sàng, phân tích 0/1 -> gợi ý tiến độ phân tích.
	norm, _ := ws.LoadSource()
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "Chương một", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", segmentPromptVersion), seg); err != nil {
		t.Fatal(err)
	}
	raw, _ := ws.readBytes(fileSegmentation)
	if err := writeArtifact(ws, fileConfirmation, Digest(raw), Confirmation{Method: confirmMethodAuto, Chapters: 1}); err != nil {
		t.Fatal(err)
	}
	if got := ResumeSummary(st); !strings.Contains(got, "đã phân tích 0/1 chương") {
		t.Fatalf("Phải gợi ý tiến độ phân tích, nhận được %q", got)
	}
}

// TestResumeStatusPublishedIsTerminal bảo vệ trạng thái cuối đã phát hành (sự cố đo được): sau khi sách đã được phát hành toàn bộ,
// segmentPromptVersion nâng cấp làm tạo tác phân đoạn trong workspace hết mới, ResumeStatus không được vì thế mà phán sách quay lại
// "đang nhập giữa chừng" -- nếu không, cổng kiểm soát khi startEngine qua khởi động lại sẽ vĩnh viễn từ chối khởi động viết tiếp sách đã phát hành.
func TestResumeStatusPublishedIsTerminal(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "book.txt")
	if err := os.WriteFile(src, []byte("Chương một\nNội dung\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, _, err := Ingest(dir, src, Intent{})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	norm, _ := ws.LoadSource()
	// Ghi phân đoạn bằng số phiên bản cũ: mô phỏng digest mất khớp do prompt nâng cấp sau khi phát hành.
	seg := Segmentation{Chapters: []ChapterSpan{{Number: 1, Title: "Chương một", Start: 0, End: len(norm)}}}
	if err := writeArtifact(ws, fileSegmentation, segmentInputDigest(Digest(norm), "", "seg-v0"), seg); err != nil {
		t.Fatal(err)
	}
	// Chưa phát hành + phân đoạn hết mới: vẫn là nhập giữa chừng, cổng kiểm soát phải chặn.
	if active, done, err := ResumeStatus(st); err != nil || !active || done {
		t.Fatalf("Workspace chưa phát hành và hết mới phải được phán là chưa hoàn thành (active=%v done=%v)", active, done)
	}
	// Kho chính thức đã ghi toàn bộ theo phân đoạn này -> đối soát phát hành thông qua, trạng thái cuối không chịu ảnh hưởng bởi thượng nguồn hết mới.
	if err := st.Book.Save(domain.BookMetadata{Title: "Sách kiểm thử", Synopsis: "Tóm tắt kiểm thử"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SavePremise("Tiền đề"); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Chương một"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatal(err)
	}
	if active, done, err := ResumeStatus(st); err != nil || !active || !done {
		t.Fatalf("Sách đã phát hành phải được phán là nhập hoàn tất (active=%v done=%v)", active, done)
	}
	if got := ResumeSummary(st); got != "" {
		t.Fatalf("Sách đã phát hành không nên gợi ý nhập chưa hoàn tất, nhận được %q", got)
	}
}

func TestImportPreconditions(t *testing.T) {
	// Sách trống được thông qua.
	empty := store.NewStore(t.TempDir())
	if err := checkImportPreconditions(empty); err != nil {
		t.Fatalf("Sách trống phải vượt qua kiểm tra tiền điều kiện: %v", err)
	}
	// Có chương hoàn thành thì bị từ chối.
	nonEmpty := store.NewStore(t.TempDir())
	if err := nonEmpty.Progress.Save(&domain.Progress{CompletedChapters: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := checkImportPreconditions(nonEmpty); err == nil {
		t.Fatal("Sách không trống phải bị từ chối nhập")
	}
	withBook := store.NewStore(t.TempDir())
	if err := withBook.Book.Save(domain.BookMetadata{Title: "Tác phẩm đã có", Synopsis: "Tóm tắt đã có"}); err != nil {
		t.Fatal(err)
	}
	if err := checkImportPreconditions(withBook); err == nil {
		t.Fatal("Khi đã có thông tin tác phẩm thì phải bị từ chối nhập")
	}
}
