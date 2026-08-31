// Gói errs cung cấp các sentinel lỗi cấp ứng dụng cho ainovel-cli.
// Bên gọi bọc lỗi bằng fmt.Errorf("...: %w", errs.ErrXxx) và dùng
// errors.Is để nhận diện từng nhóm lỗi.
//
// Các lỗi runtime của provider (rate_limit / timeout / stream_idle / network / auth
// / context_overflow) nằm trong agentcore — hãy dùng trực tiếp
// agentcore.ClassifyProvider, agentcore.IsFailoverEligible, agentcore.FailoverReason
// và agentcore.IsStreamIdleMessage.
package errs

import "errors"

var (
	ErrConfig           = errors.New("config error")
	ErrProvider         = errors.New("provider error") // khởi tạo / kết nối provider
	ErrStoreRead        = errors.New("store read error")
	ErrStoreWrite       = errors.New("store write error")
	ErrToolArgs         = errors.New("tool args invalid")
	ErrToolPrecondition = errors.New("tool precondition failed")
	ErrToolConflict     = errors.New("tool conflict")
	ErrPhaseTransition  = errors.New("invalid phase transition")
	ErrFlowTransition   = errors.New("invalid flow transition")
)
