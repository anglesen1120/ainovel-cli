package store

import (
	"os"

	"github.com/voocel/ainovel-cli/internal/rules"
)

// UserRulesStore quản lý snapshot quy tắc người dùng đã chuẩn hóa của sách này (meta/user_rules.json).
//
// nguồn sự thật duy nhất lúc chạy: novel_context injection và commit_chapter check chỉ đọc snapshot này,
// không đọc lại file rules nhiều lần nữa (tránh lệch và hai reader phân kỳ). Snapshot được chuẩn hóa khi tạo sách/import/làm mới.
type UserRulesStore struct{ io *IO }

func NewUserRulesStore(io *IO) *UserRulesStore { return &UserRulesStore{io: io} }

// Load đọc meta/user_rules.json. Nếu không tồn tại thì trả về nil (bên gọi dựa vào đó để sinh lười).
func (s *UserRulesStore) Load() (*rules.Snapshot, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	var snap rules.Snapshot
	if err := s.io.ReadJSONUnlocked("meta/user_rules.json", &snap); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &snap, nil
}

// Save lưu snapshot.
func (s *UserRulesStore) Save(snap *rules.Snapshot) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.io.WriteJSONUnlocked("meta/user_rules.json", snap)
}
