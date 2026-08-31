package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

// AuditFoundationTool nhận kết luận thẩm tra ngữ nghĩa của Architect đối với nền tảng thiết lập đã được ghi xuống đĩa.
// Phần văn học và ngữ nghĩa liên tệp do mô hình quyết định; công cụ chỉ bảo đảm phiên bản thẩm tra, kết luận và chuyển trạng thái luôn nhất quán.
type AuditFoundationTool struct {
	store *store.Store
}

func NewAuditFoundationTool(store *store.Store) *AuditFoundationTool {
	return &AuditFoundationTool{store: store}
}

func (t *AuditFoundationTool) Name() string { return "audit_foundation" }
func (t *AuditFoundationTool) Description() string {
	return "Thẩm tra xem book, premise, outline, characters, world_rules và compass đã nhất quán về ngữ nghĩa hay chưa." +
		"Phải gọi lại novel_context trước, rồi truyền nguyên xi foundation_status.fingerprint."
}
func (t *AuditFoundationTool) Label() string                          { return "Thẩm tra thiết lập" }
func (t *AuditFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *AuditFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }
func (t *AuditFoundationTool) StrictSchema() bool                     { return true }

func (t *AuditFoundationTool) Schema() map[string]any {
	issue := schema.Object(
		schema.Property("artifact", schema.String("Tác phẩm có vấn đề, chẳng hạn book/premise/characters/layered_outline/world_rules/compass")).Required(),
		schema.Property("description", schema.String("Vấn đề ngữ nghĩa liên tệp")).Required(),
		schema.Property("evidence", schema.String("Bằng chứng mâu thuẫn cụ thể lấy từ nội dung đã được ghi xuống đĩa")).Required(),
		schema.Property("suggestion", llmcontract.Nullable(schema.String("Hướng sửa đổi được khuyến nghị; nếu không cần thì để null"))).Required(),
	)
	return schema.Object(
		schema.Property("fingerprint", schema.String("foundation_status.fingerprint do novel_context trả về")).Required(),
		schema.Property("ready", schema.Bool("Tất cả thiết lập nền tảng đã nhất quán về ngữ nghĩa hay chưa, có thể bước vào viết hay không")).Required(),
		schema.Property("summary", schema.String("Tóm tắt kết luận thẩm tra")).Required(),
		schema.Property("issues", schema.Array("Các vấn đề ngữ nghĩa liên tệp được phát hiện; khi ready=true thì là mảng rỗng", issue)).Required(),
	)
}

func (t *AuditFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var audit domain.FoundationAudit
	if err := json.Unmarshal(args, &audit); err != nil {
		return nil, fmt.Errorf("đối số không hợp lệ: %w: %w", errs.ErrToolArgs, err)
	}
	if strings.TrimSpace(audit.Fingerprint) == "" {
		return nil, fmt.Errorf("fingerprint là bắt buộc: %w", errs.ErrToolArgs)
	}
	if strings.TrimSpace(audit.Summary) == "" {
		return nil, fmt.Errorf("summary là bắt buộc: %w", errs.ErrToolArgs)
	}

	missing, err := t.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("tải trạng thái nền tảng: %w: %w", errs.ErrStoreRead, err)
	}
	for _, item := range missing {
		if item != "foundation_audit" {
			return nil, fmt.Errorf("thiết lập nền tảng vẫn còn thiếu %s, chưa thể thẩm tra: %w", item, errs.ErrToolPrecondition)
		}
	}
	current, err := t.store.FoundationFingerprint()
	if err != nil {
		return nil, fmt.Errorf("ghi dấu vân tay nền tảng: %w: %w", errs.ErrStoreRead, err)
	}
	if audit.Fingerprint != current {
		return nil, fmt.Errorf("thiết lập nền tảng đã thay đổi; hãy gọi lại novel_context để lấy fingerprint mới nhất rồi mới thẩm tra: %w", errs.ErrToolConflict)
	}
	if audit.Ready && len(audit.Issues) > 0 {
		return nil, fmt.Errorf("khi ready=true thì issues phải rỗng: %w", errs.ErrToolArgs)
	}
	if !audit.Ready && len(audit.Issues) == 0 {
		return nil, fmt.Errorf("khi ready=false thì phải nêu rõ issues cụ thể: %w", errs.ErrToolArgs)
	}
	for i, issue := range audit.Issues {
		if strings.TrimSpace(issue.Artifact) == "" || strings.TrimSpace(issue.Description) == "" || strings.TrimSpace(issue.Evidence) == "" {
			return nil, fmt.Errorf("issues[%d] phải bao gồm artifact, description và evidence: %w", i, errs.ErrToolArgs)
		}
	}

	if err := t.store.Outline.SaveFoundationAudit(audit); err != nil {
		return nil, fmt.Errorf("lưu bản thẩm tra nền tảng: %w: %w", errs.ErrStoreWrite, err)
	}
	result := map[string]any{
		"foundation_ready": audit.Ready,
		"issues":           audit.Issues,
	}
	if !audit.Ready {
		result["next_action"] = "Sửa các thiết lập nền tảng tương ứng theo issues, rồi gọi lại novel_context và thẩm tra lần nữa"
		return json.Marshal(result)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "foundation_audit", "meta/foundation_audit.json"); err != nil {
		return nil, fmt.Errorf("ghi checkpoint cho bản thẩm tra nền tảng: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		return nil, fmt.Errorf("vào giai đoạn viết: %w: %w", errs.ErrStoreWrite, err)
	}
	result["phase"] = string(domain.PhaseWriting)
	return json.Marshal(result)
}
