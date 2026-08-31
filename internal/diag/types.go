package diag

// Severity biểu thị mức độ nghiêm trọng của phát hiện.
type Severity string

const (
	SevCritical Severity = "critical" // chặn tiến độ hoặc làm hỏng dữ liệu
	SevWarning  Severity = "warning"  // có thể làm giảm chất lượng hoặc lãng phí token
	SevInfo     Severity = "info"     // mục có thể tối ưu
)

// Category nhóm các phát hiện theo chiều.
type Category string

const (
	CatFlow     Category = "flow"     // tắc nghẽn luồng, bất thường trạng thái, vấn đề khôi phục
	CatQuality  Category = "quality"  // điểm thẩm định, thực hiện hợp đồng, tính nhất quán
	CatPlanning Category = "planning" // thiếu dàn ý, trôigợi ý, compass lỗi thời
	CatContext  Category = "context"  // bất thường về nhân vật / timeline / quan hệ
)

// Confidence biểu thị độ tin cậy của phán định quy tắc.
type Confidence string

const (
	ConfHigh   Confidence = "high"   // xác định cao, đáng tin cậy
	ConfMedium Confidence = "medium" // phán đoán heuristic, có thể sai
	ConfLow    Confidence = "low"    // tín hiệu thô, chỉ để tham khảo
)

// AutoLevel biểu thị Finding có thể chuyển thành hành động tự động hay không.
type AutoLevel string

const (
	AutoNone    AutoLevel = "none"    // chỉ báo cáo, không tự động
	AutoSuggest AutoLevel = "suggest" // gợi ý hành động nhưng cần người xác nhận
	AutoSafe    AutoLevel = "safe"    // có thể tự động thực thi an toàn
)

// Finding là một kết quả chẩn đoán có thể hành động.
type Finding struct {
	Rule       string     // tên quy tắc， "StaleForeshadow"
	Category   Category   // phân loại
	Severity   Severity   // mức độ nghiêm trọng
	Confidence Confidence // độ tin cậy phán định
	AutoLevel  AutoLevel  // mức tự động hóa
	Target     string     // phạm vi tác động đề xuất， "runtime.flow"
	Title      string     // tóm tắt một dòng
	Evidence   string     // bằng chứng dữ liệu cụ thể
	Suggestion string     // đề xuất cải tiến（ prompt/flow/config）
}

// RuleFunc chữ ký thống nhất。
type RuleFunc func(snap *Snapshot) []Finding

// ActionKind biểu thị loại hành động chẩn đoán。
type ActionKind string

const (
	ActionEmitNotice      ActionKind = "emit_notice"       // phát thông báo hệ thống
	ActionEnqueueFollowUp ActionKind = "enqueue_follow_up" // tạo đề xuất xử lý tiếp theo
)

// Action  Planner  Finding 。
type Action struct {
	SourceRule  string     // nguồntên quy tắc
	Kind        ActionKind //
	Severity    Severity   // kế thừa từ Finding
	Summary     string     // mô tả ngắn
	Message     string     // thông điệp truyền cho luồng điều khiển
	Fingerprint string     // nguồn Finding ，
}

// Stats 。
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

// Report đầu ra。
type Report struct {
	Stats    Stats
	Findings []Finding
	Actions  []Action
}
