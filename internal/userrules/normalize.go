// Package userrules là tầng dịch vụ chuẩn hóa quy tắc người dùng: các quy tắc ngôn ngữ tự nhiên
// từ nhiều nguồn được gọi LLM để cấu trúc thành các trường ứng viên, sau đó rules.BuildSnapshot
// hợp nhất tất định chúng thành ảnh chụp của cuốn sách.
//
// Phân tầng trách nhiệm:
//   - gói rules: dữ liệu thuần + hợp nhất tất định (Snapshot / Candidate / BuildSnapshot / SystemDefaults)
//   - gói này: chuẩn hóa bằng LLM + điều phối + lưu xuống đĩa (phụ thuộc agentcore + store + rules)
//
// Chuẩn hóa là đường tăng cường, không phải điều kiện tiên quyết để sáng tác: nguồn nào thất bại cũng hạ cấp về raw preferences để quá trình sáng tác tiếp tục.
package userrules

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/rules"
)

// normalizeMaxTokens là giới hạn đầu ra cho một lần chuẩn hóa (token suy luận và JSON dùng chung ngân sách).
// Bản thân JSON chuẩn hóa rất nhỏ (thường <1k); phần dư lớn dành cho ngân sách suy luận của các mô hình
// không thể tắt suy luận — giảm quá thấp có thể làm JSON bị cắt và không phân tích được. max_tokens là giới hạn,
// không phải chi phí tính trước, nên tăng nó không làm tăng chi phí.
const normalizeMaxTokens = 8192

// normalizeContract là DTO sát biên: tất cả trường bắt buộc, fatigue_words dùng mảng đối tượng
// (chế độ strict cấm map có khóa động); hai chế độ dùng chung một quy ước DTO.
var normalizeContract = llmcontract.Contract{
	Name:        "userrules_normalize",
	Description: "Chuẩn hóa quy tắc viết bằng ngôn ngữ tự nhiên của người dùng thành các trường có cấu trúc",
	Schema: schema.Object(
		schema.Property("structured", schema.Object(
			schema.Property("genre", schema.String("Thể loại; để chuỗi rỗng nếu không có")).Required(),
			schema.Property("forbidden_chars", schema.Array("Các ký tự bị cấm xuất hiện", schema.String("Ký tự"))).Required(),
			schema.Property("forbidden_phrases", schema.Array("Các cụm từ bị cấm xuất hiện (khớp chính xác theo nghĩa đen)", schema.String("Cụm từ"))).Required(),
			schema.Property("fatigue_words", schema.Array("Từ lặp và số lần xuất hiện tối đa mỗi chương", schema.Object(
				schema.Property("word", schema.String("Từ lặp")).Required(),
				schema.Property("max_per_chapter", schema.Int("Số lần xuất hiện tối đa mỗi chương (số nguyên dương)")).Required(),
			))).Required(),
		)).Required(),
		schema.Property("preferences", schema.String("Sở thích về văn phong/nhân vật/thẩm mỹ bằng ngôn ngữ tự nhiên; để chuỗi rỗng nếu không có")).Required(),
		schema.Property("uncertain", schema.Array("Các mục cố ý không đưa vào structured và lý do", schema.String("Mục"))).Required(),
	),
}

// Normalizer chuẩn hóa quy tắc ngôn ngữ tự nhiên của một nguồn thành rules.Candidate.
type Normalizer struct {
	model agentcore.ChatModel
}

// NewNormalizer tạo bộ chuẩn hóa từ một ChatModel. Chuẩn hóa là công cụ khởi tạo một lần,
// nên dùng mô hình có năng lực tốt (chẳng hạn mô hình mặc định của ModelSet), không cần bám theo
// mô hình viết yếu hơn.
//
// Chuẩn hóa không ghi đè thinking: tắt tường minh cũng là tham số suy luận mà chỉ một số mô hình hỗ trợ.
// Mô hình chat thông thường sẽ từ chối tham số này. Dùng mặc định provider/model và normalizeMaxTokens
// để dành ngân sách đầu ra cho những mô hình không thể tắt suy luận.
func NewNormalizer(model agentcore.ChatModel) *Normalizer {
	return &Normalizer{model: model}
}

// Normalize chuẩn hóa một nguồn. Khi thất bại, trả về error (kèm nguyên nhân thực) để gọi viên quyết định
// hạ cấp (Service.normalizeOrDegrade lưu ứng viên degraded) — lỗi kỹ thuật không bị giả làm kết quả bình thường,
// và lỗi kết thúc (xác thực/quyền hạn...) không được thử lại.
func (n *Normalizer) Normalize(ctx context.Context, source, text string) (rules.Candidate, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return rules.Candidate{Source: source}, nil
	}
	if n == nil || n.model == nil {
		return rules.Candidate{}, fmt.Errorf("chưa cấu hình mô hình chuẩn hóa")
	}

	out, err := llmcontract.Execute(ctx, n.model, llmcontract.Request[normalizerOutput]{
		Contract:     normalizeContract,
		SystemPrompt: normalizerSystemPrompt,
		Payload:      text,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(normalizeMaxTokens)},
		Validate: func(out *normalizerOutput) error {
			_, err := out.toCandidate(source)
			return err
		},
		Agent: "rules",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				slog.Debug("đã chọn giao thức chuẩn hóa quy tắc", "module", "rules", "source", source,
					"contract", normalizeContract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider, "model", res.Model,
					"schema_fingerprint", normalizeContract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("đã tự sửa đầu ra chuẩn hóa quy tắc", "module", "rules", "source", source,
					"attempt", ev.Attempt, "layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return rules.Candidate{}, fmt.Errorf("chuẩn hóa thất bại: %w", err)
	}
	return out.toCandidate(source)
}

// degraded tạo một ứng viên hạ cấp: khi chuẩn hóa thất bại, dùng nguyên văn làm sở thích phong cách và không trích xuất quy tắc cơ học.
// uncertain đánh dấu nguồn (để có thể hiển thị nguồn nào không phân tích được), nhưng không có chi tiết lỗi kỹ thuật — lỗi kỹ thuật chỉ vào nhật ký.
func degraded(source, text string) rules.Candidate {
	return rules.Candidate{
		Source:      source,
		Preferences: text,
		Uncertain:   []string{source + ": chuẩn hóa thất bại; đã giữ nguyên văn bản làm sở thích văn phong (không trích xuất quy tắc cơ học)"},
		Degraded:    true,
	}
}

// normalizerOutput là DTO biên của bộ chuẩn hóa (dùng chung cho hai chế độ): uncertain luôn là
// mảng chuỗi, fatigue_words luôn là mảng đối tượng — hình dạng được khóa bằng hợp đồng, không còn đoán nhiều dạng.
type normalizerOutput struct {
	Structured  normalizerStructured `json:"structured"`
	Preferences string               `json:"preferences"`
	Uncertain   []string             `json:"uncertain"`
}

type normalizerStructured struct {
	Genre            string             `json:"genre"`
	ForbiddenChars   []string           `json:"forbidden_chars"`
	ForbiddenPhrases []string           `json:"forbidden_phrases"`
	FatigueWords     []fatigueWordEntry `json:"fatigue_words"`
}

type fatigueWordEntry struct {
	Word          string `json:"word"`
	MaxPerChapter int    `json:"max_per_chapter"`
}

// toCandidate kiểm tra DTO biên và chuyển thành ứng viên miền: các mục fatigue phải có từ không rỗng,
// giới hạn là số nguyên dương (có thể phản hồi lỗi này để mô hình sửa); ở miền vẫn dùng map[string]int.
func (o normalizerOutput) toCandidate(source string) (rules.Candidate, error) {
	var fatigue map[string]int
	for _, e := range o.Structured.FatigueWords {
		word := strings.TrimSpace(e.Word)
		if word == "" {
			return rules.Candidate{}, fmt.Errorf("fatigue_words chứa mục từ trống")
		}
		if e.MaxPerChapter < 1 {
			return rules.Candidate{}, fmt.Errorf("fatigue_words[%q].max_per_chapter phải là số nguyên dương, nhận được %d", word, e.MaxPerChapter)
		}
		if fatigue == nil {
			fatigue = make(map[string]int, len(o.Structured.FatigueWords))
		}
		fatigue[word] = e.MaxPerChapter
	}
	return rules.Candidate{
		Source: source,
		Structured: rules.Structured{
			Genre:            strings.TrimSpace(o.Structured.Genre),
			ForbiddenChars:   nonEmpty(o.Structured.ForbiddenChars),
			ForbiddenPhrases: nonEmpty(o.Structured.ForbiddenPhrases),
			FatigueWords:     fatigue,
		},
		Preferences: strings.TrimSpace(o.Preferences),
		Uncertain:   nonEmpty(o.Uncertain),
	}, nil
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizerSystemPrompt chỉ mô tả ngữ nghĩa chuẩn hóa; cấu trúc đầu ra do normalizeContract quản lý tập trung.
// Đã xác thực việc nâng cấp thận trọng bằng 10 ví dụ thực tế (bao gồm bẫy tự đặt ngưỡng): 10/10.
const normalizerSystemPrompt = `Bạn là "bộ chuẩn hóa quy tắc" của hệ thống viết tiểu thuyết AI. Bạn đọc các quy tắc viết dài hạn (ngôn ngữ tự nhiên) của người dùng từ một nguồn, đưa các quy tắc rõ ràng và có thể kiểm tra bằng máy vào structured, còn lại vào preferences hoặc uncertain.

【Nâng cấp thận trọng — quan trọng nhất】
- Chỉ ghi vào structured khi người dùng nêu rõ ràng, không mơ hồ.
- forbidden_chars/forbidden_phrases là mức error: chỉ nâng cấp khi có lệnh cấm rõ ràng như "không được xuất hiện X/cấm dùng X/đừng viết X".
- fatigue_words: chỉ nâng cấp khi có cả "từ cụ thể" lẫn "ngưỡng số lần cụ thể"; những câu như "bớt dùng X/đừng lạm dụng X" không có số phải đưa vào preferences, tuyệt đối không tự đặt ngưỡng.
- Mong muốn về số chữ/độ dài ("mỗi chương 3000 chữ", "ngắn hơn") luôn đưa vào preferences: độ dài chương là vấn đề nhịp kể, cần tự cân nhắc khi sáng tác, không kiểm tra bằng máy.
- Nội dung không thể kiểm tra bằng máy, không có ngưỡng rõ ràng hoặc phụ thuộc ngữ cảnh luôn đưa vào preferences.
- Nguyên tắc: thà bỏ sót khỏi structured còn hơn nâng cấp sai (vì sẽ báo lỗi sai ở mọi chương).

preferences giữ các sở thích về văn phong, nhân vật và thẩm mỹ bằng một đoạn ngôn ngữ tự nhiên dễ đọc.
uncertain giải thích các mục bạn cố ý không đưa vào structured và lý do.`
