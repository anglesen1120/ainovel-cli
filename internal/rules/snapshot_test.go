package rules

import (
	"strings"
	"testing"
)

func TestBuildSnapshot_FieldOverridePrecedence(t *testing.T) {
	// Thấp -> cao: defaults đặt tu tiên, project ghi đè thành đô thị; ưu tiên cao thắng.
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{Genre: "tu tiên"}},
		{Source: "project:a.md", Structured: Structured{Genre: "đô thị"}},
	})
	if snap.Structured.Genre != "đô thị" {
		t.Fatalf("mong đợi project ghi đè defaults, got %q", snap.Structured.Genre)
	}
	if snap.Status != StatusReady {
		t.Fatalf("mong đợi ready, got %s", snap.Status)
	}
	if snap.Version != SnapshotVersion {
		t.Fatalf("version phải là %d, got %d", SnapshotVersion, snap.Version)
	}
}

func TestBuildSnapshot_EmptyAndZeroAreAbsent(t *testing.T) {
	// Bộ chuẩn hóa có thể trả placeholder: genre:"", phần tử chuỗi rỗng; tất cả phải được xem là thiếu, không ghi đè giá trị thật ưu tiên thấp.
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{
			Genre: "tu tiên",
		}},
		{Source: "startup_prompt", Structured: Structured{
			Genre:            "",                 // Chuỗi rỗng placeholder -> không ghi đè
			ForbiddenPhrases: []string{"", "  "}, // Toàn rỗng -> loại bỏ
		}},
	})
	if snap.Structured.Genre != "tu tiên" {
		t.Fatalf("genre rỗng không được ghi đè, mong đợi tu tiên, got %q", snap.Structured.Genre)
	}
	if len(snap.Structured.ForbiddenPhrases) != 0 {
		t.Fatalf("forbidden_phrases toàn rỗng phải bị loại bỏ, got %v", snap.Structured.ForbiddenPhrases)
	}
}

func TestBuildSnapshot_PreferencesPrecedenceOrder(t *testing.T) {
	snap := BuildSnapshot([]Candidate{
		{Source: "global:g.md", Preferences: "Sở thích global"},
		{Source: "project:p.md", Preferences: "Sở thích project"},
	})
	gi := strings.Index(snap.Preferences, "Sở thích global")
	pi := strings.Index(snap.Preferences, "Sở thích project")
	if gi < 0 || pi < 0 || gi > pi {
		t.Fatalf("preferences phải nối theo ưu tiên thấp -> cao (project ở sau), got:\n%s", snap.Preferences)
	}
	if !strings.Contains(snap.Preferences, "## [global:g.md]") {
		t.Fatalf("preferences phải có tiêu đề nguồn, got:\n%s", snap.Preferences)
	}
}

func TestBuildSnapshot_FatigueWordsMergeByWord(t *testing.T) {
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{FatigueWords: map[string]int{"bất ngờ": 1, "như thể": 2}}},
		{Source: "project:p.md", Structured: Structured{FatigueWords: map[string]int{"như thể": 5}}},
	})
	if snap.Structured.FatigueWords["bất ngờ"] != 1 {
		t.Fatalf("bất ngờ phải giữ ngưỡng defaults 1, got %d", snap.Structured.FatigueWords["bất ngờ"])
	}
	if snap.Structured.FatigueWords["như thể"] != 5 {
		t.Fatalf("như thể phải được project ghi đè thành 5, got %d", snap.Structured.FatigueWords["như thể"])
	}
}

func TestBuildSnapshot_DegradedPropagates(t *testing.T) {
	snap := BuildSnapshot([]Candidate{
		{Source: "system_defaults", Structured: Structured{FatigueWords: map[string]int{"bất ngờ": 1}}},
		{Source: "project:bad.md", Preferences: "Hạ cấp nguyên văn", Degraded: true},
	})
	if snap.Status != StatusDegraded {
		t.Fatalf("bất kỳ nguồn nào hạ cấp thì status=degraded, got %s", snap.Status)
	}
	// Nguồn hạ cấp vẫn đi vào dưới dạng raw preferences, không chặn; structured từ nguồn khác vẫn bình thường.
	if len(snap.Structured.FatigueWords) == 0 {
		t.Fatalf("hạ cấp không được ảnh hưởng structured của nguồn khác")
	}
	if !strings.Contains(snap.Preferences, "Hạ cấp nguyên văn") {
		t.Fatalf("nguồn hạ cấp phải được giữ như raw preferences")
	}
}

func TestSystemDefaults_MatchesLegacyDefaultMD(t *testing.T) {
	d := SystemDefaults().Structured
	if len(d.ForbiddenPhrases) != 4 {
		t.Fatalf("cụm cấm mặc định phải có 4 mục, got %d", len(d.ForbiddenPhrases))
	}
	if len(d.FatigueWords) != 16 {
		t.Fatalf("từ gây mệt mỏi mặc định phải có 16 mục, got %d", len(d.FatigueWords))
	}
}
