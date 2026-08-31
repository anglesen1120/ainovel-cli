package imp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
)

func factsN(n int) []ImportedChapterFacts {
	out := make([]ImportedChapterFacts, n)
	for i := 0; i < n; i++ {
		out[i] = ImportedChapterFacts{
			Chapter: i + 1, Title: "Chương " + itoa(i+1), CoreEvent: "Sự kiện", Summary: "Tóm tắt",
			HookType: "mystery", DominantStrand: "quest",
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestValidateStructure(t *testing.T) {
	ok := []ImportedVolumeRange{{Title: "Quyển một", Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 3}}}}
	if err := validateStructure(ok, 3); err != nil {
		t.Fatalf("Cấu trúc hợp lệ phải qua: %v", err)
	}
	gap := []ImportedVolumeRange{{Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 2}, {StartChapter: 4, EndChapter: 5}}}}
	if err := validateStructure(gap, 5); err == nil {
		t.Fatal("Khoảng trống phải bị từ chối")
	}
	short := []ImportedVolumeRange{{Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 2}}}}
	if err := validateStructure(short, 3); err == nil {
		t.Fatal("Chưa bao phủ N phải bị từ chối")
	}
}

func TestAssembleFoundationHappyClosed(t *testing.T) {
	facts := factsN(3)
	s := &BookSynthesis{
		Synopsis:     "Giới thiệu không tiết lộ cốt truyện",
		Premise:      "# Tiền đề câu chuyện\n\nTiền đề",
		Characters:   []domain.Character{{Name: "A"}},
		PlanningTier: domain.PlanningTierShort,
		StoryStatus:  storyClosed,
		Compass:      domain.StoryCompass{EndingDirection: "Kết thúc gọn"},
		Structure:    []ImportedVolumeRange{{Title: "Quyển một", Arcs: []ImportedArcRange{{Title: "Cung một", StartChapter: 1, EndChapter: 3}}}},
	}
	f, err := AssembleFoundation(s, facts, true, "book.txt")
	if err != nil {
		t.Fatalf("Lắp ghép phải thành công: %v", err)
	}
	if len(domain.FlattenOutline(f.Volumes)) != 3 {
		t.Fatal("Số chương sau khi trải phẳng phải là 3")
	}
	if !f.Volumes[len(f.Volumes)-1].Final {
		t.Fatal("Khi closed thì quyển cuối phải Final")
	}
	if f.Book.Title != "book" || f.Book.Synopsis != "Giới thiệu không tiết lộ cốt truyện" {
		t.Fatalf("Thông tin tác phẩm lắp ghép sai: %+v", f.Book)
	}
}

func TestAssembleFoundationTitleMismatch(t *testing.T) {
	facts := factsN(2)
	facts[1].Title = "" // Làm hỏng tính nhất quán tiêu đề sẽ bị kiểm tra lỗi ở FlattenOutline? Tiêu đề rỗng nhưng cấu trúc lấy từ facts, nên vẫn nhất quán.
	// Tạo bất nhất thực sự bằng cách dùng cấu trúc không bao phủ đủ chương: số chương không khớp.
	s := &BookSynthesis{
		Synopsis: "Giới thiệu không tiết lộ cốt truyện", Premise: "# Tiền đề câu chuyện", Characters: []domain.Character{{Name: "A"}},
		PlanningTier: domain.PlanningTierShort, StoryStatus: storyOpen,
		Compass:   domain.StoryCompass{EndingDirection: "x"},
		Structure: []ImportedVolumeRange{{Arcs: []ImportedArcRange{{StartChapter: 1, EndChapter: 1}}}},
	}
	if _, err := AssembleFoundation(s, facts, false, "b.txt"); err == nil {
		t.Fatal("Cấu trúc chỉ bao phủ 1 chương trong khi dữ kiện có 2 chương phải bị từ chối")
	}
}

func TestImportedBookTitle(t *testing.T) {
	if got := importedBookTitle("tiểu-thuyết-của-tôi.txt"); got != "tiểu-thuyết-của-tôi" {
		t.Fatalf("Phải suy ra tên sách từ tên tệp: %q", got)
	}
}

func TestPlanFactRangesSplits(t *testing.T) {
	facts := factsN(20)
	one := len(compactFact(facts[0]))
	ranges := planFactRanges(facts, one*3) // Mỗi khoảng khoảng 3 chương
	if len(ranges) < 2 {
		t.Fatalf("Phải chia thành nhiều khoảng, được %d", len(ranges))
	}
	if ranges[0][0] != 0 || ranges[len(ranges)-1][1] != 20 {
		t.Fatal("Khoảng chưa bao phủ đầy đủ")
	}
}

// TestToCompactCarriesEvidence bảo vệ #6: evidence character/world suy ngược theo từng chương phải đi vào chế độ xem gọn tổng hợp,
// nếu không bộ tổng hợp chỉ có thể bịa ra nhân vật chính thức và quy tắc thế giới từ phần tóm tắt.
func TestToCompactCarriesEvidence(t *testing.T) {
	f := ImportedChapterFacts{
		Chapter: 1, Title: "Chương một", CoreEvent: "e", Summary: "s",
		CharacterEvidence: []ImportedCharacterFact{{Chapter: 1, Name: "A", Note: "điềm tĩnh"}},
		WorldEvidence:     []ImportedWorldFact{{Chapter: 1, Category: "magic", Fact: "linh khí dồi dào"}},
	}
	cv := toCompact(f)
	if len(cv.CharacterEvidence) != 1 || cv.CharacterEvidence[0].Name != "A" {
		t.Fatalf("character evidence chưa được đưa vào chế độ xem gọn: %+v", cv.CharacterEvidence)
	}
	if len(cv.WorldEvidence) != 1 || cv.WorldEvidence[0].Fact != "linh khí dồi dào" {
		t.Fatalf("world evidence chưa được đưa vào chế độ xem gọn: %+v", cv.WorldEvidence)
	}
}

// TestSynthesizeRejectsRangeMismatch bảo vệ #4: phần tóm tắt khoảng ở giai đoạn Map của sách dài phải có chương bắt đầu/kết thúc khớp với yêu cầu,
// nếu không khi gộp sẽ lấy nhầm khoảng lệch làm phần tóm tắt của khoảng hiện tại.
func TestSynthesizeRejectsRangeMismatch(t *testing.T) {
	err := validateRangeDigest(&RangeDigest{StartChapter: 1, EndChapter: 5, Plot: "khoảng lệch"}, 1, 2, "range digest")
	if err == nil {
		t.Fatal("Khoảng bắt đầu/kết thúc không khớp với yêu cầu phải bị từ chối")
	}
	if !strings.Contains(err.Error(), "phạm vi chương") {
		t.Fatalf("Lỗi phải chỉ ra phạm vi khoảng không khớp, được: %v", err)
	}
}

// TestGroupDigestsByBudget bảo vệ #3 gộp nhóm: các tóm tắt khoảng liên tiếp được chia thành nhóm liên tiếp theo ngân sách byte, một tóm tắt vượt ngân sách cũng tự thành một nhóm riêng.
func TestGroupDigestsByBudget(t *testing.T) {
	ds := []RangeDigest{
		{StartChapter: 1, EndChapter: 5, Plot: strings.Repeat("x", 200)},
		{StartChapter: 6, EndChapter: 10, Plot: strings.Repeat("y", 200)},
		{StartChapter: 11, EndChapter: 15, Plot: strings.Repeat("z", 200)},
		{StartChapter: 16, EndChapter: 20, Plot: strings.Repeat("w", 200)},
	}
	per := len(mustJSON(t, ds[0]))
	groups := groupDigestsByBudget(ds, per*2+10) // Mỗi nhóm khoảng chứa được 2 cái
	if len(groups) != 2 || len(groups[0]) != 2 || len(groups[1]) != 2 {
		t.Fatalf("Phải chia thành 2 nhóm, mỗi nhóm 2 cái, được %v", groups)
	}
	if groups[0][0].StartChapter != 1 || groups[1][1].EndChapter != 20 {
		t.Fatal("Chia nhóm không giữ được tính liên tục bao phủ")
	}
}

// TestReduceToFitMergesUntilBudget bảo vệ #3: khi tổng lượng tóm tắt khoảng vượt ngân sách thì phải gộp từng tầng cho đến khi vừa sức chứa,
// chứ không đi không giới hạn vào lời gọi tổng hợp cuối cùng.
func TestReduceToFitMergesUntilBudget(t *testing.T) {
	ds := []RangeDigest{
		{StartChapter: 1, EndChapter: 5, Plot: strings.Repeat("x", 200)},
		{StartChapter: 6, EndChapter: 10, Plot: strings.Repeat("y", 200)},
		{StartChapter: 11, EndChapter: 15, Plot: strings.Repeat("z", 200)},
		{StartChapter: 16, EndChapter: 20, Plot: strings.Repeat("w", 200)},
	}
	budget := len(mustJSON(t, ds[0]))*2 + 10
	// Mỗi nhóm gộp ra một tóm tắt nhỏ: chương 1-10, chương 11-20.
	m := &mockModel{responses: []string{
		rangeDigestJSON(1, 10, "Gộp một"),
		rangeDigestJSON(11, 20, "Gộp hai"),
	}}
	out, err := reduceToFit(context.Background(), m, "range", ds, budget, 4096, callProfile{})
	if err != nil {
		t.Fatalf("reduceToFit: %v", err)
	}
	if len(out) != 2 || out[0].StartChapter != 1 || out[0].EndChapter != 10 || out[1].StartChapter != 11 || out[1].EndChapter != 20 {
		t.Fatalf("Phải gộp thành 2 tóm tắt khoảng liên tiếp, được %+v", out)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSynthesizeDirectWithMock(t *testing.T) {
	facts := factsN(3)
	resp := synthesisFixtureJSON(3, storyOpen)
	m := &mockModel{responses: []string{resp}}
	s, err := Synthesize(context.Background(), m, "sys", "range-sys", &Workspace{dir: t.TempDir()}, facts, 0, 4096, callProfile{})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if s.StoryStatus != storyOpen || len(s.Structure) != 1 {
		t.Fatalf("Kết quả tổng hợp không khớp: %+v", s)
	}
	if _, err := AssembleFoundation(s, facts, false, "b.txt"); err != nil {
		t.Fatalf("Lắp ghép phải thành công: %v", err)
	}
	_ = agentcore.StopReasonStop
}
