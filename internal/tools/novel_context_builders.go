package tools

import (
	"slices"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/rules"
)

type contextBuildState struct {
	chapter         int
	profile         domain.ContextProfile
	progress        *domain.Progress
	runMeta         *domain.RunMeta
	outline         []domain.OutlineEntry
	currentEntry    *domain.OutlineEntry
	chapterPlan     *domain.ChapterPlan
	storyThreads    []domain.RecallItem
	foreshadow      []domain.ForeshadowEntry
	relationships   []domain.RelationshipEntry
	allStateChanges []domain.StateChange
	styleRules      *domain.WritingStyleRules
}

type chapterContextEnvelope struct {
	Working    map[string]any
	Episodic   map[string]any
	References map[string]any
	Selected   map[string]any
}

type architectContextEnvelope struct {
	Planning   map[string]any
	Foundation map[string]any
	References map[string]any
}

// planningVolumeOutline là hình chiếu cấu trúc chỉ đọc cho Architect. Các cung đã hoàn tất chỉ giữ ranh giới và số lượng,
// còn cung chưa hoàn tất/chưa xảy ra giữ chi tiết chương để tránh ngữ cảnh truyện dài phình tuyến tính theo số chương đã viết.
type planningVolumeOutline struct {
	Index int                  `json:"index"`
	Title string               `json:"title"`
	Theme string               `json:"theme"`
	Final bool                 `json:"final,omitempty"`
	Arcs  []planningArcOutline `json:"arcs"`
}

type planningArcOutline struct {
	Index             int                   `json:"index"`
	Title             string                `json:"title"`
	Goal              string                `json:"goal"`
	Status            string                `json:"status"`
	StartChapter      int                   `json:"start_chapter,omitempty"`
	EndChapter        int                   `json:"end_chapter,omitempty"`
	ChapterCount      int                   `json:"chapter_count,omitempty"`
	EstimatedChapters int                   `json:"estimated_chapters,omitempty"`
	Chapters          []domain.OutlineEntry `json:"chapters,omitempty"`
}

func newChapterContextEnvelope() chapterContextEnvelope {
	return chapterContextEnvelope{
		Working:    make(map[string]any),
		Episodic:   make(map[string]any),
		References: make(map[string]any),
		Selected:   make(map[string]any),
	}
}

func newArchitectContextEnvelope() architectContextEnvelope {
	return architectContextEnvelope{
		Planning:   make(map[string]any),
		Foundation: make(map[string]any),
		References: make(map[string]any),
	}
}

func (e chapterContextEnvelope) apply(result map[string]any) {
	// Luồng chương áp dụng lần lượt nội dung của giai đoạn chuẩn bị và giai đoạn dựng, nên cần hợp nhất phân vùng đã có.
	mergeEnvelopeSection(result, "working_memory", e.Working)
	mergeEnvelopeSection(result, "episodic_memory", e.Episodic)
	mergeEnvelopeSection(result, "reference_pack", e.References)
	if len(e.Selected) > 0 {
		mergeEnvelopeSection(result, "selected_memory", e.Selected)
	}
}

// mergeEnvelopeSection hợp nhất section vào vùng chứa sẵn có ở result[key]; nếu chưa có vùng chứa thì gắn trực tiếp.
func mergeEnvelopeSection(result map[string]any, key string, section map[string]any) {
	if existing, ok := result[key].(map[string]any); ok {
		for k, v := range section {
			existing[k] = v
		}
		return
	}
	result[key] = section
}

func (e architectContextEnvelope) apply(result map[string]any) {
	result["planning_memory"] = e.Planning
	result["foundation_memory"] = e.Foundation
	result["reference_pack"] = e.References
}

// buildProgressStatus trả về tóm tắt tiến độ khi Architect không truyền chapter.
// Luồng chương của Writer/Editor không cần các thông tin này, để tránh gây nhiễu khi viết.
func (t *ContextTool) buildProgressStatus(result map[string]any, reads *contextReads) {
	progress, err := t.store.Progress.Load()
	if err != nil {
		reads.require("progress_status", err)
		return
	}
	if progress == nil {
		return
	}
	status := map[string]any{
		"phase":              string(progress.Phase),
		"flow":               string(progress.Flow),
		"completed_chapters": len(progress.CompletedChapters),
		"next_chapter":       progress.NextChapter(),
		"total_word_count":   progress.TotalWordCount,
	}
	if progress.InProgressChapter > 0 {
		status["in_progress_chapter"] = progress.InProgressChapter
	}
	if len(progress.PendingRewrites) > 0 {
		status["pending_rewrites"] = progress.PendingRewrites
		status["rewrite_reason"] = progress.RewriteReason
	}
	if progress.Layered {
		status["layered"] = true
		status["dynamic_planning"] = true
		outline, outlineErr := t.store.Outline.LoadOutline()
		if outlineErr != nil {
			reads.require("progress_status.outline", outlineErr)
		} else {
			status["outlined_chapters"] = len(outline)
		}
		status["current_volume"] = progress.CurrentVolume
		status["current_arc"] = progress.CurrentArc
	} else {
		status["total_chapters"] = progress.TotalChapters
	}
	if progress.Phase == domain.PhaseComplete {
		status["finished"] = true
	}
	result["progress_status"] = status
}

// buildUserRules nhúng Bundle đã hợp nhất vào working_memory.user_rules (đường dẫn canonical).
//
// Nhúng tại một điểm: bất kỳ luồng writer / editor / architect nào gọi novel_context
// đều lấy được cùng một bộ ưu tiên trong working_memory.user_rules. Luồng architect vốn không có working_memory,
// nên hàm này tạo mới khi cần (chỉ chứa user_rules); với luồng chapter > 0, working_memory đã tồn tại nên nhúng trực tiếp.
//
// Ngay cả khi Bundle rỗng vẫn nhúng để giữ trường ổn định, tránh LLM thấy user_rules=null rồi đi vào nhánh bất thường.
//
// Chiến lược nhúng: chỉ cho LLM xem structured + preferences, vì đây mới là hai mục cần tuân thủ khi sáng tác.
// sources / conflicts là thông tin chẩn đoán (để người dùng tra xung đột), không đưa vào LLM; CLI sẽ hiển thị trong bảng chẩn đoán khi cần.
func (t *ContextTool) buildUserRules(result map[string]any, reads *contextReads) {
	snap, err := t.store.UserRules.Load()
	if err != nil {
		reads.require("user_rules", err)
	}
	if snap == nil {
		// Khi snapshot chưa khởi tạo, dùng mặc định dựng sẵn trong mã để đảm bảo các giới hạn cơ học (số chữ/từ cấm/từ gây mệt) luôn tồn tại.
		def := rules.BuildSnapshot([]rules.Candidate{rules.SystemDefaults()})
		snap = &def
	}
	working, ok := result["working_memory"].(map[string]any)
	if !ok {
		working = map[string]any{}
		result["working_memory"] = working
	}
	working["user_rules"] = snap.Payload()
}

func (t *ContextTool) buildSimulationProfile(result map[string]any, sectionKey string, reads *contextReads) {
	profile, err := t.store.Simulation.Load()
	if err != nil {
		reads.warn("simulation_profile", err)
		return
	}
	compact := domain.CompactSimulationProfile(profile)
	if compact == nil {
		return
	}
	section, ok := result[sectionKey].(map[string]any)
	if !ok {
		section = map[string]any{}
		result[sectionKey] = section
	}
	section["simulation_profile"] = compact
}

func (t *ContextTool) buildBaseContext(result map[string]any, reads *contextReads) {
	if book, err := t.store.Book.Load(); err == nil && book != nil {
		result["book"] = book
	} else {
		reads.require("book", err)
	}
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		result["premise"] = premise
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			result["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		} else {
			reads.require("run_meta", err)
		}
		result["premise_structure"] = premiseStructure(premise, tier)
	} else {
		reads.require("premise", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		result["world_rules"] = rules
	} else {
		reads.require("world_rules", err)
	}
}

func (t *ContextTool) prepareChapterContext(chapter int, envelope *chapterContextEnvelope, reads *contextReads) contextBuildState {
	state := contextBuildState{
		chapter: chapter,
		profile: domain.NewContextProfile(0),
	}

	progress, err := t.store.Progress.Load()
	reads.require("progress", err)
	runMeta, err := t.store.RunMeta.Load()
	reads.require("run_meta", err)
	state.progress = progress
	state.runMeta = runMeta

	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Episodic["planning_tier"] = runMeta.PlanningTier
	}
	if progress != nil && progress.TotalChapters > 0 {
		state.profile = domain.NewContextProfile(progress.TotalChapters)
	}
	if progress == nil || !progress.Layered {
		state.profile.Layered = false
	}

	outline, outlineErr := t.store.Outline.LoadOutline()
	reads.require("outline", outlineErr)
	state.outline = outline
	currentEntry := findOutlineEntry(outline, chapter)
	if currentEntry != nil {
		envelope.Working["current_chapter_outline"] = currentEntry
	}
	state.currentEntry = currentEntry

	chapterPlan, chapterPlanErr := t.store.Drafts.LoadChapterPlan(chapter)
	if chapterPlanErr == nil && chapterPlan != nil {
		envelope.Working["chapter_plan"] = chapterPlan
		if len(chapterPlan.Contract.RequiredBeats) > 0 ||
			len(chapterPlan.Contract.ForbiddenMoves) > 0 ||
			len(chapterPlan.Contract.ContinuityChecks) > 0 ||
			len(chapterPlan.Contract.EvaluationFocus) > 0 ||
			chapterPlan.Contract.EmotionTarget != "" ||
			len(chapterPlan.Contract.PayoffPoints) > 0 ||
			chapterPlan.Contract.HookGoal != "" {
			envelope.Working["chapter_contract"] = chapterPlan.Contract
		}
	} else {
		reads.require("chapter_plan", chapterPlanErr)
	}
	state.chapterPlan = chapterPlan

	// Chương này có đang được viết lại hay không: quyết định novel_context có bổ sung dữ kiện "chuyên dùng khi viết lại" hay không.
	isRewrite := progress != nil && slices.Contains(progress.PendingRewrites, chapter)

	// Công bố dữ kiện bản nháp đã tồn tại hay chưa: giúp writer tự quyết định bỏ qua hay ghi đè khi được phái lại.
	// Chỉ công bố exists + word_count, không nhúng chính văn (để writer dùng read_chapter lấy khi cần).
	if _, draftWords, draftErr := t.store.Drafts.LoadChapterContent(chapter); draftErr == nil && draftWords > 0 {
		envelope.Working["chapter_draft"] = map[string]any{
			"exists":     true,
			"word_count": draftWords,
		}
	} else if draftErr != nil {
		reads.require("chapter_draft", draftErr)
	}

	// Khi viết lại, giao "vì sao sửa + sửa chỗ nào" cho writer: lý do đến từ hàng đợi làm lại, phê bình cụ thể đến từ đánh giá của chính chương này
	// (selectReviewLessons chỉ truy hồi chapter-1..chapter-3 nên bỏ sót chính chương này, còn writer không có công cụ đọc đánh giá).
	// Không nhúng chính văn ở đây, để giữ quy ước "chính văn được lấy bằng read_chapter khi cần".
	if isRewrite {
		brief := map[string]any{"reason": progress.RewriteReason}
		if reviews, reviewErr := t.store.World.LoadReviewsAffectingChapter(chapter); reviewErr == nil {
			var sources []map[string]any
			for _, review := range reviews {
				item := map[string]any{
					"review_chapter": review.Chapter,
					"scope":          review.Scope,
					"summary":        review.Summary,
				}
				var issues []domain.ConsistencyIssue
				for _, issue := range review.Issues {
					// Đánh giá mới phân phát chính xác theo ánh xạ vấn đề đến chương; đánh giá cũ chưa có ánh xạ thì giữ
					// toàn bộ vấn đề để tránh lý do làm lại trong lịch sử biến mất sau nâng cấp.
					if len(issue.Chapters) == 0 || (issue.RequiresChange && slices.Contains(issue.Chapters, chapter)) {
						issues = append(issues, issue)
					}
				}
				if len(issues) > 0 {
					item["issues"] = issues
				}
				if review.Scope == "chapter" && len(review.ContractMisses) > 0 {
					item["contract_misses"] = review.ContractMisses
				}
				sources = append(sources, item)
			}
			if len(sources) > 0 {
				brief["reviews"] = sources
				// Khi chỉ có một nguồn, giữ trường cũ để tránh mất thông tin cho bên tiêu thụ ngữ cảnh hiện có khi nâng cấp.
				if len(sources) == 1 {
					brief["review_summary"] = sources[0]["summary"]
					if issues, ok := sources[0]["issues"]; ok {
						brief["issues"] = issues
					}
					if misses, ok := sources[0]["contract_misses"]; ok {
						brief["contract_misses"] = misses
					}
				}
			}
		} else {
			reads.require("rewrite_review", reviewErr)
		}
		envelope.Working["rewrite_brief"] = brief
	}

	foreshadow, foreshadowErr := t.store.World.LoadActiveForeshadow()
	reads.require("foreshadow_ledger", foreshadowErr)
	state.foreshadow = foreshadow

	relationships, relErr := t.store.World.LoadRelationships()
	reads.require("relationship_state", relErr)
	if len(relationships) > 0 {
		envelope.Episodic["relationship_state"] = relationships
	}
	state.relationships = relationships

	allStateChanges, scErr := t.store.World.LoadStateChanges()
	reads.require("recent_state_changes", scErr)
	state.allStateChanges = allStateChanges
	if len(allStateChanges) > 0 {
		start := max(chapter-2, 1)
		var recent []domain.StateChange
		for _, c := range allStateChanges {
			if c.Chapter >= start && c.Chapter < chapter {
				recent = append(recent, c)
			}
		}
		if len(recent) > 0 {
			envelope.Episodic["recent_state_changes"] = recent
		}
	}

	styleRules, styleErr := t.store.World.LoadStyleRules()
	reads.require("style_rules", styleErr)
	state.styleRules = styleRules
	state.storyThreads = t.selectStoryThreads(state)
	if len(state.storyThreads) > 0 && len(state.storyThreads) < storyThreadRecallMinSelected {
		state.storyThreads = nil
	}

	return state
}

func (t *ContextTool) buildChapterContext(result map[string]any, state contextBuildState, reads *contextReads) {
	envelope := newChapterContextEnvelope()
	result["memory_policy"] = domain.NewChapterMemoryPolicy(state.progress, state.profile, state.currentEntry != nil)

	if state.profile.Layered {
		t.loadLayeredCharacters(envelope.Episodic, state.chapter, reads)
	} else {
		t.loadFilteredCharacters(envelope.Episodic, state.chapter, reads)
	}

	t.buildChapterEpisodicMemory(&envelope, state, reads)
	t.buildChapterWorkingMemory(&envelope, state, reads)
	t.buildChapterReferencePack(&envelope, state, reads)
	t.buildChapterSelectedMemory(&envelope, state, reads)
	t.buildStyleStats(&envelope, state, reads)
	envelope.apply(result)
}

// buildStyleStats thống kê phong cách cấp toàn sách trên toàn bộ chương đã hoàn tất và nhúng vào episodic_memory.style_stats.
// Cửa sổ đánh giá trong cung vốn mù với "tic cú pháp xuất hiện hàng chục lần mỗi chương, hình thái cuối chương đồng dạng, lặp lại xuyên chương"; chỉ
// thống kê toàn sách mới phơi ra được: thống kê do mã thực hiện (xác định), phán định do LLM đảm nhiệm (editor ở chiều aesthetic
// chấm theo số liệu, writer dựa vào đó để tự tránh). Khi số chương chưa đủ, stylestat trả nil và không nhúng.
func (t *ContextTool) buildStyleStats(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	if state.progress == nil || len(state.progress.CompletedChapters) == 0 {
		return
	}

	var titles []string
	if outline, err := t.store.Outline.LoadOutline(); err == nil {
		for _, entry := range outline {
			titles = append(titles, entry.Title)
		}
	} else {
		reads.warn("style_stats.outline", err)
	}

	stats, err := t.styleStats.Snapshot(
		state.progress.CompletedChapters,
		titles,
		t.styleStopwords(reads),
	)
	if err != nil {
		reads.warn("style_stats", err)
		return
	}
	if stats == nil {
		return
	}
	envelope.Episodic["style_stats"] = stats
}

// styleStopwords thu thập tên nhân vật và bí danh để lọc khi khai phá cụm từ; tên nhân vật xuất hiện vốn dĩ có tần suất cao, không phải vấn đề văn phong.
func (t *ContextTool) styleStopwords(reads *contextReads) []string {
	var words []string
	if chars, err := t.store.Characters.Load(); err == nil {
		for _, c := range chars {
			words = append(words, c.Name)
			words = append(words, c.Aliases...)
		}
	} else {
		reads.warn("style_stats.characters", err)
	}
	if cast, err := t.store.Cast.RecentActive(50); err == nil {
		for _, e := range cast {
			words = append(words, e.Name)
			words = append(words, e.Aliases...)
		}
	} else {
		reads.warn("style_stats.cast", err)
	}
	return words
}

func (t *ContextTool) buildChapterWorkingMemory(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	t.buildOutlineWindow(envelope.Working, state, reads)
	if next := findOutlineEntry(state.outline, state.chapter+1); next != nil {
		envelope.Working["next_chapter_outline"] = next
	}

	if state.profile.Layered {
		t.loadLayeredSummaries(envelope.Working, state.chapter, state.profile.SummaryWindow, reads)
		// Kỷ luật kết thúc: nhúng khi chương này thuộc tập kết thúc đã công bố, để ngăn writer mở móc câu mới ở đoạn kết
		//（tập kết thúc sẽ tự hoàn tất sau khi viết xong; phục bút mới chôn lúc này sẽ không còn cơ hội thu hồi）。
		if volumes, err := t.store.Outline.LoadLayeredOutline(); err == nil {
			if fv := domain.FinaleVolume(volumes); fv > 0 {
				if b, boundaryErr := t.store.Outline.CheckArcBoundary(state.chapter); boundaryErr == nil && b != nil && b.Volume == fv {
					envelope.Working["finale"] = "Tập này là tập kết thúc toàn sách: không mở tuyến dài mới hoặc chôn phục bút mới; ưu tiên thu hồi phục bút hiện có, khép các tuyến quan hệ và đẩy câu chuyện đến kết cục theo đại cương."
				} else {
					reads.require("arc_boundary", boundaryErr)
				}
			}
		} else {
			reads.require("layered_outline", err)
		}
	} else {
		if summaries, err := t.store.Summaries.LoadRecentSummaries(state.chapter, state.profile.SummaryWindow); err == nil && len(summaries) > 0 {
			envelope.Working["recent_summaries"] = summaries
		} else {
			reads.require("recent_summaries", err)
		}
	}

	if timeline, err := t.store.World.LoadRecentTimeline(state.chapter, state.profile.TimelineWindow); err == nil && len(timeline) > 0 {
		envelope.Working["timeline"] = timeline
	} else {
		reads.require("timeline", err)
	}

	if state.progress != nil {
		checkpoint := map[string]any{
			"in_progress_chapter": state.progress.InProgressChapter,
		}
		if len(state.progress.StrandHistory) > 0 {
			checkpoint["strand_history"] = state.progress.StrandHistory
		}
		if len(state.progress.HookHistory) > 0 {
			checkpoint["hook_history"] = state.progress.HookHistory
		}
		envelope.Working["checkpoint"] = checkpoint
	}

	if state.chapter > 1 {
		if prevText, err := t.store.Drafts.LoadChapterText(state.chapter - 1); err == nil && prevText != "" {
			runes := []rune(prevText)
			if len(runes) > 800 {
				runes = runes[len(runes)-800:]
			}
			envelope.Working["previous_tail"] = string(runes)
		} else {
			reads.require("previous_chapter", err)
		}
	}
}

// buildOutlineWindow giữ cho Writer/Editor phần đại cương liên quan trực tiếp đến nhiệm vụ hiện tại, thay vì nhúng
// đại cương phẳng đầy đủ tăng theo toàn sách. Chế độ phân tầng dùng cung hiện tại; chế độ không phân tầng dùng chu kỳ đánh giá gần nhất.
func (t *ContextTool) buildOutlineWindow(working map[string]any, state contextBuildState, reads *contextReads) {
	outline := state.outline
	if len(outline) == 0 {
		return
	}

	start := max(1, state.chapter-domain.ReviewInterval+1)
	end := min(state.chapter, len(outline))
	if state.profile.Layered {
		boundary, err := t.store.Outline.CheckArcBoundary(state.chapter)
		if err != nil {
			reads.require("outline_window.arc_boundary", err)
			return
		}
		if boundary == nil {
			return
		}
		start = boundary.StartChapter
		end = min(boundary.EndChapter, len(outline))
	}
	if start <= end {
		working["outline_window"] = outline[start-1 : end]
	}
}

func findOutlineEntry(outline []domain.OutlineEntry, chapter int) *domain.OutlineEntry {
	for i := range outline {
		if outline[i].Chapter == chapter {
			return &outline[i]
		}
	}
	return nil
}

func (t *ContextTool) buildChapterSelectedMemory(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	if len(state.storyThreads) > 0 {
		envelope.Selected["story_threads"] = state.storyThreads
	}
	if lessons := t.selectReviewLessons(state.chapter, reads); len(lessons) > 0 {
		envelope.Selected["review_lessons"] = lessons
	}
}

func (t *ContextTool) buildChapterEpisodicMemory(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	if len(state.foreshadow) > 0 && len(state.storyThreads) == 0 {
		envelope.Episodic["foreshadow_ledger"] = state.foreshadow
	}

	// Danh sách vai phụ: truy hồi nhân vật phụ hoạt động gần đây để Writer giữ nhất quán giọng nói/vị trí khi đưa nhân vật cũ trở lại
	// Không truy hồi mọi mục (truyện dài sẽ phình ra), chỉ đưa N mục hoạt động gần nhất, sắp giảm dần theo LastSeenChapter
	if recentCast, err := t.store.Cast.RecentActive(15); err == nil && len(recentCast) > 0 {
		simplified := make([]map[string]any, 0, len(recentCast))
		for _, e := range recentCast {
			item := map[string]any{
				"name":             e.Name,
				"first_seen":       e.FirstSeenChapter,
				"last_seen":        e.LastSeenChapter,
				"appearance_count": e.AppearanceCount,
			}
			if e.BriefRole != "" {
				item["brief_role"] = e.BriefRole
			}
			if len(e.Aliases) > 0 {
				item["aliases"] = e.Aliases
			}
			simplified = append(simplified, item)
		}
		envelope.Episodic["recent_cast"] = simplified
	} else if err != nil {
		reads.warn("recent_cast", err)
	}

	if state.progress != nil && state.progress.TotalChapters > 30 && state.currentEntry != nil {
		if related := t.buildRelatedChapters(
			state.chapter,
			state.currentEntry,
			state.foreshadow,
			state.relationships,
			state.allStateChanges,
			reads,
		); len(related) > 0 {
			envelope.Episodic["related_chapters"] = related
		}
	}

	if state.profile.Layered && state.progress != nil {
		pos := map[string]any{
			"volume": state.progress.CurrentVolume,
			"arc":    state.progress.CurrentArc,
		}
		if volumes, err := t.store.Outline.LoadLayeredOutline(); err == nil {
			globalCh := 1
			for _, v := range volumes {
				if v.Index == state.progress.CurrentVolume {
					pos["volume_title"] = v.Title
					pos["volume_theme"] = v.Theme
				}
				for _, arc := range v.Arcs {
					if v.Index == state.progress.CurrentVolume && arc.Index == state.progress.CurrentArc {
						pos["arc_title"] = arc.Title
						pos["arc_goal"] = arc.Goal
						if n := len(arc.Chapters); n > 0 {
							pos["arc_total_chapters"] = n
							pos["arc_chapter_index"] = state.chapter - globalCh + 1
						}
					}
					globalCh += len(arc.Chapters)
				}
			}
		} else {
			reads.require("layered_outline", err)
		}
		envelope.Episodic["position"] = pos
	}
}

func (t *ContextTool) buildChapterReferencePack(envelope *chapterContextEnvelope, state contextBuildState, reads *contextReads) {
	authorStyle, err := t.store.World.LoadAuthorRevisionStyle()
	reads.warn("author_revision_style", err)
	if authorStyle != nil && (len(authorStyle.Prose) > 0 || len(authorStyle.Dialogue) > 0 || len(authorStyle.Taboos) > 0) {
		envelope.References["author_revision_style"] = authorStyle
	}

	if state.styleRules != nil {
		envelope.References["style_rules"] = state.styleRules
	} else {
		var maxCompleted int
		if state.progress != nil {
			maxCompleted = maxCompletedChapter(state.progress.CompletedChapters)
		}
		anchors, err := t.store.Drafts.ExtractStyleAnchors(3, maxCompleted)
		reads.warn("style_anchors", err)
		if len(anchors) > 0 {
			envelope.References["style_anchors"] = anchors
		}

		if state.currentEntry != nil {
			var voiceSamples []map[string]any
			chars, err := t.store.Characters.Load()
			reads.warn("voice_samples.characters", err)
			for _, c := range chars {
				if c.Tier == "secondary" || c.Tier == "decorative" {
					continue
				}
				samples, err := t.store.Drafts.ExtractDialogue(c.Name, c.Aliases, 3, maxCompleted)
				reads.warn("voice_samples."+c.Name, err)
				if len(samples) > 0 {
					voiceSamples = append(voiceSamples, map[string]any{
						"character": c.Name,
						"samples":   samples,
					})
				}
				if len(voiceSamples) >= 5 {
					break
				}
			}
			if len(voiceSamples) > 0 {
				envelope.References["voice_samples"] = voiceSamples
			}
		}
	}

	envelope.References["references"] = t.writerReferences(state.chapter)
}

func (t *ContextTool) buildArchitectContext(result map[string]any, reads *contextReads) {
	envelope := newArchitectContextEnvelope()
	result["memory_policy"] = domain.NewArchitectMemoryPolicy()
	t.buildArchitectPlanning(&envelope, reads)
	t.buildArchitectFoundation(&envelope, reads)
	t.buildArchitectReferences(&envelope, reads)
	envelope.apply(result)
}

func (t *ContextTool) buildArchitectPlanning(envelope *architectContextEnvelope, reads *contextReads) {
	runMeta, err := t.store.RunMeta.Load()
	reads.require("run_meta", err)
	if runMeta != nil && runMeta.PlanningTier != "" {
		envelope.Planning["planning_tier"] = runMeta.PlanningTier
	}
	progress, progressErr := t.store.Progress.Load()
	reads.require("progress_for_planning", progressErr)

	var layered []domain.VolumeOutline
	if l, err := t.store.Outline.LoadLayeredOutline(); err == nil && len(l) > 0 {
		layered = l
		latestCompleted := 0
		if progress != nil {
			latestCompleted = progress.LatestCompleted()
		}
		if latestCompleted > 0 {
			envelope.Planning["layered_outline"] = projectLayeredOutlineForPlanning(layered, latestCompleted)
		} else {
			envelope.Planning["layered_outline"] = layered
		}
		var skeletonArcs []map[string]any
		for _, v := range layered {
			for _, a := range v.Arcs {
				if !a.IsExpanded() {
					skeletonArcs = append(skeletonArcs, map[string]any{
						"volume":             v.Index,
						"arc":                a.Index,
						"title":              a.Title,
						"goal":               a.Goal,
						"estimated_chapters": a.EstimatedChapters,
					})
				}
			}
		}
		if len(skeletonArcs) > 0 {
			envelope.Planning["skeleton_arcs"] = skeletonArcs
		}
	} else {
		reads.require("layered_outline", err)
	}
	if len(layered) == 0 {
		if outline, err := t.store.Outline.LoadOutline(); err == nil && len(outline) > 0 {
			envelope.Planning["outline"] = outline
		} else {
			reads.require("outline", err)
		}
	}

	var compass *domain.StoryCompass
	if c, err := t.store.Outline.LoadCompass(); err == nil && c != nil {
		compass = c
		envelope.Planning["compass"] = compass
	} else {
		reads.require("compass", err)
	}
	if volSummaries, err := t.store.Summaries.LoadAllVolumeSummaries(); err == nil && len(volSummaries) > 0 {
		envelope.Planning["volume_summaries"] = volSummaries
	} else {
		reads.require("volume_summaries", err)
	}
	// Tóm tắt tập nối tiếp các tập đã hoàn tất; tóm tắt cung của tập hiện tại nối tiếp tình tiết thực tế gần nhất. Khi mở rộng cung, hai phần này cùng với
	// mục tiêu khung được giao đồng thời cho Architect, để mô hình tự quyết định giữ lại hay sửa kế hoạch chưa viết.
	if progressErr == nil && progress != nil && progress.CurrentVolume > 0 {
		if arcSummaries, err := t.store.Summaries.LoadArcSummaries(progress.CurrentVolume); err == nil && len(arcSummaries) > 0 {
			envelope.Planning["arc_summaries"] = arcSummaries
		} else {
			reads.require("arc_summaries", err)
		}
	} else {
		reads.require("progress_for_arc_summaries", progressErr)
	}

	// completion_signals tập trung trình bày các dữ kiện then chốt về việc "toàn sách đã nên kết thúc chưa",
	// để kiến trúc sư thấy ngay mặt đối chiếu khi quyết định complete_book / append_volume.
	// Nếu nằm rải rác trong progress / compass / foreshadow / layered_outline và để LLM tự tính nhẩm thì dễ sót.
	envelope.Planning["completion_signals"] = t.completionSignals(layered, compass, reads)
}

func projectLayeredOutlineForPlanning(volumes []domain.VolumeOutline, latestCompleted int) []planningVolumeOutline {
	projected := make([]planningVolumeOutline, 0, len(volumes))
	chapter := 1
	for _, volume := range volumes {
		pv := planningVolumeOutline{
			Index: volume.Index, Title: volume.Title, Theme: volume.Theme, Final: volume.Final,
			Arcs: make([]planningArcOutline, 0, len(volume.Arcs)),
		}
		for _, arc := range volume.Arcs {
			pa := planningArcOutline{
				Index: arc.Index, Title: arc.Title, Goal: arc.Goal,
				EstimatedChapters: arc.EstimatedChapters,
			}
			if len(arc.Chapters) == 0 {
				pa.Status = "skeleton"
				pv.Arcs = append(pv.Arcs, pa)
				continue
			}
			pa.StartChapter = chapter
			pa.EndChapter = chapter + len(arc.Chapters) - 1
			pa.ChapterCount = len(arc.Chapters)
			if pa.EndChapter <= latestCompleted {
				pa.Status = "completed"
			} else {
				pa.Status = "expanded"
				pa.Chapters = arc.Chapters
			}
			chapter = pa.EndChapter + 1
			pv.Arcs = append(pv.Arcs, pa)
		}
		projected = append(projected, pv)
	}
	return projected
}

func (t *ContextTool) completionSignals(layered []domain.VolumeOutline, compass *domain.StoryCompass, reads *contextReads) map[string]any {
	signals := map[string]any{}
	if progress, err := t.store.Progress.Load(); progress != nil {
		signals["completed_chapters"] = len(progress.CompletedChapters)
		signals["total_word_count"] = progress.TotalWordCount
		signals["phase"] = string(progress.Phase)
	} else {
		reads.require("completion_signals.progress", err)
	}
	if len(layered) > 0 {
		signals["planned_chapters"] = len(domain.FlattenOutline(layered))
		signals["volumes_total"] = len(layered)
		if fv := domain.FinaleVolume(layered); fv > 0 {
			signals["final_volume"] = fv
		}
	}
	if compass != nil {
		if compass.EstimatedScale != "" {
			signals["compass_estimated_scale"] = compass.EstimatedScale
		}
		signals["open_threads_count"] = len(compass.OpenThreads)
	}
	if active, err := t.store.World.LoadActiveForeshadow(); err == nil {
		signals["active_foreshadow_count"] = len(active)
	} else {
		reads.require("completion_signals.foreshadow", err)
	}
	return signals
}

func (t *ContextTool) buildArchitectFoundation(envelope *architectContextEnvelope, reads *contextReads) {
	if book, err := t.store.Book.Load(); err == nil && book != nil {
		envelope.Foundation["book"] = book
	} else {
		reads.require("book", err)
	}
	if premise, err := t.store.Outline.LoadPremise(); err == nil && premise != "" {
		envelope.Foundation["premise"] = premise
		if sections := parsePremiseSections(premise); len(sections) > 0 {
			envelope.Foundation["premise_sections"] = sections
		}
		tier := domain.PlanningTier("")
		if meta, err := t.store.RunMeta.Load(); err == nil && meta != nil {
			tier = meta.PlanningTier
		} else {
			reads.require("run_meta", err)
		}
		envelope.Foundation["premise_structure"] = premiseStructure(premise, tier)
	} else {
		reads.require("premise", err)
	}

	if chars, err := t.store.Characters.Load(); err == nil && chars != nil {
		envelope.Foundation["characters"] = chars
	} else {
		reads.require("characters", err)
	}

	if snapshots, err := t.store.Characters.LoadLatestSnapshots(); err == nil && len(snapshots) > 0 {
		envelope.Foundation["character_snapshots"] = snapshots
	} else {
		reads.require("character_snapshots", err)
	}
	if rules, err := t.store.World.LoadWorldRules(); err == nil && len(rules) > 0 {
		envelope.Foundation["world_rules"] = rules
	} else {
		reads.require("world_rules", err)
	}
	if foreshadow, err := t.store.World.LoadActiveForeshadow(); err == nil && len(foreshadow) > 0 {
		envelope.Foundation["foreshadow_ledger"] = foreshadow
	} else {
		reads.require("foreshadow_ledger", err)
	}
	if status, err := t.foundationStatus(); err == nil {
		envelope.Foundation["foundation_status"] = status
	} else {
		reads.require("foundation_status", err)
	}
	// Bể phản hồi của writer cho commit_chapter: độ lệch/gợi ý dàn ý; phải tham khảo khi lập kế hoạch cung hoặc tập tiếp theo;
	// expand_arc / append_volume / update_compass dùng để xử lý phản hồi đó.
	if fbs, err := t.store.Outline.LoadPendingOutlineFeedback(); err == nil && len(fbs) > 0 {
		envelope.Foundation["writer_feedback"] = fbs
	} else {
		reads.require("writer_feedback", err)
	}
}

func (t *ContextTool) buildArchitectReferences(envelope *architectContextEnvelope, reads *contextReads) {
	if styleRules, err := t.store.World.LoadStyleRules(); err == nil && styleRules != nil {
		envelope.References["style_rules"] = styleRules
	} else {
		reads.warn("style_rules", err)
	}

	envelope.References["references"] = t.architectReferences()
}
