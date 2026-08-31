// Package llmretry là lõi retry tầng request dùng chung cho lệnh gọi mô hình trực tiếp: chỉ retry lỗi mà adapter mô hình
// đánh dấu rõ là retryable, tuân thủ Retry-After/backoff lũy thừa và thông qua ToolProgress
// đưa tiến độ vào chuỗi quan sát workbench hiện có. Lỗi kết thúc như tài khoản, xác thực, quyền sẽ trả ngay;
// lỗi retryable tiếp tục retry, vòng đời chỉ do context điều khiển.
package llmretry

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/voocel/agentcore"
)

const maxRetryDelay = 60 * time.Second

// Generator là interface mô hình tối thiểu cần cho retry request.
type Generator interface {
	Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error)
}

// Event mô tả một lần retry request sắp diễn ra.
type Event struct {
	Attempt int
	Delay   time.Duration
	Err     error
}

// Config cấu hình thông tin quan sát của retry, không đổi ngữ nghĩa retry.
type Config struct {
	Agent   string
	OnRetry func(Event)
}

// Generate gọi model.Generate. Lỗi retryable tiếp tục retry sau backoff cho tới khi thành công hoặc
// context kết thúc; lỗi không retryable trả ngay.
func Generate(ctx context.Context, model Generator, cfg Config, messages []agentcore.Message, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	for retry := 1; ; retry++ {
		resp, err := model.Generate(ctx, messages, nil, opts...)
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || !isRetryable(err) {
			return nil, err
		}

		delay := retryDelay(err, retry-1)
		event := Event{Attempt: retry, Delay: delay, Err: err}
		if cfg.OnRetry != nil {
			cfg.OnRetry(event)
		}
		meta, _ := json.Marshal(struct {
			DelayMS int64 `json:"retry_delay_ms"`
		}{DelayMS: delay.Milliseconds()})
		agentcore.ReportToolProgress(ctx, agentcore.ProgressPayload{
			Kind:    agentcore.ProgressRetry,
			Agent:   cfg.Agent,
			Attempt: retry,
			Message: err.Error(),
			Meta:    meta,
		})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func isRetryable(err error) bool {
	var retryable agentcore.RetryableError
	return errors.As(err, &retryable) && retryable.Retryable()
}

func retryDelay(err error, attempt int) time.Duration {
	var hinter agentcore.RetryHinter
	if errors.As(err, &hinter) {
		if delay := hinter.RetryAfter(); delay > 0 {
			if delay > maxRetryDelay {
				return maxRetryDelay
			}
			return delay
		}
	}
	delay := time.Second
	for i := 0; i < attempt && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}
