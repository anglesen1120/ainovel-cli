package host

import "log/slog"

type newOptions struct {
	logFile       string
	logAlsoStderr bool
	logAttrs      []slog.Attr
}

// NewOption cấu hình quá trình tạo Host, tài nguyên lúc chạy vẫn do Host giữ.
type NewOption func(*newOptions)

// WithFileLog để Host giữ một phiên nhật ký lúc chạy. Nhật ký chỉ được mở sau khi lấy được lease của thư mục tiểu thuyết,
// và sẽ đóng sau khi mọi nhật ký đóng của Host hoàn tất. Khi mở thất bại, tiếp tục dùng logger của tiến trình hiện tại,
// bên gọi phải xử lý lỗi này một cách rõ ràng qua FileLogError.
func WithFileLog(filename string, alsoStderr bool, attrs ...slog.Attr) NewOption {
	return func(opts *newOptions) {
		opts.logFile = filename
		opts.logAlsoStderr = alsoStderr
		opts.logAttrs = append([]slog.Attr(nil), attrs...)
	}
}
