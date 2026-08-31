// Gói imp triển khai pipeline nhập ngữ nghĩa theo giai đoạn cho tiểu thuyết
// bên ngoài (docs/import-pipeline.md).
//
// Model chịu trách nhiệm hiểu ngữ nghĩa mở; mã nguồn chịu trách nhiệm về tọa độ,
// độ bao phủ, kiểu, hash, thứ tự và tính bất biến khi lặp; mọi tạo tác ngữ nghĩa
// được xác thực trong workspace riêng (meta/import/) trước khi công bố vào trạng
// thái sách chính thức. Hành động tiếp theo chỉ được suy ra từ các tạo tác
// (NextAction), không lưu enum giai đoạn trôi dạt, và khôi phục không phụ thuộc
// vào from=N.
package imp

import "time"

// Options điều khiển một lần nhập. Khi khôi phục, các trường có thể để trống và
// được suy ra trực tiếp từ workspace đang hoạt động cùng Intent đã lưu.
type Options struct {
	SourcePath      string // bắt buộc với lần nhập mới; có thể để trống khi khôi phục
	AutoConfirm     bool   // --yes: tự động chấp nhận phân đoạn sau khi vượt qua kiểm tra độ bao phủ
	StoryResolution string // --story=open|closed: chỉ chọn trước khi bước tổng hợp trả về trạng thái chưa chắc chắn
	ContinueAfter   bool   // --continue: không tạo Hold hoàn tất nhập
	Guidance        string // --guide: hướng dẫn phân đoạn bằng ngôn ngữ tự nhiên; sau khi ghi vào workspace, tự phát hiện lại các điểm không khớp phân đoạn cũ
	// AcceptSegmentation: xác nhận rõ ràng của người dùng sau bản xem trước TUI (y). Cho phép phân đoạn hiện tại một lần mà không ghi intent;
	// khác với --yes: --yes là ủy quyền mù, không xem bản xem trước và không cho phép phân đoạn có ghi chú dung sai (Notes); y là phán quyết sau khi xem bản xem trước.
	AcceptSegmentation bool
}

// intent trích xuất các ủy quyền người dùng cần được lưu bền vững từ Options.
func (o Options) intent() Intent {
	return Intent{
		Version:             workspaceSchemaVersion,
		AutoConfirm:         o.AutoConfirm,
		StoryResolution:     o.StoryResolution,
		ContinueAfterImport: o.ContinueAfter,
	}
}

// Stage biểu diễn giai đoạn hiện tại của luồng nhập, chỉ dùng để hiển thị UI,
// không phải nguồn sự thật cho việc khôi phục (RFC §14.1).
type Stage string

const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)

// Event là sự kiện tiến độ được luồng nhập phát ra ra bên ngoài. Event chỉ là
// phép chiếu và không tham gia vào quá trình khôi phục.
type Event struct {
	Time      time.Time
	Stage     Stage
	Current   int       // tiến độ chương/khoảng
	Total     int       // tổng số lượng
	Message   string    // mô tả dễ đọc đối với người dùng
	Level     string    // ""=tiến độ bình thường; "warn"=trạng thái cảnh báo như thử lại do backoff/yêu cầu xác thực lại
	Key       string    // khi không rỗng, các sự kiện liên tiếp cùng Key cập nhật tại chỗ trong UI (ví dụ 7 lần backoff thay đổi trên một dòng), đồng bộ với cơ chế ID của bảng sự kiện
	RetryAt   time.Time // khác zero = thời hạn lần thử lại tiếp theo; UI hiển thị đếm ngược theo giây tương ứng và xóa tại thời điểm đó (yêu cầu đã được gửi)
	Err       error     // được mang theo khi StageError
	Continued bool      // khi StageDone, Host thiết lập: Engine đã được chuyển giao và khởi động tự động hay chưa (--continue × auto)
}
