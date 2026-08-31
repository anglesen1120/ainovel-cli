package userrules

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

// Mô hình nil + thư mục rules rỗng: mọi chuẩn hóa đều hạ cấp, nhưng snapshot vẫn tạo được (system_defaults làm nền) và được ghi xuống đĩa.
// Hai thư mục trong LoadOptions{} là chuỗi rỗng, RawFileSources trả về nil, test không chạm vào ổ đĩa thật.
func newDegradedService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	return NewService(st, nil, rules.LoadOptions{}), st
}

func TestService_Build_DegradesButPersists(t *testing.T) {
	svc, st := newDegradedService(t)

	snap, err := svc.Build(t.Context(), "Mỗi chương 1200 chữ, nhân vật chính bình tĩnh kiềm chế")
	if err != nil {
		t.Fatalf("Build không nên báo lỗi (hạ cấp thay vì chặn): %v", err)
	}
	if snap.Status != rules.StatusDegraded {
		t.Fatalf("không có mô hình thì phải hạ cấp, status=%q", snap.Status)
	}
	// system_defaults luôn làm nền cho baseline cơ học.
	if len(snap.Structured.FatigueWords) == 0 || len(snap.Structured.ForbiddenPhrases) == 0 {
		t.Fatalf("phải giữ baseline cơ học system_defaults, got %+v", snap.Structured)
	}
	// khởi động prompt hạ cấp thành raw preferences, không mất nguyên văn.
	if snap.Preferences == "" {
		t.Fatal("hạ cấp phải ghi nguyên văn khởi động prompt vào preferences")
	}

	// Đã ghi xuống đĩa: GetOrBuild đọc lại cùng một bản thay vì dựng lại.
	reloaded, err := st.UserRules.Load()
	if err != nil || reloaded == nil {
		t.Fatalf("snapshot phải đã ghi xuống đĩa: err=%v snap=%v", err, reloaded)
	}
	if reloaded.Preferences != snap.Preferences {
		t.Fatal("nội dung ghi xuống đĩa không khớp giá trị trả về")
	}
}

func TestService_GetOrBuildInitializesMissingSnapshot(t *testing.T) {
	svc, st := newDegradedService(t)

	if cur, _ := st.UserRules.Load(); cur != nil {
		t.Fatal("ban đầu không nên có snapshot")
	}
	snap, err := svc.GetOrBuild(t.Context())
	if err != nil {
		t.Fatalf("GetOrBuild không nên báo lỗi: %v", err)
	}
	if len(snap.Structured.FatigueWords) == 0 {
		t.Fatal("tạo lười phải chứa system_defaults")
	}
	if cur, _ := st.UserRules.Load(); cur == nil {
		t.Fatal("GetOrBuild phải đồng thời ghi xuống đĩa")
	}
}

func TestService_AddRuntimeRule_PersistsAndReturnsCandidate(t *testing.T) {
	svc, st := newDegradedService(t)

	const text = "Từ nay ít dùng ẩn dụ"
	merged, cand, err := svc.AddRuntimeRule(t.Context(), text)
	if err != nil {
		t.Fatalf("AddRuntimeRule không nên báo lỗi: %v", err)
	}
	// Ứng viên dùng để hiển thị lại: khi không có mô hình thì hạ cấp, nguyên văn đi vào preferences.
	if !cand.Degraded {
		t.Fatal("khi không có mô hình, ứng viên lần này phải hạ cấp")
	}
	if cand.Preferences != text {
		t.Fatalf("ứng viên phải giữ nguyên văn, got %q", cand.Preferences)
	}
	// Snapshot sau khi chồng chứa mục này và đã ghi xuống đĩa.
	if merged.Preferences == "" {
		t.Fatal("preferences sau khi chồng không được rỗng")
	}
	reloaded, err := st.UserRules.Load()
	if err != nil || reloaded == nil {
		t.Fatalf("sau khi chồng phải ghi xuống đĩa: err=%v", err)
	}
	if reloaded.Status != rules.StatusDegraded {
		t.Fatalf("có nguồn hạ cấp thì status phải là degraded, got %q", reloaded.Status)
	}
}
