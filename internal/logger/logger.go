package logger

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Setup khởi tạo logger slog mặc định.
// w là đích xuất log, level là mức log tối thiểu.
func Setup(w io.Writer, level slog.Level) {
	slog.SetDefault(slog.New(newTextHandler(w, level)))
}

func newTextHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Giữ ngày, mili giây và múi giờ để log nối thêm qua nhiều process vẫn đối chiếu chính xác được version code và session.
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02T15:04:05.000Z07:00"))
			}
			return a
		},
	})
}

func newSessionLogger(w io.Writer, level slog.Level, sessionAttrs ...slog.Attr) (*slog.Logger, string) {
	sessionID := fmt.Sprintf("%s-p%d", time.Now().Format("20060102T150405.000Z0700"), os.Getpid())
	attrs := make([]slog.Attr, 0, len(sessionAttrs)+1)
	attrs = append(attrs, slog.String("session", sessionID))
	attrs = append(attrs, sessionAttrs...)
	handler := newTextHandler(w, level).WithAttrs(attrs)
	return slog.New(handler), sessionID
}

// FileLogger trả về logger độc lập ghi vào outputDir/logs/filename cùng hàm cleanup,
// dùng cho subsystem cần file log riêng như luồng import. Nếu mở file thất bại thì fallback về logger mặc định để không ngắt nghiệp vụ,
// nhưng vẫn phải trả lỗi cho caller hiển thị với người dùng, nếu không UI sẽ chỉ người dùng xem một file log không tồn tại.
func FileLogger(outputDir, filename string) (*slog.Logger, func(), error) {
	f, err := openLogFile(outputDir, filename)
	if err != nil {
		return slog.Default(), func() {}, err
	}
	logger, sessionID := newSessionLogger(f, slog.LevelDebug)
	logger.Info("Bắt đầu phiên log", "module", "logger", "session_id", sessionID)
	return logger, func() {
		logger.Info("Kết thúc phiên log", "module", "logger", "session_id", sessionID)
		_ = f.Close()
	}, nil
}

// SetupFile khởi tạo logger mặc định ghi vào file và trả hàm cleanup.
// Khi alsoStderr=true, đồng thời xuất ra stderr.
// Nếu không mở được thư mục hoặc file log thì trả lỗi để caller xử lý rõ ràng; cấm chuyển sang io.Discard
// rồi tiếp tục chạy, vì như vậy sẽ mất toàn bộ log đúng lúc cần chẩn đoán nhất.
func SetupFile(outputDir, filename string, alsoStderr bool, sessionAttrs ...slog.Attr) (func(), error) {
	f, err := openLogFile(outputDir, filename)
	if err != nil {
		return nil, err
	}

	var w io.Writer = f
	if alsoStderr {
		w = io.MultiWriter(os.Stderr, f)
	}
	previous := slog.Default()
	logger, sessionID := newSessionLogger(w, slog.LevelDebug, sessionAttrs...)
	slog.SetDefault(logger)
	logger.Info("Bắt đầu phiên log", "module", "logger", "session_id", sessionID)

	return func() {
		logger.Info("Kết thúc phiên log", "module", "logger", "session_id", sessionID)
		slog.SetDefault(previous)
		_ = f.Close()
	}, nil
}

func openLogFile(outputDir, filename string) (*os.File, error) {
	logPath := filepath.Join(outputDir, "logs", filename)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory %q: %w", filepath.Dir(logPath), err)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("mở file log %q: %w", logPath, err)
	}
	return f, nil
}
