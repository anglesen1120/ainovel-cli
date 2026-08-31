package imp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/llmretry"
	"github.com/voocel/litellm"
)

// callModel là phụ thuộc tối thiểu của lõi vào model, thuận tiện để tiêm mock khi kiểm thử.
type callModel interface {
	Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error)
}

// errTruncated biểu thị model dừng vì độ dài (lỗi dung lượng). Mang văn bản gốc để bên gọi quyết định thất bại hay cứu vớt tiền tố (§9.5).
type errTruncated struct {
	Raw string
}

func (e *errTruncated) Error() string {
	return "đầu ra của model bị cắt ngắn do độ dài (stop=length)"
}

// errSemantic biểu thị lỗi ở tầng đầu ra không thể sửa bằng hỏi lại, mang phản hồi gốc,
// để runner thống nhất ghi artifact thất bại vào failures/ (§14.2), dùng chung cho mọi hàm ngữ nghĩa.
type errSemantic struct {
	Raw string
	Err error
}

func (e *errSemantic) Error() string { return e.Err.Error() }
func (e *errSemantic) Unwrap() error { return e.Err }

// callProfile chứa thinking và các tùy chọn quan sát được, được suy ra từ ModelRuntime do Host thăm dò.
// Giao thức có cấu trúc do callStructured lựa chọn độc lập dựa trên sự thật của model và Contract tĩnh.
type callProfile struct {
	thinking agentcore.ThinkingLevel
	// notify tùy chọn: phản hồi việc retry lùi thời gian của request / hỏi lại khi kiểm tra cho giao diện; nil thì im lặng (§14.1).
	// retryAt khác zero = thời điểm hạn chót của lần retry tiếp theo, UI dựa vào đó để render đếm ngược từng giây (event chỉ mang mốc hạn chót, thời gian còn lại tính khi render).
	notify func(msg string, retryAt time.Time)
	// progress tùy chọn: phản hồi tiến trình nội bộ của giai đoạn kéo dài (chia khối thứ N/M, tóm tắt khoảng N/M); nil thì im lặng.
	// Việc chia/tổng hợp gọi model theo từng khối/từng khoảng ở bên trong hàm, một khối có thể kéo dài vài phút; không có nó thì panel im lặng cả đoạn trông như bị treo (§14.1).
	progress func(current, total int, msg string)
	// log tùy chọn: nhật ký riêng cho import (logs/import.log); nil thì quay về logger mặc định.
	log *slog.Logger
}

func (p callProfile) logger() *slog.Logger {
	if p.log != nil {
		return p.log
	}
	return slog.Default()
}

// step phản hồi một dòng tiến trình thông thường (tiến trình nội bộ của giai đoạn kéo dài).
func (p callProfile) step(current, total int, format string, args ...any) {
	if p.progress != nil {
		p.progress(current, total, fmt.Sprintf(format, args...))
	}
}

// say phản hồi một trạng thái gọi kéo dài. Retry có thể im lặng vài phút (lùi thời gian theo cấp số nhân cộng dồn hơn 2 phút),
// nếu không phản hồi thì người dùng sẽ tưởng nhầm là bị treo.
func (p callProfile) say(format string, args ...any) {
	p.sayRetry(time.Time{}, format, args...)
}

// sayRetry phản hồi một trạng thái kèm thời điểm hạn chót retry, để UI đếm ngược.
func (p callProfile) sayRetry(retryAt time.Time, format string, args ...any) {
	if p.notify != nil {
		p.notify(fmt.Sprintf(format, args...), retryAt)
	}
}

// snippet nén văn bản nhiều dòng thành tóm tắt ngắn một dòng để phản hồi trên giao diện: gộp khoảng trắng, cắt tới max rune.
func snippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// briefErr nén lỗi thành văn bản ngắn một dòng để phản hồi trên giao diện (chuỗi lỗi đầy đủ vẫn đi vào log và artifact thất bại).
// Đưa sự thật có cấu trúc của adapter lên trước: khi cắt ngắn thì ưu tiên giữ lại "loại lỗi nào, mã trạng thái gì", message của gateway có thể hy sinh.
func briefErr(err error) string {
	s := err.Error()
	if d := modelErrDetail(err); d != "" {
		s = d + ": " + s
	}
	return snippet(s, 100)
}

// errTypeLabels dịch phân loại lỗi litellm thành nhãn ngắn tiếng Việt dễ đọc ngay.
var errTypeLabels = map[litellm.ErrorType]string{
	litellm.ErrorTypeAuth:            "xác thực thất bại",
	litellm.ErrorTypeRateLimit:       "bị giới hạn tốc độ",
	litellm.ErrorTypeNetwork:         "lỗi mạng",
	litellm.ErrorTypeValidation:      "tham số request không hợp lệ",
	litellm.ErrorTypeProvider:        "lỗi dịch vụ nguồn",
	litellm.ErrorTypeTimeout:         "hết thời gian chờ",
	litellm.ErrorTypeQuota:           "không đủ hạn ngạch",
	litellm.ErrorTypeModel:           "model không khả dụng",
	litellm.ErrorTypeInternal:        "lỗi nội bộ",
	litellm.ErrorTypeContextOverflow: "vượt giới hạn context",
	litellm.ErrorTypeOverloaded:      "upstream quá tải",
	litellm.ErrorTypeContentFilter:   "bị bộ lọc nội dung chặn",
}

// modelErrDetail trích xuất sự thật có cấu trúc của adapter từ chuỗi lỗi (phân loại lỗi, trạng thái HTTP, provider, model).
// message của gateway thường chỉ có một câu chung chung "Provider returned error"; chỉ dựa vào nó thì không thể biết là sai cấu hình,
// lỗi upstream hay bị giới hạn tốc độ; các sự thật này litellm luôn mang theo, chỉ là không đi vào văn bản Error(). adapter agentcore
// Unwrap cho phép bên gọi biết rõ litellm dùng errors.As để lấy lỗi gốc. Lỗi không phải do gọi model thì trả chuỗi rỗng.
func modelErrDetail(err error) string {
	var le *litellm.LiteLLMError
	if !errors.As(err, &le) {
		return ""
	}
	parts := make([]string, 0, 4)
	if label := errTypeLabels[le.Type]; label != "" {
		parts = append(parts, label)
	}
	if le.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", le.StatusCode))
	}
	if le.Provider != "" {
		parts = append(parts, le.Provider)
	}
	if le.Model != "" {
		parts = append(parts, le.Model)
	}
	return strings.Join(parts, ", ")
}

// callOptions lắp ráp CallOption cho lần gọi này: luôn mang giới hạn đầu ra; tùy theo khả năng mà thêm thinking.
// thinking chỉ được gửi khi không phải Auto -- với model không hỗ trợ thinking, gửi bất kỳ mức nào (kể cả off) đều là tham số bất hợp lệ (cùng chiến lược với arbiter).
func (p callProfile) callOptions(maxTokens int) []agentcore.CallOption {
	opts := []agentcore.CallOption{agentcore.WithMaxTokens(maxTokens)}
	if p.thinking != agentcore.ThinkingAuto {
		opts = append(opts, agentcore.WithThinking(p.thinking))
	}
	return opts
}

// callStructured điều chỉnh executor có cấu trúc thống nhất cho tầng import, đồng thời ánh xạ lỗi phổ dụng thành ngữ nghĩa artifact import.
func callStructured[T any](ctx context.Context, m callModel, contract llmcontract.Contract, systemPrompt, payload string, maxTokens int, prof callProfile, validate func(*T) error) (T, error) {
	out, err := llmcontract.Execute(ctx, m, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: systemPrompt,
		Payload:      payload,
		Options:      prof.callOptions(maxTokens),
		Validate:     validate,
		Agent:        "import",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				prof.logger().Debug("imp chọn giao thức có cấu trúc",
					"contract", contract.Name, "structured_mode", res.Mode,
					"capability_source", res.Source, "provider", res.Provider,
					"model", res.Model, "schema_fingerprint", contract.Fingerprint())
			},
			RequestRetry: func(ev llmretry.Event) {
				prof.sayRetry(time.Now().Add(ev.Delay), "request model thất bại (%s), tiến hành retry lần thứ %d", briefErr(ev.Err), ev.Attempt)
				prof.logger().Warn("imp retry request model", "attempt", ev.Attempt, "delay", ev.Delay, "err", ev.Err)
			},
			Correction: func(ev llmcontract.Correction) {
				prof.say("kiểm tra đầu ra không đạt (%s), hỏi lại lần thứ %d kèm phản hồi lỗi", briefErr(ev.Err), ev.Attempt+1)
				prof.logger().Warn("imp tự phục hồi đầu ra có cấu trúc", "attempt", ev.Attempt,
					"layer", ev.Layer, "structured_mode", ev.Mode, "err", ev.Err)
			},
		},
	})
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var failure *llmcontract.Failure
	if !errors.As(err, &failure) {
		return out, fmt.Errorf("imp: %w", err)
	}
	switch failure.Kind {
	case llmcontract.FailureLength:
		return out, &errTruncated{Raw: failure.Raw}
	case llmcontract.FailureSafety, llmcontract.FailureContract, llmcontract.FailureProtocol:
		if failure.Raw != "" {
			return out, &errSemantic{Raw: failure.Raw, Err: fmt.Errorf("imp: %w", failure)}
		}
	case llmcontract.FailureRequest:
		if detail := modelErrDetail(failure); detail != "" {
			return out, fmt.Errorf("imp: gọi model thất bại (%s): %w", detail, failure)
		}
	}
	return out, fmt.Errorf("imp: %w", failure)
}
