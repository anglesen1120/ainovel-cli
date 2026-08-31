// Package userrules là tầng service chuẩn hóa user rules: đưa rules ngôn ngữ tự nhiên từ các nguồn qua lời gọi có cấu trúc của LLM
// để chuẩn hóa thành các trường có cấu trúc ứng viên, rồi rules.BuildSnapshot hợp nhất xác định thành snapshot của sách này.
//
// Trách nhiệm phân tầng:
//   - package rules: dữ liệu thuần + hợp nhất xác định (Snapshot / Candidate / BuildSnapshot / SystemDefaults)
//   - package này: chuẩn hóa bằng LLM + điều phối + ghi xuống đĩa (phụ thuộc agentcore + store + rules)
//
// Chuẩn hóa là đường tăng cường, không phải điều kiện tiên quyết của sáng tác chính: nguồn nào thất bại cũng hạ cấp thành raw preferences, sáng tác chính vẫn phải tiếp tục.
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

// normalizeMaxTokens là giới hạn đầu ra cho một lần chuẩn hóa (token suy luận và đầu ra JSON dùng chung ngân sách này).
// JSON chuẩn hóa tự nó rất nhỏ (thường <1k); phần lớn ngân sách ở đây dành cho suy luận của "mô hình reasoning không thể tắt thinking";
// để quá hẹp sẽ khiến suy luận lấn vào JSON, gây cắt cụt và phân tích thất bại. max_tokens là giới hạn trên chứ không phải lượng tính phí; tăng lên không làm tăng chi phí.
const normalizeMaxTokens = 8192

// normalizeContract là DTO sát biên: mọi trường đều required, fatigue_words dùng mảng object
// (strict mode cấm map có key động), cả hai chế độ dùng chung quy ước DTO này.
var normalizeContract = llmcontract.Contract{
	Name:        "userrules_normalize",
	Description: "Chuẩn hóa rules viết bằng ngôn ngữ tự nhiên của người dùng thành các trường có cấu trúc",
	Schema: schema.Object(
		schema.Property("structured", schema.Object(
			schema.Property("genre", schema.String("Thể loại; nếu không có thì là chuỗi rỗng")).Required(),
			schema.Property("forbidden_chars", schema.Array("Ký tự bị cấm xuất hiện", schema.String("Ký tự"))).Required(),
			schema.Property("forbidden_phrases", schema.Array("Cụm từ bị cấm xuất hiện (khớp chính xác theo nghĩa đen)", schema.String("Cụm từ"))).Required(),
			schema.Property("fatigue_words", schema.Array("Từ gây mệt mỏi và giới hạn xuất hiện mỗi chương", schema.Object(
				schema.Property("word", schema.String("Từ gây mệt mỏi")).Required(),
				schema.Property("max_per_chapter", schema.Int("Giới hạn số lần xuất hiện mỗi chương (số nguyên dương)")).Required(),
			))).Required(),
		)).Required(),
		schema.Property("preferences", schema.String("Sở thích phong cách/nhân vật/thẩm mỹ bằng ngôn ngữ tự nhiên; nếu không có thì là chuỗi rỗng")).Required(),
		schema.Property("uncertain", schema.Array("Mục cố ý chưa nâng lên structured + lý do", schema.String("Mục"))).Required(),
	),
}

// Normalizer chuẩn hóa rules ngôn ngữ tự nhiên của một nguồn đơn lẻ thành rules.Candidate.
type Normalizer struct {
	model agentcore.ChatModel
}

// NewNormalizer khởi tạo bộ chuẩn hóa bằng một ChatModel. Chuẩn hóa là công cụ dùng một lần khi khởi động,
// nên truyền mô hình có năng lực mạnh (ví dụ mô hình mặc định của ModelSet), không cần đi theo mô hình yếu dùng để viết.
//
// Chuẩn hóa không override thinking: off tường minh cũng là tham số reasoning chỉ một số mô hình hỗ trợ,
// mô hình chat thông thường sẽ từ chối. Tiếp tục dùng provider/model mặc định; normalizeMaxTokens
// dành sẵn ngân sách đầu ra cho mô hình không thể tắt thinking.
func NewNormalizer(model agentcore.ChatModel) *Normalizer {
	return &Normalizer{model: model}
}

// Normalize chuẩn hóa một nguồn. Khi thất bại, trả về error (kèm nguyên nhân thật), bên gọi quyết định hạ cấp
// (Service.normalizeOrDegrade tạo ứng viên degraded); lỗi kỹ thuật không còn giả dạng kết quả bình thường,
// lỗi kết thúc (xác thực/quyền, v.v.) không retry.
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
				slog.Debug("Chọn giao thức chuẩn hóa rules", "module", "rules", "source", source,
					"contract", normalizeContract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider, "model", res.Model,
					"schema_fingerprint", normalizeContract.Fingerprint())
			},
			Correction: func(ev llmcontract.Correction) {
				slog.Warn("Tự chữa đầu ra chuẩn hóa rules", "module", "rules", "source", source,
					"attempt", ev.Attempt, "layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err != nil {
		return rules.Candidate{}, fmt.Errorf("chuẩn hóa thất bại: %w", err)
	}
	return out.toCandidate(source)
}

// degraded dựng một ứng viên hạ cấp: khi chuẩn hóa thất bại, xem nguyên văn là sở thích phong cách, không trích xuất quy tắc cơ học nào.
// uncertain đánh dấu nguồn (tiện hiển thị lại "nguồn nào không phân tích được"), nhưng không chứa chi tiết lỗi kỹ thuật; lỗi kỹ thuật chỉ đi vào log.
func degraded(source, text string) rules.Candidate {
	return rules.Candidate{
		Source:      source,
		Preferences: text,
		Uncertain:   []string{source + ": chuẩn hóa thất bại, đã xử lý nguyên văn như sở thích phong cách (chưa trích xuất quy tắc cơ học)"},
		Degraded:    true,
	}
}

// normalizerOutput là DTO biên theo hợp đồng của bộ chuẩn hóa (dùng chung cho hai chế độ): uncertain cố định
// là mảng chuỗi, fatigue_words cố định là mảng object; hình dạng do contract đóng đinh, không đoán nhiều dạng nữa.
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

// toCandidate kiểm tra DTO biên và chuyển thành ứng viên domain: mục fatigue phải có từ không rỗng và giới hạn là số nguyên dương
// (lỗi kiểm tra có thể phản hồi cho mô hình sửa), phía domain vẫn là map[string]int.
func (o normalizerOutput) toCandidate(source string) (rules.Candidate, error) {
	var fatigue map[string]int
	for _, e := range o.Structured.FatigueWords {
		word := strings.TrimSpace(e.Word)
		if word == "" {
			return rules.Candidate{}, fmt.Errorf("fatigue_words chứa mục từ rỗng")
		}
		if e.MaxPerChapter < 1 {
			return rules.Candidate{}, fmt.Errorf("fatigue_words[%q].max_per_chapter phải là số nguyên dương, got %d", word, e.MaxPerChapter)
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

// normalizerSystemPrompt chỉ mô tả ngữ nghĩa chuẩn hóa; cấu trúc đầu ra do normalizeContract duy trì ở một điểm.
// Đã dùng 10 ví dụ thật (gồm bẫy tự bịa ngưỡng) để xác minh nâng cấp bảo thủ đạt yêu cầu (10/10).
const normalizerSystemPrompt = `Bạn là "bộ chuẩn hóa rules" của hệ thống viết tiểu thuyết AI. Bạn đọc rules viết dài hạn (ngôn ngữ tự nhiên) của người dùng từ một nguồn, nâng các quy tắc rõ ràng và có thể kiểm tra cơ học vào structured; phần còn lại đưa vào preferences hoặc uncertain.

[Nâng cấp bảo thủ - quan trọng nhất]
- Chỉ ghi vào structured khi người dùng nêu rõ ràng và không mơ hồ.
- forbidden_chars/forbidden_phrases là cấp error: chỉ nâng cấp các lệnh cấm rõ ràng kiểu "đừng xuất hiện X / cấm dùng X / đừng viết X".
- fatigue_words: chỉ nâng cấp khi đồng thời có "từ rõ ràng" và "ngưỡng số lần rõ ràng"; các yêu cầu "ít dùng X / đừng hay dùng X" không có số thì đưa vào preferences, tuyệt đối không tự bịa ngưỡng.
- Mong muốn về số từ/độ dài ("mỗi chương 3000 chữ", "ngắn hơn một chút") luôn đưa vào preferences: độ dài chương là vấn đề nhịp kể, được nắm bắt tự nhiên khi sáng tác, không kiểm tra cơ học.
- Nội dung không thể kiểm tra cơ học, không có ngưỡng rõ ràng, hoặc phụ thuộc ngữ cảnh thì luôn đưa vào preferences.
- Nguyên tắc: thà bỏ sót khỏi structured còn hơn nâng cấp sai (sẽ báo sai ở mọi chương).

preferences giữ sở thích về phong cách, nhân vật và thẩm mỹ bằng một đoạn ngôn ngữ tự nhiên dễ đọc.
uncertain giải thích các mục bạn cố ý không nâng lên structured và lý do.`
