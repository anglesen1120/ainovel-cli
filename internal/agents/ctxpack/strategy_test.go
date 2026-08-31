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
		agentcore.UserMsg(strings.Repeat("ngữ cảnh cũ ", 200)),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("phản hồi cũ ", 200))},
		},
		agentcore.UserMsg("Tiếp tục viết chương ba, chú ý nối tiếp đoạn kết chương hai."),
		agentcore.Message{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock("Đã rõ, tôi sẽ sắp xếp cảnh hiện tại trước.")},
		},
	}

	out, result, err := strategy.Apply(context.Background(), msgs, msgs, corecontext.Budget{
		Tokens:    corecontext.EstimateTotal(msgs),
		Window:    128,
		Threshold: 32,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied {
		t.Fatal("chiến lược tóm tắt kho dữ liệu phải được áp dụng")
	}
	if result.Name != storeSummaryStrategyName {
		t.Fatalf("tên chiến lược không đúng: %q", result.Name)
	}
	if len(out) < 2 {
		t.Fatalf("cần bản tóm tắt cùng các tin nhắn được giữ lại, nhận %d", len(out))
	}
	summary, ok := out[0].(corecontext.ContextSummary)
	if !ok {
		t.Fatalf("cần ContextSummary, nhận %T", out[0])
	}
	if !strings.Contains(summary.Summary, "Tóm tắt chương gần đây") {
		t.Fatalf("cần các bản tóm tắt lâu dài trong checkpoint, nhận %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "Kế hoạch chương hiện tại") {
		t.Fatalf("cần kế hoạch chương trong checkpoint, nhận %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "Foreshadowing đang hoạt động") {
		t.Fatalf("cần dữ liệu foreshadow trong checkpoint, nhận %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "Vấn đề rà soát chờ sửa") {
		t.Fatalf("cần mục rà soát đang chờ trong checkpoint, nhận %q", summary.Summary)
	}
	if !strings.Contains(summary.Summary, "Manh mối nhà kho") {
		t.Fatalf("cần chi tiết rà soát đang chờ trong checkpoint, nhận %q", summary.Summary)
	}
	if result.Info == nil || result.Info.CompactedCount <= 0 {
		t.Fatalf("cần thông tin nén, nhận %+v", result.Info)
	}
}

func TestWriterRestoreIncludesOptionalDataWarnings(t *testing.T) {
	s := seededWriterStore(t)
	if err := os.WriteFile(filepath.Join(s.Dir(), "meta", "style_rules.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	text, ok, err := buildWriterRestoreText(s, restoreBudgetTokens)
	if err != nil {
		t.Fatalf("Dữ liệu phụ bị hỏng không được ngăn khôi phục ngữ cảnh: %v", err)
	}
	if !ok || !strings.Contains(text, "Cảnh báo dữ liệu") || !strings.Contains(text, "style_rules") {
		t.Fatalf("Ngữ cảnh khôi phục phải hiển thị cảnh báo đọc cho mô hình: %q", text)
	}
}

func TestStoreSummaryCompactApplyFallsBackWhenStoreDataInsufficient(t *testing.T) {
	dir := t.TempDir()
	s := storepkg.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    1,
		TotalChapters:     3,
		CompletedChapters: nil,
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
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
		t.Fatalf("Apply: %v", err)
	}
	if result.Applied {
		t.Fatal("không được áp dụng khi bộ nhớ lâu dài không đủ")
	}
	if len(out) != len(msgs) {
		t.Fatalf("tin nhắn phải giữ nguyên, nhận %d", len(out))
	}
}

func TestWriterRestorePackRefreshReusesStoreBuilder(t *testing.T) {
	s := seededWriterStore(t)
	pack := &WriterRestorePack{}
	pack.Refresh(s)

	msg, ok, err := pack.buildMessage(restoreBudgetTokens)
	if err != nil {
		t.Fatalf("buildMessage: %v", err)
	}
	if !ok {
		t.Fatal("cần tin nhắn gói khôi phục")
	}
	text := msg.TextContent()
	if !strings.Contains(text, "<post-compact-context>") {
		t.Fatalf("cần ngữ cảnh khôi phục được bao bọc, nhận %q", text)
	}
	if !strings.Contains(text, "Vấn đề rà soát chờ sửa") {
		t.Fatalf("cần mục rà soát đang chờ, nhận %q", text)
	}
	if !strings.Contains(text, "Kế hoạch chương hiện tại") {
		t.Fatalf("cần mục kế hoạch chương, nhận %q", text)
	}

	if _, _, err := pack.buildMessage(0); err == nil {
		t.Fatal("cần lỗi rõ ràng khi gói khôi phục không vừa")
	}
}

func seededWriterStore(t *testing.T) *storepkg.Store {
	t.Helper()

	s := storepkg.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Save(&domain.Progress{
		Phase:             domain.PhaseWriting,
		CurrentChapter:    3,
		TotalChapters:     6,
		CompletedChapters: []int{1, 2},
		Flow:              domain.FlowWriting,
	}); err != nil {
		t.Fatalf("Save progress: %v", err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{
		{Chapter: 1, Title: "Chương một", CoreEvent: "Mở đầu"},
		{Chapter: 2, Title: "Chương hai", CoreEvent: "Xung đột leo thang"},
		{Chapter: 3, Title: "Chương ba", CoreEvent: "Truy tìm manh mối", Scenes: []string{"Nhân vật chính điều tra vụ mất tích", "Phát hiện manh mối nhà kho cũ"}},
	}); err != nil {
		t.Fatalf("SaveOutline: %v", err)
	}
	if err := s.Drafts.SaveChapterPlan(domain.ChapterPlan{
		Chapter:    3,
		Title:      "Chương ba",
		Goal:       "Đẩy tiến điều tra vụ mất tích",
		Conflict:   "Nhân vật chính và cộng sự bất đồng về hướng điều tra",
		Hook:       "Phát hiện bản ghi âm đáng ngờ trong nhà kho",
		EmotionArc: "từ nghi ngờ tới căng thẳng",
	}); err != nil {
		t.Fatalf("SaveChapterPlan: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    1,
		Summary:    "Nhân vật chính nhận ủy thác, phát hiện vụ mất tích không đơn giản.",
		Characters: []string{"Lâm Lam", "Chu Sách"},
		KeyEvents:  []string{"Ủy thác được xác lập"},
	}); err != nil {
		t.Fatalf("SaveSummary 1: %v", err)
	}
	if err := s.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    2,
		Summary:    "Hai người điều tra bến tàu cũ, manh mối chỉ tới nhà kho bỏ hoang.",
		Characters: []string{"Lâm Lam", "Chu Sách", "Chú Thẩm"},
		KeyEvents:  []string{"Xung đột ở bến tàu cũ", "Manh mối nhà kho xuất hiện"},
	}); err != nil {
		t.Fatalf("SaveSummary 2: %v", err)
	}
	if err := s.World.SaveForeshadowLedger([]domain.ForeshadowEntry{
		{ID: "tape", Description: "Cuộn băng ghi âm người mất tích để lại", PlantedAt: 2, Status: "planted"},
	}); err != nil {
		t.Fatalf("SaveForeshadowLedger: %v", err)
	}
	if err := s.World.SaveTimeline([]domain.TimelineEvent{
		{Chapter: 2, Time: "ban đêm", Event: "Đối đầu ở bến tàu cũ", Characters: []string{"Lâm Lam", "Chu Sách"}},
	}); err != nil {
		t.Fatalf("SaveTimeline: %v", err)
	}
	if err := s.World.SaveStyleRules(domain.WritingStyleRules{
		Prose:  []string{"Câu hơi ngắn, giữ cảm giác áp bức"},
		Taboos: []string{"Tránh giải thích bí ẩn quá trực diện"},
	}); err != nil {
		t.Fatalf("SaveStyleRules: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "chapter",
		Verdict: "polish",
		Summary: "Phần cài cắm cuối chương hai hơi gấp, cần thêm một nhịp áp bức trước nhà kho.",
		Issues: []domain.ConsistencyIssue{
			{
				Type:        "pacing",
				Severity:    "warning",
				Description: "Manh mối nhà kho xuất hiện quá nhanh, phần trinh thám chưa đủ nén áp lực.",
				Suggestion:  "Thêm một đoạn do dự và miêu tả áp lực môi trường trước khi vào nhà kho.",
			},
		},
		ContractMisses: []string{"Hook cuối chương chưa đủ mạnh"},
	}); err != nil {
		t.Fatalf("Save chapter review: %v", err)
	}
	if err := s.World.SaveReview(domain.ReviewEntry{
		Chapter: 2,
		Scope:   "global",
		Verdict: "polish",
		Summary: "Nhịp đoạn kết chương hai hơi nhanh, manh mối nhà kho cần được nén thêm một nhịp.",
	}); err != nil {
		t.Fatalf("SaveReview: %v", err)
	}
	return s
}
