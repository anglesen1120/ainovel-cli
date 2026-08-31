package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/exp"
)

// exportDoneMsg là kết quả cuối cùng của lệnh /export.
//
// Không như /import đi theo Luồng sự kiện: export là IO cục bộ đồng bộ, không có tiến độ trung gian nào để nói tới;
// Chạy xong trong goroutine rồi mới gửi trả lại tin nhắn này một lần.
type exportDoneMsg struct {
	result *exp.Result
	err    error
}

// startExport phân tích tham số và trả về tea.Cmd.
// Việc export thực sự chạy trong tea.Cmd (để tránh chặn UI), và sau khi hoàn tất sẽ gửi exportDoneMsg.
func startExport(rt *host.Host, args []string) (tea.Cmd, error) {
	opts, err := parseExportArgs(args)
	if err != nil {
		return nil, err
	}
	return func() tea.Msg {
		res, err := rt.Export(context.Background(), opts)
		return exportDoneMsg{result: res, err: err}
	}, nil
}

// parseExportArgs phân tích `/export [path] [from=N] [to=M] [--overwrite]`。
//
// Tham số vị trí: tối đa một, dùng làm đường dẫn xuất; mặc định do exp.Run quyết định ({novelDir}/{BookMetadata.Title}.txt).
func parseExportArgs(args []string) (exp.Options, error) {
	var opts exp.Options
	for _, a := range args {
		if a == "--overwrite" {
			opts.Overwrite = true
			continue
		}
		if k, v, ok := strings.Cut(a, "="); ok {
			switch strings.ToLower(k) {
			case "from":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return exp.Options{}, fmt.Errorf("from phải là số nguyên không âm：%q", v)
				}
				opts.From = n
			case "to":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return exp.Options{}, fmt.Errorf("to phải là số nguyên không âm：%q", v)
				}
				opts.To = n
			default:
				return exp.Options{}, fmt.Errorf("tham số không xác định %q (hỗ trợ: from / to)", k)
			}
			continue
		}
		if strings.HasPrefix(a, "-") {
			return exp.Options{}, fmt.Errorf("flag không xác định %q", a)
		}
		if opts.OutPath != "" {
			return exp.Options{}, fmt.Errorf("chỉ hỗ trợ một tham số đường dẫn: %q", a)
		}
		opts.OutPath = a
	}
	return opts, nil
}

// formatExportSuccess hiển thị Result thành Summary của sự kiện.
func formatExportSuccess(res *exp.Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✓ Đã xuất %d chương / %s tới %s", res.Chapters, humanBytes(res.Bytes), res.Path)
	if n := len(res.Skipped); n > 0 {
		fmt.Fprintf(&b, "（bỏ qua %d chương chưa hoàn tất: %s）", n, briefIntList(res.Skipped, 5))
	}
	return b.String()
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

func briefIntList(xs []int, max int) string {
	if len(xs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(xs))
	for i, x := range xs {
		if i >= max {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, strconv.Itoa(x))
	}
	return strings.Join(parts, ",")
}
