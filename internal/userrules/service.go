package userrules

import (
	"context"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Service điều phối việc tạo và cập nhật ảnh chụp quy tắc người dùng: chuẩn hóa các nguồn → hợp nhất tất định → lưu xuống đĩa.
//
// Hai nơi gọi dùng chung cùng một logic:
//   - Mở hoặc làm mới sách: Build / GetOrBuild, do Host gọi tất định.
//   - Cập nhật khi đang chạy: sau khi Arbiter trích xuất rules, Host gọi AddRuntimeRule.
type Service struct {
	store     *store.Store
	norm      *Normalizer
	rulesOpts rules.LoadOptions
}

// NewService tạo dịch vụ. model dùng để chuẩn hóa (nên là mô hình có năng lực tốt); khi model là nil,
// mọi nguồn hạ cấp thành raw preferences (vẫn tạo được ảnh chụp, system_defaults đảm bảo kiểm tra cơ học).
func NewService(st *store.Store, model agentcore.ChatModel, opts rules.LoadOptions) *Service {
	return &Service{store: st, norm: NewNormalizer(model), rulesOpts: opts}
}

// normalizeOrDegrade chuẩn hóa một nguồn; khi thất bại, ghi lỗi thật và hạ cấp thành raw preferences
// (ảnh chụp Status=degraded, giữ nguyên văn) — hạ cấp là sự thật hiển thị được, nguyên nhân lỗi đi vào nhật ký.
func (s *Service) normalizeOrDegrade(ctx context.Context, source, text string) rules.Candidate {
	cand, err := s.norm.Normalize(ctx, source, text)
	if err != nil {
		slog.Warn("Chuẩn hóa quy tắc thất bại, hạ cấp thành sở thích nguyên văn", "module", "rules", "source", source, "err", err)
		return degraded(source, text)
	}
	return cand
}

// Build chuẩn hóa các nguồn tĩnh (system_defaults + tệp rules + prompt khởi động) để tạo và lưu ảnh chụp.
// Được gọi khi mở hoặc làm mới sách. startupPrompt có thể rỗng.
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

// GetOrBuild trả về ảnh chụp hiện tại; khi thiếu thì khởi tạo từ system_defaults + tệp rules.
// Mọi đường đọc khi chạy đều đi qua đây.
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

// AddRuntimeRule chuẩn hóa một quy tắc dài hạn được thêm khi chạy, chồng với ưu tiên cao nhất vào ảnh chụp hiện tại rồi lưu.
// Không bao giờ trả lỗi vì chuẩn hóa thất bại — khi đó quy tắc này hạ cấp thành raw preferences.
// Trả về ảnh chụp sau khi chồng cùng ứng viên chuẩn hóa của lần này.
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
