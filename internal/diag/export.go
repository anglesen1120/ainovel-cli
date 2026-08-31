package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/store"
)

// ExportRelPath là vị trí cố định của tệp chẩn đoán đã khử trùng trong thư mục output (ghi đè một bản).
const ExportRelPath = "meta/diag-export.md"

// Export chạy chẩn đoán đầy đủ + render + ghi đĩa, rồi trả về đường dẫn tuyệt đối đã ghi. Dùng cho headless / gọi ngoài.
func Export(s *store.Store) (string, error) {
	rep, rc := Diagnose(s)
	return WriteExport(s, rep, rc)
}

// WriteExport render và ghi đĩa Report + RuntimeCapture đã tính sẵn, không chụp lại.
// Dùng để lệnh /diag tái sử dụng kết quả của Diagnose.
func WriteExport(s *store.Store, rep Report, rc RuntimeCapture) (string, error) {
	data := RenderExport(rep, rc)
	abs := filepath.Join(s.Dir(), filepath.FromSlash(ExportRelPath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return "", err
	}
	return abs, nil
}

// RenderExport ghép Report sáng tác + dữ liệu chụp runtime thành Markdown đã khử trùng.
func RenderExport(rep Report, rc RuntimeCapture) []byte {
	var b strings.Builder
	st := rep.Stats

	b.WriteString("# diag-export\n\n")
	fmt.Fprintf(&b, "> Thời gian tạo %s · %s/%s\n", time.Now().Format("2006-01-02 15:04:05"), rc.GoOS, rc.GoArch)
    b.WriteString("> ⚠️ Đã khử nhạy cảm: đã loại bỏ văn bản truyện / lời nhắc / suy nghĩ, chỉ giữ khung hành vi. Có thể dán trực tiếp vào báo cáo sự cố.\n\n")

	// 1. Môi trường
	b.WriteString("## 1. Môi trường\n\n")
	fmt.Fprintf(&b, "- Giai đoạn `%s`", orDash(st.Phase))
	if st.Flow != "" {
        fmt.Fprintf(&b, " / luồng `%s`", st.Flow)
	}
    fmt.Fprintf(&b, " · chương %d/%d · số chữ %d\n", st.CompletedChapters, st.TotalChapters, st.TotalWords)
	if st.PlanningTier != "" {
		fmt.Fprintf(&b, "- Lập kế hoạch `%s`\n", st.PlanningTier)
	}
	for _, m := range rc.Models {
		fmt.Fprintf(&b, "- %s → `%s` / `%s`\n", m.Agent, orDash(m.Provider), orDash(m.Model))
	}

	// 2. Phát hiện chẩn đoán (chỉ runtime; chẩn đoán sáng tác có cốt truyện/gợi ý thì để trong báo cáo màn hình /diag, không đưa vào bản xuất có thể chia sẻ)
	b.WriteString("\n## 2. Phát hiện chẩn đoán (runtime)\n\n")
	rf := runtimeFindings(&rc)
	sortFindings(rf)
	if len(rf) == 0 {
		b.WriteString("Không phát hiện bất thường runtime.\n")
	} else {
		for _, f := range rf {
			fmt.Fprintf(&b, "- [%s] %s\n", f.Severity, f.Title)
			if f.Evidence != "" {
				fmt.Fprintf(&b, "  - Bằng chứng: %s\n", f.Evidence)
			}
			if f.Suggestion != "" {
				fmt.Fprintf(&b, "  - → %s\n", f.Suggestion)
			}
		}
	}

	// 3. Tín hiệu runtime (tổng hợp thô)
	b.WriteString("\n## 3. Tín hiệu runtime\n\n")
	wrote := false
	if rc.CurrentStep != "" {
        fmt.Fprintf(&b, "- bước hiện tại `%s`\n", rc.CurrentStep)
		wrote = true
	}
	if rc.StuckStep != "" {
		fmt.Fprintf(&b, "- ⚠️ Bị kẹt: liên tục dừng ở `%s` ×%d\n", rc.StuckStep, rc.StuckCount)
		wrote = true
	}
	if len(rc.Repeats) > 0 {
        b.WriteString("- Chữ ký tần suất cao (cửa sổ gần ≥3 lần, gồm cả công cụ lặp bình thường, chỉ để tham khảo):\n")
		for _, r := range rc.Repeats {
			fmt.Fprintf(&b, "  - `%s` ×%d\n", r.Sig, r.Count)
		}
		wrote = true
	}
	if len(rc.DupContent) > 0 {
		b.WriteString("- Lặp tạo cùng đoạn văn bản (cùng sha):\n")
		for _, d := range rc.DupContent {
			fmt.Fprintf(&b, "  - sha=%s ×%d\n", d.Sha, d.Count)
		}
		wrote = true
	}
	if len(rc.LogKinds) > 0 {
        b.WriteString("- Phân loại lỗi nhật ký:")
		b.WriteString(joinKinds(rc.LogKinds))
		b.WriteString("\n")
		wrote = true
	}
	if rc.LogErrors > 0 || rc.LogWarns > 0 {
        fmt.Fprintf(&b, "- Lỗi nhật ký ×%d · cảnh báo ×%d\n", rc.LogErrors, rc.LogWarns)
		wrote = true
	}
	if rc.StopGuard > 0 {
        fmt.Fprintf(&b, "- StopGuard đã chặn ×%d\n", rc.StopGuard)
		wrote = true
	}
	if !wrote {
		b.WriteString("- Không có tín hiệu bất thường runtime rõ ràng.\n")
	}

	// 4. Đuôi khung hành vi
	fmt.Fprintf(&b, "\n## 4. Đuôi khung hành vi ( %d mục cuối)\n\n", len(rc.Tail))
	if len(rc.Tail) == 0 {
        b.WriteString("(Không có bản ghi phiên)\n")
	} else {
		b.WriteString("```\n")
		for _, ev := range rc.Tail {
			b.WriteString(formatSkel(ev))
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}

	// 5. Tự kiểm khử trùng
	b.WriteString("\n## 5. Tự kiểm khử trùng\n\n")
    fmt.Fprintf(&b, "- Khối văn bản bị che %d chỗ · số lần lộ văn bản truyện 0\n", rc.RedactedTexts)
	if len(rc.Sources) > 0 {
		fmt.Fprintf(&b, "- Nguồn dữ liệu: %s\n", strings.Join(rc.Sources, " · "))
	}

	return []byte(b.String())
}

// formatSkel render một khung thành một dòng, theo thứ tự phân phối.
func formatSkel(ev SkelEvent) string {
	var parts []string
	parts = append(parts, "["+ev.Agent+"/"+ev.Role+"]")
	for _, t := range ev.Tools {
		parts = append(parts, t.Name+formatArgs(t.Args)+invalidTag(t))
	}
	if ev.ErrClass != "" {
		parts = append(parts, "err: "+ev.ErrClass)
	}
	if len(ev.Tools) == 0 && ev.ErrClass == "" && ev.TextSha != "" {
		parts = append(parts, "text<sha="+ev.TextSha+">")
	}
	return strings.Join(parts, " ")
}

func formatArgs(args map[string]string) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+": "+args[k])
	}
	return "{" + strings.Join(pairs, ", ") + "}"
}

func invalidTag(t SkelTool) string {
	if !t.Invalid {
		return ""
	}
	if t.ParseErr != "" {
		return " ⚠️args-invalid(" + firstLine(t.ParseErr, 80) + ")"
	}
	return " ⚠️args-invalid"
}

func joinKinds(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s ×%d", k, m[k]))
	}
	return strings.Join(parts, " · ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
