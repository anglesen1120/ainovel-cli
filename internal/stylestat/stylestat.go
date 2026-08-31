// Package stylestat thống kê phong cách toàn sách từ nội dung chính đã viết và chỉ xuất dữ kiện.
//
// Mục tiêu: cửa sổ duyệt trong một arc (~10 chương) thường mù với các mẫu đã hóa cứng ở cấp toàn sách: tic câu lặp hàng chục lần mỗi chương,
// kết chương đồng dạng, lặp nguyên văn qua chương. Trong từng chương mọi điểm đều có vẻ "bình thường"; chỉ thống kê toàn sách mới phơi ra được.
// Code giữ phần đếm xác định, không ảo giác; LLM giữ phần phán định, để editor chấm theo số và writer tự tránh.
// Compute tính toàn bộ một lần cho đánh giá offline; runtime dùng Tracker để duy trì tăng dần theo chương.
package stylestat

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// minChapters: dưới số chương này thì không xuất thống kê vì mẫu quá nhỏ, tần suất không có ý nghĩa.
const minChapters = 5

// phraseWindow chỉ đào cụm từ động trong N chương gần nhất: writer cần tránh "thói quen câu chữ hiện tại".
const phraseWindow = 20

// Input là dữ liệu thống kê. Chapters theo thứ tự số chương tăng dần; Stopwords là tên nhân vật và danh từ riêng,
// bị bỏ qua khi đào cụm từ động vì tên xuất hiện tự nhiên với tần suất cao, không phải vấn đề văn phong.
type Input struct {
	Chapters  []string
	Titles    []string
	Stopwords []string
}

// Stats là kết quả thống kê phong cách toàn sách. Mọi trường đều là số liệu thực tế, không chứa phán định hay chỉ thị.
type Stats struct {
	Chapters          int            `json:"chapters"`
	Patterns          []PatternStat  `json:"patterns,omitempty"`
	TopPhrases        []PhraseStat   `json:"top_phrases,omitempty"`
	RepeatedSentences []SentenceStat `json:"repeated_sentences,omitempty"`
	Ending            EndingStat     `json:"ending"`
	OpeningTimeRate   float64        `json:"opening_time_rate"`
	TitleFormats      *TitleStat     `json:"title_formats,omitempty"`
}

// PatternStat đếm toàn sách cho các mẫu câu cố định, tức tic văn phong AI phổ biến.
type PatternStat struct {
	Name       string  `json:"name"`
	Total      int     `json:"total"`
	PerChapter float64 `json:"per_chapter"`
}

// PhraseStat là cụm từ tần suất cao đào được trong phraseWindow chương gần nhất.
type PhraseStat struct {
	Text  string `json:"text"`
	Count int    `json:"count"`
}

// SentenceStat là câu dài lặp nguyên văn qua chương, bằng chứng trực tiếp của việc nhắc lại.
type SentenceStat struct {
	Text     string `json:"text"`
	Chapters int    `json:"chapters"`
	Count    int    `json:"count"`
}

// EndingStat mô tả phân bố hình dạng dòng cuối chương. Kết ngắn tự nó hợp lệ; đồng dạng toàn sách mới là vấn đề.
type EndingStat struct {
	ShortRatio  float64 `json:"short_ratio"`
	MedianRunes int     `json:"median_runes"`
}

// TitleStat đếm việc trộn tiền tố tiêu đề kiểu chương có số và không số, vì trộn lẫn làm lộ dấu vết cơ chế trong thành phẩm.
type TitleStat struct {
	WithPrefix    int `json:"with_prefix"`
	WithoutPrefix int `json:"without_prefix"`
}

// patternDefs là các mẫu câu văn phong AI phổ biến. Số đếm chỉ xấp xỉ vì regex không phân tích cú pháp;
// mục đích là so với đường nền dọc của chính sách này, độ chính xác tuyệt đối không quan trọng.
var patternDefs = []struct {
	name string
	re   *regexp.Regexp
}{
	{"Câu chỉnh hướng 'không phải... mà là...'", regexp.MustCompile(`(?i)không phải[^.!?\n]{1,40}mà là`)},
	{"Nhịp đếm thời gian ngắn", regexp.MustCompile(`(?i)(?:một|hai|ba|vài|nửa) (?:nhịp thở|khoảnh khắc)`)},
	{"So sánh lộ kiểu 'như thể/tựa như'", regexp.MustCompile(`(?i)như thể|tựa như|giống như|hệt như`)},
	{"Nhịp im lặng lặp lại", regexp.MustCompile(`(?i)im lặng|không nói gì|không quay đầu`)},
	{"Khuôn nét mặt lặp lại", regexp.MustCompile(`(?i)ánh mắt.*thoáng qua|khóe miệng.*nhếch|cắn môi|không thể tin`)},
	{"Phản ứng cơ thể rập khuôn", regexp.MustCompile(`(?i)tim.*thắt lại|người.*run lên|hít sâu một hơi lạnh`)},
	{"Dấu hiệu suy nghĩ lộ", regexp.MustCompile(`(?i)nghĩ thầm|nhận ra|cảm thấy|cho rằng`)},
	{"Cụm trừu tượng sáo rỗng", regexp.MustCompile(`(?i)khó nói thành lời|không thể diễn tả|ý nghĩa của.*là|điều thật sự.*là`)},
}

var (
	sentenceSplit = regexp.MustCompile(`[。！？.!?\n]+`)
	openingTimeRe = regexp.MustCompile(`đêm|sáng sớm|bình minh|trời sáng|thức dậy|nắng sớm|suốt đêm`)
	titlePrefixRe = regexp.MustCompile(`^#{0,2}\s*(?:Chương|chapter)\s*\d+`)
)

// shortEndingRunes xem dòng cuối không vượt quá số ký tự này là "kết ngắn".
const shortEndingRunes = 30

// Compute tính thống kê phong cách toàn sách; trả nil khi chưa đủ chương.
func Compute(in Input) *Stats {
	n := len(in.Chapters)
	if n < minChapters {
		return nil
	}
	all := strings.Join(in.Chapters, "\n")

	s := &Stats{Chapters: n}
	for _, def := range patternDefs {
		total := len(def.re.FindAllStringIndex(all, -1))
		if total == 0 {
			continue
		}
		s.Patterns = append(s.Patterns, PatternStat{
			Name:       def.name,
			Total:      total,
			PerChapter: round1(float64(total) / float64(n)),
		})
	}
	s.TopPhrases = minePhrases(recentWindow(in.Chapters), in.Stopwords)
	s.RepeatedSentences = repeatedSentences(in.Chapters)
	s.Ending = endingShape(in.Chapters)
	s.OpeningTimeRate = openingTimeRate(in.Chapters)
	s.TitleFormats = titleFormats(in.Titles)
	return s
}

func recentWindow(chapters []string) []string {
	if len(chapters) <= phraseWindow {
		return chapters
	}
	return chapters[len(chapters)-phraseWindow:]
}

// minePhrases đào cụm 3-6 ký tự có tần suất cao trong cửa sổ. Cụm tiếng Việt có thể chứa khoảng trắng giữa các từ.
// Lọc cụm có dấu câu, ký tự rìa quá rỗng hoặc trúng danh từ riêng; bỏ trùng khi là chuỗi con của cụm đã chọn.
func minePhrases(chapters []string, stopwords []string) []PhraseStat {
	text := strings.Join(chapters, "\n")
	runes := []rune(text)
	threshold := max(8, len(chapters)/2)

	counts := make(map[string]int)
	for size := 3; size <= 6; size++ {
		for i := 0; i+size <= len(runes); i++ {
			gram := runes[i : i+size]
			if !validGram(gram) {
				continue
			}
			counts[string(gram)]++
		}
	}

	stopGrams := stopwordBigrams(stopwords)
	type cand struct {
		text  string
		count int
	}
	var cands []cand
	for g, c := range counts {
		if c < threshold || hitStopword(g, stopGrams) {
			continue
		}
		cands = append(cands, cand{g, c})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].count != cands[j].count {
			return cands[i].count > cands[j].count
		}
		// Cùng tần suất thì lấy cụm dài hơn vì giàu thông tin hơn, rồi sắp ổn định theo thứ tự từ điển
		if len(cands[i].text) != len(cands[j].text) {
			return len(cands[i].text) > len(cands[j].text)
		}
		return cands[i].text < cands[j].text
	})

	var out []PhraseStat
	for _, c := range cands {
		if len(out) >= 8 {
			break
		}
		dup := false
		for _, picked := range out {
			if strings.Contains(picked.Text, c.text) || strings.Contains(c.text, picked.Text) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, PhraseStat{Text: c.text, Count: c.count})
		}
	}
	return out
}

// gramEdgeStop giữ các nguyên âm ASCII ở biên; nguyên âm tiếng Việt có dấu là phần hợp lệ của từ.
const gramEdgeStop = "aeiouy"

func validGram(gram []rune) bool {
	if len(gram) == 0 || gram[0] == ' ' || gram[len(gram)-1] == ' ' {
		return false
	}
	for i, r := range gram {
		if r == ' ' {
			if i == 0 || gram[i-1] == ' ' {
				return false
			}
			continue
		}
		if !isVietnamesePhraseRune(r) {
			return false
		}
	}
	if strings.ContainsRune(gramEdgeStop, gram[0]) || strings.ContainsRune(gramEdgeStop, gram[len(gram)-1]) {
		return false
	}
	return true
}

func isVietnamesePhraseRune(r rune) bool {
	return unicode.IsLetter(r)
}

// stopwordBigrams tách danh từ riêng thành mảnh 2 ký tự: tên người hay lọt vào văn bản dưới dạng một phần.
// Nếu chỉ khớp cả tên sẽ bị sót. Lọc hơi chặt vẫn tốt hơn để tên nhân vật lẫn vào danh sách thói quen câu chữ.
func stopwordBigrams(stopwords []string) []string {
	var grams []string
	for _, w := range stopwords {
		runes := []rune(strings.TrimSpace(w))
		if len(runes) < 2 {
			continue
		}
		for i := 0; i+2 <= len(runes); i++ {
			grams = append(grams, string(runes[i:i+2]))
		}
	}
	return grams
}

func hitStopword(gram string, stopGrams []string) bool {
	for _, g := range stopGrams {
		if strings.Contains(gram, g) {
			return true
		}
	}
	return false
}

// repeatedSentences tìm câu từ 12 ký tự trở lên lặp nguyên văn qua ít nhất 3 chương, lấy top 5 theo số lần.
func repeatedSentences(chapters []string) []SentenceStat {
	type rec struct {
		count    int
		chapters map[int]struct{}
	}
	seen := make(map[string]*rec)
	for ci, text := range chapters {
		for sent, count := range chapterSentenceCounts(text) {
			r := seen[sent]
			if r == nil {
				r = &rec{chapters: make(map[int]struct{})}
				seen[sent] = r
			}
			r.count += count
			r.chapters[ci] = struct{}{}
		}
	}

	var out []SentenceStat
	for sent, r := range seen {
		if len(r.chapters) < 3 {
			continue
		}
		out = append(out, SentenceStat{Text: truncateRunes(sent, 40), Chapters: len(r.chapters), Count: r.count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Text < out[j].Text
	})
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// trimWrappedQuotes bỏ dấu nháy bọc ngoài: cùng một câu thoại có hoặc không có nháy mở không nên bị tính thành hai câu.
func trimWrappedQuotes(sentence string) string {
	return strings.Trim(strings.TrimSpace(sentence), `"'`)
}

func endingShape(chapters []string) EndingStat {
	var lengths []int
	short := 0
	for _, text := range chapters {
		line := lastNonEmptyLine(text)
		if line == "" {
			continue
		}
		n := len([]rune(line))
		lengths = append(lengths, n)
		if n <= shortEndingRunes {
			short++
		}
	}
	if len(lengths) == 0 {
		return EndingStat{}
	}
	sort.Ints(lengths)
	return EndingStat{
		ShortRatio:  round2(float64(short) / float64(len(lengths))),
		MedianRunes: lengths[len(lengths)/2],
	}
}

func openingTimeRate(chapters []string) float64 {
	hit := 0
	for _, text := range chapters {
		if openingTimeRe.MatchString(firstParagraph(text)) {
			hit++
		}
	}
	return round2(float64(hit) / float64(len(chapters)))
}

func titleFormats(titles []string) *TitleStat {
	if len(titles) == 0 {
		return nil
	}
	t := &TitleStat{}
	for _, title := range titles {
		if strings.TrimSpace(title) == "" {
			continue
		}
		if titlePrefixRe.MatchString(title) {
			t.WithPrefix++
		} else {
			t.WithoutPrefix++
		}
	}
	// Chỉ trộn lẫn mới đáng báo cáo; định dạng thống nhất không phải vấn đề về mặt dữ kiện
	if t.WithPrefix == 0 || t.WithoutPrefix == 0 {
		return nil
	}
	return t
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

// firstParagraph lấy dòng đầu tiên không rỗng và không phải tiêu đề Markdown; dòng đầu file chương thường là # title.
func firstParagraph(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }
func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
