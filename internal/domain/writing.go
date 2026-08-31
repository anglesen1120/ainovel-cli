package domain

// ChapterPlan ý tưởng viết chương, Writer tự sinh.
// Không còn ép buộc chia tách cảnh; Agent tự quyết định cách tổ chức nội dung.
type ChapterPlan struct {
	Chapter    int             `json:"chapter"`
	Title      string          `json:"title"`
	Goal       string          `json:"goal"`
	Conflict   string          `json:"conflict"`
	Hook       string          `json:"hook"`
	EmotionArc string          `json:"emotion_arc,omitempty"`
	Notes      string          `json:"notes,omitempty"` // Ghi chú tự do của Agent
	Contract   ChapterContract `json:"contract,omitempty"`
}

// ChapterContract là hợp đồng nghiệm thu chương được Writer và Editor chia sẻ.
// Nó định nghĩa các điểm tiến triển bắt buộc của chương, các điểm không được vượt ranh giới và các điểm cần tập trung khi duyệt.
type ChapterContract struct {
	RequiredBeats    []string `json:"required_beats,omitempty"`    // Các điểm tiến triển bắt buộc phải được đặt xuống trong chương
	ForbiddenMoves   []string `json:"forbidden_moves,omitempty"`   // Các tiến triển mà chương này tuyệt đối không được có
	ContinuityChecks []string `json:"continuity_checks,omitempty"` // Các điểm liên tục cần đặc biệt đối chiếu trong chương này
	EvaluationFocus  []string `json:"evaluation_focus,omitempty"`  // Các điểm Editor cần kiểm tra trọng tâm
	EmotionTarget    string   `json:"emotion_target,omitempty"`    // Tuỳ chọn: cảm xúc chính mà chương này hy vọng người đọc cảm nhận
	PayoffPoints     []string `json:"payoff_points,omitempty"`     // Tuỳ chọn: các điểm cảm xúc/chốt hạ mà chương quan trọng hy vọng hồi đáp
	HookGoal         string   `json:"hook_goal,omitempty"`         // Tuỳ chọn: ham muốn theo dõi mà móc câu cuối chương hy vọng kích hoạt
}

// ChapterSummary là tóm tắt chương, dùng cho cửa sổ ngữ cảnh của các chương sau.
type ChapterSummary struct {
	Chapter    int      `json:"chapter"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Characters []string `json:"characters"`
	KeyEvents  []string `json:"key_events"`
}

// ArcSummary là tóm tắt cấp cung, do Editor tạo khi cung kết thúc.
type ArcSummary struct {
	Volume    int      `json:"volume"`
	Arc       int      `json:"arc"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// VolumeSummary là tóm tắt cấp quyển, được tạo khi quyển kết thúc.
type VolumeSummary struct {
	Volume    int      `json:"volume"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// CharacterSnapshot là ảnh chụp trạng thái nhân vật, ghi lại tại ranh giới cung.
type CharacterSnapshot struct {
	Volume     int    `json:"volume"`
	Arc        int    `json:"arc"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Power      string `json:"power,omitempty"`
	Motivation string `json:"motivation"`
	Relations  string `json:"relations,omitempty"`
}

// OutlineFeedback là phản hồi của Writer đối với dàn ý, có thể gửi kèm khi nộp chương.
type OutlineFeedback struct {
	Deviation  string `json:"deviation"`  // Mô tả độ lệch
	Suggestion string `json:"suggestion"` // Gợi ý điều chỉnh
}

// WritingStyleRules là các quy tắc viết được đúc kết từ các chương đã viết, do Editor tạo ở ranh giới cung.
// Thay thế bằng quy tắc thay cho trích đoạn gốc (style_anchors / voice_samples), dùng quy tắc để thay cho việc chép nguyên văn.
type WritingStyleRules struct {
	Volume    int              `json:"volume"`
	Arc       int              `json:"arc"`
	Prose     []string         `json:"prose"`      // 3-5 quy tắc phong cách tự sự, mỗi quy tắc ≤50 ký tự
	Dialogue  []CharacterVoice `json:"dialogue"`   // Quy tắc phong cách đối thoại của nhân vật
	Taboos    []string         `json:"taboos"`     // Danh sách điều cấm kỵ
	UpdatedAt string           `json:"updated_at"` // Dấu thời gian ISO8601
}

// CharacterVoice là quy tắc phong cách đối thoại của một nhân vật.
type CharacterVoice struct {
	Name  string   `json:"name"`
	Rules []string `json:"rules"` // 2-3 quy tắc đặc trưng ngôn ngữ, mỗi quy tắc ≤30 ký tự
}

// RelatedChapter là các chương liên quan được khuyến nghị đọc lại.
type RelatedChapter struct {
	Chapter int    `json:"chapter"`
	Reason  string `json:"reason"`
}

// RecallItem là thông tin dài hạn được chọn lọc để gọi lại theo nhiệm vụ hiện tại.
// Nó không thay thế các công đoạn chính thức, chỉ phụ trách bơm ngược một lượng nhỏ thông tin lịch sử thật sự liên quan của vòng hiện tại cho mô hình.
type RecallItem struct {
	Kind    string `json:"kind"`
	Key     string `json:"key,omitempty"`
	Chapter int    `json:"chapter,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// CommitResult là giá trị trả về có cấu trúc của công cụ commit_chapter.
// Chỉ bao gồm các trường факт; "bước tiếp theo làm gì" sẽ do kênh Reminder tự sinh dựa trên Progress hiện tại.
type CommitResult struct {
	Chapter        int              `json:"chapter"`
	Committed      bool             `json:"committed"`
	WordCount      int              `json:"word_count"`
	NextChapter    int              `json:"next_chapter"`
	ReviewRequired bool             `json:"review_required"`
	ReviewReason   string           `json:"review_reason,omitempty"`
	HookType       string           `json:"hook_type,omitempty"`
	DominantStrand string           `json:"dominant_strand,omitempty"`
	Feedback       *OutlineFeedback `json:"feedback,omitempty"`
	// Tín hiệu phân tầng cho truyện dài
	ArcEnd         bool `json:"arc_end,omitempty"`
	VolumeEnd      bool `json:"volume_end,omitempty"`
	Volume         int  `json:"volume,omitempty"`
	Arc            int  `json:"arc,omitempty"`
	NeedsExpansion bool `json:"needs_expansion,omitempty"`  // Cung tiếp theo là khung xương, cần triển khai chương
	NeedsNewVolume bool `json:"needs_new_volume,omitempty"` // Cần Architect tạo quyển tiếp theo
	NextVolume     int  `json:"next_volume,omitempty"`      // Số thứ tự cung/quyển tiếp theo
	NextArc        int  `json:"next_arc,omitempty"`         // Số thứ tự cung tiếp theo
	// Sự thật ở trạng thái hoàn thành: sau lần commit này, cả cuốn sách đã hoàn tất hay chưa
	BookComplete bool `json:"book_complete,omitempty"`
	// Ảnh chụp của Progress.Flow hiện tại (writing / reviewing / rewriting / polishing)
	Flow string `json:"flow,omitempty"`
}
