package guard

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// subagentMaxConsecutiveBlocks escalate thành kết thúc sau N lần chặn liên tiếp, tránh mô hình yếu lặp vô hạn.
const subagentMaxConsecutiveBlocks = 3

// BlockHook là callback audit của StopGuard: gọi đồng bộ mỗi lần chặn/escalate. Host dùng nó để đưa sự kiện chặn
// ra TUI Luồng sự kiện và thông báo ngoài màn hình — nếu không, việc chặn chỉ vào log, người dùng trên UI chỉ thấy
// "khựng + token chạy nhanh", không biết hệ thống đang tự phục hồi hay quay rỗng (issue #75).
// Callback không tham gia quyết định guard. Giá trị reason:
//   - "blocked"    đã inject thông điệp nhắc nhở, mô hình sẽ tiếp tục tiến triển
//   - "escalated"  quay rỗng liên tiếp quá giới hạn, run lượt này kết thúc và trả về tầng trên
//   - "hard_stop"  provider từ chối (safety/content_filter), kết thúc ngay
type BlockHook func(agent, reason string, consecutive int32)

// hardStopReasons là các lý do từ chối phía provider không thể khôi phục bằng thông điệp nhắc nhở. Inject
// "phải commit" không hiệu quả với chúng, ngược lại mỗi lần tạo chi phí token của một lần gọi LLM đầy đủ,
// và sau khi cuối cùng escalate, Engine chạy lại toàn bộ nhiệm vụ Worker, cộng dồn lãng phí nhiều lần
// (thực đo khi ch02 đụng safety, một lần viết chương tạo 3 lần dispatch lại, 17 lần gọi LLM, tỷ lệ trúng
// giảm từ 50% xuống 2.8%).
//
// Chú ý StopReasonError / StopReasonAborted không cần liệt kê: agentcore trong
// loop.go kết thúc run trực tiếp khi nhận hai stop reason này, hoàn toàn không gọi StopGuard.
// Ở đây chỉ liệt kê các ngữ nghĩa từ chối của provider thật sự đi tới StopGuard.
var hardStopReasons = map[agentcore.StopReason]struct{}{
	"safety":         {},
	"content_filter": {},
}

// newCheckpointDeltaGuard dựng một StopGuard:
// nếu sau baseline chưa xuất hiện checkpoint của step chỉ định thì từ chối end_turn.
// baseline do bên gọi bắt tại thời điểm factory, bảo đảm ngữ nghĩa per-run đúng.
//
// blockMsg nhận tập checkpoint step đã quan sát sau baseline, lắp theo tiến độ thực tế
// thông điệp nhắc nhở — thông điệp tĩnh sẽ gây hiểu sai trong cảnh "bản thân tool bắt buộc liên tục báo lỗi" (nhắc mô hình gọi một
// tool đang thất bại, xem #75).
//
// Ngữ nghĩa đếm là "có tiến triển thì reset": nếu giữa hai lần chặn xuất hiện
// bất kỳ checkpoint mới nào (draft/check lại, v.v.) thì xem như mô hình đang tiến triển, consecutive về 0;
// chỉ quay rỗng liên tiếp mà không có artifact nào mới tích lũy và escalate kết thúc.
func newCheckpointDeltaGuard(st *store.Store, agentName string, requiredSteps []string, blockMsg func(seen map[string]struct{}) string, onBlock BlockHook) agentcore.StopGuard {
	var baseline int64
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		baseline = cp.Seq
	}
	need := make(map[string]struct{}, len(requiredSteps))
	for _, s := range requiredSteps {
		need[s] = struct{}{}
	}
	var consecutive atomic.Int32
	var lastBlockSeq atomic.Int64 // Seq checkpoint mới nhất quan sát ở lần chặn trước; -1 nghĩa là chưa từng chặn
	lastBlockSeq.Store(-1)
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		// Lỗi không thể khôi phục: escalate trực tiếp, không lãng phí một lần nhắc.
		if _, hard := hardStopReasons[info.Message.StopReason]; hard {
			slog.Error("subagent stop_guard phát hiện stop không thể khôi phục, escalate ngay",
				"module", "agent.guard", "agent", agentName,
				"turn", info.TurnIndex, "stop_reason", info.Message.StopReason)
			if onBlock != nil {
				onBlock(agentName, "hard_stop", consecutive.Load())
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		// Quét ngược các checkpoint sau baseline, thu các step đã xuất hiện (dùng chung cho phán định cho qua + thông điệp tiến độ).
		// Checkpoint mới nằm ở cuối; gặp <= baseline thì có thể break.
		all := st.Checkpoints.All()
		latestSeq := baseline
		seen := make(map[string]struct{})
		for i := len(all) - 1; i >= 0; i-- {
			cp := all[i]
			if cp.Seq <= baseline {
				break
			}
			if cp.Seq > latestSeq {
				latestSeq = cp.Seq
			}
			seen[cp.Step] = struct{}{}
		}
		for s := range need {
			if _, ok := seen[s]; ok {
				consecutive.Store(0)
				return agentcore.StopDecision{Allow: true}
			}
		}
		// Có artifact mới ghi xuống kể từ lần chặn trước = mô hình đang tiến triển (ví dụ được nhắc rồi draft lại và thử kết thúc),
		// reset bộ đếm; escalate chỉ nên phạt quay rỗng không tiến triển, không gom mọi lần chặn của cả run rồi hủy bỏ.
		if prev := lastBlockSeq.Load(); prev >= 0 && latestSeq > prev {
			consecutive.Store(0)
		}
		lastBlockSeq.Store(latestSeq)
		n := consecutive.Add(1)
		if n > subagentMaxConsecutiveBlocks {
			slog.Error("subagent stop_guard chặn liên tiếp quá giới hạn, escalate thành kết thúc",
				"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
			if onBlock != nil {
				onBlock(agentName, "escalated", n)
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		slog.Warn("subagent stop_guard chặn end_turn",
			"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
		if onBlock != nil {
			onBlock(agentName, "blocked", n)
		}
		return agentcore.StopDecision{Allow: false, InjectMessage: blockMsg(seen)}
	}
}

// staticBlockMsg chuyển văn bản cố định sang chữ ký blockMsg (artifact của architect/editor là một tool ghi xuống,
// không có tiến độ nhiều bước, nhắc tĩnh là đủ).
func staticBlockMsg(msg string) func(map[string]struct{}) string {
	return func(map[string]struct{}) string { return msg }
}

// NewWriterStopGuard yêu cầu writer sinh ít nhất một commit_chapter thành công trong lượt này.
// Thông điệp nhắc nhở lắp theo tiến độ step đã ghi xuống: writer là subagent duy nhất có chuỗi tool nhiều bước,
// "phải gọi commit_chapter" tĩnh gây hiểu sai khi thiếu bước trước hoặc bản thân commit báo lỗi.
func NewWriterStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "writer", []string{"commit"}, writerBlockMsg, onBlock)
}

// writerBlockMsg phán định writer kẹt ở bước nào theo checkpoint step đã xuất hiện trong lượt này.
// Tên step tương ứng với giá trị ghi xuống của từng tool: plan / draft / edit / consistency_check / commit.
func writerBlockMsg(seen map[string]struct{}) string {
	_, hasDraft := seen["draft"]
	_, hasEdit := seen["edit"]
	_, hasCheck := seen["consistency_check"]
	switch {
	case !hasDraft && !hasEdit:
		return "Cấm kết thúc: lượt này chưa ghi xuống bất kỳ nội dung chính nào. Hãy hoàn thành chương này theo thứ tự plan_chapter → draft_chapter → check_consistency → commit_chapter; nội dung chính chỉ xuất trong chat xem như bị mất, bắt buộc phải ghi xuống và submit bằng tool."
	case !hasCheck:
		return "Cấm kết thúc: nội dung chính đã ghi xuống nhưng chưa kết thúc. Hãy gọi check_consistency để kiểm tra nhất quán trước, rồi gọi commit_chapter để submit chương này. draft_chapter / edit_chapter chỉ lưu bản nháp, không tính là hoàn tất."
	default:
		return "Cấm kết thúc: chương này chỉ còn thiếu submit bằng commit_chapter. Hãy gọi commit_chapter ngay; nếu nó trả lỗi, xử lý theo thông tin lỗi trước (kiểm tra số chương, bổ sung hành động tiền đề theo gợi ý) rồi retry submit, đừng kết thúc khi chưa submit."
	}
}

// NewArchitectStopGuard yêu cầu architect ghi xuống ít nhất một artifact lập kế hoạch trong lượt này.
func NewArchitectStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "architect",
		[]string{
			"book", "premise", "outline", "layered_outline", "characters", "world_rules",
			"foundation_audit", "expand_arc", "append_volume", "update_compass", "complete_book", "revise_outline", "resolve_outline_feedback",
		},
		staticBlockMsg("Bạn phải gọi save_book, save_foundation, revise_outline, resolve_outline_feedback hoặc audit_foundation để ghi đầu ra xuống rồi mới được kết thúc. Chỉ xuất văn bản Markdown/JSON xem như bị mất."),
		onBlock,
	)
}

// NewEditorStopGuard yêu cầu editor trong lượt này phải ghi xuống artifact khớp "nhiệm vụ" rồi mới được kết thúc.
//
// Nhận biết nhiệm vụ: khi được giao tạo tóm tắt, chỉ save_review (rà soát) không tính là hoàn tất — phải sinh bản tóm tắt tương ứng.
// Nếu không, editor "được giao tạo tóm tắt arc nhưng rà soát trước" sẽ thỏa tiêu chí nới lỏng cũ và kết thúc sớm, tóm tắt arc không bao giờ ghi xuống
// (kết hợp dispatcher khử trùng lặp im lặng từng gây vòng lặp vô hạn ở arc khung trong quyển; xem outline-exhaustion-livelock).
// Thoát bằng tool trạng thái cuối cũng tham vấn StopGuard (kiểm thử contract TestContract_TerminalToolExitConsultsStopGuard),
// nên hard stop save_review trong build.go là an toàn: khi editor trong nhiệm vụ tóm tắt rà soát trước, guard này sẽ
// từ chối lần thoát đó và nhắc nhở cho tới khi bản tóm tắt tương ứng được ghi xuống.
func NewEditorStopGuard(st *store.Store, task string, onBlock BlockHook) agentcore.StopGuard {
	switch {
	case strings.Contains(task, "save_volume_summary") || strings.Contains(task, "tóm tắt quyển"):
		return newCheckpointDeltaGuard(st, "editor", []string{"volume_summary"},
			staticBlockMsg("Nhiệm vụ lần này là tạo tóm tắt quyển: bạn phải gọi save_volume_summary để ghi xuống rồi mới được kết thúc; save_review rà soát không tính là hoàn tất."), onBlock)
	case strings.Contains(task, "save_arc_summary") || strings.Contains(task, "tóm tắt arc"):
		return newCheckpointDeltaGuard(st, "editor", []string{"arc_summary"},
			staticBlockMsg("Nhiệm vụ lần này là tạo tóm tắt arc: bạn phải gọi save_arc_summary để ghi xuống rồi mới được kết thúc; save_review rà soát không tính là hoàn tất."), onBlock)
	default:
		// Nhiệm vụ rà soát hoặc tạm thời: bất kỳ review/tóm tắt nào ghi xuống là đủ (giữ hành vi nới lỏng hiện có).
		return newCheckpointDeltaGuard(st, "editor",
			[]string{"review", "arc_summary", "volume_summary"},
			staticBlockMsg("Bạn phải gọi một trong save_review / save_arc_summary / save_volume_summary để ghi kết quả xuống rồi mới được kết thúc."), onBlock)
	}
}
