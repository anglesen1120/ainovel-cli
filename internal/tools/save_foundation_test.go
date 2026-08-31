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

func TestSaveFoundationStopsOnCorruptProgress(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"type": "premise", "content": "# Kiểm thử"})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err == nil {
		t.Fatal("Khi progress bị hỏng phải thất bại trước khi ghi premise")
	}
	if _, err := os.Stat(filepath.Join(dir, "premise.md")); !os.IsNotExist(err) {
		t.Fatalf("Lần gọi thất bại không được ghi premise, stat err=%v", err)
	}
}

func TestSaveFoundationPersistsPlanningTier(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type":    "premise",
		"content": "# Tên kiểm thử\n\n## Thể loại và tông\nKiểm thử",
		"scale":   "long",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("mong đợi có metadata phiên chạy")
	}
	if meta.PlanningTier != domain.PlanningTierLong {
		t.Fatalf("mong đợi cấp lập kế hoạch %q, nhận được %q", domain.PlanningTierLong, meta.PlanningTier)
	}
}

func TestSaveFoundationPremiseDoesNotOwnBookMetadata(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init(0); err != nil {
		t.Fatalf("Init progress: %v", err)
	}
	if err := store.Book.Save(domain.BookMetadata{Title: "Đêm dài thắp đèn", Synopsis: "Sau khi thành cũ tắt đèn, thiếu niên lần tìm sự thật về vụ mất tích."}); err != nil {
		t.Fatalf("Save book: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type": "premise",
		"content": `# Đêm dài thắp đèn

## Thể loại và tông
Kỳ ảo phương Đông, sinh tồn khắc nghiệt.`,
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	book, err := store.Book.Load()
	if err != nil {
		t.Fatalf("Load book: %v", err)
	}
	if book == nil || book.Title != "Đêm dài thắp đèn" {
		t.Fatalf("premise không được sửa thông tin tác phẩm: %+v", book)
	}
}

func TestSaveFoundationCanRevisePremiseAfterOutline(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init(0); err != nil {
		t.Fatalf("Init progress: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		t.Fatalf("UpdatePhase outline: %v", err)
	}

	args, _ := json.Marshal(map[string]any{
		"type":    "premise",
		"content": "# Tên sách mới\n\nTiền đề truyện đã sửa.",
	})
	if _, err := NewSaveFoundationTool(st).Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	p, err := st.Progress.Load()
	if err != nil {
		t.Fatalf("Load progress: %v", err)
	}
	if p.Phase != domain.PhaseOutline {
		t.Fatalf("phase = %s, muốn là outline", p.Phase)
	}
	if cp := st.Checkpoints.LatestByStep(domain.GlobalScope(), "premise"); cp == nil {
		t.Fatal("premise đã sửa phải tạo checkpoint")
	}
}

func TestSaveFoundationRejectsFullOutlineAfterComplete(t *testing.T) {
	tests := []struct {
		name    string
		typeArg string
		content any
	}{
		{
			name: "flat", typeArg: "outline",
			content: []map[string]any{{"chapter": 1, "title": "Sau khi ghi đè", "core_event": "Thay đổi", "hook": "Tiếp tục", "scenes": []string{}}},
		},
		{
			name: "layered", typeArg: "layered_outline",
			content: []map[string]any{{
				"index": 1, "title": "Tập ghi đè", "theme": "Thay đổi",
				"arcs": []map[string]any{{
					"index": 1, "title": "Cung ghi đè", "goal": "Thay đổi",
					"chapters": []map[string]any{{"title": "Sau khi ghi đè", "core_event": "Thay đổi", "hook": "Tiếp tục", "scenes": []string{}}},
				}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := store.NewStore(t.TempDir())
			if err := s.Init(); err != nil {
				t.Fatal(err)
			}
			if err := s.Progress.Init(1); err != nil {
				t.Fatal(err)
			}
			if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Tiêu đề gốc"}}); err != nil {
				t.Fatal(err)
			}
			if err := s.Progress.MarkComplete(); err != nil {
				t.Fatal(err)
			}

			args, _ := json.Marshal(map[string]any{"type": tt.typeArg, "content": tt.content})
			if _, err := NewSaveFoundationTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "Toàn bộ truyện đã hoàn tất") {
				t.Fatalf("Sau khi hoàn tất, mọi ghi đè toàn phần phải bị từ chối, err=%v", err)
			}
			outline, err := s.Outline.LoadOutline()
			if err != nil {
				t.Fatal(err)
			}
			if len(outline) != 1 || outline[0].Title != "Tiêu đề gốc" {
				t.Fatalf("Lệnh bị từ chối đã sửa outline: %+v", outline)
			}
			if _, err := os.Stat(filepath.Join(s.Dir(), "layered_outline.json")); tt.typeArg == "layered_outline" && !os.IsNotExist(err) {
				t.Fatalf("Lệnh bị từ chối đã ghi layered_outline.json: %v", err)
			}
		})
	}
}

func TestSaveFoundationOutlineClearsLayeredStateWhenDowngrading(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := store.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(store)

	layeredArgs, err := json.Marshal(map[string]any{
		"type":    "layered_outline",
		"content": `[{"index":1,"title":"Tập một","theme":"Chủ đề","arcs":[{"index":1,"title":"Cung đầu","goal":"Mục tiêu","chapters":[{"chapter":1,"title":"Chương một","core_event":"Mở đầu","hook":"Tiếp tục"}]}]}]`,
		"scale":   "long",
	})
	if err != nil {
		t.Fatalf("Marshal layered args: %v", err)
	}
	rawResult, err := tool.Execute(context.Background(), layeredArgs)
	if err != nil {
		t.Fatalf("Execute layered outline: %v", err)
	}
	var layeredResult map[string]any
	if err := json.Unmarshal(rawResult, &layeredResult); err != nil {
		t.Fatalf("Unmarshal layered result: %v", err)
	}
	if layeredResult["dynamic_planning"] != true || layeredResult["outlined_chapters"] != float64(1) {
		t.Fatalf("kết quả layered phải báo số chương đã chi tiết hóa hiện tại: %#v", layeredResult)
	}
	if _, exists := layeredResult["chapters"]; exists {
		t.Fatalf("kết quả layered không được đưa ước lượng dung lượng nội bộ vào chapters: %#v", layeredResult)
	}

	outlineArgs, err := json.Marshal(map[string]any{
		"type":    "outline",
		"content": `[{"chapter":1,"title":"Chương một","core_event":"Chuyển thành truyện vừa","hook":"Tiếp tục"}]`,
		"scale":   "mid",
	})
	if err != nil {
		t.Fatalf("Marshal outline args: %v", err)
	}
	if _, err := tool.Execute(context.Background(), outlineArgs); err != nil {
		t.Fatalf("Execute outline: %v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("LoadProgress: %v", err)
	}
	if progress == nil {
		t.Fatal("mong đợi progress tồn tại")
	}
	if progress.Layered {
		t.Fatal("mong đợi chế độ layered bị tắt")
	}
	if progress.CurrentVolume != 0 || progress.CurrentArc != 0 {
		t.Fatalf("mong đợi volume/arc được đặt lại, nhận được volume=%d arc=%d", progress.CurrentVolume, progress.CurrentArc)
	}

	volumes, err := store.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatalf("LoadLayeredOutline: %v", err)
	}
	if len(volumes) != 0 {
		t.Fatalf("mong đợi dàn ý phân tầng đã bị xóa, nhận được %d tập", len(volumes))
	}

	meta, err := store.RunMeta.Load()
	if err != nil {
		t.Fatalf("LoadRunMeta: %v", err)
	}
	if meta == nil {
		t.Fatal("mong đợi có metadata phiên chạy")
	}
	if meta.PlanningTier != domain.PlanningTierMid {
		t.Fatalf("mong đợi cấp lập kế hoạch %q, nhận được %q", domain.PlanningTierMid, meta.PlanningTier)
	}
}

func TestSaveFoundationAppendVolume(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(s)

	// Trước hết tạo layered_outline ban đầu (tập 1).
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Tập một", "theme": "Khởi đầu",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung đầu", "goal": "Mục tiêu",
				"chapters": []map[string]any{{"title": "Chương một", "core_event": "Mở đầu", "hook": "Tiếp tục"}},
			}},
		}},
		"scale": "long",
	})
	if _, err := tool.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Execute layered: %v", err)
	}

	// append_volume: thêm tập 2.
	appendArgs, _ := json.Marshal(map[string]any{
		"type":   "append_volume",
		"reason": "Mạch chính vẫn còn nhiều tuyến dài chưa khép, cần tiếp tục sang tập hai",
		"content": map[string]any{
			"index": 2, "title": "Tập hai", "theme": "Nâng cấp",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung một", "goal": "Mục tiêu",
				"chapters": []map[string]any{{"title": "Chương mới", "core_event": "Tiến triển", "hook": "Móc câu"}},
			}},
		},
	})
	res, err := tool.Execute(context.Background(), appendArgs)
	if err != nil {
		t.Fatalf("Execute append_volume: %v", err)
	}
	var result map[string]any
	json.Unmarshal(res, &result)
	if result["volume"] != float64(2) {
		t.Fatalf("mong đợi volume=2, nhận được %v", result["volume"])
	}

	// Xác nhận dàn ý có 2 tập.
	volumes, _ := s.Outline.LoadLayeredOutline()
	if len(volumes) != 2 {
		t.Fatalf("mong đợi 2 tập, nhận được %d", len(volumes))
	}
	if volumes[1].Title != "Tập hai" {
		t.Fatalf("mong đợi tiêu đề 'Tập hai', nhận được %q", volumes[1].Title)
	}

	// Lý do quyết định cuối tập phải xuất hiện trong bản ghi kiểm toán.
	recs, _ := s.Decisions.Recent(1)
	if len(recs) != 1 || recs[0].Kind != "volume_end" || recs[0].Decider != "architect" {
		t.Fatalf("append_volume phải lưu một bản ghi kiểm toán volume_end, nhận được %+v", recs)
	}
	if recs[0].Reason == "" || !strings.Contains(string(recs[0].Decision), `"append_volume"`) {
		t.Fatalf("bản ghi kiểm toán phải chứa reason và action, nhận được %+v", recs[0])
	}
}

func TestSaveFoundationExpandArcCalibratesTarget(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(5); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1, Title: "Tập một", Theme: "Lựa chọn",
		Arcs: []domain.ArcOutline{
			{Index: 1, Title: "Cung đã hoàn tất", Goal: "Thiết lập liên minh", Chapters: []domain.OutlineEntry{{Title: "Rạn nứt", CoreEvent: "Liên minh bất ngờ tan vỡ"}}},
			{Index: 2, Title: "Tiêu đề cũ", Goal: "Duy trì liên minh", EstimatedChapters: 4},
		},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "expand_arc", "volume": 1, "arc": 2,
		"content": map[string]any{
			"title": "Sau rạn nứt liên minh",
			"goal":  "Để hai phía rạn nứt cùng đẩy mạch chính bằng những lựa chọn khác nhau",
			"chapters": []map[string]any{{
				"title": "Mỗi người một ngả", "core_event": "Hai phía lần theo sự thật riêng", "hook": "Hai manh mối bất ngờ giao nhau", "scenes": []string{"Chia tay", "Truy tìm"},
			}},
		},
	})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute expand_arc: %v", err)
	}
	var facts map[string]any
	if err := json.Unmarshal(result, &facts); err != nil {
		t.Fatalf("Unmarshal result: %v", err)
	}
	if facts["title"] != "Sau rạn nứt liên minh" || facts["goal"] != "Để hai phía rạn nứt cùng đẩy mạch chính bằng những lựa chọn khác nhau" {
		t.Fatalf("dữ kiện hiệu chỉnh không đúng, nhận được %+v", facts)
	}
	volumes, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		t.Fatalf("LoadLayeredOutline: %v", err)
	}
	if got := volumes[0].Arcs[1]; got.Title != "Sau rạn nứt liên minh" || got.Goal != "Để hai phía rạn nứt cùng đẩy mạch chính bằng những lựa chọn khác nhau" || len(got.Chapters) != 1 {
		t.Fatalf("cung đã mở rộng không đúng: %+v", got)
	}
}

func TestSaveFoundationAppendVolumeValidation(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}

	tool := NewSaveFoundationTool(s)

	// Dàn ý phân tầng ban đầu.
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Tập một", "theme": "Khởi đầu",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung đầu", "goal": "Mục tiêu",
				"chapters": []map[string]any{{"title": "Chương một", "core_event": "Mở đầu", "hook": "Tiếp tục"}},
			}},
		}},
		"scale": "long",
	})
	tool.Execute(context.Background(), layeredArgs)

	// Index không tăng phải thất bại do kiểm tra cấu trúc.
	appendArgs, _ := json.Marshal(map[string]any{
		"type":   "append_volume",
		"reason": "Lý do kiểm thử",
		"content": map[string]any{
			"index": 1, "title": "Index trùng lặp", "theme": "x",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung một", "goal": "Mục tiêu",
				"chapters": []map[string]any{{"title": "Chương", "core_event": "Sự kiện", "hook": "Móc câu"}},
			}},
		},
	})
	_, err := tool.Execute(context.Background(), appendArgs)
	if err == nil {
		t.Fatal("mong đợi lỗi khi thêm tập có index không tăng")
	}
}

// TestSaveFoundationAppendVolumeRejectsAfterComplete xác nhận không được append_volume sau Phase=Complete.
// Nó thay thế ngữ nghĩa cũ "từ chối thêm vào tập Final" vì trường Final đã bị xóa.
func TestSaveFoundationAppendVolumeRejectsAfterComplete(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	appendArgs, _ := json.Marshal(map[string]any{
		"type":   "append_volume",
		"reason": "Lý do kiểm thử",
		"content": map[string]any{
			"index": 1, "title": "Thử viết tiếp", "theme": "x",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung", "goal": "g",
				"chapters": []map[string]any{{"title": "Chương", "core_event": "e", "hook": "h"}},
			}},
		},
	})
	if _, err := tool.Execute(context.Background(), appendArgs); err == nil {
		t.Fatal("mong đợi lỗi khi thêm tập sau Phase=Complete")
	}
}

func TestSaveFoundationUpdateCompass(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "update_compass",
		"content": map[string]any{
			"ending_direction": "Nhân vật chính đối mặt với lựa chọn cuối cùng",
			"open_threads":     []string{"Manh mối A", "Quan hệ B"},
			"estimated_scale":  "Dự kiến 4-6 tập",
		},
	})
	_, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute update_compass: %v", err)
	}

	compass, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if compass == nil || compass.EndingDirection != "Nhân vật chính đối mặt với lựa chọn cuối cùng" {
		t.Fatalf("compass không đúng: %+v", compass)
	}
	if len(compass.OpenThreads) != 2 {
		t.Fatalf("mong đợi 2 tuyến truyện đang mở, nhận được %d", len(compass.OpenThreads))
	}
}

func TestSaveFoundationUpdateCompassOverridesLastUpdated(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CompletedChapters: []int{1, 2, 3, 5, 4}, // Không theo thứ tự để xác nhận lấy max thay vì len.
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "update_compass",
		"content": map[string]any{
			"ending_direction": "Nhân vật chính đối mặt với lựa chọn cuối cùng",
			"open_threads":     []string{"Manh mối A"},
			"last_updated":     0, // LLM thường quên điền hoặc để 0.
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute update_compass: %v", err)
	}

	compass, err := s.Outline.LoadCompass()
	if err != nil {
		t.Fatalf("LoadCompass: %v", err)
	}
	if compass.LastUpdated != 5 {
		t.Fatalf("mong đợi LastUpdated=5 (giá trị lớn nhất của CompletedChapters), nhận được %d", compass.LastUpdated)
	}
}

func TestSaveFoundationUpdateCompassRequiresDirection(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type":    "update_compass",
		"content": map[string]any{"estimated_scale": "3 tập"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("mong đợi lỗi khi ending_direction trống")
	}
}

func TestSaveFoundationAcceptsDirectJSONArrayContent(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveFoundationTool(store)
	args, err := json.Marshal(map[string]any{
		"type": "outline",
		"content": []map[string]any{
			{
				"chapter":    1,
				"title":      "Chương một",
				"core_event": "Nhân vật chính xuất hiện",
				"hook":       "Tiếp tục",
				"scenes":     []string{"Cảnh một", "Cảnh hai"},
			},
		},
		"scale": "short",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	outline, err := store.Outline.LoadOutline()
	if err != nil {
		t.Fatalf("LoadOutline: %v", err)
	}
	if len(outline) != 1 || outline[0].Title != "Chương một" {
		t.Fatalf("dàn ý không đúng: %+v", outline)
	}
}

// completeBookSetup tạo Store nhỏ nhất ở giai đoạn writing với 2 chương để kiểm thử complete_book.
// Lớp công cụ kiểm tra các điều kiện có thể liệt kê: progress đã khởi tạo, PendingRewrites trống,
// đã viết ít nhất một chương và dàn ý không còn chương chưa viết.
func completeBookSetup(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(2); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)
	return s
}

func TestSaveFoundationCompleteBookPushesPhaseComplete(t *testing.T) {
	s := completeBookSetup(t)
	for ch := 1; ch <= 2; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "Hai chương trong dàn ý đã viết xong, mệnh đề kết cục đã được trả lời",
	})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute complete_book: %v", err)
	}
	var result map[string]any
	_ = json.Unmarshal(res, &result)
	if result["book_complete"] != true {
		t.Fatalf("mong đợi book_complete=true, nhận được %+v", result)
	}
	if result["phase"] != string(domain.PhaseComplete) {
		t.Fatalf("mong đợi phase=complete, nhận được %v", result["phase"])
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("mong đợi progress.Phase=complete, nhận được %s", progress.Phase)
	}

	// Lý do hoàn tất phải được lưu trong bản ghi kiểm toán tại thời điểm quyết định.
	recs, _ := s.Decisions.Recent(1)
	if len(recs) != 1 || recs[0].Kind != "volume_end" || recs[0].Decider != "architect" {
		t.Fatalf("complete_book phải lưu một bản ghi kiểm toán volume_end, nhận được %+v", recs)
	}
	if recs[0].Reason == "" || !strings.Contains(string(recs[0].Decision), `"complete_book"`) {
		t.Fatalf("bản ghi kiểm toán phải chứa reason và action, nhận được %+v", recs[0])
	}
	if !strings.Contains(string(recs[0].Facts), `"completed_chapters":2`) {
		t.Fatalf("dữ kiện kiểm toán phải chứa tiến độ tại thời điểm quyết định, nhận được %s", recs[0].Facts)
	}
}

// TestSaveFoundationCompleteBookRejectsZeroChapters tái hiện sự cố thật: sau khi vừa lưu kế hoạch,
// phase tự chuyển sang writing và mô hình yếu gọi nhầm complete_book. Chưa viết chương nào phải bị từ chối,
// nếu không toàn bộ sách sẽ bị bỏ qua khi bị đánh dấu hoàn tất (0/68 chương).
func TestSaveFoundationCompleteBookRejectsZeroChapters(t *testing.T) {
	s := completeBookSetup(t)
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "Lý do kiểm thử",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("complete_book khi chưa viết chương nào phải bị từ chối")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseWriting {
		t.Fatalf("phase phải giữ là writing, nhận được %s", progress.Phase)
	}
}

// TestSaveFoundationCompleteBookRejectsOpenThreads bảo vệ ở lớp công cụ quy tắc "tuyến dài chưa khép thì không thể hoàn tất".
// Hợp đồng OpenThreads yêu cầu khép lại trước khi kết thúc; ngoại lệ phải được lưu rõ ràng.
// Chỉ được hoàn tất sau khi update_compass xóa open_threads.
func TestSaveFoundationCompleteBookRejectsOpenThreads(t *testing.T) {
	s := completeBookSetup(t)
	for ch := 1; ch <= 2; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	if err := s.Outline.SaveCompass(domain.StoryCompass{
		EndingDirection: "Kết cục tiềm năng", OpenThreads: []string{"Hướng đi của hạn 80 năm", "Khả năng tái ngộ tinh biến"},
	}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{}, "reason": "Mạch chính đã khép lại",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "open_threads") {
		t.Fatalf("open_threads không trống phải từ chối hoàn tất và hướng dẫn update_compass, nhận được: %v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase != domain.PhaseWriting {
		t.Fatalf("phase phải giữ là writing, nhận được %s", p.Phase)
	}
	// Sau khi lưu việc khép lại rõ ràng bằng update_compass xóa open_threads thì cho phép.
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "Kết cục đã đạt"}); err != nil {
		t.Fatalf("SaveCompass: %v", err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Sau khi xóa sạch tuyến dài thì phải cho phép hoàn tất：%v", err)
	}
}

// TestSaveFoundationCompleteBookRejectsUnwrittenChapters không cho phép hoàn tất khi dàn ý còn chương chưa viết.
// Con đường chính thức để khép sớm là tập cuối có final.
func TestSaveFoundationCompleteBookRejectsUnwrittenChapters(t *testing.T) {
	s := completeBookSetup(t)
	if err := s.Progress.MarkChapterComplete(1, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "Lý do kiểm thử",
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("complete_book khi dàn ý còn chương chưa viết phải bị từ chối")
	}
	if !strings.Contains(err.Error(), "final") {
		t.Fatalf("thông báo từ chối phải hướng dẫn đường tập cuối final, nhận được %v", err)
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseWriting {
		t.Fatalf("phase phải giữ là writing, nhận được %s", progress.Phase)
	}
}

func TestSaveFoundationCompleteBookRejectsBeforeWriting(t *testing.T) {
	// Gọi nhầm complete_book trong giai đoạn lập kế hoạch phải bị từ chối để tránh bỏ qua toàn bộ quá trình viết.
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("InitProgress: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhasePremise)
	_ = s.Progress.UpdatePhase(domain.PhaseOutline)
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "Lý do kiểm thử",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("mong đợi lỗi khi phase khác writing")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase != domain.PhaseOutline {
		t.Fatalf("phase phải giữ là outline, nhận được %s", progress.Phase)
	}
}

// TestSaveFoundationVolumeEndRequiresReason yêu cầu lý do quyết định cho cả ba lựa chọn cuối tập.
// Đây là quyết định ngữ nghĩa quan trọng nhất của toàn truyện, nên lý do phải trở thành dữ kiện kiểm toán.
func TestSaveFoundationVolumeEndRequiresReason(t *testing.T) {
	s := completeBookSetup(t)
	tool := NewSaveFoundationTool(s)
	for _, typ := range []string{"append_volume", "complete_book"} {
		args, _ := json.Marshal(map[string]any{
			"type": typ, "content": map[string]any{},
		})
		_, err := tool.Execute(context.Background(), args)
		if err == nil || !strings.Contains(err.Error(), "reason") {
			t.Fatalf("%s thiếu reason phải bị từ chối và thông báo phải nhắc đến reason, nhận được %v", typ, err)
		}
	}
	if recs, _ := s.Decisions.Recent(1); len(recs) != 0 {
		t.Fatalf("lệnh bị từ chối không được tạo bản ghi kiểm toán, nhận được %+v", recs)
	}
}

func TestSaveFoundationCompleteBookRejectsWithPendingRewrites(t *testing.T) {
	s := completeBookSetup(t)
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "Nhịp điệu cuối chương quá nhanh"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	tool := NewSaveFoundationTool(s)
	args, _ := json.Marshal(map[string]any{
		"type": "complete_book", "content": map[string]any{},
		"reason": "Lý do kiểm thử",
	})
	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("mong đợi lỗi khi PendingRewrites không trống")
	}
	progress, _ := s.Progress.Load()
	if progress.Phase == domain.PhaseComplete {
		t.Fatalf("phase không được là Complete khi PendingRewrites không trống: %s", progress.Phase)
	}
}
