package domain

import "testing"

func TestMergeStyleDeltaPreservesEarlierEvidence(t *testing.T) {
	merged := MergeStyleDelta(
		StyleDelta{
			Prose:    []string{"Giảm giải thích"},
			Dialogue: []CharacterVoice{{Name: "Lâm Mặc", Rules: []string{"Ít dùng câu cảm thán"}}},
		},
		StyleDelta{
			Prose:    []string{"Giảm giải thích", "Động tác trực tiếp hơn"},
			Dialogue: []CharacterVoice{{Name: "Lâm Mặc", Rules: []string{"Câu ngắn"}}},
		},
	)
	if len(merged.Prose) != 2 || len(merged.Dialogue) != 1 || len(merged.Dialogue[0].Rules) != 2 {
		t.Fatalf("kết quả gộp style delta = %+v", merged)
	}
}
