package domain

// CastEntry là một bản ghi vai phụ trong sổ danh sách vai phụ.
//
// Tách rời với Character (characters.json, hồ sơ cốt lõi do Architect duy trì):
//   - CastEntry được công cụ commit_chapter tự động cộng dồn, ghi lại "các nhân vật phụ có tên đã từng xuất hiện"
//   - Character do Architect thiết kế rõ ràng, ghi lại tuyến phát triển nhân cách/đặc điểm/tier của nhân vật chính và vai phụ then chốt
//
// Khi trùng tên thì ưu tiên Character (nhân vật cốt lõi không đưa vào cast_ledger), tránh trùng lặp.
type CastEntry struct {
	Name string `json:"name"`
	// Aliases hiện chưa có kênh ghi; dành sẵn cho công cụ "người dùng steer hợp nhất bí danh" trong tương lai
	// (ví dụ khai báo 'Lý chưởng quầy' và 'Lão Lý' là cùng một người). MergeAppearances đã hỗ trợ tra cứu bí danh.
	Aliases          []string `json:"aliases,omitempty"`
	BriefRole        string   `json:"brief_role,omitempty"` // định vị bằng một câu (lần xuất hiện đầu do Writer điền, có thể bổ sung về sau; không bị ghi đè)
	FirstSeenChapter int      `json:"first_seen_chapter"`
	LastSeenChapter  int      `json:"last_seen_chapter"`
	// AppearanceCount được dẫn xuất từ len(AppearanceChapters), giữ đồng bộ khi merge.
	// Giữ trường tường minh để UI/JSON đọc trực tiếp thuận tiện, không cần tính lại mỗi lần.
	AppearanceCount    int   `json:"appearance_count"`
	AppearanceChapters []int `json:"appearance_chapters"`
	// Promoted đánh dấu mục này đã được nâng cấp vào characters.json. RecentActive sẽ bỏ qua các mục này,
	// tránh triệu hồi trùng với hồ sơ cốt lõi. Kênh nâng cấp hiện chưa được triển khai, trường này là hook dự phòng.
	Promoted bool `json:"promoted,omitempty"`
}

// CastIntro là khai báo giới thiệu của Writer về nhân vật mới xuất hiện khi commit_chapter.
// Chỉ được áp dụng khi tên đó xuất hiện lần đầu hoặc BriefRole trong ledger vẫn còn trống.
type CastIntro struct {
	Name      string `json:"name"`
	BriefRole string `json:"brief_role"`
}
