package agents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
	"github.com/voocel/agentcore/llm"
	"github.com/voocel/agentcore/subagent"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/agents/guard"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// agentToRole chuẩn hóa subagent name thành role mà ModelSet nhận biết.
// architect_short / architect_long dùng chung cấu hình role architect.
// Đồng nghĩa với host.agentRoleName; vì build và host không phụ thuộc nhau nên mỗi bên giữ một bản.
func agentToRole(name string) string {
	if strings.HasPrefix(name, "architect_") {
		return "architect"
	}
	return name
}

// promptCacheBase sinh hash ngắn ổn định từ thư mục sách làm tiền tố định danh cache prompt: cùng một sách
// chia sẻ bucket định tuyến qua các lần khởi động lại tiến trình, và không lộ đường dẫn cục bộ cho provider. Hậu tố role do bên gọi nối thêm,
// mỗi lần spawn subagent nối thêm "#seq" (một khóa cho mỗi phiên).
func promptCacheBase(bookDir string) string {
	sum := sha256.Sum256([]byte(bookDir))
	return "nvl-" + hex.EncodeToString(sum[:6])
}

// subagentMaxRetries là giới hạn retry LLM cho mọi Worker.
// Chiến lược backoff: backoff lũy thừa (bị chặn bởi maxDelay), ưu tiên tuân theo Retry-After của server.
// Tool chỉ khởi động sau khi thông điệp Assistant hoàn chỉnh được submit, nên stream-idle / 503 /
// dao động mạng ngắn có thể retry an toàn trong Worker, không phát lại side effect của tool.
const subagentMaxRetries = 7

// UsageRecorder là callback usage tùy chọn của BuildWorkers; chữ ký giống OnMessage,
// mỗi thông điệp agent đều gọi một lần, tầng Host chịu trách nhiệm tổng hợp. task là văn bản nhiệm vụ của lần spawn này
// dùng làm định danh phiên, để phát hiện đứt chuỗi cache reset baseline theo phiên.
// nil nghĩa là không theo dõi.
type UsageRecorder func(agentName, task string, msg agentcore.AgentMessage)

// ApplyThinking áp dụng mức suy luận của một role cụ thể vào Worker (dùng khi chỉnh /model lúc chạy).
// architect → hai subagent architect_*; writer/editor → subagent tương ứng.
// level rỗng = dùng tiếp mặc định của mô hình/provider. Các tên role khác bị bỏ qua.
type ApplyThinking func(role string, level agentcore.ThinkingLevel)

// ParseThinkingLevel chuyển chuỗi cấu hình thành agentcore.ThinkingLevel.
// "" hợp lệ (= không override/kế thừa); các giá trị còn lại phải là một trong off/low/medium/high/xhigh/max,
// nếu không sẽ trả error (lúc khởi động hạ cấp như rỗng và warn, lúc chạy echo error cho người dùng).
func ParseThinkingLevel(s string) (agentcore.ThinkingLevel, error) {
	lv := agentcore.NormalizeThinkingLevel(agentcore.ThinkingLevel(s))
	switch lv {
	case "", agentcore.ThinkingOff, agentcore.ThinkingLow, agentcore.ThinkingMedium,
		agentcore.ThinkingHigh, agentcore.ThinkingXHigh, agentcore.ThinkingMax:
		return lv, nil
	default:
		return "", fmt.Errorf("Mức suy luận không hợp lệ %q (có thể chọn: off/low/medium/high/xhigh/max)", s)
	}
}

func ResolveThinkingForModel(model agentcore.ChatModel, level agentcore.ThinkingLevel) (agentcore.ThinkingLevel, bool) {
	level = agentcore.NormalizeThinkingLevel(level)
	// Với mô hình chat thường không hỗ trợ thinking, off tường minh không phải no-op mà là tham số không hợp lệ.
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return agentcore.ThinkingAuto, level == agentcore.ThinkingAuto
	}
	return llm.ThinkingPolicyFor(model).Resolve(level)
}

func AvailableThinkingForModel(model agentcore.ChatModel) []agentcore.ThinkingLevel {
	if cp, ok := model.(llm.CapabilityProvider); ok && cp.Capabilities().Thinking.Supported == llm.SupportNo {
		return []agentcore.ThinkingLevel{agentcore.ThinkingAuto}
	}
	return llm.ThinkingPolicyFor(model).Available
}

// roleThinking phân tích mức suy luận có hiệu lực của một role; giá trị không hợp lệ hạ cấp thành rỗng (không override) và warn.
func roleThinking(cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	lv, err := ParseThinkingLevel(cfg.ResolveReasoningEffort(role))
	if err != nil {
		slog.Warn("Bỏ qua cấu hình mức suy luận không hợp lệ", "module", "agent", "role", role, "err", err)
		return ""
	}
	return lv
}

func resolvedRoleThinking(model agentcore.ChatModel, cfg bootstrap.Config, role string) agentcore.ThinkingLevel {
	resolved, _ := ResolveThinkingForModel(model, roleThinking(cfg, role))
	return resolved
}

// BuildWorkers lắp ba Worker (architect_short/long, writer, editor) thành runner subagent có thể gọi bằng chương trình
// Engine gọi trực tiếp entry có kiểu của runner, không có tầng tool LLM
// (docs/engine-rfc.md §1)。
// Trả về Runner, WriterRestorePack và ApplyThinking (liên động mức suy luận từng role qua /model lúc chạy;
// ContextManager của writer/architect/editor tự dựng lại qua factory).
// onGuardBlock là tùy chọn (nil an toàn): callback audit chặn/escalate của StopGuard từng Worker.
func BuildWorkers(
	cfg bootstrap.Config,
	store *store.Store,
	styleStats *tools.StyleStatsIndex,
	models *bootstrap.ModelSet,
	bundle assets.Bundle,
	recordUsage UsageRecorder,
	onGuardBlock guard.BlockHook,
) (*subagent.Runner, *ctxpack.WriterRestorePack, ApplyThinking) {
	// Tool dùng chung
	contextTool := tools.NewContextTool(store, bundle.References, cfg.Style, styleStats)
	readChapter := tools.NewReadChapterTool(store)

	architectTools := []agentcore.Tool{
		contextTool,
		tools.NewSaveBookTool(store),
		tools.NewSaveFoundationTool(store),
		tools.NewReviseOutlineTool(store),
		tools.NewResolveOutlineFeedbackTool(store),
		tools.NewAuditFoundationTool(store),
	}
	writerTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewPlanChapterTool(store),
		tools.NewDraftChapterTool(store),
		tools.NewEditChapterTool(store),
		tools.NewCheckConsistencyTool(store),
		tools.NewCommitChapterTool(store, styleStats),
	}
	editorTools := []agentcore.Tool{
		contextTool,
		readChapter,
		tools.NewSaveReviewTool(store),
		tools.NewSaveArcSummaryTool(store),
		tools.NewSaveVolumeSummaryTool(store),
	}

	// Provider failover chỉ ghi log, không thông báo host
	reportFailover := func(ev bootstrap.FailoverEvent) {
		slog.Warn("Chuyển provider",
			"module", "agent",
			"role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err,
		)
	}

	architectModel := models.ForRoleWithFailover("architect", reportFailover)
	writerModel := models.ForRoleWithFailover("writer", reportFailover)
	editorModel := models.ForRoleWithFailover("editor", reportFailover)

	// ContextManager của Writer được dựng lại mỗi lần gọi bằng factory, cửa sổ động bám theo việc swap mô hình (xem factory bên dưới).
	writerProvider, writerModelName, _ := models.CurrentSelection("writer")
	writerContextWindow, writerSource := cfg.ResolveContextWindow(writerProvider, writerModelName)
	bootstrap.LogContextWindowChoice("writer", writerModelName, writerContextWindow, writerSource)

	// modelLookup gắn _meta:{provider,model} cho mỗi thông điệp assistant khi ghi session,
	// để replay không còn dựa vào "ModelSet hiện tại" để suy ngược cost lịch sử; đổi mô hình khi chạy vẫn tính chính xác.
	modelLookup := func(agentName string) (string, string) {
		role := agentToRole(agentName)
		provider, name, _ := models.CurrentSelection(role)
		return provider, name
	}
	baseOnMsg := store.Sessions.SubAgentLogger(modelLookup)
	onMsg := func(agentName, task string, msg agentcore.AgentMessage) {
		baseOnMsg(agentName, task, msg)
		if recordUsage != nil {
			recordUsage(agentName, task, msg)
		}
	}

	// Cache prompt: mỗi sách một base, mỗi role một tên, mỗi phiên một khóa (subagent spawn nối thêm #seq).
	// Dòng OpenAI dùng prompt_cache_key để ghim định tuyến; dòng Claude dùng cache_control cho điểm cắt cuộn
	//(nền system + mũi nhọn thông điệp cuối). Khi provider không hỗ trợ, agentcore âm thầm bỏ theo capability,
	// trong hội thoại nhiều lượt lợi ích đọc cache luôn dương, nên không đặt công tắc.
	cacheBase := promptCacheBase(store.Dir())

	architectStopGuardFactory := func(_, _ string) agentcore.StopGuard {
		return guard.NewArchitectStopGuard(store, onGuardBlock)
	}
	architectThinking, _ := ResolveThinkingForModel(architectModel, roleThinking(cfg, "architect"))
	architectShort := subagent.Config{
		Name:             "architect_short",
		Description:      "Planner truyện ngắn: tạo thiết lập gọn và dàn ý phẳng cho câu chuyện một quyển, một xung đột, mật độ cao",
		Model:            architectModel,
		SystemPrompt:     bundle.Prompts.ArchitectShort,
		Tools:            architectTools,
		MaxTurns:         15,
		MaxRetries:       subagentMaxRetries,
		ThinkingLevel:    architectThinking,
		OnMessage:        onMsg,
		CacheLastMessage: "ephemeral",
		PromptCacheKey:   cacheBase + "-architect_short",
		StopAfterToolResult: func(toolName string, result json.RawMessage) bool {
			return foundationReadyResult(toolName, result)
		},
		StopGuardFactory: architectStopGuardFactory,
	}
	architectLong := subagent.Config{
		Name:                "architect_long",
		Description:         "Planner truyện dài: tạo thiết lập phân tầng và dàn ý quyển/arc cho câu chuyện nhiều kỳ có thể leo thang bền vững",
		Model:               architectModel,
		SystemPrompt:        bundle.Prompts.ArchitectLong,
		Tools:               architectTools,
		MaxTurns:            20,
		MaxRetries:          subagentMaxRetries,
		ThinkingLevel:       architectThinking,
		OnMessage:           onMsg,
		CacheLastMessage:    "ephemeral",
		PromptCacheKey:      cacheBase + "-architect_long",
		StopAfterToolResult: architectLongShouldStopAfterToolResult,
		StopGuardFactory:    architectStopGuardFactory,
	}

	// Đường lắp duy nhất: template giao thức điền đoạn văn phong vào đúng vị trí {{VOICE}}, rồi nối thêm preset phong cách.
	// voice A/B của eval dùng cùng hàm, bảo đảm hai nhánh tương đương (docs/voice-layer.md §3.2).
	writerPrompt := assets.BuildWriterPrompt(bundle.Prompts.Writer, bundle.Voice, bundle.Styles[cfg.Style])

	restore := &ctxpack.WriterRestorePack{}
	restore.Refresh(store)

	writer := subagent.Config{
		Name:             "writer",
		Description:      "Tác giả: tự hoàn thành lên ý tưởng, viết, tự rà soát và submit một chương",
		Model:            writerModel,
		SystemPrompt:     writerPrompt,
		Tools:            writerTools,
		MaxTurns:         30,
		MaxRetries:       subagentMaxRetries,
		ThinkingLevel:    resolvedRoleThinking(writerModel, cfg, "writer"),
		StopAfterTools:   []string{"commit_chapter"},
		OnMessage:        onMsg,
		CacheLastMessage: "ephemeral",
		PromptCacheKey:   cacheBase + "-writer",
		StopGuardFactory: func(_, _ string) agentcore.StopGuard {
			return guard.NewWriterStopGuard(store, onGuardBlock)
		},
		ContextManagerFactory: func(model agentcore.ChatModel) agentcore.ContextManager {
			// Dựng lại context manager theo mô hình writer hiện tại cho mỗi chương.
			window, _ := models.ResolveContextWindow(bootstrap.ModelProvider(model), bootstrap.ModelName(model))
			return newContextManager(contextManagerConfig{
				Model:         model,
				ContextWindow: window,
				ReserveTokens: bootstrap.CompactReserveTokens(window),
				Agent:         "writer",
				// Projection khi commit, tránh các lượt sau viết lại lặp đi lặp lại tiền tố request.
				CommitProjected: true,
				ToolMicrocompact: &corecontext.ToolResultMicrocompactConfig{
					MinResultTokens: 200,
				},
				ExtraStrategies: []corecontext.Strategy{
					ctxpack.NewStoreSummaryCompact(ctxpack.StoreSummaryCompactConfig{
						Store:            store,
						KeepRecentTokens: 20000,
					}),
				},
				Summary: &corecontext.FullSummaryConfig{
					PostSummaryHooks:    []corecontext.PostSummaryHook{restore.Hook()},
					SystemPrompt:        ctxpack.WriterSummarySystemPrompt,
					SummaryPrompt:       ctxpack.WriterSummaryPrompt,
					UpdateSummaryPrompt: ctxpack.WriterUpdateSummaryPrompt,
					TurnPrefixPrompt:    ctxpack.WriterTurnPrefixPrompt,
				},
			})
		},
	}

	editor := subagent.Config{
		Name:             "editor",
		Description:      "Reviewer: đọc nguyên văn, phát hiện vấn đề ở cả hai tầng cấu trúc và thẩm mỹ",
		Model:            editorModel,
		SystemPrompt:     bundle.Prompts.Editor,
		Tools:            editorTools,
		MaxTurns:         20,
		MaxRetries:       subagentMaxRetries,
		ThinkingLevel:    resolvedRoleThinking(editorModel, cfg, "editor"),
		OnMessage:        onMsg,
		CacheLastMessage: "ephemeral",
		PromptCacheKey:   cacheBase + "-editor",
		// Dừng ngay khi khớp artifact trạng thái cuối. Thoát trạng thái cuối vẫn tham vấn StopGuard (kiểm thử contract TestContract_
		// TerminalToolExitConsultsStopGuard); NewEditorStopGuard nhận biết nhiệm vụ chịu trách nhiệm
		// từ chối thoát sớm kiểu "được giao tạo tóm tắt nhưng chỉ rà soát", nên save_review có thể hard stop an toàn.
		StopAfterToolResult: func(toolName string, _ json.RawMessage) bool {
			return toolName == "save_review" || toolName == "save_arc_summary" || toolName == "save_volume_summary"
		},
		StopGuardFactory: func(_, task string) agentcore.StopGuard {
			return guard.NewEditorStopGuard(store, task, onGuardBlock)
		},
	}

	runner := subagent.NewRunner(architectShort, architectLong, writer, editor)

	// Liên động mức suy luận từng role lúc chạy (dùng khi chỉnh /model).
	applyThinking := func(role string, level agentcore.ThinkingLevel) {
		switch role {
		case "architect":
			level, _ = ResolveThinkingForModel(models.ForRole("architect"), level)
			runner.SetThinkingLevel("architect_short", level)
			runner.SetThinkingLevel("architect_long", level)
		case "writer", "editor":
			level, _ = ResolveThinkingForModel(models.ForRole(role), level)
			runner.SetThinkingLevel(role, level)
		}
	}

	return runner, restore, applyThinking
}

type saveFoundationResult struct {
	Type            string `json:"type"`
	FoundationReady bool   `json:"foundation_ready"`
}

func decodeSaveFoundationResult(toolName string, result json.RawMessage) saveFoundationResult {
	if toolName != "save_foundation" {
		return saveFoundationResult{}
	}
	var r saveFoundationResult
	_ = json.Unmarshal(result, &r)
	return r
}

func architectLongShouldStopAfterToolResult(toolName string, result json.RawMessage) bool {
	if foundationReadyResult(toolName, result) {
		return true
	}
	r := decodeSaveFoundationResult(toolName, result)
	switch r.Type {
	case "expand_arc", "complete_book":
		return true
	default:
		return false
	}
}

func foundationReadyResult(toolName string, result json.RawMessage) bool {
	if toolName != "audit_foundation" {
		return false
	}
	var r struct {
		FoundationReady bool `json:"foundation_ready"`
	}
	return json.Unmarshal(result, &r) == nil && r.FoundationReady
}
