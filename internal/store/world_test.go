package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return s
}

// TestLoadEmpty kiểm tra thống nhất hành vi đọc rỗng của mọi miền.
func TestLoadEmpty(t *testing.T) {
	s := newTestStore(t)

	if v, err := s.World.LoadTimeline(); err != nil || v != nil {
		t.Errorf("Timeline: want (nil, nil), got (%v, %v)", v, err)
	}
	if v, err := s.World.LoadForeshadowLedger(); err != nil || v != nil {
		t.Errorf("Foreshadow: want (nil, nil), got (%v, %v)", v, err)
	}
	if v, err := s.World.LoadRelationships(); err != nil || v != nil {
		t.Errorf("Relationships: want (nil, nil), got (%v, %v)", v, err)
	}
	if v, err := s.World.LoadStateChanges(); err != nil || v != nil {
		t.Errorf("StateChanges: want (nil, nil), got (%v, %v)", v, err)
	}
	if v, err := s.World.LoadStyleRules(); err != nil || v != nil {
		t.Errorf("StyleRules: want (nil, nil), got (%v, %v)", v, err)
	}
	if v, err := s.World.LoadWorldRules(); err != nil || v != nil {
		t.Errorf("WorldRules: want (nil, nil), got (%v, %v)", v, err)
	}
	if v, err := s.World.LoadReview(99); err != nil || v != nil {
		t.Errorf("Review: want (nil, nil), got (%v, %v)", v, err)
	}
	if v, err := s.World.LoadLastReview(10); err != nil || v != nil {
		t.Errorf("LastReview: want (nil, nil), got (%v, %v)", v, err)
	}
}

// ── Timeline ──

func TestTimeline_Append(t *testing.T) {
	s := newTestStore(t)

	if err := s.World.AppendTimelineEvents([]domain.TimelineEvent{
		{Chapter: 1, Time: "sáng sớm", Event: "sự kiện một"},
	}); err != nil {
		t.Fatalf("batch1: %v", err)
	}
	if err := s.World.AppendTimelineEvents([]domain.TimelineEvent{
		{Chapter: 2, Time: "chiều", Event: "sự kiện hai"},
		{Chapter: 3, Time: "chạng vạng", Event: "sự kiện ba"},
	}); err != nil {
		t.Fatalf("batch2: %v", err)
	}

	loaded, err := s.World.LoadTimeline()
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("want 3, got %d", len(loaded))
	}
	if loaded[2].Event != "sự kiện ba" {
		t.Errorf("third event: %+v", loaded[2])
	}
}

func TestTimeline_AppendIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	event := domain.TimelineEvent{
		Chapter:    1,
		Time:       "sáng sớm",
		Event:      "Lâm Mặc vào ở quán trọ",
		Characters: []string{"Lâm Mặc", "Ông Châu"},
	}
	if err := s.World.AppendTimelineEvents([]domain.TimelineEvent{event}); err != nil {
		t.Fatalf("append first: %v", err)
	}
	event.Characters = []string{"Ông Châu", "Lâm Mặc"} // thứ tự nhân vật không được ảnh hưởng cách nhận diện cùng sự kiện
	if err := s.World.AppendTimelineEvents([]domain.TimelineEvent{event}); err != nil {
		t.Fatalf("append duplicate: %v", err)
	}

	loaded, err := s.World.LoadTimeline()
	if err != nil {
		t.Fatalf("LoadTimeline: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("duplicate timeline event should be ignored, got %d: %+v", len(loaded), loaded)
	}

	// qua restart vẫn dựng lại chỉ mục khử trùng từ JSONL, phát lại commit Saga không được tạo bản ghi trùng.
	s2 := NewStore(s.Dir())
	if err := s2.World.AppendTimelineEvents([]domain.TimelineEvent{event}); err != nil {
		t.Fatalf("append duplicate after restart: %v", err)
	}
	loaded, err = s2.World.LoadTimeline()
	if err != nil || len(loaded) != 1 {
		t.Fatalf("restart idempotency: got (%+v, %v)", loaded, err)
	}
}

func TestTimeline_DedupKeyDoesNotCollideOnContent(t *testing.T) {
	s := newTestStore(t)
	events := []domain.TimelineEvent{
		{Chapter: 1, Time: "a|b", Event: "c"},
		{Chapter: 1, Time: "a", Event: "b|c"},
	}
	if err := s.World.AppendTimelineEvents(events); err != nil {
		t.Fatalf("append: %v", err)
	}
	loaded, err := s.World.LoadTimeline()
	if err != nil || len(loaded) != 2 {
		t.Fatalf("delimiter-bearing events must remain distinct: got (%+v, %v)", loaded, err)
	}
}

func TestTimeline_MigratesLegacyAndAppendsProjection(t *testing.T) {
	dir := t.TempDir()
	legacyStore := NewStore(dir)
	legacy := []domain.TimelineEvent{{Chapter: 1, Time: "sáng sớm", Event: "sự kiện cũ"}}
	if err := legacyStore.World.io.WriteJSON("timeline.json", legacy); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	if err := legacyStore.World.io.WriteMarkdown("timeline.md", "stale projection"); err != nil {
		t.Fatalf("write stale projection: %v", err)
	}

	s := NewStore(dir)
	if err := s.World.AppendTimelineEvents([]domain.TimelineEvent{
		{Chapter: 2, Time: "chiều", Event: "sự kiện mới"},
	}); err != nil {
		t.Fatalf("append with migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "timeline.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy timeline.json should be removed, err=%v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "timeline.jsonl"))
	if err != nil {
		t.Fatalf("read timeline.jsonl: %v", err)
	}

	if err := s.World.AppendTimelineEvents([]domain.TimelineEvent{
		{Chapter: 3, Time: "chạng vạng", Event: "sự kiện bổ sung"},
	}); err != nil {
		t.Fatalf("append jsonl: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "timeline.jsonl"))
	if err != nil {
		t.Fatalf("read appended timeline.jsonl: %v", err)
	}
	if !strings.HasPrefix(string(after), string(before)) || len(after) <= len(before) {
		t.Fatal("timeline.jsonl should preserve old bytes and append new records")
	}

	loaded, err := s.World.LoadTimeline()
	if err != nil || len(loaded) != 3 {
		t.Fatalf("load migrated timeline: got (%+v, %v)", loaded, err)
	}
	markdown, err := os.ReadFile(filepath.Join(dir, "timeline.md"))
	if err != nil {
		t.Fatalf("read timeline.md: %v", err)
	}
	if string(markdown) != renderTimeline(loaded) {
		t.Fatalf("timeline projection not synchronized:\n%s", markdown)
	}
}

func TestTimeline_RepairsUncommittedJSONLTail(t *testing.T) {
	s := newTestStore(t)
	if err := s.World.AppendTimelineEvents([]domain.TimelineEvent{{Chapter: 1, Event: "bản ghi hoàn chỉnh"}}); err != nil {
		t.Fatalf("append first: %v", err)
	}
	path := filepath.Join(s.Dir(), "timeline.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open jsonl: %v", err)
	}
	if _, err := f.WriteString(`{"chapter":2,"event":"chưa commit`); err != nil {
		_ = f.Close()
		t.Fatalf("write partial tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close jsonl: %v", err)
	}

	s2 := NewStore(s.Dir())
	if err := s2.World.AppendTimelineEvents([]domain.TimelineEvent{{Chapter: 2, Event: "bản ghi phát lại"}}); err != nil {
		t.Fatalf("append after partial tail: %v", err)
	}
	loaded, err := s2.World.LoadTimeline()
	if err != nil || len(loaded) != 2 || loaded[1].Event != "bản ghi phát lại" {
		t.Fatalf("tail recovery: got (%+v, %v)", loaded, err)
	}
}

func TestTimeline_RejectsCorruptCommittedJSONLRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "timeline.jsonl"), []byte("{broken}\n"), 0o644); err != nil {
		t.Fatalf("write corrupt jsonl: %v", err)
	}
	s := NewStore(dir)
	if _, err := s.World.LoadTimeline(); err == nil {
		t.Fatal("committed corrupt record must fail loudly")
	}
}

func TestTimeline_LoadRecent(t *testing.T) {
	s := newTestStore(t)
	_ = s.World.SaveTimeline([]domain.TimelineEvent{
		{Chapter: 1}, {Chapter: 3}, {Chapter: 5}, {Chapter: 7},
	})

	for _, tt := range []struct {
		current, window, want int
	}{
		{7, 10, 4}, // tất cả
		{7, 3, 2},  // ch5,ch7
		{5, 2, 3},  // ch3,ch5,ch7
	} {
		got, _ := s.World.LoadRecentTimeline(tt.current, tt.window)
		if len(got) != tt.want {
			t.Errorf("LoadRecent(%d,%d): want %d, got %d", tt.current, tt.window, tt.want, len(got))
		}
	}
}

// ── Foreshadow ──

func TestForeshadow_UpdateLifecycle(t *testing.T) {
	s := newTestStore(t)

	// plant
	_ = s.World.UpdateForeshadow(1, []domain.ForeshadowUpdate{
		{ID: "f1", Action: "plant", Description: "bóng đen"},
		{ID: "f2", Action: "plant", Description: "kiếm gãy"},
	})
	// advance f1, resolve f2
	_ = s.World.UpdateForeshadow(3, []domain.ForeshadowUpdate{
		{ID: "f1", Action: "advance"},
		{ID: "f2", Action: "resolve"},
	})

	all, _ := s.World.LoadForeshadowLedger()
	if len(all) != 2 {
		t.Fatalf("want 2, got %d", len(all))
	}
	if all[0].Status != "advanced" {
		t.Errorf("f1: want advanced, got %s", all[0].Status)
	}
	if all[1].Status != "resolved" || all[1].ResolvedAt != 3 {
		t.Errorf("f2: want resolved@3, got %s@%d", all[1].Status, all[1].ResolvedAt)
	}

	// LoadActive phải loại resolved
	active, _ := s.World.LoadActiveForeshadow()
	if len(active) != 1 || active[0].ID != "f1" {
		t.Errorf("active: want [f1], got %v", active)
	}
}

func TestForeshadow_RejectsUnknownAndInvalidOperations(t *testing.T) {
	s := newTestStore(t)
	for _, update := range []domain.ForeshadowUpdate{
		{ID: "missing", Action: "advance"},
		{ID: "missing", Action: "resolve"},
		{ID: "f1", Action: "unknown"},
		{ID: "f1", Action: "plant"},
	} {
		if err := s.World.UpdateForeshadow(1, []domain.ForeshadowUpdate{update}); err == nil {
			t.Fatalf("expected rejection for %+v", update)
		}
	}
	if ledger, err := s.World.LoadForeshadowLedger(); err != nil || len(ledger) != 0 {
		t.Fatalf("invalid operations must not mutate ledger: %+v err=%v", ledger, err)
	}
}

func TestForeshadow_PlantIsIdempotent(t *testing.T) {
	s := newTestStore(t)

	_ = s.World.UpdateForeshadow(1, []domain.ForeshadowUpdate{
		{ID: "f1", Action: "plant", Description: "bóng đen"},
	})
	_ = s.World.UpdateForeshadow(1, []domain.ForeshadowUpdate{
		{ID: "f1", Action: "plant", Description: "bóng đen"},
	})
	_ = s.World.UpdateForeshadow(3, []domain.ForeshadowUpdate{
		{ID: "f1", Action: "advance"},
	})
	_ = s.World.UpdateForeshadow(3, []domain.ForeshadowUpdate{
		{ID: "f1", Action: "plant", Description: "bóng đen"},
	})

	all, _ := s.World.LoadForeshadowLedger()
	if len(all) != 1 {
		t.Fatalf("duplicate plant should not append entries, got %d: %+v", len(all), all)
	}
	if all[0].Status != "advanced" {
		t.Fatalf("duplicate plant should not downgrade status, got %s", all[0].Status)
	}
}

// ── Relationships ──

func TestRelationships_UpdateMerge(t *testing.T) {
	s := newTestStore(t)
	_ = s.World.SaveRelationships([]domain.RelationshipEntry{
		{CharacterA: "Trương Tam", CharacterB: "Lý Tứ", Relation: "thầy trò", Chapter: 1},
	})

	// cập nhật mục đã có + thêm mới
	_ = s.World.UpdateRelationships([]domain.RelationshipEntry{
		{CharacterA: "Trương Tam", CharacterB: "Lý Tứ", Relation: "bạn tri kỷ", Chapter: 5},
		{CharacterA: "Vương Ngũ", CharacterB: "Triệu Lục", Relation: "đồng môn", Chapter: 5},
	})

	loaded, _ := s.World.LoadRelationships()
	if len(loaded) != 2 {
		t.Fatalf("want 2, got %d", len(loaded))
	}
	if loaded[0].Relation != "bạn tri kỷ" {
		t.Errorf("update failed: %+v", loaded[0])
	}
}

func TestRelationships_PairKeySymmetry(t *testing.T) {
	s := newTestStore(t)
	_ = s.World.SaveRelationships([]domain.RelationshipEntry{
		{CharacterA: "Trương Tam", CharacterB: "Lý Tứ", Relation: "thầy trò", Chapter: 1},
	})
	// B-A cập nhật theo thứ tự B-A phải khớp cùng một mục
	_ = s.World.UpdateRelationships([]domain.RelationshipEntry{
		{CharacterA: "Lý Tứ", CharacterB: "Trương Tam", Relation: "trở mặt", Chapter: 3},
	})

	loaded, _ := s.World.LoadRelationships()
	if len(loaded) != 1 {
		t.Fatalf("want 1 (merged), got %d", len(loaded))
	}
	if loaded[0].Relation != "trở mặt" {
		t.Errorf("not updated: %+v", loaded[0])
	}
}

// ── StateChanges ──

func TestStateChanges_Append(t *testing.T) {
	s := newTestStore(t)
	_ = s.World.AppendStateChanges([]domain.StateChange{
		{Chapter: 1, Entity: "Trương Tam", Field: "realm", NewValue: "Luyện Khí kỳ"},
	})
	_ = s.World.AppendStateChanges([]domain.StateChange{
		{Chapter: 3, Entity: "Trương Tam", Field: "realm", OldValue: "Luyện Khí kỳ", NewValue: "Trúc Cơ kỳ"},
	})

	loaded, _ := s.World.LoadStateChanges()
	if len(loaded) != 2 {
		t.Fatalf("want 2, got %d", len(loaded))
	}
	if loaded[1].NewValue != "Trúc Cơ kỳ" {
		t.Errorf("second: %+v", loaded[1])
	}
}

func TestStateChanges_AppendIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	change := domain.StateChange{
		Chapter:  1,
		Entity:   "Trương Tam",
		Field:    "realm",
		OldValue: "phàm nhân",
		NewValue: "Luyện Khí kỳ",
	}
	_ = s.World.AppendStateChanges([]domain.StateChange{change})
	_ = s.World.AppendStateChanges([]domain.StateChange{change})

	loaded, _ := s.World.LoadStateChanges()
	if len(loaded) != 1 {
		t.Fatalf("duplicate state change should be ignored, got %d: %+v", len(loaded), loaded)
	}
}

func TestStateChanges_DedupKeyDoesNotCollideOnContent(t *testing.T) {
	s := newTestStore(t)
	changes := []domain.StateChange{
		{Chapter: 1, Entity: "a|b", Field: "c", OldValue: "d", NewValue: "e"},
		{Chapter: 1, Entity: "a", Field: "b|c", OldValue: "d", NewValue: "e"},
	}
	if err := s.World.AppendStateChanges(changes); err != nil {
		t.Fatalf("append: %v", err)
	}
	loaded, err := s.World.LoadStateChanges()
	if err != nil || len(loaded) != 2 {
		t.Fatalf("delimiter-bearing changes must remain distinct: got (%+v, %v)", loaded, err)
	}
}

func TestStateChanges_MigratesLegacyAndRemainsIdempotent(t *testing.T) {
	dir := t.TempDir()
	legacyStore := NewStore(dir)
	legacy := []domain.StateChange{{
		Chapter: 1, Entity: "Trương Tam", Field: "realm", NewValue: "Luyện Khí kỳ",
	}}
	if err := legacyStore.World.io.WriteJSON("meta/state_changes.json", legacy); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	change := domain.StateChange{
		Chapter: 2, Entity: "Trương Tam", Field: "realm", OldValue: "Luyện Khí kỳ", NewValue: "Trúc Cơ kỳ",
	}
	s := NewStore(dir)
	if err := s.World.AppendStateChanges([]domain.StateChange{change}); err != nil {
		t.Fatalf("append with migration: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta", "state_changes.json")); !os.IsNotExist(err) {
		t.Fatalf("legacy state_changes.json should be removed, err=%v", err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "meta", "state_changes.jsonl"))
	if err != nil {
		t.Fatalf("read state_changes.jsonl: %v", err)
	}

	next := domain.StateChange{
		Chapter: 3, Entity: "Trương Tam", Field: "realm", OldValue: "Trúc Cơ kỳ", NewValue: "Kim Đan kỳ",
	}
	if err := s.World.AppendStateChanges([]domain.StateChange{next}); err != nil {
		t.Fatalf("append jsonl: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "meta", "state_changes.jsonl"))
	if err != nil {
		t.Fatalf("read appended state_changes.jsonl: %v", err)
	}
	if !strings.HasPrefix(string(after), string(before)) || len(after) <= len(before) {
		t.Fatal("state_changes.jsonl should preserve old bytes and append new records")
	}

	// Store mới sau khi khôi phục chỉ mục từ log và phát lại cùng change thì số mục vẫn giữ nguyên.
	s2 := NewStore(dir)
	if err := s2.World.AppendStateChanges([]domain.StateChange{next}); err != nil {
		t.Fatalf("restart duplicate: %v", err)
	}
	loaded, err := s2.World.LoadStateChanges()
	if err != nil || len(loaded) != 3 {
		t.Fatalf("load migrated state changes: got (%+v, %v)", loaded, err)
	}
}

// ── StyleRules ──

func TestStyleRules_SaveAndLoad(t *testing.T) {
	s := newTestStore(t)
	rules := domain.WritingStyleRules{
		Volume: 1, Arc: 2,
		Prose:    []string{"ưu tiên câu ngắn"},
		Dialogue: []domain.CharacterVoice{{Name: "Trương Tam", Rules: []string{"thô mộc"}}},
		Taboos:   []string{"không dùng tiếng lóng mạng"},
	}
	_ = s.World.SaveStyleRules(rules)

	loaded, _ := s.World.LoadStyleRules()
	if loaded == nil || loaded.Volume != 1 || len(loaded.Dialogue) != 1 {
		t.Errorf("roundtrip failed: %+v", loaded)
	}
}

// ── Reviews ──

func TestReview_SaveAndLoad(t *testing.T) {
	s := newTestStore(t)
	_ = s.World.SaveReview(domain.ReviewEntry{Chapter: 3, Scope: "chapter", Verdict: "polish"})

	loaded, _ := s.World.LoadReview(3)
	if loaded == nil || loaded.Verdict != "polish" {
		t.Errorf("chapter review: %+v", loaded)
	}
}

func TestReview_GlobalScopeIsolation(t *testing.T) {
	s := newTestStore(t)
	_ = s.World.SaveReview(domain.ReviewEntry{Chapter: 5, Scope: "global", Verdict: "accept"})

	// chapter-scoped load không được tìm thấy global review
	if got, _ := s.World.LoadReview(5); got != nil {
		t.Errorf("chapter load should not find global: %+v", got)
	}
}

func TestReview_LoadLastReview(t *testing.T) {
	s := newTestStore(t)
	for _, ch := range []int{2, 5, 8} {
		_ = s.World.SaveReview(domain.ReviewEntry{Chapter: ch, Scope: "global", Verdict: "accept"})
	}

	for _, tt := range []struct {
		from, want int
	}{
		{10, 8}, {5, 5}, {3, 2},
	} {
		got, _ := s.World.LoadLastReview(tt.from)
		if got == nil || got.Chapter != tt.want {
			t.Errorf("LoadLastReview(%d): want ch%d, got %+v", tt.from, tt.want, got)
		}
	}
	// from=1 không tìm thấy
	if got, _ := s.World.LoadLastReview(1); got != nil {
		t.Errorf("from=1 should be nil, got %+v", got)
	}
}

// ── WorldRules ──

func TestWorldRules_SaveAndLoad(t *testing.T) {
	s := newTestStore(t)
	rules := []domain.WorldRule{
		{Category: "magic", Rule: "pháp thuật tiêu hao tinh thần lực", Boundary: "cạn tinh thần lực sẽ hôn mê"},
		{Category: "society", Rule: "quý tộc có quyền phán xử", Boundary: "không được vượt quyền"},
	}
	_ = s.World.SaveWorldRules(rules)

	if _, err := os.Stat(filepath.Join(s.Dir(), "world_rules.json")); err != nil {
		t.Fatalf("json not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir(), "world_rules.md")); err != nil {
		t.Fatalf("md not created: %v", err)
	}

	loaded, _ := s.World.LoadWorldRules()
	if len(loaded) != 2 || loaded[0].Rule != "pháp thuật tiêu hao tinh thần lực" {
		t.Errorf("roundtrip: %+v", loaded)
	}
}

func TestRenderWorldRules(t *testing.T) {
	md := renderWorldRules([]domain.WorldRule{
		{Category: "magic", Rule: "pháp thuật tiêu hao tinh thần lực", Boundary: "cạn tinh thần lực sẽ hôn mê"},
		{Category: "society", Rule: "quý tộc có quyền phán xử"},
		{Category: "magic", Rule: "cấm chú cần ba người", Boundary: "thi triển một mình sẽ chết"},
	})

	// nhóm magic phải đứng trước society
	if strings.Index(md, "## magic") >= strings.Index(md, "## society") {
		t.Error("magic should appear before society")
	}
	if !strings.Contains(md, "Ranh giới: cạn tinh thần lực sẽ hôn mê") {
		t.Error("missing boundary")
	}
	// không có boundary thì không được xuất dòng ranh giới rỗng
	if strings.Contains(md, "Ranh giới: \n") {
		t.Error("empty boundary rendered")
	}
}

// TestRuleViolationsContract hợp đồng lưu fact vi phạm (lần review thứ năm):
// cùng chương bản mới nhất phủ bản cũ; sau viết lại, danh sách rỗng được xem là đã sạch; đọc được qua restart.
func TestRuleViolationsContract(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.World.SaveRuleViolations(3, []rules.Violation{
		{Rule: "fatigue_words", Target: "không kìm được", Actual: 9, Severity: rules.SeverityWarning},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := s.World.LoadRuleViolations(3); len(got) != 1 || got[0].Target != "không kìm được" {
		t.Fatalf("đọc lần đầu: %+v", got)
	}

	// cùng chương viết lại: bản mới nhất (danh sách rỗng = đã sạch) phủ vi phạm cũ
	if err := s.World.SaveRuleViolations(3, nil); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	if got := s.World.LoadRuleViolations(3); len(got) != 0 {
		t.Fatalf("vi phạm cũ phải được xóa sau khi viết lại: %+v", got)
	}

	// chương khác không bị ảnh hưởng + đọc được qua restart (Store instance mới)
	if err := s.World.SaveRuleViolations(5, []rules.Violation{{Rule: "forbidden_phrases", Target: "ở mức độ nào đó", Actual: 2, Severity: rules.SeverityWarning}}); err != nil {
		t.Fatalf("save ch5: %v", err)
	}
	s2 := NewStore(dir)
	if got := s2.World.LoadRuleViolations(5); len(got) != 1 || got[0].Rule != "forbidden_phrases" {
		t.Fatalf("đọc qua restart: %+v", got)
	}
	if got := s2.World.LoadRuleViolations(99); got != nil {
		t.Fatalf("chương không có bản ghi phải trả về nil: %+v", got)
	}
}
