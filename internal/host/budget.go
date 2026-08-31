package host

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// Máy trạng thái ngân sách: chỉ tiến một chiều, mỗi lần chuyển trạng thái chỉ kích hoạt đúng một hiệu ứng phụ, không quay lui.
// Tăng ngân sách = người dùng cấp quyền lại = đổi cấu hình rồi khởi động lại/Host mới; không quay lui trạng thái trong chính instance này.
const (
	budgetNormal      int32 = iota // Chưa tới ngưỡng cảnh báo
	budgetWarned                   // Đã phát cảnh báo, chưa vượt ngưỡng
	budgetStopPending              // Đã vượt ngưỡng, chờ dừng ở ranh giới subagent
	budgetStopped                  // Đã thực hiện dừng
)

// BudgetSentinel giám sát chi phí tích lũy, thực thi chính sách ngân sách của người dùng (khối config budget).
//
// Định vị đúng kiến trúc (architecture.md §8.3/§10): không đánh giá hành vi mô hình — dừng khi vượt ngưỡng
// tương đương việc người dùng tự tay Abort vào đúng khoảnh khắc đó; Host chỉ thay mặt thực thi một chỉ thị đã
// được ký sẵn. Nó ảnh hưởng đến luồng điều khiển, nên không phải observer, mà là một thành phần chính sách của
// Host ngang hàng với flow.Dispatcher; tầng Route/tool không biết đến nó.
//
// Thời điểm dừng: mặc định ở ranh giới subagent (Host gọi đồng bộ HandleBoundary), không làm phí các chương
// in-flight; khi hardStop=true thì vượt ngưỡng là dừng ngay. Xử lý ranh giới diễn ra trước khi flow.Dispatcher
// phát bước tiếp theo, tầng Route/tool không biết đến ngân sách.
type BudgetSentinel struct {
	limit     float64
	warnRatio float64
	hardStop  bool

	costNow func() float64              // Chi phí tích lũy hiện tại (bao bọc usage.Totals; có thể chèn stub để test)
	abort   func(reason string)         // Bao bọc dừng Host (kèm sự kiện lý do)
	report  func(level, summary string) // Cửa phát cảnh báo (emitEvent + notify, do Host chèn vào)

	state atomic.Int32

	// Phát hiện vùng mù tính phí: các model không có giá trong registry và provider không tự báo cost
	// sẽ có mỗi bước ghi sổ tăng thêm là $0, làm ngân sách âm thầm vô hiệu.
	// Dựa trên "nhiều bước liên tiếp tăng bằng 0" thay vì total==0 — cách sau không bắt được trường hợp
	// giữa chừng /model chuyển sang model không có giá (total dừng ở một giá trị lịch sử khác 0 nhưng
	// không tăng nữa).
	// Model miễn phí cũng rơi vào đây, nên cảnh báo "ngân sách sẽ không kích hoạt" cũng đúng với chúng.
	lastTotal   atomic.Uint64 // math.Float64bits(chi phí tích lũy ở lần gọi trước)
	zeroStreak  atomic.Int32
	blindWarned atomic.Bool
}

// blindZeroStreak là số lần ghi sổ tăng bằng 0 liên tiếp trước khi cảnh báo. Model tính giá bình thường
// thì mỗi bước tăng đều phải > 0 (cost là tổng float, không làm tròn), chọn 5 chỉ để tránh nhiễu cực đoan,
// không phải ngưỡng chiến lược có thể tinh chỉnh.
const blindZeroStreak = 5

// NewBudgetSentinel tạo bộ canh ngân sách; khi chính sách chưa bật thì trả về nil (mọi phương thức đều an toàn với nil).
func NewBudgetSentinel(cfg bootstrap.BudgetConfig, costNow func() float64, abort func(reason string), report func(level, summary string)) *BudgetSentinel {
	if !cfg.Enabled() {
		return nil
	}
	return &BudgetSentinel{
		limit:     cfg.BookUSD,
		warnRatio: cfg.WarnRatio,
		hardStop:  cfg.HardStop,
		costNow:   costNow,
		abort:     abort,
		report:    report,
	}
}

// OnCost được UsageTracker gọi sau mỗi lần ghi sổ, kèm theo tổng chi phí tích lũy mới nhất (ngoài lock).
// Một lần gọi có thể đi qua liền hai cấp (normal→warned→stopPending), và mỗi hiệu ứng phụ sẽ kích hoạt một lần.
func (s *BudgetSentinel) OnCost(total float64) {
	if s == nil {
		return
	}
	if prev := s.lastTotal.Swap(math.Float64bits(total)); total == math.Float64frombits(prev) {
		if s.zeroStreak.Add(1) >= blindZeroStreak && s.blindWarned.CompareAndSwap(false, true) {
			s.report("warn", fmt.Sprintf("Vùng mù ngân sách: ghi sổ liên tiếp nhưng chi phí tích lũy vẫn đứng ở $%.2f và không tăng nữa (model hiện tại không có giá trong registry và provider không tự báo cost, hoặc là model miễn phí) — ngưỡng ngân sách sẽ không kích hoạt", total))
		}
	} else {
		s.zeroStreak.Store(0)
	}
	if total >= s.limit*s.warnRatio && s.state.CompareAndSwap(budgetNormal, budgetWarned) {
		s.report("warn", fmt.Sprintf("Cảnh báo ngân sách: đã chi $%.2f, đạt %.0f%% của ngân sách $%.2f", total, s.limit, s.warnRatio*100))
	}
	if total >= s.limit && s.state.CompareAndSwap(budgetWarned, budgetStopPending) {
		if s.hardStop {
			s.report("error", fmt.Sprintf("Hết ngân sách: đã chi $%.2f, vượt ngân sách $%.2f, dừng ngay", total, s.limit))
			s.stop(total)
			return
		}
		s.report("error", fmt.Sprintf("Hết ngân sách: đã chi $%.2f, vượt ngân sách $%.2f, sẽ dừng sau khi tác vụ subagent hiện tại kết thúc", total, s.limit))
	}
}

// HandleEvent thực thi việc dừng đang chờ ở ranh giới subagent. Subscription phải được đăng ký trước Dispatcher.
// Không bỏ qua IsError — trả về lỗi cũng vẫn là một ranh giới, việc dừng không nên bị trì hoãn chỉ vì subagent thất bại.
func (s *BudgetSentinel) HandleEvent(ev agentcore.Event) {
	if s == nil {
		return
	}
	if ev.Type != agentcore.EventToolExecEnd || ev.Tool != "subagent" {
		return
	}
	s.HandleBoundary()
}

func (s *BudgetSentinel) HandleBoundary() bool {
	if s == nil || s.state.Load() != budgetStopPending {
		return false
	}
	s.stop(s.costNow())
	return true
}

func (s *BudgetSentinel) stop(total float64) {
	if s.state.CompareAndSwap(budgetStopPending, budgetStopped) {
		s.abort(fmt.Sprintf("Dừng do ngân sách: đã chi $%.2f, vượt ngân sách $%.2f; có thể tiếp tục chạy lại sau khi tăng budget.book_usd", total, s.limit))
	}
}

// Refuse kiểm tra tiền đề trước khi khởi động: nếu đã vượt ngân sách thì trả về lỗi từ chối (được gọi ở đường phục hồi Start/Resume/Continue).
// Người dùng tăng ngân sách = cấp quyền lại, với cấu hình mới Refuse sẽ tự nhiên cho qua.
func (s *BudgetSentinel) Refuse() error {
	if s == nil {
		return nil
	}
	if cost := s.costNow(); cost >= s.limit {
		return fmt.Errorf("Cuốn sách này đã chi $%.2f, đạt trần ngân sách $%.2f; vui lòng tăng cấu hình budget.book_usd rồi thử lại", cost, s.limit)
	}
	return nil
}

// Limit trả về trần ngân sách (dùng cho UI); nếu chưa bật thì trả về 0.
func (s *BudgetSentinel) Limit() float64 {
	if s == nil {
		return 0
	}
	return s.limit
}
