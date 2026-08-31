package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveFoundationTool lưu thiết lập nền tảng (premise/outline/characters), dành riêng cho Architect.
type SaveFoundationTool struct {
	store *store.Store
}

func NewSaveFoundationTool(store *store.Store) *SaveFoundationTool {
	return &SaveFoundationTool{store: store}
}

func (t *SaveFoundationTool) Name() string { return "save_foundation" }
func (t *SaveFoundationTool) Description() string {
	return "Lưu nền tảng (premise/outline/characters/world_rules/compass). **Quan trọng**: ghi dữ liệu bền vững vào store dưới dạng Markdown/JSON. Tham số gồm {type, content, scale?, volume?, arc?}. type nhận premise / outline / layered_outline / characters / world_rules / expand_arc / append_volume / update_compass / complete_book. premise nhận content Markdown; các loại khác nhận content JSON. expand_arc nhận volume + arc, content có dạng {title, goal, chapters}, dùng để mở rộng cung khung. append_volume nhận content là VolumeOutline JSON, dùng để thêm tập; đặt \"final\": true khi đây là tập kết, sau đó mới gọi complete_book. update_compass nhận content là StoryCompass JSON. complete_book nhận content {}, chỉ được gọi khi toàn bộ chương đã hoàn tất, không còn pending_rewrites, compass không còn open_threads và tập cuối đã đánh dấu final. append_volume / complete_book phải có reason (một câu giải thích quyết định tiếp tục, khép lại hoặc hoàn tất). scale là short / mid / long."
}
func (t *SaveFoundationTool) Label() string { return "Lưu thiết lập" }

// Công cụ ghi (cập nhật chéo Outline/Progress/Characters), không cho phép chạy song song.
func (t *SaveFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveFoundationTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("type", schema.Enum("Loại thiết lập", "premise", "outline", "layered_outline", "characters", "world_rules", "expand_arc", "append_volume", "update_compass", "complete_book")).Required(),
		schema.Property("content", map[string]any{
			"description": "Nội dung. Với premise, truyền chuỗi Markdown; các loại khác có thể truyền trực tiếp mảng hoặc đối tượng JSON, cũng hỗ trợ chuỗi JSON. Khi expand_arc, truyền {title, goal, chapters}; title/goal là kế hoạch cung mục tiêu đã hiệu chỉnh theo các dữ kiện đã hoàn thành.",
		}).Required(),
		schema.Property("scale", schema.Enum("Mức lập kế hoạch", "short", "mid", "long")),
		schema.Property("volume", schema.Int("Số tập mục tiêu (chỉ bắt buộc khi expand_arc)")),
		schema.Property("arc", schema.Int("Số cung mục tiêu (chỉ bắt buộc khi expand_arc)")),
		schema.Property("reason", schema.String("Lý do quyết định ở cuối tập (bắt buộc với append_volume / complete_book): đối chiếu danh sách kiểm tra kết thúc, nêu một câu vì sao tiếp tục tập, tuyên bố khép lại hoặc hoàn tất")),
	)
}

func (t *SaveFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Scale   string          `json:"scale"`
		Volume  int             `json:"volume"`
		Arc     int             `json:"arc"`
		Reason  string          `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	content, err := normalizeFoundationContent(a.Content)
	if err != nil {
		return nil, err
	}
	if a.Scale != "" {
		switch domain.PlanningTier(a.Scale) {
		case domain.PlanningTierShort, domain.PlanningTierMid, domain.PlanningTierLong:
		default:
			return nil, fmt.Errorf("invalid scale %q, expected short/mid/long: %w", a.Scale, errs.ErrToolArgs)
		}
	}

	result := map[string]any{"saved": true, "type": a.Type, "scale": a.Scale}

	// Dàn ý toàn phần chỉ thuộc giai đoạn lập kế hoạch. Giai đoạn viết phải dùng các thao tác tăng dần được bảo vệ; sau khi hoàn tất phải mở lại trước.
	// Nếu không, sẽ bỏ qua lớp bảo vệ các chương đã hoàn thành và làm hỏng tính nhất quán giữa Progress với dữ kiện chương.
	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("check foundation phase: %w: %w", errs.ErrStoreRead, err)
	}
	if (a.Type == "outline" || a.Type == "layered_outline") && progress != nil {
		switch progress.Phase {
		case domain.PhaseWriting:
			return nil, fmt.Errorf(
				"Ở giai đoạn viết, cấm dùng %s để ghi đè toàn bộ dàn ý. Hãy dùng revise_outline để sửa các chương chưa xảy ra, dùng expand_arc để mở rộng cung khung, hoặc dùng append_volume để thêm tập mới: %w",
				a.Type, errs.ErrToolPrecondition)
		case domain.PhaseComplete:
			return nil, fmt.Errorf(
				"Toàn bộ truyện đã hoàn tất, cấm dùng %s để ghi đè toàn bộ dàn ý. Hãy mở lại tác phẩm trước, rồi dùng thao tác sửa dàn ý hoặc viết tiếp được bảo vệ: %w",
				a.Type, errs.ErrToolPrecondition)
		}
	}
	if a.Scale != "" {
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("save planning tier: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	// Ba lựa chọn ở cuối tập (tiếp tục tập / khép lại / hoàn tất) là quyết định ngữ nghĩa nặng nhất của toàn truyện, nên lý do phải trở thành dữ kiện audit
	// (decisions.jsonl, cùng luồng với plan_start/intervention), nếu không thì việc khép lại quá sớm /
	// Việc tiếp tục tập không đúng chỉ còn cách soi log hội thoại để gỡ lỗi. Ảnh chụp dữ kiện lấy theo thời điểm quyết định (trước khi thay đổi được ghi xuống).
	volumeEnd := a.Type == "append_volume" || a.Type == "complete_book"
	if volumeEnd && strings.TrimSpace(a.Reason) == "" {
		return nil, fmt.Errorf("%s phải có tham số reason: đối chiếu danh sách kiểm tra kết thúc, nêu một câu vì sao lần này tiếp tục tập, tuyên bố khép lại hoặc hoàn tất: %w", a.Type, errs.ErrToolArgs)
	}
	var volumeEndFacts json.RawMessage
	if volumeEnd {
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress for volume-end facts: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil {
			facts := map[string]any{"completed_chapters": len(p.CompletedChapters)}
			if p.Layered {
				outline, outlineErr := t.store.Outline.LoadOutline()
				if outlineErr != nil {
					return nil, fmt.Errorf("load outlined chapters for volume-end facts: %w: %w", errs.ErrStoreRead, outlineErr)
				}
				facts["dynamic_planning"] = true
				facts["outlined_chapters"] = len(outline)
			} else {
				facts["total_chapters"] = p.TotalChapters
			}
			volumeEndFacts, err = json.Marshal(facts)
			if err != nil {
				return nil, fmt.Errorf("marshal volume-end facts: %w", err)
			}
		}
	}

	decode := func(typeName string, out any) error {
		return decodeFoundationJSON(typeName, content, out)
	}

	switch a.Type {
	case "premise":
		if err := t.store.Outline.SavePremise(content); err != nil {
			return nil, fmt.Errorf("save premise: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.AdvancePhase(domain.PhasePremise); err != nil {
			return nil, fmt.Errorf("update premise phase: %w: %w", errs.ErrStoreWrite, err)
		}

	case "outline":
		var entries []domain.OutlineEntry
		if err := decode("outline", &entries); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveOutline(entries); err != nil {
			return nil, fmt.Errorf("save outline: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.AdvancePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(len(entries)); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if domain.PlanningTier(a.Scale) != domain.PlanningTierLong {
			if err := t.store.Progress.SetLayered(false); err != nil {
				return nil, fmt.Errorf("disable layered mode: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Progress.UpdateVolumeArc(0, 0); err != nil {
				return nil, fmt.Errorf("reset volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Outline.ClearLayeredOutline(); err != nil {
				return nil, fmt.Errorf("clear layered outline: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["chapters"] = len(entries)

	case "layered_outline":
		var volumes []domain.VolumeOutline
		if err := decode("layered_outline", &volumes); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveLayeredOutline(volumes); err != nil {
			return nil, fmt.Errorf("save layered_outline: %w: %w", errs.ErrStoreWrite, err)
		}
		total := domain.EstimatedChapterCapacity(volumes)
		if err := t.store.Progress.AdvancePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(total); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetLayered(true); err != nil {
			return nil, fmt.Errorf("enable layered mode: %w: %w", errs.ErrStoreWrite, err)
		}
		if len(volumes) > 0 && len(volumes[0].Arcs) > 0 {
			if err := t.store.Progress.UpdateVolumeArc(volumes[0].Index, volumes[0].Arcs[0].Index); err != nil {
				return nil, fmt.Errorf("set initial volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["volumes"] = len(volumes)
		result["dynamic_planning"] = true
		result["outlined_chapters"] = len(domain.FlattenOutline(volumes))

	case "characters":
		var chars []domain.Character
		if err := decode("characters", &chars); err != nil {
			return nil, err
		}
		if err := t.store.Characters.Save(chars); err != nil {
			return nil, fmt.Errorf("save characters: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(chars)

	case "world_rules":
		var rules []domain.WorldRule
		if err := decode("world_rules", &rules); err != nil {
			return nil, err
		}
		if err := t.store.World.SaveWorldRules(rules); err != nil {
			return nil, fmt.Errorf("save world_rules: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(rules)

	case "expand_arc":
		if a.Volume <= 0 || a.Arc <= 0 {
			return nil, fmt.Errorf("expand_arc requires volume and arc parameters: %w", errs.ErrToolArgs)
		}
		var expansion domain.ArcExpansion
		if err := decode("expand_arc", &expansion); err != nil {
			return nil, err
		}
		if err := t.store.ExpandArc(a.Volume, a.Arc, expansion); err != nil {
			return nil, fmt.Errorf("expand arc: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = a.Volume
		result["arc"] = a.Arc
		result["title"] = expansion.Title
		result["goal"] = expansion.Goal
		result["chapters"] = len(expansion.Chapters)
		if err := t.consumeWriterFeedback(); err != nil {
			return nil, err
		}

	case "append_volume":
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil && p.Phase == domain.PhaseComplete {
			return nil, fmt.Errorf("Toàn bộ truyện đã hoàn tất (phase=complete), không cho phép thêm tập mới: %w", errs.ErrToolPrecondition)
		}
		var vol domain.VolumeOutline
		if err := decode("append_volume", &vol); err != nil {
			return nil, err
		}
		prior, err := t.store.Outline.LoadLayeredOutline()
		if err != nil {
			return nil, fmt.Errorf("load layered outline: %w: %w", errs.ErrStoreRead, err)
		}
		if err := t.store.AppendVolume(vol); err != nil {
			return nil, fmt.Errorf("append volume: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = vol.Index
		if vol.Final {
			result["final_volume"] = true
		} else if domain.FinaleVolume(prior) > 0 {
			// Phản hồi dữ kiện: trạng thái khép lại đã tuyên bố trước đó bị gỡ do thêm một tập mới thông thường (tập mới trở thành tập cuối)
			result["finale_released"] = true
		}
		result["arcs"] = len(vol.Arcs)
		chCount := 0
		for _, arc := range vol.Arcs {
			chCount += len(arc.Chapters)
		}
		if chCount > 0 {
			result["chapters"] = chCount
		}
		if err := t.consumeWriterFeedback(); err != nil {
			return nil, err
		}

	case "complete_book":
		// Cổng duy nhất để hoàn tất toàn bộ truyện: đẩy trực tiếp Phase=Complete.
		// Chỉ cho phép ở giai đoạn Writing, để tránh gọi nhầm trong giai đoạn lập kế hoạch và bỏ qua toàn bộ phần viết.
		// Từ chối khi còn hàng đợi sửa lại — bảo đảm PendingRewrites phải chạy hết mới được kết thúc.
		progress, perr := t.store.Progress.Load()
		if perr != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, perr)
		}
		if progress == nil {
			return nil, fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if progress.Phase != domain.PhaseWriting {
			return nil, fmt.Errorf("complete_book chỉ có thể gọi ở giai đoạn writing (phase hiện tại=%s): %w", progress.Phase, errs.ErrToolPrecondition)
		}
		if len(progress.PendingRewrites) > 0 {
			return nil, fmt.Errorf("còn %d chương trong hàng đợi sửa lại, xử lý xong rồi hãy gọi complete_book: %w", len(progress.PendingRewrites), errs.ErrToolPrecondition)
		}
		// Các kiểm tra tiền điều kiện hoàn tất có thể liệt kê được phải đặt ở lớp mã nguồn (cách chia ba), không thể chỉ dựa vào gợi ý trong prompt.
		// "Danh sách kiểm tra kết thúc" — sự cố thực tế: vừa ghi xong kế hoạch thì phase nhảy sang writing, mô hình yếu tiện tay
		// gọi nhầm complete_book, khiến 0/68 chương bị đánh dấu hoàn tất trực tiếp.
		if len(progress.CompletedChapters) == 0 {
			return nil, fmt.Errorf("Chưa viết chương nào thì không được hoàn tất; sau khi lập kế hoạch xong, hệ thống tự chuyển sang viết, không cần gọi complete_book: %w", errs.ErrToolPrecondition)
		}
		next := progress.NextChapter()
		if progress.Layered {
			outline, outlineErr := t.store.Outline.LoadOutline()
			if outlineErr != nil {
				return nil, fmt.Errorf("load outlined chapters: %w: %w", errs.ErrStoreRead, outlineErr)
			}
			if next <= len(outline) {
				return nil, fmt.Errorf("chưa viết đủ chương kế tiếp (%d/%d); nếu muốn kết thúc hãy dùng append_volume với JSON có \"final\": true: %w", next, len(outline), errs.ErrToolPrecondition)
			}
		} else if progress.TotalChapters > 0 && next <= progress.TotalChapters {
			return nil, fmt.Errorf("chưa viết đủ chương kế tiếp (%d/%d); nếu muốn kết thúc hãy dùng append_volume với JSON có \"final\": true: %w", next, progress.TotalChapters, errs.ErrToolPrecondition)
		}
		// Tuyến dài đang hoạt động mà chưa khép lại thì không thể hoàn tất — contract của OpenThreads chính là "phải khép thì mới kết cục". Đây không phải
		// tái phán ngữ nghĩa: nếu thật sự cho rằng đã khép hết, hãy dùng update_compass để xóa open_threads rồi mới hoàn tất, biến
		// việc "miễn trừ trong lập luận" thành thao tác ghi xuống có thể audit (thực tế khi nhập sách đã hoàn tất để viết tiếp, Architect trích dẫn vòng vo để lách
		// mục số 3 của danh sách hoàn tất và hoàn thẳng; nhu cầu viết tiếp của người dùng bị luật hoàn tất khóa chặt).
		compass, err := t.store.Outline.LoadCompass()
		if err != nil {
			return nil, fmt.Errorf("load compass: %w: %w", errs.ErrStoreRead, err)
		}
		if compass != nil && len(compass.OpenThreads) > 0 {
			return nil, fmt.Errorf("compass còn %d tuyến dài chưa khép (ví dụ: %s), không thể gọi complete_book; hãy dùng update_compass để đóng open_threads hoặc append_volume (\"final\": true): %w", len(compass.OpenThreads), compass.OpenThreads[0], errs.ErrToolPrecondition)
		}
		if err := t.store.Progress.MarkComplete(); err != nil {
			return nil, fmt.Errorf("mark complete: %w: %w", errs.ErrStoreWrite, err)
		}
		result["book_complete"] = true
		result["phase"] = string(domain.PhaseComplete)

	case "update_compass":
		var compass domain.StoryCompass
		if err := decode("compass", &compass); err != nil {
			return nil, err
		}
		// Tầng công cụ buộc ghi đè LastUpdated bằng số chương đã hoàn thành hiện tại, không tin giá trị tự điền của LLM.
		// LLM thường quên điền hoặc để 0, sẽ làm diag.CompassDrift báo sai và Router định tuyến lệch.
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil {
			compass.LastUpdated = p.LatestCompleted()
		}
		if err := t.store.Outline.SaveCompass(compass); err != nil {
			return nil, fmt.Errorf("save compass: %w: %w", errs.ErrStoreWrite, err)
		}
		result["ending_direction"] = compass.EndingDirection
		result["last_updated"] = compass.LastUpdated
		if err := t.consumeWriterFeedback(); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("loại không xác định %q, mong đợi premise/outline/layered_outline/characters/world_rules/expand_arc/append_volume/update_compass/complete_book: %w", a.Type, errs.ErrToolArgs)
	}

	// checkpoint
	scope := domain.GlobalScope()
	if a.Type == "expand_arc" {
		scope = domain.ArcScope(a.Volume, a.Arc)
	} else if a.Type == "append_volume" {
		scope = domain.GlobalScope()
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, a.Type, foundationArtifact(a.Type)); err != nil {
		return nil, fmt.Errorf("checkpoint foundation %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
	}

	if volumeEnd {
		t.recordVolumeEndDecision(a.Type, a.Reason, volumeEndFacts, result)
	}

	// Trả về các mục chưa hoàn thành còn lại. Sau khi các công kiện ban đầu đầy đủ vẫn sẽ còn foundation_audit; chỉ
	// audit_foundation khi báo ready=true cho phiên bản thực tế đã ghi xuống thì mới cho phép vào writing.
	remaining, err := t.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w: %w", errs.ErrStoreRead, err)
	}
	ready := len(remaining) == 0
	result["remaining"] = remaining
	result["foundation_ready"] = ready
	return json.Marshal(result)
}

func foundationArtifact(t string) string {
	switch t {
	case "premise":
		return "premise.md"
	case "outline":
		return "outline.json"
	case "layered_outline", "expand_arc", "append_volume":
		return "layered_outline.json"
	case "complete_book":
		return "meta/progress.json"
	case "characters":
		return "characters.json"
	case "world_rules":
		return "world_rules.json"
	case "update_compass":
		return "meta/compass.json"
	default:
		return ""
	}
}

// decodeFoundationJSON phân tích trường content của save_foundation; khi thất bại sẽ kèm vị trí dòng/cột
// cùng gợi ý sửa phổ biến nhất để LLM ở lần thử lại sau có thể định vị trực tiếp thay vì đoán mò.
func decodeFoundationJSON(typeName, content string, out any) error {
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	hint := `Gợi ý: dùng chuỗi thoát \" và \n trong JSON, không dùng xuống dòng trực tiếp.`
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetToLineCol(content, int(se.Offset))
		return fmt.Errorf("parse %s JSON (line %d col %d): %w — %s", typeName, line, col, err, hint)
	}
	return fmt.Errorf("parse %s JSON: %w — %s", typeName, err, hint)
}

func offsetToLineCol(s string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func normalizeFoundationContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("content là bắt buộc: %w", errs.ErrToolArgs)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	if !json.Valid(raw) {
		return "", fmt.Errorf("content không hợp lệ: cần chuỗi Markdown hoặc giá trị JSON hợp lệ: %w", errs.ErrToolArgs)
	}
	return string(raw), nil
}

// recordVolumeEndDecision ghi lý do của ba lựa chọn cuối tập (tiếp tục / khép lại / hoàn tất) vào audit quyết định.
// best-effort: thay đổi cấu trúc đã được ghi xuống, audit thất bại chỉ cảnh báo chứ không rollback — báo lỗi sẽ khiến mô hình làm lại phần đã hoàn thành
// thao tác (lặp lại việc thêm tập).
func (t *SaveFoundationTool) recordVolumeEndDecision(action, reason string, facts json.RawMessage, result map[string]any) {
	decision := map[string]any{"action": action}
	if v, ok := result["volume"]; ok {
		decision["volume"] = v
	}
	if _, ok := result["final_volume"]; ok {
		decision["final"] = true
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		slog.Error("Không thể tuần tự hóa quyết định cuối tập", "module", "tools", "action", action, "err", err)
		return
	}
	if _, err := t.store.Decisions.Append(store.DecisionRecord{
		Kind:     "volume_end",
		Decider:  "architect",
		Facts:    facts,
		Decision: raw,
		Reason:   reason,
	}); err != nil {
		slog.Error("Không thể ghi audit quyết định cuối tập xuống đĩa", "module", "tools", "action", action, "err", err)
	}
}

// consumeWriterFeedback xóa phản hồi lập kế hoạch đã xử lý sau khi thao tác cấu trúc thành công.
func (t *SaveFoundationTool) consumeWriterFeedback() error {
	if err := t.store.Outline.ClearOutlineFeedback(); err != nil {
		return fmt.Errorf("clear outline feedback: %w: %w", errs.ErrStoreWrite, err)
	}
	return nil
}
