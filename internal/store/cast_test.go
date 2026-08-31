package store

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func newCastTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Khởi tạo: %v", err)
	}
	return s
}

func TestCastMergeAppearances_NewEntries(t *testing.T) {
	s := newCastTestStore(t)
	intros := []domain.CastIntro{{Name: "Ông Châu", BriefRole: "chủ quán trọ"}}
	if err := s.Cast.MergeAppearances(5, []string{"Ông Châu", "A Vân"}, intros, nil); err != nil {
		t.Fatalf("MergeAppearances: %v", err)
	}

	entries, err := s.Cast.Load()
	if err != nil {
		t.Fatalf("Tải: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("mong đợi 2 mục, nhận %d", len(entries))
	}
	for _, e := range entries {
		if e.FirstSeenChapter != 5 || e.LastSeenChapter != 5 || e.AppearanceCount != 1 {
			t.Errorf("mục %s: các trường xuất hiện không như mong đợi %+v", e.Name, e)
		}
		if e.Name == "Ông Châu" && e.BriefRole != "chủ quán trọ" {
			t.Errorf("mong đợi BriefRole chủ quán trọ cho Ông Châu, nhận %q", e.BriefRole)
		}
		if e.Name == "A Vân" && e.BriefRole != "" {
			t.Errorf("A Vân không có intro, BriefRole phải rỗng, nhận %q", e.BriefRole)
		}
	}
}

func TestCastMergeAppearances_AccumulatesOnRepeat(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.MergeAppearances(5, []string{"Ông Châu"}, nil, nil); err != nil {
		t.Fatalf("lần gộp đầu: %v", err)
	}
	if err := s.Cast.MergeAppearances(8, []string{"Ông Châu"}, nil, nil); err != nil {
		t.Fatalf("lần gộp thứ hai: %v", err)
	}

	entries, _ := s.Cast.Load()
	if len(entries) != 1 {
		t.Fatalf("mong đợi 1 mục, nhận %d", len(entries))
	}
	e := entries[0]
	if e.FirstSeenChapter != 5 || e.LastSeenChapter != 8 || e.AppearanceCount != 2 {
		t.Fatalf("mong đợi first=5,last=8,count=2; nhận %+v", e)
	}
	if len(e.AppearanceChapters) != 2 || e.AppearanceChapters[0] != 5 || e.AppearanceChapters[1] != 8 {
		t.Errorf("AppearanceChapters sai: %v", e.AppearanceChapters)
	}
}

func TestCastMergeAppearances_IsIdempotent(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.MergeAppearances(5, []string{"Ông Châu"}, nil, nil); err != nil {
		t.Fatalf("lần gộp đầu: %v", err)
	}
	// commit cùng chương bị kích hoạt lại (khôi phục sau sập hoặc tình huống ghi đè)
	if err := s.Cast.MergeAppearances(5, []string{"Ông Châu"}, nil, nil); err != nil {
		t.Fatalf("lần gộp thứ hai: %v", err)
	}

	entries, _ := s.Cast.Load()
	if len(entries) != 1 {
		t.Fatalf("mong đợi 1 mục, nhận %d", len(entries))
	}
	if entries[0].AppearanceCount != 1 {
		t.Errorf("mong đợi AppearanceCount=1 sau bản sao lặp, nhận %d", entries[0].AppearanceCount)
	}
}

func TestCastMergeAppearances_FiltersCoreCharacters(t *testing.T) {
	s := newCastTestStore(t)
	core := map[string]bool{"Lâm Mặc": true, "Lý Thanh Nghiễn": true}
	if err := s.Cast.MergeAppearances(3, []string{"Lâm Mặc", "Lý Thanh Nghiễn", "Ông Châu"}, nil, core); err != nil {
		t.Fatalf("MergeAppearances: %v", err)
	}

	entries, _ := s.Cast.Load()
	if len(entries) != 1 || entries[0].Name != "Ông Châu" {
		t.Fatalf("chỉ mong đợi Ông Châu trong sổ ghi, nhận %+v", entries)
	}
}

func TestCastMergeAppearances_BackfillsBriefRole(t *testing.T) {
	s := newCastTestStore(t)
	// Chương 5 giới thiệu Ông Châu nhưng Writer quên điền brief_role
	if err := s.Cast.MergeAppearances(5, []string{"Ông Châu"}, nil, nil); err != nil {
		t.Fatalf("lần gộp đầu: %v", err)
	}
	// Chương 8 lại xuất hiện, lần này Writer đã bổ sung brief_role
	intros := []domain.CastIntro{{Name: "Ông Châu", BriefRole: "chủ quán trọ"}}
	if err := s.Cast.MergeAppearances(8, []string{"Ông Châu"}, intros, nil); err != nil {
		t.Fatalf("lần gộp thứ hai: %v", err)
	}

	entries, _ := s.Cast.Load()
	if entries[0].BriefRole != "chủ quán trọ" {
		t.Errorf("mong đợi BriefRole chủ quán trọ được bổ sung ngược, nhận %q", entries[0].BriefRole)
	}
}

func TestCastMergeAppearances_NoOverwriteBriefRole(t *testing.T) {
	s := newCastTestStore(t)
	// Chương 5 xác định BriefRole=chủ quán trọ
	if err := s.Cast.MergeAppearances(5,
		[]string{"Ông Châu"},
		[]domain.CastIntro{{Name: "Ông Châu", BriefRole: "chủ quán trọ"}},
		nil,
	); err != nil {
		t.Fatalf("lần gộp đầu: %v", err)
	}
	// Chương 8 Writer truyền nhầm một BriefRole khác (không được ghi đè)
	if err := s.Cast.MergeAppearances(8,
		[]string{"Ông Châu"},
		[]domain.CastIntro{{Name: "Ông Châu", BriefRole: "tay sai sòng bạc"}},
		nil,
	); err != nil {
		t.Fatalf("lần gộp thứ hai: %v", err)
	}

	entries, _ := s.Cast.Load()
	if entries[0].BriefRole != "chủ quán trọ" {
		t.Errorf("mong đợi BriefRole KHÔNG bị ghi đè, nhận %q", entries[0].BriefRole)
	}
}

func TestCastRecentActive_OrdersByLastSeen(t *testing.T) {
	s := newCastTestStore(t)
	_ = s.Cast.MergeAppearances(3, []string{"A"}, nil, nil)
	_ = s.Cast.MergeAppearances(10, []string{"B"}, nil, nil)
	_ = s.Cast.MergeAppearances(7, []string{"C"}, nil, nil)

	recent, err := s.Cast.RecentActive(2)
	if err != nil {
		t.Fatalf("RecentActive: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("mong đợi 2, nhận %d", len(recent))
	}
	if recent[0].Name != "B" || recent[1].Name != "C" {
		t.Errorf("mong đợi thứ tự B, C; nhận %s, %s", recent[0].Name, recent[1].Name)
	}
}

func TestCastRecentActive_SkipsPromoted(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.Save([]domain.CastEntry{
		{Name: "đã lên core", LastSeenChapter: 20, AppearanceCount: 8, Promoted: true},
		{Name: "phụ diễn hoạt động", LastSeenChapter: 18, AppearanceCount: 3},
		{Name: "phụ diễn khác", LastSeenChapter: 15, AppearanceCount: 2},
	}); err != nil {
		t.Fatalf("Lưu: %v", err)
	}

	recent, err := s.Cast.RecentActive(10)
	if err != nil {
		t.Fatalf("RecentActive: %v", err)
	}
	if len(recent) != 2 {
		t.Fatalf("mong đợi 2 (loại trừ Promoted), nhận %d: %+v", len(recent), recent)
	}
	for _, e := range recent {
		if e.Promoted {
			t.Errorf("mục Promoted lọt vào RecentActive: %+v", e)
		}
	}
	if recent[0].Name != "phụ diễn hoạt động" {
		t.Errorf("mong đợi phần tử đầu=phụ diễn hoạt động, nhận %s", recent[0].Name)
	}
}

func TestCastMergeAppearances_NoOpOnEmpty(t *testing.T) {
	s := newCastTestStore(t)
	if err := s.Cast.MergeAppearances(5, nil, nil, nil); err != nil {
		t.Fatalf("MergeAppearances rỗng: %v", err)
	}
	if err := s.Cast.MergeAppearances(0, []string{"Ông Châu"}, nil, nil); err != nil {
		t.Fatalf("MergeAppearances chapter=0: %v", err)
	}
	entries, _ := s.Cast.Load()
	if len(entries) != 0 {
		t.Errorf("mong đợi ledger rỗng, nhận %d mục", len(entries))
	}
}
