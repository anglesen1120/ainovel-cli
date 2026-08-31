package imp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// BoundaryDecision là nhận định ranh giới của mô hình cho một owned range (RFC §8.2).
type BoundaryDecision struct {
	UnitID    string `json:"unit_id"`
	Anchor    string `json:"anchor,omitempty"`
	Kind      string `json:"kind"` // chapter / group / front_matter / back_matter
	Title     string `json:"title,omitempty"`
	Uncertain bool   `json:"uncertain,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

const (
	kindChapter     = "chapter"
	kindGroup       = "group"
	kindFrontMatter = "front_matter"
	kindBackMatter  = "back_matter"
)

// boundaryBatch là kết quả có cấu trúc của một lần gọi phân đoạn.
type boundaryBatch struct {
	Boundaries []BoundaryDecision `json:"boundaries"`
}

// ChapterSpan là một chương có thể commit sau khi xác nhận phân đoạn: tiêu đề + phạm vi byte văn bản đã chuẩn hóa (gồm dòng tiêu đề).
type ChapterSpan struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Start  int    `json:"start_byte"`
	End    int    `json:"end_byte"`
}

// MatterSpan là tiêu đề quyển/phần hoặc vùng phụ trợ rõ ràng.
type MatterSpan struct {
	Kind  string `json:"kind"`
	Title string `json:"title,omitempty"`
	Start int    `json:"start_byte"`
	End   int    `json:"end_byte"`
}

// Segmentation là kết quả phân đoạn đã vượt qua kiểm tra phủ toàn văn (upstream của confirmation và phân tích từng chương).
type Segmentation struct {
	Chapters  []ChapterSpan `json:"chapters"`
	Matter    []MatterSpan  `json:"matter,omitempty"`    // group / front / back
	Uncertain []int         `json:"uncertain,omitempty"` // đánh dấu số chương uncertain để nhắc trong phần preview
	Notes     []string      `json:"notes,omitempty"`     // ghi chú cần người kiểm trong giai đoạn phân đoạn (ví dụ gộp tiêu đề giữ chỗ không có nội dung vào đoạn trước)
}

// Content trả về nội dung đã chuẩn hóa của chương thứ i (gồm dòng tiêu đề).
func (s *Segmentation) Content(normalized []byte, i int) string {
	c := s.Chapters[i]
	return string(normalized[c.Start:c.End])
}

// resolveSegmentation ánh xạ các nhận định ranh giới có thứ tự thành Segmentation đã kiểm tra phủ toàn văn (RFC §8.3).
// Hàm thuần: đầu ra mô hình và kiểm tra mã được tách rời; Go không phán lại "dòng nào là tiêu đề chương", nhưng bất biến phủ nội dung phải đúng.
func resolveSegmentation(normalized []byte, units []SourceUnit, decisions []BoundaryDecision) (*Segmentation, error) {
	if len(decisions) == 0 {
		return nil, fmt.Errorf("không nhận diện được ranh giới nào")
	}
	// Tiền điều kiện: units phải được sắp theo thứ tự số (Line,Part), cấm dùng thứ tự từ điển của ID.
	for i := 1; i < len(units); i++ {
		if !unitLess(units[i-1], units[i]) {
			return nil, fmt.Errorf("SourceUnit không được sắp theo thứ tự số (Line,Part): %s đứng trước %s", units[i-1].ID, units[i].ID)
		}
	}
	unitByID := make(map[string]SourceUnit, len(units))
	for _, u := range units {
		unitByID[u.ID] = u
	}

	type point struct {
		byte int
		d    BoundaryDecision
	}
	points := make([]point, 0, len(decisions))
	for i, d := range decisions {
		switch d.Kind {
		case kindChapter, kindGroup, kindFrontMatter, kindBackMatter:
		default:
			return nil, fmt.Errorf("ranh giới[%d] có kind không hợp lệ: %q", i, d.Kind)
		}
		b, err := resolveBoundaryByte(unitByID, d.UnitID, d.Anchor)
		if err != nil {
			return nil, err
		}
		points = append(points, point{byte: b, d: d})
	}
	// Các trường hợp mô hình thỉnh thoảng trả về sai thứ tự hoặc trùng lặp là vấn đề kỷ luật tọa độ; Go sửa xác định thay vì phủ quyết cuối cùng.
	// Bỏ cả giai đoạn phân đoạn chỉ vì hai ranh giới trong một khối bị đảo sau khi mọi khối đã thành công là quá đắt (thực tế từng có 319 ranh giới hỏng vì 1 lỗi đảo thứ tự trong khối,
	// và cache khối khiến lỗi tái hiện xác định). Thứ tự giữa các khối được bảo đảm bằng khoảng owned không chồng lấn; sai thứ tự chỉ có thể nằm trong khối:
	// sắp ổn định theo byte sẽ khôi phục thứ tự thật, không mất thông tin; trùng cùng byte giữ điểm xuất hiện trước và ghi Notes để người dùng kiểm trong preview xác nhận.
	sort.SliceStable(points, func(i, j int) bool { return points[i].byte < points[j].byte })
	var notes []string
	uniq := points[:0]
	for _, p := range points {
		if n := len(uniq); n > 0 && uniq[n-1].byte == p.byte {
			// Trùng hoàn toàn là dư thừa cơ học, lặng lẽ khử trùng; xung đột ngữ nghĩa cùng vị trí (khác kind/tiêu đề) đã được hỏi lại ở giai đoạn gọi.
			// Nếu vẫn đến đây thì chỉ có thể là cache cũ từ trước khi sửa; giữ điểm đầu tiên và ghi Notes để người dùng kiểm.
			if prev := uniq[n-1].d; prev.Kind != p.d.Kind || boundaryLabel(prev) != boundaryLabel(p.d) {
				notes = append(notes, fmt.Sprintf("ranh giới %q và %q trùng nhau (byte %d), đã giữ ranh giới trước",
					boundaryLabel(prev), boundaryLabel(p.d), p.byte))
			}
			continue
		}
		uniq = append(uniq, p)
	}
	points = uniq
	// Văn bản không rỗng trước ranh giới đầu tiên (giới thiệu đầu sách/quảng cáo, mô hình bỏ sót ranh giới đầu) không bị phủ quyết cuối cùng: Go thêm xác định một front_matter phủ [0, first),
	// ghi Notes để người dùng kiểm trong preview xác nhận. Vì lỗi bỏ sót đã vào cache khối, phủ quyết cuối cùng sẽ khiến chạy lại không gọi mô hình mà tái hiện cùng lỗi (cùng triết lý hấp thụ chương không có nội dung, RFC §8.3.5).
	// Phán đoán ngữ nghĩa đã được trả lại cho mô hình ở giai đoạn gọi (chunkValidator.coverStart hỏi lại); fallback này chỉ chữa cache cũ.
	if head := points[0].byte; head != 0 && strings.TrimSpace(string(normalized[:head])) != "" {
		notes = append(notes, fmt.Sprintf("%d byte đầu mô hình chưa gán thuộc về đâu (%s…), đã gom thành front_matter, vui lòng kiểm tra có bỏ sót chương không",
			head, snippet(string(normalized[:min(head, 48)]), 24)))
		points = append([]point{{byte: 0, d: BoundaryDecision{UnitID: units[0].ID, Kind: kindFrontMatter}}}, points...)
	}

	seg := &Segmentation{Notes: notes}
	chapterNo := 0
	// absorb gộp một đoạn vào span vừa tạo gần nhất (chương hoặc vùng phụ trợ đều được); nếu không có gì để gộp thì trả false.
	absorb := func(end int) bool {
		ci, mi := len(seg.Chapters)-1, len(seg.Matter)-1
		switch {
		case ci >= 0 && (mi < 0 || seg.Chapters[ci].Start > seg.Matter[mi].Start):
			seg.Chapters[ci].End = end
		case mi >= 0:
			seg.Matter[mi].End = end
		default:
			return false
		}
		return true
	}
	for i, p := range points {
		start := p.byte
		if i == 0 {
			start = 0 // Đoạn đầu hấp thụ khoảng trắng ở đầu.
		}
		end := len(normalized)
		if i+1 < len(points) {
			end = points[i+1].byte
		}
		title := strings.TrimSpace(p.d.Title)
		if title == "" {
			title = firstLine(normalized, p.byte, end)
		}
		switch p.d.Kind {
		case kindChapter:
			if strings.TrimSpace(bodyAfterTitle(normalized, p.byte, end)) == "" {
				// Nguồn tiểu thuyết mạng thực tế thường có giữ chỗ "đã khóa/chương trả phí": có tiêu đề nhưng thiếu nội dung. Không làm hỏng toàn bộ quy trình.
				// Một phủ quyết cuối cùng sẽ lãng phí toàn bộ lời gọi mô hình của giai đoạn phân đoạn; dòng tiêu đề được gộp vào đoạn trước (không mất byte văn bản nào),
				// ghi vào Notes để preview xác nhận hiển thị; nếu người dùng không đồng ý thì có thể dùng --guide để phân xử (điểm dừng RFC §8.4 tồn tại chính vì việc này).
				seg.Notes = append(seg.Notes,
					fmt.Sprintf("tiêu đề chương %q không có nội dung (byte %d..%d), đã gộp vào đoạn trước (thường gặp ở chương giữ chỗ đã khóa/trả phí)", title, start, end))
				if !absorb(end) {
					seg.Matter = append(seg.Matter, MatterSpan{Kind: kindFrontMatter, Title: title, Start: start, End: end})
				}
				continue
			}
			chapterNo++
			seg.Chapters = append(seg.Chapters, ChapterSpan{Number: chapterNo, Title: title, Start: start, End: end})
			if p.d.Uncertain {
				seg.Uncertain = append(seg.Uncertain, chapterNo)
			}
		default:
			seg.Matter = append(seg.Matter, MatterSpan{Kind: p.d.Kind, Title: title, Start: start, End: end})
		}
	}
	if chapterNo == 0 {
		return nil, fmt.Errorf("phân đoạn không tạo ra chương nào (group không được tính là chương)")
	}
	// Chương trùng tên là tín hiệu xác định cho trường hợp "một chương bị cắt nhầm thành nhiều chương" (nguồn có quy ước tiêu đề thì tên chương không nên lặp), chỉ ghi Notes
	// để người dùng kiểm trong preview xác nhận (Notes không rỗng sẽ chặn --yes); Go không tự phán có gộp hay không.
	titleAt := make(map[string]int, len(seg.Chapters))
	for _, c := range seg.Chapters {
		key := squashSpace(c.Title)
		if first, ok := titleAt[key]; ok && key != "" {
			seg.Notes = append(seg.Notes, fmt.Sprintf("chương %d và chương %d có tiêu đề trùng nhau (%q), nghi là một chương bị cắt nhầm, vui lòng kiểm tra",
				c.Number, first, snippet(c.Title, 24)))
		} else {
			titleAt[key] = c.Number
		}
	}
	return seg, nil
}

// squashSpace loại bỏ toàn bộ khoảng trắng, dùng để đối chiếu tiêu đề echoed và tiêu đề trùng; khác biệt khoảng trắng/trang trí không phải khác biệt ngữ nghĩa.
func squashSpace(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

// firstLine trả về dòng đầu tiên trong [start,end) sau khi trim khoảng trắng.
func firstLine(normalized []byte, start, end int) string {
	s := string(normalized[start:end])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// bodyAfterTitle trả về phần trong [start,end) sau khi bỏ dòng đầu tiên (tiêu đề).
// Tiêu đề chương nhiều dòng chiếm riêng dòng đầu, nội dung nằm sau đó; đoạn một dòng không có xuống dòng (trường hợp cắt theo anchor) thì cả đoạn là nội dung,
// nên trả về nguyên đoạn thay vì chuỗi rỗng, nếu không tiểu thuyết hợp lệ dạng một dòng/một dòng nhiều chương sẽ bị từ chối nhầm là "nội dung rỗng" (RFC §8.3).
func bodyAfterTitle(normalized []byte, start, end int) string {
	s := string(normalized[start:end])
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// planChunks chia units theo ngân sách byte thành các khoảng chỉ số owned [start,end) không chồng lấn và phủ đầy đủ.
// Kích thước khối được tính từ ngân sách ngữ cảnh, không theo số dòng hay số chương cố định (RFC §8.1).
func planChunks(units []SourceUnit, budgetBytes int) [][2]int {
	if len(units) == 0 {
		return nil
	}
	if budgetBytes <= 0 {
		return [][2]int{{0, len(units)}}
	}
	var chunks [][2]int
	start := 0
	acc := 0
	for i, u := range units {
		size := u.EndByte - u.StartByte
		if acc > 0 && acc+size > budgetBytes {
			chunks = append(chunks, [2]int{start, i})
			start = i
			acc = 0
		}
		acc += size
	}
	chunks = append(chunks, [2]int{start, len(units)})
	return chunks
}

// buildProjection lắp payload projection cấu trúc cho một khoảng owned (kèm ít ngữ cảnh); mô hình chỉ trả ranh giới cho owned.
// Đồng thời trả tập mọi unit_id trong projection (owned + vùng ngữ cảnh), để kiểm tra đầu ra phân biệt ảo giác và vượt biên.
func buildProjection(units []SourceUnit, owned [2]int, contextMargin, ctxBudget int, guidance string) (string, map[string]bool) {
	// Vùng ngữ cảnh thu hẹp theo cả số unit và trần byte (ctxBudget<=0 thì chỉ theo số unit): margin thường là dòng thường,
	// nhưng mảnh ảo của dòng quá dài có thể đạt MaxUnitBytes, vài mảnh đã nuốt hết ngân sách đầu vào. Ngữ cảnh chỉ là tham khảo, không đáng trả giá đó.
	lo, budget := owned[0], ctxBudget
	for lo > 0 && owned[0]-lo < contextMargin {
		if n := len(units[lo-1].Text); ctxBudget > 0 {
			if n > budget {
				break
			}
			budget -= n
		}
		lo--
	}
	hi, budget := owned[1], ctxBudget
	for hi < len(units) && hi-owned[1] < contextMargin {
		if n := len(units[hi].Text); ctxBudget > 0 {
			if n > budget {
				break
			}
			budget -= n
		}
		hi++
	}
	type projUnit struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	proj := struct {
		OwnedStart   string     `json:"owned_start"`
		OwnedEnd     string     `json:"owned_end"`
		Units        []projUnit `json:"units"`
		UserGuidance string     `json:"user_guidance,omitempty"`
	}{
		OwnedStart:   units[owned[0]].ID,
		OwnedEnd:     units[owned[1]-1].ID,
		UserGuidance: guidance,
	}
	ids := make(map[string]bool, hi-lo)
	for i := lo; i < hi; i++ {
		proj.Units = append(proj.Units, projUnit{ID: units[i].ID, Text: units[i].Text})
		ids[units[i].ID] = true
	}
	data, _ := json.MarshalIndent(proj, "", "  ")
	return string(data), ids
}

// segmentInputDigest phủ các đầu vào ngữ nghĩa thật sự tiêu thụ bởi thao tác phân đoạn: nguồn đã chuẩn hóa, hướng dẫn người dùng, phiên bản prompt (RFC §6.3).
func segmentInputDigest(normalizedDigest, guidance, promptVersion string) string {
	return Digest([]byte(strings.Join([]string{"segment", promptVersion, normalizedDigest, guidance}, "\x00")))
}

// segmentChunkPath / segmentChunkDigest: đường dẫn và danh tính artifact cache ranh giới cấp khối.
// Danh tính gắn với danh tính phân đoạn (nguồn+hướng dẫn+phiên bản prompt) và phạm vi unit owned của khối; mọi thay đổi upstream đều làm cache tự mất hiệu lực.
func segmentChunkPath(owned [2]int) string {
	return fmt.Sprintf("%s/chunk-%06d-%06d.json", dirSegmentChunks, owned[0], owned[1])
}

func segmentChunkDigest(identity, loID, hiID string) string {
	return Digest([]byte(strings.Join([]string{"segment-chunk", identity, loID, hiID}, "\x00")))
}

// Segment thực hiện phân đoạn ngữ nghĩa trên toàn bộ văn bản đã chuẩn hóa: gọi mô hình theo từng khoảng owned để nhận diện ranh giới, rồi kiểm tra phủ toàn văn.
// contextMargin là số unit ngữ cảnh, chunkBytes là ngân sách byte của khoảng owned, maxTokens là ngân sách đầu ra mỗi lần gọi.
// Khi w không rỗng, ghi cache ranh giới theo từng khối (identity = segmentInputDigest): một khối có thể mất vài phút, một khối lỗi không nên khiến phải trả lại chi phí cho các khối đã xong.
// Cơ chế này cùng triết lý với analyze theo chương và synthesize theo khoảng; trước đây phân đoạn là giai đoạn đắt duy nhất không bền vững trong nội bộ giai đoạn, lỗi một chỗ phải làm lại toàn bộ.
func Segment(ctx context.Context, m callModel, systemPrompt string, normalized []byte, units []SourceUnit, guidance string, chunkBytes, contextMargin, maxTokens int, prof callProfile, w *Workspace, identity string) (*Segmentation, error) {
	chunks := planChunks(units, planningBudget(chunkBytes, systemPrompt, guidance))
	unitByID := make(map[string]SourceUnit, len(units))
	for _, u := range units {
		unitByID[u.ID] = u
	}
	var decisions []BoundaryDecision
	// chunk xử lý một khoảng owned: cache hit thì không gọi mô hình; nếu đầu ra bị cắt do độ dài và khoảng còn tách được thì chia đôi khối để thử lại đệ quy
	// (JSON ranh giới của nhiều chương ngắn có thể vượt ngân sách đầu ra, cùng triết lý thu nhỏ batch của analyze). Nửa khối có đường cache riêng, thành quả retry không phải trả lại; nếu còn lỗi ở cấp unit thì mới là thiếu dung lượng thật.
	var chunk func(owned [2]int, cur, total int) ([]BoundaryDecision, error)
	chunk = func(owned [2]int, cur, total int) ([]BoundaryDecision, error) {
		lo, hi := units[owned[0]], units[owned[1]-1]
		rel, want := segmentChunkPath(owned), segmentChunkDigest(identity, lo.ID, hi.ID)
		if w != nil {
			if art, err := readArtifact[boundaryBatch](w, rel); err == nil && art.InputDigest == want {
				return art.Payload.Boundaries, nil
			}
		}
		// Một lời gọi mô hình cho một khối có thể kéo dài vài phút; phải echo tiến độ theo khối + số ranh giới đã có để panel không im lặng như bị treo.
		prof.step(cur, total, "phân đoạn khối %d/%d (%s..%s), đã nhận diện %d ranh giới...",
			cur, total, lo.ID, hi.ID, len(decisions))
		// Trần byte vùng ngữ cảnh lấy chunkBytes/8 nhưng không thấp hơn 4096: mục tiêu là chặn các mảnh ảo của dòng quá dài (mỗi mảnh có thể tới MaxUnitBytes) nuốt ngân sách đầu vào; margin dòng thường vốn không đáng kể.
		payload, projIDs := buildProjection(units, owned, contextMargin, max(chunkBytes/8, 4096), guidance)
		ownedIDs := make(map[string]bool, owned[1]-owned[0])
		for i := owned[0]; i < owned[1]; i++ {
			ownedIDs[units[i].ID] = true
		}
		v := chunkValidator{projIDs: projIDs, ownedIDs: ownedIDs, unitByID: unitByID,
			normalized: normalized, coverStart: owned[0] == 0}
		batch, err := callStructured[boundaryBatch](ctx, m, segmentContract, systemPrompt, payload, maxTokens, prof, func(b *boundaryBatch) error {
			return v.validate(b.Boundaries)
		})
		if err != nil {
			var tr *errTruncated
			if errors.As(err, &tr) && owned[1]-owned[0] > 1 {
				mid := (owned[0] + owned[1]) / 2
				prof.step(0, 0, "đầu ra ranh giới khối %s..%s bị cắt (chương quá dày), chia đôi khối để thử lại", lo.ID, hi.ID)
				prof.logger().Warn("đầu ra phân đoạn imp bị cắt, chia đôi khối", "chunk", lo.ID+".."+hi.ID)
				left, lerr := chunk([2]int{owned[0], mid}, cur, total)
				if lerr != nil {
					return nil, lerr
				}
				right, rerr := chunk([2]int{mid, owned[1]}, cur, total)
				if rerr != nil {
					return nil, rerr
				}
				return append(left, right...), nil
			}
			return nil, fmt.Errorf("khoảng phân đoạn %s..%s: %w", lo.ID, hi.ID, err)
		}
		// Ranh giới thuộc vùng ngữ cảnh do khối lân cận quản lý (khối đó sẽ báo lại trong khoảng owned của chính nó); Go cắt bỏ trực tiếp.
		// Kỷ luật tọa độ do mã thực thi, retry ngữ nghĩa chỉ dành cho lỗi ngữ nghĩa thật. Hành vi cũ hỏi lại khi vượt biên khiến mô hình yếu thường dùng hết 3 lần thử và làm hỏng cả khối (RFC §8.1: "mô hình lo ngữ nghĩa, Go lo tọa độ").
		kept := make([]BoundaryDecision, 0, len(batch.Boundaries))
		for _, bd := range batch.Boundaries {
			if ownedIDs[bd.UnitID] {
				kept = append(kept, bd)
			}
		}
		if n := len(batch.Boundaries) - len(kept); n > 0 {
			// Đây là kỷ luật tọa độ thường lệ, không phải bất thường; dùng tiến độ thường để tránh màu cảnh báo làm người dùng tưởng có lỗi.
			prof.step(0, 0, "đã cắt %d ranh giới bị báo thừa trong vùng ngữ cảnh (thuộc khối lân cận, không phải lỗi)", n)
		}
		// Echo phán đoán ngữ nghĩa của mô hình (các tiêu đề đã nhận diện), để người dùng thấy mô hình hiểu gì thay vì chỉ số đếm cơ học.
		if len(kept) > 0 {
			prof.step(0, 0, "mô hình nhận diện: %s", previewBoundaries(kept))
		}
		if w != nil {
			if err := writeArtifact(w, rel, want, boundaryBatch{Boundaries: kept}); err != nil {
				return nil, fmt.Errorf("ghi khối phân đoạn %s..%s: %w", lo.ID, hi.ID, err)
			}
		}
		return kept, nil
	}
	for ci, owned := range chunks {
		kept, err := chunk(owned, ci+1, len(chunks))
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, kept...)
	}
	seg, err := resolveSegmentation(normalized, units, decisions)
	if err != nil {
		// Khi hợp nhất cuối cùng thất bại, cache khối không còn giá trị: digest vẫn khớp sẽ khiến lần chạy lại không gọi mô hình mà đọc lại cùng nhóm ranh giới,
		// tái hiện xác định cùng lỗi. Xóa cache để lần sau có cơ hội phân đoạn lại; snapshot quyết định đi qua errSemantic và được ghi failures/ để điều tra sau.
		// Nếu xóa thất bại phải báo thật; nói dối là đã xóa sẽ khiến người dùng chạy lại và tiếp tục đọc cache hỏng (Debug-First).
		hint := "đã xóa cache khối, lần chạy lại sẽ phân đoạn lại"
		if w != nil {
			if cerr := w.clearDir(dirSegmentChunks); cerr != nil {
				hint = fmt.Sprintf("xóa cache khối thất bại: %v, trước khi chạy lại hãy tự xóa meta/import/segment-chunks/", cerr)
			}
		}
		raw, _ := json.MarshalIndent(decisions, "", "  ")
		return nil, &errSemantic{Raw: string(raw), Err: fmt.Errorf("hợp nhất phân đoạn toàn sách thất bại (%s): %w", hint, err)}
	}
	return seg, nil
}

// planningBudget trừ overhead cấu trúc của request khỏi ngân sách đầu vào: system prompt và guidance trừ theo độ dài thật, phần còn lại nhân 3/4 để tính độ phình của vỏ JSON projection
// (id/dấu nháy/escape xấp xỉ 1/3 nội dung). Nội dung owned chỉ là một phần request; nếu lập kế hoạch theo toàn bộ định mức sẽ vượt ngân sách đầu vào thật khi prompt dài hoặc vùng ngữ cảnh lớn.
// Sàn chunkBytes/4 tránh prompt quá dài ép ngân sách thành số âm; chunkBytes<=0 nghĩa là không giới hạn ngân sách (một khối), giữ nguyên.
func planningBudget(chunkBytes int, systemPrompt, guidance string) int {
	if chunkBytes <= 0 {
		return chunkBytes
	}
	b := (chunkBytes - len(systemPrompt) - len(guidance)) * 3 / 4
	return max(b, chunkBytes/4)
}

// boundaryLabel cho nhận định ranh giới một nhãn dễ đọc: ưu tiên tiêu đề, nếu không có thì dùng kind@unit_id.
func boundaryLabel(d BoundaryDecision) string {
	if t := strings.TrimSpace(d.Title); t != "" {
		return t
	}
	return d.Kind + "@" + d.UnitID
}

// previewBoundaries nén một nhóm nhận định ranh giới thành dòng preview tiêu đề (tối đa 3 mục + số lượng), dùng để echo trên panel.
func previewBoundaries(bs []BoundaryDecision) string {
	titles := make([]string, 0, 3)
	for _, b := range bs {
		titles = append(titles, snippet(boundaryLabel(b), 24))
		if len(titles) == 3 {
			break
		}
	}
	s := strings.Join(titles, " / ")
	if len(bs) > len(titles) {
		s += fmt.Sprintf(" (tổng %d vị trí)", len(bs))
	}
	return s
}

// chunkValidator giữ ngữ cảnh kiểm tra trong lúc gọi phân đoạn: unit_id ngoài projection là ảo giác; ranh giới trong owned còn phải có kind hợp lệ, anchor parse được và không xung đột ngữ nghĩa cùng vị trí;
// khối đầu phải có ranh giới phủ điểm bắt đầu văn bản. Nếu không chặn lúc gọi, các giá trị hỏng này sẽ đi vào cache khối; digest khớp khiến lần chạy lại đọc lại cùng dữ liệu hỏng mà không gọi mô hình,
// lỗi tái hiện xác định (RFC §8.3). Phán đoán ngữ nghĩa (giữ cái nào, phần đầu là gì) được hỏi lại với mô hình, Go không trả lời thay; ranh giới vùng ngữ cảnh chắc chắn sẽ bị kỷ luật tọa độ cắt bỏ nên không hỏi lại vì nó.
type chunkValidator struct {
	projIDs, ownedIDs map[string]bool
	unitByID          map[string]SourceUnit
	normalized        []byte
	coverStart        bool // Khối đầu: văn bản không rỗng trước điểm bắt đầu phải có ranh giới gán thuộc về.
}

func (v chunkValidator) validate(bs []BoundaryDecision) error {
	seen := make(map[int]BoundaryDecision)
	first := -1
	for _, b := range bs {
		if b.UnitID == "" {
			return fmt.Errorf("ranh giới thiếu unit_id")
		}
		if !v.projIDs[b.UnitID] {
			return fmt.Errorf("unit_id %q của ranh giới không tồn tại trong projection lần này", b.UnitID)
		}
		if !v.ownedIDs[b.UnitID] {
			continue
		}
		switch b.Kind {
		case kindChapter, kindGroup, kindFrontMatter, kindBackMatter:
		default:
			return fmt.Errorf("ranh giới %s có kind không hợp lệ: %q (chỉ được chapter/group/front_matter/back_matter)", b.UnitID, b.Kind)
		}
		at, err := resolveBoundaryByte(v.unitByID, b.UnitID, b.Anchor)
		if err != nil {
			return err
		}
		// Echo tiêu đề: tiêu đề của chapter/group phải thật sự xuất hiện trong nguyên văn unit ranh giới (bỏ qua khác biệt khoảng trắng).
		// Tiêu đề bịa được chặn bằng fact tại đây (thực tế từng có nguồn 157 chương, 67 chương là mô hình tạo ranh giới + tiêu đề bịa trên đoạn tiếp của chương).
		// Quyền cân nhắc ngữ nghĩa vẫn thuộc mô hình: nguồn thật không có quy ước tiêu đề có thể đặt uncertain để giữ tiêu đề suy luận; tiêu đề mô tả của front/back matter rủi ro thấp nên không kiểm.
		if (b.Kind == kindChapter || b.Kind == kindGroup) && !b.Uncertain {
			if t := squashSpace(b.Title); t != "" && !strings.Contains(squashSpace(v.unitByID[b.UnitID].Text), t) {
				return fmt.Errorf("không tìm thấy tiêu đề %q của ranh giới %s trong nguyên văn unit đó: nếu đây là phần tiếp của chương trước, đừng đặt ranh giới cho nó (nó thuộc về ranh giới trước, boundaries có thể rỗng); nếu nguyên văn thật sự không có dòng tiêu đề và tiêu đề là do bạn suy luận, hãy đặt uncertain=true",
					b.UnitID, snippet(b.Title, 24))
			}
		}
		// Xung đột cùng vị trí (khác kind/tiêu đề) là vấn đề ngữ nghĩa, Go không phán giữ cái nào; trùng hoàn toàn là dư thừa cơ học, cho qua rồi resolve sẽ lặng lẽ khử trùng.
		// Trùng hoàn toàn là dư thừa cơ học, cho qua rồi resolve sẽ lặng lẽ khử trùng.
		if prev, ok := seen[at]; ok {
			if prev.Kind != b.Kind || boundaryLabel(prev) != boundaryLabel(b) {
				return fmt.Errorf("ranh giới %q và %q rơi vào cùng vị trí (%s), xung đột ngữ nghĩa, vui lòng chỉ giữ một ranh giới đúng",
					boundaryLabel(prev), boundaryLabel(b), b.UnitID)
			}
		} else {
			seen[at] = b
		}
		if first < 0 || at < first {
			first = at
		}
	}
	if v.coverStart {
		head := first
		if head < 0 {
			head = len(v.normalized) // Khối đầu không báo ranh giới owned nào: toàn bộ văn bản đầu chưa được gán.
		}
		if head > 0 && strings.TrimSpace(string(v.normalized[:head])) != "" {
			return fmt.Errorf("%d byte đầu (%s…) chưa thuộc bất kỳ ranh giới nào, hãy bổ sung ranh giới cho đầu văn bản (front_matter/chapter/group)",
				head, snippet(string(v.normalized[:min(head, 48)]), 24))
		}
	}
	return nil
}
