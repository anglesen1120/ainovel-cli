package domain

// TimelineEvent sự kiện dòng thời gian.
type TimelineEvent struct {
	Chapter    int      `json:"chapter"`
	Time       string   `json:"time"`
	Event      string   `json:"event"`
	Characters []string `json:"characters,omitempty"`
}

// ForeshadowEntry mục phục bút.
type ForeshadowEntry struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	PlantedAt   int    `json:"planted_at"`
	Status      string `json:"status"` // planted / advanced / resolved
	ResolvedAt  int    `json:"resolved_at,omitempty"`
}

// ForeshadowUpdate thao tác tăng lượng phục bút.
type ForeshadowUpdate struct {
	ID          string `json:"id"`
	Action      string `json:"action"` // plant / advance / resolve
	Description string `json:"description,omitempty"`
}

// RestoreOwnPlants bổ sung các phục bút plant đã được gieo ở chương này trong bản ghi cũ nhưng không còn được khai báo trong bản ghi mới về đầu hàng đợi.
// Một chương đã chôn phục bút nào là sự thật lịch sử của chính nó; viết lại chính văn không thay đổi điều này; nếu bỏ mất nó, khi phát lại toàn bộ bản ghi chương,
// các advance/resolve của chương này và các chương sau sẽ không tìm thấy plant đứng trước, khiến toàn bộ chuỗi báo lỗi.
func RestoreOwnPlants(prev, next []ForeshadowUpdate) []ForeshadowUpdate {
	declared := make(map[string]struct{}, len(next))
	for _, u := range next {
		if u.Action == "plant" {
			declared[u.ID] = struct{}{}
		}
	}
	var restored []ForeshadowUpdate
	for _, u := range prev {
		if u.Action != "plant" {
			continue
		}
		if _, ok := declared[u.ID]; ok {
			continue
		}
		declared[u.ID] = struct{}{}
		restored = append(restored, u)
	}
	if len(restored) == 0 {
		return next
	}
	// plant phải đứng trước advance/resolve cùng chương thì khi phát lại mới có thể tạo mục trước.
	return append(restored, next...)
}

// RelationshipEntry mục quan hệ nhân vật.
type RelationshipEntry struct {
	CharacterA string `json:"character_a"`
	CharacterB string `json:"character_b"`
	Relation   string `json:"relation"`
	Chapter    int    `json:"chapter"`
}

// ConsistencyIssue vấn đề nhất quán.
type ConsistencyIssue struct {
	Type           string `json:"type"`     // chiều vấn đề cụ thể do mô hình đưa ra dựa trên rubric
	Severity       string `json:"severity"` // critical / error / warning
	Description    string `json:"description"`
	Evidence       string `json:"evidence,omitempty"` // bằng chứng: đoạn nguyên văn, tình tiết cụ thể hoặc dữ liệu trạng thái
	Suggestion     string `json:"suggestion,omitempty"`
	Chapters       []int  `json:"chapters,omitempty"` // bằng chứng thực tế nằm ở những chương nào
	RequiresChange bool   `json:"requires_change"`    // có nên lập tức đưa vào hàng đợi làm lại hay không, do ngữ nghĩa Editor phán đoán
}

// DimensionScore điểm đánh giá theo một chiều.
type DimensionScore struct {
	Dimension string `json:"dimension"`         // do rubric đánh giá định nghĩa, có thể mở rộng theo nhiệm vụ
	Score     int    `json:"score"`             // 0-100
	Verdict   string `json:"verdict,omitempty"` // tương thích với đánh giá cũ; lúc chạy không còn dùng ngưỡng để ghi đè phán đoán của mô hình
	Comment   string `json:"comment,omitempty"` // kết luận ngắn gọn của chiều này
}

// ReviewEntry mục duyệt của Editor.
type ReviewEntry struct {
	Chapter          int                `json:"chapter"`
	Scope            string             `json:"scope"` // chapter / global / arc
	Issues           []ConsistencyIssue `json:"issues"`
	Dimensions       []DimensionScore   `json:"dimensions,omitempty"`      // điểm theo từng chiều
	ContractStatus   string             `json:"contract_status,omitempty"` // met / partial / missed
	ContractMisses   []string           `json:"contract_misses,omitempty"` // các mục contract chưa đạt
	ContractNotes    string             `json:"contract_notes,omitempty"`  // tóm tắt về tình hình thực hiện contract
	Verdict          string             `json:"verdict"`                   // accept / polish / rewrite
	Summary          string             `json:"summary"`
	AffectedChapters []int              `json:"affected_chapters,omitempty"` // số chương cần viết lại/đánh bóng
}

// CriticalCount trả về số lượng vấn đề cấp critical.
func (r *ReviewEntry) CriticalCount() int {
	n := 0
	for _, issue := range r.Issues {
		if issue.Severity == "critical" {
			n++
		}
	}
	return n
}

// ErrorCount trả về số lượng vấn đề cấp error.
func (r *ReviewEntry) ErrorCount() int {
	n := 0
	for _, issue := range r.Issues {
		if issue.Severity == "error" {
			n++
		}
	}
	return n
}

// Dimension trả về điểm của chiều được chỉ định; nếu không tồn tại thì trả về nil.
func (r *ReviewEntry) Dimension(name string) *DimensionScore {
	if r == nil {
		return nil
	}
	for i := range r.Dimensions {
		if r.Dimensions[i].Dimension == name {
			return &r.Dimensions[i]
		}
	}
	return nil
}
