package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestSummaryTitleCacheTracksSave(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "Tiêu đề cũ"}); err != nil {
		t.Fatal(err)
	}
	if title, err := st.Summaries.LoadSummaryTitle(1); err != nil || title != "Tiêu đề cũ" {
		t.Fatalf("đọc tiêu đề lần đầu: title=%q err=%v", title, err)
	}
	if err := st.Summaries.SaveSummary(domain.ChapterSummary{Chapter: 1, Title: "Tiêu đề mới"}); err != nil {
		t.Fatal(err)
	}
	if title, err := st.Summaries.LoadSummaryTitle(1); err != nil || title != "Tiêu đề mới" {
		t.Fatalf("cache không cập nhật sau khi lưu: title=%q err=%v", title, err)
	}
}

func TestProjectFormatDefaultsToLegacyAndPersistsUpgrade(t *testing.T) {
	st := NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if version, err := st.LoadProjectFormatVersion(); err != nil || version != LegacyProjectFormatVersion {
		t.Fatalf("thiếu file version phải được nhận diện là định dạng cũ: version=%d err=%v", version, err)
	}
	if err := st.SaveProjectFormatVersion(CurrentProjectFormatVersion); err != nil {
		t.Fatal(err)
	}
	if version, err := st.LoadProjectFormatVersion(); err != nil || version != CurrentProjectFormatVersion {
		t.Fatalf("version định dạng chưa được lưu bền vững: version=%d err=%v", version, err)
	}
}

func TestFoundationMissingReturnsReadError(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FoundationMissing(); err == nil {
		t.Fatal("dàn ý hỏng phải trả lỗi đọc, không được hạ cấp thành mục bị thiếu")
	}
}

func TestClearHandledSteerKeepsIntentWhenProgressReadFails(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.RunMeta.Init("default", "test", "model"); err != nil {
		t.Fatalf("RunMeta.Init: %v", err)
	}
	if err := st.RunMeta.SetPendingSteer("giữ lại can thiệp này"); err != nil {
		t.Fatalf("SetPendingSteer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearHandledSteer(); err == nil {
		t.Fatal("corrupt progress should make ClearHandledSteer fail")
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		t.Fatalf("RunMeta.Load: %v", err)
	}
	if meta == nil || meta.PendingSteer != "giữ lại can thiệp này" {
		t.Fatalf("recovery intent was lost after partial clear: %+v", meta)
	}
}
