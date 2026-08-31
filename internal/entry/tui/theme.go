package tui

import "github.com/charmbracelet/lipgloss"

// Bảng màu chủ đề — sắc ấm, cảm giác sách vở
// AdaptiveColor: Light = giá trị màu nền sáng, Dark = giá trị màu nền tối
//
// Nguyên tắc thiết kế: Light giữ nguyên một mức ổn định (nền sáng đã cho hiệu ứng như ý); Dark đồng bộ cao hơn Light một mức
// Tăng lightness khoảng ~25%, nhích nhẹ độ bão hòa, bảo đảm trên nền tối có đủ độ tương phản (colorDim trước đó #6b6355
// gần như không thấy trên nền đen #1c1c1c, đường phân cách / văn bản phụ đều biến mất).
//
// colorAccent2 trên nền tối đổi từ #7a9e7e sang xanh ngọc #5fb8a3, bắt nhịp với colorSuccess của "xanh khỏe mạnh"
// — trước đây hai bên hoàn toàn cùng màu, khiến thang màu của architect agent và cảm giác vui mừng của "độ tự tin cao" bị lẫn lộn.
// bodyTextColor là chiến lược tiền cảnh cho "văn bản nội dung chung":
//   - Terminal tối → NoColor, kế thừa màu tiền cảnh mặc định của terminal, tránh việc chúng ta nhét cứng #e8e0d0 màu trắng ngà lên
//     các theme nền ấm/nền lạnh do người dùng tự phối gây chói màu (thực tế trên nền tối, màu mặc định của terminal dễ đọc hơn).
//   - Terminal sáng → dùng Light của colorText (nâu đậm #3d3529), giữ sắc ấm của thương hiệu;
//     màu đen mặc định trên nền sáng có độ tương phản quá gắt, tông nâu đậm đã tinh chỉnh sẽ dịu mắt hơn trên nền sáng.
//
// AdaptiveColor ở cả hai đầu đều phải có giá trị màu, không có cấp "không màu", nên ở đây khởi động thì kiểm tra một lần nền,
// sau đó mọi giá trị Tổng quan / nội dung chương / mô tả lệnh và các nội dung "văn bản chung" khác sẽ cùng tham chiếu bodyTextColor.
var bodyTextColor lipgloss.TerminalColor = func() lipgloss.TerminalColor {
	if lipgloss.HasDarkBackground() {
		return lipgloss.NoColor{}
	}
	return lipgloss.Color("#3d3529")
}()

var (
	colorText    = lipgloss.AdaptiveColor{Light: "#3d3529", Dark: "#e8e0d0"}
	colorDim     = lipgloss.AdaptiveColor{Light: "#8a7e6b", Dark: "#8a8175"}
	colorMuted   = lipgloss.AdaptiveColor{Light: "#7a7060", Dark: "#b8b09c"}
	colorAccent  = lipgloss.AdaptiveColor{Light: "#b8860b", Dark: "#e5b449"}
	colorAccent2 = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#5fb8a3"}
	colorRunning = lipgloss.AdaptiveColor{Light: "#6f8641", Dark: "#b5d075"}
	colorSuccess = lipgloss.AdaptiveColor{Light: "#3d7a42", Dark: "#7ec488"}
	colorError   = lipgloss.AdaptiveColor{Light: "#b5433a", Dark: "#e07060"}
	colorReview  = lipgloss.AdaptiveColor{Light: "#b07530", Dark: "#e09b5a"}
	colorContext = lipgloss.AdaptiveColor{Light: "#6b5a9e", Dark: "#a890d8"}
	colorTool    = lipgloss.AdaptiveColor{Light: "#3a7a8a", Dark: "#7ec5d8"}
)

// Ánh xạ màu nhãn trạng thái
var statusColors = map[string]lipgloss.AdaptiveColor{
	"READY":    colorDim,
	"PAUSING":  colorAccent,
	"PAUSED":   colorAccent,
	"RUNNING":  colorRunning,
	"REVIEW":   colorReview,
	"REWRITE":  colorReview,
	"COMPLETE": colorSuccess,
	"ERROR":    colorError,
}

// Hiển thị trạng thái: icon + nhãn tiếng Việt. Đồng bộ với toàn bộ chủ đề ấm, tránh các mảng màu đặc gây chói.
// Icon của RUNNING để trống, do spinner frame lấp động, để cảm giác chuyển động hòa vào chính chỉ báo trạng thái.
var statusDisplay = map[string]struct {
	icon  string
	label string
}{
	"READY":    {"○", "Sẵn sàng"},
	"RUNNING":  {"", "Đang chạy"},
	"REVIEW":   {"◆", "Duyệt"},
	"REWRITE":  {"◆", "Viết lại"},
	"COMPLETE": {"●", "Hoàn tất"},
	"PAUSED":   {"⏸", "Tạm dừng"},
	"PAUSING":  {"⏸", "Đang tạm dừng"},
	"ERROR":    {"✕", "Lỗi"},
}

// Ánh xạ màu phân loại sự kiện
var categoryColors = map[string]lipgloss.AdaptiveColor{
	"DISPATCH": colorAccent,
	"DECISION": colorContext,
	"TOOL":     colorTool,
	"SYSTEM":   colorAccent,
	"USER":     colorAccent2,
	"REVIEW":   colorReview,
	"CHECK":    colorSuccess,
	"ERROR":    colorError,
	"AGENT":    colorMuted,
	"CONTEXT":  colorContext,
	"COMPACT":  colorContext,
}

// Kiểu cơ bản
var (
	baseBorder = lipgloss.RoundedBorder()

	topBarStyle = lipgloss.NewStyle().
			Foreground(colorText).
			Padding(0, 1)

	statusIconStyle = lipgloss.NewStyle().
			Bold(true)

	statusLabelStyle = lipgloss.NewStyle().
				Foreground(colorText)

	panelTitleStyle = lipgloss.NewStyle().
			Foreground(colorAccent).
			Bold(true)

	fieldLabelStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Width(10)

	// fieldValueStyle / cardContentStyle dùng bodyTextColor —— các giá trị của khu vực Tổng quan (Trạng thái chạy,
	// số Chương đã hoàn tất, số từ, v.v.), các mục dàn ý, danh sách vai trò, tóm tắt Chương, v.v. là "nội dung chung"
	// sẽ theo màu tiền cảnh mặc định của terminal trên nền tối (tránh nhét cứng màu trắng ngà gây lệch theme), còn trên nền sáng thì dùng nâu đậm để giữ sắc ấm.
	// Các thành phần mang tính ngữ nghĩa mạnh hơn (tiêu đề, giá trị nổi bật, trạng thái, Lỗi, tô màu tỷ lệ thành công, v.v.) vẫn dùng colorAccent /
	// colorError và các màu chủ đề khác.
	fieldValueStyle = lipgloss.NewStyle().Foreground(bodyTextColor)

	highlightValueStyle = lipgloss.NewStyle().
				Foreground(colorAccent).
				Bold(true)

	contextUsageMetaStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	cardTitleStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	cardContentStyle = lipgloss.NewStyle().Foreground(bodyTextColor)
)
