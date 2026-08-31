// Package rules triển khai tầng nhập (Policy) cho sở thích người dùng: chuẩn hóa rules viết từ các nguồn và hợp nhất thành
// snapshot của sách này (xem snapshot.go); trong runtime được novel_context inject và commit_chapter kiểm tra cơ học.
//
// Rule là loại sự kiện thứ tư, ngang hàng với Progress / Checkpoint / Artifact nhưng ngược tính chất:
// Ba loại đầu là đầu ra của hệ thống, còn Rule là đầu vào bền vững của ý định người dùng.
//
// Ràng buộc thiết kế (không thỏa hiệp):
//   - Công cụ chỉ trả về sự kiện, không trả về chỉ thị (Violation là sự kiện; editor quyết định có kích hoạt viết lại hay không)
//   - Không đưa vào đường verdict mới (tái dùng PendingRewrites)
//   - Không đưa vào trường độ nghiêm ngặt (severity được ánh xạ cố định theo loại rule; editor tự phân xử ngữ nghĩa)
//   - Không đụng Flow Router (rule không tham gia định tuyến)
package rules

// SourceKind đánh dấu nguồn tệp rules, chỉ dùng để tạo nhãn nguồn (ví dụ global:my-style.md).
type SourceKind int

const (
	// SourceGlobal — sở thích global của người dùng (mọi .md trong thư mục ~/.ainovel/rules/, hợp nhất theo thứ tự từ điển tên tệp), tái dùng giữa các sách.
	SourceGlobal SourceKind = iota
	// SourceProject — rules của sách này (mọi .md trong thư mục ./.ainovel/rules/, hợp nhất theo thứ tự từ điển tên tệp), có ưu tiên cao nhất.
	SourceProject
)

// String trả về tên dễ đọc của nguồn, dùng làm tiền tố nhãn nguồn.
func (k SourceKind) String() string {
	switch k {
	case SourceGlobal:
		return "global"
	case SourceProject:
		return "project"
	default:
		return "unknown"
	}
}

// Structured chứa các trường quy tắc có cấu trúc có thể kiểm tra cơ học (ứng viên/kết quả hợp nhất sau khi chuẩn hóa các nguồn).
// Số từ chương cố ý không nằm trong đây: độ dài bao nhiêu mới là một chương là vấn đề toàn vẹn tự sự, thuộc quyền cân nhắc ngữ nghĩa (writer/editor),
// số hóa thành lằn ranh cơ học sẽ dụ mô hình bơm chữ để vượt ngưỡng; mong muốn về số từ đi qua kênh preferences bằng ngôn ngữ tự nhiên.
type Structured struct {
	Genre            string         `json:"genre,omitempty"`
	ForbiddenChars   []string       `json:"forbidden_chars,omitempty"`
	ForbiddenPhrases []string       `json:"forbidden_phrases,omitempty"`
	FatigueWords     map[string]int `json:"fatigue_words,omitempty"`
}

// IsEmpty dùng để xác định có hoàn toàn không có quy tắc có cấu trúc hay không; checker có thể dựa vào đó để bỏ qua.
func (s Structured) IsEmpty() bool {
	return s.Genre == "" &&
		len(s.ForbiddenChars) == 0 &&
		len(s.ForbiddenPhrases) == 0 &&
		len(s.FatigueWords) == 0
}

// Severity đánh dấu mức nghiêm trọng của Violation.
// Ánh xạ cố định (người dùng không cấu hình được):
//
//	forbidden_chars xuất hiện       -> Error
//	forbidden_phrases xuất hiện     -> Error
//	fatigue_words vượt ngưỡng       -> Warning
type Severity string

const (
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Violation là đầu ra của checker: phát biểu sự kiện rằng chương này vi phạm một quy tắc cơ học.
//
// Lưu ý: commit_chapter truyền nguyên violations vào JSON trả về, không chặn commit;
// khi review, editor ánh xạ các sự kiện này vào bảy chiều hiện có (aesthetic/pacing/character/consistency),
// rồi LLM tự quyết định có nâng verdict để kích hoạt polish/rewrite hay không.
type Violation struct {
	Rule     string   `json:"rule"`             // forbidden_chars / forbidden_phrases / fatigue_words
	Target   string   `json:"target,omitempty"` // Đối tượng vi phạm cụ thể (từ/ký tự nào)
	Limit    any      `json:"limit,omitempty"`  // Ngưỡng; fatigue_words=int / forbidden_*=rỗng
	Actual   any      `json:"actual"`           // Giá trị thực tế: số lần xuất hiện
	Severity Severity `json:"severity"`         // error / warning
}
