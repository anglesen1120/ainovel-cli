package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStateContent sinh nội dung thuần của sidebar trạng thái(không gồm viền/khung ngoài), để stateVP.SetContent sử dụng.
func renderStateContent(snap host.UISnapshot, contentW int) string {
	contentW = max(12, contentW)
	agents := sidebarAgents(snap.Agents)
	idleAgents := sidebarIdleAgents(snap.Agents)
	var sections []string

	if snap.RecoveryLabel != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(colorMuted).Italic(true).
			Render(truncate(snap.RecoveryLabel, contentW)))
	}

	var overview strings.Builder
	overview.WriteString(renderField("Trạng thái", snapshotRuntimeStateLabel(snap.RuntimeState)))
	overview.WriteString(renderField("Giai đoạn", snapshotPhaseLabel(snap.Phase)))
	overview.WriteString(renderField("Luồng", snapshotFlowLabel(snap.Flow)))
	if snap.AdvanceMode == "review" {
		advance := "Duyệt từng chương"
		if snap.AdvancePermitChapter > 0 {
			advance = fmt.Sprintf("Đã cho qua chương %d", snap.AdvancePermitChapter)
		}
		overview.WriteString(renderField("Tiến trình", advance))
	} else if snap.AdvanceMode == "auto" {
		overview.WriteString(renderField("Tiến trình", "Tự động"))
	}
	if snap.Layered {
		overview.WriteString(renderField("Đã hoàn tất", fmt.Sprintf("%d chương", snap.CompletedCount)))
		// quy hoạch động phân tầng: cột phải chỉ hiển thị các chương đã bung của arc hiện tại, "Đã lập kế hoạch" cũng dùng cùng một tiêu chí,
		// nếu không sẽ trộn ước tính thô EstimatedChapters của arc khung (như 92) vào đây, không khớp dàn ý đang thấy.
		// progress.TotalChapters giá trị đó chỉ dùng cho quyết định ContextProfile nội bộ, không để lộ ra UI。
		if planned := len(snap.Outline); planned > 0 {
			overview.WriteString(renderField("Đã lập kế hoạch", fmt.Sprintf("%d chương", planned)))
		}
	} else {
		switch {
		case snap.TotalChapters > 0:
			overview.WriteString(renderField("Tiến độ", fmt.Sprintf("%d / %d chương", snap.CompletedCount, snap.TotalChapters)))
		default:
			overview.WriteString(renderField("Đã hoàn tất", fmt.Sprintf("%d chương", snap.CompletedCount)))
		}
	}
	overview.WriteString(renderField("Số chữ", formatNumber(snap.TotalWordCount)))
	if label, ch := inProgressDisplay(snap); label != "" {
		overview.WriteString(renderField(label, fmt.Sprintf("Chương %d", ch)))
	}
	if headline := snapshotHeadline(snap); headline != "" {
		label := "Hiện tại"
		if !snap.IsRunning {
			label = "Chờ khôi phục"
		}
		overview.WriteString(renderHighlightField(label, truncate(headline, contentW-10)))
	}
	sections = append(sections, renderSidebarSection("Tổng quan", overview.String(), contentW))

	if len(agents) > 0 {
		var agentBody strings.Builder
		for _, agent := range agents {
			agentBody.WriteString(renderAgentLine(agent, contentW))
			agentBody.WriteString("\n")
		}
		if len(idleAgents) > 0 {
			agentBody.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Chờ: " + truncate(strings.Join(idleAgents, " · "), max(8, contentW-2))))
			agentBody.WriteString("\n")
		}
		sections = append(sections, renderSidebarSection("Vai trò đang chạy", agentBody.String(), contentW))
	}

	if len(snap.PendingRewrites) > 0 {
		var rewrite strings.Builder
		rewrite.WriteString(renderHighlightField("Hàng đợi", fmt.Sprintf("%v", snap.PendingRewrites)))
		if snap.RewriteReason != "" {
			rewrite.WriteString(renderField("Lý do", truncate(snap.RewriteReason, contentW-10)))
		}
		sections = append(sections, renderSidebarSection("Viết lại", rewrite.String(), contentW))
	}

	if snap.PendingSteer != "" {
		sections = append(sections, renderSidebarSection("Can thiệp",
			renderHighlightField("Chờ xử lý", truncate(snap.PendingSteer, contentW-10)), contentW))
	}
	if snap.HasAdvanceHold {
		sections = append(sections, renderSidebarSection("Điểm dừng duyệt",
			renderHighlightField("Đang chờ", truncate(snap.AdvanceHoldReason, contentW-10)), contentW))
	}

	if body := renderUsageSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Mức dùng", body, contentW))
	}

	if body := renderCacheSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Bộ nhớ đệm", body, contentW))
	}

	return strings.Join(sections, "\n\n")
}

func renderAgentLine(agent host.AgentSnapshot, width int) string {
	stateColor := taskStatusColor(agent.State)
	icon := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateIcon(agent.State))
	badge := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateLabel(agent.State))
	name := lipgloss.NewStyle().Bold(true).Foreground(bodyTextColor).Render(agentDisplayName(agent.Name))
	line := icon + " " + name + " " + badge

	taskLine := agentTaskLine(agent)
	if taskLine != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Render("  "+truncate(taskLine, max(8, width-2)))
	}

	detail := agent.Summary
	if agent.Tool != "" {
		detail = agent.Tool
	}
	if agent.State == "idle" && detail == "Chờ" {
		detail = ""
	}
	if detail != "" && detail != taskLine {
		line += "\n" + lipgloss.NewStyle().Foreground(colorMuted).Render("  "+truncate(detail, max(8, width-2)))
	}
	if ctx := agentContextLine(agent); ctx != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("  "+truncate(ctx, max(8, width-2)))
	}
	return line
}

func renderSidebarSection(title, body string, width int) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	lineW := max(0, width-lipgloss.Width(title)-1)
	header := panelTitleStyle.Render(title) + " " +
		lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", lineW))
	card := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorDim).
		PaddingLeft(1).
		Render(body)
	return header + "\n" + card
}

func sidebarAgents(agents []host.AgentSnapshot) []host.AgentSnapshot {
	var out []host.AgentSnapshot
	for _, agent := range agents {
		if agent.State == "idle" {
			continue
		}
		out = append(out, agent)
	}
	if len(out) == 0 {
		out = append(out, agents...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := out[i], out[j]
		if agentStateRank(li.State) != agentStateRank(lj.State) {
			return agentStateRank(li.State) < agentStateRank(lj.State)
		}
		return agentOrder(li.Name) < agentOrder(lj.Name)
	})
	return out
}

func sidebarIdleAgents(agents []host.AgentSnapshot) []string {
	var names []string
	hasActive := false
	for _, agent := range agents {
		if agent.State != "idle" {
			hasActive = true
			continue
		}
		names = append(names, agentDisplayName(agent.Name))
	}
	if !hasActive {
		return nil
	}
	sort.Strings(names)
	return names
}

// inProgressDisplay tính nhãn và số chương cho trường “đang chạy”。
// chọn động từ theo flow (trau chuốt/viết lại/viết); in_progress_chapter và flow không khớp thì xem là stale:
//   - ở chế độ polishing/rewriting, chương không nằm trong pending_rewrites -> quay về Hàng đợi chương đầu
//   - không render khi trường là 0
func inProgressDisplay(snap host.UISnapshot) (label string, chapter int) {
	ch := snap.InProgressChapter
	switch snap.Flow {
	case "polishing":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "trau chuốtTrung bình", ch
	case "rewriting":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "viết lạiTrung bình", ch
	default:
		if ch <= 0 {
			return "", 0
		}
		return "viếtTrung bình", ch
	}
}

func snapshotHeadline(snap host.UISnapshot) string {
	if snap.PendingSteer != "" {
		if !snap.IsRunning {
			return "Chờ khôi phục：Xử lý can thiệp của người dùng"
		}
		return "Đang chờ xử lý can thiệp của người dùng"
	}
	if len(snap.PendingRewrites) > 0 {
		if !snap.IsRunning {
			return "Chờ khôi phục：Xử lý viết lại"
		}
		return "Đang chờ xử lý viết lại"
	}
	if snap.AdvanceMode == "review" && !snap.IsRunning && snap.Phase == "writing" {
		return "Duyệt từng chương：Đang chờ mở khóa chương tiếp theo"
	}
	return ""
}

func snapshotPhaseLabel(phase string) string {
	switch phase {
	case "premise":
		return "Tiền đề"
	case "outline":
		return "Dàn ý"
	case "writing":
		return "viết"
	case "complete":
		return "Hoàn tất"
	case "init":
		return "Khởi tạo"
	default:
		if phase == "" {
			return "-"
		}
		return phase
	}
}

func snapshotRuntimeStateLabel(state string) string {
	switch state {
	case "running":
		return "Đang chạy"
	case "pausing":
		return "Tạm dừng trung bình"
	case "paused":
		return "Đã tạm dừng"
	case "completed":
		return "Đã hoàn tất"
	default:
		return "Rảnh"
	}
}

func snapshotFlowLabel(flow string) string {
	switch flow {
	case "":
		return "-"
	case "writing":
		return "viết"
	case "reviewing":
		return "Thẩm định"
	case "rewriting":
		return "viết lại"
	case "polishing":
		return "trau chuốt"
	case "steering":
		return "Can thiệp"
	default:
		return flow
	}
}

func renderUsageSidebar(snap host.UISnapshot, width int) string {
	if snap.TotalInputTokens <= 0 && snap.TotalOutputTokens <= 0 && snap.TotalCostUSD <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderField("Nhập", formatTokensCompact(snap.TotalInputTokens)))
	b.WriteString(renderField("Xuất", formatTokensCompact(snap.TotalOutputTokens)))
	if cost := formatCostUSD(snap.TotalCostUSD); cost != "" {
		b.WriteString(renderField("Chi phí", cost))
	}
	if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
		b.WriteString(renderField("Tiết kiệm", saved))
	}
	if snap.BudgetLimitUSD > 0 {
		pct := snap.TotalCostUSD / snap.BudgetLimitUSD * 100
		b.WriteString(renderField("Ngân sách", fmt.Sprintf("$%.2f/$%.2f (%.0f%%)", snap.TotalCostUSD, snap.BudgetLimitUSD, pct)))
	}

	agentStats := usageStatsByCost(snap.CachePerAgent)
	if len(agentStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Vai trò", width))
		limit := min(len(agentStats), 4)
		for i := 0; i < limit; i++ {
			a := agentStats[i]
			b.WriteString(renderUsageLine(agentDisplayName(a.Role), eventAgentColor(a.Role), a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	modelStats := usageStatsByCost(snap.CachePerModel)
	if len(modelStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Mô hình", width))
		limit := min(len(modelStats), 4)
		for i := 0; i < limit; i++ {
			a := modelStats[i]
			b.WriteString(renderUsageLine(modelDisplayName(a.Model), bodyTextColor, a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func usageStatsByCost(in []host.AgentCacheStat) []host.AgentCacheStat {
	out := append([]host.AgentCacheStat(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input+out[i].Output > out[j].Input+out[j].Output
	})
	return out
}

func renderUsageGroupHeader(label string, width int) string {
	line := lipgloss.NewStyle().Foreground(colorDim).
		Render(strings.Repeat("·", max(8, width-lipgloss.Width(label)-3)))
	return lipgloss.NewStyle().Foreground(colorMuted).Render(label+" ") + line + "\n"
}

func renderUsageLine(name string, color lipgloss.TerminalColor, input, output int, cost float64, width int) string {
	nameW := 11
	if width < 24 {
		nameW = 8
	}
	nameCell := lipgloss.NewStyle().Foreground(color).Width(nameW).
		Render(truncate(name, nameW))
	tokens := formatTokensCompact(input + output)
	right := tokens
	if costStr := formatCostUSD(cost); costStr != "" {
		right += " · " + costStr
	}
	// khi tên vừa khít độ rộng cột cố định thì padding sẽ không để lại khoảng trắng cuối; thêm dấu phân cách rõ ràng để tránh
	// "gpt-5.6-sol5.3k" kiểu tên mô hình dính với Mức dùng.
	return fitInlineLine(nameCell+" "+lipgloss.NewStyle().Foreground(colorDim).Render(right), width)
}

func modelDisplayName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "unknown"
	}
	parts := strings.Split(model, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[1:], "/")
	}
	if len(parts) == 2 {
		return parts[1]
	}
	return model
}

// renderCacheSidebar hiển thị khối "Bộ nhớ đệm" ở cột trái.
//
// Ba trạng thái:
//  1. Chưa tiêu thụ token nào: trả về rỗng, section không hiển thị
//  2. Toàn bộ vai trò của phiên hiện tại đang dùng mô hình không hỗ trợ prompt cache: chỉ hiển thị một dòng nhắc "Chưa bật"
//  3. Đã bật: chỉ số trên cùng "tỷ lệ trúng trung bình tích lũy/10 gần đây · tiết kiệm · đọc/ghi" + phân cách + các dòng theo vai trò
//
// Các dòng theo vai trò khi có hỗ trợ sẽ hiển thị cặp số "tích lũy/10 gần đây%"; khi không hỗ trợ thì hiển thị "Chưa bật".
// So sánh giữa tích lũy và N lần gần đây giúp nhận ra "giai đoạn đầu kéo xuống" so với "trạng thái ổn định trúng thấp".
func renderCacheSidebar(snap host.UISnapshot, width int) string {
	// streaming phía trên không gửi chunk usage cuối của OpenAI — toàn bộ dữ liệu tích lũy đều bằng 0,
	// nhưng đây không phải là "chưa bật cache" cũng không phải "mức dùng quá thấp bị chặn ẩn đi", phải báo rõ,
	// nếu không người dùng sẽ cứ nghĩ là mã Bộ nhớ đệm đã có ở cột trái mà lại không hiện ra. Ưu tiên cao nhất.
	if snap.MissingAssistantUsage > 0 && snap.TotalInputTokens <= 0 {
		warn := lipgloss.NewStyle().Foreground(colorError).Bold(true).
			Render(fmt.Sprintf("⚠ upstream chưa trả usage (%d lần)", snap.MissingAssistantUsage))
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("kiểm tra provider stream_options.include_usage", max(8, width-2)))
		return warn + "\n" + hint + "\n"
	}

	if snap.TotalInputTokens <= 0 && snap.TotalCacheWriteTokens <= 0 {
		return ""
	}

	// toàn bộ thời gian đều Chưa bật -> hiển thị một dòng giải thích, tránh người dùng hiểu sai là "0% trúng trung bình cần kiểm tra"
	if !snap.OverallCacheCapable && snap.TotalCacheReadTokens == 0 && snap.TotalCacheWriteTokens == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("Hiện tạiMô hìnhChưa bật prompt cache", max(8, width-2))) + "\n"
	}

	var b strings.Builder

	// các chỉ số tổng hợp ở trên cùng: tích lũy + 10 gần đây mỗi thứ một dòng, nhãn ghi rõ để tránh
	// ba kiểu dấu phân tách (phần trăm / chấm trúng trung bình / chữ) lẫn lộn làm mơ hồ ý nghĩa.
	overallHit := cacheHitRate(snap.TotalCacheReadTokens, snap.TotalInputTokens)
	b.WriteString(renderField("Tỷ lệ trúng trung bình tích lũy", colorPercent(overallHit)))
	if snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
		recent := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
		b.WriteString(renderField(fmt.Sprintf("Tỷ lệ trúng trung bình %d gần đây", snap.OverallRecentSamples), colorPercent(recent)))
	}

	if savedStr := formatCostUSD(snap.TotalSavedUSD); savedStr != "" {
		b.WriteString(renderField("Tiết kiệm", savedStr))
	}

	// lượng đọc/ghi tách thành hai dòng. Lượng ghi bằng 0 trong các họ Giao thức OpenAI / Gemini là bình thường —
	// hai bên này dùng caching tự động minh bạch, việc ghi cache hoàn toàn miễn phí (lần đầu chưa trúng tính theo giá nhập bình thường,
	// dựng cache không thu thêm phụ phí nào), nên bản thân Giao thức không lộ trường cache_creation, không cần thiết.
	// chỉ họ Anthropic / Bedrock mới báo lượng ghi, vì họ tính phí ghi thêm (5m +25%/1h +100%),
	// cần đưa lượng này cho người dùng để tính phí.
	b.WriteString(renderField("Lượng đọc Bộ nhớ đệm", formatTokensCompact(snap.TotalCacheReadTokens)))
	if snap.TotalCacheWriteTokens > 0 {
		b.WriteString(renderField("Lượng ghi Bộ nhớ đệm", formatTokensCompact(snap.TotalCacheWriteTokens)))
	} else if snap.TotalCacheReadTokens > 0 {
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("(Tự động bộ nhớ đệm không phụ trội)")
		b.WriteString(renderField("Bộ nhớ đệm ghi", "0 "+hint))
	}

	// Đứt gãy = tiền tố không được rút ngắn mà tỷ lệ trúng giảm mạnh (các đợt giảm hợp lệ như đổi chương/nén đã được miễn). Nhiều lần thường
	// chỉ ra việc phía máy chủ đẩy ra hoặc tỷ lệ trúng chuyển sang lấy upstream theo vòng quay, chi tiết xem warn "đứt chuỗi bộ nhớ đệm" trong tui.log.
	if snap.TotalCacheBreaks > 0 {
		v := lipgloss.NewStyle().Foreground(colorReview).Render(fmt.Sprintf("%d lần", snap.TotalCacheBreaks))
		b.WriteString(renderField("Đứt liên kết", v))
	}

	// Arbiter theo thiết kế không tham gia prompt cache (phân xử một lần cấp KB, không có tiền tố ổn định để tái sử dụng),
	// Việc luôn hiển thị "Chưa bật" hoặc "0%" chỉ khiến người ta phải dò lỗi; bảng Mức dùng vẫn ghi nhận đầy đủ.
	var roles []host.AgentCacheStat
	for _, a := range snap.CachePerAgent {
		if a.Role != "arbiter" {
			roles = append(roles, a)
		}
	}
	if len(roles) > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).
			Render(strings.Repeat("·", max(8, width-12))))
		b.WriteString("\n")
		for _, a := range roles {
			b.WriteString(renderCacheAgentLine(a, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// colorPercent tô màu phần trăm theo bậc dựa trên tỷ lệ trúng rồi chuyển thành chuỗi, chỉ dùng cho cột giá trị.
func colorPercent(p float64) string {
	return lipgloss.NewStyle().Foreground(cacheHitColor(p)).Bold(true).
		Render(formatPercent(p))
}

// renderCacheAgentLine hiển thị một dòng role: role + tỷ lệ trúng + bộ nhớ đệm đọc / tổng nhập.
//
// Đặt cả tử số lẫn mẫu số ra (cacheRead / input) để người dùng nhìn một cái là kiểm tra được nguồn gốc của tỷ lệ trúng,
// cũng có thể nhận ra dữ liệu may rủi kiểu "phần trăm cao nhưng mẫu nhỏ" (ví dụ độ tin cậy của 100% / 1k thấp hơn 80% / 300k).
//
// Phần trăm ưu tiên dùng giá trị ổn định của cửa sổ trượt; khi trong cửa sổ không có mẫu thì quay về lũy tích. Toàn bộ cột trái chỉ dùng "/" ở chỗ này,
// ngữ nghĩa chuyên một (dấu chia toán học: cache trúng lượng / tổng nhập lượng), sẽ không lẫn với các dấu phân tách khác.
//
// Ba trạng thái:
//
//	Chưa bật     "WRITER        Chưa bật"
//	Đã bật     "WRITER        85%  · 323k / 394k"
//	Không có cache  hiển thị rõ "Chưa bật", không lẫn vào 0/0 gây nhiễu khi đọc
func renderCacheAgentLine(a host.AgentCacheStat, width int) string {
	// Tên role phải hoàn toàn khớp với vùng "Vai trò đang chạy"; Width lấy 12 để ARCHITECT
	// vẫn có thể giữ 1 cột khoảng trắng cuối để phân tách, các role khác tự động được đệm bên phải.
	roleStyle := lipgloss.NewStyle().Foreground(eventAgentColor(a.Role)).Width(12)
	role := roleStyle.Render(agentDisplayName(a.Role))

	if !a.CacheCapable {
		dim := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		_ = width
		return role + dim.Render("Chưa bật")
	}

	// Ưu tiên tỷ lệ trúng ổn định; khi trong cửa sổ không có mẫu thì quay về lũy tích.
	hit := cacheHitRate(a.RecentCacheRead, a.RecentInput)
	if a.RecentSamples == 0 || a.RecentInput == 0 {
		hit = cacheHitRate(a.CacheRead, a.Input)
	}
	// Phần trăm cố định rộng 4 cột ("100%"), để cột số lượng đọc không nhảy qua lại giữa "5%" và "85%".
	pctCell := lipgloss.NewStyle().Width(4).
		Render(colorPercent(hit))

	// Lũy tích đọc / lũy tích nhập — dù phần trăm phía trên là giá trị cửa sổ trượt, tử số và mẫu số đều dùng lũy tích vì
	// "nhìn ra quy mô" mới là mục tiêu chính của cột này; phần trăm chỉ cần cung cấp tín hiệu ổn định là đủ.
	tokens := lipgloss.NewStyle().Foreground(colorDim).Render(
		" · " + formatTokensCompact(a.CacheRead) + " / " + formatTokensCompact(a.Input))
	_ = width
	return role + pctCell + tokens
}

// cacheHitRate trong ngữ nghĩa input đã bao gồm cacheRead thì chia trực tiếp ra phần trăm.
// khi input == 0 thì trả về 0, tránh xuất hiện tỷ lệ trúng giả.
func cacheHitRate(cacheRead, input int) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(input) * 100
}

// cacheHitColor tô màu tỷ lệ trúng: ≥50% xanh lá / 20–50% vàng / <20% đỏ.
// Dùng hướng ngược với tỷ lệ sử dụng ngữ cảnh: tỷ lệ trúng bộ nhớ đệm càng cao càng khỏe.
func cacheHitColor(percent float64) lipgloss.AdaptiveColor {
	switch {
	case percent >= 50:
		return colorSuccess
	case percent >= 20:
		return colorReview
	default:
		return colorError
	}
}

func formatPercent(p float64) string {
	if p <= 0 {
		return "0%"
	}
	if p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// formatTokensCompact hiển thị số token theo dạng gọn như "8.2k" / "1.4M".
// Dùng cho các dòng per-role hẹp, tránh bị kiểu dấu phẩy của formatNumber lấn chỗ.
func formatTokensCompact(n int) string {
	if n <= 0 {
		return "0"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func contextScopeLabel(scope string) string {
	switch scope {
	case "baseline":
		return "Cơ sở"
	case "projected":
		return "Dự báo"
	case "recovered":
		return "Khôi phục"
	case "committed":
		return "Đã gửi"
	case "skipped":
		return "Bỏ qua do ngắt mạch"
	default:
		return scope
	}
}

func contextStrategyLabel(strategy string) string {
	switch strategy {
	case "":
		return ""
	case "tool_result_microcompact":
		return "Nén nhẹ kết quả công cụ"
	case "light_trim":
		return "Cắt nhẹ"
	case "full_summary":
		return "Tóm tắt đầy đủ"
	default:
		return strategy
	}
}

func agentDisplayName(name string) string {
	return strings.ToUpper(name)
}

func agentTaskLine(agent host.AgentSnapshot) string {
	if agent.TaskKind != "" {
		return taskKindLabel(agent.TaskKind)
	}
	if agent.Summary != "" {
		return agent.Summary
	}
	return ""
}

func agentContextLine(agent host.AgentSnapshot) string {
	ctx := agent.Context
	if ctx.ContextWindow <= 0 || ctx.Tokens <= 0 {
		return ""
	}
	percentColor := contextPercentColor(ctx.Percent)
	percentStr := lipgloss.NewStyle().Foreground(percentColor).Render(fmt.Sprintf("ctx %.0f%%", ctx.Percent))
	parts := []string{percentStr}
	if scope := contextScopeLabel(ctx.Scope); scope != "" {
		parts = append(parts, scope)
	}
	if strategy := contextStrategyLabel(ctx.Strategy); strategy != "" {
		parts = append(parts, strategy)
	}
	return strings.Join(parts, " · ")
}

func agentStateRank(state string) int {
	switch state {
	case "running":
		return 0
	case "failed":
		return 1
	default:
		return 2
	}
}

func agentOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "architect"):
		return 0
	case name == "editor":
		return 2
	case name == "writer":
		return 3
	default:
		return 9
	}
}

func agentStateLabel(state string) string {
	switch state {
	case "running":
		return "Đang chạy"
	case "failed":
		return "Bất thường"
	case "idle":
		return "Chờ"
	default:
		return state
	}
}

func agentStateIcon(state string) string {
	switch state {
	case "running":
		return "●"
	case "failed":
		return "×"
	default:
		return "·"
	}
}

func taskStatusColor(status string) lipgloss.AdaptiveColor {
	switch status {
	case "running":
		return colorSuccess
	case "queued":
		return colorMuted
	case "failed", "canceled":
		return colorError
	case "succeeded":
		return colorSuccess
	default:
		return colorDim
	}
}

func taskKindLabel(kind string) string {
	switch kind {
	case "foundation_plan":
		return "Lập nền tảng"
	case "chapter_write":
		return "Viết chương"
	case "chapter_review":
		return "Duyệt chương"
	case "chapter_rewrite":
		return "Viết lại chương"
	case "chapter_polish":
		return "Hoàn thiện chương"
	case "arc_expand":
		return "Mở rộng cung"
	case "volume_append":
		return "Lên quyển tiếp"
	case "steer_apply":
		return "xử lý can thiệp"
	default:
		return kind
	}
}
