package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/agentcore"
)

// TestSessionStore_MetaInjected_AssistantWithUsage xác minh chỉ những thông điệp có "assistant + has Usage"
// mới được gắn _meta; đây là tiền đề để nhánh replay tính giá chính xác.
func TestSessionStore_MetaInjected_AssistantWithUsage(t *testing.T) {
	dir := t.TempDir()
	s := NewSessionStore(newIO(dir))
	lookup := ModelLookup(func(agentName string) (string, string) {
		return "meme", "gpt-5.4"
	})
	logger := s.SubAgentLogger(lookup)

	logger("writer", "Viết chương 1", agentcore.Message{
		Role:  agentcore.RoleUser,
		Usage: nil,
	})
	logger("writer", "Viết chương 1", agentcore.Message{
		Role: agentcore.RoleAssistant,
		Usage: &agentcore.Usage{
			Input: 1000, Output: 200, CacheRead: 800, TotalTokens: 1200,
		},
	})
	logger("writer", "Viết chương 1", agentcore.Message{
		Role:  agentcore.RoleAssistant,
		Usage: nil, // assistant nhưng không có usage (luồng chưa mang chunk usage cuối cùng)
	})

	entries := readJSONL(t, filepath.Join(dir, "meta/sessions/agents/writer-ch01.jsonl"))
	if len(entries) != 3 {
		t.Fatalf("entries=%d muốn 3", len(entries))
	}
	if _, has := entries[0]["_meta"]; has {
		t.Errorf("thông điệp user không nên có _meta")
	}
	if _, has := entries[2]["_meta"]; has {
		t.Errorf("assistant không có Usage không nên có _meta")
	}
	meta, ok := entries[1]["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("assistant+Usage phải có _meta map, nhận %T %v", entries[1]["_meta"], entries[1]["_meta"])
	}
	if meta["provider"] != "meme" || meta["model"] != "gpt-5.4" {
		t.Errorf("_meta = %v muốn provider=meme model=gpt-5.4", meta)
	}
}

// TestSessionStore_MetaModelSwitch xác minh khi đổi model trong lúc chạy thì _meta của các thông điệp
// tiếp theo cũng đổi theo. Đây là hỗ trợ chính xác của phương án B cho việc "chuyển /model trong cùng tiến trình".
func TestSessionStore_MetaModelSwitch(t *testing.T) {
	dir := t.TempDir()
	s := NewSessionStore(newIO(dir))

	current := "model-a"
	lookup := ModelLookup(func(agentName string) (string, string) {
		return "meme", current
	})
	logger := s.SubAgentLogger(lookup)

	logger("writer", "Viết chương 1", makeAssistantWithUsage())
	current = "model-b" // mô phỏng chuyển /model
	logger("writer", "Viết chương 1", makeAssistantWithUsage())

	entries := readJSONL(t, filepath.Join(dir, "meta/sessions/agents/writer-ch01.jsonl"))
	if len(entries) != 2 {
		t.Fatalf("entries=%d muốn 2", len(entries))
	}
	for i, want := range []string{"model-a", "model-b"} {
		meta, ok := entries[i]["_meta"].(map[string]any)
		if !ok {
			t.Fatalf("entry[%d] thiếu _meta", i)
		}
		if got := meta["model"]; got != want {
			t.Errorf("entry[%d] model = %v muốn %s", i, got, want)
		}
	}
}

// TestSessionStore_NilLookup xác minh khi lookup=nil thì việc ghi vẫn bình thường,
// chỉ là không có _meta.
func TestSessionStore_NilLookup(t *testing.T) {
	dir := t.TempDir()
	s := NewSessionStore(newIO(dir))
	logger := s.SubAgentLogger(nil)
	logger("writer", "Viết chương 1", makeAssistantWithUsage())

	rel, err := s.subAgentPath("writer", "Viết chương 1")
	if err != nil {
		t.Fatal(err)
	}
	entries := readJSONL(t, filepath.Join(dir, rel))
	if len(entries) != 1 {
		t.Fatalf("entries=%d muốn 1", len(entries))
	}
	if _, has := entries[0]["_meta"]; has {
		t.Errorf("lookup nil không nên sinh _meta")
	}
	// Nhưng các trường khác (role/usage) phải bình thường
	if entries[0]["role"] != "assistant" {
		t.Errorf("mất role: %v", entries[0]["role"])
	}
}

func TestSessionStoreContinuesAgentSequenceAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	first := NewSessionStore(newIO(dir)).SubAgentLogger(nil)
	first("architect_long", "xử lý phản hồi", makeAssistantWithUsage())

	second := NewSessionStore(newIO(dir)).SubAgentLogger(nil)
	second("architect_long", "mở rộng dàn ý", makeAssistantWithUsage())

	if got := len(readJSONL(t, filepath.Join(dir, "meta/sessions/agents/architect_long-001.jsonl"))); got != 1 {
		t.Fatalf("số entry của phiên đầu = %d, muốn 1", got)
	}
	if got := len(readJSONL(t, filepath.Join(dir, "meta/sessions/agents/architect_long-002.jsonl"))); got != 1 {
		t.Fatalf("số entry của phiên thứ hai = %d, muốn 1", got)
	}
}

func makeAssistantWithUsage() agentcore.Message {
	return agentcore.Message{
		Role:  agentcore.RoleAssistant,
		Usage: &agentcore.Usage{Input: 1000, Output: 200, TotalTokens: 1200},
	}
}

func readJSONL(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("mở %s: %v", path, err)
	}
	defer f.Close()
	var out []map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("giải mã dòng: %v\n%s", err, string(line))
		}
		out = append(out, m)
	}
	return out
}
