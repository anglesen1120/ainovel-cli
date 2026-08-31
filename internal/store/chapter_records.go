package store

import (
	"fmt"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/voocel/ainovel-cli/internal/domain"
)

const chapterRecordsDir = "meta/chapter_records"

type ChapterRecordStore struct{ io *IO }

func NewChapterRecordStore(io *IO) *ChapterRecordStore { return &ChapterRecordStore{io: io} }

func ChapterRecordPath(chapter int) string {
	return fmt.Sprintf("%s/%06d.json", chapterRecordsDir, chapter)
}

func (s *ChapterRecordStore) Load(chapter int) (*domain.ChapterRecord, error) {
	var record domain.ChapterRecord
	if err := s.io.ReadJSON(ChapterRecordPath(chapter), &record); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := validateChapterRecord(record); err != nil {
		return nil, fmt.Errorf("đọc bản ghi tiếp nhận chương %d: %w", chapter, err)
	}
	if record.Chapter != chapter {
		return nil, fmt.Errorf("đọc bản ghi tiếp nhận chương %d: số chương trong bản ghi là %d", chapter, record.Chapter)
	}
	return &record, nil
}

func (s *ChapterRecordStore) Save(record domain.ChapterRecord) error {
	if err := validateChapterRecord(record); err != nil {
		return err
	}
	return s.io.WriteJSON(ChapterRecordPath(record.Chapter), record)
}

// Prepare tạo bản ghi chương phiên bản tiếp theo nhưng không ghi xuống đĩa, để các bất biến xuyên bản ghi được kiểm tra xong trước khi ghi.
func (s *ChapterRecordStore) Prepare(chapter int, origin domain.ChapterOrigin, content string, facts domain.ChapterFacts, style domain.StyleDelta) (*domain.ChapterRecord, error) {
	existing, err := s.Load(chapter)
	if err != nil {
		return nil, err
	}
	record, _ := prepareChapterRecord(existing, chapter, origin, content, facts, style)
	return record, nil
}

func (s *ChapterRecordStore) Accept(chapter int, origin domain.ChapterOrigin, content string, facts domain.ChapterFacts, style domain.StyleDelta) (*domain.ChapterRecord, error) {
	existing, err := s.Load(chapter)
	if err != nil {
		return nil, err
	}
	record, changed := prepareChapterRecord(existing, chapter, origin, content, facts, style)
	if !changed {
		return record, nil
	}
	if err := s.Save(*record); err != nil {
		return nil, err
	}
	return record, nil
}

func prepareChapterRecord(existing *domain.ChapterRecord, chapter int, origin domain.ChapterOrigin, content string, facts domain.ChapterFacts, style domain.StyleDelta) (*domain.ChapterRecord, bool) {
	digest := domain.ChapterContentSHA256(content)
	revision := 1
	if existing != nil {
		// Khi ghi đè bản ghi cũ, giữ lại các phục bút do chính chương này gieo: viết lại chỉ thay nội dung chính, không nên xóa các sự kiện gieo phục bút của chương này.
		facts.ForeshadowUpdates = domain.RestoreOwnPlants(existing.Facts.ForeshadowUpdates, facts.ForeshadowUpdates)
		if existing.ContentSHA256 == digest && existing.Origin == origin && reflect.DeepEqual(existing.Facts, facts) && reflect.DeepEqual(existing.StyleDelta, style) {
			return existing, false
		}
		revision = existing.Revision + 1
	}
	record := domain.ChapterRecord{
		Version:       domain.ChapterRecordVersion,
		Chapter:       chapter,
		Revision:      revision,
		Origin:        origin,
		Content:       domain.NormalizeChapterContent(content),
		ContentSHA256: digest,
		Facts:         facts,
		StyleDelta:    style,
		AcceptedAt:    time.Now(),
	}
	return &record, true
}

func (s *ChapterRecordStore) LoadCompleted(chapters []int) ([]domain.ChapterRecord, error) {
	numbers := slices.Clone(chapters)
	slices.Sort(numbers)
	records := make([]domain.ChapterRecord, 0, len(numbers))
	for _, chapter := range numbers {
		record, err := s.Load(chapter)
		if err != nil {
			return nil, err
		}
		if record == nil {
			return nil, fmt.Errorf("chương %d thiếu bản ghi tiếp nhận", chapter)
		}
		records = append(records, *record)
	}
	return records, nil
}

func validateChapterRecord(record domain.ChapterRecord) error {
	switch {
	case record.Version != domain.ChapterRecordVersion:
		return fmt.Errorf("phiên bản bản ghi chương %d không được hỗ trợ", record.Version)
	case record.Chapter <= 0:
		return fmt.Errorf("chapter phải > 0")
	case record.Revision <= 0:
		return fmt.Errorf("revision phải > 0")
	case record.Origin != domain.ChapterOriginGenerated && record.Origin != domain.ChapterOriginUser:
		return fmt.Errorf("nguồn gốc chương không hợp lệ %q", record.Origin)
	case record.ContentSHA256 != domain.ChapterContentSHA256(record.Content):
		return fmt.Errorf("digest nội dung không khớp")
	case record.AcceptedAt.IsZero():
		return fmt.Errorf("accepted_at là bắt buộc")
	}
	return nil
}
