package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func setupArcSummaryStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, Chapters: []domain.OutlineEntry{{Title: "Ba"}, {Title: "Bốn"}}},
		},
	}}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 4; chapter++ {
		if err := s.Progress.MarkChapterComplete(chapter, 100, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "arc", Verdict: "accept", Summary: "Đánh giá cung một"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Title: "Cung một", Summary: "完成", KeyEvents: []string{"事件"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 4, Scope: "arc", Verdict: "accept", Summary: "Đánh giá cung hai"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func validArcSummaryArgs(t *testing.T) []byte {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"volume":     1,
		"arc":        2,
		"title":      "Vào núi",
		"summary":    "Nhân vật chính hoàn thành thử thách vào núi, xác nhận hướng truy tìm phía sau.",
		"key_events": []string{"Vượt qua thử thách", "Phát hiện manh mối vụ án cũ"},
		"character_snapshots": []map[string]any{
			{"name": "Thẩm Uyên", "status": "Còn sống", "motivation": "Truy tra vụ án cũ"},
		},
		"style_rules": map[string]any{
			"prose": []string{"Ưu tiên miêu tả môi trường bằng xúc giác và khứu giác", "Cảnh hành động dùng câu ngắn để đẩy nhịp", "Miêu tả tâm lý không giải thích kết luận"},
			"dialogue": []map[string]any{
				{"name": "Thẩm Uyên", "rules": []string{"Thoại cực gọn", "Hạn chế dùng câu hỏi"}},
			},
			"taboos": []string{"Tránh độc thoại dài ở cuối chương"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func TestSaveArcSummaryPersistsStyleRulesDialogueObjects(t *testing.T) {
	s := setupArcSummaryStore(t)

	tool := NewSaveArcSummaryTool(s)
	if _, err := tool.Execute(context.Background(), validArcSummaryArgs(t)); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rules, err := s.World.LoadStyleRules()
	if err != nil {
		t.Fatalf("LoadStyleRules: %v", err)
	}
	if rules == nil || len(rules.Dialogue) != 1 {
		t.Fatalf("expected one dialogue rule, got %+v", rules)
	}
	if rules.Dialogue[0].Name != "Thẩm Uyên" || len(rules.Dialogue[0].Rules) != 2 {
		t.Fatalf("unexpected dialogue rule: %+v", rules.Dialogue[0])
	}
}

func TestSaveArcSummaryWritesCompletionMarkerLast(t *testing.T) {
	s := setupArcSummaryStore(t)
	snapshotPath := filepath.Join(s.Dir(), "meta", "snapshots", "v01a02.json")
	if err := os.MkdirAll(snapshotPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := NewSaveArcSummaryTool(s).Execute(context.Background(), validArcSummaryArgs(t)); err == nil || !strings.Contains(err.Error(), "save character snapshots") {
		t.Fatalf("expected snapshot write failure, got %v", err)
	}
	if summary, err := s.Summaries.LoadArcSummary(1, 2); err != nil || summary != nil {
		t.Fatalf("arc summary must remain absent after partial failure, summary=%+v err=%v", summary, err)
	}
}

func TestSaveArcSummaryRetriesCheckpointWithoutOverwriting(t *testing.T) {
	s := setupArcSummaryStore(t)
	checkpointPath := filepath.Join(s.Dir(), "meta", "checkpoints.jsonl")
	if err := os.MkdirAll(checkpointPath, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewSaveArcSummaryTool(s)
	args := validArcSummaryArgs(t)

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "checkpoint arc summary") {
		t.Fatalf("expected checkpoint failure, got %v", err)
	}
	if summary, err := s.Summaries.LoadArcSummary(1, 2); err != nil || summary == nil {
		t.Fatalf("semantic summary should already be persisted, summary=%+v err=%v", summary, err)
	}
	if err := os.Remove(checkpointPath); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("idempotent checkpoint retry: %v", err)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ArcScope(1, 2), "arc_summary"); cp == nil {
		t.Fatal("checkpoint retry must finish the aggregate write")
	}
}

func TestSaveArcSummaryRejectsDialogueStringArray(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveArcSummaryTool(s)
	args, err := json.Marshal(map[string]any{
		"volume":              1,
		"arc":                 2,
		"title":               "Vào núi",
		"summary":             "Nhân vật chính hoàn thành thử thách vào núi, xác nhận hướng truy tìm phía sau.",
		"key_events":          []string{"Vượt qua thử thách"},
		"character_snapshots": []map[string]any{},
		"style_rules": map[string]any{
			"prose":    []string{"Ưu tiên miêu tả môi trường bằng xúc giác và khứu giác"},
			"dialogue": []string{"沈渊对话极简"},
		},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "style_rules.dialogue") {
		t.Fatalf("expected style_rules.dialogue validation error, got %v", err)
	}
}

func TestSaveArcSummaryRequiresStyleRules(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	args := json.RawMessage(`{
		"volume": 1,
		"arc": 2,
		"title": "Vào núi",
		"summary": "主角完成入山试炼。",
		"key_events": ["Vượt qua thử thách"],
		"character_snapshots": []
	}`)
	if _, err := NewSaveArcSummaryTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "style_rules is required") {
		t.Fatalf("expected missing style_rules error, got %v", err)
	}
}
