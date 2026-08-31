package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestSaveReviewPersistsContractAssessment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	if !tool.StrictSchema() {
		t.Fatal("save_review must use strict schema")
	}
	if err := llmcontract.ValidateStrictReady(tool.Schema()); err != nil {
		t.Fatalf("save_review schema is not strict-ready: %v", err)
	}
	args, err := json.Marshal(map[string]any{
		"chapter":    3,
		"scope":      "chapter",
		"dimensions": []map[string]any{{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "Nhất quán cơ bản"}, {"dimension": "character", "score": 82, "verdict": "pass", "comment": "Tính cách ổn định"}, {"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "Hơi chậm"}, {"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "Mạch lạc"}, {"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "Bình thường"}, {"dimension": "hook", "score": 76, "verdict": "warning", "comment": "Móc câu bình thường"}, {"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "Ngôn ngữ cơ bản ổn"}},
		"issues": []map[string]any{{
			"type": "contract", "severity": "error", "description": "Thiếu mục contract", "evidence": "Chưa xuất hiện lời mời thử thách",
			"suggestion": "Bổ sung lời mời", "chapters": []int{3}, "requires_change": true,
		}},
		"contract_status": "partial",
		"contract_misses": []string{"Chưa cài rõ lời mời thử thách nội môn"},
		"contract_notes":  "Mạch chính đã tiến tới, nhưng mục thúc đẩy thứ hai trong contract chưa rơi xuống thành dữ kiện.",
		"verdict":         "polish",
		"summary":         "Chương này cơ bản đã hoàn thành mục tiêu, nhưng contract vẫn còn thiếu sót.",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	review, err := s.World.LoadReview(3)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review == nil {
		t.Fatal("expected review saved, got nil")
	}
	if review.ContractStatus != "partial" {
		t.Fatalf("unexpected contract status: %q", review.ContractStatus)
	}
	if len(review.ContractMisses) != 1 || review.ContractMisses[0] != "Chưa cài rõ lời mời thử thách nội môn" {
		t.Fatalf("unexpected contract misses: %+v", review.ContractMisses)
	}
	if review.Dimension("aesthetic") == nil {
		t.Fatalf("expected aesthetic dimension persisted, got %+v", review.Dimensions)
	}
}

func TestSaveReviewRejectsMissingDimensions(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter":    3,
		"scope":      "chapter",
		"dimensions": []map[string]any{},
		"issues":     []map[string]any{},
		"verdict":    "accept",
		"summary":    "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "dimensions phải chứa ít nhất một đánh giá dựa trên bằng chứng") {
		t.Fatalf("mong đợi lỗi kiểm tra dimensions, nhận được %v", err)
	}
}

func TestSaveReviewRejectsDimensionWithoutComment(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "Nhất quán cơ bản"},
			{"dimension": "character", "score": 82, "comment": "Tính cách ổn định"},
			{"dimension": "pacing", "score": 78},
			{"dimension": "continuity", "score": 84, "comment": "Mạch lạc"},
			{"dimension": "foreshadow", "score": 80, "comment": "Bình thường"},
			{"dimension": "hook", "score": 76, "comment": "Móc câu bình thường"},
			{"dimension": "aesthetic", "score": 81, "comment": "Ngôn ngữ cơ bản ổn"},
		},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "bình luận cho dimension là bắt buộc: pacing") {
		t.Fatalf("mong đợi lỗi kiểm tra bình luận dimension, nhận được %v", err)
	}
}

func TestSaveReviewRejectsIssueOutsideChapterScope(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(80); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	for ch := 1; ch <= 58; ch++ {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 58,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "Nhất quán cơ bản"},
			{"dimension": "character", "score": 82, "comment": "Tính cách ổn định"},
			{"dimension": "pacing", "score": 58, "comment": "Nhịp điệu cần viết lại"},
			{"dimension": "continuity", "score": 84, "comment": "Mạch lạc"},
			{"dimension": "foreshadow", "score": 80, "comment": "Bình thường"},
			{"dimension": "hook", "score": 76, "comment": "Móc câu bình thường"},
			{"dimension": "aesthetic", "score": 81, "comment": "Ngôn ngữ cơ bản ổn"},
		},
		"issues": []map[string]any{{
			"type": "pacing", "severity": "error", "description": "Vấn đề nhịp điệu", "evidence": "Chương 65",
			"suggestion": "Điều chỉnh", "chapters": []int{65}, "requires_change": true,
		}},
		"contract_status": "partial",
		"verdict":         "polish",
		"summary":         "Cần mài giũa chương 58, không được đưa chương chưa hoàn thành vào hàng đợi.",
		"contract_misses": []string{"Nhịp điệu vượt quá phạm vi của chương này"},
		"contract_notes":  "Chỉ nên xử lý các chương đã hoàn thành.",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "phải tham chiếu chương 58") {
		t.Fatalf("mong đợi từ chối chương bị ảnh hưởng ngoài phạm vi, nhận được %v", err)
	}
	review, err := s.World.LoadReview(58)
	if err != nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if review != nil {
		t.Fatalf("review should not be saved when pending rewrite validation fails: %+v", review)
	}
	p, _ := s.Progress.Load()
	if p.Flow != domain.FlowWriting && p.Flow != "" {
		t.Fatalf("flow should not enter rewrite/polish, got %s", p.Flow)
	}
	if len(p.PendingRewrites) != 0 {
		t.Fatalf("pending_rewrites should remain empty, got %v", p.PendingRewrites)
	}
}

// TestSaveReviewKeepsModelDefinedDimension xác nhận công cụ không còn đóng cứng trục đánh giá văn học và ngưỡng điểm
// trong Go; Editor có thể bổ sung các mặt đánh giá chính xác hơn theo nhiệm vụ hiện tại.
func TestSaveReviewKeepsModelDefinedDimension(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{{
			"dimension": "dialogue_subtext", "score": 85, "verdict": "warning", "comment": "Hàm ý vẫn có thể tăng cường",
		}},
		"issues":  []map[string]any{},
		"verdict": "accept",
		"summary": "ok",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("Execute should accept model-defined dimension, got %v", err)
	}

	review, err := s.World.LoadReview(3)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if d := review.Dimension("dialogue_subtext"); d == nil || d.Verdict != "warning" {
		t.Fatalf("model-defined assessment should be preserved, got %+v", d)
	}
}

func TestSaveReviewRejectsRewriteWithoutActionableIssue(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "Nhất quán cơ bản"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "Tính cách ổn định"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "Hơi chậm"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "Mạch lạc"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "Bình thường"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "Móc câu bình thường"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "Ngôn ngữ cơ bản ổn"},
		},
		"issues":  []map[string]any{},
		"verdict": "rewrite",
		"summary": "Cần viết lại",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "yêu cầu ít nhất một issue") {
		t.Fatalf("mong đợi lỗi kiểm tra issue có thể thực hiện, nhận được %v", err)
	}
}

func TestSaveReviewRejectsIssueWithoutEvidence(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 3,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "Nhất quán cơ bản"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "Tính cách ổn định"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "Hơi chậm"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "Mạch lạc"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "Bình thường"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "Móc câu bình thường"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "Ngôn ngữ cơ bản ổn"},
		},
		"issues": []map[string]any{
			{"type": "hook", "severity": "warning", "description": "Móc câu cuối chương còn yếu"},
		},
		"verdict": "polish",
		"summary": "Cần tăng cường móc câu.",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "bằng chứng issue là bắt buộc") {
		t.Fatalf("mong đợi lỗi kiểm tra bằng chứng issue, nhận được %v", err)
	}
}

// TestSaveReviewDoesNotDirtyQueueOnIllegalFlowTransition phòng hồi quy: giữa chừng dọn sạch hàng đợi sửa lại
// (Flow=rewriting, PendingRewrites=[8,9]) khi đánh giá lại một chương đã viết lại và nhận polish,
// Flow=polishing và rewriting tạo thành chuyển trạng thái không hợp lệ. ApplyReviewOutcome phải trong cùng một khóa ghi
// hoàn tất kiểm tra và ghi, khi chuyển trạng thái không hợp lệ thì hàng đợi giữ nguyên.
func TestSaveReviewDoesNotDirtyQueueOnIllegalFlowTransition(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(10); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	for _, ch := range []int{8, 9} {
		if err := s.Progress.MarkChapterComplete(ch, 3000, "", ""); err != nil {
			t.Fatalf("MarkChapterComplete(%d): %v", ch, err)
		}
	}
	if err := s.Progress.SetPendingRewrites([]int{8, 9}, "Sửa lại"); err != nil {
		t.Fatalf("SetPendingRewrites: %v", err)
	}
	if err := s.Progress.SetFlow(domain.FlowRewriting); err != nil {
		t.Fatalf("SetFlow rewriting: %v", err)
	}

	tool := NewSaveReviewTool(s)
	args, err := json.Marshal(map[string]any{
		"chapter": 8,
		"scope":   "chapter",
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "verdict": "pass", "comment": "Nhất quán cơ bản"},
			{"dimension": "character", "score": 82, "verdict": "pass", "comment": "Tính cách ổn định"},
			{"dimension": "pacing", "score": 78, "verdict": "warning", "comment": "Hơi chậm"},
			{"dimension": "continuity", "score": 84, "verdict": "pass", "comment": "Mạch lạc"},
			{"dimension": "foreshadow", "score": 80, "verdict": "pass", "comment": "Bình thường"},
			{"dimension": "hook", "score": 76, "verdict": "warning", "comment": "Móc câu bình thường"},
			{"dimension": "aesthetic", "score": 81, "verdict": "pass", "comment": "Ngôn ngữ cơ bản ổn"},
		},
		"issues": []map[string]any{{
			"type": "contract", "severity": "error", "description": "Thiếu sót", "evidence": "Contract chưa hoàn thành",
			"suggestion": "Bổ sung đầy đủ", "chapters": []int{8}, "requires_change": true,
		}},
		"contract_status": "partial",
		"contract_misses": []string{"Thiếu sót"},
		"contract_notes":  "Đánh giá lại vẫn còn thiếu sót.",
		"verdict":         "polish",
		"summary":         "Chương 8 đánh giá lại cần mài giũa.",
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "apply review outcome") {
		t.Fatalf("expected illegal flow transition error, got %v", err)
	}

	p, _ := s.Progress.Load()
	if len(p.PendingRewrites) != 2 || p.PendingRewrites[0] != 8 || p.PendingRewrites[1] != 9 {
		t.Fatalf("PendingRewrites không được bị ghi bẩn, mong đợi [8 9], got %v", p.PendingRewrites)
	}
	if p.Flow != domain.FlowRewriting {
		t.Fatalf("Flow phải giữ nguyên rewriting, got %s", p.Flow)
	}
}

func TestSaveReviewKeepsOutcomeWhenReviewArtifactWriteFails(t *testing.T) {
	dir := t.TempDir()
	s := store.NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := s.Progress.Init(3); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := s.Progress.MarkChapterComplete(3, 3000, "", ""); err != nil {
		t.Fatalf("MarkChapterComplete: %v", err)
	}
	// Để đường dẫn tệp mục tiêu trở thành thư mục, ổn định kích hoạt lỗi rename nguyên tử.
	if err := os.MkdirAll(filepath.Join(dir, "reviews", "03.json"), 0o755); err != nil {
		t.Fatal(err)
	}

	args, err := json.Marshal(map[string]any{
		"chapter": 3, "scope": "chapter", "verdict": "polish", "summary": "Cần bổ sung liên kết",
		"issues": []map[string]any{{
			"type": "continuity", "severity": "error", "description": "Thiếu liên kết", "evidence": "Đầu chương thiếu phần nối",
			"suggestion": "Bổ sung liên kết", "chapters": []int{3}, "requires_change": true,
		}},
		"dimensions": []map[string]any{
			{"dimension": "consistency", "score": 85, "comment": "Nhất quán"},
			{"dimension": "character", "score": 82, "comment": "Ổn định"},
			{"dimension": "pacing", "score": 78, "comment": "Hơi nhanh"},
			{"dimension": "continuity", "score": 84, "comment": "Mạch lạc"},
			{"dimension": "foreshadow", "score": 80, "comment": "Bình thường"},
			{"dimension": "hook", "score": 76, "comment": "Có thể tăng cường"},
			{"dimension": "aesthetic", "score": 81, "comment": "Ngôn ngữ ổn"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewSaveReviewTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "save review") {
		t.Fatalf("expected review write failure, got %v", err)
	}

	p, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if p.Flow != domain.FlowPolishing || len(p.PendingRewrites) != 1 || p.PendingRewrites[0] != 3 {
		t.Fatalf("Sau khi công cụ đánh giá thất bại, ý định sửa lại phải vẫn có thể khôi phục, got flow=%s queue=%v", p.Flow, p.PendingRewrites)
	}
}

func setupArcReviewStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, Chapters: []domain.OutlineEntry{{Title: "Ba"}, {Title: "Bốn"}}},
		},
	}}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 4; chapter++ {
		if err := s.Progress.MarkChapterComplete(chapter, 100, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	// Cung một đã khép lại hoàn chỉnh, vì vậy công cụ duy nhất Router còn chờ là đánh giá cung hai.
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 2, Scope: "arc", Verdict: "accept", Summary: "Đánh giá cung một"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Summaries.SaveArcSummary(domain.ArcSummary{Volume: 1, Arc: 1, Title: "Cung một", Summary: "Hoàn tất", KeyEvents: []string{"Sự kiện"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func arcReviewArgs(t *testing.T, issueChapter int) []byte {
	t.Helper()
	args, err := json.Marshal(map[string]any{
		"chapter": 4,
		"scope":   "arc",
		"dimensions": []map[string]any{{
			"dimension": "pacing", "score": 70, "comment": "Nhịp điệu chương ba còn lê thê",
		}},
		"issues": []map[string]any{{
			"type": "pacing", "severity": "error", "description": "Xung đột vào quá muộn", "evidence": "Nửa đầu chương 3 không có tiến triển",
			"suggestion": "Rút gọn phần dàn trải", "chapters": []int{issueChapter}, "requires_change": true,
		}},
		"verdict": "polish",
		"summary": "Cung hai cần rút gọn một đoạn dàn trải",
	})
	if err != nil {
		t.Fatal(err)
	}
	return args
}

func TestSaveReviewRejectsIssueOutsideArcSpan(t *testing.T) {
	s := setupArcReviewStore(t)
	if _, err := NewSaveReviewTool(s).Execute(context.Background(), arcReviewArgs(t, 2)); err == nil || !strings.Contains(err.Error(), "nằm ngoài phạm vi 3-4") {
		t.Fatalf("mong đợi từ chối phạm vi cung, nhận được %v", err)
	}
	if p, _ := s.Progress.Load(); len(p.PendingRewrites) != 0 {
		t.Fatalf("invalid review must not enqueue rewrites: %v", p.PendingRewrites)
	}
}

func TestSaveReviewDerivesAffectedChaptersFromIssues(t *testing.T) {
	s := setupArcReviewStore(t)
	if _, err := NewSaveReviewTool(s).Execute(context.Background(), arcReviewArgs(t, 3)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	review, err := s.World.LoadReview(4)
	if err != nil || review == nil {
		t.Fatalf("LoadReview: %v", err)
	}
	if !slices.Equal(review.AffectedChapters, []int{3}) {
		t.Fatalf("affected chapters must be derived from issues, got %v", review.AffectedChapters)
	}
	if p, _ := s.Progress.Load(); !slices.Equal(p.PendingRewrites, []int{3}) {
		t.Fatalf("rewrite queue = %v, want [3]", p.PendingRewrites)
	}
}

func TestSaveReviewRetriesCheckpointWithoutReapplyingOutcome(t *testing.T) {
	s := setupArcReviewStore(t)
	checkpointPath := filepath.Join(s.Dir(), "meta", "checkpoints.jsonl")
	if err := os.MkdirAll(checkpointPath, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewSaveReviewTool(s)
	args := arcReviewArgs(t, 3)

	if _, err := tool.Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "checkpoint review") {
		t.Fatalf("expected checkpoint failure, got %v", err)
	}
	if err := os.Remove(checkpointPath); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), args); err != nil {
		t.Fatalf("idempotent checkpoint retry: %v", err)
	}
	progress, err := s.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(progress.PendingRewrites, []int{3}) {
		t.Fatalf("review outcome must not be duplicated or changed, queue=%v", progress.PendingRewrites)
	}
	if cp := s.Checkpoints.LatestByStep(domain.ArcScope(1, 2), "review"); cp == nil {
		t.Fatal("checkpoint retry must finish the review write")
	}
}

func TestSaveArcReviewCanReplaceChapterReviewAtEndpoint(t *testing.T) {
	s := setupArcReviewStore(t)
	if err := s.World.SaveReview(domain.ReviewEntry{Chapter: 4, Scope: "chapter", Verdict: "accept", Summary: "Đánh giá đơn chương cuối"}); err != nil {
		t.Fatal(err)
	}

	if _, err := NewSaveReviewTool(s).Execute(context.Background(), arcReviewArgs(t, 3)); err != nil {
		t.Fatalf("arc review should replace chapter-scope artifact at the same endpoint: %v", err)
	}
	review, err := s.World.LoadReview(4)
	if err != nil {
		t.Fatal(err)
	}
	if review == nil || review.Scope != "arc" {
		t.Fatalf("stored review = %+v, want scope=arc", review)
	}
}

func TestSaveReviewRejectsFutureArc(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(4); err != nil {
		t.Fatal(err)
	}
	volumes := []domain.VolumeOutline{{
		Index: 1,
		Arcs: []domain.ArcOutline{
			{Index: 1, Chapters: []domain.OutlineEntry{{Title: "Một"}, {Title: "Hai"}}},
			{Index: 2, Chapters: []domain.OutlineEntry{{Title: "Ba"}, {Title: "Bốn"}}},
		},
	}}
	if err := s.Outline.SaveLayeredOutline(volumes); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline(domain.FlattenOutline(volumes)); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.SetLayered(true); err != nil {
		t.Fatal(err)
	}
	for chapter := 1; chapter <= 2; chapter++ {
		if err := s.Progress.MarkChapterComplete(chapter, 100, "", ""); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := NewSaveReviewTool(s).Execute(context.Background(), arcReviewArgs(t, 3)); err == nil || !strings.Contains(err.Error(), "review chapter 4 must be completed") {
		t.Fatalf("mong đợi từ chối cung tương lai, nhận được %v", err)
	}
	if review, err := s.World.LoadReview(4); err != nil || review != nil {
		t.Fatalf("future review must not be persisted, review=%+v err=%v", review, err)
	}
}
