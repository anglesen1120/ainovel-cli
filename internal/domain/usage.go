package domain

import "time"

// UsageSchemaVersion là phiên bản tương thích của meta/usage.json.
// Trong tương lai nếu ngữ nghĩa các trường AgentUsageTotals thay đổi, hãy tăng giá trị này; khi UsageStore.Load thấy phiên bản khác, nên bỏ qua và kích hoạt replay để dựng lại.
const UsageSchemaVersion = 2

// UsageState là snapshot có thể lưu bền vững của lượng sử dụng token / cost tích lũy.
// Trong bộ nhớ, nó được UsageTracker duy trì và định kỳ debounce ghi xuống meta/usage.json.
//
// Lưu ý: các samples cửa sổ trượt bên trong UsageTracker ("tỷ lệ trúng trong N lần gần nhất") **không được lưu bền vững**——
// nó chỉ phục vụ chẩn đoán ngắn hạn trên UI; sau khi tiến trình khởi động lại, bắt đầu tích lũy lại vài vòng từ rỗng là đủ để khôi phục ngữ nghĩa.
// MissingAssistantUsage vẫn được lưu bền vững; tích lũy qua các lần khởi động lại có giá trị chẩn đoán hơn.
type UsageState struct {
	Schema       int                         `json:"schema"`
	UpdatedAt    time.Time                   `json:"updated_at"`
	Overall      AgentUsageTotals            `json:"overall"`
	PerAgent     map[string]AgentUsageTotals `json:"per_agent"`
	PerModel     map[string]AgentUsageTotals `json:"per_model,omitempty"`
	MissingUsage int                         `json:"missing_assistant_usage"`
}

// AgentUsageTotals là dạng có thể lưu bền vững của các bộ đếm tích lũy cho một vai trò đơn lẻ (hoặc overall).
type AgentUsageTotals struct {
	Input        int     `json:"input"`
	Output       int     `json:"output"`
	CacheRead    int     `json:"cache_read"`
	CacheWrite   int     `json:"cache_write"`
	Cost         float64 `json:"cost_usd"`
	Saved        float64 `json:"saved_usd"`
	CacheCapable bool    `json:"cache_capable"`
	// CacheBreaks là số lần đứt chuỗi cache được phát hiện live (tiền tố chưa ngắn lại nhưng số lần trúng giảm mạnh).
	// Chỉ tích lũy trên đường dẫn thời gian thực; session replay không phát lại việc phát hiện.
	CacheBreaks int `json:"cache_breaks,omitempty"`
}
