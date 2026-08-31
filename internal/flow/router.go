// Package flow triển khai định tuyến theo vai trò: Host dựa trên fact để quyết định worker kế tiếp và nhiệm vụ cần chạy.
//
// Nguyên tắc thiết kế:
//   - Route là hàm thuần: nhận State, trả *Instruction. Không IO, không gọi Store, dễ kiểm thử.
//   - State do LoadState dựng từ Store ở bên ngoài, gom đủ fact mà bộ định tuyến cần.
//   - Trả nil là hợp lệ: nghĩa là hiện không có chỉ thị worker nào suy ra chắc chắn từ fact;
//     Engine sẽ xử lý theo trạng thái cuối, phán quyết khởi động bổ sung hoặc chờ người dùng can thiệp.
//
// Router bao phủ các quyết định dạng tra bảng: bước tiếp theo mỗi chương, hậu xử lý cuối arc và hàng đợi rewrite.
// Router không bao phủ quyết định cần hiểu ngữ nghĩa: chọn planner, xử lý Steer của người dùng hoặc viết tổng kết.
package flow

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// plannerForTier suy ra planner từ cấp lập kế hoạch đã ghi: short dùng architect_short,
// mid/long dùng architect_long, cùng chuẩn chọn vai trò với Arbiter khởi động.
func plannerForTier(tier domain.PlanningTier) string {
	if tier == domain.PlanningTierShort {
		return "architect_short"
	}
	return "architect_long"
}

// Instruction chỉ cho Engine worker cần chạy trực tiếp và nhiệm vụ tương ứng.
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // mô tả nhiệm vụ gửi cho subagent
	Reason  string // lý do định tuyến, dùng cho event, log và phán quyết lỗi
	Chapter int    // chương liên quan tới writer task; 0 nghĩa là không gắn với chương cụ thể
}

type AggregateKind string

const (
	AggregateArcReview     AggregateKind = "arc_review"
	AggregateArcSummary    AggregateKind = "arc_summary"
	AggregateVolumeSummary AggregateKind = "volume_summary"
	AggregateGlobalReview  AggregateKind = "global_review"
)

type AggregateRefresh struct {
	Kind         AggregateKind
	Volume       int
	Arc          int
	StartChapter int
	EndChapter   int
}

// State là input của Route: mọi fact phải được khai báo rõ ở đây; Route không đọc Store.
type State struct {
	Progress *domain.Progress

	// Số chương lớn nhất trong tập chương đã hoàn thành; 0 nghĩa là chưa bắt đầu viết.
	LastCompleted int

	// Ranh giới arc của chương trước. Khi IsArcEnd=false, các trường còn lại không có ý nghĩa.
	// Khi LastCompleted=0 hoặc không ở chế độ Layered thì phải là nil.
	ArcBoundary *storepkg.ArcBoundary

	// Ba fact hậu xử lý cuối arc: review, tóm tắt arc và tóm tắt quyển đã hoàn tất chưa.
	HasArcReview     bool
	HasArcSummary    bool
	HasVolumeSummary bool

	// Các mục thiết lập nền tảng còn thiếu trong giai đoạn lập kế hoạch.
	FoundationMissing []string

	// Cấp lập kế hoạch đã ghi bằng save_foundation scale.
	// Rỗng nghĩa là lần lập kế hoạch đầu chưa ghi thiết lập nào, nên chưa thể suy ra planner.
	PlanningTier domain.PlanningTier

	// Sách không phân tầng: chương gần nhất đã hoàn thành có scope=global review chưa.
	// Chỉ có ý nghĩa tại điểm domain.ShouldReview; sách phân tầng luôn false.
	HasGlobalReview bool

	// Số sửa đổi bên ngoài phải được Architect lan truyền vào kế hoạch sau đó trước khi viết tiếp.
	// Feedback thông thường của Writer được hấp thụ ở thao tác cấu trúc kế tiếp, không dispatch planner từng chương.
	ImmediateFeedbackCount int

	// Artifact arc/quyển đầu tiên cần Editor dựng lại sau sửa đổi bên ngoài.
	AggregateRefresh *AggregateRefresh
}

// Route trả chỉ thị xác định cho bước kế tiếp dựa trên fact; nil nghĩa là Engine xử lý theo ngữ cảnh gọi.
//
// Thứ tự ưu tiên, khớp nhánh đầu tiên từ trên xuống:
//  1. Phase=Complete        → nil, Host xuất tổng kết xác định.
//  2. Giai đoạn lập kế hoạch thiếu thiết lập và suy ra được planner → tiếp tục giao cùng planner;
//     nếu chưa suy ra được planner → nil, Engine hỏi phán quyết khởi động bổ sung.
//  3. PendingRewrites không rỗng → writer xử lý chương đầu hàng đợi.
//  4. Flow=Reviewing        → nil, nhường cho LLM.
//  5. Flow=Steering         → nil, đang xử lý can thiệp người dùng.
//  6. Sửa đổi bên ngoài làm stale artifact tổng hợp → editor dựng lại artifact.
//  7. Sửa đổi bên ngoài ảnh hưởng kế hoạch sau đó → architect xử lý writer_feedback.
//  8. Sách phân tầng ở cuối arc → review, summary, mở rộng arc hoặc thêm quyển.
//  9. Sách không phân tầng tới kỳ review toàn cục → editor review global.
//
// 10. Dàn ý không phân tầng đã hết → architect quyết định hoàn tất hoặc nối tiếp.
// 11. Còn lại → writer viết chương kế tiếp.
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	if p.Phase == domain.PhaseComplete {
		return nil
	}

	if p.Phase != domain.PhaseWriting {
		if len(s.FoundationMissing) > 0 && s.PlanningTier != "" {
			task := fmt.Sprintf("Bổ sung các mục còn thiếu của thiết lập nền tảng và thông tin tác phẩm: %s; book dùng save_book, các thiết lập nền tảng khác dùng save_foundation để ghi xuống đĩa", strings.Join(s.FoundationMissing, ", "))
			if len(s.FoundationMissing) == 1 && s.FoundationMissing[0] == "foundation_audit" {
				task = "Thiết lập nền tảng đã đủ: gọi lại novel_context để đọc toàn bộ artifact đã ghi và foundation_status.fingerprint, kiểm tra tính nhất quán ngữ nghĩa giữa các tệp rồi gọi audit_foundation; nếu có vấn đề thì sửa và kiểm tra lại"
			}
			return &Instruction{
				Agent:  plannerForTier(s.PlanningTier),
				Task:   task,
				Reason: "thiếu thiết lập nền tảng, tiếp tục giao cùng planner",
			}
		}
		return nil
	}

	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "viết lại"
		if p.Flow == domain.FlowPolishing {
			verb = "trau chuốt"
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%s chương %d", verb, ch),
			Reason:  fmt.Sprintf("hàng đợi PendingRewrites còn %d chương", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	if p.Flow == domain.FlowReviewing {
		return nil
	}
	if p.Flow == domain.FlowSteering {
		return nil
	}

	if refresh := s.AggregateRefresh; refresh != nil {
		switch refresh.Kind {
		case AggregateArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"Duyệt arc quyển %d, cung %d (chương %d-%d): gọi novel_context(chapter=%d), save_review với scope=arc, chapter=%d",
					refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter,
				),
				Reason: "thiếu duyệt arc",
			}
		case AggregateArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo tóm tắt arc quyển %d, cung %d, ảnh chụp nhân vật và quy tắc viết (save_arc_summary)", refresh.Volume, refresh.Arc),
				Reason: "thiếu tóm tắt arc",
			}
		case AggregateVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo tóm tắt quyển %d (save_volume_summary)", refresh.Volume),
				Reason: "thiếu tóm tắt quyển",
			}
		case AggregateGlobalReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Duyệt toàn cục %d chương đầu: gọi novel_context(chapter=%d), save_review với scope=global, chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter),
				Reason: "thiếu duyệt toàn cục",
			}
		}
	}

	if s.ImmediateFeedbackCount > 0 {
		return &Instruction{
			Agent:  plannerForTier(s.PlanningTier),
			Task:   "Chỉ xử lý writer_feedback trong novel_context: đối chiếu tình tiết đã xảy ra với kế hoạch sau đó; khi cần chỉnh thì gọi revise_outline hoặc tool cấu trúc tương ứng, không cần chỉnh thì gọi resolve_outline_feedback; không xử lý foundation_status hoặc kế hoạch khác; sau khi ghi xuống đĩa thì kết thúc bằng một câu",
			Reason: fmt.Sprintf("có %d ảnh hưởng sửa đổi bên ngoài chưa lan truyền vào kế hoạch sau đó", s.ImmediateFeedbackCount),
		}
	}

	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{
				Agent: "editor",
				Task: fmt.Sprintf(
					"Duyệt arc quyển %d, cung %d (chương %d-%d): gọi novel_context(chapter=%d), save_review với scope=arc, chapter=%d; issues[].chapters chỉ được nằm trong khoảng này",
					b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter,
				),
				Reason: "duyệt arc chưa hoàn tất",
			}
		case !s.HasArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo tóm tắt arc quyển %d, cung %d, ảnh chụp nhân vật và quy tắc viết (save_arc_summary)", b.Volume, b.Arc),
				Reason: "tóm tắt arc chưa hoàn tất",
			}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo tóm tắt quyển %d (save_volume_summary)", b.Volume),
				Reason: "tóm tắt quyển chưa hoàn tất",
			}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("Mở rộng arc quyển %d, cung %d (save_foundation type=expand_arc)", b.NextVolume, b.NextArc),
				Reason: "khung arc tiếp theo đang chờ mở rộng",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "Tạo quyển tiếp theo: đánh giá theo danh sách kiểm tra hoàn tất rồi gọi save_foundation — câu chuyện tiếp tục → type=append_volume; câu chuyện gần kết thúc → type=append_volume với JSON cấp cao nhất có \"final\": true (quyển kết, khép lại toàn bộ tuyến, viết xong tự động hoàn tất); mọi điều kiện hoàn tất đã đủ → type=complete_book. Cả ba lựa chọn đều phải kèm tham số reason nêu rõ lý do",
				Reason: "cuối quyển cần quyết định thêm quyển, tạo quyển kết hoặc hoàn tất toàn bộ",
			}
		}
	}

	if !p.Layered && s.LastCompleted > 0 {
		if due, reason := domain.ShouldReview(len(p.CompletedChapters)); due && !s.HasGlobalReview {
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Duyệt toàn cục %d chương đầu (save_review scope=global, chapter=%d)", s.LastCompleted, s.LastCompleted),
				Reason: reason,
			}
		}
	}

	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if !p.Layered && p.TotalChapters > 0 && next > p.TotalChapters {
		return &Instruction{
			Agent: plannerForTier(s.PlanningTier),
			Task: fmt.Sprintf(
				"Dàn ý không phân tầng đã viết xong (đã hoàn thành %d chương, tổng %d chương): nếu câu chuyện đã khép lại, gọi save_foundation(type=complete_book); nếu vẫn cần tiếp tục, dùng revise_outline để nối kế hoạch từ chương %d",
				len(p.CompletedChapters), p.TotalChapters, next,
			),
			Reason: "dàn ý không phân tầng đã hết, cần quyết định hoàn tất hoặc nối tiếp",
		}
	}

	return &Instruction{
		Agent:   "writer",
		Task:    fmt.Sprintf("Viết chương %d", next),
		Reason:  "viết tiếp chương kế tiếp",
		Chapter: next,
	}
}
