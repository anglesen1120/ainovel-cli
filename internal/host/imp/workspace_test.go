package imp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDigestStableAndDistinct(t *testing.T) {
	a := Digest([]byte("Chương một"))
	if a != Digest([]byte("Chương một")) {
		t.Fatal("digest không ổn định với cùng đầu vào")
	}
	if a == Digest([]byte("Chương hai")) {
		t.Fatal("digest giống nhau với đầu vào khác nhau")
	}
	if len(a) < 8 || a[:7] != "sha256:" {
		t.Fatalf("tiền tố digest không khớp: %s", a)
	}
}

func TestWorkspaceAtomicRoundtrip(t *testing.T) {
	w := &Workspace{dir: t.TempDir()}
	if err := w.writeAtomic("nested/x.txt", []byte("hello")); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	got, err := os.ReadFile(w.path("nested/x.txt"))
	if err != nil || string(got) != "hello" {
		t.Fatalf("đọc lại không khớp: %q %v", got, err)
	}
}

func TestArtifactRoundtripPreservesIdentity(t *testing.T) {
	w := &Workspace{dir: t.TempDir()}
	type payload struct {
		N int `json:"n"`
	}
	if err := writeArtifact(w, "seg.json", "sha256:abc", payload{N: 7}); err != nil {
		t.Fatalf("writeArtifact: %v", err)
	}
	a, err := readArtifact[payload](w, "seg.json")
	if err != nil {
		t.Fatalf("readArtifact: %v", err)
	}
	if a.InputDigest != "sha256:abc" || a.Payload.N != 7 || a.SchemaVersion != workspaceSchemaVersion {
		t.Fatalf("không giữ nguyên định danh: %+v", a)
	}
}

func TestReadArtifactRejectsSchemaMismatch(t *testing.T) {
	w := &Workspace{dir: t.TempDir()}
	// Ghi trực tiếp một artifact có phiên bản schema không khớp.
	raw := Artifact[string]{SchemaVersion: 999, InputDigest: "sha256:x", Payload: "y"}
	if err := w.writeJSON("seg.json", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := readArtifact[string](w, "seg.json"); err == nil {
		t.Fatal("artifact không khớp phiên bản schema phải bị từ chối")
	}
}

func TestCreateWorkspacePublishesAtomically(t *testing.T) {
	book := t.TempDir()
	norm := []byte("Chương một\nNội dung\n")
	m := Manifest{
		Version:          workspaceSchemaVersion,
		SourceName:       "book.txt",
		NormalizedSHA256: Digest(norm),
		Encoding:         encodingUTF8,
	}
	ws, err := createWorkspace(book, m, Intent{Version: workspaceSchemaVersion}, norm)
	if err != nil {
		t.Fatalf("createWorkspace: %v", err)
	}
	if !ws.Active() {
		t.Fatal("Sau khi phát hành, workspace phải ở trạng thái hoạt động")
	}
	for _, f := range []string{fileManifest, fileIntent, fileSource} {
		if !ws.has(f) {
			t.Fatalf("Thiếu artifact %s", f)
		}
	}
	// Sau khi createWorkspace thành công, không nên để lộ thư mục tạm bán khởi tạo (meta/import.init-*).
	if dirs, _ := filepath.Glob(filepath.Join(book, "meta", "import.init-*")); len(dirs) != 0 {
		t.Fatalf("Sau khi phát hành thành công, không nên còn lại thư mục init: %v", dirs)
	}
	// Tạo lại khi đã tồn tại phải thất bại.
	if _, err := createWorkspace(book, m, Intent{}, norm); err == nil {
		t.Fatal("Khi đã có workspace hoạt động, việc tạo lại phải thất bại")
	}
}

func TestCreateWorkspaceRejectsInconsistentSnapshot(t *testing.T) {
	book := t.TempDir()
	m := Manifest{Version: workspaceSchemaVersion, NormalizedSHA256: Digest([]byte("A"))}
	// Digest mà manifest khai báo không khớp với normalized thực tế được ghi → kiểm tra trước khi phát hành phải chặn lại.
	if _, err := createWorkspace(book, m, Intent{}, []byte("B")); err == nil {
		t.Fatal("Khi snapshot nguồn không khớp với digest của manifest thì phải từ chối phát hành")
	}
	if _, err := os.Stat(filepath.Join(book, "meta", "import")); !os.IsNotExist(err) {
		t.Fatal("Sau khi phát hành thất bại, không được để lại workspace hoạt động")
	}
}
