package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestReviseOutlineSchemaIsStrict(t *testing.T) {
	tool := NewReviseOutlineTool(store.NewStore(t.TempDir()))
	if !tool.StrictSchema() {
		t.Fatal("revise_outline must use strict schema")
	}
	if err := llmcontract.ValidateStrictReady(tool.Schema()); err != nil {
		t.Fatalf("revise_outline schema is not strict-ready: %v", err)
	}
}

func TestReviseOutlineReplacesFlatTailIdempotently(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Đã hoàn tất"},
		{Chapter: 2, Title: "Cũ hai"},
		{Chapter: 3, Title: "Cũ ba"},
		{Chapter: 4, Title: "Cũ bốn"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"from_chapter": 2,
		"replacement": []map[string]any{
			{"title": "Mới hai", "core_event": "bước ngoặt", "hook": "truy tìm", "scenes": []string{"hiện trường"}},
			{"title": "Mới ba", "core_event": "tiết lộ", "hook": "khủng hoảng", "scenes": []string{}},
		},
		"reason": "điều chỉnh phần tiếp theo dựa trên chính văn đã hoàn tất",
	})
	tool := NewReviseOutlineTool(s)
	for i := 0; i < 2; i++ {
		if _, err := tool.Execute(context.Background(), args); err != nil {
			t.Fatalf("Execute #%d: %v", i+1, err)
		}
	}

	outline, err := s.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	if len(outline) != 3 || outline[0].Title != "Đã hoàn tất" || outline[1].Title != "Mới hai" || outline[2].Title != "Mới ba" {
		t.Fatalf("unexpected revised outline: %+v", outline)
	}
	for i, entry := range outline {
		if entry.Chapter != i+1 {
			t.Fatalf("outline chapter numbering broken: %+v", outline)
		}
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if progress.TotalChapters != 3 {
		t.Fatalf("TotalChapters = %d, want 3", progress.TotalChapters)
	}
	if cp := s.Checkpoints.LatestByStep(domain.GlobalScope(), "revise_outline"); cp == nil {
		t.Fatal("revise_outline checkpoint missing")
	}
}

func TestReviseOutlineProtectsCompletedChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(2); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Một"}, {Chapter: 2, Title: "Hai"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"from_chapter":1,"replacement":[{"title":"viết lại","core_event":"viết lại","hook":"tiếp tục","scenes":[]}],"reason":"kiểm thử"}`)
	if _, err := NewReviseOutlineTool(s).Execute(context.Background(), args); err == nil {
		t.Fatal("expected completed chapter revision to be rejected")
	}
	outline, err := s.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	if len(outline) != 2 || outline[0].Title != "Một" {
		t.Fatalf("rejected revision changed outline: %+v", outline)
	}
}

func TestReviseOutlinePreservesOtherLayeredArcs(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Title: "Tập một",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "Cung một", Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "Đã hoàn tất"},
				{Chapter: 2, Title: "Cũ hai"},
				{Chapter: 3, Title: "Cũ ba"},
			}},
			{Index: 2, Title: "Cung hai", Chapters: []domain.OutlineEntry{
				{Chapter: 4, Title: "Giữ bốn"},
				{Chapter: 5, Title: "Giữ năm"},
			}},
		},
	}}
	if err := s.Progress.Init(5); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatal(err)
	}

	args := json.RawMessage(`{"from_chapter":2,"replacement":[{"title":"Mới hai","core_event":"bước ngoặt","hook":"tiếp tục","scenes":[]}],"reason":"nén cung hiện tại"}`)
	tool := NewReviseOutlineTool(s)
	var rawResult json.RawMessage
	for i := 0; i < 2; i++ {
		var err error
		rawResult, err = tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Execute #%d: %v", i+1, err)
		}
	}
	var result map[string]any
	if err := json.Unmarshal(rawResult, &result); err != nil {
		t.Fatal(err)
	}
	if result["dynamic_planning"] != true || result["outlined_chapters"] != float64(4) {
		t.Fatalf("layered revise result = %#v", result)
	}
	if _, exists := result["total_chapters"]; exists {
		t.Fatalf("chỉnh sửa phân tầng không được công bố tổng số chương cố định: %#v", result)
	}

	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatal(err)
	}
	if got := layered[0].Arcs[0].Chapters; len(got) != 2 || got[1].Title != "Mới hai" {
		t.Fatalf("target arc revision = %+v", got)
	}
	if got := layered[0].Arcs[1].Chapters; len(got) != 2 || got[0].Title != "Giữ bốn" || got[1].Title != "Giữ năm" {
		t.Fatalf("following arc changed: %+v", got)
	}
	outline, err := s.Outline.LoadOutline()
	if err != nil {
		t.Fatal(err)
	}
	if len(outline) != 4 || outline[2].Title != "Giữ bốn" || outline[2].Chapter != 3 {
		t.Fatalf("flat projection not regenerated: %+v", outline)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if progress.TotalChapters != 4 {
		t.Fatalf("TotalChapters = %d, want 4", progress.TotalChapters)
	}
}
