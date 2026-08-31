package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestInvalidateChapterAggregatesRemovesAffectedArtifacts(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "Chương một"}, {Chapter: 2, Title: "Chương hai"},
			},
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Summary: "Tóm tắt cung cũ"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Summaries.SaveVolumeSummary(domain.VolumeSummary{Volume: 1, Summary: "Tóm tắt quyển cũ"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Characters.SaveSnapshots(1, 1, []domain.CharacterSnapshot{{Volume: 1, Arc: 1, Name: "Lâm Mặc"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveStyleRules(domain.WritingStyleRules{Volume: 1, Arc: 1, Prose: []string{"Quy tắc cũ"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "arc", Verdict: "accept", Summary: "Đánh giá cũ"}); err != nil {
		t.Fatal(err)
	}

	if err := st.InvalidateChapterAggregates(1); err != nil {
		t.Fatal(err)
	}
	if sum, _ := st.Summaries.LoadArcSummary(1, 1); sum != nil {
		t.Fatalf("Tóm tắt cung chưa bị vô hiệu hóa: %+v", sum)
	}
	if sum, _ := st.Summaries.LoadVolumeSummary(1); sum != nil {
		t.Fatalf("Tóm tắt quyển chưa bị vô hiệu hóa: %+v", sum)
	}
	if snapshots, _ := st.Characters.LoadSnapshots(1, 1); len(snapshots) != 0 {
		t.Fatalf("Ảnh chụp nhân vật chưa bị vô hiệu hóa: %+v", snapshots)
	}
	if rules, _ := st.World.LoadStyleRules(); rules != nil {
		t.Fatalf("Quy tắc viết chưa bị vô hiệu hóa: %+v", rules)
	}
	if review, _ := st.World.LoadReview(2); review != nil {
		t.Fatalf("Đánh giá chưa bị vô hiệu hóa: %+v", review)
	}
}
