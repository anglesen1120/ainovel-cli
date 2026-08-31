package rules

import (
	"fmt"
	"maps"
	"strings"
)

// Snapshot là ảnh chụp user rules đã chuẩn hóa của sách này (meta/user_rules.json).
//
// Đây là nguồn sự thật duy nhất trong runtime: khi mở sách/import/làm mới, nó được chuẩn hóa và hợp nhất từ các nguồn; sau đó novel_context
// injection và kiểm tra commit_chapter đều chỉ đọc bản này, không đọc lại tệp rules nhiều lần (tránh trôi lệch và hai reader phân kỳ).
//
// Chỉ Structured + Preferences được inject cho mô hình (xem Payload); Version / Status / Sources /
// Uncertain là metadata vận hành và chẩn đoán, không đi vào working_memory.user_rules.
type Snapshot struct {
	Version     int        `json:"version"`
	Status      Status     `json:"status"`
	Structured  Structured `json:"structured"`
	Preferences string     `json:"preferences"`
	Sources     []string   `json:"sources"`
	Uncertain   []string   `json:"uncertain"`
}

// Status đánh dấu việc chuẩn hóa snapshot có thành công đầy đủ hay không.
type Status string

const (
	// StatusReady nghĩa là mọi nguồn đều được chuẩn hóa thành công.
	StatusReady Status = "ready"
	// StatusDegraded nghĩa là ít nhất một nguồn chuẩn hóa thất bại và đã hạ cấp thành raw preferences (xem Uncertain / log).
	StatusDegraded Status = "degraded"
)

// SnapshotVersion là phiên bản schema snapshot hiện tại, thuận tiện cho migration sau này.
// v2: chapter_words rời structured (số từ là ràng buộc mềm về ngữ nghĩa, đi qua preferences).
// Snapshot v1 được load tương thích trực tiếp: trường lạ bị bỏ qua khi deserialize, lần lưu chồng tiếp theo sẽ tự hội tụ về v2;
// Cố ý không làm kiểu "lệch phiên bản thì dựng lại", vì như vậy sẽ mất các quy tắc không tái tạo được do AddRuntimeRule thêm trong runtime.
const SnapshotVersion = 2

// Candidate là kết quả ứng viên sau khi chuẩn hóa một nguồn đơn lẻ.
//
// Các nguồn được sắp theo ưu tiên thấp -> cao rồi giao cho BuildSnapshot hợp nhất xác định. LLM chỉ chịu trách nhiệm biến một nguồn đơn lẻ
// từ ngôn ngữ tự nhiên thành Structured/Preferences ứng viên; ưu tiên và ghi đè trường do BuildSnapshot (Go) phân xử.
type Candidate struct {
	Source      string     // Nhãn nguồn dễ đọc, đi vào Snapshot.Sources (ví dụ system_defaults / startup_prompt / global:my.md)
	Structured  Structured // Các trường có cấu trúc ứng viên của nguồn này
	Preferences string     // Nội dung sở thích bằng ngôn ngữ tự nhiên của nguồn này
	Uncertain   []string   // Các mục của nguồn này cố ý chưa nâng lên structured + lý do (chẩn đoán)
	Degraded    bool       // Nguồn này chuẩn hóa thất bại và đã hạ cấp thành raw preferences
}

// Payload trả về hình dạng inject vào working_memory.user_rules: chỉ phơi bày structured + preferences.
// Ngay cả khi cả hai đều rỗng, vẫn trả về cấu trúc ổn định để tránh LLM thấy user_rules=null rồi đi vào nhánh bất thường.
func (s Snapshot) Payload() map[string]any {
	return map[string]any{
		"structured":  s.Structured,
		"preferences": s.Preferences,
	}
}

// BuildSnapshot hợp nhất xác định các ứng viên đã sắp theo ưu tiên (thấp -> cao) thành snapshot.
//
// Quy tắc hợp nhất (toàn bộ xác định ở phía Go, không giao cho LLM):
//   - structured: ghi đè theo trường, nguồn ưu tiên cao ghi đè nguồn ưu tiên thấp; fatigue_words chồng theo từ
//   - preferences: không ghi đè, nối theo thứ tự nguồn (ưu tiên cao ở sau), kèm tiêu đề nguồn
//   - Giá trị rỗng/zero được xem là thiếu trường, không ghi đè giá trị đã có (sanitizeStructured)
//   - Bất kỳ nguồn nào Degraded -> snapshot status=degraded
func BuildSnapshot(cands []Candidate) Snapshot {
	snap := Snapshot{
		Version: SnapshotVersion,
		Status:  StatusReady,
		Sources: make([]string, 0, len(cands)),
	}
	var prefs []string
	for _, c := range cands {
		s := sanitizeStructured(c.Structured)
		if s.Genre != "" {
			snap.Structured.Genre = s.Genre
		}
		if len(s.ForbiddenChars) > 0 {
			snap.Structured.ForbiddenChars = s.ForbiddenChars
		}
		if len(s.ForbiddenPhrases) > 0 {
			snap.Structured.ForbiddenPhrases = s.ForbiddenPhrases
		}
		if len(s.FatigueWords) > 0 {
			snap.Structured.FatigueWords = mergeFatigueWords(snap.Structured.FatigueWords, s.FatigueWords)
		}

		if p := strings.TrimSpace(c.Preferences); p != "" {
			if src := strings.TrimSpace(c.Source); src != "" {
				prefs = append(prefs, fmt.Sprintf("## [%s]\n\n%s", src, p))
			} else {
				prefs = append(prefs, p)
			}
		}
		if src := strings.TrimSpace(c.Source); src != "" {
			snap.Sources = append(snap.Sources, src)
		}
		snap.Uncertain = append(snap.Uncertain, c.Uncertain...)
		if c.Degraded {
			snap.Status = StatusDegraded
		}
	}
	snap.Preferences = strings.Join(prefs, "\n\n")
	return snap
}

// OverlaySnapshot chồng một ứng viên ưu tiên cao lên snapshot hiện có (ứng viên thắng).
//
// Dùng cho action rules của Arbiter trong runtime: không chuẩn hóa lại mọi nguồn, chỉ ghi đè rule mới vào snapshot hiện tại:
// structured ghi đè theo trường, preferences nối thêm một đoạn, sources/uncertain cộng dồn, trạng thái hạ cấp lan truyền.
func OverlaySnapshot(base Snapshot, cand Candidate) Snapshot {
	out := base
	out.Version = SnapshotVersion
	s := sanitizeStructured(cand.Structured)
	if s.Genre != "" {
		out.Structured.Genre = s.Genre
	}
	if len(s.ForbiddenChars) > 0 {
		out.Structured.ForbiddenChars = s.ForbiddenChars
	}
	if len(s.ForbiddenPhrases) > 0 {
		out.Structured.ForbiddenPhrases = s.ForbiddenPhrases
	}
	if len(s.FatigueWords) > 0 {
		out.Structured.FatigueWords = mergeFatigueWords(cloneFatigue(out.Structured.FatigueWords), s.FatigueWords)
	}
	if p := strings.TrimSpace(cand.Preferences); p != "" {
		section := p
		if src := strings.TrimSpace(cand.Source); src != "" {
			section = fmt.Sprintf("## [%s]\n\n%s", src, p)
		}
		if strings.TrimSpace(out.Preferences) == "" {
			out.Preferences = section
		} else {
			out.Preferences = out.Preferences + "\n\n" + section
		}
	}
	if src := strings.TrimSpace(cand.Source); src != "" {
		out.Sources = append(append([]string{}, out.Sources...), src)
	}
	if len(cand.Uncertain) > 0 {
		out.Uncertain = append(append([]string{}, out.Uncertain...), cand.Uncertain...)
	}
	if cand.Degraded {
		out.Status = StatusDegraded
	}
	return out
}

// mergeFatigueWords chồng ngưỡng từ gây mệt mỏi theo từ; src ghi đè ngưỡng cùng từ trong dst (ưu tiên nguồn gần hơn).
// Cho phép người dùng chỉ thêm một số ít từ gây mệt mỏi, không cần liệt kê lại baseline tích hợp.
func mergeFatigueWords(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int, len(src))
	}
	maps.Copy(dst, src)
	return dst
}

func cloneFatigue(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	maps.Copy(out, m)
	return out
}

// SystemDefaults là baseline cơ học tích hợp trong code (nguồn ưu tiên thấp nhất), không đi qua chuẩn hóa LLM.
//
// Các giá trị được chuyển từ front matter của assets/rules/default.md cũ. Căn cứ ngưỡng cũng được giữ lại:
// Các từ gây mệt mỏi ở đoạn sau (như "một", "im lặng", "không nói gì", "thở") đến từ bằng chứng sản phẩm chạy dài 196 chương:
// sau khi bảng phía trước loại bỏ sáo ngữ AI truyền thống, mô hình chuyển sang dùng các "từ nhịp" này 5-7 lần mỗi chương, nên ngưỡng được nới để dung nạp cách dùng bình thường.
func SystemDefaults() Candidate {
	return Candidate{
		Source: "system_defaults",
		Structured: Structured{
			// Các câu sáo AI có chuỗi cố định; checker khớp substring theo nghĩa đen, mẫu có biến (không phải X mà là Y) thuộc tầng ngữ nghĩa.
			ForbiddenPhrases: []string{"ở mức độ nào đó", "điều đáng chú ý là", "không hiểu vì sao", "ngổn ngang trăm mối"},
			FatigueWords: map[string]int{
				"bất giác": 1, "bất ngờ": 1, "như thể": 2, "ngoài ra": 1, "tuy nhiên": 2,
				"một chút": 2, "một thoáng": 2, "một làn": 2, "tựa như": 1, "không khỏi": 1,
				"như một": 3, "im lặng": 2, "không nói gì": 2, "vài nhịp thở": 3, "một nhịp thở": 3, "mấy nhịp thở": 2,
			},
		},
	}
}

// sanitizeStructured thực thi "giá trị rỗng/zero = thiếu trường": bộ chuẩn hóa có thể trả placeholder như genre:"",
// (đã kiểm chứng trong prototype), nên phải xem là chưa khai báo để tránh làm bẩn hợp nhất và kiểm tra cơ học.
func sanitizeStructured(s Structured) Structured {
	out := Structured{}
	if g := strings.TrimSpace(s.Genre); g != "" {
		out.Genre = g
	}
	out.ForbiddenChars = nonEmptyStrings(s.ForbiddenChars)
	out.ForbiddenPhrases = nonEmptyStrings(s.ForbiddenPhrases)
	out.FatigueWords = sanitizeFatigueWords(s.FatigueWords)
	return out
}

func nonEmptyStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func sanitizeFatigueWords(m map[string]int) map[string]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]int, len(m))
	for w, n := range m {
		if w = strings.TrimSpace(w); w == "" || n <= 0 {
			continue
		}
		out[w] = n
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
