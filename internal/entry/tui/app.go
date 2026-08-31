package tui

import (
	"fmt"
	"log/slog"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	buildversion "github.com/voocel/ainovel-cli/internal/version"
)

// Run khởi động TUI。
// Quy ước phân tầng chế độ khởi động：
// 1. chế độ nhanh và đồng sáng tạo thuộc “điều phối khởi động”;
// 2. phiên sáng tác chính thức đi vào host.Host；
// 3. nếu sau này thêm các chế độ dùng chung như “viết tiếp tiểu thuyết có sẵn”, đều đưa vào internal/entry/startup。
func Run(cfg bootstrap.Config, bundle assets.Bundle, build buildversion.Info) error {
	rt, err := host.New(cfg, bundle, host.WithFileLog("tui.log", false,
		slog.String("version", build.Version),
		slog.String("commit", build.Commit),
		slog.String("built", build.Date),
	))
	if err != nil {
		return err
	}
	defer rt.Close()

	m := NewModel(rt, build.Version)
	if logErr := rt.FileLogError(); logErr != nil {
		logWarning := fmt.Errorf("log file không khả dụng, đã tiếp tục dùng log terminal：%w", logErr)
		m.err = logWarning
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: logWarning.Error(), Detail: logWarning.Error(),
		})
	}
	// không bật báo chuột toàn cục khi khởi động: trang chào mừng không cần chuột, tắt báo chuột để giữ
	// kéo chọn để sao chép nguyên bản. Khi vào bàn làm việc sáng tạo (modeRunning), enterRunning sẽ bật báo cáo,
	// để hỗ trợ nhấp để chuyển panel / con lăn / kéo thả thanh bên.
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}
