package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
)

func TestRenderTopBarShowsVersion(t *testing.T) {
	out := renderTopBar(host.UISnapshot{
		Provider:  "openrouter",
		ModelName: "test-model",
		BookTitle: "Tiểu thuyết kiểm thử",
	}, 120, "", "v1.2.3")
	if !strings.Contains(out, "ainovel-cli v1.2.3") {
		t.Fatalf("top bar missing version: %q", out)
	}
}

func TestRenderDetailContentShowsSynopsis(t *testing.T) {
	out := ansi.Strip(renderDetailContent(host.UISnapshot{Synopsis: "Thiếu niên đi tìm bình minh trong đêm vĩnh cửu."}, 80))
	if !strings.Contains(out, "Giới thiệu") || !strings.Contains(out, "Thiếu niên đi tìm bình minh trong đêm vĩnh cửu.") {
		t.Fatalf("detail panel missing synopsis: %q", out)
	}
}

func TestSameDetailSnapshotDetectsOutlineStateChanges(t *testing.T) {
	base := host.UISnapshot{Outline: []host.OutlineSnapshot{{Chapter: 1, Title: "Chương một"}}}
	if !sameDetailSnapshot(base, base) {
		t.Fatal("Chi tiết giống nhau không nên kích hoạt tạo lại")
	}
	changed := base
	changed.InProgressChapter = 1
	if sameDetailSnapshot(base, changed) {
		t.Fatal("Thay đổi trạng thái Chương phải kích hoạt tạo lại Chi tiết")
	}
}

func TestRenderErrorEventKeepsOneLineSummary(t *testing.T) {
	out := ansi.Strip(renderEventLine(host.Event{
		Time:     time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
		Category: "ERROR",
		Summary:  "lỗi tham số commit_chapter: " + strings.Repeat("Dấu vết hé lộ từ tài liệu", 20),
	}, 60, 0))
	if strings.Contains(out, "\n") {
		t.Fatalf("Sự kiện ERROR phải giữ tóm tắt một dòng, got %q", out)
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("Tóm tắt ERROR quá rộng phải bị cắt ngắn trong TUI, got %q", out)
	}
}

// TestRenderStatusBar bảo vệ hợp đồng thông tin của thanh trạng thái dưới cùng: danh tính Mô hình (cửa sổ + suy nghĩ), token phiên,
// chi phí/ngân sách, thư mục sách đều phải có (sau khi loại bỏ style thì assert theo văn bản thuần).
func TestRenderStatusBar(t *testing.T) {
	out := ansi.Strip(renderStatusBar(host.UISnapshot{
		Provider:           "openrouter",
		ModelName:          "test-model",
		ModelContextWindow: 200000,
		ThinkingLevel:      "medium",
		TotalInputTokens:   1_234_000,
		TotalOutputTokens:  89_300,
		TotalCostUSD:       0.31,
		BudgetLimitUSD:     5,
		TotalSavedUSD:      0.12,
	}, "/tmp/output", 120))
	for _, want := range []string{"test-model(200K,med)", "↑1.2M", "↓89.3k", "$0.31/$5.00", "tiết kiệm $0.12", "./output"} {
		if !strings.Contains(out, want) {
			t.Fatalf("thanh trạng thái thiếu %q: %q", want, out)
		}
	}
}

func TestRenderStatusBarAutoThinkingAndEmpty(t *testing.T) {
	out := ansi.Strip(renderStatusBar(host.UISnapshot{
		ModelName:          "test-model",
		ModelContextWindow: 128000,
	}, "", 120))
	if !strings.Contains(out, "test-model(128K,auto)") {
		t.Fatalf("thiếu chú thích ngoặc về mức suy nghĩ auto: %q", out)
	}
	if out := ansi.Strip(renderStatusBar(host.UISnapshot{}, "", 120)); out != "SẴN SÀNG" {
		t.Fatalf("snapshot rỗng phải fallback về SẴN SÀNG, got %q", out)
	}
}

func TestRenderUsageLineSeparatesFullWidthNameAndTokens(t *testing.T) {
	out := renderUsageLine("gpt-5.6-sol", bodyTextColor, 5300, 0, 0.23, 32)
	if !strings.Contains(out, "gpt-5.6-sol 5.3k") {
		t.Fatalf("model name and tokens should have a visible gap: %q", out)
	}
}

func TestTruncateByDisplayWidth(t *testing.T) {
	// Văn bản Trung bình thuần bị cắt theo độ rộng hiển thị: ngân sách 10 cột = 3 ký tự rộng (6 cột) + "..."(3 cột), cắt theo rune sẽ tràn tới 17 cột
	got := truncate("kiểm toán viên đạo đức thuật toán công cộng thành phố Lâm Cảng", 10)
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("truncate tràn độ rộng cột: %d > 10 (%q)", w, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("cắt ngắn quá rộng phải kèm dấu ba chấm: %q", got)
	}
	// Hành vi ASCII nhất quán với triển khai cũ
	if got := truncate("abcdef", 6); got != "abcdef" {
		t.Errorf("chưa quá rộng thì không nên cắt ngắn: %q", got)
	}
	if got := truncate("abcdefgh", 6); got != "abc..." {
		t.Errorf("cắt ngắn ASCII: got %q want %q", got, "abc...")
	}
}

func TestRenderDetailContentWrapsCJK(t *testing.T) {
	long := "Thẩm Nghiễn (nhân vật chính; kiểm toán viên đạo đức thuật toán công cộng thành phố Lâm Cảng, người phụ trách điều tra sự cố đêm bão, kiên trì công lý thủ tục)"
	const contentW = 40
	out := renderDetailContent(host.UISnapshot{
		Characters:       []string{long},
		SupportingCount:  1,
		RecentSupporting: []string{long},
		RecentSummaries:  []string{"Chương 6: " + long},
	}, contentW)
	for line := range strings.SplitSeq(out, "\n") {
		if w := lipgloss.Width(line); w > contentW {
			t.Errorf("dòng tràn độ rộng panel: %d > %d (%q)", w, contentW, line)
		}
	}
	// Mô tả dài phải được gập thành nhiều dòng (dòng tiếp theo thụt lề treo), thay vì cắt ngắn làm mất thông tin
	joined := strings.ReplaceAll(strings.ReplaceAll(out, "\n", ""), " ", "")
	if !strings.Contains(joined, strings.ReplaceAll("kiên trì công lý thủ tục", " ", "")) {
		t.Errorf("sau khi gập dòng phải giữ mô tả đầy đủ, Xuất thực tế:\n%s", out)
	}
}
