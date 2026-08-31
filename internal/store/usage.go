package store

import (
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// UsageStore lưu bền vững tổng usage token / cost vào meta/usage.json.
// ghi qua atomic write của IO (tmp + rename), đường Save mỗi lần ghi đè toàn bộ state.
type UsageStore struct{ io *IO }

func NewUsageStore(io *IO) *UsageStore { return &UsageStore{io: io} }

// Load đọc usage.json. Khi file không tồn tại hoặc version schema không khớp thì trả về (nil, nil),
// bên gọi quyết định có chạy session replay để điền lại một lần hay không.
func (s *UsageStore) Load() (*domain.UsageState, error) {
	var state domain.UsageState
	if err := s.io.ReadJSON("meta/usage.json", &state); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if state.Schema != domain.UsageSchemaVersion {
		return nil, nil
	}
	return &state, nil
}

// Save ghi đè toàn bộ state xuống đĩa. Bên gọi chịu trách nhiệm debounce / throttle.
func (s *UsageStore) Save(state domain.UsageState) error {
	state.Schema = domain.UsageSchemaVersion
	return s.io.WriteJSON("meta/usage.json", state)
}
