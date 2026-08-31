package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"
)

// DecisionStore ghi nhật ký kiểm tra các quyết định ngữ nghĩa của LLM trong lúc chạy(meta/decisions.jsonl,append-only).
//
// Định vị(docs/engine-arbiter.md §4.3): nguồn dữ liệu cho audit và phát lại ngoại tuyến — ghi lại “lúc đó thấy gì,
// có những fact nào, đã ra quyết định gì”, phục vụ eval hồi quy và đối chiếu A/B cho Arbiter trong tương lai. Nó **không phải** event sourcing,
// cũng **không phải** nguồn dữ liệu khôi phục(khôi phục chỉ dựa vào Progress/Checkpoint/RunMeta và các fact layer khác).
type DecisionStore struct{ io *IO }

func NewDecisionStore(io *IO) *DecisionStore { return &DecisionStore{io: io} }

const (
	decisionSchemaVersion = 1
	decisionsFile         = "meta/decisions.jsonl"
	// maxDecisionInputBytes giới hạn trên cho từng input; nếu vượt sẽ bị cắt và đánh dấu, để tránh văn bản dán dài làm phình to file audit.
	maxDecisionInputBytes = 8 << 10
)

// DecisionRecord là bản ghi kiểm tra của một lầnquyết định ngữ nghĩa. facts chỉ lưu facts có cấu trúc và tham chiếu, không sao chép nguyên văn nội dung.
// input được giữ trong bản ghi(chuẩn cho phát lại ngoại tuyến); việc làm mờ diễn ra ở ranh giới xuất diag, không phải lúc ghi xuống đĩa.
type DecisionRecord struct {
	SchemaVersion  int             `json:"schema_version"`
	ID             string          `json:"id"`
	At             string          `json:"at"`
	Kind           string          `json:"kind"`    // intervention | plan_start | volume_end | ...
	Decider        string          `json:"decider"` // arbiter | architect(duyệt xét cuối tập)
	CheckpointSeq  int64           `json:"checkpoint_seq,omitempty"`
	Input          string          `json:"input,omitempty"`
	InputTruncated bool            `json:"input_truncated,omitempty"`
	Facts          json.RawMessage `json:"facts,omitempty"`
	Decision       json.RawMessage `json:"decision,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Error          string          `json:"error,omitempty"` // chuỗi lỗi khiquyết định thất bại — thất bại cũng là facts audit, thiếu nó thì chẩn đoán chỉ còn suy đoán
	Model          string          `json:"model,omitempty"`
	DurationMs     int64           `json:"duration_ms,omitempty"`
}

// Append Ghi một bản ghiquyết định xuống đĩa; SchemaVersion/At/ID do phương thức này bổ sung, input quá giới hạn sẽ bị cắt.
// Trả về bản ghi đã được bổ sung đầy đủ(ID để bên gọi liên kết, như PlanStartRecord.DecisionID).
func (s *DecisionStore) Append(rec DecisionRecord) (DecisionRecord, error) {
	rec.SchemaVersion = decisionSchemaVersion
	if rec.At == "" {
		rec.At = time.Now().Format(time.RFC3339)
	}
	if rec.ID == "" {
		rec.ID = newDecisionID()
	}
	if len(rec.Input) > maxDecisionInputBytes {
		rec.Input = rec.Input[:maxDecisionInputBytes]
		rec.InputTruncated = true
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return rec, fmt.Errorf("marshal decision: %w", err)
	}
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	// Lần append trước đó có thể đã bị crash trước khi ghi xong newline. Hãy xóa phần đuôi chưa được commit mà giao thức chứng minh được,
	// để tránh ghép trực tiếp JSON mới vào sau một dòng dở dang; không tự ý sửa các bản ghi đã hoàn chỉnh kết thúc bằng newline.
	if _, err := s.committedDataUnlocked(); err != nil {
		return rec, fmt.Errorf("sửa đuôi quyết định: %w", err)
	}
	if err := s.io.AppendLineUnlocked(decisionsFile, append(data, '\n')); err != nil {
		return rec, err
	}
	return rec, nil
}

// Recent trả về n bản ghi gần nhất(cũ → mới); nếu file không tồn tại thì trả về rỗng.
//
// Các dòng hỏng đã được commit phải trả lỗi rõ ràng — Arbiter không thể tiếp tụcquyết định trên gói facts bị thiếu một phần lịch sử.
// Phần đuôi dở dang bị ngắt bởi crash(không có byte cuối là '\n') sẽ được committedDataUnlocked cắt bỏ và cảnh báo rõ ràng; đây không phải
// là sửa chữa theo suy đoán, vì giao thức của file này quy định chỉ các bản ghi kết thúc bằng newline mới được xem là đã commit.
func (s *DecisionStore) Recent(n int) ([]DecisionRecord, error) {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	data, err := s.committedDataUnlocked()
	if err != nil {
		return nil, err
	}
	all, err := parseDecisionRecords(data)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// committedDataUnlocked trả về các bản ghi hoàn chỉnh kết thúc bằng newline, và cắt bỏ khỏi đĩa mọi byte dư sau newline. Bên gọi
// phải giữ write lock io.mu. Việc cắt là idempotent, nếu thất bại thì file gốc được giữ nguyên, lỗi sẽ được đẩy lên rõ ràng.
func (s *DecisionStore) committedDataUnlocked() ([]byte, error) {
	data, err := s.io.ReadFileUnlocked(decisionsFile)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data, nil
	}
	keep := bytes.LastIndexByte(data, '\n') + 1
	if err := os.Truncate(s.io.path(decisionsFile), int64(keep)); err != nil {
		return nil, err
	}
	slog.Warn("đã sửa phần đuôi chưa commit của audit quyết định",
		"module", "store", "file", decisionsFile, "discarded_bytes", len(data)-keep)
	return data[:keep], nil
}

func parseDecisionRecords(data []byte) ([]DecisionRecord, error) {
	var all []DecisionRecord
	lines := bytes.Split(data, []byte{'\n'})
	for i, raw := range lines {
		if i == len(lines)-1 && len(raw) == 0 {
			break
		}
		var rec DecisionRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return nil, fmt.Errorf("phân tích dòng %d của %s: %w", i+1, decisionsFile, err)
		}
		all = append(all, rec)
	}
	return all, nil
}

func newDecisionID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("dec-%d", time.Now().UnixNano())
	}
	return "dec-" + hex.EncodeToString(b[:])
}
