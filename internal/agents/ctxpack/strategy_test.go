package ctxpack

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

func TestStoreSummaryCompactApplyUsesPersistentStoreData(t *testing.T) {
	s := seededWriterStore(t)
	strategy := NewStoreSummaryCompact(StoreSummaryCompactConfig{
		Store:              s,
		KeepRecentTokens:   80,
		SummaryTokenBudget: 2000,
	})

	msgs := []agentcore.AgentMessage{
		agentcore.UserMsg(strings.Repeat("ngữ cảnh cũ", 400)),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("phản hồi cũ", 400))},
		},
		agentcore.UserMsg("tiếp tục viết chương ba, chú ý nối tiếp phần cuối chương hai."),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock("Đã rõ, trước hết tôi sẽ hệ thống tình huống hiện tại.")},
		},
	}

	out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{
		Tokens:    corecontext.EstimateTotal(msgs),
		Window:    128,
		Threshold: 32,
	})
	if err != nil {
		t.Fatalf("Apply thất bại: %v", err)
	}
	if !result.Applied {
		t.Fatal("phải áp dụng chiến lược tóm tắt store")
	}
	if result.Name != storeSummaryStrategyName {
		t.Fatalf("tên chiến lược bất thường: %q", result.Name)
	}
	if len(out) < 2 {
		t.Fatalf("phải có tóm tắt và tin nhắn được giữ lại, nhận được %d", len(out))
	}
	summary, ok := out[0].(corecontext.ContextSummary)
	if !ok {
		t.Fatalf("phải là ContextSummary, nhận được %T", out[0])
	}
	if !strings.Contains(summary.Summary, "Tóm tắt chương gần đây") {
		t.Fatalf("điểm kiểm tra phải chứa tóm tắt bền vững, nhận được %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "Kế hoạch chương hiện tại") {
		t.Fatalf("điểm kiểm tra phải chứa kế hoạch chương, nhận được %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "Chi tiết gợi mở đang hoạt động") {
		t.Fatalf("điểm kiểm tra phải chứa dữ liệu điềm báo, nhận được %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "Vấn đề bản thảo cần sửa") {
		t.Fatalf("điểm kiểm tra phải chứa vấn đề biên tập cần sửa, nhận được %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "manh mối nhà kho cần thêm một nhịp tích áp lực") {
		t.Fatalf("điểm kiểm tra phải chứa chi tiết biên tập cần sửa, nhận được %q", summary.Summary)
	}
	if result.Info == nil || result.Info.CompactedCount <= 0 {
		t.Fatalf("phải có thông tin nén, nhận được %+v", result.Info)
	}
}

func TestWriterRestoreIncludesOptionalDataWarnings(t *testing.T) {
	s := seededWriterStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "style_rules.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	text, ok, err := buildWriterRestoreText(s, restoreBudgetTokens)
	if err != nil {
		t.Fatalf("dữ liệu phụ hỏng không được cản trở khôi phục ngữ cảnh: %v", err)
	}
	if !ok || !strings.Contains(text, "Cảnh báo dữ liệu") || !strings.Contains(text, "style_rules") {
		t.Fatalf("ngữ cảnh khôi phục phải cung cấp cảnh báo đọc cho mô hình: %q", text)
	}
}

func TestStoreSummaryCompactApplyFallsBackWhenStoreDataInsufficient(t *testing.T) {
	dir := t.TempDir()
	s := storepkg.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("khởi tạo store: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    1,
		TotalChapters:     3,
		CompletedChapters: nil,
	}); err != nil {
		t.Fatalf("lưu tiến độ: %v", err)
	}

	strategy := NewStoreSummaryCompact(StoreSummaryCompactConfig{Store: s, KeepRecentTokens: 20})
	msgs := []agentcore.AgentMessage{
		agentcore.UserMsg(strings.Repeat("ngữ cảnh cũ", 40)),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("phản hồi cũ", 40))},
		},
	}

	out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{
		Tokens:    corecontext.EstimateTotal(msgs),
		Window:    64,
		Threshold: 16,
	})
	if err != nil {
		t.Fatalf("Apply thất bại: %v", err)
	}
	if result.Applied {
		t.Fatal("khi bộ nhớ bền vững không đủ thì không được thực hiện thao tác")
	}
	if len(out) != len(msgs) {
		t.Fatalf("tin nhắn phải được giữ nguyên, nhận được %d", len(out))
	}
}

func TestWriterRestorePackRefreshReusesStoreBuilder(t *testing.T) {
	s := seededWriterStore(t)
	pack := &WriterRestorePack{}
	pack.Refresh(s)

	msg, ok, err := pack.buildMessage(restoreBudgetTokens)
	if err != nil {
		t.Fatalf("buildMessage thất bại: %v", err)
	}
	if !ok {
		t.Fatal("phải có tin nhắn gói khôi phục")
	}
	text := msg.TextContent()
	if !strings.Contains(text, "<post-compact-context>") {
		t.Fatalf("phải có ngữ cảnh khôi phục đã đóng gói, nhận được %q", text)
	}
	if !strings.Contains(text, "Vấn đề bản thảo cần sửa") {
		t.Fatalf("phải có vấn đề biên tập cần sửa , nhận được %q", text)
	}
	if !strings.Contains(text, "Kế hoạch chương hiện tại") {
		t.Fatalf("phải có kế hoạch chương , nhận được %q", text)
	}

	if _, _, err := pack.buildMessage(0); err == nil {
		t.Fatal("khi gói khôi phục không vừa phải trả về lỗi rõ ràng")
	}
}

func seededWriterStore(t *testing.T) *storepkg.Store {
	t.Helper()

	s := storepkg.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("khởi tạo store: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    3,
		TotalChapters:     6,
		CompletedChapters: []int{1, 2},
		Flow:              domain.FlowWriting,
	}); err != nil {
		t.Fatalf("lưu tiến độ: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Chương một", CoreEvent: "mở đầu"},
		{Chapter: 2, Title: "Chương hai", CoreEvent: "xung đột leo thang"},
		{Chapter: 3, Title: "Chương ba", CoreEvent: "truy tìm manh mối", Scenes: []string{"nhân vật chính điều tra vụ mất tích", "phát hiện manh mối về nhà kho cũ"}},
	}); err != nil {
		t.Fatalf("lưu dàn ý: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter:    3,
		Title:      "Chương ba",
		Goal:       "thúc đẩy điều tra vụ mất tích",
		Conflict:   "nhân vật chính và cộng sự bất đồng hướng điều tra",
		Hook:       "phát hiện bản ghi âm đáng ngờ trong nhà kho",
		EmotionArc: "từ nghi ngờ đến căng thẳng",
	}); err != nil {
		t.Fatalf("lưu kế hoạch chương: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "nhân vật chính nhận ủy thác và phát hiện vụ mất tích không đơn giản.",
		Characters: []string{"Lâm Lam", "Chu Sách"},
		KeyEvents:  []string{"ủy thác được xác lập"},
	}); err != nil {
		t.Fatalf("lưu tóm tắt 1: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    2,
		Summary:    "hai người điều tra bến tàu cũ, manh mối chỉ đến nhà kho bỏ hoang.",
		Characters: []string{"Lâm Lam", "Chu Sách", "chú Thẩm"},
		KeyEvents:  []string{"xung đột ở bến tàu cũ", "manh mối nhà kho xuất hiện"},
	}); err != nil {
		t.Fatalf("lưu tóm tắt 2: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "tape", Description: "băng ghi âm người mất tích để lại", PlantedAt: 2, Status: "planted"},
	}); err != nil {
		t.Fatalf("lưu sổ điềm báo: %v", err)
	}
	if err := s.World.SaveTimeline([]domain.TimelineEvent{
		{Chapter: 2, Time: "ban đêm", Event: "đụng độ ở bến tàu cũ", Characters: []string{"Lâm Lam", "Chu Sách"}},
	}); err != nil {
		t.Fatalf("lưu dòng thời gian: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Prose:  []string{"câu ngắn, duy trì cảm giác áp lực"},
		Taboos: []string{"tránh giải thích bí ẩn quá trực tiếp"},
	}); err != nil {
		t.Fatalf("lưu quy tắc phong cách: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "polish",
		Summary: "phần chuẩn bị cuối chương hai hơi gấp, cần thêm một nhịp áp lực trước nhà kho.",
		Issues: []domain.ConsistencyIssue{
			{
				Type:        "pacing",
				Severity:    "warning",
				Description: "manh mối nhà kho xuất hiện quá nhanh, chưa tích đủ áp lực hồi hộp.",
				Suggestion:  "thêm đoạn do dự và miêu tả áp lực môi trường trước khi vào nhà kho.",
			},
		},
		ContractMisses: []string{"móc cuối chương chưa đủ mạnh"},
	}); err != nil {
		t.Fatalf("lưu biên tập chương: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "global",
		Verdict: "polish",
		Summary: "nhịp cuối chương hai hơi nhanh, manh mối nhà kho cần thêm một nhịp tích áp lực.",
	}); err != nil {
		t.Fatalf("lưu biên tập: %v", err)
	}
	return s
}
