package store

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func newTestCheckpointStore(t *testing.T) (*CheckpointStore, string) {
	t.Helper()
	dir := t.TempDir()
	io := newIO(dir)
	return NewCheckpointStore(io), dir
}

func TestCheckpointStore_AppendAndQuery(t *testing.T) {
	cs, _ := newTestCheckpointStore(t)

	cp1, err := cs.Append(domain.ChapterScope(1), "plan", "drafts/01.plan.json", "sha256:abc")
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if cp1.Seq != 1 {
		t.Fatalf("seq muốn 1 nhưng nhận %d", cp1.Seq)
	}

	cp2, _ := cs.Append(domain.ChapterScope(1), "draft", "drafts/01.draft.md", "sha256:def")
	if cp2.Seq != 2 {
		t.Fatalf("seq muốn 2 nhưng nhận %d", cp2.Seq)
	}

	if got := cs.Latest(domain.ChapterScope(1)); got == nil || got.Step != "draft" {
		t.Fatalf("latest nhận %+v", got)
	}
	if got := cs.LatestByStep(domain.ChapterScope(1), "plan"); got == nil || got.Digest != "sha256:abc" {
		t.Fatalf("latestByStep plan nhận %+v", got)
	}
	if got := cs.LatestGlobal(); got == nil || got.Seq != 2 {
		t.Fatalf("latestGlobal nhận %+v", got)
	}
	if all := cs.All(); len(all) != 2 {
		t.Fatalf("độ dài all muốn 2 nhưng nhận %d", len(all))
	}
}

func TestCheckpointStore_Idempotent(t *testing.T) {
	cs, dir := newTestCheckpointStore(t)

	cp1, _ := cs.Append(domain.ChapterScope(1), "plan", "drafts/01.plan.json", "sha256:abc")
	cp2, err := cs.Append(domain.ChapterScope(1), "plan", "drafts/01.plan.json", "sha256:abc")
	if err != nil {
		t.Fatalf("thực hiện append lại: %v", err)
	}
	if cp1.Seq != cp2.Seq {
		t.Fatalf("idempotent phải trả về cùng seq, nhận %d và %d", cp1.Seq, cp2.Seq)
	}
	if all := cs.All(); len(all) != 1 {
		t.Fatalf("cache phải giữ 1 mục, nhận %d", len(all))
	}

	// Trên đĩa cũng phải chỉ có một dòng
	data, _ := os.ReadFile(filepath.Join(dir, checkpointsFile))
	if got := countLines(data); got != 1 {
		t.Fatalf("trên đĩa phải có 1 dòng, nhận %d", got)
	}
}

func TestCheckpointStore_AppendArtifactsTracksEveryArtifact(t *testing.T) {
	cs, dir := newTestCheckpointStore(t)
	if err := os.WriteFile(filepath.Join(dir, "chapter.md"), []byte("Nội dung chính"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"title":"Tiêu đề cũ"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := cs.AppendArtifacts(domain.ChapterScope(1), "commit", "chapter.md", "summary.json")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := cs.AppendArtifacts(domain.ChapterScope(1), "commit", "chapter.md", "summary.json")
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Seq != first.Seq {
		t.Fatalf("tập artifact giống nhau phải idempotent: first=%d replay=%d", first.Seq, replayed.Seq)
	}

	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"title":"Tiêu đề mới"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := cs.AppendArtifacts(domain.ChapterScope(1), "commit", "chapter.md", "summary.json")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Seq <= first.Seq {
		t.Fatalf("thay đổi một artifact phải thêm checkpoint: first=%d changed=%d", first.Seq, changed.Seq)
	}
}

func TestCheckpointStore_EmptyDigestNotIdempotent(t *testing.T) {
	cs, _ := newTestCheckpointStore(t)

	// digest rỗng không tham gia khử trùng lặp idempotent
	cs.Append(domain.GlobalScope(), "note", "", "")
	cs.Append(domain.GlobalScope(), "note", "", "")
	if all := cs.All(); len(all) != 2 {
		t.Fatalf("digest rỗng phải thêm cả hai, nhận %d", len(all))
	}
}

func TestCheckpointStore_Reset(t *testing.T) {
	cs, dir := newTestCheckpointStore(t)
	cs.Append(domain.ChapterScope(1), "plan", "p", "sha256:1")
	cs.Append(domain.ChapterScope(1), "draft", "d", "sha256:2")

	if err := cs.Reset(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if all := cs.All(); len(all) != 0 {
		t.Fatalf("cache phải rỗng sau reset, nhận %d", len(all))
	}
	if cs.LatestGlobal() != nil {
		t.Fatalf("latestGlobal phải là nil sau reset")
	}
	if _, err := os.Stat(filepath.Join(dir, checkpointsFile)); !os.IsNotExist(err) {
		t.Fatalf("file phải bị xóa, err=%v", err)
	}

	// Sau Reset seq được đặt lại: lần append tiếp theo bắt đầu từ 1
	cp, _ := cs.Append(domain.ChapterScope(1), "plan", "p", "sha256:1")
	if cp.Seq != 1 {
		t.Fatalf("seq sau reset phải khởi động lại từ 1, nhận %d", cp.Seq)
	}
}

func TestCheckpointStore_RestoreFromDisk(t *testing.T) {
	dir := t.TempDir()
	io1 := newIO(dir)
	cs1 := NewCheckpointStore(io1)
	cs1.Append(domain.ChapterScope(1), "plan", "p", "sha256:1")
	cs1.Append(domain.ChapterScope(1), "draft", "d", "sha256:2")
	cs1.Append(domain.ChapterScope(2), "plan", "p2", "sha256:3")

	// Mô phỏng khởi động lại: instance mới tải từ cùng một thư mục
	io2 := newIO(dir)
	cs2 := NewCheckpointStore(io2)

	if all := cs2.All(); len(all) != 3 {
		t.Fatalf("độ dài cache sau khôi phục muốn 3 nhưng nhận %d", len(all))
	}
	if got := cs2.LatestGlobal(); got == nil || got.Seq != 3 {
		t.Fatalf("seq latestGlobal sau khôi phục muốn 3 nhưng nhận %+v", got)
	}

	// seq phải tiếp nối từ 4, và idempotent vẫn có hiệu lực
	cp, _ := cs2.Append(domain.ChapterScope(2), "draft", "d2", "sha256:4")
	if cp.Seq != 4 {
		t.Fatalf("seq tiếp nối sau khôi phục muốn 4 nhưng nhận %d", cp.Seq)
	}
	dup, _ := cs2.Append(domain.ChapterScope(1), "plan", "p", "sha256:1")
	if dup.Seq != 1 {
		t.Fatalf("idempotent xuyên qua lần khởi động lại, muốn seq 1 nhưng nhận %d", dup.Seq)
	}
}

func TestStoreInitRejectsCorruptCheckpointLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, checkpointsFile), []byte("{\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(dir).Init(); err == nil {
		t.Fatal("nhật ký checkpoint bị hỏng phải chặn Store khởi tạo")
	}
}

func TestCheckpointStore_AllReturnsCopy(t *testing.T) {
	cs, _ := newTestCheckpointStore(t)
	cs.Append(domain.ChapterScope(1), "plan", "p", "sha256:1")

	all := cs.All()
	all[0].Step = "tampered"

	if got := cs.LatestGlobal(); got.Step != "plan" {
		t.Fatalf("cache nội bộ phải không bị ảnh hưởng bởi sửa đổi từ phía gọi, nhận %q", got.Step)
	}
}

func TestCheckpointStore_ConcurrentAppend(t *testing.T) {
	cs, _ := newTestCheckpointStore(t)

	const goroutines = 10
	const perGoroutine = 20

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := range perGoroutine {
				cs.Append(domain.ChapterScope(gid*100+i), "plan", "p", "")
			}
		}(g)
	}
	wg.Wait()

	all := cs.All()
	if len(all) != goroutines*perGoroutine {
		t.Fatalf("append đồng thời làm mất dữ liệu: muốn %d nhưng nhận %d", goroutines*perGoroutine, len(all))
	}

	// seq phải là 1..N, không trùng lặp
	seen := make(map[int64]bool, len(all))
	for _, cp := range all {
		if seen[cp.Seq] {
			t.Fatalf("seq trùng lặp %d", cp.Seq)
		}
		seen[cp.Seq] = true
	}
	for i := int64(1); i <= int64(len(all)); i++ {
		if !seen[i] {
			t.Fatalf("thiếu seq %d", i)
		}
	}
}

func TestCheckpointStore_SeqNotConsumedOnWriteFailure(t *testing.T) {
	cs, dir := newTestCheckpointStore(t)
	if _, err := cs.Append(domain.ChapterScope(1), "plan", "p", "sha256:1"); err != nil {
		t.Fatalf("append khởi tạo: %v", err)
	}

	// Đổi chính file jsonl thành chỉ đọc để lần OpenFile tiếp theo ghi thất bại
	jsonlPath := filepath.Join(dir, checkpointsFile)
	if err := os.Chmod(jsonlPath, 0o444); err != nil {
		t.Skipf("chmod readonly không được hỗ trợ: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(jsonlPath, 0o644) })

	if _, err := cs.Append(domain.ChapterScope(2), "plan", "p", "sha256:2"); err == nil {
		t.Fatal("mong đợi lỗi ghi trên file chỉ đọc")
	}

	// cache không được bị nhiễm
	if all := cs.All(); len(all) != 1 {
		t.Fatalf("cache rò rỉ entry thất bại, len=%d", len(all))
	}

	// Khôi phục quyền ghi, thử lại phải được seq=2 chứ không phải seq=3
	if err := os.Chmod(jsonlPath, 0o644); err != nil {
		t.Fatalf("khôi phục chmod: %v", err)
	}
	cp, err := cs.Append(domain.ChapterScope(2), "plan", "p", "sha256:2")
	if err != nil {
		t.Fatalf("thử append lại: %v", err)
	}
	if cp.Seq != 2 {
		t.Fatalf("seq không được bị consume bởi append thất bại, muốn 2 nhưng nhận %d", cp.Seq)
	}
}

func countLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := 0
	for _, b := range data {
		if b == '\n' {
			n++
		}
	}
	return n
}
