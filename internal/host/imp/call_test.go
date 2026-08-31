package imp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/litellm"
)

// flakyModel sẽ trả về lỗi có thể thử lại trong số lần fails đầu, sau đó phản hồi như mockModel.
type flakyModel struct {
	mockModel
	fails int
}

func (f *flakyModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	if f.fails > 0 {
		f.fails--
		return nil, fastRetryErr{}
	}
	return f.mockModel.Generate(ctx, msgs, tools, opts...)
}

// fastRetryErr có thể thử lại và thời gian chờ cực ngắn (RetryAfter khớp RetryHinter), bảo đảm kiểm thử chạy nhanh.
type fastRetryErr struct{}

func (fastRetryErr) Error() string             { return "rate limited" }
func (fastRetryErr) Retryable() bool           { return true }
func (fastRetryErr) RetryAfter() time.Duration { return time.Millisecond }

// TestCallStructuredNotifiesRetries bảo vệ khả năng nhìn thấy việc thử lại: thông báo về backoff yêu cầu và việc hỏi lại khi kiểm tra đều phải hiện ra,
// nếu không, backoff theo hàm mũ có thể âm thầm kéo dài vài phút, người dùng sẽ tưởng nhập liệu bị treo (vấn đề ảnh chụp màn hình: 3 phút im lặng rồi mới báo lỗi).
// Backoff của yêu cầu còn phải kèm retryAt khác không — bộ đếm ngược của UI phụ thuộc vào nó; việc hỏi lại khi kiểm tra diễn ra ngay lập tức, retryAt bằng không.
func TestCallStructuredNotifiesRetries(t *testing.T) {
	m := &flakyModel{mockModel: mockModel{responses: []string{"không phải JSON", `{"boundaries":[]}`}}, fails: 2}
	var notes []string
	var retries, reasks int
	prof := callProfile{notify: func(s string, retryAt time.Time) {
		notes = append(notes, s)
		if !retryAt.IsZero() {
			retries++
		}
		if strings.Contains(s, "hỏi lại") {
			reasks++
		}
	}}
	if _, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "sys", "p", 100, prof, nil); err != nil {
		t.Fatalf("Nên thành công cuối cùng: %v", err)
	}
	if retries != 2 || reasks != 1 {
		t.Fatalf("Phải hiển thị 2 lần backoff yêu cầu kèm thời điểm hết hạn + 1 lần hỏi lại khi kiểm tra, nhận %d/%d: %v", retries, reasks, notes)
	}
}

// TestBriefErrIncludesAdapterFacts bảo vệ khả năng chẩn đoán của phần lỗi hiển thị: message của gateway có thể chỉ là một câu
// "Provider returned error", phần hiển thị lại phải bổ sung các thông tin có cấu trúc mà litellm mang theo (phân loại/HTTP status/provider/model),
// và các thông tin này phải đứng trước — khi bị cắt bớt, cần giữ lại chúng trước tiên; lỗi không phải từ adapter thì giữ nguyên.
func TestBriefErrIncludesAdapterFacts(t *testing.T) {
	le := &litellm.LiteLLMError{
		Type: litellm.ErrorTypeProvider, StatusCode: 502,
		Provider: "openai", Model: "gpt-x", Message: "Provider returned error",
	}
	got := briefErr(fmt.Errorf("bao bọc bên ngoài: %w", le))
	for _, want := range []string{"lỗi dịch vụ nguồn", "HTTP 502", "openai", "gpt-x", "Provider returned error"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Phần hiển thị phải chứa %q, nhận %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "lỗi dịch vụ nguồn") {
		t.Fatalf("Các thông tin có cấu trúc phải đứng trước, nhận %q", got)
	}
	if got := briefErr(errors.New("lỗi thông thường")); got != "lỗi thông thường" {
		t.Fatalf("Lỗi không phải từ adapter պետք giữ nguyên, nhận %q", got)
	}
}

// TestCallStructuredCancelIsNotSemanticFailure bảo vệ ngữ nghĩa hủy: việc người dùng hủy (Esc) không phải là thất bại ngữ nghĩa,
// nên không được bọc thành errSemantic kiểu "N lần thử" — điều đó sẽ làm lệch hướng điều tra và còn ghi thêm một artifact failures/ gây hiểu nhầm.
func TestCallStructuredCancelIsNotSemanticFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &mockModel{responses: []string{"đầu ra rác"}}
	_, err := callStructured[boundaryBatch](ctx, m, segmentContract, "sys", "p", 100, callProfile{}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Nên trả về context.Canceled, nhận %v", err)
	}
	var se *errSemantic
	if errors.As(err, &se) {
		t.Fatal("Việc hủy không nên bị bọc thành thất bại ngữ nghĩa")
	}
}

// TestCallStructuredCarriesRawOnSemanticFailure bảo vệ §14.2: khi lớp đầu ra vi phạm hợp đồng,
// lỗi phải mang theo phản hồi thô để runner ghi thống nhất vào artifacts lỗi failures/.
func TestCallStructuredCarriesRawOnSemanticFailure(t *testing.T) {
	m := &nativeImportModel{mockModel: &mockModel{responses: []string{"đầu ra lỗi không phải JSON"}}}
	_, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "sys", "payload", 100, callProfile{}, nil)
	var se *errSemantic
	if !errors.As(err, &se) {
		t.Fatalf("Phải trả về errSemantic, nhận %T: %v", err, err)
	}
	if se.Raw != "đầu ra lỗi không phải JSON" || !strings.Contains(se.Error(), "Vi phạm hợp đồng Schema gốc") {
		t.Fatalf("Raw phải mang phản hồi thô cuối cùng, nhận %q", se.Raw)
	}
}

func TestCallStructuredCarriesRawOnProtocolFailure(t *testing.T) {
	m := &nativeImportModel{mockModel: &mockModel{
		responses: []string{"upstream malformed output"},
		stops:     []agentcore.StopReason{agentcore.StopReasonError},
	}}
	_, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "sys", "payload", 100, callProfile{}, nil)
	var se *errSemantic
	if !errors.As(err, &se) || se.Raw != "upstream malformed output" || !strings.Contains(se.Error(), "stop_reason=error") {
		t.Fatalf("Lỗi giao thức phải mang phản hồi thô, nhận %T: %v", err, err)
	}
}
