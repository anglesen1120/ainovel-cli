package store

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
)

// WorldStore quản lý dòng thời gian, mồi báo trước, quan hệ nhân vật, thay đổi trạng thái, quy tắc thế giới, quy tắc phong cách, duyệt xét và bàn giao.
type WorldStore struct {
	io *IO

	timeline                *appendLog[domain.TimelineEvent]
	stateChanges            *appendLog[domain.StateChange]
	timelineProjectionReady bool
}

func NewWorldStore(io *IO) *WorldStore {
	return &WorldStore{
		io: io,
		timeline: newAppendLog(
			"timeline.jsonl",
			"timeline.json",
			timelineEventKey,
			cloneTimelineEvent,
		),
		stateChanges: newAppendLog(
			"meta/state_changes.jsonl",
			"meta/state_changes.json",
			stateChangeKey,
			func(change domain.StateChange) domain.StateChange { return change },
		),
	}
}

// ── Dòng thời gian ──

// SaveTimeline thay thế toàn bộ fact dòng thời gian và projection cho người đọc.
func (s *WorldStore) SaveTimeline(events []domain.TimelineEvent) error {
	return s.io.WithWriteLock(func() error {
		if err := s.timeline.replaceUnlocked(s.io, events); err != nil {
			s.timelineProjectionReady = false
			return err
		}
		if err := s.io.WriteMarkdownUnlocked("timeline.md", renderTimeline(events)); err != nil {
			s.timelineProjectionReady = false
			return err
		}
		s.timelineProjectionReady = true
		return nil
	})
}

// LoadTimeline đọc dòng thời gian.
func (s *WorldStore) LoadTimeline() ([]domain.TimelineEvent, error) {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.timeline.allUnlocked(s.io)
}

// AppendTimelineEvents append sự kiện dòng thời gian. Khi cùng sự kiện được submit lặp lại, dùng key ổn định để khử trùng, bảo đảm
// chạy lại sau crash commit_chapter không làm bẩn dòng thời gian.
func (s *WorldStore) AppendTimelineEvents(newEvents []domain.TimelineEvent) error {
	return s.io.WithWriteLock(func() error {
		if !s.timelineProjectionReady {
			existing, err := s.timeline.allUnlocked(s.io)
			if err != nil {
				return err
			}
			if err := s.ensureTimelineProjectionUnlocked(existing); err != nil {
				return err
			}
		}

		added, err := s.timeline.appendUnlocked(s.io, newEvents)
		if err != nil {
			// khi append lỗi, trên đĩa có thể đã có một phần bản ghi hoàn chỉnh; trước khi phát lại phải
			// dựng lại projection từ fact log, không được chỉ dựa vào giá trị trả về của added.
			s.timelineProjectionReady = false
			return err
		}
		if len(added) == 0 {
			return nil
		}
		if err := s.io.AppendLineUnlocked("timeline.md", []byte(renderTimelineEntries(added))); err != nil {
			s.timelineProjectionReady = false
			return err
		}
		return nil
	})
}

// ensureTimelineProjectionUnlocked đối chiếu timeline.md khi tiến trình append lần đầu hoặc sau lần projection trước bị lỗi.
// đường bình thường chỉ đối chiếu toàn bộ một lần, sau đó mỗi chương append đồng bộ với JSONL; projection không tham gia đọc fact.
func (s *WorldStore) ensureTimelineProjectionUnlocked(events []domain.TimelineEvent) error {
	if s.timelineProjectionReady {
		return nil
	}
	expected := renderTimeline(events)
	actual, err := s.io.ReadFileUnlocked("timeline.md")
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if string(actual) != expected {
		if err := s.io.WriteMarkdownUnlocked("timeline.md", expected); err != nil {
			return err
		}
	}
	s.timelineProjectionReady = true
	return nil
}

// LoadRecentTimeline trả về sự kiện dòng thời gian trong window chương gần nhất.
func (s *WorldStore) LoadRecentTimeline(current, window int) ([]domain.TimelineEvent, error) {
	all, err := s.LoadTimeline()
	if err != nil {
		return nil, err
	}
	minCh := max(current-window, 1)
	var filtered []domain.TimelineEvent
	for _, e := range all {
		if e.Chapter >= minCh {
			filtered = append(filtered, e)
		}
	}
	return filtered, nil
}

// ── mồi báo trước ──

// SaveForeshadowLedger ghi toàn bộ foreshadow_ledger.json + foreshadow_ledger.md (atomic write).
func (s *WorldStore) SaveForeshadowLedger(entries []domain.ForeshadowEntry) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("foreshadow_ledger.json", entries); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("foreshadow_ledger.md", renderForeshadow(entries))
	})
}

// LoadForeshadowLedger đọc sổ mồi báo trước.
func (s *WorldStore) LoadForeshadowLedger() ([]domain.ForeshadowEntry, error) {
	var entries []domain.ForeshadowEntry
	if err := s.io.ReadJSON("foreshadow_ledger.json", &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// UpdateForeshadow áp dụng hàng loạt thao tác delta mồi báo trước.
func (s *WorldStore) UpdateForeshadow(chapter int, updates []domain.ForeshadowUpdate) error {
	return s.io.WithWriteLock(func() error {
		var entries []domain.ForeshadowEntry
		if err := s.io.ReadJSONUnlocked("foreshadow_ledger.json", &entries); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		idx := make(map[string]int, len(entries))
		for i, e := range entries {
			idx[e.ID] = i
		}
		for _, u := range updates {
			if strings.TrimSpace(u.ID) == "" {
				return fmt.Errorf("foreshadow id không được rỗng")
			}
			switch u.Action {
			case "plant":
				if strings.TrimSpace(u.Description) == "" {
					return fmt.Errorf("plant foreshadow %q cần có description", u.ID)
				}
				if i, ok := idx[u.ID]; ok {
					if entries[i].Description == "" {
						entries[i].Description = u.Description
					}
					if entries[i].PlantedAt == 0 {
						entries[i].PlantedAt = chapter
					}
					if entries[i].Status == "" {
						entries[i].Status = "planted"
					}
					continue
				}
				idx[u.ID] = len(entries)
				entries = append(entries, domain.ForeshadowEntry{
					ID:          u.ID,
					Description: u.Description,
					PlantedAt:   chapter,
					Status:      "planted",
				})
			case "advance":
				if i, ok := idx[u.ID]; ok {
					entries[i].Status = "advanced"
				} else {
					return fmt.Errorf("không tìm thấy foreshadow %q để advance", u.ID)
				}
			case "resolve":
				if i, ok := idx[u.ID]; ok {
					entries[i].Status = "resolved"
					entries[i].ResolvedAt = chapter
				} else {
					return fmt.Errorf("không tìm thấy foreshadow %q để resolve", u.ID)
				}
			default:
				return fmt.Errorf("action foreshadow không hợp lệ %q", u.Action)
			}
		}
		if err := s.io.WriteJSONUnlocked("foreshadow_ledger.json", entries); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("foreshadow_ledger.md", renderForeshadow(entries))
	})
}

// LoadActiveForeshadow trả về các mục mồi báo trước chưa thu hồi.
func (s *WorldStore) LoadActiveForeshadow() ([]domain.ForeshadowEntry, error) {
	all, err := s.LoadForeshadowLedger()
	if err != nil {
		return nil, err
	}
	var active []domain.ForeshadowEntry
	for _, e := range all {
		if e.Status != "resolved" {
			active = append(active, e)
		}
	}
	return active, nil
}

// ── Quan hệ nhân vật ──

// SaveRelationships ghi toàn bộ relationship_state.json + relationship_state.md (atomic write).
func (s *WorldStore) SaveRelationships(entries []domain.RelationshipEntry) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("relationship_state.json", entries); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("relationship_state.md", renderRelationships(entries))
	})
}

// LoadRelationships đọc trạng thái quan hệ nhân vật.
func (s *WorldStore) LoadRelationships() ([]domain.RelationshipEntry, error) {
	var entries []domain.RelationshipEntry
	if err := s.io.ReadJSON("relationship_state.json", &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// UpdateRelationships gộp thay đổi quan hệ.
func (s *WorldStore) UpdateRelationships(changes []domain.RelationshipEntry) error {
	return s.io.WithWriteLock(func() error {
		var existing []domain.RelationshipEntry
		if err := s.io.ReadJSONUnlocked("relationship_state.json", &existing); err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		}
		idx := make(map[string]int, len(existing))
		for i, e := range existing {
			idx[pairKey(e.CharacterA, e.CharacterB)] = i
		}
		for _, c := range changes {
			key := pairKey(c.CharacterA, c.CharacterB)
			if i, ok := idx[key]; ok {
				existing[i].Relation = c.Relation
				existing[i].Chapter = c.Chapter
			} else {
				idx[key] = len(existing)
				existing = append(existing, c)
			}
		}
		if err := s.io.WriteJSONUnlocked("relationship_state.json", existing); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("relationship_state.md", renderRelationships(existing))
	})
}

// ── Thay đổi trạng thái ──

// AppendStateChanges append thay đổi trạng thái nhân vật. Khi cùng thay đổi trạng thái được submit lặp lại, dùng key ổn định để khử trùng.
func (s *WorldStore) AppendStateChanges(changes []domain.StateChange) error {
	return s.io.WithWriteLock(func() error {
		_, err := s.stateChanges.appendUnlocked(s.io, changes)
		return err
	})
}

// LoadStateChanges đọc toàn bộ bản ghi thay đổi trạng thái.
func (s *WorldStore) LoadStateChanges() ([]domain.StateChange, error) {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.stateChanges.allUnlocked(s.io)
}

// SaveStateChanges thay thế toàn bộ fact thay đổi trạng thái để dựng lại projection sau khi sửa chương.
func (s *WorldStore) SaveStateChanges(changes []domain.StateChange) error {
	return s.io.WithWriteLock(func() error {
		return s.stateChanges.replaceUnlocked(s.io, changes)
	})
}

// ── Quy tắc thế giới ──

// SaveWorldRules ghi toàn bộ world_rules.json + world_rules.md (atomic write).
func (s *WorldStore) SaveWorldRules(rules []domain.WorldRule) error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.WriteJSONUnlocked("world_rules.json", rules); err != nil {
			return err
		}
		return s.io.WriteMarkdownUnlocked("world_rules.md", renderWorldRules(rules))
	})
}

// LoadWorldRules đọc quy tắc thế giới.
func (s *WorldStore) LoadWorldRules() ([]domain.WorldRule, error) {
	var rules []domain.WorldRule
	if err := s.io.ReadJSON("world_rules.json", &rules); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return rules, nil
}

// ── Quy tắc phong cách ──

// SaveStyleRules lưu quy tắc phong cách viết.
func (s *WorldStore) SaveStyleRules(rules domain.WritingStyleRules) error {
	return s.io.WriteJSON("meta/style_rules.json", rules)
}

// LoadStyleRules đọc quy tắc phong cách viết.
func (s *WorldStore) LoadStyleRules() (*domain.WritingStyleRules, error) {
	var rules domain.WritingStyleRules
	if err := s.io.ReadJSON("meta/style_rules.json", &rules); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rules, nil
}

func (s *WorldStore) SaveAuthorRevisionStyle(style domain.AuthorRevisionStyle) error {
	return s.io.WriteJSON("meta/author_revision_style.json", style)
}

func (s *WorldStore) LoadAuthorRevisionStyle() (*domain.AuthorRevisionStyle, error) {
	var style domain.AuthorRevisionStyle
	if err := s.io.ReadJSON("meta/author_revision_style.json", &style); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &style, nil
}

// ── Duyệt xét ──

// SaveReview lưu kết quả duyệt xét.
func (s *WorldStore) SaveReview(r domain.ReviewEntry) error {
	rel := fmt.Sprintf("reviews/%02d.json", r.Chapter)
	if r.Scope == "global" {
		rel = fmt.Sprintf("reviews/%02d-global.json", r.Chapter)
	}
	return s.io.WriteJSON(rel, r)
}

// HasArcReview kiểm tra chương chỉ định (chương cuối arc) đã lưu review scope=arc chưa.
func (s *WorldStore) HasArcReview(chapter int) (bool, error) {
	rv, err := s.LoadReview(chapter)
	if err != nil {
		return false, err
	}
	return rv != nil && rv.Scope == "arc", nil
}

// HasGlobalReview kiểm tra chương chỉ định đã lưu duyệt xét toàn cục scope=global chưa
// (save_review ghi xuống reviews/%02d-global.json; sách không phân tầng kích hoạt theo ReviewInterval).
func (s *WorldStore) HasGlobalReview(chapter int) (bool, error) {
	r, err := s.LoadGlobalReview(chapter)
	if err != nil {
		return false, err
	}
	return r != nil && r.Scope == "global", nil
}

// LoadGlobalReview đọc duyệt xét toàn cục của chương kết thúc chỉ định.
func (s *WorldStore) LoadGlobalReview(chapter int) (*domain.ReviewEntry, error) {
	var r domain.ReviewEntry
	if err := s.io.ReadJSON(fmt.Sprintf("reviews/%02d-global.json", chapter), &r); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// LoadReview đọc kết quả duyệt xét chương.
func (s *WorldStore) LoadReview(chapter int) (*domain.ReviewEntry, error) {
	var r domain.ReviewEntry
	if err := s.io.ReadJSON(fmt.Sprintf("reviews/%02d.json", chapter), &r); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// LoadLastReview đọc lần duyệt xét toàn cục gần nhất.
func (s *WorldStore) LoadLastReview(fromChapter int) (*domain.ReviewEntry, error) {
	for ch := fromChapter; ch >= 1; ch-- {
		var r domain.ReviewEntry
		if err := s.io.ReadJSON(fmt.Sprintf("reviews/%02d-global.json", ch), &r); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		return &r, nil
	}
	return nil, nil
}

// LoadReviewsAffectingChapter trả về mọi review đưa chapter vào hàng đợi làm lại một cách rõ ràng,
// sắp xếp mới đến cũ. Review arc/global nằm ở điểm cuối review, không thể tra theo tên file chương mục tiêu.
func (s *WorldStore) LoadReviewsAffectingChapter(chapter int) ([]domain.ReviewEntry, error) {
	entries, err := os.ReadDir(s.io.path("reviews"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var reviews []domain.ReviewEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(s.io.path("reviews/" + entry.Name()))
		if err != nil {
			return nil, err
		}
		var review domain.ReviewEntry
		if err := json.Unmarshal(data, &review); err != nil {
			return nil, fmt.Errorf("phân tích reviews/%s: %w", entry.Name(), err)
		}
		if slices.Contains(review.AffectedChapters, chapter) ||
			(review.Scope == "chapter" && review.Chapter == chapter && review.Verdict != "accept" && len(review.AffectedChapters) == 0) {
			reviews = append(reviews, review)
		}
	}
	slices.SortFunc(reviews, func(a, b domain.ReviewEntry) int {
		return b.Chapter - a.Chapter
	})
	return reviews, nil
}

// ── render helpers ──

func pairKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "|" + b
}

func timelineEventKey(e domain.TimelineEvent) string {
	chars := append([]string(nil), e.Characters...)
	slices.Sort(chars)
	parts := make([]string, 0, len(chars)+2)
	parts = append(parts, e.Time, e.Event)
	parts = append(parts, chars...)
	return stableRecordKey(e.Chapter, parts...)
}

func cloneTimelineEvent(event domain.TimelineEvent) domain.TimelineEvent {
	event.Characters = append([]string(nil), event.Characters...)
	return event
}

func stateChangeKey(c domain.StateChange) string {
	return stableRecordKey(c.Chapter, c.Entity, c.Field, c.OldValue, c.NewValue)
}

// stableRecordKey dùng tiền tố độ dài để mã hóa text biến đổi, tránh separator trong nội dung gây đụng key khử trùng.
func stableRecordKey(chapter int, parts ...string) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(chapter))
	for _, part := range parts {
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(len(part)))
		b.WriteByte(':')
		b.WriteString(part)
	}
	return b.String()
}

func renderTimeline(events []domain.TimelineEvent) string {
	var b strings.Builder
	b.WriteString("# Dòng thời gian\n\n")
	b.WriteString(renderTimelineEntries(events))
	return b.String()
}

func renderTimelineEntries(events []domain.TimelineEvent) string {
	var b strings.Builder
	for _, e := range events {
		chars := ""
		if len(e.Characters) > 0 {
			chars = "（" + strings.Join(e.Characters, "、") + "）"
		}
		fmt.Fprintf(&b, "- **Chương %d [%s]**: %s%s\n", e.Chapter, e.Time, e.Event, chars)
	}
	return b.String()
}

func renderForeshadow(entries []domain.ForeshadowEntry) string {
	var b strings.Builder
	b.WriteString("# Sổ mồi báo trước\n\n")
	for _, e := range entries {
		status := e.Status
		if e.ResolvedAt > 0 {
			status = fmt.Sprintf("đã thu hồi (chương %d)", e.ResolvedAt)
		}
		fmt.Fprintf(&b, "- **[%s]** %s — gài ở chương %d, trạng thái: %s\n",
			e.ID, e.Description, e.PlantedAt, status)
	}
	return b.String()
}

func renderRelationships(entries []domain.RelationshipEntry) string {
	var b strings.Builder
	b.WriteString("# Quan hệ nhân vật\n\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "- **%s ↔ %s**: %s（Chương %d）\n",
			e.CharacterA, e.CharacterB, e.Relation, e.Chapter)
	}
	return b.String()
}

func renderWorldRules(rules []domain.WorldRule) string {
	grouped := make(map[string][]domain.WorldRule)
	var order []string
	for _, r := range rules {
		cat := r.Category
		if cat == "" {
			cat = "other"
		}
		if _, exists := grouped[cat]; !exists {
			order = append(order, cat)
		}
		grouped[cat] = append(grouped[cat], r)
	}

	var b strings.Builder
	b.WriteString("# Quy tắc thế giới\n\n")
	for _, cat := range order {
		fmt.Fprintf(&b, "## %s\n\n", cat)
		for _, r := range grouped[cat] {
			fmt.Fprintf(&b, "- **Quy tắc**: %s\n", r.Rule)
			if r.Boundary != "" {
				fmt.Fprintf(&b, "  - Ranh giới: %s\n", r.Boundary)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ── Fact vi phạm cơ học theo chương ──
//
// rule_violations của commit_chapter (kết quả mức warning từ kiểm tra cơ học user_rules) được lưu bền vững,
// editor đọc qua novel_context(chapter=N) khi review chương đó và ánh xạ vào review bảy chiều
// (editor.md §ánh xạ kiểm tra cơ học). writer cũng thấy khi làm lại chương đó. Dạng append-only; cùng chương lấy bản mới nhất.

// ChapterViolations là bản ghi vi phạm cơ học của một chương.
type ChapterViolations struct {
	Chapter    int               `json:"chapter"`
	Violations []rules.Violation `json:"violations"`
	At         string            `json:"at"`
}

const ruleViolationsFile = "meta/rule_violations.jsonl"

// SaveRuleViolations append vi phạm cơ học của một chương (danh sách rỗng cũng append — phủ bản cũ, biểu thị đã sạch sau khi viết lại).
func (s *WorldStore) SaveRuleViolations(chapter int, violations []rules.Violation) error {
	rec := ChapterViolations{Chapter: chapter, Violations: violations, At: time.Now().Format(time.RFC3339)}
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.io.AppendLine(ruleViolationsFile, append(data, '\n'))
}

// LoadRuleViolations đọc bản ghi vi phạm cơ học mới nhất của một chương; không có thì trả về nil.
func (s *WorldStore) LoadRuleViolations(chapter int) []rules.Violation {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	data, err := os.ReadFile(s.io.path(ruleViolationsFile))
	if err != nil {
		return nil
	}
	var latest []rules.Violation
	var found bool
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec ChapterViolations
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Chapter == chapter {
			latest, found = rec.Violations, true
		}
	}
	if !found {
		return nil
	}
	return latest
}
