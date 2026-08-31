package userrules

import (
	"context"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Service điều phối việc tạo và cập nhật snapshot user rules: chuẩn hóa các nguồn -> hợp nhất xác định -> ghi xuống đĩa.
//
// Hai bên gọi dùng chung một logic:
//   - Mở sách/làm mới: Build / GetOrBuild, do Host gọi xác định.
//   - Cập nhật trong runtime: sau khi Arbiter trích xuất rules, Host gọi AddRuntimeRule.
type Service struct {
	store     *store.Store
	norm      *Normalizer
	rulesOpts rules.LoadOptions
}

// NewService khởi tạo service. model dùng cho chuẩn hóa (nên là mô hình có năng lực mạnh); khi model là nil,
// mọi nguồn hạ cấp thành raw preferences (vẫn tạo được snapshot, kiểm tra cơ học do system_defaults làm nền).
func NewService(st *store.Store, model agentcore.ChatModel, opts rules.LoadOptions) *Service {
	return &Service{store: st, norm: NewNormalizer(model), rulesOpts: opts}
}

// normalizeOrDegrade chuẩn hóa một nguồn; khi thất bại, ghi lỗi thật và hạ cấp thành raw preferences
// (snapshot Status=degraded, giữ nguyên văn bản gốc); hạ cấp là sự kiện thấy được, nguyên nhân lỗi đi vào log.
func (s *Service) normalizeOrDegrade(ctx context.Context, source, text string) rules.Candidate {
	cand, err := s.norm.Normalize(ctx, source, text)
	if err != nil {
		slog.Warn("Chuẩn hóa rules thất bại, hạ cấp thành sở thích nguyên văn", "module", "rules", "source", source, "err", err)
		return degraded(source, text)
	}
	return cand
}

// Build chuẩn hóa từ các nguồn tĩnh (system_defaults + tệp rules + khởi động prompt), tạo snapshot rồi ghi xuống đĩa.
// Được gọi khi mở sách/làm mới. startupPrompt có thể rỗng.
func (s *Service) Build(ctx context.Context, startupPrompt string) (*rules.Snapshot, error) {
	cands := []rules.Candidate{rules.SystemDefaults()}
	for _, rs := range rules.RawFileSources(s.rulesOpts) {
		cands = append(cands, s.normalizeOrDegrade(ctx, rs.Label, rs.Text))
	}
	if strings.TrimSpace(startupPrompt) != "" {
		cands = append(cands, s.normalizeOrDegrade(ctx, "startup_prompt", startupPrompt))
	}
	snap := rules.BuildSnapshot(cands)
	if err := s.store.UserRules.Save(&snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// GetOrBuild trả về snapshot hiện tại; nếu thiếu thì khởi tạo theo system_defaults + tệp rules.
// Đường đọc trong runtime thống nhất đi qua đây.
func (s *Service) GetOrBuild(ctx context.Context) (*rules.Snapshot, error) {
	cur, err := s.store.UserRules.Load()
	if err != nil {
		return nil, err
	}
	if cur != nil {
		return cur, nil
	}
	return s.Build(ctx, "")
}

// AddRuntimeRule chuẩn hóa một rule dài hạn trong runtime, chồng lên snapshot hiện tại với ưu tiên cao nhất rồi ghi xuống đĩa.
// Không bao giờ báo lỗi vì chuẩn hóa thất bại; khi thất bại, rule đó hạ cấp thành raw preferences.
// Trả về snapshot sau khi chồng và ứng viên chuẩn hóa của lần này.
func (s *Service) AddRuntimeRule(ctx context.Context, text string) (*rules.Snapshot, rules.Candidate, error) {
	cur, err := s.GetOrBuild(ctx)
	if err != nil {
		return nil, rules.Candidate{}, err
	}
	cand := s.normalizeOrDegrade(ctx, "runtime_update", text)
	merged := rules.OverlaySnapshot(*cur, cand)
	if err := s.store.UserRules.Save(&merged); err != nil {
		return nil, cand, err
	}
	return &merged, cand, nil
}
