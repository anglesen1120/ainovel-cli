package host

import (
	"time"
)

// Event là sự kiện có cấu trúc được TUI tiêu thụ.
//
// Với các sự kiện gọi TOOL / DISPATCH / DECISION, lần gọi đó dùng chung một ID cho cả bắt đầu và kết thúc:
// lúc bắt đầu sẽ phát một sự kiện có FinishedAt là giá trị zero (TUI hiển thị theo kiểu "đang thực hiện");
// lúc kết thúc sẽ phát thêm một sự kiện cùng ID, điền FinishedAt + Duration (+ Failed),
// TUI dùng ID để tìm đúng dòng cũ và cập nhật ngay tại chỗ, tránh sự thừa thãi kiểu "bắt đầu một dòng, xong lại một dòng".
//
// Các sự kiện không phải gọi như SYSTEM / ERROR / CONTEXT thì ID để trống, mỗi sự kiện sẽ được thêm riêng.
type Event struct {
	ID         string    // dùng chung cho cùng một lần gọi ở cả bắt đầu/kết thúc; với sự kiện không phải gọi thì để trống
	Time       time.Time // thời điểm phát lần đầu (thời điểm bắt đầu)
	FinishedAt time.Time // giá trị zero = đang thực hiện; khác zero = đã hoàn tất
	Failed     bool      // đã hoàn tất nhưng thất bại (chỉ có ý nghĩa ở trạng thái hoàn tất)
	Category   string    // DISPATCH / TOOL / DECISION / SYSTEM / REVIEW / CHECK / ERROR / CONTEXT
	Agent      string    // agent tạo ra sự kiện
	Summary    string
	Detail     string        // văn bản đầy đủ, ghi vào log không cắt ngắn để phục vụ tra soát; nếu trống thì quay về Summary. UI chỉ đọc Summary
	Kind       string        // phân loại lỗi (ví dụ stream_idle), xuất cùng log để lọc/cảnh báo; nếu trống thì không xuất
	Level      string        // info / warn / error / success
	Depth      int           // 0 = tầng Engine, 1 = tầng Worker
	Duration   time.Duration // thời gian thực thi khi hoàn tất
	RetryAt    time.Time     // sự kiện dạng thử lại: thời điểm chốt cho lần thử lại tiếp theo; UI sẽ đếm ngược từng giây rồi xóa khi đến hạn (yêu cầu đã gửi đi)
}

// Running trả về việc sự kiện có đang ở trạng thái thực hiện hay không.
// Chỉ các sự kiện gọi có lifecycle (TOOL / DISPATCH / DECISION có ID) mới có thể đang thực hiện; các loại khác luôn trả về false.
func (e Event) Running() bool {
	return e.hasLifecycle() && e.FinishedAt.IsZero()
}

func (e Event) hasLifecycle() bool {
	if e.ID == "" {
		return false
	}
	switch e.Category {
	case "TOOL", "DISPATCH", "DECISION":
		return true
	default:
		return false
	}
}

// UISnapshot là ảnh chụp trạng thái tổng hợp cần cho TUI render.
type UISnapshot struct {
	Provider             string
	BookTitle            string
	ModelName            string
	ModelContextWindow   int // cửa sổ ngữ cảnh của model mặc định hiện tại (được phân giải lại theo thời gian thực khi đổi /model)
	ThinkingLevel        string
	Style                string
	RuntimeState         string // idle / running / pausing / paused / completed
	StatusLabel          string
	Phase                string
	Flow                 string
	CurrentChapter       int
	TotalChapters        int
	CompletedCount       int
	TotalWordCount       int
	InProgressChapter    int
	PendingRewrites      []int
	RewriteReason        string
	PendingSteer         string
	AdvanceMode          string
	AdvancePermitChapter int
	HasAdvanceHold       bool
	AdvanceHoldReason    string
	RecoveryLabel        string
	IsRunning            bool
	Agents               []AgentSnapshot

	// tổng mức dùng tích lũy (toàn bộ phiên, qua mọi agent và mọi lần đổi model)
	TotalInputTokens      int
	TotalOutputTokens     int
	TotalCacheReadTokens  int
	TotalCacheWriteTokens int
	TotalCostUSD          float64
	TotalSavedUSD         float64 // số đô la tiết kiệm được nhờ CacheRead trúng (so với việc tính toàn bộ theo giá input không cache)
	BudgetLimitUSD        float64 // giới hạn ngân sách (config budget.book_usd); 0 = chưa bật

	// chẩn đoán cache
	OverallCacheCapable    bool // ít nhất một role đã chạy model hỗ trợ prompt cache (phân biệt "chưa bật" và "tỷ lệ trúng 0%")
	OverallRecentCacheRead int  // tổng cacheRead của N lần gần nhất trong cửa sổ trượt
	OverallRecentInput     int  // tổng input của N lần gần nhất trong cửa sổ trượt
	OverallRecentSamples   int  // số mẫu trong cửa sổ trượt (≤ recentSampleCap)
	TotalCacheBreaks       int  // số lần đứt chuỗi cache được phát hiện trực tiếp (prefix không rút ngắn nhưng tỷ lệ trúng giảm đột ngột), xem noteCacheBreak trong usage.go

	// MissingAssistantUsage > 0 thường có nghĩa là luồng streaming phía trên không phát final usage chunk theo
	// giao thức OpenAI stream_options.include_usage (thường gặp ở proxy tự dựng),
	// khiến UsageTracker không nhận được dữ liệu tích lũy nào. UI sẽ hiển thị rõ để người dùng kiểm tra backend,
	// tránh để người dùng tưởng rằng chính mô-đun cache đã hỏng.
	MissingAssistantUsage int

	// cache theo từng role, sắp xếp giảm dần theo CacheRead, đã lọc bỏ role chưa tiêu thụ token
	CachePerAgent []AgentCacheStat
	CachePerModel []AgentCacheStat

	// thiết lập cơ bản
	Synopsis         string
	Premise          string
	Outline          []OutlineSnapshot
	Characters       []string
	SupportingCount  int      // tổng số nhân vật phụ trong danh sách vai phụ
	RecentSupporting []string // các nhân vật phụ hoạt động gần đây (tối đa 5, sắp theo LastSeenChapter giảm dần)
	Layered          bool
	CurrentVolumeArc string
	NextVolumeTitle  string
	CompassDirection string
	CompassScale     string

	// chi tiết
	LastCommitSummary  string
	LastReviewSummary  string
	LastCheckpointName string
	RecentSummaries    []string
}

// OutlineSnapshot là bản tóm tắt hiển thị của một mục dàn ý.
type OutlineSnapshot struct {
	Chapter   int
	Title     string
	CoreEvent string
}

// AgentSnapshot là ảnh chiếu hiển thị trạng thái của Agent.
type AgentSnapshot struct {
	Name      string
	State     string
	TaskID    string
	TaskKind  string
	Summary   string
	Tool      string
	Turn      int
	Context   AgentContextSnapshot
	UpdatedAt time.Time
}

// AgentCacheStat là số liệu tích lũy cache hit của một agent đơn lẻ (chiếu ra cột bên trái).
// HitRate = CacheRead / Input; ở tầng litellm, Input đã được chuẩn hóa thành nghĩa "bao gồm CacheRead".
//
// CacheCapable dùng để phân biệt hai trường hợp trúng 0%:
//   - true  → model hỗ trợ prompt cache, 0% là do thiết kế prompt kém hoặc tiền tố không ổn định, cần tối ưu
//   - false → model/provider không hỗ trợ prompt cache, 0% là bình thường, không cần tra soát
//
// Recent* là dữ liệu trúng trong cửa sổ trượt (N lần gọi gần nhất), so với số tích lũy có thể nhận ra "bị kéo xuống ở giai đoạn đầu" so với "trạng thái ổn định nhưng trúng thấp".
type AgentCacheStat struct {
	Role            string
	Model           string
	Input           int
	Output          int
	CacheRead       int
	CacheWrite      int
	Cost            float64
	Saved           float64
	CacheCapable    bool
	RecentCacheRead int
	RecentInput     int
	RecentSamples   int
}

// AgentContextSnapshot là tình trạng sử dụng ngữ cảnh của Agent.
type AgentContextSnapshot struct {
	Tokens          int
	ContextWindow   int
	Percent         float64
	Scope           string
	Strategy        string
	ActiveMessages  int
	SummaryMessages int
	CompactedCount  int
	KeptCount       int
}

// CoCreateMessage là tin nhắn của cuộc trò chuyện đồng sáng tạo.
type CoCreateMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CoCreateReply là câu trả lời LLM của cuộc trò chuyện đồng sáng tạo. Raw giữ nguyên đủ bốn đoạn gốc của model,
// để ghi lại vào history và cho vòng sau nhìn thấy [DRAFT] của vòng trước, từ đó thật sự tích lũy cập nhật trên
// bản nháp đã có sẵn (chỉ dùng Message mà không có [DRAFT] sẽ khiến model mỗi vòng lại tự tổng hợp từ đối thoại).
// Suggestions là phần AI chủ động gợi ý "tiếp theo bạn có thể muốn nói gì", để khi người dùng bị kẹt có thể bấm số và chèn thẳng vào ô nhập.
type CoCreateReply struct {
	Message     string
	Prompt      string
	Ready       bool
	Suggestions []string
	Raw         string
}
