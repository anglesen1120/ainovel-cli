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
		t.Fatalf("Khởi tạo kho: %v", err)
	}

	progress := &domain.Progress{TotalChapters: 10}
	body := "# Chương N\nkhông phải anh không dám, mà là anh sợ hãi. Anh im lặng vài nhịp. Như một luồng sáng.\nMàn đêm buông xuống.\nAnh rời đi."
	for ch := 1; ch <= 6; ch++ {
		if err := st.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("Lưu bản cuối chương: %v", err)
		}
		progress.CompletedChapters = append(progress.CompletedChapters, ch)
	}
	if err := st.Progress.Save(progress); err != nil {
		t.Fatalf("Lưu tiến độ: %v", err)
	}

	tool := newTestContextTool(st, References{}, "default")
	args, _ := json.Marshal(map[string]any{"chapter": 7})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload struct {
		Episodic map[string]json.RawMessage `json:"episodic_memory"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	statsRaw, ok := payload.Episodic["style_stats"]
	if !ok {
		t.Fatalf("Thiếu episodic_memory.style_stats; các khóa hiện có: %v", keysOf(payload.Episodic))
	}
	var stats struct {
		Chapters int `json:"chapters"`
		Patterns []struct {
			Name  string `json:"name"`
			Total int    `json:"total"`
		} `json:"patterns"`
	}
	if err := json.Unmarshal(statsRaw, &stats); err != nil {
		t.Fatalf("Giải mã thống kê phong cách: %v", err)
	}
	if stats.Chapters != 6 || len(stats.Patterns) == 0 {
		t.Errorf("Thống kê phong cách không đầy đủ: %+v", stats)
	}
	if usage, ok := payload.Episodic["_usage"]; !ok || len(usage) == 0 {
		t.Error("Thiếu chú thích episodic_memory._usage")
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
		t.Fatalf("Khởi tạo kho: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("Ghi progress.json: %v", err)
	}

	tool := newTestContextTool(store, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
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
		t.Fatalf("Khởi tạo kho: %v", err)
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
		t.Fatalf("Lưu tiền đề: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Nhập môn", CoreEvent: "nhân vật chính vào tông môn", Scenes: []string{"bái sư", "lập thệ"}},
		{Chapter: 2, Title: "Thử luyện", CoreEvent: "tham gia thử luyện ngoại môn", Scenes: []string{"tập hợp", "xuất phát"}},
	}); err != nil {
		t.Fatalf("Lưu đại cương: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "Lâm Nghiễn", Role: "nhân vật chính", Description: "tu sĩ thiếu niên", Arc: "trưởng thành", Traits: []string{"điềm tĩnh"}},
	}); err != nil {
		t.Fatalf("Lưu nhân vật: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "magic", Rule: "linh khí có thể luyện hóa", Boundary: "người phàm không thể trực tiếp điều khiển"},
	}); err != nil {
		t.Fatalf("Lưu quy tắc thế giới: %v", err)
	}
	if err := s.Progress.Init(2); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "nhân vật chính bái nhập tông môn và xác lập mục tiêu.",
		Characters: []string{"Lâm Nghiễn"},
		KeyEvents:  []string{"bái sư"},
	}); err != nil {
		t.Fatalf("Lưu tóm tắt chương: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(1, "Cuối nội dung chương một để lại hồi hộp về thử luyện."); err != nil {
		t.Fatalf("Lưu bản cuối chương: %v", err)
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
		t.Fatalf("Lưu kế hoạch chương: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"lối kể giữ tiết chế"},
	}); err != nil {
		t.Fatalf("Lưu quy tắc phong cách: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("Đặt tầng lập kế hoạch: %v", err)
	}

	tool := newTestContextTool(s, References{
		Consistency:      "kiểm tra tính nhất quán",
		HookTechniques:   "kỹ thuật móc câu",
		QualityChecklist: "danh sách kiểm tra chất lượng",
	}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
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
			t.Fatalf("Thiếu khóa %q trong ngữ cảnh chương", key)
		}
	}
	if _, ok := payload["outline"]; ok {
		t.Fatal("Ngữ cảnh chương không được chứa toàn bộ đại cương")
	}
	working := payload["working_memory"].(map[string]any)
	for _, key := range []string{"current_chapter_outline", "recent_summaries", "chapter_plan", "chapter_contract", "previous_tail"} {
		if _, ok := working[key]; !ok {
			t.Fatalf("Thiếu working_memory.%s", key)
		}
	}
	episodic := payload["episodic_memory"].(map[string]any)
	if _, ok := episodic["planning_tier"]; !ok {
		t.Fatal("Thiếu episodic_memory.planning_tier")
	}
	referencePack := payload["reference_pack"].(map[string]any)
	for _, key := range []string{"style_rules", "references"} {
		if _, ok := referencePack[key]; !ok {
			t.Fatalf("Thiếu reference_pack.%s", key)
		}
	}
	for _, key := range []string{"planning_tier", "current_chapter_outline", "recent_summaries", "chapter_plan", "chapter_contract", "previous_tail", "style_rules", "references"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("Trường bộ nhớ cấp cao không mong đợi %q", key)
		}
	}
}

func TestContextToolArchitectModeIncludesPlanningAndFoundation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Progress.Init(6); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatalf("Bật đại cương phân tầng: %v", err)
	}
	if err := s.Progress.UpdateVolumeArc(1, 1); err != nil {
		t.Fatalf("Cập nhật quyển và cung: %v", err)
	}
	if err := s.Outline.SavePremise(`## Thể loại và tông điệu
phiêu lưu quần tượng, thiên về sử thi lạnh lùng.

## Định vị thể loại
phiêu lưu dài kỳ quần tượng

## Xung đột cốt lõi
mọi người phải tìm trật tự mới trong trật tự cũ liên tục mất kiểm soát.

## Mục tiêu nhân vật chính
đến được lõi sự thật.

## Hướng kết cục
vén màn sự thật cổ xưa và tái thiết trật tự.

## Vùng cấm viết
không kết thúc bằng thiết lập từ trên trời rơi xuống.

## Điểm bán khác biệt
đẩy tiến quan hệ quần tượng.

## Móc câu khác biệt
mỗi tập đều thay đổi cấu trúc quan hệ của đội.

## Cam kết thực hiện cốt lõi
liên tục cung cấp khám phá, hy sinh và lựa chọn.

## Động cơ câu chuyện
hành trình, điều tra sự thật và quan hệ đội cùng thúc đẩy.

## Tuyến quan hệ/trưởng thành
đội từ chỗ không tin nhau đi đến chia rẽ rồi tái hợp.

## Lộ trình nâng cấp
từ sự kiện địa phương leo lên khủng hoảng cấp thế giới.

## Bước ngoặt giữa chặng
sự thật không phải kẻ thù; chính trật tự mới có vấn đề.

## Luận đề kết cục
ai nên định nghĩa trật tự.
`); err != nil {
		t.Fatalf("Lưu tiền đề: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Khởi điểm", CoreEvent: "hành trình bắt đầu"},
	}); err != nil {
		t.Fatalf("Lưu đại cương: %v", err)
	}
	if err := s.Characters.Save([]domain.Character{
		{Name: "Thẩm Diệu", Role: "nhân vật chính", Description: "kiếm khách lang bạt", Arc: "tìm kiếm sự thật", Traits: []string{"nhạy bén"}},
	}); err != nil {
		t.Fatalf("Lưu nhân vật: %v", err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{
		{Category: "society", Rule: "thành bang mọc lên dày đặc", Boundary: "hoàng quyền không thể trực tiếp cai quản biên địa"},
	}); err != nil {
		t.Fatalf("Lưu quy tắc thế giới: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{
		{
			Index: 1, Title: "Tập một", Theme: "lên đường",
			Arcs: []domain.ArcOutline{
				{Index: 1, Title: "Khởi hành", Goal: "lập đội", Chapters: []domain.OutlineEntry{{Chapter: 1, Title: "Khởi điểm"}}},
				{Index: 2, Title: "Màn sương", Goal: "tiến gần bí mật", EstimatedChapters: 5},
			},
		},
	}); err != nil {
		t.Fatalf("Lưu đại cương phân tầng: %v", err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{
		Volume: 1, Arc: 1, Title: "Khởi hành", Summary: "đội được lập, nhưng rạn nứt xuất hiện vì bất đồng về sự thật.", KeyEvents: []string{"đội được lập", "bất đồng nổi lên"},
	}); err != nil {
		t.Fatalf("Lưu tóm tắt cung: %v", err)
	}
	if err := s.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "vén màn sự thật cổ xưa",
		EstimatedScale:  "dự kiến 3 tập",
	}); err != nil {
		t.Fatalf("Lưu định hướng truyện: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Volume: 1,
		Arc:    1,
		Prose:  []string{"giữ chất lạnh lùng tiết chế"},
	}); err != nil {
		t.Fatalf("Lưu quy tắc phong cách: %v", err)
	}
	if err := s.RunMeta.SetPlanningTier(domain.PlanningTierLong); err != nil {
		t.Fatalf("Đặt tầng lập kế hoạch: %v", err)
	}

	tool := newTestContextTool(s, References{
		OutlineTemplate:   "mẫu đại cương",
		CharacterTemplate: "mẫu nhân vật",
		LongformPlanning:  "lập kế hoạch truyện dài",
	}, "default")
	args, err := json.Marshal(map[string]any{})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}

	for _, key := range []string{"memory_policy", "planning_memory", "foundation_memory", "reference_pack"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("Thiếu khóa %q trong ngữ cảnh Architect", key)
		}
	}
	planning := payload["planning_memory"].(map[string]any)
	for _, key := range []string{"planning_tier", "layered_outline", "skeleton_arcs", "arc_summaries", "compass"} {
		if _, ok := planning[key]; !ok {
			t.Fatalf("Thiếu planning_memory.%s", key)
		}
	}
	foundation := payload["foundation_memory"].(map[string]any)
	for _, key := range []string{"premise", "premise_sections", "premise_structure", "characters", "foundation_status"} {
		if _, ok := foundation[key]; !ok {
			t.Fatalf("Thiếu foundation_memory.%s", key)
		}
	}
	referencePack := payload["reference_pack"].(map[string]any)
	for _, key := range []string{"style_rules", "references"} {
		if _, ok := referencePack[key]; !ok {
			t.Fatalf("Thiếu reference_pack.%s", key)
		}
	}
	for _, key := range []string{"planning_tier", "layered_outline", "skeleton_arcs", "arc_summaries", "compass", "premise", "premise_sections", "premise_structure", "characters", "foundation_status", "style_rules", "references"} {
		if _, ok := payload[key]; ok {
			t.Fatalf("Trường bộ nhớ cấp cao không mong đợi %q", key)
		}
	}
}

func TestContextToolArchitectModeIncludesFlatOutline(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Mở đầu"}}); err != nil {
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
		t.Fatal("Thiếu planning_memory.outline")
	}
	if _, ok := payload["outline"]; ok {
		t.Fatal("Không mong đợi outline ở cấp cao")
	}
}

func TestTrimByBudgetRemovesCanonicalMemoryKeys(t *testing.T) {
	result := map[string]any{
		"reference_pack": map[string]any{
			"references": map[string]string{
				"a": strings.Repeat("x", 200),
				"b": strings.Repeat("y", 200),
			},
			"style_rules": []string{"tiết chế"},
		},
	}

	if err := trimByBudget(result, 80); err != nil {
		t.Fatal(err)
	}

	pack, ok := result["reference_pack"].(map[string]any)
	if !ok {
		t.Fatal("reference_pack phải còn sau khi cắt")
	}
	if _, ok := pack["references"]; ok {
		t.Fatal("references phải bị cắt khỏi reference_pack")
	}
}

func TestTrimByBudgetKeepsStyleStats(t *testing.T) {
	styleStats := map[string]any{
		"chapters": 200,
		"patterns": []map[string]any{
			{"name": "câu chỉnh sửa", "total": 80, "per_chapter": 0.4},
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
		t.Fatal("style_stats phải còn trong episodic_memory")
	}
	if trimmed, ok := result["_trimmed"].([]string); ok && slices.Contains(trimmed, "style_stats") {
		t.Fatal("style_stats không được ghi nhận là đã cắt")
	}
}

func TestTrimByBudgetRejectsUntrimmablePayload(t *testing.T) {
	result := map[string]any{"required": strings.Repeat("x", 500)}
	if err := trimByBudget(result, 100); err == nil || !strings.Contains(err.Error(), "exceeds budget") {
		t.Fatalf("Phải trả lỗi ngân sách rõ ràng, nhận được %v", err)
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
		t.Fatalf("Payload có %d byte, ngân sách là 400", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	summary, _ := payload["_loading_summary"].(string)
	if !strings.Contains(summary, "đã cắt:references") {
		t.Fatalf("Tóm tắt tải phải phản ánh lần cắt cuối, nhận được %q", summary)
	}
}

func TestProjectLayeredOutlineCompactsOnlyCompletedArcs(t *testing.T) {
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, Chapters: []domain.OutlineEntry{{Title: "Ba"}, {Title: "Bốn"}}},
		},
	}}

	projected := projectLayeredOutlineForPlanning(volumes, 2)
	if got := projected[0].Arcs[0]; got.Status != "completed" || len(got.Chapters) != 0 || got.StartChapter != 1 || got.EndChapter != 2 {
		t.Fatalf("Kết quả chiếu cung đã hoàn thành = %+v", got)
	}
	if got := projected[0].Arcs[1]; got.Status != "expanded" || len(got.Chapters) != 2 || got.StartChapter != 3 || got.EndChapter != 4 {
		t.Fatalf("Kết quả chiếu cung sắp tới = %+v", got)
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
		volumes[vi] = domain.VolumeOutline{Index: vi + 1, Title: fmt.Sprintf("tập%d", vi+1), Theme: strings.Repeat("chủ đề", 10)}
		for ai := 0; ai < 10; ai++ {
			arc := domain.ArcOutline{Index: ai + 1, Title: fmt.Sprintf("cung%d", ai+1), Goal: strings.Repeat("mục tiêu", 20)}
			for ci := 0; ci < 5; ci++ {
				chapter++
				completed = append(completed, chapter)
				arc.Chapters = append(arc.Chapters, domain.OutlineEntry{
					Title: fmt.Sprintf("Chương %d", chapter), CoreEvent: strings.Repeat("sự kiện then chốt", 30),
					Hook: strings.Repeat("hồi hộp", 20), Scenes: []string{strings.Repeat("cảnh", 20)},
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
		t.Fatalf("Payload Architect có %d byte, ngân sách là %d", len(raw), 60*1024)
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
	if strings.Contains(string(encoded), "sự kiện then chốt") {
		t.Fatal("Chi tiết chương đã hoàn thành không được còn trong kết quả chiếu lập kế hoạch Architect")
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
			CoreEvent: strings.Repeat("sự kiện", 20), Hook: strings.Repeat("hồi hộp", 10),
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
		t.Fatal("Payload Writer không được chứa toàn bộ đại cương")
	}
	working := payload["working_memory"].(map[string]any)
	if _, ok := working["current_chapter_outline"]; !ok {
		t.Fatal("Payload Writer phải giữ đại cương chương hiện tại")
	}
	window, ok := working["outline_window"].([]any)
	if !ok || len(window) != domain.ReviewInterval {
		t.Fatalf("Cửa sổ đại cương Writer = %T/%d, muốn %d mục", working["outline_window"], len(window), domain.ReviewInterval)
	}
}

func TestContextToolSelectedMemoryRecallsStoryThreadsAndReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Lời mời", CoreEvent: "trưởng lão bí mật đưa lời mời thử luyện nội môn", Scenes: []string{"mật đàm", "để lại lệnh bài thử luyện"}},
		{Chapter: 2, Title: "Đêm trước thử luyện", CoreEvent: "Lâm Nghiễn chuẩn bị đáp lại lời mời thử luyện nội môn", Hook: "ai đứng sau thúc đẩy cuộc thử luyện này", Scenes: []string{"sắp xếp manh mối", "quyết định đến hẹn"}},
	}); err != nil {
		t.Fatalf("Lưu đại cương: %v", err)
	}
	if err := s.Progress.Init(8); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "mục đích thật của lời mời thử luyện nội môn", PlantedAt: 1, Status: "planted"},
		{ID: "trial_mastermind", Description: "ai đứng sau thúc đẩy cuộc thử luyện này", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "mảnh bia chứa quy tắc cổ", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "tranh chấp nợ cũ của đệ tử ngoại môn", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "lai lịch lệnh bài trong tay trưởng lão", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "lối đi ẩn sau sơn môn", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "kẻ thao túng sau kèo cược thử luyện", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("Lưu sổ phục bút: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter: 2,
		Title:   "Đêm trước thử luyện",
		Goal:    "quyết định có đáp lại lời mời hay không",
		Contract: domain.ChapterContract{
			PayoffPoints: []string{"đáp lại lời mời thử luyện nội môn"},
			HookGoal:     "gợi ra ai đứng sau thúc đẩy cuộc thử luyện",
		},
	}); err != nil {
		t.Fatalf("Lưu kế hoạch chương: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter:        1,
		Scope:          "chapter",
		Verdict:        "polish",
		Summary:        "tuyến chính đã khởi động xong, nhưng phục bút chưa đủ rõ.",
		ContractStatus: "partial",
		ContractMisses: []string{"chưa cài rõ lời mời thử luyện nội môn"},
		Issues: []domain.ConsistencyIssue{
			{Type: "hook", Severity: "warning", Description: "móc câu cuối chương chưa đủ cụ thể"},
		},
	}); err != nil {
		t.Fatalf("Lưu nhận xét: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}

	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads  []domain.RecallItem `json:"story_threads"`
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
		Summary string `json:"_loading_summary"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	if len(payload.Selected.StoryThreads) == 0 {
		t.Fatal("Phải truy hồi ít nhất một tuyến truyện")
	}
	if len(payload.Selected.ReviewLessons) == 0 {
		t.Fatal("Phải truy hồi ít nhất một bài học nhận xét")
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "lời mời thử") {
		t.Fatalf("Tuyến truyện được truy hồi phải nhắc đến lời mời, nhận được %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "thúc đẩy cuộc thử") {
		t.Fatalf("Tuyến truyện được truy hồi phải nhắc đến kẻ chủ mưu thử luyện, nhận được %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "mảnh bia chứa quy tắc cổ") {
		t.Fatalf("Phục bút có độ chồng lấp yếu phải bị loại, nhận được %+v", payload.Selected.StoryThreads)
	}
	if containsRecallSummary(payload.Selected.StoryThreads, "nên xem lại chương") {
		t.Fatalf("related_chapters không được lặp lại trong story_threads, nhận được %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "thiếu mục contract") {
		t.Fatalf("Bài học nhận xét phải nhắc đến mục cam kết bị thiếu, nhận được %+v", payload.Selected.ReviewLessons)
	}
	if !strings.Contains(payload.Summary, "truy hồi tuyến truyện:") || !strings.Contains(payload.Summary, "truy hồi nhận xét:") {
		t.Fatalf("Tóm tắt tải phải báo bộ nhớ đã chọn, nhận được %q", payload.Summary)
	}
}

// Phục bút treo lâu chưa thu hồi vẫn phải được bù vào story_threads theo tuổi treo,
// dù không liên quan từ khóa chương hiện tại. Đây chính là điểm mù của truy hồi liên quan:
// tuyến treo đơn độc quá lâu nhưng không khớp từ khóa trong chương này.
// Phục bút mới cài gần đây (tuổi treo < ngưỡng) không được đánh dấu nhầm là "chưa thu hồi".
func TestContextToolSelectedMemorySurfacesAgingForeshadow(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	// Chủ đề chương hiện tại không dính đến phục bút nào, để truy hồi liên quan rỗng và chỉ phần bù theo tuổi treo có hiệu lực.
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 50, Title: "Dịch bệnh", CoreEvent: "Lâm Nghiễn chữa trị bệnh nhân dịch ở y quán phía nam thành", Scenes: []string{"sắc thuốc", "phong tỏa phố hẻm"}},
	}); err != nil {
		t.Fatalf("Lưu đại cương: %v", err)
	}
	if err := s.Progress.Init(60); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	// 6 mục đạt ngưỡng truy hồi; hai mục đầu có tuổi treo >=30 (treo lâu), bốn mục sau có tuổi treo <30 (gần đây).
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "ancient_seal", Description: "khe nứt của phong ấn thượng cổ", PlantedAt: 3, Status: "planted"},
		{ID: "lost_bloodline", Description: "lai lịch huyết mạch thất lạc của nhân vật chính", PlantedAt: 5, Status: "advanced"},
		{ID: "market_feud", Description: "cuộc cãi vã ở chợ tối qua", PlantedAt: 47, Status: "planted"},
		{ID: "rumor_a", Description: "tin đồn A gần đây", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_b", Description: "tin đồn B gần đây", PlantedAt: 48, Status: "planted"},
		{ID: "rumor_c", Description: "tin đồn C gần đây", PlantedAt: 49, Status: "planted"},
	}); err != nil {
		t.Fatalf("Lưu sổ phục bút: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 50})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload struct {
		Selected struct {
			StoryThreads []domain.RecallItem `json:"story_threads"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}

	// Hai phục bút treo lâu phải được bù vào, kèm ghi chú tuổi treo "chưa thu hồi".
	if !containsRecallSummary(payload.Selected.StoryThreads, "phong ấn thượng cổ") {
		t.Fatalf("Phục bút treo lâu phải được nêu dù không liên quan, nhận được %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "huyết mạch thất lạc") {
		t.Fatalf("Phục bút treo lâu thứ hai phải được nêu, nhận được %+v", payload.Selected.StoryThreads)
	}
	if !containsRecallSummary(payload.Selected.StoryThreads, "chưa thu hồi") {
		t.Fatalf("Mục treo lâu phải có ghi chú quá hạn, nhận được %+v", payload.Selected.StoryThreads)
	}
	// Phục bút gần đây (tuổi treo <30 và không liên quan) không được bù vào.
	if containsRecallSummary(payload.Selected.StoryThreads, "cuộc cãi vã ở chợ tối qua") {
		t.Fatalf("Phục bút mới không được gắn nhãn quá hạn, nhận được %+v", payload.Selected.StoryThreads)
	}
}

func TestContextToolSelectedMemoryIncludesGlobalReviewLessons(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Mở đầu", CoreEvent: "câu chuyện bắt đầu"},
		{Chapter: 2, Title: "Đẩy tiến", CoreEvent: "tiếp tục đẩy tiến tuyến chính"},
	}); err != nil {
		t.Fatalf("Lưu đại cương: %v", err)
	}
	if err := s.Progress.Init(6); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 1,
		Scope:   "global",
		Verdict: "polish",
		Summary: "đẩy tiến toàn cục đạt yêu cầu, nhưng cách diễn đạt mục tiêu nhân vật còn chưa đủ ổn định.",
		Issues: []domain.ConsistencyIssue{
			{Type: "character", Severity: "warning", Description: "cách diễn đạt mục tiêu nhân vật chính chưa đủ ổn định"},
		},
	}); err != nil {
		t.Fatalf("Lưu nhận xét toàn cục: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload struct {
		Selected struct {
			ReviewLessons []domain.RecallItem `json:"review_lessons"`
		} `json:"selected_memory"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	if !containsRecallSummary(payload.Selected.ReviewLessons, "mục tiêu nhân vật") {
		t.Fatalf("Bài học nhận xét toàn cục phải được truy hồi, nhận được %+v", payload.Selected.ReviewLessons)
	}
}

func TestContextToolKeepsFullForeshadowWhenRecallNotTriggered(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Khởi thế", CoreEvent: "câu chuyện khởi thế"},
		{Chapter: 2, Title: "Đẩy tiến", CoreEvent: "tiếp tục đẩy tiến"},
	}); err != nil {
		t.Fatalf("Lưu đại cương: %v", err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "small_1", Description: "phục bút nhỏ thứ nhất", PlantedAt: 1, Status: "planted"},
		{ID: "small_2", Description: "phục bút nhỏ thứ hai", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("Lưu sổ phục bút: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	episodic := payload["episodic_memory"].(map[string]any)
	if _, ok := episodic["foreshadow_ledger"]; !ok {
		t.Fatal("Phải giữ toàn bộ sổ phục bút khi không kích hoạt truy hồi chọn lọc")
	}
	if _, ok := payload["selected_memory"]; ok {
		t.Fatalf("Tập phục bút nhỏ không được có selected_memory, nhận được %+v", payload["selected_memory"])
	}
}

func TestContextToolFallsBackToFullForeshadowWhenSelectionIsTooSparse(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Lời mời", CoreEvent: "trưởng lão bí mật đưa lời mời thử luyện nội môn"},
		{Chapter: 2, Title: "Đêm trước thử luyện", CoreEvent: "Lâm Nghiễn chuẩn bị đáp lại lời mời thử luyện nội môn", Scenes: []string{"sắp xếp manh mối", "quyết định đến hẹn"}},
	}); err != nil {
		t.Fatalf("Lưu đại cương: %v", err)
	}
	if err := s.Progress.Init(8); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "trial_invite", Description: "mục đích thật của lời mời thử luyện nội môn", PlantedAt: 1, Status: "planted"},
		{ID: "trial_rules", Description: "mảnh bia chứa quy tắc cổ", PlantedAt: 1, Status: "planted"},
		{ID: "outer_disciple", Description: "tranh chấp về bản đồ kho báu", PlantedAt: 1, Status: "planted"},
		{ID: "elder_token", Description: "chiếc nhẫn rơi trong đầm lầy", PlantedAt: 1, Status: "planted"},
		{ID: "hidden_gate", Description: "đèn hiệu ở bến cảng bỏ hoang", PlantedAt: 1, Status: "planted"},
		{ID: "trial_bet", Description: "mảnh thư bị cháy dở trong thư viện", PlantedAt: 1, Status: "planted"},
	}); err != nil {
		t.Fatalf("Lưu sổ phục bút: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	episodic := payload["episodic_memory"].(map[string]any)
	if _, ok := episodic["foreshadow_ledger"]; !ok {
		t.Fatal("Phải giữ toàn bộ sổ phục bút khi kết quả chọn lọc quá thưa")
	}
	if selected, ok := payload["selected_memory"].(map[string]any); ok {
		if _, exists := selected["story_threads"]; exists {
			t.Fatalf("story_threads quá thưa phải quay về sổ đầy đủ, nhận được %+v", selected["story_threads"])
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
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("Đánh dấu chương hoàn thành: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "nhịp chậm lê thê, cần nén nửa đầu"); err != nil {
		t.Fatalf("Đặt hàng đợi viết lại: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "rewrite",
		Summary: "phần đệm nửa đầu quá dài, xung đột mãi chưa xuất hiện.",
		Issues: []domain.ConsistencyIssue{
			{Type: "pacing", Severity: "error", Description: "2000 chữ đầu không có tiến triển"},
		},
		ContractMisses: []string{"chưa thực hiện cam kết mở đầu thử luyện"},
	}); err != nil {
		t.Fatalf("Lưu nhận xét: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	working := payload["working_memory"].(map[string]any)
	brief, ok := working["rewrite_brief"].(map[string]any)
	if !ok {
		t.Fatalf("Thiếu working_memory.rewrite_brief, nhận được %T", working["rewrite_brief"])
	}
	if got := brief["reason"]; got != "nhịp chậm lê thê, cần nén nửa đầu" {
		t.Fatalf("Lý do viết lại không đúng, nhận được %v", got)
	}
	if got, _ := brief["review_summary"].(string); !strings.Contains(got, "phần đệm") {
		t.Fatalf("Thiếu tóm tắt nhận xét của chương, nhận được %v", brief["review_summary"])
	}
	if issues, _ := brief["issues"].([]any); len(issues) == 0 {
		t.Fatalf("Thiếu các vấn đề nhận xét trong rewrite_brief, nhận được %v", brief["issues"])
	}
	if misses, _ := brief["contract_misses"].([]any); len(misses) == 0 {
		t.Fatalf("Thiếu các mục cam kết bị bỏ sót trong rewrite_brief, nhận được %v", brief["contract_misses"])
	}
}

func TestContextToolOmitsRewriteBriefForNormalChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	args, err := json.Marshal(map[string]any{"chapter": 2})
	if err != nil {
		t.Fatalf("Mã hóa đối số: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	working := payload["working_memory"].(map[string]any)
	if _, ok := working["rewrite_brief"]; ok {
		t.Fatal("Chương ngoài PendingRewrites không được có rewrite_brief")
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
	if err := s.Progress.SetPendingRewrites([]int{3}, "làm lại theo đánh giá cung"); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 4, Scope: "arc", Verdict: "polish", Summary: "Cung hai cần nén nhịp", AffectedChapters: []int{3},
		Issues: []domain.ConsistencyIssue{{
			Type: "pacing", Severity: "error", Description: "chương 3 có phần đệm quá dài", Evidence: "xung đột đến muộn",
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
		t.Fatalf("Phải bàn giao nhận xét cung cho chương 3, nhận được %#v", brief)
	}
}

func TestContextToolDoesNotInjectUserDirectives(t *testing.T) {
	// save_directive đã bị gỡ bỏ: novel_context không còn nhúng working_memory.user_directives;
	// yêu cầu viết dài hạn thống nhất đi qua user_rules. Khóa điều này để chống hồi quy.
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	tool := newTestContextTool(s, References{}, "default")
	for name, chapter := range map[string]int{"writer": 1, "architect": 0} {
		args, _ := json.Marshal(map[string]any{"chapter": chapter})
		result, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("[%s] Thực thi công cụ: %v", name, err)
		}
		var payload map[string]any
		if err := json.Unmarshal(result, &payload); err != nil {
			t.Fatalf("[%s] Giải mã ngữ cảnh: %v", name, err)
		}
		working, ok := payload["working_memory"].(map[string]any)
		if !ok {
			t.Fatalf("[%s] Thiếu working_memory", name)
		}
		if _, exists := working["user_directives"]; exists {
			t.Errorf("[%s] working_memory không được còn user_directives (đã thống nhất vào user_rules)", name)
		}
		// user_rules vẫn phải được nhúng ổn định
		if _, ok := working["user_rules"].(map[string]any); !ok {
			t.Errorf("[%s] working_memory.user_rules phải được nhúng ổn định", name)
		}
	}
}

// TestContextToolInjectsRuleViolations kiểm tra hợp đồng đường ống dữ kiện vi phạm (vòng review thứ năm):
// vi phạm cơ học do commit lưu phải được novel_context(chapter=N) nhúng thật sự;
// editor.md §ánh xạ kiểm tra cơ học tiêu thụ chính trường này, nếu đường ống đứt thì prompt thành lời hứa rỗng.
func TestContextToolInjectsRuleViolations(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Khởi tạo kho: %v", err)
	}
	if err := st.Progress.Save(&domain.Progress{TotalChapters: 3, Phase: domain.PhaseWriting}); err != nil {
		t.Fatalf("Lưu tiến độ: %v", err)
	}
	if err := st.World.SaveRuleViolations(2, []rules.Violation{
		{Rule: "fatigue_words", Target: "bất giác", Actual: 9, Severity: rules.SeverityWarning},
	}); err != nil {
		t.Fatalf("Lưu vi phạm: %v", err)
	}

	tool := newTestContextTool(st, References{}, "default")
	args, _ := json.Marshal(map[string]any{"chapter": 2})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi công cụ: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("Giải mã ngữ cảnh: %v", err)
	}
	vs, ok := result["rule_violations"].([]any)
	if !ok || len(vs) != 1 {
		t.Fatalf("rule_violations phải được nhúng vào ngữ cảnh chương, nhận được %v", result["rule_violations"])
	}

	// Chương không có vi phạm: trường được bỏ trống (quy ước editor.md)
	args3, _ := json.Marshal(map[string]any{"chapter": 3})
	raw3, err := tool.Execute(context.Background(), args3)
	if err != nil {
		t.Fatalf("Thực thi chương 3: %v", err)
	}
	var result3 map[string]any
	_ = json.Unmarshal(raw3, &result3)
	if _, has := result3["rule_violations"]; has {
		t.Fatal("chương không có vi phạm không được mang trường rule_violations")
	}
}
