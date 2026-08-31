package imp

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

// TestDiscardAnalysesAfter Cảnh giới #4a: dọn sạch các tác phẩm phân tích cũ nằm vượt quá tiền tố mới,
// bảo đảm "phân tích lại một chương sẽ làm vô hiệu toàn bộ phân tích phía sau", ngăn ledger cũ bị tái sử dụng cho các chương tiếp theo.
func TestDiscardAnalysesAfter(t *testing.T) {
	ws := OpenWorkspace(t.TempDir())
	for c := 1; c <= 5; c++ {
		if err := writeArtifact(ws, analysisPath(c), "d", ChapterAnalysisPayload{Facts: ImportedChapterFacts{Chapter: c}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := discardAnalysesAfter(ws, 2, 5); err != nil {
		t.Fatalf("Dọn dẹp không được thất bại: %v", err)
	}
	for c := 1; c <= 2; c++ {
		if !ws.has(analysisPath(c)) {
			t.Fatalf("Chương %d trong tiền tố mới phải được giữ lại", c)
		}
	}
	for c := 3; c <= 5; c++ {
		if ws.has(analysisPath(c)) {
			t.Fatalf("Chương %d vượt quá tiền tố mới phải được dọn sạch", c)
		}
	}
}

// analyzeFixture dựng một phân tách gồm n chương, với phần thân đều rất ngắn, dùng cho các test batch/phân tích.
func analyzeFixture(t *testing.T, n int) ([]byte, *Segmentation) {
	t.Helper()
	var b strings.Builder
	for c := 1; c <= n; c++ {
		b.WriteString("Chương ")
		b.WriteString(strconv.Itoa(c))
		b.WriteString("\nNội dung\n")
	}
	norm := []byte(b.String())
	units := buildSourceUnits(norm, 0)
	var ds []BoundaryDecision
	for i := 0; i < len(units); i += 2 { // mỗi 2 dòng là một chương (dòng tiêu đề + dòng nội dung)
		ds = append(ds, BoundaryDecision{UnitID: units[i].ID, Kind: kindChapter, Title: units[i].Text})
	}
	seg, err := resolveSegmentation(norm, units, ds)
	if err != nil {
		t.Fatalf("fixture phân tách thất bại: %v", err)
	}
	if len(seg.Chapters) != n {
		t.Fatalf("Số chương của fixture %d != %d", len(seg.Chapters), n)
	}
	return norm, seg
}

func TestPlanBatchOutputBudgetCaps(t *testing.T) {
	_, seg := analyzeFixture(t, 10)
	// Input rộng rãi, nhưng ngân sách output nhìn thấy chỉ đủ cho 2 chương (#83 cảnh giới độ lớn batch, §20.4.2).
	b := AnalyzeBudget{ContextBytes: 1 << 20, MaxOutputTokens: 250, PerChapterOutput: 100, PromptOverhead: 0}
	end := planBatch(seg.Chapters, 0, 0, b)
	if end != 2 {
		t.Fatalf("Ngân sách output phải giới hạn batch ở 2 chương, nhận end=%d", end)
	}
}

func TestPlanBatchInputBudgetCaps(t *testing.T) {
	_, seg := analyzeFixture(t, 10)
	// Output rộng rãi, nhưng ngân sách byte input chỉ đủ khoảng 1 chương.
	one := chapterBytes(seg.Chapters, 0)
	b := AnalyzeBudget{ContextBytes: one + 1, MaxOutputTokens: 1 << 20, PerChapterOutput: 1, PromptOverhead: 0}
	end := planBatch(seg.Chapters, 0, 0, b)
	if end != 1 {
		t.Fatalf("Ngân sách input phải giới hạn batch ở 1 chương, nhận end=%d", end)
	}
}

func factsJSON(chapter int, title string) string {
	f := map[string]any{
		"chapter": chapter, "title": title, "summary": "Tóm tắt", "core_event": "Sự kiện cốt lõi",
		"key_events": []string{"Sự kiện"}, "hook": nil, "scenes": []string{}, "characters": []string{},
		"character_evidence": []any{}, "world_evidence": []any{}, "timeline_events": []any{},
		"foreshadow_updates": []any{}, "relationship_changes": []any{}, "state_changes": []any{},
		"hook_type": "mystery", "dominant_strand": "quest",
	}
	data, _ := json.Marshal(f)
	return string(data)
}

func TestValidateBatchRejections(t *testing.T) {
	_, seg := analyzeFixture(t, 2)
	// Số lượng không khớp
	bad := &AnalysisBatchResult{Chapters: []ImportedChapterFacts{{Chapter: 1}}}
	if err := validateBatch(bad, seg, 0, 2); err == nil {
		t.Fatal("Số lượng không khớp phải bị từ chối")
	}
	// hook_type không hợp lệ
	var f ImportedChapterFacts
	_ = json.Unmarshal([]byte(factsJSON(1, seg.Chapters[0].Title)), &f)
	f.HookType = "bogus"
	if err := validateBatch(&AnalysisBatchResult{Chapters: []ImportedChapterFacts{f}}, seg, 0, 1); err == nil {
		t.Fatal("hook_type không hợp lệ phải bị từ chối")
	}
	// Biến thể hoa/thường của enum: kiểm tra vẫn qua và tự chuẩn hóa tại chỗ thành chữ thường — commit_chapter không kiểm tra lại enum,
	// nên biến thể đi thẳng vào trạng thái chính thức sẽ bị logic tiêu thụ chuỗi chính xác coi là kiểu không xác định.
	_ = json.Unmarshal([]byte(factsJSON(1, seg.Chapters[0].Title)), &f)
	f.HookType, f.DominantStrand = "Crisis", "QUEST"
	got := &AnalysisBatchResult{Chapters: []ImportedChapterFacts{f}}
	if err := validateBatch(got, seg, 0, 1); err != nil {
		t.Fatalf("Biến thể hoa/thường phải qua kiểm tra: %v", err)
	}
	if got.Chapters[0].HookType != "crisis" || got.Chapters[0].DominantStrand != "quest" {
		t.Fatalf("Enum phải được chuẩn hóa thành chữ thường khi ghi xuống đĩa: %+v", got.Chapters[0])
	}
}

func TestAnalyzeNextPersistsWithRebatchOnTruncation(t *testing.T) {
	norm, seg := analyzeFixture(t, 2)
	book := t.TempDir()
	ws := &Workspace{dir: book}
	// Batch đầu 2 chương bị cắt cụt: chương 1 đầy đủ, chương 2 mới nửa chừng → cứu vớt tiền tố liên tục của chương 1 (§9.5).
	truncated := `{"chapters":[` + factsJSON(1, seg.Chapters[0].Title) + `,{"chapter":2,"summary":"Cắt cụt`
	m := &mockModel{
		responses: []string{truncated},
		stops:     []agentcore.StopReason{agentcore.StopReasonLength},
	}
	budget := AnalyzeBudget{ContextBytes: 1 << 20, MaxOutputTokens: 1000, PerChapterOutput: 10, PromptOverhead: 0}
	done, err := AnalyzeNext(context.Background(), m, "sys", ws, norm, seg, "segid", "v1", budget, callProfile{})
	if err != nil {
		t.Fatalf("AnalyzeNext: %v", err)
	}
	if done != 1 {
		t.Fatalf("Trường hợp bị cắt cụt phải vớt được tiền tố liên tục của chương 1, nhận %d", done)
	}
	if !ws.has(analysisPath(1)) || ws.has(analysisPath(2)) {
		t.Fatal("Chỉ nên ghi xuống đĩa chương 1")
	}
	if analyzedChapters(ws, seg, norm, "segid", "v1") != 1 {
		t.Fatal("Số chương đã phân tích phải là 1")
	}
	// failures/ phải lưu phản hồi gốc và trạng thái vớt lại (§14.2).
	if !ws.has("failures/last-response.txt") || !ws.has("failures/last.json") {
		t.Fatal("Phải lưu phản hồi gốc và siêu dữ liệu của lần thất bại")
	}
}

func TestSalvagePrefixContiguous(t *testing.T) {
	_, seg := analyzeFixture(t, 3)
	// 2 chương đầu đầy đủ, chương 3 bị cắt cụt.
	raw := `{"chapters":[` +
		factsJSON(1, seg.Chapters[0].Title) + `,` +
		factsJSON(2, seg.Chapters[1].Title) + `,` +
		`{"chapter":3,"summary":"Cắt cụt`
	got := salvagePrefix(raw, seg, 0)
	if len(got) != 2 {
		t.Fatalf("Phải vớt được tiền tố liên tục của 2 chương đầu, nhận %d", len(got))
	}
	if got[0].Chapter != 1 || got[1].Chapter != 2 {
		t.Fatal("Số chương trong tiền tố không liên tục")
	}
}

func TestSalvagePrefixStopsAtGap(t *testing.T) {
	_, seg := analyzeFixture(t, 3)
	// Sau chương 1 nhảy thẳng sang chương 3 → việc vớt dừng tại chỗ nhảy số, chỉ trả về chương 1.
	raw := `{"chapters":[` + factsJSON(1, seg.Chapters[0].Title) + `,` + factsJSON(3, seg.Chapters[2].Title) + `]}`
	got := salvagePrefix(raw, seg, 0)
	if len(got) != 1 {
		t.Fatalf("Phải dừng ở chỗ nhảy số, nhận %d", len(got))
	}
}

// TestAnalyzedChaptersInvalidatesOnUpstreamChange kiểm tra rằng đổi danh tính chia tách hoặc phiên bản prompt sẽ làm phân tích đã ghi xuống đĩa mất hiệu lực (bất biến 1).
// Đây là cốt lõi để cơ chế InputDigest thực sự hoạt động: đổi upstream thì downstream mất hiệu lực, thay vì chỉ nhìn file có tồn tại hay không.
func TestAnalyzedChaptersInvalidatesOnUpstreamChange(t *testing.T) {
	norm, seg := analyzeFixture(t, 2)
	ws := &Workspace{dir: t.TempDir()}
	m := &mockModel{responses: []string{
		`{"chapters":[` + factsJSON(1, seg.Chapters[0].Title) + `,` + factsJSON(2, seg.Chapters[1].Title) + `]}`,
	}}
	budget := AnalyzeBudget{ContextBytes: 1 << 20, MaxOutputTokens: 1 << 20, PerChapterOutput: 10, PromptOverhead: 0}
	if _, err := AnalyzeNext(context.Background(), m, "sys", ws, norm, seg, "segid-A", "v1", budget, callProfile{}); err != nil {
		t.Fatalf("AnalyzeNext: %v", err)
	}
	if got := analyzedChapters(ws, seg, norm, "segid-A", "v1"); got != 2 {
		t.Fatalf("Với cùng danh tính/phiên bản phải công nhận 2 chương, nhận %d", got)
	}
	if got := analyzedChapters(ws, seg, norm, "segid-B", "v1"); got != 0 {
		t.Fatalf("Thay đổi danh tính chia tách phải làm vô hiệu toàn bộ phân tích, nhận %d", got)
	}
	if got := analyzedChapters(ws, seg, norm, "segid-A", "v2"); got != 0 {
		t.Fatalf("Thay đổi phiên bản prompt phải làm vô hiệu toàn bộ phân tích, nhận %d", got)
	}
}
