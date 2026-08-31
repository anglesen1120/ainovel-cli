package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

func newTestCommitChapterTool(st *store.Store) *CommitChapterTool {
	return NewCommitChapterTool(st, NewStyleStatsIndex(st))
}

func saveTestChapterRecord(t *testing.T, st *store.Store, chapter int, content string) {
	t.Helper()
	if _, err := st.ChapterRecords.Accept(chapter, domain.ChapterOriginGenerated, content, domain.ChapterFacts{
		Title: fmt.Sprintf("Chương %d", chapter), Summary: "Tóm tắt sẵn có", KeyEvents: []string{"Sự kiện sẵn có"},
	}, domain.StyleDelta{}); err != nil {
		t.Fatalf("Lưu bản ghi chương %d: %v", chapter, err)
	}
}

func TestCommitChapterSchemaDescribesFeedbackAsObject(t *testing.T) {
	tool := newTestCommitChapterTool(store.NewStore(t.TempDir()))
	if !tool.StrictSchema() {
		t.Fatal("commit_chapter phải dùng schema nghiêm ngặt")
	}
	if err := llmcontract.ValidateStrictReady(tool.Schema()); err != nil {
		t.Fatalf("schema của commit_chapter chưa sẵn sàng cho chế độ nghiêm ngặt: %v", err)
	}
	schema := tool.Schema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("thiếu properties của schema: %#v", schema["properties"])
	}
	feedback, ok := props["feedback"].(map[string]any)
	if !ok {
		t.Fatalf("thiếu schema cho feedback: %#v", props["feedback"])
	}
	desc, _ := feedback["description"].(string)
	if !strings.Contains(desc, "JSON object") || !strings.Contains(desc, "JSON đã stringify") {
		t.Fatalf("mô tả feedback phải cảnh báo không dùng JSON chuỗi hóa, nhận được %q", desc)
	}
	if got := fmt.Sprint(feedback["type"]); got != "[object null]" {
		t.Fatalf("kiểu feedback = %v, mong đợi object có thể null", feedback["type"])
	}
}

func TestCommitChapterRejectsUnknownForeshadowReferenceBeforePending(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "title": "Chương 1", "summary": "Tiến triển", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Phát hiện manh mối"},
		"foreshadow_updates": []map[string]any{{"id": "missing", "action": "resolve"}},
	})
	if _, err := newTestCommitChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "id không xác định") {
		t.Fatalf("mong đợi từ chối manh mối chưa biết, nhận được %v", err)
	}
	if pending, err := s.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("đối số không hợp lệ không được tạo commit chờ: pending=%+v err=%v", pending, err)
	}
}

func TestCommitChapterRejectsSkippedNormalChapter(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal(map[string]any{
		"chapter": 2, "title": "Chương 2", "summary": "Bỏ qua chương 1", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := newTestCommitChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "chỉ được gửi chương kế tiếp 1") {
		t.Fatalf("mong đợi từ chối chương bị bỏ qua, nhận được %v", err)
	}
	if pending, err := s.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("commit bị từ chối không được tạo trạng thái chờ, pending=%+v err=%v", pending, err)
	}
}

func TestCommitChapterRejectsInvalidNestedFields(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "title": "Chương 1", "summary": "Tiến triển", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Phát hiện manh mối"},
		"relationship_changes": []map[string]any{{"character_a": "Nhân vật chính", "character_b": "", "relation": "đối địch"}},
	})
	if _, err := newTestCommitChapterTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "relationship_changes[0]") {
		t.Fatalf("mong đợi từ chối trường lồng nhau, nhận được %v", err)
	}
}

func TestCommitChapterRejectsNonPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := store.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := store.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương: %v", err)
	}
	if err := store.Progress.SetPendingRewrites([]int{2}, "thử nghiệm viết lại"); err != nil {
		t.Fatalf("Đặt các bản viết lại chờ xử lý: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}
	if err := store.Drafts.SaveDraft(3, "Đây là phần nội dung sai của chương."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	tool := newTestCommitChapterTool(store)
	args, err := json.Marshal(map[string]any{
		"chapter":         3,
		"title":           "Chương 3",
		"summary":         "Gửi nhầm",
		"characters":      []string{"Nhân vật chính"},
		"key_events":      []string{"Gửi nhầm"},
		"timeline_events": []any{},
	})
	if err != nil {
		t.Fatalf("Mã hóa JSON: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil {
		t.Fatal("mong đợi commit bị từ chối trong luồng viết lại")
	}

	if _, err := os.Stat(dir + "/chapters/03.md"); !os.IsNotExist(err) {
		t.Fatalf("chương không được lưu xuống đĩa, stat err=%v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("Tải tiến độ: %v", err)
	}
	if len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 2 {
		t.Fatalf("các chương hoàn thành chỉ nên chứa chương 2 gốc, nhận được %v", progress.CompletedChapters)
	}
	if progress.CurrentChapter != 3 {
		t.Fatalf("chương hiện tại không được vượt quá tiến độ gốc, nhận được %d", progress.CurrentChapter)
	}
}

func TestCommitChapterAllowsPendingRewrite(t *testing.T) {
	dir := t.TempDir()
	store := store.NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := store.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := store.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương: %v", err)
	}
	if err := store.Progress.SetPendingRewrites([]int{2}, "thử nghiệm viết lại"); err != nil {
		t.Fatalf("Đặt các bản viết lại chờ xử lý: %v", err)
	}
	if err := store.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}
	if err := store.Drafts.SaveDraft(2, "Đây là phần nội dung đúng của chương đang chờ viết lại."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	tool := newTestCommitChapterTool(store)
	args, err := json.Marshal(map[string]any{
		"chapter":         2,
		"title":           "Chương 2",
		"summary":         "Gửi đúng",
		"characters":      []string{"Nhân vật chính"},
		"key_events":      []string{"Hoàn thành viết lại"},
		"timeline_events": []any{},
	})
	if err != nil {
		t.Fatalf("Mã hóa JSON: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi: %v", err)
	}

	if _, err := os.Stat(dir + "/chapters/02.md"); err != nil {
		t.Fatalf("chương phải được lưu xuống đĩa: %v", err)
	}

	progress, err := store.Progress.Load()
	if err != nil {
		t.Fatalf("Tải tiến độ: %v", err)
	}
	if len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 2 {
		t.Fatalf("các chương hoàn thành không như mong đợi: %v", progress.CompletedChapters)
	}
	pending, err := store.Signals.LoadPendingCommit()
	if err != nil {
		t.Fatalf("Tải commit chờ: %v", err)
	}
	if pending != nil {
		t.Fatalf("mong đợi commit chờ đã được xóa, nhận được %+v", pending)
	}
}

// TestCommitChapterRewriteKeepsOwnForeshadowPlant khóa cứng issue #112: khi viết lại chương gieo manh mối,
// Writer thấy trong sổ cái manh mối đó đã tồn tại thì chỉ viết advance; bản cũ ghi đè toàn bộ bản ghi chương,
// plant bị mất theo, Projector phát lại toàn bộ sẽ báo "tiến triển manh mối không xác định" và khóa cứng hàng đợi sửa chữa.
// Sự thật về việc gieo manh mối phải được giữ lại.
func TestCommitChapterRewriteKeepsOwnForeshadowPlant(t *testing.T) {
	const foreshadowID = "f_spillway_photo"
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	// Trạng thái đã ghi đĩa sau khi nộp lần đầu chương 2: trong bản ghi là plant, trong sổ cái đã có mục.
	if _, err := s.ChapterRecords.Accept(2, domain.ChapterOriginGenerated, "Văn bản cũ.", domain.ChapterFacts{
		Title: "Chương 2", Summary: "Gài manh mối", KeyEvents: []string{"Phát hiện bức ảnh cũ"},
		ForeshadowUpdates: []domain.ForeshadowUpdate{{ID: foreshadowID, Action: "plant", Description: "Bức ảnh cũ ở đường xả lũ"}},
	}, domain.StyleDelta{}); err != nil {
		t.Fatalf("Chấp nhận: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: foreshadowID, Description: "Bức ảnh cũ ở đường xả lũ", PlantedAt: 2, Status: "planted"},
	}); err != nil {
		t.Fatalf("Lưu sổ cái manh mối: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "thử nghiệm viết lại"); err != nil {
		t.Fatalf("Đặt các bản viết lại chờ xử lý: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, "Đây là văn bản của chương 2 sau khi viết lại."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"chapter": 2, "title": "Chương 2", "summary": "Viết lại rồi gài manh mối",
		"characters": []string{"Nhân vật chính"}, "key_events": []string{"Tái phát hiện bức ảnh cũ"},
		"foreshadow_updates": []map[string]any{{"id": foreshadowID, "action": "advance"}},
	})
	if err != nil {
		t.Fatalf("Mã hóa JSON: %v", err)
	}
	if _, err := newTestCommitChapterTool(s).Execute(context.Background(), args); err != nil {
		t.Fatalf("Viết lại chương gieo manh mối không được thất bại: %v", err)
	}

	record, err := s.ChapterRecords.Load(2)
	if err != nil || record == nil {
		t.Fatalf("Tải bản ghi: %+v err=%v", record, err)
	}
	var planted bool
	for _, u := range record.Facts.ForeshadowUpdates {
		if u.ID == foreshadowID && u.Action == "plant" {
			planted = true
		}
	}
	if !planted {
		t.Fatalf("sau khi viết lại phải giữ plant của chính chương này, thực tế %+v", record.Facts.ForeshadowUpdates)
	}

	ledger, err := s.World.LoadForeshadowLedger()
	if err != nil {
		t.Fatalf("Tải sổ cái manh mối: %v", err)
	}
	if len(ledger) != 1 || ledger[0].ID != foreshadowID || ledger[0].PlantedAt != 2 {
		t.Fatalf("sổ cái phải giữ sự thật gieo mầm của chương 2, thực tế %+v", ledger)
	}

	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("Tải tiến độ: %v", err)
	}
	if len(progress.PendingRewrites) != 0 {
		t.Fatalf("hàng đợi sửa chữa phải trống, thực tế %v", progress.PendingRewrites)
	}
}

func TestCommitChapterRewriteRepairsPlantLostByPreviousFailure(t *testing.T) {
	const foreshadowID = "F24_LICENSE_SUSPENSION"
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	// Mô phỏng trạng thái sau khi lần viết lại đầu của bản cũ thất bại: sổ cái phái sinh vẫn giữ đúng sự thật gieo mầm,
	// nhưng bản ghi chương đã bị advance ghi đè, thiếu plant cùng chương.
	oldContent := "Văn bản cuối cùng còn sót lại sau khi bản cũ thất bại."
	if _, err := s.ChapterRecords.Accept(2, domain.ChapterOriginGenerated, oldContent, domain.ChapterFacts{
		Title: "Chương 2", Summary: "Bản ghi hỏng", KeyEvents: []string{"Tiến triển manh mối"},
		ForeshadowUpdates: []domain.ForeshadowUpdate{{ID: foreshadowID, Action: "advance"}},
	}, domain.StyleDelta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveFinalChapter(2, oldContent); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{{
		ID: foreshadowID, Description: "Manh mối tạm dừng cấp phép", PlantedAt: 2, Status: "planted",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "khôi phục bản ghi hỏng của bản cũ"); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(2, "Văn bản chương 2 sau khi được sửa."); err != nil {
		t.Fatal(err)
	}

	args, err := json.Marshal(map[string]any{
		"chapter": 2, "title": "Chương 2", "summary": "Khôi phục chuỗi manh mối",
		"characters": []string{"Nhân vật chính"}, "key_events": []string{"Tiến triển manh mối"},
		"foreshadow_updates": []map[string]any{{"id": foreshadowID, "action": "advance"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTestCommitChapterTool(s).Execute(context.Background(), args); err != nil {
		t.Fatalf("plant cùng chương bị mất ở bản cũ phải có thể khôi phục một cách xác định: %v", err)
	}

	record, err := s.ChapterRecords.Load(2)
	if err != nil || record == nil {
		t.Fatalf("Tải bản ghi: %+v err=%v", record, err)
	}
	if got := record.Facts.ForeshadowUpdates; len(got) != 2 || got[0].Action != "plant" || got[0].ID != foreshadowID || got[1].Action != "advance" {
		t.Fatalf("chuỗi manh mối đã sửa = %+v", got)
	}
	ledger, err := s.World.LoadForeshadowLedger()
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger) != 1 || ledger[0].Status != "advanced" || ledger[0].PlantedAt != 2 {
		t.Fatalf("sổ cái sau khi tái chiếu = %+v", ledger)
	}
}

func TestCommitChapterRewriteValidatesRecordSetBeforeWriting(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChapterRecords.Accept(1, domain.ChapterOriginGenerated, "Văn bản cuối của chương 1.", domain.ChapterFacts{
		Title: "Chương 1", Summary: "Đường cơ sở hỏng", KeyEvents: []string{"Tiến triển sai"},
		ForeshadowUpdates: []domain.ForeshadowUpdate{{ID: "missing", Action: "advance"}},
	}, domain.StyleDelta{}); err != nil {
		t.Fatal(err)
	}
	oldContent := "Văn bản cuối cũ của chương 2."
	if _, err := s.ChapterRecords.Accept(2, domain.ChapterOriginGenerated, oldContent, domain.ChapterFacts{
		Title: "Chương 2", Summary: "Tóm tắt gốc", KeyEvents: []string{"Sự kiện gốc"},
	}, domain.StyleDelta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveFinalChapter(2, oldContent); err != nil {
		t.Fatal(err)
	}
	for _, chapter := range []int{1, 2} {
		if err := s.Progress.MarkChapterComplete(chapter, 3000, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "kiểm tra trước khi ghi"); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(2, "Nội dung mới cho chương 2."); err != nil {
		t.Fatal(err)
	}

	args, err := json.Marshal(map[string]any{
		"chapter": 2, "title": "Chương 2", "summary": "Tóm tắt mới",
		"characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện mới"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = newTestCommitChapterTool(s).Execute(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "đã gỡ đóng băng và chưa ghi kết quả làm lại") {
		t.Fatalf("mong đợi lỗi chiếu trước khi ghi, nhận được %v", err)
	}
	if strings.Contains(err.Error(), errs.ErrStoreWrite.Error()) {
		t.Fatalf("bất biến chiếu không được phân loại là lỗi ghi kho dữ liệu: %v", err)
	}
	final, err := s.Drafts.LoadChapterText(2)
	if err != nil || final != oldContent {
		t.Fatalf("văn bản cuối của chương đã thay đổi trước khi xác thực: %q err=%v", final, err)
	}
	record, err := s.ChapterRecords.Load(2)
	if err != nil || record == nil || record.Revision != 1 || record.Content != oldContent {
		t.Fatalf("bản ghi chương đã thay đổi trước khi xác thực: %+v err=%v", record, err)
	}
	if pending, err := s.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("commit đông cứng không hợp lệ phải được xóa: %+v err=%v", pending, err)
	}
}

// TestCommitChapterRewriteRejectsForwardForeshadowReference cùng nguồn với ca trước (issue #112):
// sổ cái là ảnh chiếu của toàn bộ sách, khi viết lại chương sớm thì trong đó vẫn còn những manh mối chỉ được gieo ở chương sau.
// Bản cũ cho đi qua → Projector phát lại theo thứ tự chương sẽ báo "tiến triển manh mối không xác định", và lúc này bản ghi chương đã bị ghi đè nên hàng đợi sửa chữa bị khóa cứng.
// Phải chặn trước khi ghi xuống đĩa, đồng thời nói rõ "được gieo ở chương nào" thì mô hình mới tự sửa được.
func TestCommitChapterRewriteRejectsForwardForeshadowReference(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	for _, ch := range []int{2, 7} {
		if _, err := s.ChapterRecords.Accept(ch, domain.ChapterOriginGenerated, "Văn bản cũ.", domain.ChapterFacts{
			Title: fmt.Sprintf("Chương %d", ch), Summary: "Tóm tắt", KeyEvents: []string{"Sự kiện"},
		}, domain.StyleDelta{}); err != nil {
			t.Fatalf("Chấp nhận %d: %v", ch, err)
		}
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("Đánh dấu hoàn thành chương %d: %v", ch, err)
		}
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "f_late", Description: "Manh mối được gieo ở chương 7", PlantedAt: 7, Status: "planted"},
	}); err != nil {
		t.Fatalf("Lưu sổ cái manh mối: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "thử nghiệm viết lại"); err != nil {
		t.Fatalf("Đặt các bản viết lại chờ xử lý: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, "Đây là văn bản chương 2 sau khi viết lại."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	args, err := json.Marshal(map[string]any{
		"chapter": 2, "title": "Chương 2", "summary": "s",
		"characters": []string{"Nhân vật chính"}, "key_events": []string{"e"},
		"foreshadow_updates": []map[string]any{{"id": "f_late", "action": "advance"}},
	})
	if err != nil {
		t.Fatalf("Mã hóa JSON: %v", err)
	}
	_, err = newTestCommitChapterTool(s).Execute(context.Background(), args)
	if err == nil {
		t.Fatal("manh mối được gieo ở chương sau phải bị từ chối")
	}
	if !strings.Contains(err.Error(), "được gieo ở chương 7") {
		t.Fatalf("lỗi phải chỉ rõ chương gieo mầm để mô hình có thể tự sửa, thực tế: %v", err)
	}
	// Điểm mấu chốt: chặn trước khi ghi xuống đĩa — bản ghi chương và hàng đợi sửa chữa đều không được bị lỗi này làm nhiễm bẩn.
	if pending, err := s.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("xác thực thất bại không được để lại commit chờ: pending=%+v err=%v", pending, err)
	}
	record, err := s.ChapterRecords.Load(2)
	if err != nil || record == nil {
		t.Fatalf("Tải bản ghi: %+v err=%v", record, err)
	}
	if record.Content != "Văn bản cũ." {
		t.Fatalf("xác thực thất bại không được ghi đè bản ghi chương, thực tế %q", record.Content)
	}
	p, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("Tải tiến độ: %v", err)
	}
	if len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 2 {
		t.Fatalf("hàng đợi sửa chữa phải giữ nguyên để thử lại: %v", p.PendingRewrites)
	}
}

func TestCommitChapterClearsInvalidLegacyRewritePending(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	oldContent := "Văn bản cuối cũ."
	if _, err := s.ChapterRecords.Accept(2, domain.ChapterOriginGenerated, oldContent, domain.ChapterFacts{
		Title: "Chương 2", Summary: "Tóm tắt cũ", KeyEvents: []string{"Sự kiện cũ"},
	}, domain.StyleDelta{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveFinalChapter(2, oldContent); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{{
		ID: "f_late", Description: "Manh mối về sau", PlantedAt: 7, Status: "planted",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(2, 3000, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "khôi phục commit đóng băng cũ"); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"chapter": 2, "title": "Chương 2", "summary": "Gửi cũ trái phép",
		"characters": []string{"Nhân vật chính"}, "key_events": []string{"Tiến triển sớm"},
		"foreshadow_updates": []map[string]any{{"id": "f_late", "action": "advance"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter: 2, Stage: domain.CommitStageStarted, Rewrite: true,
		Payload: payload, DraftContent: "Văn bản đã đóng băng.",
	}); err != nil {
		t.Fatal(err)
	}

	_, err = newTestCommitChapterTool(s).Execute(context.Background(), payload)
	if err == nil || !strings.Contains(err.Error(), "đã gỡ đóng băng") || !strings.Contains(err.Error(), "được gieo ở chương 7") {
		t.Fatalf("mong đợi lỗi có thể hành động cho pending di sản, nhận được %v", err)
	}
	if pending, err := s.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("pending di sản không hợp lệ phải được xóa: %+v err=%v", pending, err)
	}
	record, err := s.ChapterRecords.Load(2)
	if err != nil || record == nil || record.Revision != 1 || record.Content != oldContent {
		t.Fatalf("việc xóa pending đã làm đổi bản ghi chương: %+v err=%v", record, err)
	}
}

func TestCommitChapterRefreshesSharedStyleStatsAfterRewrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	completed := []int{1, 2, 3, 4, 5}
	for _, chapter := range completed {
		content := fmt.Sprintf("# Chương %d\nNội dung bình thường.\nCâu chuyện tiếp tục.", chapter)
		if err := s.Drafts.SaveFinalChapter(chapter, content); err != nil {
			t.Fatal(err)
		}
		saveTestChapterRecord(t, s, chapter, content)
		if err := s.Progress.MarkChapterComplete(chapter, 100, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	styleStats := NewStyleStatsIndex(s)
	before, err := styleStats.Snapshot(completed, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before == nil {
		t.Fatal("mong đợi thống kê phong cách đã được khởi tạo")
	}

	if err := s.Progress.SetPendingRewrites([]int{2}, "kiểm tra thống kê phong cách gia tăng"); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveDraft(2, "# Chương 2\nAnh ấy không phải lùi bước mà là đang chờ đợi.\nCâu chuyện sau khi viết lại vẫn tiếp tục."); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"title":      "Chương 2",
		"summary":    "Hoàn tất bài kiểm tra viết lại thống kê gia tăng",
		"characters": []string{"Nhân vật chính"},
		"key_events": []string{"Hoàn thành viết lại"},
	})
	if _, err := NewCommitChapterTool(s, styleStats).Execute(context.Background(), args); err != nil {
		t.Fatal(err)
	}

	after, err := styleStats.Snapshot(completed, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, pattern := range after.Patterns {
		if strings.HasPrefix(pattern.Name, "Câu chỉnh hướng") && pattern.Total == 1 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("viết lại không làm mới thống kê phong cách dùng chung: %+v", after.Patterns)
	}
}

func TestCommitChapterRewriteRecoveryUsesFrozenDraft(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("Cập nhật giai đoạn: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, "Văn bản cuối cũ của chương 2"); err != nil {
		t.Fatalf("Lưu văn bản cuối của chương: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, 100, "", ""); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương: %v", err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "kiểm tra khôi phục viết lại"); err != nil {
		t.Fatalf("Đặt các bản viết lại chờ xử lý: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}

	persistedArgs, err := json.Marshal(map[string]any{
		"chapter": 2, "title": "Tiêu đề đóng băng", "summary": "Tóm tắt đóng băng", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện đóng băng"},
	})
	if err != nil {
		t.Fatalf("Mã hóa JSON: %v", err)
	}
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter: 2, Stage: domain.CommitStageStarted, Rewrite: true, RewriteMode: "rewrite",
		Payload: persistedArgs, DraftContent: "Văn bản viết lại đã hoàn thành của chương 2",
	}); err != nil {
		t.Fatalf("Lưu commit chờ: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, "Bản nháp bị ghi đè sai sau khi khởi động lại"); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	tool := newTestCommitChapterTool(s)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":2,"summary":"Không được dùng tham số mới"}`)); err != nil {
		t.Fatalf("Thực thi khôi phục: %v", err)
	}
	final, err := s.Drafts.LoadChapterText(2)
	if err != nil {
		t.Fatalf("Tải văn bản chương: %v", err)
	}
	if final != "Văn bản viết lại đã hoàn thành của chương 2" {
		t.Fatalf("khôi phục viết lại đã dùng nháp bị ghi đè: %q", final)
	}
	summary, err := s.Summaries.LoadSummary(2)
	if err != nil {
		t.Fatalf("Tải tóm tắt: %v", err)
	}
	if summary == nil || summary.Summary != "Tóm tắt đóng băng" {
		t.Fatalf("khôi phục viết lại đã dùng tham số tái sinh: %+v", summary)
	}
}

// TestCommitChapterUpdatesCastLedger xác minh: commit_chapter cộng dồn characters của chương này vào cast_ledger,
// brief_role do cast_intros cung cấp được sử dụng, và các nhân vật lõi trong characters.json không đi vào ledger.
func TestCommitChapterUpdatesCastLedger(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("Cập nhật giai đoạn: %v", err)
	}
	// Đặt hồ sơ nhân vật lõi (những người này không được vào cast_ledger)
	if err := s.Characters.Save([]domain.Character{
		{Name: "Lâm Mặc", Role: "Nhân vật chính", Tier: "core"},
		{Name: "Lý Thanh Nghiên", Role: "Người dẫn đường", Tier: "important"},
	}); err != nil {
		t.Fatalf("Lưu nhân vật lõi: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "Văn bản chương 1, Lâm Mặc gặp chủ quán trọ Lão Chu và tiểu đồng A Vân."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	tool := newTestCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    1,
		"title":      "Chương 1",
		"summary":    "Lâm Mặc ở trọ",
		"characters": []string{"Lâm Mặc", "Lý Thanh Nghiên", "Lão Chu", "A Vân"},
		"key_events": []string{"Nhập trọ"},
		"cast_intros": []any{
			map[string]any{"name": "Lão Chu", "brief_role": "Chủ quán trọ"},
			map[string]any{"name": "A Vân", "brief_role": "Tiểu đồng của quán trọ"},
		},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi: %v", err)
	}
	summary, err := s.Summaries.LoadSummary(1)
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.Title != "Chương 1" {
		t.Fatalf("tiêu đề đã commit = %+v", summary)
	}

	entries, err := s.Cast.Load()
	if err != nil {
		t.Fatalf("Cast.Load: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("mong đợi 2 mục ledger (Lão Chu/A Vân), nhận được %d: %+v", len(entries), entries)
	}
	byName := map[string]domain.CastEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if e, ok := byName["Lão Chu"]; !ok || e.BriefRole != "Chủ quán trọ" || e.FirstSeenChapter != 1 {
		t.Errorf("mục của Lão Chu sai: %+v", e)
	}
	if e, ok := byName["A Vân"]; !ok || e.BriefRole != "Tiểu đồng của quán trọ" || e.AppearanceCount != 1 {
		t.Errorf("mục của A Vân sai: %+v", e)
	}
	if _, ok := byName["Lâm Mặc"]; ok {
		t.Errorf("nhân vật lõi Lâm Mặc không được vào ledger")
	}
	if _, ok := byName["Lý Thanh Nghiên"]; ok {
		t.Errorf("nhân vật lõi Lý Thanh Nghiên không được vào ledger")
	}
}

func TestCommitChapterReplayAfterPartialCommitDoesNotDuplicateWorldState(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "Văn bản chương 1, Lâm Mặc gặp bóng đen và đột phá."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}

	timeline := []domain.TimelineEvent{{
		Chapter:    1,
		Time:       "buổi sáng",
		Event:      "Lâm Mặc gặp bóng đen",
		Characters: []string{"Lâm Mặc"},
	}}
	stateChanges := []domain.StateChange{{
		Chapter:  1,
		Entity:   "Lâm Mặc",
		Field:    "realm",
		OldValue: "phàm nhân",
		NewValue: "luyện khí kỳ",
	}}
	foreshadow := []domain.ForeshadowUpdate{{
		ID:          "f1",
		Action:      "plant",
		Description: "thân phận bóng đen",
	}}

	// Mô phỏng commit_chapter đã ghi trạng thái thế giới, nhưng tiến trình bị sập trước khi MarkChapterComplete.
	if err := s.World.AppendTimelineEvents(timeline); err != nil {
		t.Fatalf("Bổ sung sự kiện dòng thời gian ban đầu: %v", err)
	}
	if err := s.World.AppendStateChanges(stateChanges); err != nil {
		t.Fatalf("Bổ sung thay đổi trạng thái ban đầu: %v", err)
	}
	if err := s.World.UpdateForeshadow(1, foreshadow); err != nil {
		t.Fatalf("Cập nhật manh mối ban đầu: %v", err)
	}
	persistedArgs, _ := json.Marshal(map[string]any{
		"chapter":            1,
		"title":              "Chương 1",
		"summary":            "Lâm Mặc gặp bóng đen và đột phá",
		"characters":         []string{"Lâm Mặc"},
		"key_events":         []string{"Gặp bóng đen", "Đột phá"},
		"timeline_events":    timeline,
		"state_changes":      stateChanges,
		"foreshadow_updates": foreshadow,
	})
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter:      1,
		Stage:        domain.CommitStageStarted,
		Summary:      "Tóm tắt nửa chừng",
		Payload:      persistedArgs,
		DraftContent: "Văn bản chương 1, Lâm Mặc gặp bóng đen và đột phá.",
	}); err != nil {
		t.Fatalf("Lưu commit chờ: %v", err)
	}
	if err := s.Drafts.SaveDraft(1, "Sau khi khởi động lại bị Worker mới ghi đè, tuyệt đối không được lẫn vào commit cũ."); err != nil {
		t.Fatalf("ghi đè bản nháp: %v", err)
	}

	tool := newTestCommitChapterTool(s)
	// Mô phỏng Writer sau khởi động lại đã tạo lại tham số khác; khôi phục phải bỏ qua nó và dùng persistedArgs.
	args, _ := json.Marshal(map[string]any{
		"chapter":         1,
		"title":           "Tiêu đề sai",
		"summary":         "Tóm tắt mới sai",
		"characters":      []string{"Lâm Mặc"},
		"key_events":      []string{"Sự kiện sai"},
		"timeline_events": []domain.TimelineEvent{{Time: "ban đêm", Event: "Sự kiện mới không được ghi"}},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi phát lại: %v", err)
	}

	events, _ := s.World.LoadTimeline()
	if len(events) != 1 {
		t.Fatalf("dòng thời gian bị nhân đôi sau khi phát lại, nhận được %d: %+v", len(events), events)
	}
	changes, _ := s.World.LoadStateChanges()
	if len(changes) != 1 {
		t.Fatalf("các thay đổi trạng thái bị nhân đôi sau khi phát lại, nhận được %d: %+v", len(changes), changes)
	}
	ledger, _ := s.World.LoadForeshadowLedger()
	if len(ledger) != 1 {
		t.Fatalf("manh mối bị nhân đôi sau khi phát lại, nhận được %d: %+v", len(ledger), ledger)
	}
	pending, _ := s.Signals.LoadPendingCommit()
	if pending != nil {
		t.Fatalf("commit chờ phải được xóa, nhận được %+v", pending)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil {
		t.Fatal("phải ghi checkpoint commit")
	}
	final, err := s.Drafts.LoadChapterText(1)
	if err != nil {
		t.Fatalf("Tải văn bản chương: %v", err)
	}
	if final != "Văn bản chương 1, Lâm Mặc gặp bóng đen và đột phá." {
		t.Fatalf("khôi phục đã dùng bản nháp bị ghi đè: %q", final)
	}
}

func TestCommitChapterRecoversProgressMarkedWindowWithExactOutput(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(2); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}
	if err := s.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("Cập nhật giai đoạn: %v", err)
	}
	if err := s.Progress.StartChapter(1); err != nil {
		t.Fatalf("Bắt đầu chương: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(1, "Văn bản cuối của chương 1"); err != nil {
		t.Fatalf("Lưu văn bản cuối của chương: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "Chương 1", Summary: "Tóm tắt"}); err != nil {
		t.Fatalf("Lưu tóm tắt: %v", err)
	}
	if _, err := s.ChapterRecords.Accept(1, domain.ChapterOriginGenerated, "Văn bản cuối của chương 1", domain.ChapterFacts{
		Title: "Chương 1", Summary: "Tóm tắt", KeyEvents: []string{"Sự kiện"},
	}, domain.StyleDelta{}); err != nil {
		t.Fatalf("Lưu bản ghi chương: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(1, 100, "mystery", "quest"); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương: %v", err)
	}

	want := json.RawMessage(`{"chapter":1,"committed":true,"recovered":"exact"}`)
	if err := s.Signals.SavePendingCommit(domain.PendingCommit{
		Chapter: 1,
		Stage:   domain.CommitStageProgressMarked,
		Output:  want,
	}); err != nil {
		t.Fatalf("Lưu commit chờ: %v", err)
	}

	tool := newTestCommitChapterTool(s)
	got, err := tool.Execute(context.Background(), json.RawMessage(`{"chapter":1}`))
	if err != nil {
		t.Fatalf("Thực thi khôi phục: %v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, got); err != nil {
		t.Fatalf("Rút gọn đầu ra khôi phục: %v", err)
	}
	if compact.String() != string(want) {
		t.Fatalf("đầu ra khôi phục = %s, mong đợi tài liệu chính xác %s", got, want)
	}
	if pending, err := s.Signals.LoadPendingCommit(); err != nil || pending != nil {
		t.Fatalf("commit chờ phải được xóa, pending=%+v err=%v", pending, err)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil {
		t.Fatal("phải sửa checkpoint commit")
	}
	p, err := s.Progress.Load()
	if err != nil {
		t.Fatalf("Tải tiến độ: %v", err)
	}
	if p.InProgressChapter != 0 {
		t.Fatalf("chương đang xử lý phải được xóa, nhận được %d", p.InProgressChapter)
	}
}

// TestCommitChapterRejectsPolishWithoutDraftChange xác minh: khi chương đã hoàn thành đi vào hàng đợi đánh bóng/viết lại,
// nếu nội dung và tiêu đề đều không đổi thì commit_chapter phải từ chối một lần viết lại rỗng.
// TestCommitChapterNonLayeredRecompletesAfterRework xác minh: với sách không phân lớp, sau khi hoàn tất rồi reopen để viết lại,
// khi sửa xong chương và commit, nếu hàng đợi đã rỗng thì có thể tự quay lại complete (nhánh không phân lớp sau khi drain xong).
func TestCommitChapterNonLayeredRecompletesAfterRework(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(2); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	// Hai chương đã viết xong và kết thúc. Chương 2 đã chuẩn bị sẵn drafts/chapters để nộp bản viết lại.
	ch1 := "Văn bản gốc của chương 1."
	ch2 := "Văn bản gốc của chương 2, dùng để mô phỏng bản cuối đã được nộp."
	if err := s.Drafts.SaveFinalChapter(1, ch1); err != nil {
		t.Fatalf("Lưu văn bản cuối của chương 1: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, ch2); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, ch2); err != nil {
		t.Fatalf("Lưu văn bản cuối của chương 2: %v", err)
	}
	saveTestChapterRecord(t, s, 1, ch1)
	saveTestChapterRecord(t, s, 2, ch2)
	if err := s.Progress.MarkChapterComplete(1, 100, "", ""); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương 1: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(ch2)), "", ""); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương 2: %v", err)
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("Đánh dấu hoàn tất: %v", err)
	}

	// reopen chương 2 → phase quay về writing, PendingRewrites=[2], flow=rewriting
	if err := s.Progress.Reopen([]int{2}, "sửa lại"); err != nil {
		t.Fatalf("Mở lại: %v", err)
	}

	// Nộp bản viết lại (bản nháp phải khác bản cuối thì mới được qua)
	if err := s.Drafts.SaveDraft(2, ch2+"\n\nĐoạn mới được thêm khi sửa lại."); err != nil {
		t.Fatalf("Lưu bản nháp (đã sửa): %v", err)
	}
	tool := newTestCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"title":      "Chương 2",
		"summary":    "Tóm tắt sau khi sửa",
		"characters": []string{"Nhân vật chính"},
		"key_events": []string{"Dọn dẹp"},
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi commit viết lại: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Giải mã JSON: %v", err)
	}
	if payload["book_complete"] != true {
		t.Errorf("book_complete = %v, muốn true", payload["book_complete"])
	}

	p, _ := s.Progress.Load()
	if p.Phase != domain.PhaseComplete {
		t.Errorf("phase = %s, muốn complete (phải tự động kết thúc lại)", p.Phase)
	}
	if len(p.PendingRewrites) != 0 {
		t.Errorf("PendingRewrites = %v, muốn rỗng", p.PendingRewrites)
	}
}

// TestCommitChapterLayeredReopenRecompletesDespiteOpenThread xác minh chốt hạ: với sách phân lớp sau khi reopen để viết lại,
// dù compass vẫn còn một số tuyến dài chưa khép (viết lại có thể làm xáo trộn), sau khi hàng đợi rỗng cũng sẽ được kết thúc lại
// theo kiểu "cấu trúc đã đầy đủ" — không bị kẹt ở writing, tránh vòng lặp viết tiếp vô hạn ở cuối tập (họ lỗi §6.5 / known_outline_exhaustion).
// Phản chứng: nếu đường reopen vẫn dùng layeredBookComplete ở mức chất lượng, trường hợp này open thread sẽ làm trả false,
// book_complete sẽ là giả, và test sẽ thất bại.
func TestCommitChapterLayeredReopenRecompletesDespiteOpenThread(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	// Một quyển, một cung, hai chương, tất cả đều đã triển khai
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Quyển 1", "theme": "Chủ đề",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung 1", "goal": "Mục tiêu",
				"chapters": []map[string]any{
					{"title": "Chương đầu", "core_event": "Khởi", "hook": "Tiếp"},
					{"title": "Chương sau", "core_event": "Thừa", "hook": "Kết"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Thực thi phân lớp: %v", err)
	}

	// Hai chương đã viết xong và kết thúc
	ch2 := "Văn bản gốc của chương 2, mô phỏng bản cuối đã được nộp."
	for ch, body := range map[int]string{1: "Nội dung chương 1.", 2: ch2} {
		if err := s.Drafts.SaveDraft(ch, body); err != nil {
			t.Fatalf("Lưu bản nháp %d: %v", ch, err)
		}
		if err := s.Drafts.SaveFinalChapter(ch, body); err != nil {
			t.Fatalf("Lưu văn bản cuối %d: %v", ch, err)
		}
		saveTestChapterRecord(t, s, ch, body)
		if err := s.Progress.MarkChapterComplete(ch, len([]rune(body)), "", ""); err != nil {
			t.Fatalf("Đánh dấu hoàn thành chương %d: %v", ch, err)
		}
	}
	if err := s.Progress.MarkComplete(); err != nil {
		t.Fatalf("Đánh dấu hoàn tất: %v", err)
	}

	// Mô phỏng "viết lại làm xáo trộn tuyến dài": compass vẫn còn một open thread chưa khép
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "Nhân vật chính trở về quê", OpenThreads: []string{"kẻ thù chưa bị loại bỏ"}}); err != nil {
		t.Fatalf("Lưu compass: %v", err)
	}

	// reopen chương 2 → nộp bản viết lại (bản nháp phải khác bản cuối thì mới được qua)
	if err := s.Progress.Reopen([]int{2}, "sửa lại"); err != nil {
		t.Fatalf("Mở lại: %v", err)
	}
	if err := s.Drafts.SaveDraft(2, ch2+"\n\nĐoạn mới được thêm khi sửa lại."); err != nil {
		t.Fatalf("Lưu bản nháp đã sửa: %v", err)
	}
	tool := newTestCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 2, "title": "Chương 2", "summary": "Tóm tắt sau sửa", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Dọn dẹp"},
	})
	raw, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Thực thi commit viết lại: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Giải mã JSON: %v", err)
	}
	if bc, _ := out["book_complete"].(bool); !bc {
		t.Error("sau khi reopen và dọn xong hàng đợi, sách phải được kết thúc lại theo cấu trúc đầy đủ dù tuyến dài chưa khép")
	}
	p, _ := s.Progress.Load()
	if p.Phase != domain.PhaseComplete {
		t.Errorf("phase = %s, muốn complete", p.Phase)
	}
	if p.ReopenedFromComplete {
		t.Error("sau khi kết thúc lại, ReopenedFromComplete phải được xóa")
	}
}

func TestCommitChapterRejectsPolishWithoutDraftChange(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	// Mô phỏng chương 2 đã hoàn thành bình thường: drafts và chapters có cùng nội dung.
	original := "Nội dung gốc của chương 2, dùng để mô phỏng bản cuối đã được nộp."
	if err := s.Drafts.SaveDraft(2, original); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	if err := s.Drafts.SaveFinalChapter(2, original); err != nil {
		t.Fatalf("Lưu văn bản cuối: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(original)), "mystery", "quest"); err != nil {
		t.Fatalf("Đánh dấu hoàn thành chương: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 2, Title: "Chương 2", Summary: "Tóm tắt gốc"}); err != nil {
		t.Fatalf("Lưu tóm tắt: %v", err)
	}

	// Vào hàng đợi đánh bóng: Flow=Polishing, PendingRewrites=[2]
	if err := s.Progress.SetPendingRewrites([]int{2}, "kiểm tra đánh bóng"); err != nil {
		t.Fatalf("Đặt các bản viết lại chờ xử lý: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatalf("Đặt luồng: %v", err)
	}

	tool := newTestCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"title":      "Chương 2",
		"summary":    "Giả vờ đã đánh bóng",
		"characters": []string{"Nhân vật chính"},
		"key_events": []string{"Không đổi"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("mong đợi commit bị từ chối khi drafts trùng với nội dung cuối")
	}

	// Viết lại một bản nháp khác → phải qua
	polished := original + "\n\nĐoạn mới được thêm sau khi đánh bóng."
	if err := s.Drafts.SaveDraft(2, polished); err != nil {
		t.Fatalf("Lưu bản nháp (đã đánh bóng): %v", err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi sau khi đánh bóng thật: %v", err)
	}
}

func TestCommitChapterAllowsTitleOnlyRewrite(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatal(err)
	}
	body := "Phần thân không cần sửa, chỉ tiêu đề cần được trau chuốt."
	if err := s.Drafts.SaveDraft(2, body); err != nil {
		t.Fatal(err)
	}
	if err := s.Drafts.SaveFinalChapter(2, body); err != nil {
		t.Fatal(err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 2, Title: "Tiêu đề cũ", Summary: "Tóm tắt gốc"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Checkpoints.AppendArtifacts(
		domain.ChapterScope(2), "commit", "chapters/02.md", "summaries/02.json",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.MarkChapterComplete(2, len([]rune(body)), "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetPendingRewrites([]int{2}, "tối ưu tiêu đề"); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetFlow(domain.FlowPolishing); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"chapter": 2, "title": "Tiêu đề mới chính xác hơn", "summary": "Tóm tắt gốc",
		"characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện có sẵn"},
	})
	if _, err := newTestCommitChapterTool(s).Execute(context.Background(), args); err != nil {
		t.Fatalf("viết lại chỉ tiêu đề thất bại: %v", err)
	}
	summary, err := s.Summaries.LoadSummary(2)
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil || summary.Title != "Tiêu đề mới chính xác hơn" {
		t.Fatalf("tiêu đề đã commit = %+v", summary)
	}
	after := s.Checkpoints.LatestByStep(domain.ChapterScope(2), "commit")
	if after == nil || after.Seq <= before.Seq {
		t.Fatalf("viết lại chỉ tiêu đề không tạo checkpoint mới: before=%+v after=%+v", before, after)
	}
	final, err := s.Drafts.LoadChapterText(2)
	if err != nil {
		t.Fatal(err)
	}
	if final != body {
		t.Fatalf("viết lại chỉ tiêu đề đã làm đổi phần thân: %q", final)
	}
}

// TestCommitChapterLayeredRejectsOutOfRangeChapter xác minh trong chế độ phân lớp,
// commit của chương vượt ra ngoài layered_outline phải thất bại cứng, thay vì chỉ slog.Warn rồi cho qua.
// Đây là phanh vật lý để ngăn "phán đoán sai rồi writer chạy trần" (trường hợp kiểu Phàm Cốt ch204..347).
func TestCommitChapterLayeredRejectsOutOfRangeChapter(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	// Dựng một layered_outline chỉ có 1 quyển 1 cung 1 chương
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Quyển 1", "theme": "Chủ đề",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung 1", "goal": "Mục tiêu",
				"chapters": []map[string]any{
					{"title": "Chương đầu", "core_event": "Khởi", "hook": "Tiếp"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Thực thi phân lớp: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	// Commit chương 2 vượt phạm vi phải thất bại cứng
	if err := s.Drafts.SaveDraft(2, "Văn bản chương vượt phạm vi, phải bị chặn."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	tool := newTestCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter":    2,
		"title":      "Chương 2",
		"summary":    "Chương vượt phạm vi",
		"characters": []string{"Nhân vật chính"},
		"key_events": []string{"Không nên được phép"},
	})
	_, err := tool.Execute(context.Background(), args)
	if err == nil {
		t.Fatal("mong đợi commit thất bại khi chương nằm ngoài phạm vi outline phân lớp")
	}

	// Tệp chương không được ghi xuống đĩa, Progress cũng không được tiến lên
	if _, statErr := os.Stat(dir + "/chapters/02.md"); !os.IsNotExist(statErr) {
		t.Fatalf("chương 2 không được lưu xuống đĩa, stat err=%v", statErr)
	}
	progress, _ := s.Progress.Load()
	if len(progress.CompletedChapters) != 0 {
		t.Fatalf("CompletedChapters phải vẫn rỗng, nhận được %v", progress.CompletedChapters)
	}
}

// TestCommitChapterLayeredAutoCompletesWhenDone xác minh cơ chế hoàn tất xác định trong chế độ phân lớp:
// khi toàn bộ outline đã triển khai và viết xong + không còn arc khung + không còn viết lại + số manh mối đang hoạt động bằng 0 + các tuyến dài trên compass đã khép,
// commit của chương cuối tự động đẩy Phase=Complete, không phụ thuộc kiến trúc sư gọi complete_book thủ công.
// Đây là bản sửa cho livelock sau khi 9bf26a5 xóa tự động hoàn tất của phân lớp (ở cuối tập, mô hình vừa không append vừa không complete → writer chạy trần, lặp vô hạn).
func TestCommitChapterLayeredAutoCompletesWhenDone(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	// Một quyển, một cung, hai chương, tất cả đều đã triển khai (không có arc khung)
	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Quyển 1", "theme": "Chủ đề",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung 1", "goal": "Mục tiêu",
				"chapters": []map[string]any{
					{"title": "Chương đầu", "core_event": "Khởi", "hook": "Tiếp"},
					{"title": "Chương sau", "core_event": "Thừa", "hook": "Kết"},
				},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Thực thi phân lớp: %v", err)
	}
	// Các tuyến dài trên compass đã khép (OpenThreads rỗng)
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "Nhân vật chính trở về quê"}); err != nil {
		t.Fatalf("Lưu compass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	tool := newTestCommitChapterTool(s)
	commit := func(ch int) map[string]any {
		if err := s.Drafts.SaveDraft(ch, fmt.Sprintf("Nội dung văn bản chương %d, dùng để kiểm tra hoàn tất xác định.", ch)); err != nil {
			t.Fatalf("Lưu bản nháp %d: %v", ch, err)
		}
		args, _ := json.Marshal(map[string]any{
			"chapter": ch, "title": fmt.Sprintf("Chương %d", ch), "summary": "Tóm tắt", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện"},
		})
		raw, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Thực thi chương %d: %v", ch, err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("Giải mã chương %d: %v", ch, err)
		}
		return out
	}

	// Chương 1: chưa viết xong, không được kết thúc
	if bc, _ := commit(1)["book_complete"].(bool); bc {
		t.Fatal("viết xong chương 1 không được kích hoạt kết thúc")
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("viết xong chương 1 thì phase không được là complete")
	}

	// Chương 2 (chương cuối): phải tự động kết thúc
	if bc, _ := commit(2)["book_complete"].(bool); !bc {
		t.Fatal("viết xong chương cuối phải tự động kết thúc")
	}
	if p, _ := s.Progress.Load(); p.Phase != domain.PhaseComplete {
		t.Fatalf("mong đợi phase=complete, nhận được %s", p.Phase)
	}
}

// TestCommitChapterFinaleVolumeCompletesDespiteOpenThreads xác minh toàn bộ đường kết thúc cho quyển finale:
// sau khi đã công bố quyển kết thúc (append_volume với final:true) —
//  1. commit của chương cuối không được tự kết thúc: việc hoàn tất không được chen lên trước bộ ba chốt cuối quyển
//     (review cung / tóm tắt cung / tóm tắt quyển), vì đoạn kết bắt buộc phải đi qua cổng chất lượng của editor;
//  2. khi bộ ba đã đủ, và tóm tắt quyển đã được ghi (điểm kích hoạt save_volume_summary) thì phải kết thúc,
//     không còn yêu cầu cả manh mối lẫn tuyến dài phải về 0 — nếu không, những sách bị ước lượng scale quá cao sẽ không bao giờ hợp lệ để hoàn thành.
//
// Đối chiếu với NoAutoCompleteWithOpenThreads bên dưới: cùng có tuyến dài chưa khép, nhưng chưa công bố thì không kết thúc,
// còn đã công bố thì phải kết thúc.
func TestCommitChapterFinaleVolumeCompletesDespiteOpenThreads(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Quyển 1", "theme": "Chủ đề",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung 1", "goal": "Mục tiêu",
				"chapters": []map[string]any{{"title": "Chương đầu", "core_event": "Khởi", "hook": "Tiếp"}},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Thực thi phân lớp: %v", err)
	}

	// Công bố quyển cuối ở cuối quyển: append_volume với final:true
	appendArgs, _ := json.Marshal(map[string]any{
		"type":   "append_volume",
		"reason": "Các tuyến dài có thể khép trong một quyển, công bố đây là quyển cuối",
		"content": map[string]any{
			"index": 2, "title": "Quyển cuối", "theme": "Khép lại", "final": true,
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung khép", "goal": "Thu hồi mọi tuyến dài",
				"chapters": []map[string]any{{"title": "Chương kết", "core_event": "Hợp", "hook": "Kết"}},
			}},
		},
	})
	raw, err := foundation.Execute(context.Background(), appendArgs)
	if err != nil {
		t.Fatalf("Thực thi append_volume: %v", err)
	}
	var appendOut map[string]any
	if err := json.Unmarshal(raw, &appendOut); err != nil {
		t.Fatalf("Giải mã kết quả append: %v", err)
	}
	if appendOut["final_volume"] != true {
		t.Fatalf("append_volume phải trả về sự thật final_volume=true, nhận được %v", appendOut)
	}

	// Tuyến dài chưa khép (khi chưa công bố quyển cuối, điều này sẽ ngăn kết thúc, xem test đối chiếu)
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "Nhân vật chính trở về quê", OpenThreads: []string{"kẻ thù chưa bị loại bỏ"}}); err != nil {
		t.Fatalf("Lưu compass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	tool := newTestCommitChapterTool(s)
	commit := func(ch int) map[string]any {
		if err := s.Drafts.SaveDraft(ch, fmt.Sprintf("Nội dung văn bản chương %d, dùng cho kiểm tra hoàn thành quyển cuối.", ch)); err != nil {
			t.Fatalf("Lưu bản nháp %d: %v", ch, err)
		}
		args, _ := json.Marshal(map[string]any{
			"chapter": ch, "title": fmt.Sprintf("Chương %d", ch), "summary": "Tóm tắt", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện"},
		})
		raw, err := tool.Execute(context.Background(), args)
		if err != nil {
			t.Fatalf("Thực thi chương %d: %v", ch, err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("Giải mã chương %d: %v", ch, err)
		}
		return out
	}

	// Chương 1 (không phải chương cuối của quyển cuối): không được kết thúc
	if bc, _ := commit(1)["book_complete"].(bool); bc {
		t.Fatal("quyển cuối chưa viết xong thì không được kết thúc")
	}
	// Các công phẩm tổng hợp của quyển trước phải hoàn thành trước, còn tóm tắt quyển ở cuối quyển mới là mục tiêu hiện tại của Router.
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "arc", Verdict: "accept", Summary: "Đánh giá quyển 1"}); err != nil {
		t.Fatalf("Lưu review v1: %v", err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Title: "Cung 1", Summary: "Hoàn thành", KeyEvents: []string{"Khởi"}}); err != nil {
		t.Fatalf("Lưu tóm tắt cung v1: %v", err)
	}
	if err := s.Summaries.SaveVolumeSummary(domain.VolumeSummary{Volume: 1, Title: "Quyển 1", Summary: "Hoàn thành", KeyEvents: []string{"Khởi"}}); err != nil {
		t.Fatalf("Lưu tóm tắt quyển v1: %v", err)
	}
	// Chương 2 (chương cuối của quyển cuối): khi bộ ba chốt cuối quyển chưa đủ, không được hoàn tất trước review/tóm tắt của editor
	if bc, _ := commit(2)["book_complete"].(bool); bc {
		t.Fatal("khi bộ ba ở cuối quyển chưa đủ thì không được kết thúc")
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("việc kết thúc không được xảy ra trước review và tóm tắt ở cuối quyển")
	}

	// Bộ ba chốt cuối quyển: sau khi review cung + tóm tắt cung được lưu, tóm tắt quyển (save_volume_summary) là điểm kích hoạt kết thúc
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "arc", Verdict: "accept", Summary: "Đánh giá cung cuối"}); err != nil {
		t.Fatalf("Lưu review: %v", err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 2, Arc: 1, Title: "Cung khép", Summary: "Khép lại", KeyEvents: []string{"Kết cục"}}); err != nil {
		t.Fatalf("Lưu tóm tắt cung: %v", err)
	}
	volTool := NewSaveVolumeSummaryTool(s)
	volArgs, _ := json.Marshal(map[string]any{
		"volume": 2, "title": "Quyển cuối", "summary": "Khép lại toàn bộ", "key_events": []string{"Kết cục"},
	})
	volRaw, err := volTool.Execute(context.Background(), volArgs)
	if err != nil {
		t.Fatalf("Thực thi lưu tóm tắt quyển: %v", err)
	}
	var volOut map[string]any
	if err := json.Unmarshal(volRaw, &volOut); err != nil {
		t.Fatalf("Giải mã kết quả tóm tắt quyển: %v", err)
	}
	if volOut["book_complete"] != true {
		t.Fatalf("việc lưu tóm tắt quyển phải kích hoạt hoàn tất và phản hồi book_complete, nhận được %v", volOut)
	}
	if p, _ := s.Progress.Load(); p.Phase != domain.PhaseComplete {
		t.Fatalf("mong đợi phase=complete, nhận được %s", p.Phase)
	}
}

// TestCommitChapterFinaleSkeletonArcBlocksCompletion xác minh cổng cấu trúc của việc kết thúc:
// khi quyển cuối vẫn còn arc khung (nội dung dự kiến chưa viết) thì dù bộ ba chốt đã đủ cũng không được kết thúc — đây là
// tuyến phòng thủ duy nhất để ngăn "kết thúc quá sớm" (điều kiện 2 của layeredStructurallyComplete).
func TestCommitChapterFinaleSkeletonArcBlocksCompletion(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	foundation := NewSaveFoundationTool(s)
	// Quyển cuối: cung đầu tiên triển khai 1 chương, cung thứ hai vẫn là khung
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Quyển cuối", "theme": "Khép lại", "final": true,
			"arcs": []map[string]any{
				{"index": 1, "title": "Cung khép", "goal": "Khép tuyến",
					"chapters": []map[string]any{{"title": "Chương đầu", "core_event": "Khởi", "hook": "Tiếp"}}},
				{"index": 2, "title": "Cung khung", "goal": "Chờ triển khai", "estimated_chapters": 5},
			},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Thực thi phân lớp: %v", err)
	}
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "Trở về quê"}); err != nil {
		t.Fatalf("Lưu compass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	tool := newTestCommitChapterTool(s)
	if err := s.Drafts.SaveDraft(1, "Văn bản chương 1."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "title": "Chương 1", "summary": "Tóm tắt", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện"},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi: %v", err)
	}
	// Dù bộ ba đã đủ cũng không được qua: arc khung có nghĩa là nội dung dự kiến vẫn chưa được viết
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 1, Scope: "arc", Verdict: "accept", Summary: "Đánh giá cung"}); err != nil {
		t.Fatalf("Lưu review: %v", err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Title: "Cung khép", Summary: "s", KeyEvents: []string{"e"}}); err != nil {
		t.Fatalf("Lưu tóm tắt cung: %v", err)
	}
	volTool := NewSaveVolumeSummaryTool(s)
	volArgs, _ := json.Marshal(map[string]any{
		"volume": 1, "title": "Quyển cuối", "summary": "s", "key_events": []string{"e"},
	})
	if _, err := volTool.Execute(context.Background(), volArgs); err == nil || !strings.Contains(err.Error(), "không có artifact volume_summary nào đang chờ xử lý") {
		t.Fatalf("khi arc khung chưa được triển khai thì quyển chưa kết thúc, tóm tắt quyển phải bị từ chối, nhận được %v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("arc khung chưa triển khai, phase không được là complete")
	}
}

// TestCommitChapterLayeredNoAutoCompleteWithOpenThreads xác minh tính thận trọng: khi vẫn còn tuyến dài đang hoạt động
// thì dù đã viết đủ chương cũng không tự động kết thúc, mà để quyền quyết định "có tiếp tục hay không" cho kiến trúc sư.

// TestCommitChapterLayeredNoAutoCompleteWithOpenThreads xác minh tính thận trọng: khi vẫn còn tuyến dài đang hoạt động
// thì dù đã viết đủ chương cũng không tự động kết thúc, mà để quyền quyết định "có tiếp tục hay không" cho kiến trúc sư.
func TestCommitChapterLayeredNoAutoCompleteWithOpenThreads(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	if err := s.Progress.Init(0); err != nil {
		t.Fatalf("Khởi tạo tiến độ: %v", err)
	}

	foundation := NewSaveFoundationTool(s)
	layeredArgs, _ := json.Marshal(map[string]any{
		"type": "layered_outline",
		"content": []map[string]any{{
			"index": 1, "title": "Quyển 1", "theme": "Chủ đề",
			"arcs": []map[string]any{{
				"index": 1, "title": "Cung 1", "goal": "Mục tiêu",
				"chapters": []map[string]any{{"title": "Chương đầu", "core_event": "Khởi", "hook": "Tiếp"}},
			}},
		}},
		"scale": "long",
	})
	if _, err := foundation.Execute(context.Background(), layeredArgs); err != nil {
		t.Fatalf("Thực thi phân lớp: %v", err)
	}
	// Vẫn còn tuyến dài đang hoạt động chưa khép
	if err := s.Outline.SaveCompass(domain.StoryCompass{EndingDirection: "Nhân vật chính trở về quê", OpenThreads: []string{"kẻ thù chưa bị loại bỏ"}}); err != nil {
		t.Fatalf("Lưu compass: %v", err)
	}
	_ = s.Progress.UpdatePhase(domain.PhaseWriting)

	if err := s.Drafts.SaveDraft(1, "Nội dung của chương duy nhất, nhưng tuyến dài chưa khép."); err != nil {
		t.Fatalf("Lưu bản nháp: %v", err)
	}
	tool := newTestCommitChapterTool(s)
	args, _ := json.Marshal(map[string]any{
		"chapter": 1, "title": "Chương 1", "summary": "Tóm tắt", "characters": []string{"Nhân vật chính"}, "key_events": []string{"Sự kiện"},
	})
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Thực thi: %v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseComplete {
		t.Fatal("khi tuyến dài đang hoạt động chưa khép thì không được tự động kết thúc")
	}
}
