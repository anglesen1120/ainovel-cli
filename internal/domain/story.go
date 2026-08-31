package domain

import (
	"fmt"
	"strings"
)

// BookMetadata là thông tin tác phẩm dành cho độc giả và ấn phẩm.
// Thiết lập sáng tác thuộc về Foundation, tiến độ vận hành thuộc về Progress; cả hai đều không chứa dữ liệu này.
type BookMetadata struct {
	Title    string `json:"title"`
	Synopsis string `json:"synopsis"`
}

// Normalized trả về giá trị chuẩn hóa có thể lưu bền vững và so sánh.
func (b BookMetadata) Normalized() BookMetadata {
	b.Title = strings.TrimSpace(b.Title)
	b.Synopsis = strings.TrimSpace(b.Synopsis)
	return b
}

// Validate kiểm tra các trường bắt buộc của thông tin tác phẩm.
func (b BookMetadata) Validate() error {
	b = b.Normalized()
	if b.Title == "" {
		return fmt.Errorf("bắt buộc phải có tiêu đề sách")
	}
	if b.Synopsis == "" {
		return fmt.Errorf("bắt buộc phải có tóm tắt sách")
	}
	return nil
}

// OutlineEntry mục đại cương, tương ứng với một chương.
type OutlineEntry struct {
	Chapter   int      `json:"chapter"`
	Title     string   `json:"title"`
	CoreEvent string   `json:"core_event"`
	Hook      string   `json:"hook"`
	Scenes    []string `json:"scenes"`
}

// Character hồ sơ nhân vật.
type Character struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"` // bí danh/danh hiệu/biệt danh (như "thiếu niên phế vật", "anh Viêm")
	Role        string   `json:"role"`
	Description string   `json:"description"`
	Arc         string   `json:"arc"`
	Traits      []string `json:"traits"`
	Tier        string   `json:"tier,omitempty"` // core / important / secondary / decorative (mặc định important)
}

// VolumeOutline đại cương cấp tập (chế độ phân tầng truyện dài).
type VolumeOutline struct {
	Index int          `json:"index"`
	Title string       `json:"title"`
	Theme string       `json:"theme"`           // xung đột/chủ đề cốt lõi của tập này
	Final bool         `json:"final,omitempty"` // tập kết thúc: toàn sách khép lại trong tập này (được kiến trúc sư tuyên bố khi append_volume)
	Arcs  []ArcOutline `json:"arcs"`
}

// IsExpanded xác định tập đã được mở rộng hay chưa (có cấu trúc cấp cung truyện).
func (v *VolumeOutline) IsExpanded() bool { return len(v.Arcs) > 0 }

// FinaleVolume trả về số thứ tự tập kết thúc đã được tuyên bố; nếu chưa tuyên bố thì trả về 0.
// Sự thật kết thúc = "tập cuối cùng mang dấu Final": sau khi tuyên bố, toàn sách đi vào trạng thái khép lại (lập kế hoạch thu tuyến, cấu trúc tập cuối
// viết xong là hoàn tất); nếu sau đó lại bổ sung tập mới không đánh dấu, tập mới trở thành tập cuối cùng, trạng thái khép lại tự nhiên được gỡ bỏ —
// vì vậy không cần công cụ hủy bỏ; trạng thái luôn có thể suy ra từ dữ liệu đại cương.
func FinaleVolume(volumes []VolumeOutline) int {
	if n := len(volumes); n > 0 && volumes[n-1].Final {
		return volumes[n-1].Index
	}
	return 0
}

// StoryCompass la bàn định hướng kết cục, thay thế danh sách tập khung cố định.
// Architect có thể cập nhật ở mỗi ranh giới tập, cho phép hướng truyện tiến hóa theo quá trình sáng tác.
type StoryCompass struct {
	EndingDirection string   `json:"ending_direction"`          // hướng kết cục (mô tả mang tính chủ đề)
	OpenThreads     []string `json:"open_threads,omitempty"`    // tuyến dài đang hoạt động (cần khép lại mới có thể kết thúc)
	EstimatedScale  string   `json:"estimated_scale,omitempty"` // quy mô mơ hồ (ví dụ "dự kiến 4-6 tập")
	LastUpdated     int      `json:"last_updated,omitempty"`    // số chương đã hoàn thành tại thời điểm cập nhật
}

// ArcOutline đại cương cấp cung truyện.
type ArcOutline struct {
	Index             int            `json:"index"` // số thứ tự cung truyện trong tập
	Title             string         `json:"title"`
	Goal              string         `json:"goal"`                         // mục tiêu cung truyện (khởi-thừa-chuyển-hợp)
	EstimatedChapters int            `json:"estimated_chapters,omitempty"` // số chương ước tính của cung truyện khung (đặt về 0 sau khi mở rộng)
	Chapters          []OutlineEntry `json:"chapters"`
}

// IsExpanded xác định cung truyện đã được mở rộng hay chưa (có chương chi tiết).
func (a *ArcOutline) IsExpanded() bool { return len(a.Chapters) > 0 }

// ArcExpansion là kế hoạch hoàn chỉnh mà Architect lập cho một cung truyện chưa viết tại ranh giới cấu trúc.
// Title/Goal không phải bản sao máy móc của khung: mô hình có thể dựa vào chính văn đã hoàn thành để chỉnh sửa kế hoạch chưa xảy ra.
type ArcExpansion struct {
	Title    string         `json:"title"`
	Goal     string         `json:"goal"`
	Chapters []OutlineEntry `json:"chapters"`
}

// EstimatedChapterCapacity tính ước lượng dung lượng nội bộ của đại cương phân tầng: cung truyện đã mở rộng tính theo số chương thật,
// cung truyện khung tính theo EstimatedChapters. Nó chỉ dùng cho chiến lược ngữ cảnh, không phải tổng số chương toàn sách; các chương đã thực sự được chi tiết hóa
// và có thể viết luôn đến từ FlattenOutline; cấm phơi bày giá trị này cho người dùng hoặc mô hình.
func EstimatedChapterCapacity(volumes []VolumeOutline) int {
	n := 0
	for _, v := range volumes {
		for _, a := range v.Arcs {
			if a.IsExpanded() {
				n += len(a.Chapters)
			} else {
				n += a.EstimatedChapters
			}
		}
	}
	return n
}

// FlattenOutline bung đại cương phân tầng thành danh sách chương phẳng, giữ số chương toàn cục liên tục.
func FlattenOutline(volumes []VolumeOutline) []OutlineEntry {
	var result []OutlineEntry
	ch := 1
	for _, v := range volumes {
		for _, a := range v.Arcs {
			for _, e := range a.Chapters {
				e.Chapter = ch
				result = append(result, e)
				ch++
			}
		}
	}
	return result
}

// WorldRule mục quy tắc thế giới quan.
type WorldRule struct {
	Category string `json:"category"` // magic / technology / geography / society / other
	Rule     string `json:"rule"`     // mô tả quy tắc
	Boundary string `json:"boundary"` // ranh giới không được vi phạm
}
