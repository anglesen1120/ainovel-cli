package imp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"maps"
	"os"
	"slices"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

// analysisSchemaVersion là phiên bản schema fact theo từng chương, được đưa vào InputDigest.
const analysisSchemaVersion = 2

// ImportedCharacterFact / ImportedWorldFact là những quan sát cô đọng để tổng hợp cho toàn cuốn,
// không ghi trực tiếp thành quy tắc nhân vật hay thế giới chính thức.
// Ít nhất phải mang theo số chương để kết quả tổng hợp có nguồn gốc ổn định (RFC §9.1).
type ImportedCharacterFact struct {
	Chapter int    `json:"chapter"`
	Name    string `json:"name"`
	Note    string `json:"note,omitempty"`
}

type ImportedWorldFact struct {
	Chapter  int    `json:"chapter"`
	Category string `json:"category,omitempty"`
	Fact     string `json:"fact"`
}

// ImportedChapterFacts là sản phẩm có cấu trúc được suy ra từ một chương (RFC §9.1).
type ImportedChapterFacts struct {
	Chapter             int                        `json:"chapter"`
	Title               string                     `json:"title"`
	Summary             string                     `json:"summary"`
	KeyEvents           []string                   `json:"key_events"`
	CoreEvent           string                     `json:"core_event"`
	Hook                string                     `json:"hook,omitempty"`
	Scenes              []string                   `json:"scenes,omitempty"`
	Characters          []string                   `json:"characters,omitempty"`
	CharacterEvidence   []ImportedCharacterFact    `json:"character_evidence,omitempty"`
	WorldEvidence       []ImportedWorldFact        `json:"world_evidence,omitempty"`
	TimelineEvents      []domain.TimelineEvent     `json:"timeline_events,omitempty"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates,omitempty"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes,omitempty"`
	StateChanges        []domain.StateChange       `json:"state_changes,omitempty"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
}

// AnalysisBatchResult là kết quả có cấu trúc của một lần gọi theo lô, mỗi phần tử là fact của một chương.
type AnalysisBatchResult struct {
	Chapters []ImportedChapterFacts `json:"chapters"`
}

// ChapterAnalysisPayload là payload của artefact phân tích một chương; các chương trong cùng lô dùng chung BatchStart/BatchEnd.
type ChapterAnalysisPayload struct {
	BatchStart int                  `json:"batch_start"`
	BatchEnd   int                  `json:"batch_end"`
	Facts      ImportedChapterFacts `json:"facts"`
}

// AnalyzeBudget là ngân sách đôi input/output cho phân tích theo từng chương (RFC §9.2).
// Input được ước lượng bằng byte gần đúng với context window; output được ước lượng bằng giới hạn completion bảo thủ cho mỗi chương.
type AnalyzeBudget struct {
	ContextBytes     int // ngân sách input (nội dung + ledger + overhead)
	MaxOutputTokens  int // ngân sách output nhìn thấy (giới hạn completion)
	PerChapterOutput int // phần dự phòng output bảo thủ cho mỗi chương
	PromptOverhead   int // chi phí input cố định của system/ledger (byte)
}

func analysisPath(chapter int) string {
	return fmt.Sprintf("%s/%06d.json", dirAnalyses, chapter)
}

// analyzedChapters trả về số artefact phân tích liên tiếp từ chương 1,
// trong đó InputDigest khớp với danh tính tách chương hiện tại / phiên bản / nội dung (RFC §9.6).
// Thiếu, lỗi phân tích, hoặc digest không khớp đều bị cắt tại đây, để các thay đổi thượng nguồn
// (tách lại, đổi prompt/schema version) tự động làm vô hiệu các phân tích hạ nguồn.
func analyzedChapters(w *Workspace, seg *Segmentation, normalized []byte, segIdentity, promptVersion string) int {
	n := 0
	for c := 1; c <= len(seg.Chapters); c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			break
		}
		if a.InputDigest != chapterInputDigest(segIdentity, promptVersion, seg, normalized, c-1) {
			break
		}
		n++
	}
	return n
}

// analyzedChaptersStrict có ngữ nghĩa về độ mới giống analyzedChapters, nhưng sẽ bộc lộ các artefact
// hiện có bị hỏng hoặc không đọc được. Khôi phục trạng thái dùng bản strict để tránh coi lỗi đọc thật
// như là "chưa phân tích" rồi ghi đè làm lại.
func analyzedChaptersStrict(w *Workspace, seg *Segmentation, normalized []byte, segIdentity, promptVersion string) (int, error) {
	n := 0
	for c := 1; c <= len(seg.Chapters); c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return n, fmt.Errorf("đọc artefact phân tích chương %d: %w", c, err)
		}
		if a.InputDigest != chapterInputDigest(segIdentity, promptVersion, seg, normalized, c-1) {
			break
		}
		n++
	}
	return n, nil
}

// discardAnalysesAfter xóa các artefact phân tích theo chương có số > keep,
// để bảo đảm "phân tích lại một chương thì làm mất hiệu lực toàn bộ phần sau" (4a).
// Trong luồng phân tích xuôi bình thường, sau keep vốn đã không có artefact nào, nên đây là thao tác không-op mang tính idempotent;
// chỉ khi phân tích lại giữa chừng (vượt qua tiền tố còn mới) mới dọn phần đuôi cũ.
// Phải truyền lỗi lên: đây là điểm thực thi duy nhất của bất biến này; nuốt lỗi sẽ khiến phần đuôi cũ
// (digest theo từng chương vẫn khớp) bị tái sử dụng như tiền tố mới, và tổng hợp sẽ tiêu thụ một tập fact trộn cũ mới mà không có lỗi nào.
func discardAnalysesAfter(w *Workspace, keep, total int) error {
	for c := keep + 1; c <= total; c++ {
		if err := os.Remove(w.path(analysisPath(c))); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("dọn artefact phân tích cũ %s: %w", analysisPath(c), err)
		}
	}
	return nil
}

// loadPriorFacts đọc các fact đã ghi xuống đĩa của các chương 1..count để dựng ledger.
func loadPriorFacts(w *Workspace, count int) []ImportedChapterFacts {
	var out []ImportedChapterFacts
	for c := 1; c <= count; c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			break
		}
		out = append(out, a.Payload.Facts)
	}
	return out
}

func loadPriorFactsStrict(w *Workspace, count int) ([]ImportedChapterFacts, error) {
	out := make([]ImportedChapterFacts, 0, count)
	for c := 1; c <= count; c++ {
		a, err := readArtifact[ChapterAnalysisPayload](w, analysisPath(c))
		if err != nil {
			return out, fmt.Errorf("đọc fact phân tích chương %d: %w", c, err)
		}
		out = append(out, a.Payload.Facts)
	}
	return out, nil
}

// buildLedger suy ra ngữ cảnh liên tục cô đọng từ các chương đã phân tích: bí danh nhân vật + ID mồi nhử đang hoạt động + trạng thái gần đây.
func buildLedger(prior []ImportedChapterFacts) string {
	if len(prior) == 0 {
		return ""
	}
	names := map[string]bool{}
	active := map[string]string{} // foreshadow id -> desc
	var recent []string
	for _, f := range prior {
		for _, c := range f.Characters {
			names[c] = true
		}
		for _, fu := range f.ForeshadowUpdates {
			switch fu.Action {
			case "plant", "advance":
				if fu.Description != "" {
					active[fu.ID] = fu.Description
				} else if _, ok := active[fu.ID]; !ok {
					active[fu.ID] = ""
				}
			case "resolve":
				delete(active, fu.ID)
			}
		}
	}
	if len(prior) > 0 {
		last := prior[len(prior)-1]
		for _, sc := range last.StateChanges {
			recent = append(recent, fmt.Sprintf("%s.%s=%s", sc.Entity, sc.Field, sc.NewValue))
		}
	}
	var b strings.Builder
	if len(names) > 0 {
		b.WriteString("Nhân vật đã biết:")
		b.WriteString(strings.Join(slices.Sorted(maps.Keys(names)), "、"))
		b.WriteString("\n")
	}
	if len(active) > 0 {
		b.WriteString("Các gợi ý trước đang hoạt động (dùng lại ID, đừng tạo mới):\n")
		for _, id := range slices.Sorted(maps.Keys(active)) {
			fmt.Fprintf(&b, "- %s：%s\n", id, active[id])
		}
	}
	if len(recent) > 0 {
		b.WriteString("Trạng thái gần đây:")
		b.WriteString(strings.Join(recent, "；"))
		b.WriteString("\n")
	}
	return b.String()
}

// planBatch từ chương bắt đầu start, theo ngân sách input/output đôi, trả về điểm kết thúc end liên tiếp của batch ([start,end), chỉ số chương tính từ 0).
// Ít nhất 1 chương; ngay cả khi một chương vượt ngân sách thì cũng tự tạo thành batch riêng, và bên thực thi sẽ báo thiếu dung lượng khi bị cắt ngắn (RFC §9.2).
func planBatch(chapters []ChapterSpan, start, ledgerBytes int, b AnalyzeBudget) int {
	end := start + 1
	if b.ContextBytes <= 0 || b.MaxOutputTokens <= 0 || b.PerChapterOutput <= 0 {
		return end // chưa cấu hình ngân sách: phân tích từng chương
	}
	inAcc := ledgerBytes + b.PromptOverhead + chapterBytes(chapters, start)
	outAcc := b.PerChapterOutput
	for end < len(chapters) {
		cb := chapterBytes(chapters, end)
		if inAcc+cb > b.ContextBytes {
			break
		}
		if outAcc+b.PerChapterOutput > b.MaxOutputTokens {
			break
		}
		inAcc += cb
		outAcc += b.PerChapterOutput
		end++
	}
	return end
}

func chapterBytes(chapters []ChapterSpan, i int) int {
	return chapters[i].End - chapters[i].Start
}

// chapterInputDigest ràng buộc artefact phân tích theo từng chương: danh tính tách chương + phiên bản prompt/schema + số chương + nội dung của chương đó.
// Ràng buộc theo từng chương chứ không theo batch - cách chia batch là chi tiết thực thi thay đổi theo năng lực mô hình,
// không nên làm toàn bộ các chương đã phân tích mất hiệu lực chỉ vì đổi model;
// ràng buộc segIdentity (InputDigest của artefact segmentation) bảo đảm khi tách lại thì mọi phân tích đều tự nhiên không khớp (RFC §9.1/§6.3).
func chapterInputDigest(segIdentity, promptVersion string, seg *Segmentation, normalized []byte, i int) string {
	var b strings.Builder
	b.WriteString("analyze\x00")
	b.WriteString(promptVersion)
	fmt.Fprintf(&b, "\x00v%d\x00", analysisSchemaVersion)
	b.WriteString(segIdentity)
	fmt.Fprintf(&b, "\x00ch%d\x00", seg.Chapters[i].Number)
	b.WriteString(seg.Content(normalized, i))
	return Digest([]byte(b.String()))
}

// validateBatch kiểm tra ở hai tầng: batch phải liên tục, không thiếu, không lặp; từng chương phải đúng miền giá trị và tham chiếu (RFC §9.4).
func validateBatch(r *AnalysisBatchResult, seg *Segmentation, start, end int) error {
	want := end - start
	if len(r.Chapters) != want {
		return fmt.Errorf("số chương trong batch %d != %d như mong đợi", len(r.Chapters), want)
	}
	for i, f := range r.Chapters {
		want := seg.Chapters[start+i]
		if f.Chapter != want.Number {
			return fmt.Errorf("mục %d của batch có số chương %d != %d", i, f.Chapter, want.Number)
		}
		if strings.TrimSpace(f.Summary) == "" || strings.TrimSpace(f.CoreEvent) == "" {
			return fmt.Errorf("summary/core_event của chương %d không được để trống", f.Chapter)
		}
		if !domain.ValidHookType(strings.ToLower(f.HookType)) {
			return fmt.Errorf("hook_type của chương %d không hợp lệ: %q", f.Chapter, f.HookType)
		}
		if !domain.ValidDominantStrand(strings.ToLower(f.DominantStrand)) {
			return fmt.Errorf("dominant_strand của chương %d không hợp lệ: %q", f.Chapter, f.DominantStrand)
		}
		for j, fu := range f.ForeshadowUpdates {
			if fu.Action == "plant" && strings.TrimSpace(fu.Description) == "" {
				return fmt.Errorf("foreshadow[%d] của chương %d với action plant cần có description", j, f.Chapter)
			}
		}
		// Nếu đã kiểm tra enum theo chữ thường thì cũng ghi xuống đĩa theo chữ thường:
		// commit_chapter không kiểm tra lại enum, các biến thể hoa/thường sẽ đi thẳng vào trạng thái chính thức
		// (HookHistory, v.v. tiêu thụ theo chuỗi chính xác, nên biến thể được xem là kiểu chưa biết); qua được kiểm tra thì chuẩn hóa luôn.
		r.Chapters[i].HookType = strings.ToLower(f.HookType)
		r.Chapters[i].DominantStrand = strings.ToLower(f.DominantStrand)
	}
	return nil
}

// AnalyzeNext ghép một batch bắt đầu từ phần phân tích bị thiếu đầu tiên và ghi xuống đĩa một cách nguyên tử, rồi trả về số chương đã commit lần này.
// Cắt ngắn tức là "thất bại + thu nhỏ rồi ghép batch lại" (mặc định, §9.5); nếu batch đã co tới một chương mà vẫn bị cắt thì sẽ báo rõ là thiếu dung lượng.
func AnalyzeNext(ctx context.Context, m callModel, systemPrompt string, w *Workspace, normalized []byte, seg *Segmentation, segIdentity, promptVersion string, budget AnalyzeBudget, prof callProfile) (int, error) {
	total := len(seg.Chapters)
	start := analyzedChapters(w, seg, normalized, segIdentity, promptVersion)
	if start >= total {
		return 0, nil
	}
	ledger := buildLedger(loadPriorFacts(w, start))
	end := planBatch(seg.Chapters, start, len(ledger), budget)

	for {
		payload := buildAnalyzePayload(normalized, seg, ledger, start, end)
		res, err := callStructured[AnalysisBatchResult](ctx, m, analysisContract, systemPrompt, payload, budget.MaxOutputTokens, prof, func(r *AnalysisBatchResult) error {
			return validateBatch(r, seg, start, end)
		})
		if err != nil {
			var tr *errTruncated
			if errors.As(err, &tr) {
				// Khi bị cắt ngắn, ưu tiên cứu vớt tiền tố hợp lệ liên tục dài nhất tính từ chương đầu của batch;
				// phần đã commit không làm lại (¶9.5).
				if salvaged := salvagePrefix(tr.Raw, seg, start); len(salvaged) > 0 {
					for i, f := range salvaged {
						ch := start + i + 1
						digest := chapterInputDigest(segIdentity, promptVersion, seg, normalized, start+i)
						art := ChapterAnalysisPayload{BatchStart: start + 1, BatchEnd: end, Facts: f}
						if werr := writeArtifact(w, analysisPath(ch), digest, art); werr != nil {
							return i, fmt.Errorf("ghi xuống đĩa chương cứu vớt %d: %w", ch, werr)
						}
					}
					w.writeFailure(FailureMeta{Stage: "analyze", Detail: fmt.Sprintf("batch %d-%d bị cắt ngắn do độ dài", start+1, end),
						StopReason: "length", PrefixSalvage: fmt.Sprintf("available:%d", len(salvaged))}, tr.Raw)
					prof.logger().Info("imp phân tích bị cắt ngắn, cứu vớt tiền tố liên tục", "batch_start", start+1, "salvaged", len(salvaged))
					echoChapterFacts(prof, salvaged)
					return len(salvaged), nil
				}
				// Không có tiền tố nào cứu vớt được: ghi nhận là không dùng được và "thất bại + thu nhỏ rồi ghép batch lại";
				// nếu chỉ còn một chương mà vẫn cắt ngắn thì báo thiếu dung lượng.
				w.writeFailure(FailureMeta{Stage: "analyze", Detail: fmt.Sprintf("batch %d-%d bị cắt ngắn do độ dài, không có tiền tố cứu vớt được", start+1, end),
					StopReason: "length", PrefixSalvage: "unavailable"}, tr.Raw)
				if end-start > 1 {
					prof.logger().Warn("imp phân tích bị cắt ngắn, thu nhỏ rồi ghép batch lại", "batch", fmt.Sprintf("%d-%d", start+1, end), "prefix_salvage", "unavailable")
					end = start + (end-start)/2
					// Dòng tiến độ không có Key: vừa để người dùng thấy hành động thu nhỏ batch,
					// vừa tách hai lần gọi độc lập trước và sau đó để các dòng backoff không bị gộp nhầm theo cùng Key
					// (hợp đồng Key chỉ bao trùm backoff tạm thời trong cùng một lần gọi).
					prof.step(0, 0, "Đầu ra bị cắt ngắn do độ dài và không có tiền tố nào cứu vớt được, thu nhỏ batch để thử lại từ chương %d-%d", start+1, end)
					continue
				}
				return 0, fmt.Errorf("batch một chương của chương %d vẫn bị cắt ngắn do độ dài, năng lực output nhìn thấy của mô hình không đủ", start+1)
			}
			return 0, err
		}
		for i, f := range res.Chapters {
			ch := start + i + 1
			digest := chapterInputDigest(segIdentity, promptVersion, seg, normalized, start+i)
			payloadArt := ChapterAnalysisPayload{BatchStart: start + 1, BatchEnd: end, Facts: f}
			if err := writeArtifact(w, analysisPath(ch), digest, payloadArt); err != nil {
				return i, fmt.Errorf("ghi phân tích chương %d xuống đĩa: %w", ch, err)
			}
		}
		echoChapterFacts(prof, res.Chapters)
		return end - start, nil
	}
}

// echoChapterFacts lặp lại hiểu biết cốt lõi của mô hình cho từng chương lên bảng điều khiển — người dùng nên thấy mô hình đã hiểu gì,
// chứ không chỉ là đếm batch máy móc (¶14.1).
func echoChapterFacts(prof callProfile, facts []ImportedChapterFacts) {
	for _, f := range facts {
		prof.step(0, 0, "Chương %d〈%s〉: %s", f.Chapter, snippet(f.Title, 24), snippet(f.CoreEvent, 60))
	}
}

// buildAnalyzePayload ghép input của batch: nguyên văn các chương liên tiếp + ledger trước batch.
func buildAnalyzePayload(normalized []byte, seg *Segmentation, ledger string, start, end int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hãy phân tích các chương %d-%d, trả về {\"chapters\":[mỗi chương một đối tượng fact]}, thứ tự mảng phải khớp với số chương.\n\n", start+1, end)
	if ledger != "" {
		b.WriteString("## Ledger liên tục (tham khảo)\n\n")
		b.WriteString(ledger)
		b.WriteString("\n")
	}
	for i := start; i < end; i++ {
		c := seg.Chapters[i]
		fmt.Fprintf(&b, "## Chương %d: %s\n\n", c.Number, c.Title)
		b.WriteString(seg.Content(normalized, i))
		b.WriteString("\n\n---\n\n")
	}
	return b.String()
}

// salvagePrefix phân tích từ phản hồi batch bị cắt ngắn ra tiền tố hợp lệ liên tục dài nhất (RFC §9.5).
// Chỉ lưu các đối tượng liên tục từ chương đầu của batch, từng chương đều qua kiểm tra; hễ gặp đối tượng đầu tiên không đầy đủ/không hợp lệ/lệch số chương thì dừng,
// các byte phía sau không giải nghĩa nữa.
// Đây là hàm thuần, được AnalyzeNext gọi ưu tiên khi bị cắt do dung lượng, để tránh bỏ đi các chương tiền tố đã sinh ra đầy đủ.
func salvagePrefix(raw string, seg *Segmentation, start int) []ImportedChapterFacts {
	arr := extractChaptersArray(raw)
	if arr == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(arr))
	if _, err := dec.Token(); err != nil { // consume '['
		return nil
	}
	var out []ImportedChapterFacts
	for dec.More() {
		var f ImportedChapterFacts
		if err := dec.Decode(&f); err != nil {
			break // đối tượng không đầy đủ đầu tiên, dừng lại
		}
		idx := start + len(out)
		if idx >= len(seg.Chapters) || f.Chapter != seg.Chapters[idx].Number {
			break // lệch số chương / vượt biên
		}
		one := AnalysisBatchResult{Chapters: []ImportedChapterFacts{f}}
		if err := validateBatch(&one, seg, idx, idx+1); err != nil {
			break
		}
		out = append(out, one.Chapters[0]) // validateBatch đã chuẩn hóa enum tại chỗ, lấy giá trị đã qua kiểm tra
	}
	return out
}

// extractChaptersArray cắt ra văn bản mảng JSON đứng sau "chapters" (có thể bị cắt ở phần đuôi).
func extractChaptersArray(raw string) string {
	i := strings.Index(raw, "\"chapters\"")
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(raw[i:], '[')
	if j < 0 {
		return ""
	}
	return raw[i+j:]
}
