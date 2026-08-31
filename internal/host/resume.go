package host

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/revision"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

func upgradeProject(st *storepkg.Store) error {
	version, err := st.LoadProjectFormatVersion()
	if err != nil {
		return fmt.Errorf("đọc phiên bản định dạng dự án: %w", err)
	}
	if version > storepkg.CurrentProjectFormatVersion {
		return fmt.Errorf("phiên bản định dạng dự án v%d cao hơn v%d mà chương trình hiện tại hỗ trợ, vui lòng nâng cấp ainovel-cli", version, storepkg.CurrentProjectFormatVersion)
	}
	for version < storepkg.CurrentProjectFormatVersion {
		next := version + 1
		switch version {
		case storepkg.LegacyProjectFormatVersion:
			if err := migrateLegacyBook(st); err != nil {
				return fmt.Errorf("nâng cấp dữ liệu dự án v%d→v%d: %w", version, next, err)
			}
			if err := revision.MigrateLegacyBaseline(st); err != nil {
				return fmt.Errorf("nâng cấp dữ liệu dự án v%d→v%d: %w", version, next, err)
			}
		default:
			return fmt.Errorf("không hỗ trợ nâng cấp từ định dạng dự án v%d", version)
		}
		if err := st.SaveProjectFormatVersion(next); err != nil {
			return fmt.Errorf("lưu phiên bản định dạng dự án v%d: %w", next, err)
		}
		slog.Info("đã nâng cấp xong dữ liệu dự án", "module", "migration", "from", version, "to", next)
		version = next
	}
	return nil
}

func migrateLegacyBook(st *storepkg.Store) error {
	book, err := st.Book.Load()
	if err != nil {
		return err
	}
	if book == nil {
		book, err = loadLegacyBook(st)
		if err != nil || book == nil {
			return err
		}
	}
	if err := st.Book.Save(*book); err != nil {
		return fmt.Errorf("lưu thông tin tác phẩm cũ: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return fmt.Errorf("ghi nhận thông tin tác phẩm cũ: %w", err)
	}
	return nil
}

func loadLegacyBook(st *storepkg.Store) (*domain.BookMetadata, error) {
	data, err := os.ReadFile(filepath.Join(st.Dir(), "meta", "progress.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("đọc tiến độ tác phẩm cũ: %w", err)
	}
	var legacy struct {
		NovelName string `json:"novel_name"`
	}
	if err := json.Unmarshal(data, &legacy); err != nil {
		return nil, fmt.Errorf("phân tích tiến độ tác phẩm cũ: %w", err)
	}
	legacy.NovelName = strings.TrimSpace(legacy.NovelName)
	if legacy.NovelName == "" {
		return nil, nil
	}
	premise, err := st.Outline.LoadPremise()
	if err != nil {
		return nil, fmt.Errorf("đọc premise truyện cũ: %w", err)
	}
	title := legacyPremiseTitle(premise)
	if title == "" {
		return nil, fmt.Errorf("premise truyện cũ thiếu tiêu đề tên sách")
	}
	if title != legacy.NovelName {
		return nil, fmt.Errorf("tên sách của tác phẩm cũ xung đột: progress=%q, premise=%q", legacy.NovelName, title)
	}
	synopsis := legacyPremiseSection(premise, "xung đột cốt lõi")
	if synopsis == "" {
		return nil, fmt.Errorf("premise truyện cũ thiếu \"xung đột cốt lõi\", không thể tạo tóm tắt tác phẩm")
	}
	return &domain.BookMetadata{Title: title, Synopsis: synopsis}, nil
}

func legacyPremiseTitle(premise string) string {
	for _, line := range strings.Split(premise, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "# ")), "«»\"")
		}
	}
	return ""
}

func legacyPremiseSection(premise, heading string) string {
	var body []string
	matched := false
	for _, line := range strings.Split(premise, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if matched {
				break
			}
			matched = strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(trimmed, "## ")), heading)
			continue
		}
		if matched {
			body = append(body, line)
		}
	}
	return strings.TrimSpace(strings.Join(body, "\n"))
}

// resumeLabel tạo nhãn UI cho Resume dựa trên sự kiện thực tế.
// label rỗng nghĩa là không có trạng thái có thể khôi phục (nên đi theo luồng tạo mới). Bản thân việc khôi phục không cần prompt nào cả —
// Engine chỉ khôi phục sự kiện thực tế: tính lại tuyến từ store rồi chạy tiếp (docs/engine-rfc.md §6).
func resumeLabel(store *storepkg.Store) (string, error) {
	progress, err := store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if progress == nil || progress.Phase == domain.PhaseComplete {
		return "", nil
	}
	return describeResume(store, progress)
}

// describeResume tạo nhãn khôi phục dễ đọc cho con người; không ảnh hưởng đến tuyến Engine.
// Mọi tuyến thực thi đều do Flow Router suy luận theo sự kiện thực tế; ở đây chỉ dành cho UI "Khôi phục: xxx".
func describeResume(store *storepkg.Store, progress *domain.Progress) (string, error) {
	switch progress.Phase {
	case domain.PhasePremise, domain.PhaseOutline:
		return fmt.Sprintf("Khôi phục: giai đoạn lập kế hoạch (%s)", progress.Phase), nil
	case domain.PhaseWriting:
		// Thứ tự ưu tiên khớp với thứ tự ưu tiên quyết định của Router, để label nhất quán với lệnh sắp được phái phát.
		pending, err := store.Signals.LoadPendingCommit()
		if err != nil {
			return "", fmt.Errorf("đọc commit đang chờ khôi phục: %w", err)
		}
		if pending != nil {
			return fmt.Sprintf("Khôi phục: commit chương %d bị gián đoạn", pending.Chapter), nil
		}
		if len(progress.PendingRewrites) > 0 {
			verb := "Viết lại"
			if progress.Flow == domain.FlowPolishing {
				verb = "Đánh bóng"
			}
			return fmt.Sprintf("%s khôi phục: %d chương đang chờ xử lý", verb, len(progress.PendingRewrites)), nil
		}
		if progress.Flow == domain.FlowReviewing {
			return "Khôi phục: đánh giá bị gián đoạn", nil
		}
		if progress.InProgressChapter > 0 {
			return fmt.Sprintf("Khôi phục: chương %d đang tiến hành", progress.InProgressChapter), nil
		}
		label, err := describeArcEndLabel(store, progress)
		if err != nil {
			return "", err
		}
		if label != "" {
			return label, nil
		}
		return fmt.Sprintf("Khôi phục: tiếp tục từ chương %d", progress.NextChapter()), nil
	}
	return "Khôi phục", nil
}

// describeArcEndLabel tạo nhãn phù hợp với UI cho nhiều trạng thái trung gian ở cuối arc/cuối volume.
// Giữ cùng thứ tự với nhánh cuối arc của flow.Route, bảo đảm label khớp với lệnh đầu tiên của Router.
func describeArcEndLabel(store *storepkg.Store, progress *domain.Progress) (string, error) {
	if !progress.Layered || len(progress.CompletedChapters) == 0 {
		return "", nil
	}
	lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
	boundary, err := store.Outline.CheckArcBoundary(lastCh)
	if err != nil {
		return "", fmt.Errorf("kiểm tra ranh giới arc: %w", err)
	}
	if boundary == nil || !boundary.IsArcEnd {
		return "", nil
	}
	vol, arc := boundary.Volume, boundary.Arc
	hasArcReview, err := store.World.HasArcReview(lastCh)
	if err != nil {
		return "", fmt.Errorf("đọc đánh giá arc: %w", err)
	}
	hasArcSummary, err := store.Summaries.HasArcSummary(vol, arc)
	if err != nil {
		return "", fmt.Errorf("đọc tóm tắt arc: %w", err)
	}
	hasVolumeSummary := false
	if boundary.IsVolumeEnd {
		hasVolumeSummary, err = store.Summaries.HasVolumeSummary(vol)
		if err != nil {
			return "", fmt.Errorf("đọc tóm tắt volume: %w", err)
		}
	}
	switch {
	case !hasArcReview:
		return fmt.Sprintf("Khôi phục: đánh giá cuối arc đang chờ xử lý (V%d A%d)", vol, arc), nil
	case !hasArcSummary:
		return fmt.Sprintf("Khôi phục: tóm tắt arc đang chờ tạo (V%d A%d)", vol, arc), nil
	case boundary.IsVolumeEnd && !hasVolumeSummary:
		return fmt.Sprintf("Khôi phục: tóm tắt volume đang chờ tạo (V%d)", vol), nil
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return fmt.Sprintf("Khôi phục: đang chờ mở rộng arc tiếp theo (V%d A%d)", boundary.NextVolume, boundary.NextArc), nil
	case boundary.NeedsNewVolume:
		return fmt.Sprintf("Khôi phục: đang chờ quyết định volume tiếp theo (cuối V%d)", vol), nil
	}
	return "", nil
}
