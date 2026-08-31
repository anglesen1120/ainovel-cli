package stylestat

import (
	"strings"
	"testing"
)

func chapterWith(body string) string {
	return "# Tiêu đề\n" + body
}

func TestComputeBelowMinChapters(t *testing.T) {
	in := Input{Chapters: []string{"a", "b", "c", "d"}}
	if Compute(in) != nil {
		t.Fatal("below minChapters should return nil")
	}
}

func TestComputePatterns(t *testing.T) {
	body := "Anh không phải giận dữ mà là sợ hãi. Im lặng vài nhịp thở. Như thể một ngọn đèn. Ánh mắt cô thoáng qua hoảng loạn, tim thắt lại. Anh cảm thấy đó là nỗi lạnh khó nói thành lời.\nNội dung.\n"
	chapters := make([]string, 6)
	for i := range chapters {
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	want := map[string]int{
		"Câu chỉnh hướng 'không phải... mà là...'": 6,
		"Nhịp đếm thời gian ngắn":                  6,
		"So sánh lộ kiểu 'như thể/tựa như'":        6,
		"Nhịp im lặng lặp lại":                     6,
		"Khuôn nét mặt lặp lại":                    6,
		"Phản ứng cơ thể rập khuôn":                6,
		"Dấu hiệu suy nghĩ lộ":                     6,
		"Cụm trừu tượng sáo rỗng":                  6,
	}
	for _, p := range s.Patterns {
		if w, ok := want[p.Name]; ok && p.Total != w {
			t.Errorf("%s total: got %d want %d", p.Name, p.Total, w)
		}
		if p.PerChapter != 1.0 {
			t.Errorf("%s per_chapter: got %v want 1.0", p.Name, p.PerChapter)
		}
	}
	if len(s.Patterns) != len(want) {
		t.Errorf("want %d pattern classes, got %d: %+v", len(want), len(s.Patterns), s.Patterns)
	}
}

func TestComputeTopPhrasesWithStopwords(t *testing.T) {
	// "Núi" xuất hiện thường xuyên; tên nhân vật phải bị lọc.
	line := "Núi đá. Lục Cửu Uyên đứng chắp tay sau lưng.\n"
	chapters := make([]string, 10)
	for i := range chapters {
		chapters[i] = chapterWith(strings.Repeat(line, 3))
	}
	s := Compute(Input{Chapters: chapters, Stopwords: []string{"Lục Cửu Uyên"}})
	if s == nil {
		t.Fatal("expected stats")
	}
	var hasMountain, hasName bool
	for _, p := range s.TopPhrases {
		if strings.Contains(p.Text, "Núi") {
			hasMountain = true
		}
		if strings.Contains(p.Text, "Cửu") || strings.Contains(p.Text, "Lục") {
			hasName = true
		}
	}
	if !hasMountain {
		t.Errorf("expected Núi phrase mined, got %+v", s.TopPhrases)
	}
	if hasName {
		t.Errorf("character name should be filtered, got %+v", s.TopPhrases)
	}
}

func TestComputeRepeatedSentences(t *testing.T) {
	motto := "Đời này chưa thể đi xa, mong con thay ta ngắm núi biển phương xa."
	chapters := make([]string, 6)
	for i := range chapters {
		body := "Nội dung.\n"
		if i%2 == 0 {
			body += motto + "\n"
		}
		chapters[i] = chapterWith(body)
	}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if len(s.RepeatedSentences) == 0 {
		t.Fatalf("expected repeated sentence, got none")
	}
	got := s.RepeatedSentences[0]
	if got.Chapters != 3 || got.Count != 3 {
		t.Errorf("repeated sentence: %+v", got)
	}
	if !strings.HasPrefix(got.Text, "Đời này chưa thể đi xa") {
		t.Errorf("text: %q", got.Text)
	}
}
func TestComputeEndingAndOpening(t *testing.T) {
	short := chapterWith("Suốt đêm không ngủ.\nNội dung rất dài rất dài rất dài.\nAnh rời đi.")
	long := chapterWith("Chuyện ban ngày.\nNội dung.\nĐây là một câu kết rất rất rất dài, vượt xa ngưỡng ba mươi ký tự để kiểm tra trung vị.")
	chapters := []string{short, short, short, long, long}
	s := Compute(Input{Chapters: chapters})
	if s == nil {
		t.Fatal("expected stats")
	}
	if s.Ending.ShortRatio != 0.6 {
		t.Errorf("short_ratio: got %v want 0.6", s.Ending.ShortRatio)
	}
	if s.OpeningTimeRate != 0.6 {
		t.Errorf("opening_time_rate: got %v want 0.6", s.OpeningTimeRate)
	}
}

func TestComputeTitleFormats(t *testing.T) {
	chapters := make([]string, 5)
	for i := range chapters {
		chapters[i] = chapterWith("Nội dung.")
	}
	// Trộn lẫn thì báo cáo
	s := Compute(Input{Chapters: chapters, Titles: []string{"Chương 1 Gió nổi", "Mây cuộn", "chapter 3 Sấm động"}})
	if s.TitleFormats == nil || s.TitleFormats.WithPrefix != 2 || s.TitleFormats.WithoutPrefix != 1 {
		t.Errorf("title formats: %+v", s.TitleFormats)
	}
	// Thống nhất thì không báo cáo
	s = Compute(Input{Chapters: chapters, Titles: []string{"Gió nổi", "Mây cuộn"}})
	if s.TitleFormats != nil {
		t.Errorf("uniform titles should not report: %+v", s.TitleFormats)
	}
}
