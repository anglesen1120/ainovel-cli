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

// sentinel là một đoạn "nội dung tiểu thuyết" tuyệt đối không được xuất hiện trong bản xuất.
const sentinel = "Đêm tuyết, nhân vật chính vạch trần âm mưu kinh thiên của phản diện đây là nội dung bí mật"

// writeSession ghi một số thông điệp vào thư mục đầu ra tạm theo định dạng sessions/*.jsonl.
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
		t.Fatalf("Tạo thư mục: %v", err)
	}
	var b strings.Builder
	for _, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("Lỗi tuần tự hoá: %v", err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("Lỗi ghi: %v", err)
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

// TestExport_DeathLoopShape tái hiện đầu-cuối #34: mô hình biến chapter của commit_chapter
// thành chuỗi khiến vòng lặp kiểm tra xảy ra. Khẳng định bản xuất có thể định vị và nội dung tiểu thuyết không bị lộ.
func TestExport_DeathLoopShape(t *testing.T) {
	var msgs []agentcore.Message
	// Một đoạn nội dung thô do tác nhân xuất ra (<4KB, vượt qua session_compact) phải được che.
	msgs = append(msgs, agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(sentinel)},
	})
	// 14 vòng commit_chapter(chapter:"7") + InputValidationError.
	for range 14 {
		msgs = append(msgs, commitCall(`"7"`))
		msgs = append(msgs, errResult("InputValidationError: chapter must be int"))
	}

	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), msgs)
	s := store.NewStore(dir)
	rep, rc := Diagnose(s)
	out := string(RenderExport(rep, rc))

	if strings.Contains(out, sentinel) {
		t.Fatalf("Nội dung tiểu thuyết bị lộ! Bản xuất chứa sentinel:\n%s", out)
	}
	if !strings.Contains(out, `chapter: "7"`) {
		t.Errorf("Thiếu tín hiệu bất thường kiểu chapter: \"7\" (nguyên nhân #34)\n%s", out)
	}
	if !strings.Contains(out, "InputValidationError") {
		t.Errorf("Chuỗi lỗi chưa được giữ lại\n%s", out)
	}
	if !strings.Contains(out, "×14") {
		t.Errorf("Tổng hợp lặp chưa liệt kê ×14\n%s", out)
	}
	// Giai đoạn 2: phát hiện thời gian chạy phải phân loại vòng lặp này thành lỗi RepeatedToolError critical.
	if !strings.Contains(out, "Công cụ lặp lại cùng một lỗi") {
		t.Errorf("Phát hiện thời gian chạy không sinh RepeatedToolError\n%s", out)
	}
	if !strings.Contains(out, "[critical]") {
		t.Errorf("14 lần lặp phải được nâng lên critical\n%s", out)
	}
}

// TestExport_NumberVsStringArg chứng minh phép chiếu vô hướng và chuỗi có thể phân biệt kiểu:
// chapter:7 (số) giữ là 7, chapter:"7" (chuỗi) giữ là "7".
func TestExport_NumberVsStringArg(t *testing.T) {
	intDir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`7`)})
	si := store.NewStore(intDir)
	repInt, rcInt := Diagnose(si)
	outInt := string(RenderExport(repInt, rcInt))
	if !strings.Contains(outInt, "chapter: 7") || strings.Contains(outInt, `chapter: "7"`) {
		t.Errorf("Tham số số phải được hiển thị là chapter: 7 (không có dấu ngoặc kép)\n%s", outInt)
	}
}

// TestProjectValue_ProseArgRedacted bảo vệ ranh giới khử nhạy cảm: giữ lại các giá trị ngắn dạng định danh,
// còn các giá trị ngắn có chữ Hán / có khoảng trắng (như tác vụ phân phối, tiêu đề chương) đều bị che.
func TestProjectValue_ProseArgRedacted(t *testing.T) {
	keep := map[string]string{
		`"7"`:       `"7"`,       // Số bị chuyển thành chuỗi (tín hiệu #34)
		`"premise"`: `"premise"`, // Giá trị liệt kê
		`"writer"`:  `"writer"`,  // Tên vai trò
		`7`:         `7`,         // Vô hướng số
		`true`:      `true`,      // Vô hướng bool
	}
	for in, want := range keep {
		if got := projectValue([]byte(in)); got != want {
			t.Errorf("Phải giữ lại %s: nhận được %q, mong đợi %q", in, got, want)
		}
	}
	// Có chữ Hán / khoảng trắng → bắt buộc che và không chứa nguyên văn.
	prose := []string{`"Chương 7 sự thật đêm tuyết"`, `"sát cơ đêm tuyết"`, `"nhân vật chính vạch trần âm mưu"`}
	for _, in := range prose {
		got := projectValue([]byte(in))
		if !strings.HasPrefix(got, "<redacted") {
			t.Errorf("Giá trị ngắn có chữ Hán / khoảng trắng phải bị che: %s → %q", in, got)
		}
		if strings.Contains(got, "đêm tuyết") || strings.Contains(got, "nhân vật chính") {
			t.Errorf("Sau khi che vẫn còn nội dung: %s → %q", in, got)
		}
	}
}

// TestWriteExport_WritesFile chứng minh đường dẫn hàm thuần: không phụ thuộc TUI, ghi ra đường dẫn tương đối cố định.
func TestWriteExport_WritesFile(t *testing.T) {
	dir := writeSession(t, filepath.Join("agents", "writer-ch07.jsonl"), []agentcore.Message{commitCall(`"7"`), errResult("boom")})
	s := store.NewStore(dir)

	rep, rc := Diagnose(s)
	path, err := WriteExport(s, rep, rc)
	if err != nil {
		t.Fatalf("WriteExport gặp lỗi: %v", err)
	}
	if want := filepath.Join(dir, filepath.FromSlash(ExportRelPath)); path != want {
		t.Errorf("Đường dẫn không đúng: nhận được %s, mong đợi %s", path, want)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Lỗi đọc lại: %v", err)
	}
	if !strings.Contains(string(data), "diag-export") {
		t.Errorf("Nội dung tệp bất thường\n%s", data)
	}
	if strings.Contains(string(data), sentinel) {
		t.Errorf("Tệp đã ghi còn lẫn nội dung")
	}
}

// TestRedactMessage_DupSha chứng minh cùng một đoạn văn bản lặp lại sẽ tạo cùng sha (tín hiệu vòng lặp).
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
		t.Errorf("Nội dung giống nhau phải có cùng sha: %q so với %q", a.TextSha, b.TextSha)
	}
	if a.Redacted != 1 {
		t.Errorf("Phải che 1 khối văn bản, nhận được %d", a.Redacted)
	}
}
