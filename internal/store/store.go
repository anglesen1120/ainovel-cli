package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
)

// Store là gốc kết hợp của quản lý trạng thái, giữ tất cả các kho con.
type Store struct {
	dir string

	Progress       *ProgressStore
	Book           *BookStore
	Outline        *OutlineStore
	Drafts         *DraftStore
	Summaries      *SummaryStore
	RunMeta        *RunMetaStore
	UserRules      *UserRulesStore
	Signals        *SignalStore
	Runtime        *RuntimeStore
	Characters     *CharacterStore
	Cast           *CastStore
	World          *WorldStore
	Checkpoints    *CheckpointStore
	Sessions       *SessionStore
	Usage          *UsageStore
	Simulation     *SimulationStore
	Decisions      *DecisionStore
	ChapterRecords *ChapterRecordStore
	Revisions      *RevisionStore

	crossMu sync.Mutex // Đồng bộ hóa điều phối liên miền; không đại diện cho tính nguyên tử giao dịch trên nhiều tệp
}

const (
	LegacyProjectFormatVersion  = 1
	CurrentProjectFormatVersion = 2
	projectFormatPath           = "meta/format.json"
)

type projectFormat struct {
	Version int `json:"version"`
}

// NewStore tạo trình quản lý trạng thái; dir là thư mục gốc đầu ra của tiểu thuyết.
func NewStore(dir string) *Store {
	io := newIO(dir)
	outline := NewOutlineStore(io)
	return &Store{
		dir:            dir,
		Progress:       NewProgressStore(newIO(dir)),
		Book:           NewBookStore(newIO(dir)),
		Outline:        outline,
		Drafts:         NewDraftStore(newIO(dir)),
		Summaries:      NewSummaryStore(newIO(dir), outline),
		RunMeta:        NewRunMetaStore(newIO(dir)),
		UserRules:      NewUserRulesStore(newIO(dir)),
		Signals:        NewSignalStore(newIO(dir)),
		Runtime:        NewRuntimeStore(newIO(dir)),
		Characters:     NewCharacterStore(newIO(dir), outline),
		Cast:           NewCastStore(newIO(dir)),
		World:          NewWorldStore(newIO(dir)),
		Checkpoints:    NewCheckpointStore(io),
		Sessions:       NewSessionStore(newIO(dir)),
		Usage:          NewUsageStore(newIO(dir)),
		Simulation:     NewSimulationStore(newIO(dir)),
		Decisions:      NewDecisionStore(newIO(dir)),
		ChapterRecords: NewChapterRecordStore(newIO(dir)),
		Revisions:      NewRevisionStore(newIO(dir)),
	}
}

// Dir trả về thư mục gốc đầu ra.
func (s *Store) Dir() string { return s.dir }

// LoadProjectFormatVersion trả về phiên bản định dạng dữ liệu của thư mục tác phẩm. Tác phẩm cũ không có tệp phiên bản,
// được xem là v1, và sẽ được nâng cấp thống nhất bởi quá trình di trú khi khởi động; mã nghiệp vụ không cần giữ nhánh định dạng cũ.
func (s *Store) LoadProjectFormatVersion() (int, error) {
	var format projectFormat
	if err := s.Progress.io.ReadJSON(projectFormatPath, &format); err != nil {
		if os.IsNotExist(err) {
			return LegacyProjectFormatVersion, nil
		}
		return 0, err
	}
	if format.Version <= 0 {
		return 0, fmt.Errorf("phiên bản định dạng dự án không hợp lệ: %d", format.Version)
	}
	return format.Version, nil
}

// SaveProjectFormatVersion cập nhật nguyên tử phiên bản định dạng dự án sau khi một lần di trú hoàn tất.
func (s *Store) SaveProjectFormatVersion(version int) error {
	if version <= 0 {
		return fmt.Errorf("phiên bản định dạng dự án phải lớn hơn 0: %d", version)
	}
	return s.Progress.io.WriteJSON(projectFormatPath, projectFormat{Version: version})
}

// CheckConsistency thực hiện kiểm tra nông trên tầng sự thật, dùng để tạo cảnh báo khi khởi động/khôi phục.
// Chỉ đọc thuần túy: không sửa dữ liệu, chỉ trả về mô tả vấn đề có thể đọc được. Bên gọi quyết định cách hiển thị (log / UI).
// Để tránh chi phí IO do quét toàn bộ thư mục, chỉ kiểm tra các điểm then chốt của Progress:
//   - chương hoàn thành cuối cùng phải có bản thảo cuối cùng tồn tại trong chapters/
//   - trong chế độ Layered, Volume/Arc hiện tại phải tìm thấy trong layered_outline
func (s *Store) CheckConsistency() []string {
	var warnings []string
	progress, err := s.Progress.Load()
	if err != nil {
		return append(warnings, fmt.Sprintf("đọc progress thất bại: %v", err))
	}
	if progress == nil {
		return warnings
	}
	if n := len(progress.CompletedChapters); n > 0 {
		lastCh := progress.CompletedChapters[n-1]
		if text, err := s.Drafts.LoadChapterText(lastCh); err != nil {
			warnings = append(warnings, fmt.Sprintf("đọc bản thảo cuối cùng của chương %d thất bại: %v", lastCh, err))
		} else if text == "" {
			warnings = append(warnings, fmt.Sprintf("progress đánh dấu chương %d đã hoàn thành, nhưng chapters/%02d.md không tồn tại hoặc rỗng", lastCh, lastCh))
		}
	}
	if progress.Layered && progress.CurrentVolume > 0 && progress.CurrentArc > 0 {
		volumes, err := s.Outline.LoadLayeredOutline()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("đọc đại cương phân lớp thất bại: %v", err))
		} else if len(volumes) > 0 {
			found := false
			for _, v := range volumes {
				if v.Index != progress.CurrentVolume {
					continue
				}
				for _, a := range v.Arcs {
					if a.Index == progress.CurrentArc {
						found = true
						break
					}
				}
				break
			}
			if !found {
				warnings = append(warnings, fmt.Sprintf("progress hiện tại V%d A%d không tìm thấy mục tương ứng trong đại cương phân lớp", progress.CurrentVolume, progress.CurrentArc))
			}
		}
	}
	return warnings
}

// FoundationMissing trả về thông tin tác phẩm và thiết lập nền còn thiếu trong lập kế hoạch ban đầu, theo thứ tự ổn định.
// Chế độ trường thiên (đã có layered_outline) còn yêu cầu compass. Nếu đọc thất bại phải trả về nguyên trạng, không được
// nhầm tạo phẩm bị hỏng hoặc không có quyền đọc thành "chưa tạo", nếu không bên gọi có thể ghi đè dữ liệu thật.
func (s *Store) FoundationMissing() ([]string, error) {
	var missing []string
	book, err := s.Book.Load()
	if err != nil {
		return nil, fmt.Errorf("tải siêu dữ liệu sách: %w", err)
	}
	if book == nil {
		missing = append(missing, "book")
	}
	premise, err := s.Outline.LoadPremise()
	if err != nil {
		return nil, fmt.Errorf("tải premise: %w", err)
	}
	if premise == "" {
		missing = append(missing, "premise")
	}
	outline, err := s.Outline.LoadOutline()
	if err != nil {
		return nil, fmt.Errorf("tải outline: %w", err)
	}
	if len(outline) == 0 {
		missing = append(missing, "outline")
	}
	characters, err := s.Characters.Load()
	if err != nil {
		return nil, fmt.Errorf("tải characters: %w", err)
	}
	if len(characters) == 0 {
		missing = append(missing, "characters")
	}
	rules, err := s.World.LoadWorldRules()
	if err != nil {
		return nil, fmt.Errorf("tải world rules: %w", err)
	}
	if len(rules) == 0 {
		missing = append(missing, "world_rules")
	}
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return nil, fmt.Errorf("tải layered outline: %w", err)
	}
	if len(layered) > 0 {
		compass, err := s.Outline.LoadCompass()
		if err != nil {
			return nil, fmt.Errorf("tải compass: %w", err)
		}
		if compass == nil {
			missing = append(missing, "compass")
		}
	}
	// Chỉ khi mô hình đã thực hiện rà soát ngữ nghĩa rõ ràng đối với các tạo phẩm đã ghi xuống đĩa,
	// một cuốn sách mới mới được phép chuyển từ lập kế hoạch sang viết.
	// PhaseWriting/Complete đại diện cho sách cũ hoặc sách mới đã được rà soát, giữ tương thích với
	// dự án lịch sử; bản thân việc rà soát là một hành động chứ không phải thiếu tệp, nên chỉ bổ sung
	// khi các tạo phẩm khác đều đầy đủ.
	if len(missing) == 0 {
		progress, err := s.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("tải progress: %w", err)
		}
		if progress == nil || (progress.Phase != domain.PhaseWriting && progress.Phase != domain.PhaseComplete) {
			missing = append(missing, "foundation_audit")
		}
	}
	return missing, nil
}

// FoundationFingerprint trả về dấu vân tay nội dung của các tạo phẩm thiết lập nền hiện tại. Architect phải
// trả lại nguyên giá trị này cho công cụ rà soát từ novel_context đã đọc, để bảo đảm kết luận nhắm vào
// đúng phiên bản đã ghi xuống đĩa thực tế, chứ không phải nội dung trong phiên làm việc chưa lưu hoặc đã lỗi thời.
func (s *Store) FoundationFingerprint() (string, error) {
	files := []string{"meta/book.json", "premise.md", "outline.json", "characters.json", "world_rules.json"}
	layered, err := s.Outline.LoadLayeredOutline()
	if err != nil {
		return "", fmt.Errorf("tải layered outline: %w", err)
	}
	if len(layered) > 0 {
		files = append(files, "layered_outline.json", "meta/compass.json")
	}

	h := sha256.New()
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("đọc %s: %w", rel, err)
		}
		_, _ = h.Write([]byte(rel))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Init tạo cấu trúc thư mục con cần thiết.
func (s *Store) Init() error {
	if err := s.Checkpoints.InitError(); err != nil {
		return fmt.Errorf("load checkpoints: %w", err)
	}
	return s.Progress.io.EnsureDirs([]string{
		"chapters", "summaries", "drafts", "reviews", "meta", "meta/chapter_records", "meta/runtime", "meta/runtime/tasks", "meta/sessions", "meta/sessions/agents",
	})
}

// ── Các phương thức điều phối liên miền ──

// ExpandArc căn chỉnh và mở rộng arc khung xương thành các chương chi tiết (liên động Outline + Progress).
func (s *Store) ExpandArc(volumeIdx, arcIdx int, expansion domain.ArcExpansion) error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	volumes, err := s.Outline.expandArcUnlocked(volumeIdx, arcIdx, expansion)
	if err != nil {
		return err
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		p = &domain.Progress{}
	}
	p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
	return s.Progress.saveUnlocked(p)
}

// AppendVolume thêm volume mới vào cuối đại cương phân lớp (liên động Outline + Progress).
func (s *Store) AppendVolume(vol domain.VolumeOutline) error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()

	volumes, err := s.Outline.appendVolumeUnlocked(vol)
	if err != nil {
		return err
	}

	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return err
	}
	if p == nil {
		p = &domain.Progress{}
	}
	p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
	return s.Progress.saveUnlocked(p)
}

// ReviseOutline thay thế phần đuôi kế hoạch chưa xảy ra, bắt đầu từ fromChapter.
// Với đại cương phẳng, thay thế phần đuôi của toàn sách; với đại cương phân lớp, chỉ thay thế phần đuôi của arc chứa chương mục tiêu. Định nghĩa này giúp cùng một tải
// trọng phát lại vẫn cho cùng một kết quả, đồng thời tránh các enum thao tác JSON Patch và insert/delete.
func (s *Store) ReviseOutline(fromChapter int, replacement []domain.OutlineEntry) (int, error) {
	if fromChapter <= 0 {
		return 0, fmt.Errorf("from_chapter must be > 0: %w", errs.ErrToolArgs)
	}

	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.Outline.io.mu.Lock()
	defer s.Outline.io.mu.Unlock()
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	p, err := s.Progress.loadUnlocked()
	if err != nil {
		return 0, fmt.Errorf("tải progress: %w: %w", errs.ErrStoreRead, err)
	}
	if p == nil {
		return 0, fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
	}
	if p.Phase == domain.PhaseComplete {
		return 0, fmt.Errorf("toàn bộ sách đã hoàn tất, không cho phép sửa đại cương: %w", errs.ErrToolPrecondition)
	}
	protected := p.InProgressChapter
	if latest := p.LatestCompleted(); latest > protected {
		protected = latest
	}
	if fromChapter <= protected {
		return 0, fmt.Errorf("chương %d đã hoàn thành hoặc đang viết; việc sửa đại cương phải bắt đầu sau chương %d: %w",
			fromChapter, protected, errs.ErrToolPrecondition)
	}

	if p.Layered {
		volumes, err := s.Outline.reviseLayeredTailUnlocked(fromChapter, replacement)
		if err != nil {
			return 0, err
		}
		p.TotalChapters = domain.EstimatedChapterCapacity(volumes)
		if err := s.Progress.saveUnlocked(p); err != nil {
			return 0, fmt.Errorf("lưu progress: %w: %w", errs.ErrStoreWrite, err)
		}
		return p.TotalChapters, nil
	}

	outline, err := s.Outline.reviseFlatTailUnlocked(fromChapter, replacement)
	if err != nil {
		return 0, err
	}
	p.TotalChapters = len(outline)
	if err := s.Progress.saveUnlocked(p); err != nil {
		return 0, fmt.Errorf("lưu progress: %w: %w", errs.ErrStoreWrite, err)
	}
	return p.TotalChapters, nil
}

// ClearHandledSteer xóa PendingSteer và đặt lại trạng thái FlowSteering kiểu cũ.
// Hai tệp không thể tạo thành giao dịch hệ thống tệp, nên trước hết ghi Progress có thể lặp lại, rồi mới xóa ý định khôi phục;
// nếu bất kỳ bước nào thất bại thì ít nhất PendingSteer vẫn được giữ lại, để Resume lần sau có thể phát lại an toàn.
func (s *Store) ClearHandledSteer() error {
	s.crossMu.Lock()
	defer s.crossMu.Unlock()

	s.RunMeta.io.mu.Lock()
	defer s.RunMeta.io.mu.Unlock()
	s.Progress.io.mu.Lock()
	defer s.Progress.io.mu.Unlock()

	meta, err := s.RunMeta.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	p, err := s.Progress.loadUnlocked()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if p != nil && p.Flow == domain.FlowSteering {
		if err := domain.ValidateFlowTransition(p.Flow, domain.FlowWriting); err != nil {
			return err
		}
		p.Flow = domain.FlowWriting
		if err := s.Progress.saveUnlocked(p); err != nil {
			return err
		}
	}
	if meta != nil && meta.PendingSteer != "" {
		meta.PendingSteer = ""
		if err := s.RunMeta.saveUnlocked(*meta); err != nil {
			return err
		}
	}
	return nil
}
