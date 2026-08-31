package imp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ChapterCommitter là giao diện tối thiểu cần để xuất bản chương, được tools.CommitChapterTool đáp ứng.
// Tái sử dụng PendingCommit saga, checkpoint và kiểm tra idempotent với chương đã hoàn tất của nó,
// không sao chép một bộ logic commit thứ hai (RFC §12.3).
type ChapterCommitter interface {
	Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// publishFoundation xuất bản Foundation theo đúng thứ tự phụ thuộc chính thức,
// nhất quán với thứ tự Architect ghi tác phẩm dài xuống đĩa (RFC §12.2).
// Xuất bản lặp lại cùng một nội dung là idempotent (Store ghi đè cùng nội dung + checkpoint khử trùng lặp).
func publishFoundation(st *store.Store, f *Foundation) error {
	// Đối soát xung đột trước khi xuất bản: từ chối ghi đè artifact chính thức đã tồn tại và khác nội dung (§12.2 / bất biến 6).
	// Nội dung giống nhau thì tiếp tục ghi theo kiểu idempotent (Store ghi đè cùng nội dung + checkpoint khử trùng lặp).
	if err := checkFoundationConflicts(st, f); err != nil {
		return err
	}
	if err := st.Book.Save(f.Book); err != nil {
		return fmt.Errorf("book: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "book", "meta/book.json"); err != nil {
		return fmt.Errorf("checkpoint book: %w", err)
	}
	if err := st.RunMeta.SetPlanningTier(f.PlanningTier); err != nil {
		return fmt.Errorf("planning tier: %w", err)
	}
	// premise
	if err := st.Outline.SavePremise(f.Premise); err != nil {
		return fmt.Errorf("premise: %w", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhasePremise); err != nil {
		return fmt.Errorf("phase premise: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "premise", "premise.md"); err != nil {
		return fmt.Errorf("checkpoint premise: %w", err)
	}
	// characters
	if err := st.Characters.Save(f.Characters); err != nil {
		return fmt.Errorf("characters: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "characters", "characters.json"); err != nil {
		return fmt.Errorf("checkpoint characters: %w", err)
	}
	// world rules
	if err := st.World.SaveWorldRules(f.WorldRules); err != nil {
		return fmt.Errorf("world_rules: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "world_rules", "world_rules.json"); err != nil {
		return fmt.Errorf("checkpoint world_rules: %w", err)
	}
	// layered outline là nguồn duy nhất; Store đồng bộ xây dựng lại flat outline.
	if err := st.Outline.SaveLayeredOutline(f.Volumes); err != nil {
		return fmt.Errorf("layered outline: %w", err)
	}
	// Tiến độ của giai đoạn outline là căn cứ để engine tính lại định tuyến
	// (sức chứa chương/phân tầng/cung truyện tập hiện tại); nếu ghi thất bại sẽ để lại
	// trạng thái đã xuất bản không nhất quán, nên phải bộc lộ lỗi thay vì nuốt lỗi (RFC §12.2).
	if err := st.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
		return fmt.Errorf("phase outline: %w", err)
	}
	if err := st.Progress.SetTotalChapters(domain.EstimatedChapterCapacity(f.Volumes)); err != nil {
		return fmt.Errorf("total chapters: %w", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		return fmt.Errorf("set layered: %w", err)
	}
	if len(f.Volumes) > 0 && len(f.Volumes[0].Arcs) > 0 {
		if err := st.Progress.UpdateVolumeArc(f.Volumes[0].Index, f.Volumes[0].Arcs[0].Index); err != nil {
			return fmt.Errorf("volume arc: %w", err)
		}
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "layered_outline", "layered_outline.json"); err != nil {
		return fmt.Errorf("checkpoint layered outline: %w", err)
	}
	// compass
	if err := st.Outline.SaveCompass(f.Compass); err != nil {
		return fmt.Errorf("compass: %w", err)
	}
	if _, err := st.Checkpoints.AppendArtifact(domain.GlobalScope(), "compass", "meta/compass.json"); err != nil {
		return fmt.Errorf("checkpoint compass: %w", err)
	}
	// Toàn bộ các lần ghi chính thức của Foundation được nhập đều đã thành công,
	// có thể chuyển sang writing một cách tường minh.
	// Không thể tái sử dụng FoundationMissing của quy trình sáng tác thông thường:
	// nhập cho phép world_rules rỗng; nếu xem "giá trị rỗng hợp lệ" là thiếu,
	// tiến độ sẽ mãi kẹt ở outline, rồi StartChapter bị cổng giai đoạn từ chối.
	p, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("load progress: %w", err)
	}
	if p == nil {
		return fmt.Errorf("load progress: progress chưa được khởi tạo")
	}
	if p.Phase != domain.PhaseWriting && p.Phase != domain.PhaseComplete {
		if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
			return fmt.Errorf("phase writing: %w", err)
		}
	}
	return nil
}

// checkFoundationConflicts kiểm tra tính nhất quán giữa Foundation chờ xuất bản và các artifact chính thức hiện có:
// hiện có rỗng thì xem là xuất bản lần đầu; giống nhau thì xem là idempotent; khác nhau thì báo xung đột và không ghi đè (RFC §12.2 / bất biến 6).
// compass và outline phẳng được suy ra từ outline phân tầng; nếu phân tầng nhất quán thì phần suy ra cũng nhất quán, nên không kiểm tra riêng artifact suy ra.
// Không được nuốt lỗi đọc thành "tệp không tồn tại": loader của store trả về (giá trị zero, nil) khi thiếu,
// nên mọi lỗi khác nil đều là lỗi thật (hỏng/quyền/JSON không hợp lệ); nếu xem là giá trị rỗng và tiếp tục,
// sẽ ghi đè artifact chính thức không đọc được (RFC §12.2).
func checkFoundationConflicts(st *store.Store, f *Foundation) error {
	wantBook := f.Book.Normalized()
	book, err := st.Book.Load()
	if err != nil {
		return fmt.Errorf("đọc book chính thức: %w", err)
	}
	if book != nil && !jsonEqual(book, wantBook) {
		return fmt.Errorf("book chính thức xung đột với bản nhập tổng hợp (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	cur, err := st.Outline.LoadPremise()
	if err != nil {
		return fmt.Errorf("đọc premise chính thức: %w", err)
	}
	if cur != "" && cur != f.Premise {
		return fmt.Errorf("premise chính thức xung đột với bản nhập tổng hợp (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	chars, err := st.Characters.Load()
	if err != nil {
		return fmt.Errorf("đọc characters chính thức: %w", err)
	}
	if len(chars) > 0 && !jsonEqual(chars, f.Characters) {
		return fmt.Errorf("characters chính thức xung đột với bản nhập tổng hợp (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	rules, err := st.World.LoadWorldRules()
	if err != nil {
		return fmt.Errorf("đọc world_rules chính thức: %w", err)
	}
	if len(rules) > 0 && !jsonEqual(rules, f.WorldRules) {
		return fmt.Errorf("world_rules chính thức xung đột với bản nhập tổng hợp (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	layered, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return fmt.Errorf("đọc layered_outline chính thức: %w", err)
	}
	if len(layered) > 0 && !jsonEqual(layered, f.Volumes) {
		return fmt.Errorf("layered_outline chính thức xung đột với bản nhập tổng hợp (đã tồn tại phiên bản khác), từ chối ghi đè")
	}
	return nil
}

// jsonEqual so sánh hai giá trị có tương đương nhau hay không theo byte JSON đã chuẩn hóa.
func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(ab, bb)
}

// publishChapter tái sử dụng commit_chapter để xuất bản một chương; chương đã hoàn tất sẽ được kiểm tra idempotent của nó bỏ qua (RFC §12.3).
func publishChapter(ctx context.Context, st *store.Store, commit ChapterCommitter, chapter int, content string, f ImportedChapterFacts) error {
	completed, err := st.Progress.IsChapterCompleted(chapter)
	if err != nil {
		return fmt.Errorf("load progress ch%d: %w", chapter, err)
	}
	if completed {
		// Sự cố có thể xảy ra giữa MarkChapterComplete và ClearPendingCommit: pending_commit còn sót lại
		// trỏ đến chương này. Nếu bỏ qua trực tiếp sẽ vòng qua nhánh dọn dẹp mà công cụ commit chuẩn bị riêng
		// cho cửa sổ này (bổ sung checkpoint + xóa phần còn sót); Execute của chương tiếp theo sẽ từ chối
		// vì "tồn tại commit chương chưa khôi phục", khiến mỗi lần chạy lại nhập đều chết tại cùng một chỗ
		// và phải xóa thủ công meta/pending_commit.json mới mở khóa được. Khi gặp phần còn sót, vẫn đi qua
		// đường idempotent của công cụ để hoàn tất dọn dẹp.
		pending, err := st.Signals.LoadPendingCommit()
		if err != nil {
			return fmt.Errorf("load pending commit ch%d: %w", chapter, err)
		}
		if pending != nil && pending.Chapter == chapter {
			raw, err := json.Marshal(commitArgs(chapter, f))
			if err != nil {
				return fmt.Errorf("marshal commit ch%d: %w", chapter, err)
			}
			if _, err := commit.Execute(ctx, raw); err != nil {
				return fmt.Errorf("commit ch%d: %w", chapter, err)
			}
		}
		return nil
	}
	if err := st.Drafts.SaveDraft(chapter, content); err != nil {
		return fmt.Errorf("save draft ch%d: %w", chapter, err)
	}
	if err := st.Progress.StartChapter(chapter); err != nil {
		return fmt.Errorf("start ch%d: %w", chapter, err)
	}
	raw, err := json.Marshal(commitArgs(chapter, f))
	if err != nil {
		return fmt.Errorf("marshal commit ch%d: %w", chapter, err)
	}
	if _, err := commit.Execute(ctx, raw); err != nil {
		return fmt.Errorf("commit ch%d: %w", chapter, err)
	}
	return nil
}

// commitArgs ánh xạ sự kiện thực tế theo từng chương thành tham số đầu vào của commit_chapter.
func commitArgs(chapter int, f ImportedChapterFacts) map[string]any {
	keyEvents := f.KeyEvents
	if len(keyEvents) == 0 {
		keyEvents = []string{f.CoreEvent} // core_event đã được kiểm tra là không rỗng
	}
	args := map[string]any{
		"chapter":         chapter,
		"title":           f.Title,
		"summary":         f.Summary,
		"characters":      f.Characters,
		"key_events":      keyEvents,
		"hook_type":       f.HookType,
		"dominant_strand": f.DominantStrand,
	}
	if len(f.TimelineEvents) > 0 {
		args["timeline_events"] = f.TimelineEvents
	}
	if len(f.ForeshadowUpdates) > 0 {
		args["foreshadow_updates"] = f.ForeshadowUpdates
	}
	if len(f.RelationshipChanges) > 0 {
		args["relationship_changes"] = f.RelationshipChanges
	}
	if len(f.StateChanges) > 0 {
		args["state_changes"] = f.StateChanges
	}
	return args
}

// isPublished xác định trạng thái chính thức đã phản ánh đầy đủ lần nhập hay chưa:
// Foundation đã được ghi xuống đĩa và số chương đã hoàn tất đạt kỳ vọng.
// Chỉ đối soát các artifact mà quá trình nhập thật sự tạo ra -- book, premise, outline phẳng bao phủ toàn bộ chương,
// và các chương đã hoàn tất -- chứ không tái sử dụng FoundationMissing(): cái sau là cổng "có thể viết"
// của quy trình sáng tác thông thường, sẽ đánh giá nhầm world_rules rỗng hợp lệ là chưa hoàn tất,
// khiến đối soát xuất bản không bao giờ hội tụ (RFC §12.3).
func isPublished(st *store.Store, expected int) (bool, error) {
	if expected == 0 {
		return false, nil
	}
	book, err := st.Book.Load()
	if err != nil {
		return false, fmt.Errorf("đọc book chính thức: %w", err)
	}
	if book == nil {
		return false, nil
	}
	p, err := st.Outline.LoadPremise()
	if err != nil {
		return false, fmt.Errorf("đọc premise chính thức: %w", err)
	}
	if p == "" {
		return false, nil
	}
	o, err := st.Outline.LoadOutline()
	if err != nil {
		return false, fmt.Errorf("đọc outline chính thức: %w", err)
	}
	if len(o) < expected {
		return false, nil
	}
	prog, err := st.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("đọc progress chính thức: %w", err)
	}
	return prog != nil && len(prog.CompletedChapters) >= expected, nil
}
