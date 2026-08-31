package userrules

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Mô hình nil và thư mục quy tắc trống: toàn bộ chuẩn hóa hạ cấp nhưng vẫn có thể tạo và lưu ảnh chụp (system_defaults làm nền).
// Hai thư mục của LoadOptions{} là chuỗi rỗng, RawFileSources trả về nil nên kiểm thử không chạm vào đĩa thật.
func newDegradedService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	return NewService(st, nil, rules.LoadOptions{}), st
}

func TestService_Build_DegradesButPersists(t *testing.T) {
	svc, st := newDegradedService(t)

	snap, err := svc.Build(t.Context(), "Mỗi chương 1.200 từ, nhân vật chính điềm tĩnh, kiềm chế")
	if err != nil {
		t.Fatalf("Build không được báo lỗi (hạ cấp thay vì chặn): %v", err)
	}
	if snap.Status != rules.StatusDegraded {
		t.Fatalf("không có mô hình phải hạ cấp, status=%q", snap.Status)
	}
	// system_defaults luôn đảm bảo nền kiểm tra cơ học.
	if len(snap.Structured.FatigueWords) == 0 || len(snap.Structured.ForbiddenPhrases) == 0 {
		t.Fatalf("phải giữ nền cơ học system_defaults, nhận %+v", snap.Structured)
	}
	// Prompt khởi động hạ cấp thành raw preferences; nguyên văn không bị mất.
	if snap.Preferences == "" {
		t.Fatal("hạ cấp phải lưu nguyên văn prompt khởi động vào preferences")
	}

	// Đã lưu: GetOrBuild đọc lại đúng ảnh chụp, không dựng lại.
	reloaded, err := st.UserRules.Load()
	if err != nil || reloaded == nil {
		t.Fatalf("ảnh chụp phải được lưu: err=%v snap=%v", err, reloaded)
	}
	if reloaded.Preferences != snap.Preferences {
		t.Fatal("nội dung lưu và nội dung trả về không khớp")
	}
}

func TestService_GetOrBuildInitializesMissingSnapshot(t *testing.T) {
	svc, st := newDegradedService(t)

	if cur, _ := st.UserRules.Load(); cur != nil {
		t.Fatal("ban đầu không được có ảnh chụp")
	}
	snap, err := svc.GetOrBuild(t.Context())
	if err != nil {
		t.Fatalf("GetOrBuild không được báo lỗi: %v", err)
	}
	if len(snap.Structured.FatigueWords) == 0 {
		t.Fatal("tạo lười phải chứa system_defaults")
	}
	if cur, _ := st.UserRules.Load(); cur == nil {
		t.Fatal("GetOrBuild phải đồng thời lưu ảnh chụp")
	}
}

func TestService_AddRuntimeRule_PersistsAndReturnsCandidate(t *testing.T) {
	svc, st := newDegradedService(t)

	const text = "Từ nay hạn chế dùng ẩn dụ"
	merged, cand, err := svc.AddRuntimeRule(t.Context(), text)
	if err != nil {
		t.Fatalf("AddRuntimeRule không được báo lỗi: %v", err)
	}
	// Ứng viên dùng để hiển thị lại: không có mô hình thì hạ cấp, nguyên văn vào preferences.
	if !cand.Degraded {
		t.Fatal("không có mô hình thì ứng viên lần này phải hạ cấp")
	}
	if cand.Preferences != text {
		t.Fatalf("ứng viên phải giữ nguyên văn, nhận %q", cand.Preferences)
	}
	// Ảnh chụp sau khi chồng phải có quy tắc này và đã được lưu.
	if merged.Preferences == "" {
		t.Fatal("preferences sau khi chồng không được rỗng")
	}
	reloaded, err := st.UserRules.Load()
	if err != nil || reloaded == nil {
		t.Fatalf("sau khi chồng phải được lưu: err=%v", err)
	}
	if reloaded.Status != rules.StatusDegraded {
		t.Fatalf("có nguồn hạ cấp thì status phải là degraded, nhận %q", reloaded.Status)
	}
}
