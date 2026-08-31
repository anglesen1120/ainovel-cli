package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecisionStore_AppendAndRecent(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("khởi tạo: %v", err)
	}

	first, err := s.Decisions.Append(DecisionRecord{
		Kind: "intervention", Decider: "arbiter",
		Input: "Viết lại chương 3", Facts: json.RawMessage(`{"phase":"writing"}`),
	})
	if err != nil {
		t.Fatalf("thêm: %v", err)
	}
	if first.ID == "" || first.At == "" || first.SchemaVersion != decisionSchemaVersion {
		t.Fatalf("Append phải điền ID/At/SchemaVersion: %+v", first)
	}

	if _, err := s.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "Tiếp tục viết"}); err != nil {
		t.Fatalf("thêm 2: %v", err)
	}
	// Phán quyết thất bại: error là sự kiện kiểm toán, phải được ghi nguyên dạng xuống đĩa và đọc lại được.
	if _, err := s.Decisions.Append(DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: "Phàm Nhân Tu Tiên", Error: "USER_INACTIVE"}); err != nil {
		t.Fatalf("thêm 3: %v", err)
	}

	recent, err := s.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("gần đây: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("phải có 3 bản ghi, got %d", len(recent))
	}
	if recent[2].Error != "USER_INACTIVE" || len(recent[2].Decision) != 0 {
		t.Fatalf("phán quyết thất bại phải có error và không có decision: %+v", recent[2])
	}
	if recent[0].Input != "Viết lại chương 3" || recent[1].Input != "Tiếp tục viết" {
		t.Fatalf("thứ tự bản ghi phải là cũ→mới: %+v", recent)
	}

	// Cắt theo n: chỉ lấy 1 bản ghi gần nhất
	last, err := s.Decisions.Recent(1)
	if err != nil || len(last) != 1 || last[0].Input != "Phàm Nhân Tu Tiên" {
		t.Fatalf("Recent(1) phải lấy bản ghi mới nhất, got %+v err=%v", last, err)
	}
}

func TestDecisionStore_InputTruncation(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("khởi tạo: %v", err)
	}
	huge := strings.Repeat("dài", maxDecisionInputBytes) // 3 byte/chữ, vượt xa giới hạn
	rec, err := s.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: huge})
	if err != nil {
		t.Fatalf("thêm: %v", err)
	}
	if !rec.InputTruncated || len(rec.Input) > maxDecisionInputBytes {
		t.Fatalf("input vượt giới hạn phải bị cắt ngắn và đánh dấu: truncated=%v len=%d", rec.InputTruncated, len(rec.Input))
	}
	// Bản ghi sau khi cắt ngắn vẫn có thể đọc lại
	recent, err := s.Decisions.Recent(1)
	if err != nil || len(recent) != 1 {
		t.Fatalf("đọc lại thất bại: %v", err)
	}
}

// Dòng hỏng đã commit ở giữa file (sau đó vẫn có dòng commit hoàn chỉnh) phải thất bại cứng —— không thể phán quyết trên lịch sử khiếm khuyết.
func TestDecisionStore_RecentRejectsCommittedCorruptLine(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Init(); err != nil {
		t.Fatalf("khởi tạo: %v", err)
	}
	if _, err := s.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "Được"}); err != nil {
		t.Fatalf("thêm: %v", err)
	}
	// Dòng hỏng đã kết thúc bằng '\n' (đã commit đầy đủ nhưng hỏng), sau đó lại thêm một bản ghi hoàn chỉnh.
	if err := s.Decisions.io.AppendLine(decisionsFile, []byte("{\"schema_version\":1,\"kind\":\"interv\n")); err != nil {
		t.Fatalf("thêm dòng hỏng: %v", err)
	}
	if _, err := s.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "Sau đó"}); err != nil {
		t.Fatalf("thêm phần sau: %v", err)
	}
	if _, err := s.Decisions.Recent(10); err == nil {
		t.Fatal("dòng hỏng đã commit ở giữa file phải báo lỗi rõ ràng")
	}
}

// Dòng sót ở đuôi do crash để lại (phần append chưa commit có byte cuối không phải '\n') được dung thứ như not-exist: bỏ dòng sót, trả về các
// bản ghi hoàn chỉnh trước nó, không thất bại cứng —— nếu không thì một lần crash sẽ đầu độc vĩnh viễn kiểm toán append-only.
func TestDecisionStore_RecentToleratesUncommittedTail(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("khởi tạo: %v", err)
	}
	if _, err := s.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "Được"}); err != nil {
		t.Fatalf("thêm: %v", err)
	}
	// Mô phỏng dòng sót ở đuôi bị crash cắt ngang: không kết thúc bằng xuống dòng.
	if err := s.Decisions.io.AppendLine(decisionsFile, []byte(`{"schema_version":1,"kind":"interv`)); err != nil {
		t.Fatalf("thêm phần dở dang: %v", err)
	}
	recent, err := s.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("dòng sót ở đuôi phải được dung thứ, không nên báo lỗi: %v", err)
	}
	if len(recent) != 1 || recent[0].Input != "Được" {
		t.Fatalf("phải bỏ dòng sót và giữ bản ghi đã commit, nhận được: %+v", recent)
	}
	// Khôi phục phải thật sự cắt ngắn phần đuôi trên đĩa, không chỉ bỏ qua trong lần đọc này; nếu không lần append tiếp theo sẽ ghép hai đoạn
	// JSON thành hỏng vĩnh viễn. Sau khi append, đọc lại phải giữ vòng khép kín hoàn chỉnh.
	if _, err := s.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "Sau khôi phục"}); err != nil {
		t.Fatalf("thêm sau khôi phục: %v", err)
	}
	recent, err = s.Decisions.Recent(10)
	if err != nil {
		t.Fatalf("gần đây sau khi thêm: %v", err)
	}
	if len(recent) != 2 || recent[0].Input != "Được" || recent[1].Input != "Sau khôi phục" {
		t.Fatalf("sau khi khôi phục đuôi phải có thể tiếp tục append, nhận được: %+v", recent)
	}
	raw, err := os.ReadFile(filepath.Join(dir, decisionsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("file kiểm toán sau khôi phục phải kết thúc bằng dòng xuống commit: %q", raw)
	}
}

// Ngay cả khi phần đuôi tình cờ là JSON hoàn chỉnh, miễn là không có dòng xuống theo yêu cầu giao thức thì vẫn thuộc về bản ghi chưa commit; khôi phục phải bỏ
// nó và đảm bảo append sau đó không xảy ra ghép `}{`.
func TestDecisionStore_RecoveryDropsValidJSONWithoutCommitNewline(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Init(); err != nil {
		t.Fatalf("khởi tạo: %v", err)
	}
	if _, err := s.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "Đã commit"}); err != nil {
		t.Fatal(err)
	}
	partial, err := json.Marshal(DecisionRecord{SchemaVersion: decisionSchemaVersion, Kind: "intervention", Input: "Chưa commit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Decisions.io.AppendLine(decisionsFile, partial); err != nil {
		t.Fatal(err)
	}

	// Mô phỏng khởi động lại: lần đọc hoặc append tiếp theo chính là ranh giới khôi phục kiểm toán.
	reopened := NewStore(dir)
	if err := reopened.Init(); err != nil {
		t.Fatalf("khởi tạo sau restart: %v", err)
	}
	if _, err := reopened.Decisions.Append(DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: "Sau khởi động lại"}); err != nil {
		t.Fatal(err)
	}
	recent, err := reopened.Decisions.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Input != "Đã commit" || recent[1].Input != "Sau khởi động lại" {
		t.Fatalf("JSON không xuống dòng chưa commit không nên được chấp nhận, nhận được: %+v", recent)
	}
}
