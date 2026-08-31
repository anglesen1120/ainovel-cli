package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

func completeShortFoundation(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: "Bài kiểm tra thẩm định", Synopsis: "Lâm Chu tìm đường sống trong thành phố cấm đi lại ban đêm."}); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SavePremise("# Bài kiểm tra thẩm định\n\n## Mục tiêu của nhân vật chính\nLâm Chu cầu sinh"); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "Cầu sinh", CoreEvent: "Lâm Chu thoát nạn"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "Lâm Chu", Role: "Nhân vật chính", Description: "Người cầu sinh"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{{Category: "society", Rule: "Cổng thành cấm đi lại ban đêm", Boundary: "Đóng khi đêm xuống"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAuditFoundationControlsWritingTransition(t *testing.T) {
	s := completeShortFoundation(t)
	tool := NewAuditFoundationTool(s)
	if !tool.StrictSchema() {
		t.Fatal("audit_foundation phải dùng lược đồ nghiêm ngặt")
	}
	if err := llmcontract.ValidateStrictReady(tool.Schema()); err != nil {
		t.Fatalf("lược đồ audit_foundation chưa sẵn sàng cho chế độ nghiêm ngặt: %v", err)
	}
	missing, err := s.FoundationMissing()
	if err != nil || len(missing) != 1 || missing[0] != "foundation_audit" {
		t.Fatalf("mong đợi chỉ có foundation_audit, nhận được %v, err=%v", missing, err)
	}
	fingerprint, err := s.FoundationFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	failed, _ := json.Marshal(map[string]any{
		"fingerprint": fingerprint,
		"ready":       false,
		"summary":     "Tên nhân vật không nhất quán",
		"issues": []map[string]any{{
			"artifact": "characters", "description": "Nhân vật không nhất quán", "evidence": "Tiền đề là Lâm Chu, bảng nhân vật lại là người khác", "suggestion": "Thống nhất nhân vật",
		}},
	})
	if _, err := tool.Execute(context.Background(), failed); err != nil {
		t.Fatalf("thẩm định thất bại phải lưu lại hướng dẫn: %v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseWriting {
		t.Fatal("thẩm định thất bại không được chuyển sang giai đoạn viết")
	}

	passed, _ := json.Marshal(map[string]any{
		"fingerprint": fingerprint,
		"ready":       true,
		"summary":     "Thiết lập nền tảng nhất quán",
		"issues":      []any{},
	})
	if _, err := tool.Execute(context.Background(), passed); err != nil {
		t.Fatalf("thẩm định đạt: %v", err)
	}
	if p, _ := s.Progress.Load(); p.Phase != domain.PhaseWriting {
		t.Fatalf("thẩm định đạt phải chuyển sang giai đoạn viết, nhận %s", p.Phase)
	}
}

func TestSaveFoundationWaitsForSemanticAudit(t *testing.T) {
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if err := s.Progress.Init(1); err != nil {
		t.Fatal(err)
	}
	if err := s.Book.Save(domain.BookMetadata{Title: "Sách kiểm tra", Synopsis: "Lâm Chu mạo hiểm bước vào thành phố cấm đi lại ban đêm."}); err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SavePremise("# test"); err != nil {
		t.Fatal(err)
	}
	if err := s.Characters.Save([]domain.Character{{Name: "Lâm Chu", Role: "Nhân vật chính"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.World.SaveWorldRules([]domain.WorldRule{{Category: "society", Rule: "Cấm đi lại ban đêm", Boundary: "Khi trời tối"}}); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"type": "outline", "scale": "short",
		"content": []map[string]any{{"chapter": 1, "title": "Khởi đầu", "core_event": "Lâm Chu vào thành", "hook": "Cấm đi lại ban đêm", "scenes": []string{"Vào thành"}}},
	})
	result, err := NewSaveFoundationTool(s).Execute(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Ready     bool     `json:"foundation_ready"`
		Remaining []string `json:"remaining"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Ready || len(payload.Remaining) != 1 || payload.Remaining[0] != "foundation_audit" {
		t.Fatalf("save_foundation phải chờ thẩm định: %+v", payload)
	}
	if p, _ := s.Progress.Load(); p.Phase == domain.PhaseWriting {
		t.Fatal("save_foundation không được vào giai đoạn viết trước khi thẩm định")
	}
}

func TestAuditFoundationRejectsStaleFingerprint(t *testing.T) {
	s := completeShortFoundation(t)
	fingerprint, err := s.FoundationFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Outline.SavePremise("# Phiên bản đã được sửa"); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{
		"fingerprint": fingerprint, "ready": true, "summary": "Đạt", "issues": []any{},
	})
	if _, err := NewAuditFoundationTool(s).Execute(context.Background(), args); err == nil || !strings.Contains(err.Error(), "hãy gọi lại novel_context") {
		t.Fatalf("mong đợi từ chối fingerprint lỗi thời, nhận %v", err)
	}
}
