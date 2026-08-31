package domain

// FoundationAuditIssue là vấn đề nhất quán giữa các file do Architect phát hiện trong thiết lập nền đã lưu。
type FoundationAuditIssue struct {
	Artifact    string `json:"artifact"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
	Suggestion  string `json:"suggestion,omitempty"`
}

// FoundationAudit ghi lại một lần model rà soát thiết lập nền của phiên bản xác định。
type FoundationAudit struct {
	Fingerprint string                 `json:"fingerprint"`
	Ready       bool                   `json:"ready"`
	Summary     string                 `json:"summary"`
	Issues      []FoundationAuditIssue `json:"issues"`
}
