package guard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s := store.NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return s
}

// TestSubAgentGuard_HardStopReasonEscalatesImmediately xác minh: khi mô hình trả
// từ chối phía provider không thể khôi phục như safety / content_filter, StopGuard của subagent
// phải Escalate ngay thay vì inject thông điệp nhắc nhở.
//
// Bối cảnh lịch sử: thực đo hy3-preview:free khi viết chương 2 gặp stop_reason='safety' 8 lần liên tiếp
// bị từ chối; logic cũ lặp lại chèn "phải commit", mô hình tiếp tục safety, tích đủ 3 lần chặn mới leo thang,
// sau đó Engine chạy lại writer tổng cộng 3 lần. Mỗi lần là SubAgent mới → bộ nhớ đệm
// tiền tố đều khởi động lạnh. Sau khi sửa, safety lần đầu leo thang ngay, Engine có thể tạm dừng trực tiếp theo lỗi không thể khôi phục.
//
// Chú ý chỉ kiểm thử safety / content_filter: StopReasonError / StopReasonAborted đi theo
// nhánh kết thúc run trực tiếp trong agentcore loop.go, hoàn toàn không gọi StopGuard; liệt kê vào lại
// đưa vào dead code.
func TestSubAgentGuard_HardStopReasonEscalatesImmediately(t *testing.T) {
	cases := []agentcore.StopReason{
		agentcore.StopReason("safety"),
		agentcore.StopReason("content_filter"),
	}
	for _, sr := range cases {
		t.Run(string(sr), func(t *testing.T) {
			s := newTestStore(t)
			guard := NewWriterStopGuard(s, nil)
			info := agentcore.StopInfo{
				TurnIndex: 1,
				Message:   agentcore.Message{StopReason: sr},
			}
			d := guard(context.Background(), info)
			if !d.Escalate {
				t.Fatalf("stop_reason=%q phải leo thang ngay lập tức, nhận được %#v", sr, d)
			}
			if d.InjectMessage != "" {
				t.Fatalf("stop_reason=%q không được chèn thông điệp nào, nhận được %q", sr, d.InjectMessage)
			}
		})
	}
}

// TestSubAgentGuard_NormalStopStillBlocks đảm bảo hành vi chặn stop_reason bình thường
// không bị ảnh hưởng bởi đường vòng lỗi cứng — khi LLM tự dừng mà chưa commit vẫn phải nhắc.
func TestSubAgentGuard_NormalStopStillBlocks(t *testing.T) {
	s := newTestStore(t)
	guard := NewWriterStopGuard(s, nil)
	info := agentcore.StopInfo{
		TurnIndex: 1,
		Message:   agentcore.Message{StopReason: agentcore.StopReasonStop},
	}
	d := guard(context.Background(), info)
	if d.Escalate {
		t.Fatal("lần chặn đầu tiên với dừng thông thường không được leo thang")
	}
	if d.Allow {
		t.Fatal("dừng thông thường phải bị chặn khi chưa có checkpoint commit")
	}
	if d.InjectMessage == "" {
		t.Fatal("dừng thông thường phải chèn thông điệp tiếp theo")
	}
}

// TestSubAgentGuard_ProgressBetweenBlocksResetsCounter xác minh: nếu giữa hai lần chặn xuất hiện
// checkpoint mới (mô hình được nhắc rồi tạo bản nháp lại, v.v.) thì bộ đếm liên tiếp được đặt lại — chỉ leo thang khi hoàn toàn không có
// artifact mà vẫn quay rỗng liên tiếp, tuân theo ngữ nghĩa "có tiến triển thì đặt lại" (issue #75).
func TestSubAgentGuard_ProgressBetweenBlocksResetsCounter(t *testing.T) {
	s := newTestStore(t)
	guard := NewWriterStopGuard(s, nil)
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	// Chặn → ghi bản nháp mới (có tiến triển) → chặn lại: dù lặp quá ngưỡng cũng không được escalate.
	for i := 0; i < subagentMaxConsecutiveBlocks+2; i++ {
		if d := guard(context.Background(), normalStop); d.Escalate {
			t.Fatalf("đã leo thang ở lần chặn %d dù có tiến triển giữa các lần chặn", i)
		}
		if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "draft", "drafts/01.draft.md", fmt.Sprintf("d%d", i)); err != nil {
			t.Fatalf("thêm bản nháp: %v", err)
		}
	}
	// Dừng tiến triển: chỉ escalate sau khi chặn quay rỗng liên tiếp tích đủ ngưỡng.
	for i := 0; i < subagentMaxConsecutiveBlocks; i++ {
		if d := guard(context.Background(), normalStop); d.Escalate {
			t.Fatalf("leo thang quá sớm ở lần chặn nhàn rỗi %d", i)
		}
	}
	if d := guard(context.Background(), normalStop); !d.Escalate {
		t.Fatal("phải leo thang sau các lần chặn liên tiếp không có tiến triển")
	}
}

// TestWriterStopGuard_StageAwareBlockMessage xác minh thông điệp nhắc nhở được lắp theo step đã ghi xuống:
// "phải gọi commit_chapter" tĩnh sẽ làm mô hình hiểu sai khi thiếu bước trước đó hoặc commit báo lỗi (issue #75).
func TestWriterStopGuard_StageAwareBlockMessage(t *testing.T) {
	s := newTestStore(t)
	guard := NewWriterStopGuard(s, nil)
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	// Không có artifact nào: phải dẫn qua quy trình đầy đủ, thay vì nhắc commit trực tiếp.
	d := guard(context.Background(), normalStop)
	if !strings.Contains(d.InjectMessage, "draft_chapter") || !strings.Contains(d.InjectMessage, "plan_chapter") {
		t.Fatalf("thông điệp không có bản nháp phải đi qua quy trình, nhận được %q", d.InjectMessage)
	}

	// Bản nháp đã ghi xuống: nên nhắc check_consistency để kết thúc.
	if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "draft", "drafts/01.draft.md", "d1"); err != nil {
		t.Fatalf("thêm bản nháp: %v", err)
	}
	d = guard(context.Background(), normalStop)
	if !strings.Contains(d.InjectMessage, "check_consistency") {
		t.Fatalf("thông điệp chỉ có bản nháp phải trỏ đến check_consistency, nhận được %q", d.InjectMessage)
	}

	// Bản nháp + kiểm tra nhất quán đã hoàn thành: chỉ còn submit, và phải chừa lối ra cho trường hợp commit báo lỗi.
	if _, err := s.Checkpoints.Append(domain.ChapterScope(1), "consistency_check", "meta/checks/01.json", "c1"); err != nil {
		t.Fatalf("thêm consistency_check: %v", err)
	}
	d = guard(context.Background(), normalStop)
	if !strings.Contains(d.InjectMessage, "commit_chapter") || !strings.Contains(d.InjectMessage, "lỗi") {
		t.Fatalf("thông điệp sẵn sàng commit phải đề cập commit và xử lý lỗi, nhận được %q", d.InjectMessage)
	}
}

// TestSubAgentGuard_BlockHookReceivesAgentAndReason xác minh callback audit nhận đúng
// tên agent và chuỗi reason — Host dựa vào nó để đưa việc chặn lên TUI.
func TestSubAgentGuard_BlockHookReceivesAgentAndReason(t *testing.T) {
	s := newTestStore(t)
	var agents, reasons []string
	guard := NewWriterStopGuard(s, func(agent, reason string, _ int32) {
		agents = append(agents, agent)
		reasons = append(reasons, reason)
	})
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	for i := 0; i < subagentMaxConsecutiveBlocks+1; i++ {
		guard(context.Background(), normalStop)
	}
	if len(reasons) != subagentMaxConsecutiveBlocks+1 {
		t.Fatalf("hook được gọi %d lần, cần %d", len(reasons), subagentMaxConsecutiveBlocks+1)
	}
	for i, agent := range agents {
		if agent != "writer" {
			t.Fatalf("lần gọi hook %d: agent = %q, cần writer", i, agent)
		}
	}
	for i := 0; i < subagentMaxConsecutiveBlocks; i++ {
		if reasons[i] != "blocked" {
			t.Fatalf("reason[%d] = %q, cần blocked", i, reasons[i])
		}
	}
	if last := reasons[len(reasons)-1]; last != "escalated" {
		t.Fatalf("reason cuối = %q, cần escalated", last)
	}

	// hard_stop cũng phải được báo cáo.
	var hardReasons []string
	hardGuard := NewWriterStopGuard(s, func(_, reason string, _ int32) {
		hardReasons = append(hardReasons, reason)
	})
	hardGuard(context.Background(), agentcore.StopInfo{
		TurnIndex: 1,
		Message:   agentcore.Message{StopReason: agentcore.StopReason("safety")},
	})
	if len(hardReasons) != 1 || hardReasons[0] != "hard_stop" {
		t.Fatalf("reason của hook hard stop = %v, cần [hard_stop]", hardReasons)
	}
}

// TestEditorStopGuard_TaskAware xác minh nhận biết nhiệm vụ: khi được giao tạo tóm tắt arc, chỉ save_review (rà soát)
// không tính là hoàn tất; phải sinh arc_summary mới cho qua — điểm bắt đầu chặn vòng lặp vô hạn của arc khung trong quyển Defect C.
func TestEditorStopGuard_TaskAware(t *testing.T) {
	normalStop := agentcore.StopInfo{TurnIndex: 1, Message: agentcore.Message{StopReason: agentcore.StopReasonStop}}

	// Nhiệm vụ tóm tắt + chỉ lưu review → phải chặn (review không thỏa yêu cầu arc_summary).
	t.Run("nhiệm vụ tóm tắt bị chặn khi chỉ có review", func(t *testing.T) {
		s := newTestStore(t)
		guard := NewEditorStopGuard(s, "Tạo tóm tắt arc 1 quyển 5 (save_arc_summary)", nil)
		if _, err := s.Checkpoints.Append(domain.ArcScope(5, 1), "review", "reviews/v05a01.json", "d1"); err != nil {
			t.Fatalf("thêm review: %v", err)
		}
		if d := guard(context.Background(), normalStop); d.Allow {
			t.Fatal("nhiệm vụ tóm tắt KHÔNG được thỏa bởi checkpoint review")
		}
	})

	// Nhiệm vụ tóm tắt + đã lưu arc_summary → cho qua.
	t.Run("nhiệm vụ tóm tắt được chấp nhận khi có arc_summary", func(t *testing.T) {
		s := newTestStore(t)
		guard := NewEditorStopGuard(s, "Tạo tóm tắt arc 1 quyển 5 (save_arc_summary)", nil)
		if _, err := s.Checkpoints.Append(domain.ArcScope(5, 1), "arc_summary", "summaries/arc-v05a01.json", "d1"); err != nil {
			t.Fatalf("thêm arc_summary: %v", err)
		}
		if d := guard(context.Background(), normalStop); !d.Allow {
			t.Fatal("nhiệm vụ tóm tắt phải được thỏa bởi checkpoint arc_summary")
		}
	})

	// Nhiệm vụ rà soát + đã lưu review → cho qua (giữ nguyên hành vi nới lỏng mặc định).
	t.Run("nhiệm vụ rà soát được chấp nhận khi có review", func(t *testing.T) {
		s := newTestStore(t)
		guard := NewEditorStopGuard(s, "Rà soát cấp arc cho arc 1 quyển 5 (scope=arc)", nil)
		if _, err := s.Checkpoints.Append(domain.ArcScope(5, 1), "review", "reviews/v05a01.json", "d1"); err != nil {
			t.Fatalf("thêm review: %v", err)
		}
		if d := guard(context.Background(), normalStop); !d.Allow {
			t.Fatal("nhiệm vụ rà soát phải được thỏa bởi checkpoint review")
		}
	})
}
