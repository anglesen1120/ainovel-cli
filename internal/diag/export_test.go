package diag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// sentinel là một đoạn văn bản truyện riêng biệt, không được xuất hiện trong bản xuất.
const sentinel = "Đêm tuyết, nhân vật chính vạch trần âm mưu kinh thiên của phản diện — nội dung mật"

// writeSession ghi các sessions/*.jsonl đầu ra.
func writeSession(t *testing.T, rel string, msgs []agentcore.Message) string {
	t.Helper()
	dir := t.TempDir()
	writeSessionAt(t, dir, rel, msgs)
	return dir
}

func writeSessionAt(t *testing.T, dir, rel string, msgs []agentcore.Message) {
	t.Helper()
	path := filepath.Join(dir, "meta", "sessions", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("tạo thư mục: %v", err)
	}
	var b strings.Builder
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("tuần tự hóa: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("ghi: %v", err)
	}
}

func commitCall(chapterRaw string) agentcore.Message {
	args := json.RawMessage(`{"chapter":` + chapterRaw + `,"content":"` + sentinel + sentinel + `"}`)
	return agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.ToolCallBlock(agentcore.ToolCall{Name: "commit_chapter", Args: args})},
	}
}

func errResult(msg string) agentcore.Message {
	return agentcore.Message{
		Role:     agentcore.RoleTool,
		Content:  []agentcore.ContentBlock{agentcore.TextBlock(msg)},
		Metadata: map[string]any{"is_error": true},
	}
}

// TestExport_DeathLoopShape #34: commit_chapter với chapter.
// Xác nhận vòng lặp được phát hiện và văn bản truyện được che.
func TestExport_DeathLoopShape(t *testing.T) {
	var msgs []agentcore.Message
	// Worker đầu ra là văn bản truyện (<4KB, session_compact).
	msgs = append(msgs, agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	// 14 lần commit_chapter(chapter:"7") cùng InputValidationError.
	for range 14 {
		msgs = append(msgs, commitCall(`"7"`))
		msgs = append(msgs, errResult("InputValidationError: chapter must be int"))
	}

	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), msgs)
	s := store.NewStore(dir)
	rep, rc := Diagnose(s)
	out := string(RenderExport(rep, rc))

	if strings.Contains(out, sentinel) {
		t.Fatalf("văn bản truyện sentinel bị lộ:\n%s", out)
	}
	if !strings.Contains(out, `chapter: "7"`) {
		t.Errorf("thiếu tín hiệu kiểu chapter bất thường: \"7\" (#34)\n%s", out)
	}
	if !strings.Contains(out, "InputValidationError") {
		t.Errorf("lỗi phải được giữ lại\n%s", out)
	}
	if !strings.Contains(out, "×14") {
		t.Errorf("thiếu số lần lặp ×14\n%s", out)
	}
	// Giai đoạn 2: phát hiện vòng lặp nghiêm trọng RepeatedToolError.
	if !strings.Contains(out, "lỗi") {
		t.Errorf("phải phát hiện RepeatedToolError\n%s", out)
	}
	if !strings.Contains(out, "[critical]") {
		t.Errorf("14 lần lặp phải ở mức critical\n%s", out)
	}
}

// TestExport_NumberVsStringArg phân loại:
// chapter:7 (số) giữ lại 7, chapter:"7" (chuỗi) giữ lại "7".
func TestExport_NumberVsStringArg(t *testing.T) {
	intDir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`7`)})
	si := store.NewStore(intDir)
	repInt, rcInt := Diagnose(si)
	outInt := string(RenderExport(repInt, rcInt))
	if !strings.Contains(outInt, "chapter: 7") || strings.Contains(outInt, `chapter: "7"`) {
		t.Errorf("tham số phải là chapter: 7 (không có dấu ngoặc kép)\n%s", outInt)
	}
}

// TestProjectValue_ProseArgRedacted  :giữ lại、
// /（ dispatch task、chapter title）。
func TestProjectValue_ProseArgRedacted(t *testing.T) {
	keep := map[string]string{
		`"7"`:       `"7"`,       // (#34)
		`"premise"`: `"premise"`, //
		`"writer"`:  `"writer"`,  //
		`7`:         `7`,         //
		`true`:      `true`,      // bool
	}
	for in, want := range keep {
		if got := projectValue([]byte(in)); got != want {
			t.Errorf("cần giữ lại %s: nhận %q, mong đợi %q", in, got, want)
		}
	}
	// Chuỗi văn xuôi phải được che để tránh xuất nội dung truyện.
	prose := []string{`"Chương 7: Đêm tuyết vạch trần sự thật"`, `"Sát cơ trong đêm tuyết"`, `"Nhân vật chính vạch trần âm mưu"`}
	for _, in := range prose {
		got := projectValue([]byte(in))
		if !strings.HasPrefix(got, "<redacted") {
		t.Errorf("chuỗi văn xuôi phải được che: %s → %q", in, got)
		}
		if strings.Contains(got, "đêm tuyết") || strings.Contains(got, "âm mưu") {
			t.Errorf("sau khi che vẫn còn văn bản truyện: %s → %q", in, got)
		}
	}
}

// TestWriteExport_WritesFile  : TUI，。
func TestWriteExport_WritesFile(t *testing.T) {
	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`"7"`), errResult("boom")})
	s := store.NewStore(dir)

	rep, rc := Diagnose(s)
	path, err := WriteExport(s, rep, rc)
	if err != nil {
		t.Fatalf("WriteExport: %v", err)
	}
	if want := filepath.Join(dir, filepath.FromSlash(ExportRelPath)); path != want {
		t.Errorf("đường dẫn sai: nhận %s, mong đợi %s", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("đọc lại: %v", err)
	}
	if !strings.Contains(string(data), "diag-export") {
		t.Errorf("nội dung\n%s", data)
	}
	if strings.Contains(string(data), sentinel) {
		t.Errorf("văn bản truyện")
	}
}

// TestRedactMessage_DupSha  sha (vòng lặp).
func TestRedactMessage_DupSha(t *testing.T) {
	a := redactMessage("writer-ch07", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	b := redactMessage("writer-ch07", agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	if a.TextSha == "" || a.TextSha != b.TextSha {
		t.Errorf("văn bản truyện cần SHA: %q và %q", a.TextSha, b.TextSha)
	}
	if a.Redacted != 1 {
		t.Errorf("cần 1, nhận %d", a.Redacted)
	}
}
