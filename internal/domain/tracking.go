package domain

// StateChange Bản ghi thay đổi trạng thái của nhân vật/thực thể.
type StateChange struct {
	Chapter  int    `json:"chapter"`
	Entity   string `json:"entity"`              // Tên nhân vật hoặc tên thực thể
	Field    string `json:"field"`               // Thuộc tính thay đổi: realm/location/status/power/relation, v.v.
	OldValue string `json:"old_value,omitempty"` // Giá trị trước đó (lần đầu xuất hiện có thể để trống)
	NewValue string `json:"new_value"`           // Giá trị sau khi thay đổi
	Reason   string `json:"reason,omitempty"`    // Lý do thay đổi
}
