package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"

	"github.com/voocel/ainovel-cli/internal/arbiter"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/notify"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

// engine là bộ máy thực thi quyết định xác định: đọc facts → Route → kiểm tra trước →
// chạy trực tiếp Worker → kiểm tra tiến độ → lặp; các tình huống ngữ nghĩa sẽ hỏi
// Arbiter khi cần. Nó chỉ thực thi quyết định, không tham gia phán đoán văn chương
// (docs/engine-rfc.md). Chỉ một goroutine chạy tuần tự, trạng thái điều khiển chỉ đổi
// ở ranh giới vòng lặp.
type engine struct {
	store   *storepkg.Store
	workers *subagent.Runner

	arbiterModel    agentcore.ChatModel
	failurePrompt   string
	planStartPrompt string // prompt hệ thống cho phán quyết khởi động: khi chưa hoàn tất, engine bổ sung phán quyết tại chỗ theo StartPrompt
	style           string // tên phong cách, được truyền cho DecidePlanStart khi bổ sung phán quyết
	// reconsult gửi các can thiệp đã quá hạn trở lại đường phán quyết đầy đủ của host
	// (lưu trữ/kiểm toán/áp dụng toàn bộ hành động), chạy bất đồng bộ - engine chỉ bỏ
	// qua các phân công cũ, không tự phán quyết lại phần dang dở.
	reconsult func(text string)

	observer  *observer
	budget    *BudgetSentinel
	gate      *ChapterAdvanceGate
	refresh   func() // làm mới RestorePack trước mỗi lần writer được phân công
	emitEvent func(Event)
	notify    func(kind, level, title, body string)
	onPause   func(summary string) // engine tự tạm dừng (bế tắc/phán quyết thất bại abort): đi theo ngữ nghĩa tạm dừng thống nhất của host (lifecycle=paused)
	onDone    func()               // run kết thúc (bất kỳ lý do nào); host dựa vào facts trong store để xác định trạng thái cuối

	mu      sync.Mutex
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	running bool
	pending []controlOp       // các hành động điều khiển của can thiệp, được commit ở ranh giới
	next    *flow.Instruction // chỉ thị được ưu tiên cho vòng tiếp theo (plan_start / arbiter dispatch)
	// deferGateForNext chỉ sống chết cùng next: hold+dispatch phải chạy trước một cặp
	// editor/writer, để nó tạo hàng đợi sửa lại, sau đó Gate mới có thể xét rewrites_drained.
	deferGateForNext bool

	// Theo dõi bế tắc: nếu sau một vòng Route vẫn sinh cùng một khóa chỉ thị thì cộng dồn.
	// Chỉ thị Router là ảnh chiếu của điều kiện hoàn tất sau tác vụ; khi thật sự xong thì chỉ thị kế tiếp sẽ đổi.
	lastKey string
	repeats int
	// Thử lại khi thất bại: cùng một khóa chỉ thị chỉ được thử lại một lần, thất bại nữa mới hỏi Arbiter.
	failedKey string
	// Giữ lại lỗi Worker gần nhất cho cùng chỉ thị, để phán quyết bế tắc thấy được nguyên nhân thất bại thật.
	lastWorkerErrorKey string
	lastWorkerError    error
}

// deadlockConsultAt / deadlockAbortAt: khi repeats đạt mốc đầu thì hỏi Arbiter, đạt mốc sau thì ngắt cứng.
// Engine xác định phải có giới hạn rõ ràng cho vòng lặp không có tiến triển (RFC §5).
const (
	deadlockConsultAt = 3
	deadlockAbortAt   = 5
)

// controlOp là hành động thay đổi trạng thái điều khiển trong phán quyết can thiệp
// (commit theo ranh giới; RFC §3).
// text/facts giữ nguyên ngữ cảnh tham chiếu ban đầu: khi dispatch đối soát thất bại thì dùng facts mới để hỏi lại.
type controlOp struct {
	hold     *arbiter.AdvanceHoldOp
	reopen   *arbiter.ReopenOp
	dispatch *arbiter.DispatchOp
	text     string
	facts    arbiter.InterventionFacts
}

// start khởi động vòng lặp engine; nếu đã chạy thì no-op (trả về false).
func (e *engine) start(initial *flow.Instruction) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = agentcore.WithToolProgress(ctx, e.observer.workerProgress)
	e.cancel = cancel
	e.running = true
	// Khi initial là nil thì không ghi đè e.next - các can thiệp trong thời gian dừng
	// có thể đã được xếp vào qua applyControlOp (như editor sửa lại), start(nil) xóa nó
	// sẽ khiến Route chuyển sang writer để viết tiếp, trái với ý định của người dùng.
	if initial != nil {
		e.next = initial
		e.deferGateForNext = false
	}
	e.lastKey, e.repeats, e.failedKey = "", 0, ""
	e.lastWorkerErrorKey, e.lastWorkerError = "", nil
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		e.run(ctx)
	}()
	return true
}

// abort hủy vòng lặp hiện tại (ngữ nghĩa tạm dừng; checkpoint đảm bảo không mất dữ liệu).
func (e *engine) abort() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// wait đợi goroutine Engine hiện tại thoát hoàn toàn. Host.Close sẽ cancel trước rồi mới gọi hàm này,
// bảo đảm công cụ ghi và runEnded đều hoàn tất trước khi đóng kênh sự kiện và thoát tiến trình.
func (e *engine) wait() {
	e.wg.Wait()
}

func (e *engine) isRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

// enqueue đưa hành động điều khiển của can thiệp vào hàng đợi ở ranh giới (khi engine đang chạy);
// trả về false nghĩa là chưa chạy, bên gọi nên tự thực thi ngay.
func (e *engine) enqueue(op controlOp) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return false
	}
	e.pending = append(e.pending, op)
	return true
}

func (e *engine) run(ctx context.Context) {
	defer func() {
		e.mu.Lock()
		e.running = false
		e.cancel = nil
		leftover := e.pending
		e.pending = nil
		e.mu.Unlock()
		// Cạnh tranh khi thoát: nếu enqueue và thoát chạy song song, các hành động can thiệp
		// còn sót lại không được mất âm thầm - hold/reopen là các ghi fact idempotent,
		// sẽ dùng ctx riêng để thực thi bù; dispatch thì không còn engine để phân công,
		// nên khôi phục PendingSteer đã lưu (host có thể đã xóa theo "enqueue thành công"),
		// lần Continue/Resume sau sẽ phát lại toàn bộ can thiệp.
		for _, op := range leftover {
			if op.dispatch != nil {
				if op.text != "" {
					if err := e.store.RunMeta.SetPendingSteer(op.text); err != nil {
						slog.Warn("khôi phục lưu lại can thiệp tồn dư thất bại", "module", "engine", "err", err)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Engine đã dừng, lệnh phán quyết chưa được thực thi; can thiệp đã được giữ lại, sẽ tự phán quyết lại khi tiếp tục sáng tác"})
				op.dispatch = nil
			}
			if op.hold != nil || op.reopen != nil {
				if err := e.applyControlOp(context.Background(), op); err != nil {
					e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Level: "error",
						Summary: "Thực thi bù can thiệp khi thoát engine thất bại: " + err.Error()})
				}
			}
		}
		e.onDone()
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		// hold+dispatch phải để cặp phân công chạy trước để tạo fact sửa lại; các trường hợp khác
		// sẽ kiểm tra Gate thống nhất trước khi phân công, bảo đảm boundary hold và review không có phép
		// sẽ không chạy thêm một Worker.
		deferGate := e.applyPendingOps(ctx) || e.nextDefersGate()
		if !deferGate {
			if e.gate.HandleBoundary() {
				return
			}
		}

		inst := e.takeNext()
		if inst == nil {
			state, err := flow.LoadState(e.store)
			if err != nil {
				e.pauseWithNotify(notify.KindWorkerFailure, "Đọc facts định tuyến thất bại, đã tạm dừng: "+err.Error())
				return
			}
			// Có thể bản tóm tắt tập đã được lưu xuống đĩa trong khi tiến trình chưa kịp MarkComplete.
			// Khi đủ hết các artifact tổng hợp thì trước tiên xác định hoàn tất theo facts, rồi mới
			// giao lại cho Router, tránh việc tập chốt bị nhầm sang viết tiếp.
			if state.AggregateRefresh == nil && state.Progress != nil && state.Progress.Layered &&
				state.Progress.Phase == domain.PhaseWriting && state.ArcBoundary != nil &&
				state.ArcBoundary.IsVolumeEnd && state.HasArcReview && state.HasArcSummary && state.HasVolumeSummary {
				complete, reconcileErr := tools.ReconcileLayeredCompletion(e.store)
				if reconcileErr != nil {
					e.pauseWithNotify(notify.KindWorkerFailure, "Khôi phục trạng thái hoàn tất thất bại, đã tạm dừng: "+reconcileErr.Error())
					return
				}
				if complete {
					continue
				}
			}
			inst = flow.Route(state)
		}
		if inst == nil {
			var err error
			inst, err = e.planStartFallback(ctx)
			if err != nil {
				e.pauseWithNotify(notify.KindPlanStart, "Đọc facts phục hồi kế hoạch thất bại, đã tạm dừng: "+err.Error())
				return
			}
		}
		if inst == nil {
			// Tình huống ngữ nghĩa hoặc trạng thái cuối: đã hoàn bản → kết thúc xác định;
			// các trường hợp khác (Steering còn sót, v.v.) → dừng tự nhiên, chờ người dùng Continue / can thiệp.
			return
		}
		replaced, err := e.precheck(inst)
		if err != nil {
			e.pauseWithNotify(notify.KindWorkerFailure, "Kiểm tra trước khi phân công thất bại, đã tạm dừng: "+err.Error())
			return
		}
		if replaced != nil {
			inst = replaced
		}
		allowed, gateErr := e.gate.Allow(inst)
		if gateErr != nil {
			e.pauseWithNotify(notify.KindAdvanceGate, "Lỗi kiểm soát tiến tới chương, đã tạm dừng: "+gateErr.Error())
			return
		}
		if !allowed {
			return
		}
		if stop := e.trackDeadlock(ctx, &inst); stop {
			return
		}
		if inst == nil {
			continue // phán quyết bế tắc yêu cầu tính lại route
		}

		err = e.runWorker(ctx, inst)
		if ctx.Err() != nil {
			return
		}
		e.rememberWorkerError(inst, err)
		if err != nil {
			// trackDeadlock đã ghi trước lần thử này trước khi dispatch. Lỗi chưa đi vào
			// thực thi ngữ nghĩa thực sự của Worker không được tính là "cùng một tác vụ không tiến triển".
			e.discardNonSemanticDeadlockAttempt(inst, err)
			if stop := e.handleWorkerError(ctx, inst, err); stop {
				return
			}
		}

		// Ranh giới chính sách: chặn ngân sách có ưu tiên hơn tạm dừng do kiểm nhận / tiến tới.
		if e.budget.HandleBoundary() {
			return
		}
		if e.gate.HandleBoundary() {
			return
		}
	}
}

func (e *engine) takeNext() *flow.Instruction {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst := e.next
	e.next = nil
	e.deferGateForNext = false
	return inst
}

func (e *engine) nextDefersGate() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.next != nil && e.deferGateForNext
}

// planStartFallback bao phủ hai cửa sổ khi facts kế hoạch bị thiếu và Route không suy ra được planner:
//  1. Phán quyết đã lưu xuống đĩa, nhưng save_foundation đầu tiên chưa xảy ra → tiếp tục chạy theo
//     PlanStartRecord đã cố định, không phán quyết lại (RFC §6); sau khi foundation đầu tiên được lưu,
//     tier đã vào vị trí, nhánh bù sẽ tiếp quản.
//  2. Phán quyết chưa bao giờ hoàn tất (mô hình lỗi lúc khởi động) nhưng facts đầu vào StartPrompt còn đó
//     → bổ sung phán quyết ngay tại chỗ. Đây là lần thử lại của phán quyết đầu tiên, không vi phạm
//     "khôi phục không phụ thuộc vào phán quyết lại" - kỷ luật đó áp dụng cho các phán quyết đã tồn tại.
//     Nếu bổ sung phán quyết thất bại sẽ đi qua tạm dừng rõ ràng: không cho phép dừng im lặng khi khởi động thất bại.
func (e *engine) planStartFallback(ctx context.Context) (*flow.Instruction, error) {
	progress, err := e.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return nil, nil
	}
	if progress.Phase == domain.PhaseWriting || progress.Phase == domain.PhaseComplete {
		return nil, nil
	}
	meta, err := e.store.RunMeta.Load()
	if err != nil {
		return nil, fmt.Errorf("load run meta: %w", err)
	}
	if meta == nil || meta.PlanningTier != "" {
		return nil, nil
	}
	missing, err := e.store.FoundationMissing()
	if err != nil {
		return nil, fmt.Errorf("load foundation state: %w", err)
	}
	if len(missing) == 0 {
		return nil, nil
	}
	if meta.PlanStart != nil {
		return &flow.Instruction{
			Agent:  meta.PlanStart.Planner,
			Task:   meta.PlanStart.PlannerTask,
			Reason: "Bắt đầu lập kế hoạch theo phán quyết khởi động đã được cố định",
		}, nil
	}
	if meta.StartPrompt == "" {
		return nil, nil
	}
	return e.retryPlanStart(ctx, meta.StartPrompt), nil
}

// retryPlanStart bổ sung phán quyết khởi động và cố định nó (phán quyết trước hết ghi fact rồi mới thực thi,
// cùng cấu trúc với StartPrepared).
func (e *engine) retryPlanStart(ctx context.Context, prompt string) *flow.Instruction {
	start := time.Now()
	decision, derr := runObservedDecision(e.observer, "bổ sung phán quyết khởi động", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(ctx, e.arbiterModel, e.planStartPrompt, prompt, e.style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: prompt,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	rec, recErr := e.store.Decisions.Append(rec)
	if recErr != nil {
		slog.Warn("ghi xuống đĩa kiểm toán cho bổ sung phán quyết khởi động thất bại", "module", "engine", "err", recErr)
	}
	if derr != nil {
		e.pauseWithNotify(notify.KindPlanStart, "Phán quyết khởi động thất bại, đã tạm dừng (vui lòng kiểm tra cấu hình mô hình/mạng rồi tiếp tục): "+derr.Error())
		return nil
	}
	if err := e.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: prompt, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		e.pauseWithNotify(notify.KindPlanStart, "Phán quyết khởi động không thể lưu xuống đĩa, đã tạm dừng: "+err.Error())
		return nil
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: fmt.Sprintf("Phán quyết khởi động đã được bổ sung (planner: %s - %s)", decision.Planner, decision.Reason)})
	return &flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}
}

// precheck là hình thái xác định của ToolGate: phân công không hợp lệ sẽ được viết lại trực tiếp,
// không cần văn bản giảng giải.
func (e *engine) precheck(inst *flow.Instruction) (*flow.Instruction, error) {
	progress, err := e.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if progress != nil && progress.Phase == domain.PhaseComplete {
		// Lối ra hợp lệ duy nhất ở giai đoạn hoàn bản là reopen (hành động can thiệp), mọi phân công khác sẽ bị bỏ.
		slog.Warn("phân công ở giai đoạn hoàn bản bị bỏ", "module", "engine", "agent", inst.Agent)
		return &flow.Instruction{}, nil // đặt rỗng: vòng sau Route về nil thì dừng tự nhiên
	}
	if inst.Agent == "writer" {
		if progress == nil || progress.Phase != domain.PhaseWriting {
			phase := "<nil>"
			if progress != nil {
				phase = string(progress.Phase)
			}
			return nil, fmt.Errorf("writer chỉ có thể được phân công ở giai đoạn writing (phase hiện tại=%s): %w", phase, errInvalidWriteTarget)
		}
		ch, err := writerTargetChapter(e.store)
		if err != nil {
			return nil, err
		}
		if ch > 0 {
			if err := tools.EnsureChapterExpanded(e.store, ch); err != nil {
				if !errors.Is(err, errs.ErrToolPrecondition) {
					return nil, err
				}
				// Chương đích chưa được mở rộng -> đổi sang architect_long để mở rộng một cách xác định
				// (văn bản giảng giải của gate cũ vốn dành cho LLM; Engine tự làm điều đúng).
				return &flow.Instruction{
					Agent:  "architect_long",
					Task:   fmt.Sprintf("Cung tiếp theo là khung xương (%s). Gọi save_foundation(type=expand_arc) để mở rộng cung tiếp theo; nếu tập hiện tại đã viết xong, hãy dùng type=append_volume để bổ sung rồi mở rộng tập kế tiếp.", err),
					Reason: "Chương đích viết chưa được mở rộng, cần mở rộng trước rồi mới viết tiếp",
				}, nil
			}
		}
		e.refresh()
	}
	return nil, nil
}

// writerTargetChapter suy ra chương thực tế mà writer sẽ được phân công viết ở lần tiếp theo
// (đầu hàng đợi sửa lại, nếu không thì là chương kế tiếp).
func writerTargetChapter(st *storepkg.Store) (int, error) {
	progress, err := st.Progress.Load()
	if err != nil {
		return 0, fmt.Errorf("load progress: %w", err)
	}
	if progress == nil {
		return 0, fmt.Errorf("progress chưa được khởi tạo")
	}
	if len(progress.PendingRewrites) > 0 {
		return progress.PendingRewrites[0], nil
	}
	return progress.NextChapter(), nil
}

// trackDeadlock duy trì bộ đếm bế tắc: nếu cùng một Agent+Task xuất hiện liên tiếp thì
// suy ra vòng trước chưa thỏa điều kiện hậu định tuyến. Các checkpoint trung gian như plan/draft/edit
// bên trong Worker chỉ phục vụ khôi phục và quan sát, không được reset bộ đếm cấp Engine (issue #84).
// Khi repeats chạm ngưỡng sẽ hỏi Arbiter, còn ngưỡng cứng thì ngắt.
// Trả về stop=true nghĩa là vòng này phải kết thúc; inst có thể bị Arbiter viết lại (reroute) hoặc đặt nil (tính lại).
func (e *engine) trackDeadlock(ctx context.Context, inst **flow.Instruction) (stop bool) {
	in := *inst
	if in == nil || in.Agent == "" {
		*inst = nil
		return false
	}
	key := instructionKey(in)
	if key == e.lastKey {
		e.repeats++
	} else {
		e.lastKey, e.repeats = key, 1
	}
	if e.repeats < deadlockConsultAt {
		return false
	}
	if e.repeats >= deadlockAbortAt {
		e.pauseStuck(notify.KindDeadlock, in, fmt.Sprintf("Ngắt cứng bế tắc: chỉ thị liên tiếp không có tiến triển %d lần (%s), đã tạm dừng chờ can thiệp thủ công", e.repeats, in.Agent))
		return true
	}
	// Tham vấn Arbiter cho bế tắc (repeats ∈ [consultAt, abortAt)). Phán quyết retry sẽ không xóa bộ đếm.
	facts := e.failureFacts("deadlock", in, e.workerErrorFor(in))
	decision, err := runObservedDecision(e.observer, "phán quyết bế tắc", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("deadlock", in, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindDeadlock, "Phán quyết bế tắc thất bại, đã tạm dừng chờ can thiệp thủ công: "+err.Error())
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		*inst = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		return false
	default: // abort
		e.pauseStuck(notify.KindDeadlock, in, "Phán quyết bế tắc: "+decision.Reason)
		return true
	}
}

// runWorker chạy trực tiếp một subagent: sự kiện DISPATCH + trung chuyển tiến độ + phân tích kết quả.
func (e *engine) runWorker(ctx context.Context, inst *flow.Instruction) error {
	e.observer.dispatchStart(inst.Agent, inst.Task, inst.Reason)
	// Gắn trước trạng thái đang tiến hành cho nhiệm vụ Writer (giống Dispatcher cũ:
	// UI dàn ý phản ánh ngay "▸ Đang tiến hành").
	if inst.Agent == "writer" && inst.Chapter > 0 {
		if err := e.store.Progress.ValidateChapterWork(inst.Chapter); err != nil {
			runErr := fmt.Errorf("%w: %w", errInvalidWriteTarget, err)
			e.observer.dispatchFinish(inst.Agent, runErr)
			return runErr
		}
		if err := e.store.Progress.StartChapter(inst.Chapter); err != nil {
			runErr := fmt.Errorf("%w: gắn trước chương %d đang tiến hành thất bại: %w", errInvalidWriteTarget, inst.Chapter, err)
			e.observer.dispatchFinish(inst.Agent, runErr)
			return runErr
		}
	}

	// Tiến độ của Worker được trung chuyển qua ctx ToolProgress tới observer.
	runCtx := agentcore.WithToolProgress(ctx, func(p agentcore.ProgressPayload) {
		e.observer.workerProgress(p)
	})
	_, err := e.workers.Run(runCtx, inst.Agent, inst.Task)
	if err == nil {
		// Thành công thì xóa dấu vết thất bại: lần thất bại tiếp theo của cùng khóa sẽ lại có "thử lại trước một lần" riêng.
		e.failedKey = ""
	}
	e.observer.dispatchFinish(inst.Agent, err)
	return err
}

// handleWorkerError trước hết thử lại một lần với cùng chỉ thị, sau đó mới giao loại lỗi và facts hiện tại cho Arbiter.
// Engine không mã hóa cứng các lỗi thực thi nào "chắc chắn không thể phục hồi"; việc đổi hướng theo ngữ nghĩa do mô hình quyết định,
// còn ranh giới Store vẫn chịu trách nhiệm ngăn ghi không hợp lệ.
func (e *engine) handleWorkerError(ctx context.Context, inst *flow.Instruction, werr error) (stop bool) {
	msg := werr.Error()

	key := instructionKey(inst)
	if e.failedKey != key {
		// Lần thất bại đầu: thử lại đúng chỉ thị một lần (vòng sau Route tính lại, facts điều khiển vốn idempotent).
		e.failedKey = key
		return false
	}
	e.failedKey = ""
	facts := e.failureFacts("worker_failure", inst, werr)
	decision, err := runObservedDecision(e.observer, "phán quyết thất bại", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("worker_failure", inst, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindWorkerFailure, "Phán quyết thất bại không khả dụng, đã tạm dừng chờ can thiệp thủ công: "+msg+contentFilterAdvice(werr))
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		e.mu.Lock()
		e.next = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		e.deferGateForNext = false
		e.mu.Unlock()
		return false
	default: // abort
		e.pauseStuck(notify.KindWorkerFailure, inst, "Phán quyết thất bại: "+decision.Reason+contentFilterAdvice(werr))
		return true
	}
}

// pauseStuck khi engine từ bỏ một chỉ thị thì tạm dừng: chương sửa lại phải được đưa ra khỏi hàng đợi trước rồi mới dừng.
// Chỉ dùng khi engine đã kết luận chỉ thị này không thể đi tiếp (ngắt cứng bế tắc, abort từ phán quyết bế tắc/thất bại);
// các lỗi hạ tầng như phán quyết không khả dụng vẫn đi theo pauseWithNotify - đó là vấn đề bên ngoài, không nên đánh đổi bằng
// một chương sửa lại.
func (e *engine) pauseStuck(kind string, inst *flow.Instruction, body string) {
	if e.dropStuckRewrite(inst) {
		body += fmt.Sprintf("; chương %d đã được lấy ra khỏi hàng đợi sửa lại (giữ nguyên bản hoàn thiện trước đó), việc sáng tác tiếp theo sẽ đi từ các chương sau", inst.Chapter)
	}
	e.pauseWithNotify(kind, body)
}

// dropStuckRewrite đưa chương sửa lại bị kẹt ra khỏi hàng đợi. PendingRewrites là fact đã được lưu;
// nếu engine bỏ chỉ thị này mà không lấy ra khỏi hàng đợi, sau khi khởi động lại sẽ phát lại ngay đúng chỉ thị chết đó,
// khiến cả cuốn sách bị khóa vĩnh viễn (issue #110).
// Trả về true nghĩa là đã lấy ra thật.
func (e *engine) dropStuckRewrite(inst *flow.Instruction) bool {
	if inst == nil || inst.Agent != "writer" || inst.Chapter <= 0 {
		return false
	}
	progress, err := e.store.Progress.Load()
	if err != nil || progress == nil || !slices.Contains(progress.PendingRewrites, inst.Chapter) {
		return false
	}
	if err := e.store.Progress.CompleteRewrite(inst.Chapter); err != nil {
		slog.Warn("gỡ chương sửa lại bị kẹt khỏi hàng đợi thất bại", "module", "engine", "chapter", inst.Chapter, "err", err)
		return false
	}
	return true
}

// discardNonSemanticDeadlockAttempt hủy lần thử ngữ nghĩa mà trackDeadlock đã ghi trước
// cho lần phân công này. Chỉ loại bỏ các kiểu lỗi ổn định khi lời gọi mô hình chưa thực thi đầy đủ;
// content_filter vẫn giữ trong đường tự hồi phục vốn có, còn max_turns, stop_guard, hủy, v.v. là
// các trường hợp thật sự không tiến triển nên vẫn được tính.
func (e *engine) discardNonSemanticDeadlockAttempt(inst *flow.Instruction, werr error) {
	if inst == nil || !isNonSemanticWorkerFailure(werr) {
		return
	}
	key := instructionKey(inst)
	if e.lastKey != key || e.repeats <= 0 {
		return
	}
	e.repeats--
	if e.repeats == 0 {
		e.lastKey = ""
	}
}

// isNonSemanticWorkerFailure chỉ nhận diện lỗi mà "lần thực thi mô hình này không tạo ra ngữ nghĩa có thể đánh giá".
// Ưu tiên dựa vào hợp đồng chuỗi lỗi của agentcore; khi chuỗi lỗi bị nhà cung cấp làm phẳng thì tái dùng phân loại từ log.
func isNonSemanticWorkerFailure(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, agentcore.ErrContextOverflow) || errors.Is(err, agentcore.ErrStreamPartial) {
		return true
	}
	providerErr := agentcore.ClassifyProvider(err)
	classified := errors.Is(providerErr, agentcore.ErrProviderStreamIdle) ||
		errors.Is(providerErr, agentcore.ErrProviderQuota) ||
		errors.Is(providerErr, agentcore.ErrProviderRateLimit) ||
		errors.Is(providerErr, agentcore.ErrProviderTimeout) ||
		errors.Is(providerErr, agentcore.ErrProviderAuth) ||
		errors.Is(providerErr, agentcore.ErrProviderNetwork) ||
		errors.Is(providerErr, agentcore.ErrProviderOverloaded)
	return classified || errorKind(err, err.Error()) == "overloaded"
}

func instructionKey(inst *flow.Instruction) string {
	if inst == nil {
		return ""
	}
	return inst.Agent + "\x00" + inst.Task
}

func (e *engine) rememberWorkerError(inst *flow.Instruction, workerErr error) {
	if workerErr == nil || inst == nil {
		e.lastWorkerErrorKey, e.lastWorkerError = "", nil
		return
	}
	e.lastWorkerErrorKey, e.lastWorkerError = instructionKey(inst), workerErr
}

func (e *engine) workerErrorFor(inst *flow.Instruction) error {
	if e.lastWorkerErrorKey != instructionKey(inst) {
		return nil
	}
	return e.lastWorkerError
}

// contentFilterAdvice thêm cho lần tạm dừng vì kiểm duyệt nội dung một lối thoát người dùng có thể làm được.
// Kiểm duyệt là hộp đen của nhà cung cấp, không thể tiền kiểm / né tránh, thứ có thể làm chỉ là đưa quyết định
// về tay người dùng; bản thân việc chặn không ngắt cứng sớm - đổi ngữ cảnh rồi phân công lại có tỷ lệ tự hồi phục thật
// (thực đo ch21-24), đi hết "thử lại miễn phí → trọng tài" rồi mới tạm dừng.
func contentFilterAdvice(werr error) string {
	if !errors.Is(werr, agentcore.ErrProviderContentFilter) {
		return ""
	}
	return "。Đây là chặn kiểm duyệt nội dung của nhà cung cấp (không phải lỗi cục bộ), có thể chọn: /model chuyển sang nhà cung cấp không có lớp kiểm duyệt rồi nhập \"tiếp tục\"; hoặc sửa cách diễn đạt của bản nháp chương này (drafts/) rồi tiếp tục; thử lại nguyên trạng rất có thể vẫn bị chặn"
}

// errInvalidWriteTarget đánh dấu mục tiêu viết không hợp lệ bị chặn ở phần kiểm tra trước runWorker,
// để chuỗi lỗi và facts của Arbiter giữ được ngữ nghĩa ổn định; việc thử lại hay đổi hướng vẫn do luồng lỗi thống nhất quyết định.
var errInvalidWriteTarget = errors.New("mục tiêu viết không hợp lệ")

func (e *engine) failureFacts(kind string, inst *flow.Instruction, workerErr error) arbiter.FailureFacts {
	f := arbiter.FailureFacts{Kind: kind, Agent: inst.Agent, Task: inst.Task, Repeats: e.repeats}
	if workerErr != nil {
		f.Error = workerErr.Error()
		f.ErrorKind = errorKind(workerErr, f.Error)
		if f.ErrorKind == "" {
			f.ErrorKind = "unknown"
		}
	}
	missing, err := e.store.FoundationMissing()
	if err != nil {
		f.FactWarnings = append(f.FactWarnings, "Đọc trạng thái thiết lập nền thất bại: "+err.Error())
	} else {
		f.FoundationGap = missing
	}
	p, err := e.store.Progress.Load()
	if err != nil {
		f.FactWarnings = append(f.FactWarnings, "Đọc tiến độ sáng tác thất bại: "+err.Error())
	}
	if p != nil {
		f.Phase = string(p.Phase)
		f.NextChapter = p.NextChapter()
		f.PendingQueue = p.PendingRewrites
	}
	return f
}

func (e *engine) recordFailureDecision(kind string, inst *flow.Instruction, facts arbiter.FailureFacts, d arbiter.FailureDecision, derr error) {
	rec := storepkg.DecisionRecord{Kind: kind, Decider: "arbiter", Input: inst.Agent + ": " + inst.Task, Reason: d.Reason}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(d); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("ghi xuống đĩa kiểm toán phán quyết thất bại", "module", "engine", "kind", kind, "err", err)
	}
}

// applyPendingOps thực thi các hành động điều khiển của can thiệp ở ranh giới vòng lặp;
// đợi cho vòng lặp trống - khi hỏi lại đồng bộ (reconsult) trong lúc áp dụng có thể thêm hành động mới,
// nên phải xử lý hết ngay trong ranh giới này, nếu không sẽ có thêm một worker được phân công ở giữa
// (can thiệp phải có hiệu lực trước các bước sáng tác sau).
// Trả về việc có hold+dispatch cần cho cặp phân công chạy trước hay không; trong trường hợp đó bên gọi tạm hoãn kiểm tra Gate.
func (e *engine) applyPendingOps(ctx context.Context) (deferGate bool) {
	for {
		e.mu.Lock()
		ops := e.pending
		e.pending = nil
		e.mu.Unlock()
		if len(ops) == 0 {
			return deferGate
		}
		for _, op := range ops {
			pairedHoldDispatch := op.hold != nil && !op.hold.Cancel && op.dispatch != nil
			err := e.applyControlOp(ctx, op)
			if err != nil {
				// Lỗi lưu bền trạng thái: host đã xóa PendingSteer theo "enqueue thành công",
				// ở đây sẽ ghi lại toàn bộ can thiệp, để khi khôi phục/tiếp tục thì phán quyết lại
				// và thử lại (hành động idempotent + hỏi lại theo facts mới).
				if op.text != "" {
					if serr := e.store.RunMeta.SetPendingSteer(op.text); serr != nil {
						slog.Warn("khôi phục lưu can thiệp thất bại", "module", "engine", "err", serr)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "Thực thi hành động can thiệp thất bại, đã được giữ lại; khi khôi phục/tiếp tục sẽ tự thử lại"})
			} else if pairedHoldDispatch && e.nextDefersGate() {
				// Chỉ khi cả hold lẫn phân công đi kèm đều được ghi xuống đĩa thành công, mới được phép bỏ qua Gate lần này.
				// Nếu ghi hold thất bại hoặc phân công bị bỏ vì facts đã cũ, thì tiếp tục bỏ qua sẽ khiến Worker không được bảo vệ tiến lên.
				deferGate = true
			}
		}
	}
}

// applyControlOp thực thi một hành động điều khiển đơn lẻ (hold ghi thẳng RunMeta, reopen gọi kernel công cụ, dispatch thì đối soát trước).
// Khi engine không chạy, host gọi trực tiếp ở đường can thiệp; trả về lỗi lưu bền đầu tiên (dựa vào đó bên gọi quyết định có
// giữ PendingSteer để phát lại khi khôi phục hay không).
func (e *engine) applyControlOp(ctx context.Context, op controlOp) error {
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	if op.dispatch != nil {
		// Expect phải được kiểm tra trước khi ghi xuống đĩa các hành động đi kèm như hold. Nếu không, sau khi phân công hết hạn,
		// hold cũ sẽ còn sót lại và xung đột với hold được phán quyết lại theo facts mới, cuối cùng chỉ tạm dừng mà bỏ sót thay đổi.
		fresh, err := arbiter.CollectInterventionFacts(e.store)
		if err != nil {
			return fmt.Errorf("làm mới facts can thiệp: %w", err)
		}
		if fresh.Phase != op.facts.Phase || fresh.Flow != op.facts.Flow ||
			fresh.QueueHead() != op.facts.QueueHead() {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "Lệnh phân công của phán quyết đã lỗi thời (facts đã tiến), sẽ phán quyết lại theo facts mới nhất"})
			e.recordStale(op)
			if op.text != "" && e.reconsult != nil {
				// Hỏi lại đồng bộ: can thiệp phải có hiệu lực trước các bước sáng tác sau - bất đồng bộ
				// sẽ làm engine lại phân công một worker trước khi phán quyết mới kịp áp dụng.
				// Hành động mới sẽ được applyPendingOps xử lý hết ở ranh giới này.
				e.reconsult(op.text)
			}
			return nil
		}
	}
	if op.hold != nil {
		if op.hold.Cancel {
			meta, err := e.store.RunMeta.Load()
			if err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Đọc tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
				return err
			}
			if meta != nil && meta.AdvanceHold != nil {
				if err := e.store.RunMeta.ClearAdvanceHold(*meta.AdvanceHold); err != nil {
					e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Hủy tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
					return err
				}
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã hủy tạm dừng một lần", Level: "info"})
		} else {
			hold := domain.AdvanceHold{After: op.hold.After, TargetChapter: op.hold.TargetChapter, Reason: op.hold.Reason}
			if err := e.store.RunMeta.SetAdvanceHold(hold); err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Đặt tạm dừng một lần thất bại: " + err.Error(), Level: "error"})
				return err // khi hold chưa ghi xuống đĩa thì không được thực thi dispatch đi kèm
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Đã đặt tạm dừng một lần: " + op.hold.Reason, Level: "info"})
		}
	}
	if op.reopen != nil {
		args, _ := json.Marshal(map[string]any{"chapters": op.reopen.Chapters, "reason": op.reopen.Reason})
		if _, err := tools.NewReopenBookTool(e.store).Execute(ctx, args); err != nil {
			e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "Mở lại việc sửa lại toàn sách thất bại: " + err.Error(), Level: "error"})
			fail(err)
		} else {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
				Summary: fmt.Sprintf("Đã mở lại sửa toàn bộ sách: các chương %v đã vào hàng đợi", op.reopen.Chapters), Level: "info"})
		}
	}
	if op.dispatch != nil {
		// Expect đã được kiểm tra trước mọi lần ghi trạng thái đi kèm. CheckpointSeq chỉ giữ để kiểm toán,
		// không tham gia đối soát: khi can thiệp đến, worker đa phần đang chạy, seq tất nhiên sẽ tiến lên.
		e.mu.Lock()
		// Cửa sổ đã biết (ranh giới best-effort, xem engine-arbiter.md làm rõ mục 3): phân công từ đây chỉ tồn tại trong bộ nhớ;
		// nếu worker bị kill cứng (kill -9, defer không chạy) trước khi khởi động thì ý định phân công lần này sẽ mất -
		// thoát bình thường / Abort sẽ được chạy bù từ defer của run để khôi phục PendingSteer.
		e.next = &flow.Instruction{Agent: op.dispatch.Agent, Task: interventionDispatchTask(op.dispatch.Task, op.text), Reason: "phán quyết can thiệp của người dùng"}
		e.deferGateForNext = op.hold != nil && !op.hold.Cancel
		e.mu.Unlock()
	}
	return firstErr
}

// interventionDispatchTask giữ nguyên can thiệp gốc của người dùng, tránh việc Arbiter vô tình mở rộng
// mục tiêu sửa đổi khi diễn giải lại tác vụ. Phía sau có thể đọc bối cảnh rộng hơn để đánh giá,
// nhưng chỉ được coi văn bản gốc là nguồn ủy quyền cho hành động.
func interventionDispatchTask(task, original string) string {
	task = strings.TrimSpace(task)
	if strings.TrimSpace(original) == "" {
		return task
	}
	return task + "\n\nCan thiệp gốc của người dùng (nguồn ủy quyền duy nhất cho lần sửa đổi này; bối cảnh chỉ dùng để hiểu, không được mở rộng mục tiêu hay phạm vi):\n" + original
}

func (e *engine) recordStale(op controlOp) {
	rec := storepkg.DecisionRecord{Kind: "decision_stale", Decider: "engine", Input: op.text}
	if data, err := json.Marshal(op.facts); err == nil {
		rec.Facts = data
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("ghi stale thất bại", "module", "engine", "err", err)
	}
}

// pauseWithNotify engine tự tạm dừng (ngắt cứng bế tắc / phán quyết thất bại abort): thông báo ngoài màn hình + đi theo
// ngữ nghĩa tạm dừng thống nhất của host (onPause → abortWithEvent: lifecycle=paused + sự kiện trong màn hình + cancel ctx).
func (e *engine) pauseWithNotify(kind, body string) {
	e.notify(kind, "warn", "ainovel: Engine tạm dừng", body)
	if e.onPause != nil {
		e.onPause(body)
		return
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: body, Level: "warn"})
	e.abort()
}

// completionSummary là báo cáo kết thúc xác định cho hoàn bản, không tốn lời gọi LLM.
func completionSummary(progress domain.Progress, book domain.BookMetadata) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tác phẩm \"%s\" đã hoàn thành: tổng %d chương, %d chữ", book.Title, len(progress.CompletedChapters), progress.TotalWordCount)
	return b.String()
}
