package store

import (
	"os"
	"slices"
	"sort"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// CastStore quản lý danh sách vai phụ (meta/cast_ledger.json).
//
// Danh sách vai phụ ghi lại "những nhân vật thứ yếu có tên đã từng xuất hiện", và trực giao với characters.json (hồ sơ nhân vật cốt lõi):
//   - characters.json: Architect thiết kế rõ ràng nhân vật chính + vai phụ then chốt, không sửa đổi trong giai đoạn viết
//   - cast_ledger.json: công cụ commit_chapter tự động cộng dồn, tất cả vai phụ không cốt lõi có tên
//
// MergeAppearances là idempotent: cùng một chương nếu commit lặp lại sẽ không cộng trùng AppearanceCount.
type CastStore struct{ io *IO }

func NewCastStore(io *IO) *CastStore { return &CastStore{io: io} }

const castLedgerPath = "meta/cast_ledger.json"

// Load đọc danh sách vai phụ. Khi tệp không tồn tại thì trả về lát cắt rỗng.
func (s *CastStore) Load() ([]domain.CastEntry, error) {
	var entries []domain.CastEntry
	if err := s.io.ReadJSON(castLedgerPath, &entries); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// Save lưu toàn bộ danh sách vai phụ (ghi nguyên tử).
func (s *CastStore) Save(entries []domain.CastEntry) error {
	return s.io.WriteJSON(castLedgerPath, entries)
}

// MergeAppearances hợp nhất các lần xuất hiện của chương này vào danh sách.
//
// Tham số:
//   - chapter: số chương
//   - characters: mảng tên xuất hiện trong chương này (từ commit_chapter.Characters)
//   - intros: phần giới thiệu nhân vật mới do Writer khai báo rõ ràng (lần xuất hiện đầu tiên hoặc bổ sung BriefRole)
//   - knownCore: tập hợp tên nhân vật cốt lõi đã có trong characters.json (những tên này sẽ bỏ qua khi ghi ledger)
//
// Hành vi:
//   - Tên nằm trong knownCore: bỏ qua (hồ sơ nhân vật cốt lõi là điểm ghi duy nhất của chúng)
//   - Tên đã có trong ledger và chapter đã có trong AppearanceChapters: bỏ qua hoàn toàn (idempotent)
//   - Tên đã có trong ledger nhưng chapter là mới: cập nhật LastSeenChapter + thêm chapter + count++
//   - Tên chưa có trong ledger: thêm mục mới
//   - BriefRole trong intros chỉ được dùng khi BriefRole của mục ledger vẫn trống, để tránh ghi đè phần giới thiệu sớm hơn
func (s *CastStore) MergeAppearances(
	chapter int,
	characters []string,
	intros []domain.CastIntro,
	knownCore map[string]bool,
) error {
	if chapter <= 0 || len(characters) == 0 {
		return nil
	}
	return s.io.WithWriteLock(func() error {
		var entries []domain.CastEntry
		if err := s.io.ReadJSONUnlocked(castLedgerPath, &entries); err != nil && !os.IsNotExist(err) {
			return err
		}

		introMap := make(map[string]string, len(intros))
		for _, in := range intros {
			if in.Name != "" {
				introMap[in.Name] = in.BriefRole
			}
		}

		index := make(map[string]int, len(entries))
		for i, e := range entries {
			index[e.Name] = i
			for _, alias := range e.Aliases {
				index[alias] = i
			}
		}

		seen := make(map[string]bool, len(characters))
		for _, name := range characters {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			if knownCore[name] {
				continue
			}
			if i, ok := index[name]; ok {
				entry := &entries[i]
				if !slices.Contains(entry.AppearanceChapters, chapter) {
					entry.AppearanceChapters = append(entry.AppearanceChapters, chapter)
					entry.AppearanceCount = len(entry.AppearanceChapters)
					if chapter > entry.LastSeenChapter {
						entry.LastSeenChapter = chapter
					}
					if chapter < entry.FirstSeenChapter || entry.FirstSeenChapter == 0 {
						entry.FirstSeenChapter = chapter
					}
				}
				if entry.BriefRole == "" {
					if br, ok := introMap[name]; ok && br != "" {
						entry.BriefRole = br
					}
				}
				continue
			}
			entries = append(entries, domain.CastEntry{
				Name:               name,
				BriefRole:          introMap[name],
				FirstSeenChapter:   chapter,
				LastSeenChapter:    chapter,
				AppearanceCount:    1,
				AppearanceChapters: []int{chapter},
			})
		}
		return s.io.WriteJSONUnlocked(castLedgerPath, entries)
	})
}

// RecentActive trả về N mục vai phụ hoạt động gần đây nhất (theo LastSeenChapter giảm dần).
// Dùng cho novel_context gọi lại các "vai phụ vừa xuất hiện" mà Writer có thể cần khi viết chương tiếp theo.
//
// Các mục đã được thăng cấp lên characters.json (Promoted=true) sẽ bị bỏ qua để tránh gọi lại trùng với hồ sơ cốt lõi.
func (s *CastStore) RecentActive(limit int) ([]domain.CastEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	entries, err := s.Load()
	if err != nil {
		return nil, err
	}
	active := entries[:0:0]
	for _, e := range entries {
		if e.Promoted {
			continue
		}
		active = append(active, e)
	}
	if len(active) == 0 {
		return nil, nil
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].LastSeenChapter != active[j].LastSeenChapter {
			return active[i].LastSeenChapter > active[j].LastSeenChapter
		}
		return active[i].AppearanceCount > active[j].AppearanceCount
	})
	if len(active) > limit {
		active = active[:limit]
	}
	return active, nil
}
