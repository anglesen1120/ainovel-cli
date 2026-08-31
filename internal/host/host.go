package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/arbiter"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/host/exp"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/host/sim"
	runtimelog "github.com/voocel/ainovel-cli/internal/logger"
	modelreg "github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/revision"
	"github.com/voocel/ainovel-cli/internal/rules"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
	"github.com/voocel/ainovel-cli/internal/userrules"
)

// Host là lớp vỏ thời gian chạy: vòng đời/cửa vào can thiệp/projection sự kiện/quản lý mô hình.
// Điều phối và thực thi nằm ở engine(vòng lặp quyết định); phán quyết ngữ nghĩa nằm ở arbiter(LLM-as-function).
type Host struct {
	cfg             bootstrap.Config
	bundle          assets.Bundle
	store           *storepkg.Store
	bookLease       *bookLease
	styleStats      *tools.StyleStatsIndex
	models          *bootstrap.ModelSet
	engine          *engine
	thinkingApplier agents.ApplyThinking // Khi /model điều chỉnh cường độ suy luận thì đồng bộ tới từng Worker
	writerRestore   *ctxpack.WriterRestorePack
	userRules       *userrules.Service
	observer        *observer
	usage           *UsageTracker
	usageCancel     context.CancelFunc  // Dừng autoSaveLoop và kích hoạt flush cuối cùng
	budget          *BudgetSentinel     // Chính sách ngân sách; nếu chưa bật thì là nil (phương thức an toàn với nil)
	gate            *ChapterAdvanceGate // Cổng tiến chương và chính sách tạm dừng một lần
	notifier        *notify.Notifier    // Cảnh báo không giám sát; nếu chưa bật thì là nil (Send an toàn với nil)
	configPath      string              // Mục tiêu ghi cấu hình: /config, /model ghi gần nhất bản đang có hiệu lực (nếu có cấp dự án thì ghi nó, nếu không thì ghi toàn cục)
	logCleanup      func()
	fileLogErr      error

	events   chan Event
	streamCh chan string
	done     chan struct{}

	mu         sync.Mutex
	lifecycle  lifecycle
	cocreating bool   // Chiếm dụng đồng sáng tạo theo giai đoạn: trong cửa sổ paused chặn can thiệp đồng thời vào import/simulate/continue
	exclusive  string // Chiếm dụng tác vụ độc quyền nền (import/phỏng theo/sửa đổi): chuỗi khác rỗng nghĩa là có một tác vụ đang chạy, chặn các cửa vào độc quyền đồng thời
	// exclusiveCancel là hàm hủy của tác vụ độc quyền hiện tại: dừng cứng vì ngân sách/hay tạm dừng thủ công phải có thể dừng cả phần đang tiêu tiền
	// import, chứ không chỉ Engine — abortWithEvent hủy nó khi Engine chưa chạy (callback abort của budget sentinel và Abort thủ công
	// dùng chung cơ chế dừng này). releaseExclusive sẽ dọn sạch cùng lúc.
	exclusiveCancel context.CancelFunc
	closeOnce       sync.Once
	asyncWG         sync.WaitGroup
	closing         bool

	interMu sync.Mutex // Phân xử can thiệp FIFO tuần tự (mỗi thời điểm nhiều nhất một lần hỏi ý kiến đang diễn ra)

	outputMu     sync.RWMutex
	outputClosed bool

	// runCtx ràng buộc các lời gọi phán quyết LLM phía Host (phán quyết khởi động/canh thiệp);
	// Close sẽ hủy để tránh lúc thoát vẫn còn phán quyết đang diễn ra và không thể dừng.
	runCtx    context.Context
	runCancel context.CancelFunc
}

type lifecycle string

const (
	lifecycleIdle      lifecycle = "idle"
	lifecycleRunning   lifecycle = "running"
	lifecyclePaused    lifecycle = "paused"
	lifecycleCompleted lifecycle = "completed"
)

// New tạo Host.
func New(cfg bootstrap.Config, bundle assets.Bundle, options ...NewOption) (*Host, error) {
	cfg.FillDefaults()
	if err := cfg.ValidateBase(); err != nil {
		return nil, err
	}
	var opts newOptions
	for _, option := range options {
		if option != nil {
			option(&opts)
		}
	}

	bookLease, err := acquireBookLease(cfg.OutputDir)
	if err != nil {
		return nil, err
	}
	keepBookLease := false
	var logCleanup func()
	defer func() {
		if keepBookLease {
			return
		}
		if err := bookLease.Close(); err != nil {
			slog.Error("không thể giải phóng chiếm dụng thư mục tiểu thuyết", "module", "host", "dir", cfg.OutputDir, "err", err)
		}
		if logCleanup != nil {
			logCleanup()
		}
	}()

	var fileLogErr error
	if opts.logFile != "" {
		logCleanup, fileLogErr = runtimelog.SetupFile(cfg.OutputDir, opts.logFile, opts.logAlsoStderr, opts.logAttrs...)
		if fileLogErr != nil {
			logCleanup = nil
			slog.Warn("nhật ký file không khả dụng, tiếp tục dùng nhật ký của tiến trình hiện tại", "module", "host", "file", opts.logFile, "err", fileLogErr)
		}
	}

	slog.Info("khởi động", "module", "boot", "provider", cfg.Provider, "model", cfg.ModelName, "output", cfg.OutputDir)

	// Khởi chạy một goroutine nền để làm mới siêu dữ liệu mô hình từ OpenRouter (cửa sổ/giá), bộ nhớ đệm đĩa trong 24h.
	modelreg.StartPricingRefresh(modelreg.DefaultRegistry(), bootstrap.DefaultConfigDir())

	store := storepkg.NewStore(cfg.OutputDir)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("khởi tạo store: %w", err)
	}
	// RunMeta là nguồn sự thật cho mọi ngữ nghĩa điều khiển, bắt buộc phải kiểm tra xong trước khi dựng mô hình/nhiệm vụ nền.
	// Nếu advance mode không hợp lệ thì trả lỗi có cấu trúc ngay; cấm đoán mò rồi hạ cấp và tiếp tục ghi đĩa.
	if err := store.RunMeta.Init(cfg.Style, cfg.Provider, cfg.ModelName); err != nil {
		return nil, fmt.Errorf("khởi tạo run meta: %w", err)
	}

	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return nil, fmt.Errorf("tạo models: %w", err)
	}
	slog.Info("mô hình đã sẵn sàng", "module", "boot", "summary", models.Summary())

	usage := NewUsageTracker(models, store)
	// Ưu tiên đọc meta/usage.json; các trường hợp sau đều đi qua sessions/*.jsonl để bù lại một lần:
	//   - file không tồn tại (trước lần lưu bền vững đầu tiên)
	//   - schema không khớp phiên bản (bỏ định dạng cũ sau nâng cấp trong tương lai)
	//   - file tồn tại nhưng hỏng / lỗi IO (không để dữ liệu xấu làm số tích lũy về 0 vĩnh viễn)
	// Sau khi bù xong thì SaveNow ngay để cố định kết quả, lần khởi động sau sẽ Load trúng luôn.
	loaded, loadErr := usage.LoadFromStore()
	if loadErr != nil {
		slog.Warn("tải usage thất bại, sẽ thử bù từ sessions", "module", "usage", "err", loadErr)
	}
	if !loaded {
		if n, err := usage.ReplaySessions(cfg.OutputDir); err != nil {
			slog.Warn("replay usage thất bại", "module", "usage", "err", err)
		} else if n > 0 {
			slog.Info("bù usage từ session hoàn tất", "module", "usage", "messages", n)
			if err := usage.SaveNow(); err != nil {
				slog.Warn("lưu sau khi bù usage thất bại", "module", "usage", "err", err)
			}
		}
	}
	usageCtx, usageCancel := context.WithCancel(context.Background())
	usage.StartAutoSave(usageCtx)

	// Khai báo trước onGuardBlock: chỉ sau khi h được dựng xong mới có thể gắn callback nổi sự kiện.
	var onGuardBlock func(agent, reason string, consecutive int32)
	styleStats := tools.NewStyleStatsIndex(store)
	workers, restore, applyThinking := agents.BuildWorkers(cfg, store, styleStats, models, bundle, usage.Record,
		func(agent, reason string, consecutive int32) {
			if onGuardBlock != nil {
				onGuardBlock(agent, reason, consecutive)
			}
		})
	store.Signals.ClearStaleSignals()

	h := &Host{
		cfg:             cfg,
		bundle:          bundle,
		store:           store,
		bookLease:       bookLease,
		styleStats:      styleStats,
		models:          models,
		thinkingApplier: applyThinking,
		writerRestore:   restore,
		userRules:       userrules.NewService(store, models.Default, rules.DefaultOptions()),
		usage:           usage,
		usageCancel:     usageCancel,
		configPath:      bootstrap.EffectiveConfigPath(),
		logCleanup:      logCleanup,
		fileLogErr:      fileLogErr,
		events:          make(chan Event, 100),
		streamCh:        make(chan string, 256),
		done:            make(chan struct{}, 4),
		lifecycle:       lifecycleIdle,
	}
	h.runCtx, h.runCancel = context.WithCancel(context.Background())
	h.observer = newObserver(store, h.emitEvent, h.emitDelta, h.emitClear)
	// Arbiter phía Host và Worker cùng dùng chung một chuỗi ToolProgress → observer → bàn làm việc.
	h.runCtx = agentcore.WithToolProgress(h.runCtx, h.observer.workerProgress)
	if cfg.Notify.IsEnabled() {
		h.notifier = notify.New(cfg.Notify.Command, cfg.Notify.Events)
	}
	// Sentinel ngân sách: Engine gọi thẳng HandleBoundary ở ranh giới mỗi vòng lặp (không qua subscribe sự kiện nữa).
	if sentinel := NewBudgetSentinel(cfg.Budget,
		func() float64 { c, _, _, _, _ := usage.Totals(); return c },
		func(reason string) { h.abortWithEvent(reason, "error") },
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: level, Title: "ainovel: ngân sách", Body: summary})
		},
	); sentinel != nil {
		h.budget = sentinel
		usage.SetOnCost(sentinel.OnCost)
		// Cảnh báo vùng mù tính phí: nếu mô hình không báo usage thì chi phí luôn bằng 0, ngân sách sẽ không bao giờ kích hoạt — cầu chì chưa nối phải báo người.
		usage.SetOnMissingUsage(func() {
			const blind = "Vùng mù ngân sách: mô hình không trả về dữ liệu usage, thống kê chi phí bằng 0, giới hạn ngân sách sẽ không kích hoạt (nếu dùng model tùy chỉnh, hãy kiểm tra giá trong registry hoặc include_usage từ upstream)"
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: blind, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: "warn", Title: "ainovel: ngân sách", Body: blind})
		})
	}
	// Cổng tiến chương thống nhất: thực thi hold một lần và chặn chương mới không được phép trong chế độ review.
	h.gate = NewChapterAdvanceGate(store,
		func(reason string) {
			h.abortWithEvent(reason, "info")
			h.notifier.Send(notify.Notification{Kind: notify.KindAdvanceGate, Level: "info", Title: "ainovel: đang chờ nghiệm thu", Body: reason})
		},
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindAdvanceGate, Level: level, Title: "ainovel: tiến chương", Body: summary})
		},
	)
	// Chặn nổi StopGuard: blocked là hành động tự chữa lỗi tần suất cao, chỉ đi vào luồng sự kiện trong màn hình (push sẽ spam);
	// escalated / hard_stop nghĩa là tác vụ con của vòng này bị hủy, sự kiện + notify phải đi thành cặp (kiến trúc §2.3).
	onGuardBlock = func(agent string, reason string, n int32) {
		switch reason {
		case "escalated":
			body := fmt.Sprintf("%s liên tiếp %d lần lặp vô ích mà không sinh ra sản phẩm bắt buộc, tác vụ vòng này kết thúc, trả lại Engine xử lý", agent, n)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard nâng cấp: " + body, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: "ainovel: StopGuard", Body: body})
		case "hard_stop":
			body := fmt.Sprintf("%s bị provider từ chối (safety/content_filter), tác vụ vòng này dừng ngay", agent)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard nâng cấp: " + body, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: "ainovel: StopGuard", Body: body})
		default: // blocked
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent,
				Summary: fmt.Sprintf("StopGuard: %s chưa hoàn tất sản phẩm bắt buộc mà đã thử kết thúc, đã chặn và thúc tiếp (lần liên tiếp thứ %d)", agent, n), Level: "info"})
		}
	}
	// Engine: bộ máy thực thi quyết định(docs/engine-rfc.md). arbiter dùng model Default (giới hạn chuyển tiếp,
	// xem engine-arbiter.md §4.2).
	h.engine = &engine{
		store:           store,
		workers:         workers,
		arbiterModel:    newUsageTrackedModel(models.Default, "arbiter", usage.Record),
		failurePrompt:   bundle.Prompts.ArbiterFailure,
		planStartPrompt: bundle.Prompts.ArbiterPlanStart,
		style:           cfg.Style,
		// Hỏi lại đồng bộ: chặn vòng lặp Engine một nhịp phán quyết (vài giây), đổi lại "can thiệp có hiệu lực trước sáng tác tiếp theo".
		reconsult: h.handleIntervention,
		observer:  h.observer,
		budget:    h.budget,
		gate:      h.gate,
		refresh:   h.refreshWriterRestore,
		emitEvent: h.emitEvent,
		notify: func(kind, level, title, body string) {
			h.notifier.Send(notify.Notification{Kind: kind, Level: level, Title: title, Body: body})
		},
		onPause: func(summary string) { h.abortWithEvent(summary, "warn") },
		onDone:  h.runEnded,
	}

	keepBookLease = true
	return h, nil
}

// ── Vòng đời ──

// PrepareUserRules tạo snapshot quy tắc người dùng của cuốn sách này ở chế độ tạo mới (xác định ở phía khởi động, không đi vào Run sáng tác chính).
//
// Đầu vào là yêu cầu sáng tác **nguyên gốc** của người dùng (chưa được bọc bởi BuildStartPrompt) — điều cần chuẩn hóa là chính quy tắc người dùng,
// chứ không phải lớp khung khởi động. Cửa vào phải được gọi đúng một lần trước StartPrepared (cả hai nhánh tạo mới quick/cocreate đều đi qua đây).
//
// Chuẩn hóa thất bại thì chỉ hạ cấp chứ không báo lỗi (đường tăng cường); chỉ khi snapshot không thể ghi ra đĩa mới trả error để dừng việc mở sách —
// các lần chạy sau sẽ không còn nguồn sự thật ổn định (xem thiết kế §thất bại và hạ cấp).
func (h *Host) PrepareUserRules(rawPrompt string) error {
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := svc.Build(context.Background(), rawPrompt)
	if err != nil {
		return fmt.Errorf("không thể ghi snapshot quy tắc người dùng, không thể tiếp tục: %w", err)
	}
	logUserRulesSnapshot(snap)
	return nil
}

// ensureUserRules bảo đảm snapshot tồn tại trong đường hồi phục; nếu thiếu thì tạo từ
// system_defaults + file rules.
func (h *Host) ensureUserRules() {
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := svc.GetOrBuild(context.Background())
	if err != nil {
		slog.Warn("không đọc/tạo được snapshot quy tắc người dùng, lúc chạy sẽ rơi về mặc định tích hợp sẵn", "module", "rules", "err", err)
		return
	}
	logUserRulesSnapshot(snap)
}

// logUserRulesSnapshot: hiển thị lại lúc khởi động để người dùng thấy hệ thống hiểu các quy tắc thành gì (tái sử dụng nhật ký, không thêm cơ chế mới).
func logUserRulesSnapshot(snap *rules.Snapshot) {
	if snap == nil {
		return
	}
	slog.Info("snapshot quy tắc người dùng",
		"module", "rules",
		"status", string(snap.Status),
		"nguồn", snap.Sources,
		"cụm từ cấm", len(snap.Structured.ForbiddenPhrases),
		"từ mệt", len(snap.Structured.FatigueWords),
	)
	if snap.Status == rules.StatusDegraded {
		slog.Warn("một phần quy tắc không thể phân tích, đã chạy theo raw preferences (có thể tạo lại snapshot)",
			"module", "rules", "uncertain", snap.Uncertain)
	}
}

// StartPrepared bắt đầu sáng tác bằng yêu cầu **nguyên gốc** của người dùng: phán quyết plan_start chọn người lập kế hoạch và mở rộng
// yêu cầu, rồi cố định kết quả phán quyết thành
// sự thật(PlanStartRecord) trước khi khởi động Engine — phục hồi luôn phụ thuộc vào sự thật đã ghi đĩa, không làm lại phán quyết đã có.
// Sự thật đầu vào(StartPrompt) được ghi đĩa trước khi phán quyết: khi phán quyết thất bại, nó vẫn là căn cứ để Engine bù phán quyết,
// lần khởi động thất bại không còn là ngõ cụt nữa.
func (h *Host) StartPrepared(rawRequirement string) error {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return fmt.Errorf("đã chạy rồi")
	}
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("đang đồng sáng tạo theo giai đoạn, hãy kết thúc trước")
	}
	h.mu.Unlock()

	rawRequirement = strings.TrimSpace(rawRequirement)
	if rawRequirement == "" {
		return fmt.Errorf("prompt là bắt buộc")
	}
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	if err := upgradeProject(h.store); err != nil {
		return err
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	if err := h.store.Checkpoints.Reset(); err != nil {
		return fmt.Errorf("đặt lại checkpoints: %w", err)
	}
	if err := h.store.Progress.Init(0); err != nil {
		return fmt.Errorf("khởi tạo progress: %w", err)
	}
	// Sự thật đầu vào được ghi đĩa trước phán quyết: nếu phán quyết thất bại (lỗi mô hình, v.v.) thì StartPrompt vẫn còn,
	// để khi phục hồi/tiếp tục Engine có thể bù phán quyết dựa trên đó(planStartFallback), nên thất bại khởi động không còn là ngõ cụt.
	if err := h.store.RunMeta.SetStartPrompt(rawRequirement); err != nil {
		return fmt.Errorf("ghi nhu cầu sáng tác: %w", err)
	}

	// Phán quyết khởi động: nếu thất bại thì trả lỗi rõ ràng và dừng (trong giai đoạn khởi động người dùng đang ở đó, báo lỗi tốt hơn đoán mò).
	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "phán quyết khởi động", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterPlanStart, rawRequirement, h.cfg.Style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: rawRequirement,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	var recErr error
	if rec, recErr = h.store.Decisions.Append(rec); recErr != nil {
		slog.Warn("ghi audit phán quyết khởi động thất bại", "module", "host", "err", recErr)
	}
	if derr != nil {
		return fmt.Errorf("phán quyết khởi động thất bại: %w", derr)
	}
	if err := h.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: rawRequirement, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		return fmt.Errorf("ghi phán quyết khởi động: %w", err)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("bắt đầu sáng tác (người lập kế hoạch: %s - %s)", decision.Planner, decision.Reason), Level: "info"})
	if !h.startEngine(&flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}) {
		return fmt.Errorf("Engine đã chạy hoặc đang dừng, không thể khởi động sách mới")
	}
	return nil
}

// refuseNewBookOverExisting từ chối mở sách mới trong thư mục đã có sách có chương: StartPrepared sẽ đặt lại
// checkpoints và progress, chỉ cần bấm nhầm là sẽ âm thầm xóa toàn bộ chuỗi tiến độ của cuốn sách (trường hợp điển hình là
// sau khi import xong rồi dừng ở trang chào mừng mà nhấn Enter nhầm). Chỉ xét số chương đã hoàn tất — phần còn lại ở giai đoạn lập kế hoạch/lỗi khởi động
// chưa tạo ra chương nào, nên cho qua để giữ lại đường tự phục hồi của việc tái sử dụng cùng phiên giữa Ctrl+S và bù phán quyết trong đồng sáng tạo.
func (h *Host) refuseNewBookOverExisting() error {
	progress, err := h.store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil
	}
	book, err := h.store.Book.Load()
	if err != nil {
		return err
	}
	if book == nil {
		return fmt.Errorf("thư mục đầu ra đã có chương, nhưng thông tin tác phẩm không tồn tại")
	}
	name := book.Title
	return fmt.Errorf("thư mục đầu ra đã có tiến độ sáng tác %d chương của \"%s\", tạo mới sẽ đặt lại tiến độ và các checkpoint: nếu muốn viết tiếp hãy đi qua cửa vào phục hồi (khởi động lại ứng dụng sẽ tự phục hồi), nếu muốn mở sách mới hãy đổi thư mục đầu ra",
		len(progress.CompletedChapters), name)
}

// startEngine là cửa vào thống nhất để khởi động engine (Start/Resume/Continue/tái khởi động do can thiệp đều dùng chung).
// lifecycle phải được đặt thành running trước khi goroutine được khởi chạy: engine có thể kết thúc ngay lập tức (xong truyện/không có tuyến),
// runEnded sẽ kéo lifecycle về trạng thái cuối; nếu đảo thứ tự, runEnded chạy trước, rồi ở đây mới ghi running,
// UI sẽ mãi hiển thị "đang chạy" trong khi engine thực ra đã dừng.
func (h *Host) startEngine(initial *flow.Instruction) bool {
	// Cổng trước khi khởi động lại: khi còn workspace import chưa hoàn tất, cấm Engine bình thường tiêu thụ trạng thái nửa phát hành (RFC §12.5).
	active, done, importErr := imp.ResumeStatus(h.store)
	if importErr != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "đọc trạng thái import thất bại, đã chặn việc sáng tác bình thường ghi đè lên hiện vật hiện có: " + importErr.Error()})
		return false
	}
	if active && !done {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "còn một lần import tiểu thuyết bên ngoài chưa hoàn tất, hãy chạy /import để khôi phục xong rồi hãy tiếp tục sáng tác"})
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}
	// Khi có tác vụ nền độc quyền (import/phỏng theo) đang chạy, engine không được tranh chạy trước, tránh tranh chấp ghi chép với nó.
	// Đây là backstop thống nhất cho mọi đường khởi động engine (Resume/Continue/tái khởi động/tiếp nối tự động/next) — chốt chặn ở cửa vào là lớp đầu tiên, ở đây là lớp cuối.
	if h.exclusive != "" {
		return false
	}
	// lifecycle có thể đã là paused, nhưng goroutine cũ của Engine vẫn còn đang chạy defer thoát.
	// Phải kiểm tra cả trạng thái thật của Engine; nếu không sẽ kéo lifecycle về running,
	// trong khi start thực tế là no-op, rồi runEnded cũ lại kéo nó về idle.
	if h.engine.isRunning() {
		return false
	}
	h.observer.setAborting(false)
	previous := h.lifecycle
	h.lifecycle = lifecycleRunning
	if !h.engine.start(initial) {
		h.lifecycle = previous
		return false
	}
	return true
}

// Reopen buộc mở lại cuốn sách đã hoàn tất thành trạng thái sáng tác. Hoàn tất và mở lại đều là tái phán quyết:
// hoàn tất có thể do kiến trúc sư phán quyết, còn mở lại chỉ do người dùng chủ động khởi phát (/reopen), không qua phán quyết mô hình.
// Khi direction không rỗng thì ghi nhận là can thiệp chờ xử lý, lúc phục hồi sẽ đi qua Arbiter để phán quyết và chèn vào trước
// (cùng kênh với can thiệp trong thời gian dừng máy), rồi mới cho engine chạy tiếp (phân tuyến cuối cuốn sẽ phân phát cuốn tiếp theo).
func (h *Host) Reopen(direction string) error {
	h.mu.Lock()
	switch {
	case h.lifecycle == lifecycleRunning:
		h.mu.Unlock()
		return fmt.Errorf("động cơ sáng tác đang chạy, không cần mở lại")
	case h.cocreating:
		h.mu.Unlock()
		return fmt.Errorf("đang đồng sáng tạo theo giai đoạn, hãy kết thúc trước")
	case h.exclusive != "":
		ex := h.exclusive
		h.mu.Unlock()
		return fmt.Errorf("%s đang diễn ra, hãy hoàn tất rồi mới mở lại", ex)
	}
	h.mu.Unlock()
	if err := h.requireCleanChapters(); err != nil {
		return err
	}

	if err := h.store.Progress.ReopenContinue(); err != nil {
		return err
	}
	reopenEvent := Event{Time: time.Now(), Category: "SYSTEM", Summary: "đã mở lại cuốn sách này về trạng thái sáng tác (người dùng hủy phán quyết hoàn tất)", Level: "info"}
	if d := strings.TrimSpace(direction); d != "" {
		reopenEvent.Detail = reopenEvent.Summary + "\nđịnh hướng viết tiếp: " + d
	}
	h.emitEvent(reopenEvent)
	if d := strings.TrimSpace(direction); d != "" {
		if err := h.store.RunMeta.SetPendingSteer(d); err != nil {
			return fmt.Errorf("đã mở lại, nhưng không ghi được định hướng viết tiếp: %v, hãy nhập lại định hướng trực tiếp trong ô nhập", err)
		}
	}
	return nil
}

// Resume là chế độ phục hồi: tạo resume prompt từ checkpoint + progress rồi khởi động.
func (h *Host) Resume() (string, error) {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return "", fmt.Errorf("đã chạy rồi")
	}
	if h.cocreating {
		h.mu.Unlock()
		return "", fmt.Errorf("đang đồng sáng tạo theo giai đoạn, hãy kết thúc trước")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		return "", fmt.Errorf("%s đang diễn ra, hãy hoàn tất rồi mới tiếp tục sáng tác", ex)
	}
	h.mu.Unlock()
	if err := upgradeProject(h.store); err != nil {
		return "", err
	}

	label, err := resumeLabel(h.store)
	if err != nil {
		return "", err
	}
	if label == "" {
		return "", nil // Chế độ tạo mới, không có phục hồi
	}
	if err := h.requireCleanChapters(); err != nil {
		return label, err
	}
	if err := h.budget.Refuse(); err != nil {
		return "", err
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "phục hồi sáng tác: " + label, Level: "info"})
	for _, w := range h.store.CheckConsistency() {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "cảnh báo nhất quán: " + w, Level: "warn"})
	}
	// Bảo đảm snapshot quy tắc người dùng tồn tại; nếu đã có thì chỉ đọc lại với chi phí thấp.
	h.ensureUserRules()
	h.refreshWriterRestore()
	// Các can thiệp đang chờ (để lại trong thời gian dừng máy / còn sót sau lúc phán quyết bị treo) phải được xử lý trước khi engine tiếp tục phán quyết —
	// nếu không engine có thể kịp viết ra chương trái ngược với can thiệp trước khi phán quyết xong. Thực thi đồng bộ (đợi vài giây là chấp nhận được,
	// UI đã hiển thị "phục hồi sáng tác"); sau khi doIntervention thành công sẽ tự xóa PendingSteer và, nếu restart=true, kéo engine lên.
	// Không có can thiệp chờ → chạy tiếp trực tiếp.
	meta, err := h.store.RunMeta.Load()
	if err != nil {
		return label, fmt.Errorf("đọc can thiệp đang chờ: %w", err)
	}
	if meta != nil && meta.PendingSteer != "" {
		if err := h.doIntervention(meta.PendingSteer, true); err != nil {
			return label, err
		}
	} else {
		// Chỉ phục hồi sự thật, không phục hồi phiên(RFC §6): Engine sẽ tính lại tuyến từ store rồi tiếp tục.
		if !h.startEngine(nil) {
			return label, fmt.Errorf("Engine đang hoàn tất lần dừng trước, hãy thử lại sau để phục hồi")
		}
	}
	// lifecycle do startEngine / runEnded quản lý, ở đây không ghi đè nữa —
	// nếu engine kết thúc ngay (xong truyện, v.v.) thì ghi đè sẽ kéo trạng thái cuối về running.
	return label, nil
}

// handleIntervention là callback hỏi lại không trả về giá trị để thích ứng với Engine; lỗi đã được doIntervention phát sự kiện.
func (h *Host) handleIntervention(text string) {
	_ = h.doIntervention(text, false)
}

// doIntervention là đường phán quyết thống nhất cho can thiệp của người dùng: Collect → Decide → thực thi.
// FIFO tuần tự (mỗi thời điểm tối đa một lần hỏi ý kiến đang diễn ra); answer/rules thực thi ngay, các hành động điều khiển
// (hold/reopen/dispatch) khi engine đang chạy sẽ xếp hàng chờ tới ranh giới, khi engine dừng thì thực thi ngay.
// restart=true (ngữ nghĩa Continue) nghĩa là sau xử lý can thiệp phải bảo đảm engine chạy lại.
func (h *Host) doIntervention(text string, restart bool) error {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	// Bảo vệ khi sập: trước hết ghi bền (PendingSteer), sau khi áp dụng thành công hoặc đã hiển thị lỗi trước mặt thì xóa nguyên tử
	// (ClearHandledSteer đồng thời đặt lại FlowSteering). Sập trong lúc phán quyết → lần Resume sau sẽ phát lại.
	if err := h.store.RunMeta.SetPendingSteer(text); err != nil {
		wrapped := fmt.Errorf("không thể ghi bền can thiệp, đã dừng phán quyết: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}
	clearPending := func() error {
		if err := h.store.ClearHandledSteer(); err != nil {
			return fmt.Errorf("xóa can thiệp đã xử lý thất bại: %w", err)
		}
		return nil
	}

	facts, err := arbiter.CollectInterventionFacts(h.store)
	if err != nil {
		wrapped := fmt.Errorf("thu thập sự thật can thiệp thất bại, chưa gọi Arbiter: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}
	facts.Running = h.engine.isRunning()

	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "phán quyết can thiệp của người dùng", func() (arbiter.InterventionDecision, error) {
		return arbiter.DecideIntervention(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterIntervention, facts, text)
	})

	rec := storepkg.DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: text,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		rec.CheckpointSeq = cp.Seq
	}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := h.store.Decisions.Append(rec); err != nil {
		wrapped := fmt.Errorf("ghi audit phán quyết can thiệp thất bại, từ chối thực thi hành động: %w", err)
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: "arbiter",
			Summary: wrapped.Error(), Detail: wrapped.Error(), Level: "error"})
		return wrapped
	}

	if derr != nil {
		// Thà không động còn hơn động sai: không tạo ra bất kỳ ghi nào. Lỗi gọi và
		// lỗi kiểm tra đầu ra dùng chung một kênh error, phải trả lại nguyên trạng, không được ngụy trang thành "không hiểu".
		// Đã thông báo trước mặt → xóa pending (nếu không lần Resume sau sẽ tự phát lại cùng một can thiệp thất bại).
		h.emitEvent(newInterventionFailureEvent(derr))
		if err := clearPending(); err != nil {
			return fmt.Errorf("%v; %w", derr, err)
		}
		return derr
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "phán quyết: " + decision.Reason, Level: "info"})
	if decision.Answer != "" {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: decision.Answer, Level: "info"})
	}
	// Bất kỳ lỗi bền bỉ nào của một hành động → giữ PendingSteer (lúc phục hồi sẽ phát lại cả chuỗi và phán quyết lại;
	// hold/reopen là idempotent, dispatch sẽ hỏi lại qua sự thật mới, nên phát lại an toàn).
	var actionErr error
	if decision.Rules != "" {
		if snap, _, err := h.userRules.AddRuntimeRule(h.runCtx, decision.Rules); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "ghi bền quy tắc viết thất bại: " + err.Error(), Level: "error"})
			actionErr = err
		} else if snap != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "quy tắc viết đã được cập nhật và lưu bền", Level: "info"})
		}
	}

	if decision.Hold != nil || decision.Reopen != nil || decision.Dispatch != nil {
		op := controlOp{hold: decision.Hold, reopen: decision.Reopen, dispatch: decision.Dispatch, text: text, facts: facts}
		if !h.engine.enqueue(op) {
			// Engine chưa chạy: thực thi ngay; nếu lỗi ghi bền → giữ PendingSteer, phục hồi sẽ phát lại toàn bộ can thiệp.
			if err := h.engine.applyControlOp(context.Background(), op); err != nil {
				h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "thực thi hành động can thiệp thất bại, đã giữ lại; khi phục hồi/tiếp tục sẽ tự thử lại"})
				return err
			}
			// reopen/dispatch thể hiện ý định tiếp tục sáng tác, nên kéo engine lên.
			if decision.Reopen != nil || decision.Dispatch != nil {
				restart = true
			}
		}
	}
	if actionErr != nil {
		// Giữ PendingSteer: khi phục hồi/tiếp tục sẽ phát lại cả chuỗi và phán quyết lại.
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "một phần hành động can thiệp không thành công, can thiệp đã được giữ lại; sẽ tự thử lại khi phục hồi/tiếp tục"})
		return actionErr
	}
	// Hành động đã được áp dụng/đưa vào hàng đợi thành công, xóa bảo vệ sập (nếu sau đó engine phía dưới lỗi hoặc thoát do cạnh tranh,
	// engine sẽ tự ghi lại PendingSteer làm phương án dự phòng).
	if err := clearPending(); err != nil {
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error", Summary: err.Error()})
		return err
	}

	if restart && !h.engine.isRunning() {
		if err := h.budget.Refuse(); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: err.Error(), Level: "warn"})
			return err
		}
		h.refreshWriterRestore()
		if !h.startEngine(nil) {
			// Lúc này hành động can thiệp đã có hiệu lực và PendingSteer đã được xóa, chỉ là engine chưa thể khởi động lại ngay — không thể nói dối là "đã lưu".
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "can thiệp đã có hiệu lực, nhưng Engine chưa thể tiếp tục ngay; hãy chờ một chút rồi dùng ô nhập để tiếp tục hoặc khởi động lại ứng dụng để phục hồi"})
			return fmt.Errorf("can thiệp đã có hiệu lực, nhưng Engine chưa thể tiếp tục ngay")
		}
	}
	return nil
}

func newInterventionFailureEvent(err error) Event {
	detail := err.Error()
	return Event{
		Time:     time.Now(),
		Category: "ERROR",
		Agent:    "arbiter",
		Summary:  "phán quyết can thiệp thất bại: " + detail + " (không thay đổi gì)",
		Detail:   detail,
		Kind:     errorKind(err, detail),
		Level:    "error",
	}
}

// arbiterModel trả về model phán quyết có theo dõi usage (token/chi phí đi vào ngân sách và hệ thống usage).
func (h *Host) arbiterModel() agentcore.ChatModel {
	return newUsageTrackedModel(h.models.Default, "arbiter", h.usage.Record)
}

// Continue được gọi khi người dùng nhập trong ô nhập sau khi dừng: phán quyết can thiệp + bảo đảm engine chạy lại.
func (h *Host) Continue(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text là bắt buộc")
	}
	h.mu.Lock()
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("đang đồng sáng tạo theo giai đoạn, hãy kết thúc trước")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		// Trong lúc có tác vụ độc quyền phải chặn trước khi phán quyết: nếu không Arbiter đã sửa PendingSteer/quy tắc/trạng thái điều khiển,
		// rồi engine mới bị cổng chặn lại.
		return fmt.Errorf("%s đang diễn ra, hãy hoàn tất rồi mới tiếp tục sáng tác", ex)
	}
	h.mu.Unlock()
	if err := h.requireCleanChapters(); err != nil {
		return err
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}

	err, launched := h.runAsync(func() error {
		h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[tiếp tục] " + text, Level: "info"})
		return h.doIntervention(text, true)
	})
	if !launched {
		return fmt.Errorf("Host đang đóng, không thể tiếp tục sáng tác")
	}
	return err
}

// SetAdvanceMode chuyển đổi xác định chế độ tiến chương. Nó chỉ ghi vào ý định chạy của người dùng,
// không gọi Arbiter, cũng không tự động khởi động Engine đang tạm dừng.
func (h *Host) SetAdvanceMode(mode domain.ChapterAdvanceMode) error {
	h.interMu.Lock()
	defer h.interMu.Unlock()
	if err := h.store.RunMeta.SetAdvanceMode(mode); err != nil {
		return err
	}
	label := "tự động tiến"
	if mode == domain.ChapterAdvanceReview {
		label = "nghiệm thu từng chương"
	}
	summary := "chế độ tiến chương đã chuyển sang " + label
	h.mu.Lock()
	state := h.lifecycle
	h.mu.Unlock()
	if mode == domain.ChapterAdvanceAuto && state != lifecycleRunning && state != lifecycleCompleted {
		summary += "; hiện vẫn đang tạm dừng, nhập lệnh tiếp tục rồi sẽ chạy lại"
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "info"})
	return nil
}

// AdvanceOneChapter cho phép một chương chính xác trong chế độ nghiệm thu từng chương và khởi động Engine.
func (h *Host) AdvanceOneChapter() error {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	h.mu.Lock()
	running, cocreating, ex := h.lifecycle == lifecycleRunning, h.cocreating, h.exclusive
	h.mu.Unlock()
	if running || h.engine.isRunning() {
		return fmt.Errorf("sáng tác vẫn đang chạy hoặc đang hoàn tất tạm dừng, hãy chờ rồi mới chạy /next")
	}
	if cocreating {
		return fmt.Errorf("đang đồng sáng tạo theo giai đoạn, hãy kết thúc trước")
	}
	if ex != "" {
		return fmt.Errorf("%s đang diễn ra, hãy hoàn tất rồi mới chạy /next", ex)
	}
	if err := h.requireCleanChapters(); err != nil {
		return err
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("RunMeta chưa được khởi tạo")
	}
	if meta.AdvanceMode != domain.ChapterAdvanceReview {
		return fmt.Errorf("/next chỉ dùng cho chế độ nghiệm thu từng chương, hãy chạy /review on trước")
	}
	if meta.AdvanceHold != nil {
		return fmt.Errorf("vẫn còn ý định tạm dừng một lần đang chờ xử lý (%s), hãy phục hồi hoặc hoàn tất can thiệp hiện tại trước", meta.AdvanceHold.Reason)
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	progress, err := h.store.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil || progress.Phase != domain.PhaseWriting {
		phase := "<nil>"
		if progress != nil {
			phase = string(progress.Phase)
		}
		return fmt.Errorf("không thể cho phép chương mới ở giai đoạn hiện tại (phase=%s)", phase)
	}
	target := progress.NextChapter()
	if target <= 0 {
		return fmt.Errorf("không thể suy ra chương tiếp theo từ tiến độ hiện tại")
	}
	if err := h.store.RunMeta.GrantAdvancePermit(target); err != nil {
		return err
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("đã cho phép chương %d; sau khi chương này nộp xong sẽ hoàn tất các việc đánh giá và bảo trì cấu trúc arc/volume cần thiết, rồi lại chờ cấp phép", target), Level: "info"})
	h.refreshWriterRestore()
	if !h.startEngine(nil) {
		// Giấy phép được lưu bền theo số chương và idempotent với cùng mục tiêu, nên gọi lại sau sẽ không cấp hai lần.
		return fmt.Errorf("giấy phép chương đã được lưu, nhưng Engine vẫn đang hoàn tất lần dừng trước; hãy thử lại /next sau")
	}
	return nil
}

// Steer gửi can thiệp của người dùng (có thể dùng bất kỳ lúc nào khi đang chạy; khi dừng thì sau phán quyết sẽ quyết định có kéo engine lên hay không).
// TUI chờ kết quả qua tea.Cmd, nên có thể nhận lỗi phán quyết/lưu bền thật mà không làm treo giao diện.
func (h *Host) Steer(text string) error {
	err, launched := h.runAsync(func() error {
		h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[can thiệp của người dùng] " + text, Level: "info"})
		return h.doIntervention(text, false)
	})
	if !launched {
		return fmt.Errorf("Host đang đóng, không thể gửi can thiệp")
	}
	return err
}

// Abort tạm dừng vòng lặp Engine hiện tại.
func (h *Host) Abort() bool {
	return h.abortWithEvent("người dùng tạm dừng thủ công sáng tác hiện tại", "warn")
}

// abortWithEvent thực thi tạm dừng bằng sự kiện với lý do chỉ định. Dừng vì ngân sách và tạm dừng thủ công dùng chung cùng một cơ chế dừng,
// chỉ khác phần văn bản sự kiện (dừng do ngân sách = lệnh Abort do người dùng ký trước, nghĩa tương đương tạm dừng thủ công).
func (h *Host) abortWithEvent(summary, level string) bool {
	h.mu.Lock()
	running := h.lifecycle == lifecycleRunning
	if running {
		h.lifecycle = lifecyclePaused
	}
	cancelExclusive := h.exclusiveCancel
	h.mu.Unlock()
	if running {
		// Phải đặt cờ trước engine.abort: việc hủy sẽ ngay lập tức kích hoạt các sự kiện lỗi khởi tạo stream / worker,
		// observer dựa vào cờ này để nhận ra đó là nhiễu sinh ra từ abort và chặn lại.
		h.observer.setAborting(true)
		h.engine.abort()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	// Engine chưa chạy nhưng tác vụ độc quyền (import, v.v.) đang chạy: nó cũng đang tiêu tiền, nên dừng cứng vì ngân sách / tạm dừng thủ công
	// phải có thể dừng nó — nếu không chính sách ngân sách sẽ vô hiệu với import (docs/import-pipeline.md §13.1).
	if cancelExclusive != nil {
		cancelExclusive()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	return false
}

// Close kết thúc engine và đóng kênh sự kiện.
//
// Ngữ nghĩa lưu bền Usage: trước hết hủy autoSaveLoop (nó sẽ tự flush lần cuối trạng thái dirty),
// rồi thêm một lần SaveNow đồng bộ để khép lại. Sau khi kết thúc, vài trăm token cuối của các lời gọi LLM đang diễn ra
// có thể bị mất, và sẽ được replay tự động từ jsonl của session ở lần khởi động kế tiếp.
func (h *Host) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closing = true
		cancelExclusive := h.exclusiveCancel
		h.mu.Unlock()

		h.observer.setAborting(true)
		if h.runCancel != nil {
			h.runCancel() // ngắt các lời gọi phán quyết phía Host đang diễn ra và luồng chuyển tiếp của supervisor
		}
		if cancelExclusive != nil {
			cancelExclusive()
		}
		h.engine.abort()
		h.engine.wait()
		h.asyncWG.Wait()

		if h.usageCancel != nil {
			h.usageCancel()
			h.usageCancel = nil
		}
		h.usage.WaitAutoSave()
		if err := h.usage.SaveNow(); err != nil {
			slog.Warn("ghi bền usage trước khi thoát thất bại", "module", "usage", "err", err)
		}
		h.closeOutputChannels()
		if err := h.bookLease.Close(); err != nil {
			slog.Error("không thể giải phóng chiếm dụng thư mục tiểu thuyết", "module", "host", "dir", h.cfg.OutputDir, "err", err)
		}
		if h.logCleanup != nil {
			h.logCleanup()
			h.logCleanup = nil
		}
	})
}

// FileLogError trả về lỗi khởi tạo nhật ký file ở giai đoạn dựng; vòng đời Host sẽ không thay đổi giá trị này.
func (h *Host) FileLogError() error {
	return h.fileLogErr
}

// runEnded được engine.onDone gọi khi vòng lặp engine kết thúc (bất kỳ lý do nào): xác định trạng thái cuối theo facts trong store.
//   - Phase=Complete  → đánh dấu completed, phát sự kiện "sáng tác hoàn tất"
//   - Còn lại          → đánh dấu idle/paused, phát sự kiện "sáng tác dừng"
func (h *Host) runEnded() {
	h.observer.finalize()

	h.mu.Lock()
	progress, err := h.store.Progress.Load()
	if err != nil {
		if h.lifecycle == lifecycleRunning {
			h.lifecycle = lifecycleIdle
		}
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "đọc tiến độ khi engine kết thúc thất bại: " + err.Error()})
		select {
		case h.done <- struct{}{}:
		default:
		}
		return
	}
	book, err := h.store.Book.Load()
	if err != nil {
		h.lifecycle = lifecycleIdle
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
			Summary: "đọc thông tin tác phẩm khi engine kết thúc thất bại: " + err.Error()})
		select {
		case h.done <- struct{}{}:
		default:
		}
		return
	}
	if progress != nil && progress.Phase == domain.PhaseComplete {
		if book == nil {
			h.lifecycle = lifecycleIdle
			h.mu.Unlock()
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
				Summary: "thông tin tác phẩm không tồn tại khi engine kết thúc"})
			select {
			case h.done <- struct{}{}:
			default:
			}
			return
		}
		h.lifecycle = lifecycleCompleted
		// Khép lại khi hoàn tất: sinh kết quả xác định (store đã có đủ mọi sự thật, không tốn thêm lời gọi LLM; phần cuối RFC).
		summary := completionSummary(*progress, *book)
		h.mu.Unlock()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "success"})
		h.notifier.Send(notify.Notification{
			Kind: notify.KindRunEnd, Level: "info", Title: "ainovel: sáng tác hoàn tất",
			Body: h.runEndBody("", summary),
		})
	} else {
		wasRunning := h.lifecycle == lifecycleRunning
		if wasRunning {
			h.lifecycle = lifecycleIdle
		}
		completed := 0
		title := ""
		if progress != nil {
			completed = len(progress.CompletedChapters)
		}
		if book != nil {
			title = book.Title
		}
		h.mu.Unlock()
		if wasRunning {
			summary := fmt.Sprintf("Engine dừng (đã hoàn thành %d chương)", completed)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "warn"})
			h.notifier.Send(notify.Notification{
				Kind: notify.KindRunEnd, Level: "warn", Title: "ainovel: sáng tác dừng",
				Body: h.runEndBody(title, summary),
			})
		}
	}

	select {
	case h.done <- struct{}{}:
	default:
	}
}

// runEndBody ghép phần thân thông báo run_end: tên sách + tóm tắt tiến độ + chi phí tích lũy.
func (h *Host) runEndBody(title, summary string) string {
	if name := strings.TrimSpace(title); name != "" {
		summary = "cuốn \"" + name + "\" " + summary
	}
	cost, _, _, _, _ := h.usage.Totals()
	if cost > 0 {
		summary += fmt.Sprintf(" · chi phí $%.2f", cost)
	}
	return summary
}

// ── Kênh ──

// StreamClearSentinel được gửi đơn lẻ qua streamCh để báo hiệu "xóa round stream hiện tại".
// Không còn dùng clearCh riêng — hai kênh không có thứ tự làm cho header ✻ thường rơi vào cuối round trước.
const StreamClearSentinel = "\x00\x00CLEAR\x00\x00"

func (h *Host) Events() <-chan Event  { return h.events }
func (h *Host) Stream() <-chan string { return h.streamCh }
func (h *Host) Done() <-chan struct{} { return h.done }
func (h *Host) Dir() string           { return h.store.Dir() }

// ── Phát sự kiện ──

func (h *Host) emitEvent(ev Event) {
	h.outputMu.RLock()
	defer h.outputMu.RUnlock()
	if h.outputClosed {
		return
	}
	// Khóa đọc bảo đảm các sự kiện trước khi đóng được ghi xong đầy đủ; sự kiện sau khi đóng bị từ chối ngay.
	LogEvent(ev)
	select {
	case h.events <- ev:
	default:
		select {
		case <-h.events:
		default:
		}
		select {
		case h.events <- ev:
		default:
		}
	}
}

func (h *Host) emitDelta(delta string) {
	h.outputMu.RLock()
	defer h.outputMu.RUnlock()
	if h.outputClosed {
		return
	}
	select {
	case h.streamCh <- delta:
	default:
		select {
		case <-h.streamCh:
		default:
		}
		select {
		case h.streamCh <- delta:
		default:
		}
	}
}

func (h *Host) closeOutputChannels() {
	h.outputMu.Lock()
	defer h.outputMu.Unlock()
	if h.outputClosed {
		return
	}
	h.outputClosed = true
	close(h.done)
	close(h.events)
	close(h.streamCh)
}

func (h *Host) emitClear() {
	// Đi qua streamCh bằng "sentinel", để bảo đảm gửi đến TUI theo đúng thứ tự cùng một kênh với emitDelta.
	h.emitDelta(StreamClearSentinel)
}

// ── Snapshot (gom trạng thái TUI) ──

func (h *Host) Snapshot() UISnapshot {
	h.mu.Lock()
	state := h.lifecycle
	provider, model, _ := h.models.CurrentSelection("default")
	modelWindow, _ := h.cfg.ResolveContextWindow(provider, model)
	thinkingLevel := h.cfg.ResolveReasoningEffort("default")
	style := h.cfg.Style
	h.mu.Unlock()

	// Phân giải động cửa sổ ngữ cảnh của mô hình hiện tại, sau khi /model hoặc /config đổi thì Snapshot kế tiếp sẽ tự phản ánh.
	cost, tokIn, tokOut, cacheRead, cacheWrite := h.usage.Totals()
	saved := h.usage.SavedUSD()
	overallCapable := h.usage.OverallCacheCapable()
	recentRead, recentInput, recentSamples := h.usage.OverallRecent()
	perAgent := h.usage.PerAgent()
	cacheStats := make([]AgentCacheStat, 0, len(perAgent))
	for _, a := range perAgent {
		cacheStats = append(cacheStats, AgentCacheStat{
			Role:            a.Role,
			Input:           a.Input,
			Output:          a.Output,
			CacheRead:       a.CacheRead,
			CacheWrite:      a.CacheWrite,
			Cost:            a.Cost,
			Saved:           a.Saved,
			CacheCapable:    a.CacheCapable,
			RecentCacheRead: a.RecentCacheRead,
			RecentInput:     a.RecentInput,
			RecentSamples:   a.RecentSamples,
		})
	}
	perModel := h.usage.PerModel()
	modelStats := make([]AgentCacheStat, 0, len(perModel))
	for _, a := range perModel {
		modelStats = append(modelStats, AgentCacheStat{
			Model:        a.Model,
			Input:        a.Input,
			Output:       a.Output,
			CacheRead:    a.CacheRead,
			CacheWrite:   a.CacheWrite,
			Cost:         a.Cost,
			Saved:        a.Saved,
			CacheCapable: a.CacheCapable,
		})
	}

	snap := UISnapshot{
		Provider:               provider,
		ModelName:              model,
		ModelContextWindow:     modelWindow,
		ThinkingLevel:          thinkingLevel,
		Style:                  style,
		RuntimeState:           string(state),
		IsRunning:              state == lifecycleRunning,
		TotalInputTokens:       tokIn,
		TotalOutputTokens:      tokOut,
		TotalCacheReadTokens:   cacheRead,
		TotalCacheWriteTokens:  cacheWrite,
		TotalCostUSD:           cost,
		TotalSavedUSD:          saved,
		BudgetLimitUSD:         h.budget.Limit(),
		OverallCacheCapable:    overallCapable,
		OverallRecentCacheRead: recentRead,
		OverallRecentInput:     recentInput,
		OverallRecentSamples:   recentSamples,
		TotalCacheBreaks:       h.usage.OverallCacheBreaks(),
		CachePerAgent:          cacheStats,
		CachePerModel:          modelStats,
		MissingAssistantUsage:  h.usage.MissingAssistantUsage(),
	}

	if book, _ := h.store.Book.Load(); book != nil {
		snap.BookTitle = book.Title
		snap.Synopsis = truncate(book.Synopsis, 200)
	}
	progress, _ := h.store.Progress.Load()
	if progress != nil {
		snap.Phase = string(progress.Phase)
		snap.Flow = string(progress.Flow)
		snap.CurrentChapter = progress.CurrentChapter
		snap.TotalChapters = progress.TotalChapters
		snap.CompletedCount = len(progress.CompletedChapters)
		snap.TotalWordCount = progress.TotalWordCount
		snap.InProgressChapter = progress.InProgressChapter
		snap.PendingRewrites = progress.PendingRewrites
		snap.RewriteReason = progress.RewriteReason
		snap.Layered = progress.Layered
		if progress.CurrentVolume > 0 {
			snap.CurrentVolumeArc = fmt.Sprintf("tập %d · arc %d", progress.CurrentVolume, progress.CurrentArc)
		}
	}
	if meta, _ := h.store.RunMeta.Load(); meta != nil {
		snap.PendingSteer = meta.PendingSteer
		snap.AdvanceMode = string(meta.AdvanceMode)
		snap.AdvancePermitChapter = meta.AdvancePermitChapter
		if meta.AdvanceHold != nil {
			snap.HasAdvanceHold = true
			snap.AdvanceHoldReason = meta.AdvanceHold.Reason
		}
	}

	snap.Agents = h.observer.agentSnapshots()
	snap.StatusLabel = deriveStatusLabel(snap)

	// Nhãn phục hồi
	if label, err := resumeLabel(h.store); err == nil && label != "" {
		snap.RecoveryLabel = label
	}

	h.fillDetails(&snap, progress)

	return snap
}

// fillDetails điền phần chi tiết: thiết lập, nhân vật, commit/review/tóm tắt gần nhất.
func (h *Host) fillDetails(snap *UISnapshot, progress *domain.Progress) {
	if premise, _ := h.store.Outline.LoadPremise(); premise != "" {
		snap.Premise = truncate(premise, 80)
	}
	if outline, _ := h.store.Outline.LoadOutline(); len(outline) > 0 {
		completed := make(map[int]struct{})
		if progress != nil {
			completed = make(map[int]struct{}, len(progress.CompletedChapters))
			for _, chapter := range progress.CompletedChapters {
				completed[chapter] = struct{}{}
			}
		}
		for _, e := range outline {
			title := e.Title
			if _, ok := completed[e.Chapter]; ok {
				committedTitle, err := h.store.Summaries.LoadSummaryTitle(e.Chapter)
				if err != nil {
					slog.Warn("lỗi projection tiêu đề chương", "module", "host.snapshot", "chapter", e.Chapter, "err", err)
				} else if strings.TrimSpace(committedTitle) != "" {
					title = committedTitle
				}
			}
			snap.Outline = append(snap.Outline, OutlineSnapshot{
				Chapter: e.Chapter, Title: title, CoreEvent: e.CoreEvent,
			})
		}
	}
	if progress != nil && progress.Layered {
		if compass, _ := h.store.Outline.LoadCompass(); compass != nil {
			snap.CompassDirection = compass.EndingDirection
			snap.CompassScale = compass.EstimatedScale
		}
		if volumes, _ := h.store.Outline.LoadLayeredOutline(); len(volumes) > 0 {
			for _, v := range volumes {
				if v.Index > progress.CurrentVolume {
					snap.NextVolumeTitle = v.Title
					break
				}
			}
		}
	}
	if chars, _ := h.store.Characters.Load(); len(chars) > 0 {
		for _, c := range chars {
			label := c.Name
			if c.Role != "" {
				label += " (" + c.Role + ")"
			}
			snap.Characters = append(snap.Characters, label)
		}
	}
	if ledger, _ := h.store.Cast.Load(); len(ledger) > 0 {
		snap.SupportingCount = len(ledger)
		recent, _ := h.store.Cast.RecentActive(5)
		for _, e := range recent {
			label := e.Name
			if e.BriefRole != "" {
				label += " (" + e.BriefRole + ")"
			}
			snap.RecentSupporting = append(snap.RecentSupporting, label)
		}
	}
	if progress != nil && len(progress.CompletedChapters) > 0 {
		lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
		wc := progress.ChapterWordCounts[lastCh]
		snap.LastCommitSummary = fmt.Sprintf("chương %d %d từ", lastCh, wc)
	}
	currentCh := 1
	if progress != nil && len(progress.CompletedChapters) > 0 {
		currentCh = progress.CompletedChapters[len(progress.CompletedChapters)-1]
	}
	if review, err := h.store.World.LoadLastReview(currentCh); err == nil && review != nil {
		snap.LastReviewSummary = fmt.Sprintf("verdict=%s %d vấn đề", review.Verdict, len(review.Issues))
		if len(review.AffectedChapters) > 0 {
			snap.LastReviewSummary += fmt.Sprintf(" ảnh hưởng %v", review.AffectedChapters)
		}
	}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		snap.LastCheckpointName = fmt.Sprintf("%s.%s", cp.Scope, cp.Step)
	}
	if progress != nil {
		for i := len(progress.CompletedChapters) - 1; i >= 0 && len(snap.RecentSummaries) < 2; i-- {
			ch := progress.CompletedChapters[i]
			if summary, err := h.store.Summaries.LoadSummary(ch); err == nil && summary != nil {
				snap.RecentSummaries = append(snap.RecentSummaries,
					fmt.Sprintf("chương %d: %s", ch, truncate(summary.Summary, 50)))
			}
		}
	}
}

func deriveStatusLabel(s UISnapshot) string {
	switch {
	case s.Phase == string(domain.PhaseComplete):
		return "COMPLETE"
	case s.Flow == string(domain.FlowReviewing):
		return "REVIEW"
	case s.Flow == string(domain.FlowRewriting) || s.Flow == string(domain.FlowPolishing):
		return "REWRITE"
	case s.RuntimeState == "running":
		return "RUNNING"
	default:
		return "READY"
	}
}

// ── Quản lý mô hình ──

func (h *Host) ConfiguredProviders() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	providers := make([]string, 0, len(h.cfg.Providers))
	for name := range h.cfg.Providers {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	return providers
}

func (h *Host) ConfiguredModels(provider string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.CandidateModels(provider)
}

func (h *Host) CurrentModelSelection(role string) (string, string, bool) {
	return h.models.CurrentSelection(role)
}

func (h *Host) SwitchModel(role, provider, model string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if provider == "" || model == "" {
		return fmt.Errorf("provider và model là bắt buộc")
	}
	if err := h.models.Swap(role, provider, model); err != nil {
		return err
	}
	if role == "" || role == "default" {
		h.cfg.Provider = provider
		h.cfg.ModelName = model
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.Provider = provider
		rc.Model = model
		h.cfg.Roles[role] = rc
	}
	// Đổi model không sửa ý định cường độ suy luận đã lưu: chỉ kẹp theo năng lực model mới lúc phát hành.
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("lưu cấu hình thất bại", "module", "host", "err", err)
		}
	}
	h.applyThinkingLocked(role)
	// Khi chuyển sang model chưa đăng ký thì ghi một dòng warn, báo người dùng đang đi qua nhánh dự phòng 128k — truyện dài dễ bị nén sớm.
	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	window, source := h.cfg.ResolveContextWindow(provider, model)
	bootstrap.LogContextWindowChoice(logRole, model, window, source)

	// Không có ngữ cảnh thường trú cần đồng bộ: ContextManager của writer/architect/editor đi qua
	// ContextManagerFactory, lần spawn kế tiếp sẽ tái dựng theo cửa sổ của model mới.

	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("model đã chuyển: %s -> %s/%s", role, provider, model),
		Level:    "info",
	})
	return nil
}

// concreteThinkingRoles là các vai cụ thể có thể áp dụng cường độ suy luận (trùng với tuyến agents.ApplyThinking).
// Khi gọi default thì sẽ áp dụng lại từng vai theo ResolveReasoningEffort của riêng từng vai.
var concreteThinkingRoles = []string{"architect", "writer", "editor"}

// CurrentThinking trả về chuỗi gốc cường độ suy luận đang có hiệu lực cho một vai (để /model đồng bộ giá trị hiện tại).
func (h *Host) CurrentThinking(role string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.ResolveReasoningEffort(strings.ToLower(strings.TrimSpace(role)))
}

func (h *Host) AvailableThinking(role string) []agentcore.ThinkingLevel {
	h.mu.Lock()
	model := h.models.ForRole(strings.ToLower(strings.TrimSpace(role)))
	h.mu.Unlock()
	return agents.AvailableThinkingForModel(model)
}

// resolveThinkingForRoleLocked tính cường độ suy luận thực tế có hiệu lực cho một vai: lấy ý định gốc của nó
// (ResolveReasoningEffort: cấp vai -> mặc định cấp trên), rồi kẹp theo năng lực model hiện tại của vai đó.
// Việc kẹp chỉ xảy ra trên đường "có hiệu lực" này, không ghi ngược lại cấu hình — kho lưu trữ luôn giữ ý định gốc của người dùng.
func (h *Host) resolveThinkingForRoleLocked(role string) agentcore.ThinkingLevel {
	parsed, _ := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role))
	resolved, _ := agents.ResolveThinkingForModel(h.models.ForRole(role), parsed)
	return resolved
}

// applyThinkingLocked đẩy cường độ có hiệu lực xuống live agent; mỗi vai được kẹp theo model riêng của nó.
func (h *Host) applyThinkingLocked(role string) {
	if h.thinkingApplier == nil {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" || role == "default" {
		for _, r := range concreteThinkingRoles {
			h.thinkingApplier(r, h.resolveThinkingForRoleLocked(r))
		}
		return
	}
	h.thinkingApplier(role, h.resolveThinkingForRoleLocked(role))
}

// SetRoleThinking đặt cường độ suy luận cho một vai (hoặc default): kiểm tra -> lưu bền -> đồng bộ live agent -> sự kiện.
// Cấu trúc giống SwitchModel; độc lập với chọn model, có thể chỉnh riêng. level rỗng = không ghi đè (kế thừa).
func (h *Host) SetRoleThinking(role, level string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	parsed, err := agents.ParseThinkingLevel(level)
	if err != nil {
		return err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	// Kho lưu trữ giữ nguyên ý định gốc: ghi bền trực tiếp mức người dùng chọn, việc kẹp chỉ xảy ra khi phát(applyThinkingLocked) theo năng lực model.
	if role == "" || role == "default" {
		h.cfg.ReasoningEffort = string(parsed)
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.ReasoningEffort = string(parsed)
		h.cfg.Roles[role] = rc
	}
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("lưu cấu hình thất bại", "module", "host", "err", err)
		}
	}

	// Đồng bộ live: vai cụ thể áp dụng trực tiếp; còn default thì duyệt qua các vai cụ thể và áp dụng lại theo ResolveReasoningEffort
	// (vai nào đã bị ghi đè riêng thì giữ của nó, vai chưa ghi đè sẽ nhận default mới).
	h.applyThinkingLocked(role)

	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	shown := string(parsed)
	if shown == "" {
		shown = "mặc định (kế thừa)"
	}
	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("cường độ suy luận đã chuyển: %s -> %s", logRole, shown),
		Level:    "info",
	})
	return nil
}

// ── Phát lại sự kiện ──

func (h *Host) ReplayQueue(afterSeq int64) ([]domain.RuntimeQueueItem, error) {
	if h.store == nil || h.store.Runtime == nil {
		return nil, nil
	}
	return h.store.Runtime.LoadQueueAfter(afterSeq)
}

// ── Đồng sáng tạo ──

// CoCreateStream khởi động lạnh đồng sáng tạo: từ con số 0 làm rõ nhu cầu, sinh ra bộ chỉ dẫn sáng tác cho cả cuốn sách.
func (h *Host) CoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, coCreateSystemPrompt, history, onProgress)
}

// StageCoCreateStream: đồng sáng tạo theo giai đoạn, lên kế hoạch cho hướng đi tiếp theo dựa trên phần đã viết.
// Prompt hệ thống = prompt giai đoạn + tóm tắt trạng thái truyện hiện tại, để trợ lý biết "đã viết những gì rồi".
func (h *Host) StageCoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, stageSystemPrompt(h.store), history, onProgress)
}

// stagePlanPrefix bọc "brief định hướng tiếp theo" do đồng sáng tạo tạo ra thành một can thiệp lập kế hoạch theo giai đoạn để giao Arbiter phán quyết.
// Chỉ gắn nhãn sự thật [Lập kế hoạch giai đoạn] + câu mô tả trung tính, không đóng đinh "triển khai thế nào" — tuyến cụ thể (compass / architect /
// user_rules) để cho bộ tiêu chí "lập kế hoạch giai đoạn" trong arbiter-intervention.md quyết định, tránh biến prompt thành nguồn chân lý thứ hai,
// cũng không chặn các yêu cầu về phong cách đi qua user_rules (giữ nguyên "phân loại do LLM phán quyết"). Continue sẽ nối thêm tiền tố [can thiệp của người dùng].
const stagePlanPrefix = "[Lập kế hoạch giai đoạn] Tôi đã tạm dừng sáng tác và cùng trợ lý đồng sáng tạo gom lại hướng đi tiếp theo dưới đây, xin hãy dựa vào phân loại can thiệp của bạn để quyết định cách triển khai, rồi tiếp tục sáng tác. Hướng đi tiếp theo như sau:\n\n"

// PauseForCoCreate đi vào đồng sáng tạo theo giai đoạn: đặt cờ chiếm dụng đồng sáng tạo, nếu đang chạy thì tạm dừng Engine luôn.
// Trả false nghĩa là không thể vào (cuốn sách đã hoàn tất hoặc đang ở trong đồng sáng tạo), bên gọi có thể bỏ qua.
// Cờ chiếm dụng trong cửa sổ đồng sáng tạo sẽ chặn đồng thời import/simulate/start/resume/continue —
// khi đang chạy và dừng lại thì lifecycle=paused, ràng buộc ==running hiện có không còn hiệu lực, nên phải có cờ này bù vào;
// khi đã dừng (idle/paused) vẫn được vào, sau khi lên kế hoạch sẽ tiếp tục qua Continue.
func (h *Host) PauseForCoCreate() bool {
	h.mu.Lock()
	if h.cocreating || h.lifecycle == lifecycleCompleted {
		h.mu.Unlock()
		return false
	}
	h.cocreating = true
	running := h.lifecycle == lifecycleRunning
	h.mu.Unlock()

	// Khi đang chạy dùng lại abortWithEvent để dừng (running -> paused + setAborting + Abort + sự kiện), cùng thứ tự như
	// tạm dừng thủ công, không viết lại một lần nữa; khi đã dừng (idle/paused) chỉ cần đặt cờ, sau khi lên kế hoạch sẽ tiếp tục qua Continue.
	if running {
		h.abortWithEvent("đi vào đồng sáng tạo theo giai đoạn, sáng tác đã tạm dừng", "info")
	} else {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "đi vào đồng sáng tạo theo giai đoạn", Level: "info"})
	}
	return true
}

// ResumeFromCoCreate kết thúc đồng sáng tạo theo giai đoạn: đưa hướng đi tiếp theo do đồng sáng tạo tạo ra vào như một can thiệp rồi phục hồi sáng tác.
// Sau khi dọn cờ chiếm dụng sẽ tái dùng đường chèn can thiệp của Continue (chịu ràng buộc ngân sách trước).
// Lưu ý: nếu draft rỗng thì trả sớm, không dọn cờ là có chủ ý (đồng sáng tạo chưa kết thúc); guard canStart() ở phía TUI
// và chỗ này dùng cùng tiêu chí "không rỗng", nên đường này không thể đi tới, cocreating sẽ không bị rò.
func (h *Host) ResumeFromCoCreate(draft string) error {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return fmt.Errorf("draft là bắt buộc")
	}
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("không ở chế độ đồng sáng tạo")
	}
	h.cocreating = false
	h.mu.Unlock()

	// Abort của PauseForCoCreate là bất đồng bộ: đợi vòng lặp engine thực sự lắng xuống rồi mới tiếp tục, quay về cùng điều kiện
	// "engine đã dừng thật" như khi Continue sau tạm dừng thủ công. Cửa sổ đồng sáng tạo là thang thời gian tương tác người-máy, polling ngắn không đáng kể.
	for h.engine.isRunning() {
		time.Sleep(20 * time.Millisecond)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "đồng sáng tạo theo giai đoạn đã hoàn tất, đã chèn hướng đi tiếp theo và phục hồi sáng tác", Level: "info"})
	return h.Continue(stagePlanPrefix + draft)
}

// CancelCoCreate bỏ đồng sáng tạo theo giai đoạn: dọn cờ chiếm dụng, giữ nguyên trạng thái tạm dừng (người dùng có thể tiếp tục trong ô nhập hoặc khởi động lại bằng Resume).
func (h *Host) CancelCoCreate() {
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return
	}
	h.cocreating = false
	h.mu.Unlock()
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "đã thoát đồng sáng tạo theo giai đoạn, sáng tác vẫn tạm dừng (có thể tiếp tục trong ô nhập)", Level: "info"})
}

// ── Công cụ ──

func (h *Host) refreshWriterRestore() {
	if h.writerRestore != nil {
		h.writerRestore.Refresh(h.store)
	}
}

func (h *Host) CheckChapterRevisions() ([]int, error) {
	pending, err := h.store.Revisions.LoadPending()
	if err != nil {
		return nil, fmt.Errorf("đọc hồ sơ phục hồi sửa đổi: %w", err)
	}
	if pending != nil {
		chapters := make([]int, 0, len(pending.Items))
		for _, item := range pending.Items {
			chapters = append(chapters, item.Chapter)
		}
		return chapters, nil
	}
	changes, err := revision.Scan(h.store)
	if err != nil {
		return nil, err
	}
	return revision.ChangedChapters(changes), nil
}

func (h *Host) SyncChapterRevisions(ctx context.Context) (*revision.Result, error) {
	if err := h.acquireExclusive("đồng bộ sửa đổi chương"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()
	defer h.releaseExclusive()

	pending, err := h.store.Revisions.LoadPending()
	if err != nil {
		return nil, err
	}
	if pending == nil {
		changes, err := revision.Scan(h.store)
		if err != nil {
			return nil, err
		}
		if len(changes) == 0 {
			return &revision.Result{}, nil
		}
		if err := h.budget.Refuse(); err != nil {
			return nil, err
		}
	}
	model := h.models.ForRoleWithFailover("editor", func(ev bootstrap.FailoverEvent) {
		slog.Warn("chuyển provider khi sửa đổi chương", "module", "revision", "role", ev.Role,
			"reason", ev.Reason, "from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel), "err", ev.Err)
	})
	model = newUsageTrackedModel(model, "editor", h.usage.Record)
	service := revision.NewService(h.store, model, h.bundle.Prompts.RevisionAnalyze, h.styleStats)
	return service.Sync(ctx)
}

func (h *Host) requireCleanChapters() error {
	chapters, err := h.CheckChapterRevisions()
	if err != nil {
		return fmt.Errorf("kiểm tra sửa đổi bên ngoài của chương: %w", err)
	}
	if len(chapters) > 0 {
		return fmt.Errorf("phát hiện thân chương đã bị sửa bên ngoài: %v; hãy chạy /sync trước", chapters)
	}
	return nil
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

// ImportFrom khởi động một lần nhập biên dịch ngữ nghĩa tiểu thuyết bên ngoài: ingest -> segment -> analyze -> synthesize -> publish.
// Mô hình chỉ phán quyết ngữ nghĩa mở (ranh giới/sự thật/tổng hợp), Go quản lý tọa độ/phủ/đảm bảo idempotent; độc quyền với Engine,
// sau khi import xong sẽ do AdvanceHold quyết định có viết tiếp hay không.
// Kênh sự kiện trả về sẽ do imp.Run đóng, phần gọi chịu trách nhiệm tiêu thụ (nếu đầy thì bỏ để tránh chặn goroutine của pipeline).
func (h *Host) ImportFrom(ctx context.Context, opts imp.Options) (<-chan imp.Event, error) {
	// Kiểm tra trước khi bắt đầu ngân sách dùng cùng kỷ luật với Start/Resume/Continue: import là toàn quy trình gọi mô hình,
	// ngân sách đã vượt giới hạn thì không được khởi động (§13.1 "gắn vào sentinel ngân sách hiện có").
	if err := h.budget.Refuse(); err != nil {
		return nil, err
	}
	if err := h.acquireExclusive("import"); err != nil {
		return nil, err
	}
	// Đăng ký hàm hủy: khi dừng cứng do ngân sách / tạm dừng thủ công thì abortWithEvent sẽ hủy context riêng của import
	// (nếu không sentinel chỉ dừng Engine chưa chạy còn import vẫn tiếp tục tiêu tiền).
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	deps := imp.Deps{
		Store:         h.store,
		CommitChapter: tools.NewCommitChapterTool(h.store, h.styleStats),
		Segment:       h.importCaller("segment"),
		Analyze:       h.importCaller("analyze"),
		Synthesize:    h.importCaller("synthesize"),
		Prompts: imp.Prompts{
			Segment:    h.bundle.Prompts.ImportSegment,
			Analyze:    h.bundle.Prompts.ImportAnalyze,
			Synthesize: h.bundle.Prompts.ImportSynthesize,
			Range:      h.bundle.Prompts.ImportRange,
		},
	}
	ch, err := imp.Run(ctx, deps, opts)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return h.superviseImport(ch, opts), nil
}

// ImportResumeHint trả về một dòng gợi ý cho import chưa hoàn tất (nếu không có thì chuỗi rỗng), để TUI chủ động thông báo lúc khởi động (RFC §18.2).
// Chỉ gọi một lần khi khởi động: bên trong sẽ tính lại InputDigest của từng hiện vật trong workspace, không thích hợp để đưa vào polling snapshot.
func (h *Host) ImportResumeHint() string {
	return imp.ResumeSummary(h.store)
}

// importCaller phân giải cấp mô hình cho một hàm ngữ nghĩa import (RFC §13.1): nếu cấu hình roles có import_<fn>
// thì dùng cấp đó (usage cũng tính vào tài khoản vai đó), nếu không thì rơi về architect. Đây là cấu hình cuộc gọi, không đổi bất kỳ hợp đồng ngữ nghĩa nào.
func (h *Host) importCaller(fn string) imp.Caller {
	role := "import_" + fn
	if _, _, explicit := h.models.CurrentSelection(role); !explicit {
		role = "architect"
	}
	model := h.models.ForRoleWithFailover(role, func(ev bootstrap.FailoverEvent) {
		slog.Warn("chuyển provider khi import", "module", "import", "role", ev.Role,
			"reason", ev.Reason,
			"from", fmt.Sprintf("%s/%s", ev.FromProvider, ev.FromModel),
			"to", fmt.Sprintf("%s/%s", ev.ToProvider, ev.ToModel),
			"err", ev.Err)
	})
	model = newUsageTrackedModel(model, role, h.usage.Record)
	return imp.Caller{Model: model, Runtime: h.importModelRuntime(role, model)}
}

// importModelRuntime dò khả năng gọi của model theo cấp vai được chọn, để imp dùng ngân sách kép / thinking thích ứng (RFC §13/§21).
// Các trường dò không thành công sẽ để giá trị zero, phía imp sẽ lùi về mặc định bảo thủ, bảo đảm không có thông tin năng lực vẫn chạy đúng.
// Structured output được llmcontract của imp đọc trực tiếp từ thực tế model trước mỗi request, không cache lại trong Runtime.
func (h *Host) importModelRuntime(role string, model agentcore.ChatModel) imp.ModelRuntime {
	var rt imp.ModelRuntime
	provider, name, _ := h.models.CurrentSelection(role)
	if name == "" {
		name = bootstrap.ModelName(model)
		provider = bootstrap.ModelProvider(model)
	}
	// giới hạn context / completion: registry là nguồn tin cậy duy nhất (Info() của model bọc không chứa cửa sổ).
	rt.ContextTokens, _ = h.cfg.ResolveContextWindow(provider, name)
	if entry, ok := modelreg.DefaultRegistry().Resolve(name); ok {
		rt.MaxOutputTokens = entry.MaxTokens
	}
	// thinking: resolve theo reasoning effort của vai và năng lực model; nếu không hỗ trợ thì không phát (chung chiến lược với arbiter).
	if level, err := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role)); err == nil {
		if resolved, ok := agents.ResolveThinkingForModel(model, level); ok {
			rt.Thinking = resolved
		}
	}
	return rt
}

// Simulate đọc thư mục simulate và tạo hoặc cập nhật dần hồ sơ phỏng theo.
func (h *Host) Simulate(ctx context.Context) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("tạo hồ sơ phỏng theo"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	wd, err := os.Getwd()
	if err != nil {
		h.releaseExclusive()
		return nil, fmt.Errorf("lấy thư mục làm việc: %w", err)
	}
	deps := sim.Deps{
		Store: h.store,
		LLM:   h.models.ForRole("architect"),
		Prompts: sim.Prompts{
			Source: h.bundle.Prompts.SimulationSource,
			Merge:  h.bundle.Prompts.SimulationMerge,
		},
	}
	ch, err := sim.Run(ctx, deps, sim.Options{SourceDir: filepath.Join(wd, "simulate")})
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

// ImportSimulationProfile nhập hồ sơ phỏng theo đã tạo trước đó.
func (h *Host) ImportSimulationProfile(ctx context.Context, path string) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("nhập hồ sơ phỏng theo"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()
	ch, err := sim.RunImport(ctx, h.store, path)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

// acquireExclusive chiếm dụng nguyên tử một ô tác vụ nền độc quyền (import/simulate/revision): khi Engine đang chạy, trong cửa sổ đồng sáng tạo,
// hoặc đã có một tác vụ độc quyền khác đang chạy thì từ chối. Thành công thì đăng ký chiếm dụng, tác vụ kết thúc phải gọi releaseExclusive để nhả ra —
// nếu không hai lần import hoặc import + phỏng theo sẽ tranh ghi cùng một trạng thái. Bổ sung phần còn thiếu trước đây chỉ kiểm tra ==running/cocreating mà không đăng ký chính tác vụ.
func (h *Host) acquireExclusive(action string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.closing:
		return fmt.Errorf("Host đang đóng, không thể %s", action)
	// engine.isRunning() phải kiểm tra: Abort sẽ đặt lifecycle=paused trước rồi chờ goroutine thoát bất đồng bộ,
	// trong khoảng đó lifecycle không còn running nhưng engine vẫn có thể đang ghi store (cùng kỷ luật với cổng khởi động).
	case h.lifecycle == lifecycleRunning || h.engine.isRunning():
		return fmt.Errorf("động cơ sáng tác đang chạy hoặc đang dừng, hãy chờ rồi mới %s", action)
	case h.cocreating:
		return fmt.Errorf("đang đồng sáng tạo theo giai đoạn, hãy kết thúc trước rồi mới %s", action)
	case h.exclusive != "":
		return fmt.Errorf("%s đang diễn ra, hãy hoàn tất trước rồi mới %s", h.exclusive, action)
	}
	h.exclusive = action
	return nil
}

// releaseExclusive giải phóng ô tác vụ nền độc quyền (kèm theo hàm hủy đã đăng ký).
func (h *Host) releaseExclusive() {
	h.mu.Lock()
	cancel := h.exclusiveCancel
	h.exclusive = ""
	h.exclusiveCancel = nil
	h.mu.Unlock()
	if cancel != nil {
		cancel() // Tác vụ đã kết thúc: giải phóng context phát sinh; không ảnh hưởng gì đến runner đã thoát
	}
}

// superviseExclusive chuyển tiếp sự kiện của tác vụ độc quyền, và khi kênh đóng (tức tác vụ kết thúc) thì giải phóng ô chiếm dụng.
func superviseExclusive[T any](h *Host, src <-chan T) <-chan T {
	out := make(chan T, 32)
	if !h.launchAsync(func() {
		defer close(out)
		defer h.releaseExclusive()
		for ev := range src {
			select {
			case out <- ev:
			case <-h.runCtx.Done():
				// Trong thời gian đóng tiếp tục xả cạn kênh nguồn, để producer không bị kẹt vì sự kiện cuối cùng và có thể thoát.
				for range src {
				}
				return
			}
		}
	}) {
		close(out)
		h.releaseExclusive()
	}
	return out
}

// superviseImport là chủ sở hữu duy nhất của quyết định "sau import xong có tiếp lực hay không": chuyển tiếp sự kiện import, khi hoàn thành thành công thì trước hết nhả ô độc quyền,
// rồi mới quyết định và thực thi tiếp lực, cuối cùng ghi kết quả tiếp lực thật vào trường Continued của sự kiện StageDone. TUI chỉ dựa vào đây để hiển thị,
// không còn dùng cờ --continue cục bộ để đoán trạng thái chạy (loại bỏ cạnh tranh thời điểm do Runner/Host/TUI tự diễn giải riêng).
func (h *Host) superviseImport(src <-chan imp.Event, opts imp.Options) <-chan imp.Event {
	out := make(chan imp.Event, 32)
	if !h.launchAsync(func() {
		defer close(out)
		released := false
		release := func() {
			if !released {
				released = true
				h.releaseExclusive()
			}
		}
		defer release()
		for ev := range src {
			if ev.Stage == imp.StageDone {
				release() // Nhả ô độc quyền trước, để startEngine cho tiếp lực đi qua được cổng độc quyền
				ev.Continued = h.continueAfterImport(opts)
			}
			select {
			case out <- ev:
			case <-h.runCtx.Done():
				for range src {
				}
				return
			}
		}
	}) {
		close(out)
		h.releaseExclusive()
	}
	return out
}

// launchAsync đăng ký một tác vụ nền trong vòng đời Host. closing và WaitGroup.Add được bảo vệ bởi cùng một khóa,
// bảo đảm khi Close bắt đầu Wait thì sẽ không còn Add mới nào xuất hiện.
func (h *Host) launchAsync(fn func()) bool {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		return false
	}
	h.asyncWG.Add(1)
	h.mu.Unlock()
	go func() {
		defer h.asyncWG.Done()
		fn()
	}()
	return true
}

// runAsync dùng lại cơ chế đăng ký tác vụ nền sẵn có của Host, đồng thời trả lỗi nghiệp vụ cho bên gọi.
func (h *Host) runAsync(fn func() error) (error, bool) {
	result := make(chan error, 1)
	if !h.launchAsync(func() { result <- fn() }) {
		return nil, false
	}
	return <-result, true
}

// continueAfterImport quyết định và thực thi tiếp lực tự động thật sự của --continue, trả về việc Engine đã được khởi động hay chưa.
// Ý định tiếp lực hợp lệ = lần gọi hiện tại trong opts hoặc intent đã lưu bền trong workspace (bao phủ trường hợp khôi phục /import không có tham số sau sập);
// chỉ tiếp lực ở chế độ tiến tự động, để quy hoạch theo cung mở rộng tiếp nhận truyện mở, hoặc để khép lại truyện đã hoàn tất; review thì giao cho người dùng /next.
func (h *Host) continueAfterImport(opts imp.Options) bool {
	want := opts.ContinueAfter
	if !want {
		in, err := imp.OpenWorkspace(h.store.Dir()).LoadIntent()
		if err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "import đã hoàn tất, nhưng không đọc được ý định tiếp lực tự động: " + err.Error()})
		} else if in != nil {
			want = in.ContinueAfterImport
		}
	}
	if !want {
		return false
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil || meta == nil {
		slog.Warn("đọc RunMeta cho tiếp lực tự động của import thất bại", "module", "host", "err", err)
		return false
	}
	if meta.AdvanceMode != domain.ChapterAdvanceAuto {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: "import đã hoàn tất; hiện đang ở chế độ nghiệm thu từng chương, hãy nhập tiếp tục hoặc /next để tiếp lực viết tiếp"})
		return false
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: "import đã hoàn tất, tự động tiếp lực viết tiếp"})
	if !h.startEngine(nil) {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "khởi động tiếp lực tự động thất bại, hãy nhập lệnh tiếp tục để phục hồi thủ công"})
		return false
	}
	return true
}

// Export xuất các chương đã hoàn thành ra file bên ngoài (hiện chỉ hỗ trợ TXT).
//
// Khác với ImportFrom: xuất là thao tác chỉ đọc (không đụng Progress / Checkpoint),
// vì vậy **không yêu cầu Engine phải dừng** — giữa lúc viết vẫn có thể xuất bất cứ lúc nào để lấy "sản phẩm hiện tại".
// Chỉ đọc một snapshot nhất quán từ Progress.CompletedChapters + bản cuối chương + dàn ý + premise.
func (h *Host) Export(ctx context.Context, opts exp.Options) (*exp.Result, error) {
	return exp.Run(ctx, exp.Deps{Store: h.store}, opts)
}
