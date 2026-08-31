package rules

import (
	"os"
	"path/filepath"
)

// LoadOptions liệt kê các thư mục nguồn tệp rules để RawFileSources quét và chuẩn hóa.
//
// Thư mục không tồn tại không được xem là lỗi; khi quét sẽ âm thầm bỏ qua.
type LoadOptions struct {
	// HomeRulesDir là thư mục ~/.ainovel/rules/; quét mọi .md cấp đỉnh bên dưới (hợp nhất theo thứ tự từ điển tên tệp). Rỗng nghĩa là bỏ qua.
	HomeRulesDir string

	// ProjectRulesDir là thư mục ./.ainovel/rules/ (đối xứng với global, cũng quét mọi .md cấp đỉnh bên dưới). Rỗng nghĩa là bỏ qua.
	ProjectRulesDir string
}

// ainovelDirName là tên dotdir ainovel dùng chung ở hai cấp user / project.
// Nhờ đó global ~/.ainovel/rules/ và project ./.ainovel/rules/ đối xứng nhau.
const ainovelDirName = ".ainovel"

// DefaultProjectRulesDir ghép đường dẫn tuyệt đối của ./.ainovel/rules/ (dựa trên thư mục project đã cho).
// Bên gọi truyền project root để tránh phụ thuộc cwd bên trong loader; đối xứng DefaultHomeRulesDir.
func DefaultProjectRulesDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, ainovelDirName, "rules")
}

// DefaultHomeRulesDir ghép đường dẫn tuyệt đối tới thư mục ~/.ainovel/rules/.
// Nếu không phân giải được home thì trả về chuỗi rỗng (bên gọi dựa vào đó để bỏ qua nguồn này).
func DefaultHomeRulesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ainovelDirName, "rules")
}

// homeRulesReadme là hướng dẫn được ghi vào ~/.ainovel/rules/README.txt trong lần khởi tạo đầu tiên.
// Cố ý dùng hậu tố .txt thay vì .md; trình quét chỉ nhận .md nên hướng dẫn này sẽ không bị chuẩn hóa như quy tắc.
const homeRulesReadme = `Đặt các sở thích viết toàn cục tại đây; chúng có hiệu lực với mọi sách.

Tạo một tệp .md mới (ví dụ my-style.md), chỉ cần viết yêu cầu bằng lời tự nhiên:
Không cần định dạng đặc biệt, không cần YAML:

    # Nhân vật
    - Đừng viết nhân vật chính Lâm Trần thành kiểu thánh mẫu; chỉ cần ngoài lạnh trong ấm
    # Phong cách
    - Dùng cảm giác cơ thể (khớp ngón tay trắng bệch) thay cho nhãn cảm xúc (căng thẳng)
    - Đối thoại đừng quá văn viết, mỗi chương khoảng 3000 chữ
    - Đừng dùng giọng AI kiểu "ở mức độ nào đó"

Viết xong không cần lo định dạng: hệ thống sẽ dùng mô hình để chuẩn hóa các yêu cầu ngôn ngữ tự nhiên này thành ràng buộc có cấu trúc
(khoảng số từ, từ cấm, ngưỡng từ gây mệt mỏi, v.v.), tự động tuân thủ khi viết và tự kiểm tra khi submit.

Nhiều tệp .md được hợp nhất theo thứ tự từ điển tên tệp; tệp ẩn bắt đầu bằng dấu chấm và tệp không phải .md đều bị bỏ qua
(vì vậy README.txt này sẽ không bị xem là rules).

Baseline cơ học cho các câu sáo AI và từ gây mệt mỏi thường gặp đã được tích hợp sẵn; dùng ngay được, không viết thêm cũng không sao.

Ưu tiên tải (cao -> thấp): ./.ainovel/rules/*.md (sách này) > ~/.ainovel/rules/*.md (tại đây) > mặc định tích hợp
`

// EnsureHomeRulesDir cố gắng tạo thư mục ~/.ainovel/rules/ và ghi README.txt hướng dẫn,
// giúp người dùng phát hiện điểm mở rộng sở thích global này và biết cách viết.
// nice-to-have, không thuộc đường trọng yếu: nếu phân giải home thất bại hoặc ghi lỗi thì âm thầm bỏ qua, tuyệt đối không chặn khởi động.
func EnsureHomeRulesDir() {
	if dir := DefaultHomeRulesDir(); dir != "" {
		_ = ensureRulesDirAt(dir)
	}
}

// ensureRulesDirAt tạo thư mục và ghi README.txt thành mẫu hướng dẫn hiện tại; đây là lõi có thể test của EnsureHomeRulesDir.
// README.txt là tệp hướng dẫn do hệ thống tạo (sở thích người dùng viết trong *.md; nó không bị quét/tải), mỗi lần đều ghi đè bằng
// mẫu mới nhất; không giữ nội dung cũ nên cũng không cần logic tương thích phiên bản.
func ensureRulesDirAt(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(homeRulesReadme), 0o644)
}

// DefaultOptions dựng LoadOptions thường dùng dựa trên thư mục làm việc hiện tại.
//
// Phù hợp để Host gọi một lần khi khởi động, để service user rules tái dùng cùng cấu hình nguồn.
// Khi phân giải cwd thất bại, ProjectRulesDir để rỗng (trình quét sẽ bỏ qua nguồn này).
//
// Ngữ nghĩa đường dẫn: ProjectRulesDir gắn với **thư mục làm việc hiện tại (cwd)** chứ không phải outputDir.
// Người dùng cd tới thư mục khác để khởi động viết sách khác; ./.ainovel/rules/ tự nhiên đi theo cwd. Nếu cần chia sẻ giữa các sách,
// chỉ cần đặt trong thư mục global ~/.ainovel/rules/ (mọi .md bên dưới sẽ được tải).
func DefaultOptions() LoadOptions {
	cwd, _ := os.Getwd()
	return LoadOptions{
		HomeRulesDir:    DefaultHomeRulesDir(),
		ProjectRulesDir: DefaultProjectRulesDir(cwd),
	}
}
