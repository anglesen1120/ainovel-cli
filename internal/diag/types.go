package diag

// Severity biểu thị mức độ nghiêm trọng của phát hiện.
type Severity string

const (
	SevCritical Severity = "critical" // Chặn tiến độ hoặc dữ liệu bị hỏng
	SevWarning  Severity = "warning"  // Có thể làm giảm chất lượng hoặc lãng phí token
	SevInfo     Severity = "info"     // Mục có thể tối ưu
)

// Category nhóm các phát hiện theo chiều.
type Category string

const (
	CatFlow     Category = "flow"     // Tắc nghẽn quy trình, bất thường trạng thái, vấn đề khôi phục
	CatQuality  Category = "quality"  // Điểm đánh giá, hoàn thành contract, tính nhất quán
	CatPlanning Category = "planning" // Thiếu dàn ý, foreshadow trôi lệch, compass lỗi thời
	CatContext  Category = "context"  // Bất thường về nhân vật / timeline / quan hệ
)

// Confidence biểu thị độ tin cậy của quyết định quy tắc.
type Confidence string

const (
	ConfHigh   Confidence = "high"   // Tính xác định cao, đáng tin cậy
	ConfMedium Confidence = "medium" // Phán đoán theo heuristics, có thể sai lệch
	ConfLow    Confidence = "low"    // Tín hiệu thô, chỉ để tham khảo
)

// AutoLevel biểu thị Finding có thể chuyển thành hành động tự động hay không.
type AutoLevel string

const (
	AutoNone    AutoLevel = "none"    // Chỉ báo cáo, không tự động
	AutoSuggest AutoLevel = "suggest" // Đề xuất hành động nhưng cần người xác nhận
	AutoSafe    AutoLevel = "safe"    // Có thể tự động thực thi an toàn
)

// Finding là một kết quả chẩn đoán có thể thực thi.
type Finding struct {
	Rule       string     // Tên quy tắc, ví dụ "StaleForeshadow"
	Category   Category   // Phân loại
	Severity   Severity   // Mức độ nghiêm trọng
	Confidence Confidence // Độ tin cậy của phán định
	AutoLevel  AutoLevel  // Mức độ tự động hóa
	Target     string     // Mặt tác động được đề xuất, ví dụ "runtime.flow"
	Title      string     // Tóm tắt một dòng
	Evidence   string     // Dữ liệu chứng cứ cụ thể
	Suggestion string     // Gợi ý cải thiện (chỉ tới prompt/flow/config)
}

// RuleFunc là chữ ký thống nhất của quy tắc chẩn đoán.
type RuleFunc func(snap *Snapshot) []Finding

// ActionKind biểu thị loại hành động chẩn đoán.
type ActionKind string

const (
	ActionEmitNotice      ActionKind = "emit_notice"       // Phát thông báo hệ thống
	ActionEnqueueFollowUp ActionKind = "enqueue_follow_up" // Tạo đề xuất xử lý tiếp theo
)

// Action là hành động có thể thực thi do Planner tạo từ Finding có độ tin cậy cao.
type Action struct {
	SourceRule  string     // Tên quy tắc nguồn
	Kind        ActionKind // Loại hành động
	Severity    Severity   // Kế thừa từ Finding
	Summary     string     // Mô tả ngắn
	Message     string     // Thông điệp truyền cho luồng điều khiển
	Fingerprint string     // Vân tay ổn định của Finding nguồn, dùng để khử trùng lặp khi chạy
}

// Stats là các chỉ số tổng quan hiển thị song song với phát hiện.
type Stats struct {
	CompletedChapters int
	TotalChapters     int
	TotalWords        int
	AvgWordsPerCh     int
	Phase             string
	Flow              string
	PlanningTier      string
	ReviewCount       int
	RewriteCount      int
	AvgReviewScore    float64
	ForeshadowOpen    int
	ForeshadowStale   int
}

// Report là đầu ra đầy đủ của một lần chẩn đoán.
type Report struct {
	Stats    Stats
	Findings []Finding
	Actions  []Action
}
