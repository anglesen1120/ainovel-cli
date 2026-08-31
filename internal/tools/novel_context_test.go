package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

func newTestContextTool(st *store.Store, refs References, style string) *ContextTool {
	return NewContextTool(st, refs, style, NewStyleStatsIndex(st))
}

func TestBuildProgressStatusHidesLayeredCapacityEstimate(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 66, Layered: true}); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, EstimatedChapters: 64},
		},
	}}); err != nil {
		t.Fatal(err)
	}

	result := map[string]any{}
	newTestContextTool(st, References{}, "default").buildProgressStatus(result, &contextReads{})
	status, ok := result["progress_status"].(map[string]any)
	if !ok {
		t.Fatalf("progress_status = %#v", result["progress_status"])
	}
	if status["dynamic_planning"] != true || status["outlined_chapters"] != 2 {
		t.Fatalf("tiến độ lập kế hoạch động sai: %#v", status)
	}
	if _, exists := status["total_chapters"]; exists {
		t.Fatalf("ước lượng dung lượng phân tầng không được công bố dưới dạng total_chapters: %#v", status)
	}
}

func TestContextToolInjectsStyleStats(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	progress := &domain.Progress{TotalChapters: 10}
	body := "# Chương N\nAnh không do dự, chỉ đang sợ hãi. Im lặng vài nhịp thở. Ánh sáng vụt qua.\nMàn đêm buông xuống.\nAnh bước đi."
	for ch := 1; ch <= 6; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("SaveFinalChapter: %v", err)
		}
		progress.CompletedChapters = append(progress.CompletedChapters, ch)
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	tool := newTestContextTool(st, References{}, "default")
	args, _ := json.Marshal(map[string]any{"chapter": 7})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Episodic map[string]json.RawMessage `json:"episodic_memory"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	statsRaw, ok := payload.Episodic["style_stats"]
	if !ok {
		t.Fatalf("expected episodic_memory.style_stats, got keys %v", keysOf(payload.Episodic))
	}
	var stats struct {
		Chapters int `json:"chapters"`
		Patterns []struct {
			Name  string `json:"name"`
			Total int    `json:"total"`
		} `json:"patterns"`
	}
	if err := json.Unmarshal(statsRaw, &stats); err != nil {
		t.Fatalf("Unmarshal stats: %v", err)
	}
	if stats.Chapters != 6 || len(stats.Patterns) == 0 {
		t.Errorf("stats content: %+v", stats)
	}
	if usage, ok := payload.Episodic["_usage"]; !ok || len(usage) == 0 {
		t.Error("expected episodic_memory._usage annotation")
	}
}

func TestContextToolWarnsWhenOptionalDataIsCorrupt(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "simulation_profile.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := newTestContextTool(st, References{}, "default").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	warnings, _ := got["_warnings"].([]any)
	if len(warnings) == 0 || !strings.Contains(warnings[0].(string), "simulation_profile") {
		t.Fatalf("tư liệu tùy chọn bị hỏng phải cảnh báo rõ: %+v", got["_warnings"])
	}
}

func keysOf(m map[string]json.RawMessage) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestContextToolRejectsCorruptCoreState(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("write progress.json: %v", err)
	}

	tool := newTestContextTool(store, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	_, err = tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "progress") {
		t.Fatalf("dữ kiện cốt lõi bị hỏng phải dừng lắp ráp ngữ cảnh: %v", err)
	}
}

func TestContextToolChapterModeIncludesWorkingAndReferenceFields(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SavePremise(`## Thể loại và tông điệu
thiếu niên trưởng thành, thiên về căng thẳng và áp lực.

## Định vị thể loại
dòng thiếu niên nâng cấp

## Xung đột cốt lõi
nhân vật chính phải sống sót trong cạnh tranh tông môn.

## Mục tiêu nhân vật chính
vào nội môn.

## Hướng kết cục
trở thành người thực sự cầm cờ.

## Vùng cấm viết
không tiết lộ sớm chân tướng về sư tôn.

## Điểm bán khác biệt
kẻ yếu lật ngược thế cờ.

## Móc câu khác biệt
mỗi giai đoạn đều phải đổi trưởng thành bằng cái giá cao hơn.

## Cam kết thực hiện cốt lõi
liên tục thực hiện cam kết về khủng hoảng và đột phá.

## Động cơ câu chuyện
thử luyện, tranh đoạt tài nguyên và nâng cấp thân phận cùng thúc đẩy câu chuyện.

## Bước ngoặt giữa chặng
nhân vật chính buộc phải chuyển sang một con đường tu luyện khác.
`); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Nhập môn", CoreEvent: "nhân vật chính vào tông môn", Scenes: []string{"bái sư", "lập thệ"}},
		{Chapter: 2, Title: "Thử luyện", CoreEvent: "tham gia thử luyện ngoại môn", Scenes: []string{"tập hợp", "xuất phát"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "Lâm Nghiễn", Role: "nhân vật chính", Description: "tu sĩ thiếu niên", Arc: "trưởng thành", Traits: []string{"điềm tĩnh"}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "magic", Rule: "linh khí có thể luyện hóa", Boundary: "người phàm không thể trực tiếp điều khiển"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := s.Progress.Init(2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "nhân vật chính bái nhập tông môn và xác lập mục tiêu.",
		Characters: []string{"Lâm Nghiễn"},
		KeyEvents:  []string{"bái sư"},
	}); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(1, "Cuối nội dung chương một để lại hồi hộp về thử luyện."); err != nil {
		t.Fatalf("SaveFinalChapter: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "Thử luyện",
		Goal:    "vượt qua cửa thứ nhất",
		Contract: domain.ChapterContract{
			RequiredBeats:    []string{"phải để nhân vật chính vượt qua cửa thứ nhất", "phải cài lời mời thử luyện nội môn"},
			ForbiddenMoves:   []string{"không được tiết lộ sớm thân phận thật của sư tôn"},
			ContinuityChecks: []string{"vết thương cũ ở tay trái nhân vật chính vẫn chưa lành"},
			EvaluationFocus:  []string{"kiểm tra trọng tâm xem nhịp thử luyện có lê thê không"},
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"lối kể giữ tiết chế"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	tool := newTestContextTool(s, References{
		Consistency:      "kiểm tra tính nhất quán",
		HookTechniques:   "kỹ thuật móc câu",
		QualityChecklist: "danh sách chất lượng",
	}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{
		"premise",
		"premise_sections",
		"premise_structure",
		"world_rules",
		"memory_policy",
		"working_memory",
		"episodic_memory",
		"reference_pack",
	} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected key %q in chapter context", key)
		}
	}
	if _, ok := payload["outline"]; ok {
		t.Fatal("chapter context must not include the whole outline")
	}
	working := payload["working_memory"].(map[string]any)
	for _, key := range []string{"current_chapter_outline", "recent_summaries", "chapter_plan", "chapter_contract", "previous_tail"} {
		if _, ok := working[key]; !ok {
			t.Fatalf("expected working_memory.%s", key)
		}
	}
	episodic := payload["episodic_memory"].(map[string]any)
	if _, ok := episodic["planning_tier"]; !ok {
		t.Fatal("expected episodic_memory.planning_tier")
	}
	referencePack := payload["reference_pack"].(map[string]any)
	for _, key := range []string{"style_rules", "references"} {
		if _, ok := referencePack[key]; !ok {
			t.Fatalf("expected reference_pack.%s", key)
		}
	}
	for _, key := range []string{"planning_tier", "current_chapter_outline", "recent_summaries", "chapter_plan", "chapter_contract", "previous_tail", "style_rules", "references"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("unexpected top-level memory field %q", key)
		}
	}
}

func TestContextToolArchitectModeIncludesPlanningAndFoundation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(6); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}
	if err := s.Progress.UpdateVolumeArc(1, 1); err != nil {
		t.Fatalf("UpdateVolumeArc: %v", err)
	}
	if err := s.Outline.SavePremise(`## Thể loại và tông điệu
Phiêu lưu quần thể, sử thi lạnh lùng.

## Định vị thể loại
Tiểu thuyết phiêu lưu dài về nhiều nhân vật

## Xung đột cốt lõi
Mọi người phải tìm kiếm trật tự mới trong trật tự cũ không ngừng mất kiểm soát.

## Mục tiêu nhân vật chính
Chạm đến cốt lõi sự thật.

## Hướng kết cục
Vén mở sự thật cổ xưa và tái thiết trật tự.

## Vùng cấm viết
Không kết thúc bằng thiết lập từ trên trời rơi xuống.

## Điểm bán khác biệt
Thúc đẩy quan hệ giữa các nhân vật.

## Móc câu khác biệt
Mỗi tập đều thay đổi cấu trúc quan hệ của đội.

## Cam kết thực hiện cốt lõi
Liên tục mang đến khám phá, hy sinh và lựa chọn.

## Động cơ câu chuyện
Hành trình, điều tra sự thật và quan hệ đội ngũ cùng thúc đẩy câu chuyện.

## Tuyến quan hệ/trưởng thành
Đội ngũ từ không tin tưởng nhau đến tan rã rồi tái hợp.

## Lộ trình nâng cấp
Từ sự kiện địa phương đến khủng hoảng cấp thế giới.

## Bước ngoặt giữa chặng
Sự thật không phải kẻ thù, mà chính trật tự có vấn đề.

## Luận đề kết cục
Ai nên định nghĩa trật tự.
`); err != nil {
		t.Fatalf("SavePremise: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Điểm khởi đầu", CoreEvent: "Hành trình bắt đầu"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "Thẩm Diệu", Role: "nhân vật chính", Description: "Kiếm khách lang bạt", Arc: "Tìm kiếm sự thật", Traits: []string{"Sắc bén"}},
	}); err != nil {
		t.Fatalf("SaveCharacters: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "society", Rule: "Các thành bang san sát", Boundary: "Hoàng quyền không thể trực tiếp cai trị vùng biên"},
	}); err != nil {
		t.Fatalf("SaveWorldRules: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{
		{
			Index: 1, Title: "Tập một", Theme: "Bước lên hành trình",
			Arcs: []domain.ArcOutline{
				{Index: 1, Title: "Khởi hành", Goal: "Xây dựng đội ngũ", Chapters: []domain.OutlineEntry{{Chapter: 1, Title: "Điểm khởi đầu"}}},
				{Index: 2, Title: "Sương mù", Goal: "Tiến gần bí mật", EstimatedChapters: 5},
			},
		},
	}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{
		Volume: 1, Arc: 1, Title: "Khởi hành", Summary: "Đội ngũ rời khỏi thành phố.", KeyEvents: []string{"Gặp người dẫn đường", "Nhận nhiệm vụ đầu tiên"},
	}); err != nil {
		t.Fatalf("SaveArcSummary: %v", err)
	}
	if err := s.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "Nhân vật chính đối mặt với lựa chọn cuối cùng",
		EstimatedScale:  "3 tập",
	}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"Lối kể giữ tiết chế"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("SetPlanningTier: %v", err)
	}

	tool := newTestContextTool(s, References{
		OutlineTemplate:   "Mẫu dàn ý tiếng Việt",
		CharacterTemplate: "Mẫu nhân vật",
		LongformPlanning:  "Mẫu lập kế hoạch dài",
	}, "default")
	args, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, key := range []string{"memory_policy", "planning_memory", "foundation_memory", "reference_pack"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("expected key %q in architect context", key)
		}
	}
	planning := payload["planning_memory"].(map[string]any)
	for _, key := range []string{"planning_tier", "layered_outline", "skeleton_arcs", "arc_summaries", "compass"} {
		if _, ok := planning[key]; !ok {
			t.Fatalf("expected planning_memory.%s", key)
		}
	}
	foundation := payload["foundation_memory"].(map[string]any)
	for _, key := range []string{"premise", "premise_sections", "premise_structure", "characters", "foundation_status"} {
		if _, ok := foundation[key]; !ok {
			t.Fatalf("expected foundation_memory.%s", key)
		}
	}
	referencePack := payload["reference_pack"].(map[string]any)
	for _, key := range []string{"style_rules", "references"} {
		if _, ok := referencePack[key]; !ok {
			t.Fatalf("expected reference_pack.%s", key)
		}
	}
	for _, key := range []string{"planning_tier", "layered_outline", "skeleton_arcs", "arc_summaries", "compass", "premise", "premise_sections", "premise_structure", "characters", "foundation_status", "style_rules", "references"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("unexpected top-level memory field %q", key)
		}
	}
}

func TestContextToolArchitectModeIncludesFlatOutline(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Khởi đầu"}}); err != nil {
		t.Fatal(err)
	}

	raw, err := newTestContextTool(s, References{}, "default").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	planning := payload["planning_memory"].(map[string]any)
	if _, ok := planning["outline"]; !ok {
		t.Fatal("expected planning_memory.outline")
	}
	if _, ok := payload["outline"]; ok {
		t.Fatal("unexpected top-level outline")
	}
}

func TestTrimByBudgetRemovesCanonicalMemoryKeys(t *testing.T) {
	result := map[string]any{
		"reference_pack": map[string]any{
			"references": map[string]string{
				"a": strings.Repeat("x", 200),
				"b": strings.Repeat("y", 200),
			},
			"style_rules": []string{"Giữ nhịp kể chậm"},
		},
	}

	if err := trimByBudget(result, 80); err != nil {
		t.Fatal(err)
	}

	pack, ok := result["reference_pack"].(map[string]any)
	if !ok {
		t.Fatal("expected reference_pack to remain available")
	}
	if _, ok := pack["references"]; ok {
		t.Fatal("expected references to be trimmed from reference_pack")
	}
}

func TestTrimByBudgetKeepsStyleStats(t *testing.T) {
	styleStats := map[string]any{
		"chapters": 200,
		"patterns": []map[string]any{
			{"name": "Câu ngắn", "total": 80, "per_chapter": 0.4},
		},
	}
	result := map[string]any{
		"reference_pack": map[string]any{
			"references": strings.Repeat("x", 500),
		},
		"episodic_memory": map[string]any{
			"style_stats": styleStats,
		},
	}

	if err := trimByBudget(result, 200); err != nil {
		t.Fatal(err)
	}

	episodic := result["episodic_memory"].(map[string]any)
	if _, ok := episodic["style_stats"]; !ok {
		t.Fatal("style_stats must remain in episodic_memory")
	}
	if trimmed, ok := result["_trimmed"].([]string); ok && slices.Contains(trimmed, "style_stats") {
		t.Fatal("style_stats must not be reported as trimmed")
	}
}

func TestTrimByBudgetRejectsUntrimmablePayload(t *testing.T) {
	result := map[string]any{"required": strings.Repeat("x", 500)}
	if err := trimByBudget(result, 100); err == nil || !strings.Contains(err.Error(), "exceeds budget") {
		t.Fatalf("expected explicit budget error, got %v", err)
	}
}

func TestFinalizeContextPayloadReportsAppliedTrimming(t *testing.T) {
	result := map[string]any{
		"reference_pack": map[string]any{
			"references": map[string]string{"guide": strings.Repeat("x", 1000)},
		},
		"episodic_memory": map[string]any{
			"style_stats": map[string]any{"chapters": 20},
		},
	}
	raw, err := finalizeContextPayload(result, 3, 400)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 400 {
		t.Fatalf("payload = %d bytes, budget = 400", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	summary, _ := payload["_loading_summary"].(string)
	if !strings.Contains(summary, "đã cắt:references") {
		t.Fatalf("loading summary must reflect final trimming, got %q", summary)
	}
}

func TestProjectLayeredOutlineCompactsOnlyCompletedArcs(t *testing.T) {
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, Chapters: []domain.OutlineEntry{{Title: "Hội ngộ"}, {Title: "Thử thách"}}},
		},
	}}

	projected := projectLayeredOutlineForPlanning(volumes, 2)
	if got := projected[0].Arcs[0]; got.Status != "completed" || len(got.Chapters) != 0 || got.StartChapter != 1 || got.EndChapter != 2 {
		t.Fatalf("completed arc projection = %+v", got)
	}
	if got := projected[0].Arcs[1]; got.Status != "expanded" || len(got.Chapters) != 2 || got.StartChapter != 3 || got.EndChapter != 4 {
		t.Fatalf("future arc projection = %+v", got)
	}
}

func TestContextToolLongLayeredPlanningStaysWithinBudget(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	volumes := make([]domain.VolumeOutline, 10)
	completed := make([]int, 0, 500)
	chapter := 0
	for vi := range volumes {
		volumes[vi] = domain.VolumeOutline{Index: vi + 1, Title: fmt.Sprintf("Tập %d", vi+1), Theme: strings.Repeat("Bối cảnh rộng mở ", 10)}
		for ai := 0; ai < 10; ai++ {
			arc := domain.ArcOutline{Index: ai + 1, Title: fmt.Sprintf("Cung %d", ai+1), Goal: strings.Repeat("Mục tiêu rõ ràng ", 20)}
			for ci := 0; ci < 5; ci++ {
				chapter++
				completed = append(completed, chapter)
				arc.Chapters = append(arc.Chapters, domain.OutlineEntry{
					Title: fmt.Sprintf("Chương %d", chapter), CoreEvent: strings.Repeat("Sự kiện quan trọng ", 30),
					Hook: strings.Repeat("Móc câu hấp dẫn ", 20), Scenes: []string{strings.Repeat("Cảnh truyện ", 20)},
				})
			}
			volumes[vi].Arcs = append(volumes[vi].Arcs, arc)
		}
	}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase: domain.PhaseWriting, Layered: true, CompletedChapters: completed,
		CurrentVolume: 10, CurrentArc: 10,
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := newTestContextTool(s, References{}, "default").Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 60*1024 {
		t.Fatalf("architect payload = %d bytes, budget = %d", len(raw), 60*1024)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	planning := payload["planning_memory"].(map[string]any)
	encoded, err := json.Marshal(planning["layered_outline"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Sự kiện quan trọng") {
		t.Fatal("completed chapter details must not remain in architect planning projection")
	}
}

func TestContextToolWriterDoesNotIncludeWholeOutline(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	outline := make([]domain.OutlineEntry, 200)
	for i := range outline {
		outline[i] = domain.OutlineEntry{
			Chapter: i + 1, Title: fmt.Sprintf("Chương %d", i+1),
			CoreEvent: strings.Repeat("Diễn biến chính ", 20), Hook: strings.Repeat("Móc câu ", 10),
		}
	}
	if err := s.Outline.SaveOutline(outline); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Save(&domain.Progress{Phase: domain.PhaseWriting, TotalChapters: 200}); err != nil {
		t.Fatal(err)
	}

	raw, err := newTestContextTool(s, References{}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":100}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["outline"]; ok {
		t.Fatal("writer payload must not include the whole outline")
	}
	working := payload["working_memory"].(map[string]any)
	if _, ok := working["current_chapter_outline"]; !ok {
		t.Fatal("writer payload must retain the current chapter outline")
	}
	window, ok := working["outline_window"].([]any)
	if !ok || len(window) != domain.ReviewInterval {
		t.Fatalf("writer outline window = %T/%d, want %d entries", working["outline_window"], len(window), domain.ReviewInterval)
	}
}

func TestContextToolSelectedMemoryRecallsStoryThreadsAndReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Lời mời", CoreEvent: "Nhận lời thử thách", Scenes: []string{"Gặp người dẫn đường", "Nhận nhiệm vụ"}},
		{Chapter: 2, Title: "Thử thách", CoreEvent: "Bước vào cuộc thi", Hook: "Kẻ đứng sau lộ diện", Scenes: []string{"Tập luyện", "Đối đầu"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init(8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "lời mời thử thách", PlantedAt: 1, Status: "planted"},
		{ID: "trial_mastermind", Description: "kẻ chủ mưu trong thử thách", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "bản khắc luật lệ trên bia đá", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "đệ tử ngoại môn", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "tín vật trưởng lão", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "cánh cổng ẩn", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "người giật dây vụ cược", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "Lời mời thử thách",
		Goal:    "Vượt qua vòng tuyển chọn",
		Contract: domain.ChapterContract{
			PayoffPoints: []string{"lời mời thử thách"},
			HookGoal:     "kẻ chủ mưu lộ diện",
		},
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter:        1,
		Scope:          "chapter",
		Verdict:        "polish",
		Summary:        "Lời mời thử thách còn để lại phục bút.",
		ContractStatus: "partial",
		ContractMisses: []string{"chưa thực hiện lời mời thử thách"},
		Issues: []domain.ConsistencyIssue{
			{Type: "hook", Severity: "warning", Description: "móc câu cuối chương chưa đủ mạnh"},
		},
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads  []domain.RecallItem `json:"story_threads"`
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
		Summary string `json:"_loading_summary"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(payload.Selected.StoryThreads) == 0 {
		t.Fatal("expected story thread recall items")
	}
	if len(payload.Selected.ReviewLessons) == 0 {
		t.Fatal("expected review lesson recall items")
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "lời mời thử thách") {
		t.Fatalf("expected story thread recall to mention invite, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "kẻ chủ mưu trong thử thách") {
		t.Fatalf("expected story thread recall to mention trial mastermind, got %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "bản khắc luật lệ trên bia đá") {
		t.Fatalf("expected weak-overlap foreshadow to stay out, got %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "đệ tử ngoại môn") {
		t.Fatalf("expected related_chapters not to be duplicated into story_threads, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "chưa thực hiện lời mời thử thách") {
		t.Fatalf("expected review lesson recall to mention contract miss, got %+v", payload.Selected.ReviewLessons)
	}
	if !strings.Contains(payload.Summary, "truy hồi tuyến truyện:") || !strings.Contains(payload.Summary, "truy hồi nhận xét:") {
		t.Fatalf("expected loading summary to report selected memory, got %q", payload.Summary)
	}
}

// Kiểm tra phục bút được truy hồi theo tuyến truyện và tuổi.
// Kiểm tra phục bút lâu ngày không liên quan trực tiếp vẫn được truy hồi theo tuổi.
func TestContextToolSelectedMemorySurfacesAgingForeshadow(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Phục bút cũ, phục bút mới và một mục đã tiến triển để kiểm tra ngưỡng tuổi.
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 50, Title: "Cuộc gặp", CoreEvent: "Lâm Nghiễn trở về", Scenes: []string{"Bước vào chợ", "Nghe tin đồn"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init(60); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	// Mục gieo cách hiện tại ít nhất 30 chương là quá hạn; mục gần đây thì không.
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "ancient_seal", Description: "con dấu cổ", PlantedAt: 3, Status: "planted"},
		{ID: "lost_bloodline", Description: "dòng máu thất lạc", PlantedAt: 5, Status: "advanced"},
		{ID: "market_feud", Description: "mối thù ở chợ", PlantedAt: 47, Status: "planted"},
		{ID: "rumor_a", Description: "lời đồn về chiếc nhẫn", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_b", Description: "lời đồn về người đưa tin", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_c", Description: "lời đồn về cánh cổng", PlantedAt: 49, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 50})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads []domain.RecallItem `json:"story_threads"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// Hai phục bút quá hạn phải xuất hiện; mục đã tiến triển vẫn được ghi chú, mục mới không bị gắn nhãn quá hạn.
	if !containsRecallSummary(payload.Selected.StoryThreads, "con dấu cổ") {
		t.Fatalf("expected aging foreshadow to surface despite no relevance, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "dòng máu thất lạc") {
		t.Fatalf("expected second aging foreshadow to surface, got %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "đã cách") {
		t.Fatalf("expected aging item to carry age annotation, got %+v", payload.Selected.StoryThreads)
	}
	foundRecent := false
	for _, item := range payload.Selected.StoryThreads {
		if strings.Contains(item.Summary, "mối thù ở chợ") {
			foundRecent = true
			if strings.Contains(item.Summary, "quá hạn") {
				t.Fatalf("recent foreshadow must not be labeled overdue, got %+v", item)
			}
		}
	}
	if foundRecent {
		t.Fatalf("recent unrelated foreshadow must not be recalled, got %+v", payload.Selected.StoryThreads)
	}
}

func TestContextToolSelectedMemoryIncludesGlobalReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Khởi đầu", CoreEvent: "Gặp người dẫn đường"},
		{Chapter: 2, Title: "Tiếp nối", CoreEvent: "Bước tiếp trên hành trình"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init(6); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 1,
		Scope:   "global",
		Verdict: "polish",
		Summary: "Mạch truyện và mục tiêu nhân vật cần củng cố.",
		Issues: []domain.ConsistencyIssue{
			{Type: "character", Severity: "warning", Description: "Mục tiêu nhân vật chính chưa rõ"},
		},
	}); err != nil {
		t.Fatalf("SaveReview(global): %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload struct {
		Selected struct {
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "Mục tiêu nhân vật chính chưa rõ") {
		t.Fatalf("expected global review lesson to be recalled, got %+v", payload.Selected.ReviewLessons)
	}
}

func TestContextToolKeepsFullForeshadowWhenRecallNotTriggered(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Khởi đầu", CoreEvent: "Gặp người lạ"},
		{Chapter: 2, Title: "Tiếp tục", CoreEvent: "Rời thành phố"},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "small_1", Description: "Một phục bút nhỏ", PlantedAt: 1, Status: "planted"},
		{ID: "small_2", Description: "Hai phục bút nhỏ", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	episodic := payload["episodic_memory"].(map[string]any)
	if _, ok := episodic["foreshadow_ledger"]; !ok {
		t.Fatal("expected full foreshadow ledger to remain when selected recall is not triggered")
	}
	if _, ok := payload["selected_memory"]; ok {
		t.Fatalf("expected no selected_memory for small foreshadow sets, got %+v", payload["selected_memory"])
	}
}

func TestContextToolFallsBackToFullForeshadowWhenSelectionIsTooSparse(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Lời mời", CoreEvent: "Nhận lời thử thách"},
		{Chapter: 2, Title: "Thử thách", CoreEvent: "Bước vào cuộc thi", Scenes: []string{"Tập luyện", "Đối đầu"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Progress.Init(8); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "lời mời thử thách", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "bản đồ kho báu cổ", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "món nợ cũ của đệ tử", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "nguồn gốc tín vật trưởng lão", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "lối đi bí mật sau núi", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "người giật dây vụ cược", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	episodic := payload["episodic_memory"].(map[string]any)
	if _, ok := episodic["foreshadow_ledger"]; !ok {
		t.Fatal("expected full foreshadow ledger when selection is too sparse")
	}
	if selected, ok := payload["selected_memory"].(map[string]any); ok {
		if _, exists := selected["story_threads"]; exists {
			t.Fatalf("expected sparse story_threads to fall back to full ledger, got %+v", selected["story_threads"])
		}
	}
}

func containsRecallSummary(items []domain.RecallItem, want string) bool {
	for _, item := range items {
		if strings.Contains(item.Summary, want) {
			return true
		}
	}
	return false
}

func TestContextToolInjectsRewriteBriefForPendingRewriteChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "Cần viết lại để làm rõ xung đột"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "rewrite",
		Summary: "Xung đột cần được làm rõ.",
		Issues: []domain.ConsistencyIssue{
			{Type: "pacing", Severity: "error", Description: "Chương quá dài", Evidence: "Đoạn giữa lặp lại"},
		},
		ContractMisses: []string{"chưa thực hiện cam kết thử thách"},
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working := payload["working_memory"].(map[string]any)
	brief, ok := working["rewrite_brief"].(map[string]any)
	if !ok {
		t.Fatalf("expected working_memory.rewrite_brief, got %T", working["rewrite_brief"])
	}
	if got := brief["reason"]; got != "Cần viết lại để làm rõ xung đột" {
		t.Fatalf("expected rewrite reason, got %v", got)
	}
	if got, _ := brief["review_summary"].(string); !strings.Contains(got, "Xung đột") {
		t.Fatalf("expected review summary from chapter review, got %v", brief["review_summary"])
	}
	if issues, _ := brief["issues"].([]any); len(issues) == 0 {
		t.Fatalf("expected review issues in rewrite_brief, got %v", brief["issues"])
	}
	if misses, _ := brief["contract_misses"].([]any); len(misses) == 0 {
		t.Fatalf("expected contract misses in rewrite_brief, got %v", brief["contract_misses"])
	}
}

func TestContextToolOmitsRewriteBriefForNormalChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	working := payload["working_memory"].(map[string]any)
	if _, ok := working["rewrite_brief"]; ok {
		t.Fatal("expected no rewrite_brief for chapter outside PendingRewrites")
	}
}

func TestContextToolLoadsArcReviewAffectingEarlierChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 4; chapter++ {
		if err := s.Progress.MarkChapterComplete(chapter, 100, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Progress.SetPendingRewrites([]int{3}, "Cần củng cố cung hai"); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 4, Scope: "arc", Verdict: "polish", Summary: "Cung hai cần củng cố", AffectedChapters: []int{3},
		Issues: []domain.ConsistencyIssue{{
			Type: "pacing", Severity: "error", Description: "Nhịp chương ba chưa đều", Evidence: "Xung đột bị kéo dài",
			Chapters: []int{3}, RequiresChange: true,
		}},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := newTestContextTool(s, References{}, "default").Execute(context.Background(), json.RawMessage(`{"chapter":3}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	working := payload["working_memory"].(map[string]any)
	brief, _ := working["rewrite_brief"].(map[string]any)
	if brief == nil || !strings.Contains(fmt.Sprint(brief["review_summary"]), "Cung hai") {
		t.Fatalf("expected arc review handoff for chapter 3, got %#v", brief)
	}
}

func TestContextToolDoesNotInjectUserDirectives(t *testing.T) {
	// save_directive không được đưa vào working_memory.user_directives; chỉ giữ user_rules.
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	for name, chapter := range map[string]int{"writer": 1, "architect": 0} {
		args, _ := json.Marshal(map[string]any{"chapter": chapter})
		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("[%s] Execute: %v", name, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(result, &payload); err != nil {
			t.Fatalf("[%s] Unmarshal: %v", name, err)
		}
		working, ok := payload["working_memory"].(map[string]any)
		if !ok {
			t.Fatalf("[%s] missing working_memory", name)
		}
		if _, exists := working["user_directives"]; exists {
			t.Errorf("[%s] working_memory không được có user_directives", name)
		}
		// user_rules phải luôn là bản đồ cấu trúc ổn định.
		if _, ok := working["user_rules"].(map[string]any); !ok {
			t.Errorf("[%s] working_memory.user_rules phải là bản đồ", name)
		}
	}
}

// TestContextToolInjectsRuleViolations xác nhận novel_context đưa vi phạm cơ học vào ngữ cảnh editor.
func TestContextToolInjectsRuleViolations(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 3, Phase: domain.PhaseWriting}); err != nil {
		t.Fatalf("progress: %v", err)
	}
	if err := st.World.SaveRuleViolations(2, []rules.Violation{
		{Rule: "fatigue_words", Target: "từ ngữ mệt mỏi", Actual: 9, Severity: rules.SeverityWarning},
	}); err != nil {
		t.Fatalf("save violations: %v", err)
	}

	tool := newTestContextTool(st, References{}, "default")
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	vs, ok := result["rule_violations"].([]any)
	if !ok || len(vs) != 1 {
		t.Fatalf("rule_violations phải được đưa vào ngữ cảnh, nhận %v", result["rule_violations"])
	}

	// Chương không có vi phạm thì không được tạo trường rule_violations.
	args3, _ := json.Marshal(map[string]any{"chapter": 3})
	raw3, err := tool.Execute(context.Background(), args3)
	if err != nil {
		t.Fatalf("Execute ch3: %v", err)
	}
	var result3 map[string]any
	_ = json.Unmarshal(raw3, &result3)
	if _, has := result3["rule_violations"]; has {
		t.Fatal("chương không có vi phạm không được có rule_violations")
	}
}
