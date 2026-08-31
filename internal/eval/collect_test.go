package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestCollectReadsStyleUsageAndToolCalls(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	progress := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		TotalChapters:     5,
		CompletedChapters: []int{3, 1, 5, 2, 4},
	}
	if err := s.Progress.Save(progress); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Chương 1: Gió nổi"},
		{Chapter: 2, Title: "Phá cục"},
		{Chapter: 3, Title: "Chương 3: Vào cục"},
		{Chapter: 4, Title: "Truy vấn"},
		{Chapter: 5, Title: "Chương 5: Hồi âm"},
	}); err != nil {
		t.Fatalf("save outline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "Lâm Mặc", Aliases: []string{"dược đồng"}}}); err != nil {
		t.Fatalf("save characters: %v", err)
	}
	for ch := 1; ch <= 5; ch++ {
		text := "# Tiêu đề\n\nKhông phải gió ngừng, mà là tất cả mọi người đều nín thở. Im lặng một lúc, Lâm Mặc siết chặt túi thuốc."
		if err := s.Drafts.SaveFinalChapter(ch, text); err != nil {
			t.Fatalf("save chapter %d: %v", ch, err)
		}
	}
	if err := s.Usage.Save(domain.UsageState{
		Overall:      domain.AgentUsageTotals{Input: 100, Output: 40, Cost: 0.12},
		MissingUsage: 1,
	}); err != nil {
		t.Fatalf("save usage: %v", err)
	}
	writeSessionLine(t, dir, "meta/sessions/agents/writer-ch01.jsonl", agentcore.Message{
		Role: agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{
			agentcore.ToolCallBlock(agentcore.ToolCall{Name: "dispatch"}),
			agentcore.ToolCallBlock(agentcore.ToolCall{Name: "novel_context"}),
		},
	})

	col := Collect(dir, nil)
	if len(col.LoadErrors) != 0 {
		t.Fatalf("Không nên có lỗi đọc: %v", col.LoadErrors)
	}
	if col.Style.Status != "ok" || col.Style.Stats == nil {
		t.Fatalf("stylestat phải tính được, nhận status=%s stats=%v", col.Style.Status, col.Style.Stats)
	}
	if col.Style.Stats.TitleFormats == nil {
		t.Fatal("stylestat phải bắt được việc trộn lẫn tiêu đề")
	}
	if !col.Usage.UsageRecorded || col.Usage.Input != 100 || col.Usage.Output != 40 || col.Usage.CostUSD != 0.12 {
		t.Fatalf("Đọc usage không đúng: %+v", col.Usage)
	}
	if col.ToolCalls != 2 {
		t.Fatalf("tool calls = %d want 2", col.ToolCalls)
	}
}

func TestCollectStyleInsufficientSample(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(1, "Chỉ có một chương"); err != nil {
		t.Fatalf("save chapter: %v", err)
	}
	col := Collect(dir, nil)
	if col.Style.Status != "insufficient_sample" {
		t.Fatalf("Mẫu một chương phải là insufficient_sample, nhận %s", col.Style.Status)
	}
}

func TestCollectFailsLoudWhenCompletedChapterMissing(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Progress.Save(&domain.Progress{CompletedChapters: []int{1}}); err != nil {
		t.Fatalf("save progress: %v", err)
	}
	col := Collect(dir, nil)
	if !containsString(col.LoadErrors, "progress đánh dấu đã hoàn thành nhưng bản cuối trống") {
		t.Fatalf("Thiếu bản cuối phải vào LoadErrors, thực tế %v", col.LoadErrors)
	}
}

func TestChapterTitleUsesLayeredEntryChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{
		{
			Index: 1,
			Arcs: []domain.ArcOutline{
				{Index: 1}, // Arc chưa mở rộng không được làm lệch vị trí các chương phía sau
				{
					Index: 2,
					Chapters: []domain.OutlineEntry{
						{Chapter: 7, Title: "Chương bảy: Tiêu đề thật"},
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("save layered outline: %v", err)
	}

	got := chapterTitle(s, 7, "# Tiêu đề dự phòng của thân bài\n\nNội dung", func(string, error) {})
	if got != "Chương bảy: Tiêu đề thật" {
		t.Fatalf("Phải khớp tiêu đề phân tầng theo entry.Chapter, nhận %q", got)
	}
}

func TestChapterTitleUsesCommittedTitle(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Tiêu đề kế hoạch"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "Tiêu đề bản cuối"}); err != nil {
		t.Fatal(err)
	}
	got := chapterTitle(s, 1, "# Tiêu đề nội dung\n\nNội dung", func(string, error) {})
	if got != "Tiêu đề bản cuối" {
		t.Fatalf("chapterTitle = %q, want committed title", got)
	}
}

func containsString(items []string, sub string) bool {
	for _, item := range items {
		if strings.Contains(item, sub) {
			return true
		}
	}
	return false
}

func writeSessionLine(t *testing.T, root, rel string, msg agentcore.Message) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir session: %v", err)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}
