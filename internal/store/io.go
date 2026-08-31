package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// IO đóng gói thao tác đọc/ghi hệ thống tệp, cung cấp khóa và ghi nguyên tử.
// Mỗi kho con giữ một phiên bản IO độc lập, có sync.RWMutex riêng.
type IO struct {
	dir string
	mu  sync.RWMutex
}

func newIO(dir string) *IO {
	return &IO{dir: dir}
}

func (io *IO) path(rel string) string {
	return filepath.Join(io.dir, rel)
}

func (io *IO) ReadFile(rel string) ([]byte, error) {
	io.mu.RLock()
	defer io.mu.RUnlock()
	return io.ReadFileUnlocked(rel)
}

func (io *IO) ReadFileUnlocked(rel string) ([]byte, error) {
	return os.ReadFile(io.path(rel))
}

func (io *IO) WriteFileUnlocked(rel string, data []byte) error {
	p := io.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, p)
}

func (io *IO) ReadJSON(rel string, v any) error {
	io.mu.RLock()
	defer io.mu.RUnlock()
	return io.ReadJSONUnlocked(rel, v)
}

func (io *IO) ReadJSONUnlocked(rel string, v any) error {
	data, err := io.ReadFileUnlocked(rel)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (io *IO) WriteJSON(rel string, v any) error {
	io.mu.Lock()
	defer io.mu.Unlock()
	return io.WriteJSONUnlocked(rel, v)
}

func (io *IO) WriteJSONUnlocked(rel string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return io.WriteFileUnlocked(rel, data)
}

func (io *IO) WriteMarkdown(rel string, content string) error {
	io.mu.Lock()
	defer io.mu.Unlock()
	return io.WriteFileUnlocked(rel, []byte(content))
}

// WriteMarkdownUnlocked ghi sidecar .md. Quy ước: mỗi .md đều là
// dạng hiển thị dễ đọc cho con người theo nỗ lực tốt nhất của .json tương ứng,
// tuyệt đối không phải nguồn dữ liệu — khi chạy và khi xuất đều kết xuất lại từ .json.
// Các phương thức Save ghi .json trước rồi ghi .md này trong cùng một khóa ghi,
// là hai lần tmp+rename độc lập; nếu sập giữa hai lần này thì .md sẽ lạc hậu so với .json,
// điều đó chấp nhận được (không ai đọc .md làm dữ liệu, lần ghi tiếp theo trong cùng scope
// sẽ tự lành). Cố ý không thêm commit nguyên tử hai tệp cho việc này — như vậy là thiết kế quá mức.
func (io *IO) WriteMarkdownUnlocked(rel string, content string) error {
	return io.WriteFileUnlocked(rel, []byte(content))
}

func (io *IO) AppendLine(rel string, data []byte) error {
	io.mu.Lock()
	defer io.mu.Unlock()
	return io.AppendLineUnlocked(rel, data)
}

func (io *IO) AppendLineUnlocked(rel string, data []byte) error {
	p := io.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// syncFileUnlocked xác nhận trong khi phát lại lũy đẳng rằng các bản ghi append đã tồn tại đã được lưu bền vững.
// Bên gọi chịu trách nhiệm giữ khóa ghi io.mu.
func (io *IO) syncFileUnlocked(rel string) error {
	f, err := os.OpenFile(io.path(rel), os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func (io *IO) RemoveFile(rel string) error {
	io.mu.Lock()
	defer io.mu.Unlock()
	return io.RemoveFileUnlocked(rel)
}

func (io *IO) RemoveFileUnlocked(rel string) error {
	err := os.Remove(io.path(rel))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (io *IO) WithWriteLock(fn func() error) error {
	io.mu.Lock()
	defer io.mu.Unlock()
	return fn()
}

// EnsureDirs tạo các thư mục con được chỉ định.
func (io *IO) EnsureDirs(dirs []string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(io.dir, d), 0o755); err != nil {
			return fmt.Errorf("tạo thư mục %s: %w", d, err)
		}
	}
	return nil
}
