// Package notify cung cấp kênh cảnh báo cho chế độ không có người trực.
//
// Vị trí theo kiến trúc (architecture.md §2.3): hành động thuần quan sát. Cảnh báo không bao giờ can thiệp luồng điều khiển
// như retry, đổi phân công hay dừng máy; nó chỉ đưa sự kiện đã có trong TUI ra ngoài màn hình.
// Send chạy bất đồng bộ, không bao giờ chặn Host; lỗi chỉ được ghi bằng slog.
package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Notification chứa toàn bộ dữ kiện của một cảnh báo.
type Notification struct {
	Kind  string `json:"kind"`  // Tên sự kiện ổn định do Kinds trả về
	Level string `json:"level"` // info / warn / error
	Title string `json:"title"`
	Body  string `json:"body"`
}

const (
	KindRunEnd        = "run_end"
	KindBudget        = "budget"
	KindAdvanceGate   = "advance_gate"
	KindStopGuard     = "stop_guard"
	KindPlanStart     = "plan_start"
	KindDeadlock      = "deadlock"
	KindWorkerFailure = "worker_failure"
)

// Kinds trả về mọi tên sự kiện mà phiên bản hiện tại cho phép dùng trong notify.events.
// Đây là nguồn sự thật duy nhất của hợp đồng sự kiện thông báo.
func Kinds() []string {
	return []string{
		KindRunEnd,
		KindBudget,
		KindAdvanceGate,
		KindStopGuard,
		KindPlanStart,
		KindDeadlock,
		KindWorkerFailure,
	}
}

func IsKnownKind(kind string) bool {
	for _, known := range Kinds() {
		if kind == known {
			return true
		}
	}
	return false
}

// Notifier phân phối thông báo theo cấu hình. Giá trị zero không dùng được, phải tạo qua New; nil an toàn vì Send là noop.
type Notifier struct {
	command string          // Không rỗng thì thay kênh system; đẩy thông báo điện thoại đi qua đây
	events  map[string]bool // nil = cho phép mọi kind
	timeout time.Duration
}

// New tạo Notifier. Nếu command rỗng thì dùng kênh system tích hợp: bong bóng Windows,
// macOS osascript hoặc Linux notify-send; nếu events không rỗng thì chỉ cho phép kind đã liệt kê.
func New(command string, events []string) *Notifier {
	n := &Notifier{command: strings.TrimSpace(command), timeout: 10 * time.Second}
	if len(events) > 0 {
		n.events = make(map[string]bool, len(events))
		for _, ev := range events {
			n.events[ev] = true
		}
	}
	return n
}

// Send gửi một thông báo bất đồng bộ. Lọc, thực thi và xử lý lỗi đều không ảnh hưởng caller.
func (n *Notifier) Send(nt Notification) {
	if !n.allows(nt.Kind) {
		return
	}
	go n.deliver(nt)
}

// allows trả về kind này có được cho phép không; nil Notifier hoặc kind không nằm trong events sẽ bị chặn.
func (n *Notifier) allows(kind string) bool {
	if n == nil {
		return false
	}
	return n.events == nil || n.events[kind]
}

// deliver thực hiện một lần gửi đồng bộ và ghi lỗi; Send gọi hàm này trong goroutine.
func (n *Notifier) deliver(nt Notification) {
	if err := n.deliverError(nt); err != nil {
		slog.Warn("Gửi thông báo thất bại", "module", "notify", "kind", nt.Kind, "err", err)
	}
}

// deliverError thực hiện một lần gửi đồng bộ và trả lỗi gốc. Send gọi deliver trong goroutine để ghi lỗi;
// test gọi trực tiếp hàm này để tránh lỗi bị che bởi triệu chứng phụ.
func (n *Notifier) deliverError(nt Notification) error {
	ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
	defer cancel()

	if n.command != "" {
		return runCommand(ctx, n.command, nt)
	}
	return runSystem(ctx, nt)
}

// runCommand thực thi lệnh do người dùng cấu hình: các trường đi qua biến môi trường, phù hợp một dòng curl, không thêm phụ thuộc và tránh injection.
// JSON đầy đủ cũng được ghi vào stdin để kịch bản phân phối phức tạp tự xử lý. Hết hạn thì ctx cưỡng bức dừng.
func runCommand(ctx context.Context, command string, nt Notification) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		powershell, err := findPowerShell()
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Env = notificationEnv(nt)
	payload, _ := json.Marshal(nt)
	cmd.Stdin = strings.NewReader(string(payload))
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("lệnh thông báo quá hạn: %w", ctxErr)
		}
		return err
	}
	return nil
}

func notificationEnv(nt Notification) []string {
	return append(os.Environ(),
		"NOTIFY_KIND="+nt.Kind,
		"NOTIFY_LEVEL="+nt.Level,
		"NOTIFY_TITLE="+nt.Title,
		"NOTIFY_BODY="+nt.Body,
	)
}

// runSystem gửi thông báo desktop tích hợp: chỉ bao phủ tình huống người dùng đang ở gần máy; không tìm thấy lệnh thì giảm cấp im lặng.
func runSystem(ctx context.Context, nt Notification) error {
	switch runtime.GOOS {
	case "windows":
		return runWindowsNotification(ctx, nt)
	case "darwin":
		script := "display notification " + appleScriptString(nt.Body) + " with title " + appleScriptString(nt.Title)
		return exec.CommandContext(ctx, "osascript", "-e", script).Run()
	case "linux":
		if _, err := exec.LookPath("notify-send"); err != nil {
			slog.Info("Thông báo giảm cấp thành log vì không có notify-send", "module", "notify", "title", nt.Title, "body", nt.Body)
			return nil
		}
		return exec.CommandContext(ctx, "notify-send", nt.Title, nt.Body).Run()
	default:
		slog.Info("Thông báo giảm cấp thành log vì nền tảng không có kênh system", "module", "notify", "title", nt.Title, "body", nt.Body)
		return nil
	}
}

// runWindowsNotification dùng PowerShell tích hợp của hệ thống cùng WinForms NotifyIcon.
// Windows 10/11 hiển thị bong bóng ở góc trên bên phải và đưa vào trải nghiệm thông báo hệ thống; không cần cài module, đăng ký app
// hay mang thêm binary. Caller vốn chạy bất đồng bộ; giữ sống ngắn chỉ để hệ thống nhận thông báo bong bóng.
func runWindowsNotification(ctx context.Context, nt Notification) error {
	powershell, err := findPowerShell()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, powershell,
		"-NoLogo", "-NoProfile", "-NonInteractive", "-STA", "-Command", windowsNotificationScript)
	cmd.Env = notificationEnv(nt)
	return cmd.Run()
}

func findPowerShell() (string, error) {
	// Ưu tiên PowerShell 7: GitHub Windows runner và môi trường Windows hiện đại có pwsh
	// ổn định hơn với stdin redirect; Windows PowerShell 5.1 chỉ là fallback tương thích.
	for _, name := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("thông báo Windows cần PowerShell, nhưng hệ thống không tìm thấy powershell.exe hoặc pwsh.exe")
}

const windowsNotificationScript = `$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$notify = New-Object System.Windows.Forms.NotifyIcon
$notify.Icon = [System.Drawing.SystemIcons]::Information
$notify.BalloonTipTitle = $env:NOTIFY_TITLE
$notify.BalloonTipText = $env:NOTIFY_BODY
$notify.BalloonTipIcon = switch ($env:NOTIFY_LEVEL) {
  'error' { [System.Windows.Forms.ToolTipIcon]::Error; break }
  'warn'  { [System.Windows.Forms.ToolTipIcon]::Warning; break }
  default { [System.Windows.Forms.ToolTipIcon]::Info }
}
$notify.Visible = $true
$notify.ShowBalloonTip(4000)
Start-Sleep -Milliseconds 4500
$notify.Dispose()`

// appleScriptString bọc văn bản bất kỳ thành literal chuỗi AppleScript.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
