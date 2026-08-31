// Gói flow triển khai định tuyến theo quy tắc: Host dựa trên các sự kiện để quyết định gọi worker con nào tiếp theo và thực hiện việc gì.
//
// Nguyên tắc thiết kế:
//   - Route là hàm thuần: nhận State và trả về *Instruction. Không có IO hay lời gọi Store, nên có thể kiểm thử đơn vị.
//   - State được LoadState (không thuần) dựng từ Store, đọc một lần tất cả sự kiện cần cho việc định tuyến.
//   - Trả về nil là hợp lệ: nghĩa là hiện không có chỉ thị Worker nào có thể suy ra từ các sự kiện tất định;
//     Engine sẽ xử lý tiếp dựa trên trạng thái kết thúc, phân xử bổ sung lúc khởi động hoặc chờ người dùng can thiệp.
//
// Router bao quát các quyết định "dạng tra bảng" (bước tiếp theo của mỗi chương, xử lý cuối cung, điều khiển theo hàng đợi),
// không bao quát các quyết định "dạng hiểu ngữ nghĩa" (chọn planner, xử lý Steer của người dùng, xuất bản tóm tắt).
package flow

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// Suy ra danh tính planner từ cấp độ lập kế hoạch đã lưu: short thuộc planner truyện ngắn,
// mid/long thuộc planner truyện dài (nhất quán với cách chọn Arbiter lúc khởi động).
func plannerForTier(tier domain.PlanningTier) string {
	if tier == domain.PlanningTierShort {
		return "architect_short"
	}
	return "architect_long"
}

// Instruction chỉ dẫn Worker mà Engine sẽ chạy trực tiếp tiếp theo và nhiệm vụ tương ứng.
type Instruction struct {
	Agent   string // architect_long / architect_short / writer / editor
	Task    string // Mô tả nhiệm vụ giao cho worker con
	Reason  string // Lý do định tuyến (dùng cho sự kiện, nhật ký và phân xử khi thất bại)
	Chapter int    // Số chương liên quan đến nhiệm vụ writer (viết tiếp/viết lại/biên tập); 0 nghĩa là không liên quan (nhiệm vụ editor/architect)
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

// State là đầu vào của Route: mọi sự kiện phải được khai báo tường minh tại đây, cấm Route đọc Store nội bộ.
type State struct {
	Progress *domain.Progress

	// Số chương lớn nhất trong các chương đã hoàn thành; 0 nghĩa là chưa bắt đầu viết.
	LastCompleted int

	// Thông tin ranh giới cung của chương trước; các trường khác không có ý nghĩa khi IsArcEnd=false.
	// Phải là nil khi LastCompleted=0 hoặc ở chế độ không phân tầng.
	ArcBoundary *storepkg.ArcBoundary

	// Ba sự kiện xử lý cuối cung: đánh giá / tóm tắt cung / tóm tắt quyển đã hoàn tất hay chưa.
	HasArcReview     bool
	HasArcSummary    bool
	HasVolumeSummary bool

	// Các mục thiếu trong thiết lập cơ bản (tín hiệu bổ sung trong giai đoạn lập kế hoạch).
	FoundationMissing []string

	// Cấp độ lập kế hoạch đã lưu (được ghi vào RunMeta khi save_foundation ghi scale).
	// Rỗng = lần lập kế hoạch đầu tiên chưa tạo ra thiết lập nào, không thể xác định danh tính planner.
	PlanningTier domain.PlanningTier

	// Sách không phân tầng: chương gần nhất đã hoàn thành có đánh giá toàn cục scope=global hay chưa
	// (chỉ có ý nghĩa tại điểm kích hoạt ShouldReview; sách phân tầng luôn là false).
	HasGlobalReview bool

	// Ảnh hưởng của sửa đổi bên ngoài mà Architect phải xử lý trước khi viết tiếp. Phản hồi thông thường
	// của Writer được giữ lại cho lần thao tác cấu trúc tự nhiên tiếp theo và không giao planner riêng cho từng chương.
	ImmediateFeedbackCount int

	// Hiện vật cung/quyển sớm nhất cần Editor tạo lại sau sửa đổi bên ngoài.
	AggregateRefresh *AggregateRefresh
}

// Route trả về chỉ thị tất định tiếp theo dựa trên các sự kiện; Engine xử lý nil theo ngữ cảnh gọi.
//
// Thứ tự ưu tiên quyết định (loại trừ lẫn nhau, khớp mục đầu tiên từ trên xuống):
//  1. Phase=Complete        → nil (Host xuất bản tóm tắt tất định)
//  2. Thiếu thiết lập trong giai đoạn lập kế hoạch và xác định được planner → bổ sung bằng cùng planner; nếu không thì nil (Engine phân xử bổ sung lúc khởi động)
//  3. PendingRewrites không rỗng → writer viết lại/biên tập theo hàng đợi
//  4. Flow=Reviewing        → nil (dormant: hiện không có worker ghi; trong giai đoạn đánh giá, Flow thực tế là writing)
//  5. Flow=Steering         → nil (đang xử lý can thiệp của người dùng)
//  6. Sửa đổi bên ngoài làm vô hiệu hiện vật tổng hợp → editor tạo lại
//  7. Sửa đổi bên ngoài ảnh hưởng kế hoạch tiếp theo → architect xử lý
//  8. Sách phân tầng đến cuối cung → đánh giá, tóm tắt, mở rộng cung hoặc tiếp tục quyển
//  9. Đến hạn đánh giá toàn cục của sách không phân tầng → editor(global review)
//
// 10. Đại cương của sách không phân tầng đã dùng hết → architect (quyết định kết thúc hoặc nối tiếp đại cương)
// 11. Các trường hợp khác → writer (viết next_chapter)
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. Trạng thái kết thúc: Host tạo tóm tắt tất định dựa trên các sự kiện trong store
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. Bổ sung trong giai đoạn lập kế hoạch: suy ra planner từ cấp độ đã lưu.
	if p.Phase != domain.PhaseWriting {
		if len(s.FoundationMissing) > 0 && s.PlanningTier != "" {
			task := fmt.Sprintf("Bổ sung các mục thiếu trong thiết lập cơ bản và thông tin tác phẩm: %s; book dùng save_book, các thiết lập cơ bản khác dùng save_foundation để lưu", strings.Join(s.FoundationMissing, ", "))
			if len(s.FoundationMissing) == 1 && s.FoundationMissing[0] == "foundation_audit" {
				task = "Thiết lập cơ bản đã đầy đủ: gọi lại novel_context để đọc toàn bộ hiện vật đã lưu và foundation_status.fingerprint, kiểm tra tính nhất quán ngữ nghĩa giữa các tệp rồi gọi audit_foundation; nếu có vấn đề thì sửa trước và kiểm tra lại"
			}
			return &Instruction{
				Agent:  plannerForTier(s.PlanningTier),
				Task:   task,
				Reason: "Thiếu mục thiết lập cơ bản, tiếp tục giao cho cùng planner theo các mục còn thiếu",
			}
		}
		return nil
	}

	// 3. Ưu tiên hàng đợi viết lại/biên tập.
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "Viết lại "
		if p.Flow == domain.FlowPolishing {
			verb = "Biên tập "
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%sChương %d", verb, ch),
			Reason:  fmt.Sprintf("Còn %d chương trong hàng đợi PendingRewrites", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. Đang đánh giá → giao lại cho LLM.
	if p.Flow == domain.FlowReviewing {
		return nil
	}

	// 5. Đang xử lý can thiệp của người dùng: Arbiter đang phân xử.
	if p.Flow == domain.FlowSteering {
		return nil
	}
	if refresh := s.AggregateRefresh; refresh != nil {
		switch refresh.Kind {
		case AggregateArcReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Đánh giá quyển %d, cung %d (Chương %d-%d): gọi novel_context(chapter=%d), gọi save_review với scope=arc, chapter=%d", refresh.Volume, refresh.Arc, refresh.StartChapter, refresh.EndChapter, refresh.EndChapter, refresh.EndChapter),
				Reason: "Thiếu đánh giá cấp cung",
			}
		case AggregateArcSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo bản tóm tắt cung %d quyển %d, ảnh chụp nhân vật và quy tắc viết (save_arc_summary)", refresh.Arc, refresh.Volume),
				Reason: "Thiếu bản tóm tắt cấp cung",
			}
		case AggregateVolumeSummary:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Tạo bản tóm tắt quyển %d (save_volume_summary)", refresh.Volume),
				Reason: "Thiếu bản tóm tắt quyển",
			}
		case AggregateGlobalReview:
			return &Instruction{
				Agent:  "editor",
				Task:   fmt.Sprintf("Đánh giá %d chương đầu: gọi novel_context(chapter=%d), gọi save_review với scope=global, chapter=%d", refresh.EndChapter, refresh.EndChapter, refresh.EndChapter),
				Reason: "Thiếu đánh giá toàn cục",
			}
		}
	}

	if s.ImmediateFeedbackCount > 0 {
		return &Instruction{
			Agent:  plannerForTier(s.PlanningTier),
			Task:   "Chỉ xử lý writer_feedback về sửa đổi bên ngoài trong novel_context: đối chiếu diễn biến đã xảy ra với kế hoạch tiếp theo, khi cần điều chỉnh thì gọi revise_outline hoặc công cụ cấu trúc tương ứng, khi không cần điều chỉnh thì gọi resolve_outline_feedback; không xử lý foundation_status hay quy hoạch khác, sau khi lưu hãy kết thúc bằng một câu",
			Reason: fmt.Sprintf("Còn %d sửa đổi bên ngoài ảnh hưởng chưa được truyền vào kế hoạch tiếp theo", s.ImmediateFeedbackCount),
		}
	}

	// 8. Hậu xử lý cuối cung trong sách phân tầng.
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case !s.HasArcReview:
			return &Instruction{Agent: "editor", Task: fmt.Sprintf("Đánh giá cấp cung cho quyển %d, cung %d (Chương %d-%d): gọi novel_context(chapter=%d), save_review với scope=arc, chapter=%d; issues[].chapters chỉ được nằm trong khoảng này", b.Volume, b.Arc, b.StartChapter, b.EndChapter, b.EndChapter, b.EndChapter), Reason: "Chưa hoàn tất đánh giá cuối cung"}
		case !s.HasArcSummary:
			return &Instruction{Agent: "editor", Task: fmt.Sprintf("Tạo bản tóm tắt cung %d quyển %d, ảnh chụp nhân vật và quy tắc viết (save_arc_summary)", b.Arc, b.Volume), Reason: "Chưa hoàn tất bản tóm tắt cung"}
		case b.IsVolumeEnd && !s.HasVolumeSummary:
			return &Instruction{Agent: "editor", Task: fmt.Sprintf("Tạo bản tóm tắt quyển %d (save_volume_summary)", b.Volume), Reason: "Chưa hoàn tất bản tóm tắt quyển"}
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{Agent: "architect_long", Task: fmt.Sprintf("Mở rộng cung %d quyển %d (save_foundation type=expand_arc)", b.NextArc, b.NextVolume), Reason: "Khung cung tiếp theo đang chờ mở rộng"}
		case b.NeedsNewVolume:
			return &Instruction{Agent: "architect_long", Task: "Tạo quyển tiếp theo: đánh giá theo danh sách kiểm tra kết thúc rồi gọi save_foundation — câu chuyện tiếp tục → type=append_volume; câu chuyện gần kết thúc → type=append_volume và JSON cấp cao nhất của quyển có \"final\": true (quyển kết thúc, khép lại toàn bộ quyển, viết xong tự động hoàn tất); mọi điều kiện kết thúc đã thỏa mãn → type=complete_book. Cả ba lựa chọn đều phải kèm tham số reason nêu rõ lý do đánh giá", Reason: "Cuối quyển cần quyết định thêm quyển mới, tạo quyển kết thúc hoặc kết thúc toàn bộ tác phẩm"}
		}
	}

	// 11. Đánh giá toàn cục cho sách không phân tầng.
	if !p.Layered && s.LastCompleted > 0 {
		if due, reason := domain.ShouldReview(len(p.CompletedChapters)); due && !s.HasGlobalReview {
			return &Instruction{Agent: "editor", Task: fmt.Sprintf("Đánh giá toàn cục %d chương đầu (save_review scope=global, chapter=%d)", s.LastCompleted, s.LastCompleted), Reason: reason}
		}
	}

	// 12. Khi đại cương không phân tầng đã dùng hết, Architect quyết định kết thúc hoặc nối tiếp.
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if !p.Layered && p.TotalChapters > 0 && next > p.TotalChapters {
		return &Instruction{Agent: plannerForTier(s.PlanningTier), Task: fmt.Sprintf("Đại cương không phân tầng đã viết xong (đã hoàn thành %d chương, tổng cộng %d chương): nếu câu chuyện đã khép lại, gọi save_foundation(type=complete_book); nếu cần tiếp tục, dùng revise_outline để nối kế hoạch từ Chương %d", len(p.CompletedChapters), p.TotalChapters, next), Reason: "Đại cương không phân tầng đã dùng hết, cần quyết định kết thúc hoặc nối tiếp"}
	}

	// 13. Viết tiếp bình thường.
	return &Instruction{Agent: "writer", Task: fmt.Sprintf("Viết Chương %d", next), Reason: "Viết tiếp chương kế", Chapter: next}
}
