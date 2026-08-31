package host

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

const bookLockFile = ".ainovel.lock"

// ErrBookInUse cho biết cùng một thư mục tiểu thuyết đã bị một tiến trình khác chiếm dụng.
var ErrBookInUse = errors.New("thư mục tiểu thuyết đã bị một phiên bản ainovel-cli khác chiếm dụng")

// bookLease giữ quyền độc quyền liên tiến trình đối với thư mục tiểu thuyết trong suốt vòng đời của Host.
// Tệp khóa sẽ được giữ lại trong thư mục; trạng thái chiếm dụng thực sự do hệ điều hành quản lý, và cũng sẽ tự động được giải phóng nếu tiến trình thoát bất thường.
type bookLease struct {
	lock *flock.Flock
}

func acquireBookLease(dir string) (*bookLease, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("phân giải thư mục tiểu thuyết: %w", err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return nil, fmt.Errorf("tạo thư mục tiểu thuyết: %w", err)
	}
	fileLock := flock.New(filepath.Join(absDir, bookLockFile), flock.SetPermissions(0o600))
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, closeBookLockAfterFailure(fileLock, fmt.Errorf("chiếm dụng thư mục tiểu thuyết %q: %w", absDir, err))
	}
	if !locked {
		return nil, closeBookLockAfterFailure(fileLock, fmt.Errorf(
			"%w: %s; vui lòng đóng terminal khác đang thao tác với thư mục này, hoặc sử dụng một thư mục tiểu thuyết khác",
			ErrBookInUse,
			absDir,
		))
	}
	return &bookLease{lock: fileLock}, nil
}

func closeBookLockAfterFailure(fileLock *flock.Flock, cause error) error {
	if err := fileLock.Close(); err != nil {
		return errors.Join(cause, fmt.Errorf("đóng khóa thư mục tiểu thuyết: %w", err))
	}
	return cause
}

func (l *bookLease) Close() error {
	if l == nil || l.lock == nil {
		return nil
	}
	err := l.lock.Close()
	l.lock = nil
	return err
}
