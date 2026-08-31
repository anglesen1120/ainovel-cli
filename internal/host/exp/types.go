// Package exp cung cấp khả năng xuất các chương đã hoàn tất.
//
// Đối xứng với imp/: chỉ dùng IO cục bộ, không phụ thuộc LLM và không sửa trạng thái store.
// Export có thể chạy song song với Engine vì chỉ đọc Progress và bản thảo cuối của chương;
// đây là năng lực ngang.
//
// Hiện hỗ trợ TXT và EPUB.
package exp

import "github.com/voocel/ainovel-cli/internal/store"

// Format định danh định dạng xuất.
type Format string

const (
	// FormatTXT xuất văn bản thuần.
	FormatTXT Format = "txt"
	// FormatEPUB là container EPUB 3 chuẩn (zip + xhtml).
	FormatEPUB Format = "epub"
)

// Options điều khiển hành vi xuất. Zero-value tương đương "xuất toàn bộ vào đường dẫn mặc định, báo lỗi nếu file đã tồn tại".
//
// Bố cục: 《Tên sách》 → vạch phân quyển → thân chương. Hai loại dữ liệu nội bộ không xuất hiện trong bản xuất:
// premise (bản thiết kế sáng tác, gồm độc giả mục tiêu / điểm tiêu thụ cốt lõi / vùng cấm viết... dành cho tác giả và engine,
// không phải lời tựa cho độc giả); vạch phân hồi (hồi quá chi tiết dưới góc nhìn độc giả). Tên sách và vạch phân quyển luôn được giữ.
type Options struct {
	// Khi Format là chuỗi rỗng, suy ra từ phần mở rộng OutPath (.txt → TXT, .epub → EPUB);
	// khi OutPath cũng rỗng, dùng FormatTXT. Bên gọi SDK có thể chỉ định rõ để bỏ qua bước suy luận.
	Format Format

	// OutPath là đường dẫn file xuất; rỗng nghĩa là {novelDir}/{BookMetadata.Title}.{ext}.
	OutPath string

	// From / To là phạm vi chương đóng. 0 nghĩa là từ chương 1 / tới chương cuối.
	// Chương chưa hoàn tất trong phạm vi sẽ bị bỏ qua và ghi vào Result.Skipped, không xem là lỗi.
	From, To int

	// Overwrite cho phép ghi đè khi file đã tồn tại; mặc định là từ chối.
	Overwrite bool
}

// Deps là các phụ thuộc Run cần. Chỉ cần store; export không cần LLM, prompt hay bundle.
type Deps struct {
	Store *store.Store
}

// Result là tóm tắt sản phẩm của một lần xuất thành công.
type Result struct {
	// Path là đường dẫn file đã ghi thực tế (tuyệt đối hoặc tương đối do bên gọi truyền).
	Path string
	// Chapters là số chương đã ghi thực tế.
	Chapters int
	// Bytes là số byte của file (UTF-8).
	Bytes int
	// Skipped là các số chương nằm trong phạm vi yêu cầu nhưng chưa hoàn tất.
	Skipped []int
}
