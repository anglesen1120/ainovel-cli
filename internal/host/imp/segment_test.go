package imp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
)

func TestUnitLessNumericNotLexical(t *testing.T) {
	// Thứ tự từ điển sẽ kết luận L900 > L1000, L1257.2 > L1800; thứ tự số phải ngược lại.
	if !unitLess(SourceUnit{Line: 900}, SourceUnit{Line: 1000}) {
		t.Fatal("L900 phải < L1000 (theo thứ tự số)")
	}
	if !unitLess(SourceUnit{Line: 1257, Part: 2}, SourceUnit{Line: 1800}) {
		t.Fatal("L1257.2 phải < L1800")
	}
	if !unitLess(SourceUnit{Line: 1257, Part: 1}, SourceUnit{Line: 1257, Part: 2}) {
		t.Fatal("part cùng dòng phải theo thứ tự số")
	}
	if unitLess(SourceUnit{Line: 5}, SourceUnit{Line: 5}) {
		t.Fatal("bằng nhau thì không được less")
	}
}

func TestBuildSourceUnitsRoundtrip(t *testing.T) {
	norm := []byte("Chương một\nNội dung một\n\nChương hai\nNội dung hai")
	units := buildSourceUnits(norm, 0)
	// Ghép lại: văn bản của từng unit + '\n' giữa các dòng phải khôi phục văn bản đã chuẩn hóa.
	var b strings.Builder
	for i, u := range units {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(u.Text)
		if u.Text != string(norm[u.StartByte:u.EndByte]) {
			t.Fatalf("phạm vi byte của unit %s không khớp với văn bản", u.ID)
		}
	}
	if b.String() != string(norm) {
		t.Fatalf("ghép lại không khớp: %q", b.String())
	}
	if units[0].ID != "L1" || units[3].ID != "L4" {
		t.Fatalf("ID không khớp: %s %s", units[0].ID, units[3].ID)
	}
}

func TestBuildSourceUnitsVirtualShard(t *testing.T) {
	// Một dòng nguyên vẹn vượt xa ngân sách -> tách thành nhiều unit ảo, ranh giới nằm ở biên ký tự UTF-8.
	long := strings.Repeat("ữ", 100) // mỗi ký tự 2 byte = 200 byte
	units := buildSourceUnits([]byte(long), 30)
	if len(units) < 2 {
		t.Fatalf("dòng vượt ngân sách phải được phân mảnh, nhận được %d", len(units))
	}
	var b strings.Builder
	for _, u := range units {
		if u.Line != 1 || u.Part == 0 {
			t.Fatalf("phân mảnh ảo phải cùng Line, Part>=1: %+v", u)
		}
		b.WriteString(u.Text) // các mảnh cùng một dòng, không phân tách bằng xuống dòng
	}
	if b.String() != long {
		t.Fatal("ghép lại phân mảnh ảo bị mất chữ")
	}
}

func TestResolveBoundaryByteAnchor(t *testing.T) {
	units := []SourceUnit{{ID: "L1", Line: 1, StartByte: 0, EndByte: 9, Text: "mo gio mo"}}
	m := map[string]SourceUnit{"L1": units[0]}
	if _, err := resolveBoundaryByte(m, "L1", "gio"); err != nil {
		t.Fatalf("anchor duy nhất phải thành công: %v", err)
	}
	if _, err := resolveBoundaryByte(m, "L1", "mo"); err == nil {
		t.Fatal("anchor lặp phải thất bại")
	}
	if _, err := resolveBoundaryByte(m, "L1", "vang"); err == nil {
		t.Fatal("anchor không tồn tại phải thất bại")
	}
	if _, err := resolveBoundaryByte(m, "L9", ""); err == nil {
		t.Fatal("unit không tồn tại phải thất bại")
	}
}

func TestPlanChunksCoversWithoutGap(t *testing.T) {
	units := buildSourceUnits([]byte(strings.Repeat("noi dung dong\n", 50)), 0)
	chunks := planChunks(units, 40)
	if len(chunks) < 2 {
		t.Fatalf("phải chia thành nhiều khối, nhận được %d", len(chunks))
	}
	// Không hở, không chồng lấp và phủ đầy đủ.
	if chunks[0][0] != 0 || chunks[len(chunks)-1][1] != len(units) {
		t.Fatal("chưa phủ đầy đủ")
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i][0] != chunks[i-1][1] {
			t.Fatalf("khối %d không nối với khối trước: %v", i, chunks)
		}
	}
}

func segFixture() ([]byte, []SourceUnit) {
	norm := []byte("Lời nói đầu\nCảm ơn đã đọc\nChương một Gió nổi\nNội dung một\nQuyển hai\nChương hai Mây dâng\nNội dung hai")
	return norm, buildSourceUnits(norm, 0)
}

func TestResolveSegmentationHappy(t *testing.T) {
	norm, units := segFixture()
	// L1 Lời nói đầu(front) / L3 Chương một / L5 Quyển hai(group) / L6 Chương hai
	decisions := []BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter, Title: "Lời nói đầu"},
		{UnitID: "L3", Kind: kindChapter, Title: "Chương một Gió nổi"},
		{UnitID: "L5", Kind: kindGroup, Title: "Quyển hai"},
		{UnitID: "L6", Kind: kindChapter, Title: "Chương hai Mây dâng"},
	}
	seg, err := resolveSegmentation(norm, units, decisions)
	if err != nil {
		t.Fatalf("kiểm tra phủ phải qua: %v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("số chương phải là 2 (không tính group), nhận được %d", len(seg.Chapters))
	}
	if seg.Chapters[0].Number != 1 || seg.Chapters[1].Number != 2 {
		t.Fatal("số thứ tự chương phải liên tục")
	}
	if !strings.Contains(seg.Content(norm, 0), "Nội dung một") {
		t.Fatalf("nội dung chương một không khớp: %q", seg.Content(norm, 0))
	}
	// Phủ: đoạn đầu(front_matter) bắt đầu từ 0, chương cuối phủ đến cuối văn bản.
	if len(seg.Matter) == 0 || seg.Matter[0].Kind != kindFrontMatter || seg.Matter[0].Start != 0 {
		t.Fatalf("đoạn đầu phải là front_matter bắt đầu từ 0: %+v", seg.Matter)
	}
	if seg.Chapters[len(seg.Chapters)-1].End != len(norm) {
		t.Fatal("chương cuối phải phủ đến cuối văn bản")
	}
}

func TestResolveSegmentationRejections(t *testing.T) {
	norm, units := segFixture()
	cases := []struct {
		name string
		ds   []BoundaryDecision
	}{
		{"không có chương", []BoundaryDecision{
			{UnitID: "L1", Kind: kindFrontMatter},
		}},
		{"kind không hợp lệ", []BoundaryDecision{
			{UnitID: "L1", Kind: "verse"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := resolveSegmentation(norm, units, c.ds); err == nil {
				t.Fatalf("phải bị từ chối: %s", c.name)
			}
		})
	}
}

// TestResolveSegmentationReordersAndDedups bảo vệ kỷ luật tọa độ của bước dự phòng cuối: khi model trong khối thỉnh thoảng trả sai thứ tự,
// khôi phục xác định bằng cách sắp xếp theo byte (thực đo 319 ranh giới từng thất bại vì 1 chỗ đảo thứ tự, và cache khối sẽ khiến lỗi tái hiện xác định);
// các mục trùng cùng byte giữ mục xuất hiện trước và ghi Notes để đưa vào phần xem trước xác nhận.
func TestResolveSegmentationReordersAndDedups(t *testing.T) {
	norm, units := segFixture()
	seg, err := resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L3", Kind: kindChapter, Title: "Chương một Gió nổi"},
		{UnitID: "L1", Kind: kindChapter, Title: "Mở đầu"}, // sai thứ tự: vị trí nằm trước L3
		{UnitID: "L6", Kind: kindChapter, Title: "Chương hai Mây dâng"},
		{UnitID: "L6", Kind: kindChapter, Title: "Chương hai lặp"}, // trùng cùng byte
	})
	if err != nil {
		t.Fatalf("sai thứ tự/trùng lặp phải được sửa xác định thay vì bị từ chối: %v", err)
	}
	if len(seg.Chapters) != 3 {
		t.Fatalf("phải có 3 chương, nhận được %d: %+v", len(seg.Chapters), seg.Chapters)
	}
	if seg.Chapters[0].Title != "Mở đầu" || seg.Chapters[0].Start != 0 {
		t.Fatalf("sau khi sắp xếp, chương đầu phải là ranh giới có vị trí sớm nhất: %+v", seg.Chapters[0])
	}
	if seg.Chapters[2].Title != "Chương hai Mây dâng" {
		t.Fatalf("trùng cùng byte phải giữ mục xuất hiện trước: %+v", seg.Chapters[2])
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "trùng") {
		t.Fatalf("ranh giới trùng phải được ghi vào Notes: %v", seg.Notes)
	}
}

// TestResolveSegmentationAbsorbsLeadingText bảo vệ sửa chữa xác định cho trường hợp bỏ sót phần đầu: nếu phần giới thiệu/quảng cáo v.v. không rỗng
// ở đầu sách bị model bỏ sót ranh giới, không được phủ quyết ở bước cuối -- phần bỏ sót đã đi vào cache khối, phủ quyết sẽ khiến chạy lại không gọi gì mà tái hiện xác định
// lỗi. Go bổ sung một front_matter để giữ [0, first) và ghi Notes cho phần xem trước xác nhận.
func TestResolveSegmentationAbsorbsLeadingText(t *testing.T) {
	norm, units := segFixture()
	// Chỉ báo cáo các chương bắt đầu từ L3: văn bản không rỗng L1/L2 không có nơi thuộc về.
	seg, err := resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L3", Kind: kindChapter, Title: "Chương một Gió nổi"},
		{UnitID: "L6", Kind: kindChapter, Title: "Chương hai Mây dâng"},
	})
	if err != nil {
		t.Fatalf("văn bản đầu chưa được gán phải được thu vào front_matter thay vì bị từ chối: %v", err)
	}
	if len(seg.Matter) != 1 || seg.Matter[0].Kind != kindFrontMatter || seg.Matter[0].Start != 0 {
		t.Fatalf("phải bổ sung front_matter bắt đầu từ 0: %+v", seg.Matter)
	}
	if len(seg.Chapters) != 2 || seg.Chapters[0].Start == 0 {
		t.Fatalf("chương không được nuốt văn bản phần đầu: %+v", seg.Chapters)
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "mô hình chưa gán") {
		t.Fatalf("phải ghi mô tả để con người kiểm tra: %v", seg.Notes)
	}
}

// TestResolveSegmentationNotesDuplicateTitles bảo vệ tính hiển thị của chương cùng tên: trong nguồn có quy ước tiêu đề,
// tên chương không nên trùng; trùng là tín hiệu xác định của "cùng một chương bị cắt nhầm" -- chỉ ghi Notes (chặn --yes, hiển thị trong preview) để con người
// kiểm tra, Go không quyết định có gộp hay không.
func TestResolveSegmentationNotesDuplicateTitles(t *testing.T) {
	norm, units := segFixture()
	seg, err := resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter, Title: "Lời nói đầu"},
		{UnitID: "L3", Kind: kindChapter, Title: "Chương một Gió nổi"},
		{UnitID: "L6", Kind: kindChapter, Title: "Chương mộtGió nổi"}, // cùng tên (bỏ qua khác biệt khoảng trắng)
	})
	if err != nil {
		t.Fatalf("chương cùng tên phải được cho qua và ghi Notes: %v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("phải có 2 chương, nhận được %d", len(seg.Chapters))
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "tiêu đề trùng nhau") {
		t.Fatalf("phải ghi một mô tả kiểm tra cùng tên: %v", seg.Notes)
	}
}

// TestChunkValidatorOwnedDiscipline bảo vệ phạm vi bao phủ của kiểm tra trong giai đoạn gọi: kind không hợp lệ trong vùng owned,
// anchor hỏng, xung đột ngữ nghĩa cùng vị trí, và phần đầu khối đầu chưa được gán đều phải phản hồi để hỏi lại trong giai đoạn gọi -- nếu cho qua sẽ đi vào cache cùng khối,
// đến cuối resolve mới phát hiện thì chạy lại sẽ không gọi gì và lặp lại đúng dữ liệu xấu đó; ranh giới trong vùng ngữ cảnh chắc chắn bị cắt bỏ, không hỏi lại vì nó;
// mục trùng hoàn toàn cùng vị trí là dư thừa cơ học, cho qua rồi resolve sẽ lặng lẽ khử trùng.
func TestChunkValidatorOwnedDiscipline(t *testing.T) {
	norm, units := segFixture()
	unitByID := map[string]SourceUnit{}
	proj, owned := map[string]bool{}, map[string]bool{}
	for _, u := range units {
		unitByID[u.ID] = u
		proj[u.ID] = true
	}
	owned["L1"], owned["L2"], owned["L3"] = true, true, true
	v := chunkValidator{projIDs: proj, ownedIDs: owned, unitByID: unitByID, normalized: norm}

	cases := []struct {
		name    string
		bs      []BoundaryDecision
		wantErr bool
	}{
		{"owned kind không hợp lệ", []BoundaryDecision{{UnitID: "L1", Kind: "volume"}}, true},
		{"owned anchor hỏng", []BoundaryDecision{{UnitID: "L3", Kind: kindChapter, Anchor: "anchor không tồn tại"}}, true},
		{"owned anchor hợp lệ", []BoundaryDecision{{UnitID: "L3", Kind: kindChapter, Anchor: "Chương một"}}, false},
		{"kind không hợp lệ trong vùng ngữ cảnh không hỏi lại", []BoundaryDecision{{UnitID: "L6", Kind: "volume"}}, false},
		{"ID ảo giác ngoài projection", []BoundaryDecision{{UnitID: "L99", Kind: kindChapter}}, true},
		{"xung đột ngữ nghĩa cùng vị trí hỏi lại", []BoundaryDecision{
			{UnitID: "L1", Kind: kindChapter, Title: "Lời nói đầu"},
			{UnitID: "L1", Kind: kindFrontMatter, Title: "Lời nói đầu"},
		}, true},
		{"trùng hoàn toàn cùng vị trí cho qua", []BoundaryDecision{
			{UnitID: "L1", Kind: kindChapter, Title: "Lời nói đầu"},
			{UnitID: "L1", Kind: kindChapter, Title: "Lời nói đầu"},
		}, false},
		// Phản chiếu tiêu đề: tên chương/tên quyển phải thật sự tồn tại trong nguyên văn unit ranh giới -- tiêu đề bịa của ranh giới ảo bị chặn ở đây.
		{"tiêu đề chương bịa hỏi lại", []BoundaryDecision{{UnitID: "L2", Kind: kindChapter, Title: "Chương nào đó do tôi bịa"}}, true},
		{"tiêu đề suy luận phải uncertain thì cho qua", []BoundaryDecision{{UnitID: "L2", Kind: kindChapter, Title: "Chương nào đó do tôi bịa", Uncertain: true}}, false},
		{"phản chiếu cho phép khác biệt khoảng trắng", []BoundaryDecision{{UnitID: "L3", Kind: kindChapter, Title: "Chương mộtGió nổi"}}, false},
		{"tên quyển bịa hỏi lại", []BoundaryDecision{{UnitID: "L2", Kind: kindGroup, Title: "Quyển chín"}}, true},
		{"tiêu đề mô tả phụ thuộc không kiểm tra", []BoundaryDecision{{UnitID: "L2", Kind: kindFrontMatter, Title: "Dẫn nhập"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := v.validate(c.bs); (err != nil) != c.wantErr {
				t.Fatalf("wantErr=%v, nhận được %v", c.wantErr, err)
			}
		})
	}

	// Phủ phần đầu khối đầu: L1/L2 không rỗng nhưng không có ranh giới gán -> hỏi lại; bổ sung ranh giới điểm đầu thì qua.
	vs := v
	vs.coverStart = true
	if err := vs.validate([]BoundaryDecision{{UnitID: "L3", Kind: kindChapter}}); err == nil {
		t.Fatal("phần đầu khối đầu chưa được gán phải hỏi lại")
	}
	if err := vs.validate([]BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter}, {UnitID: "L3", Kind: kindChapter},
	}); err != nil {
		t.Fatalf("điểm đầu đã được phủ thì phải qua: %v", err)
	}
	if err := vs.validate(nil); err == nil {
		t.Fatal("khối đầu không có ranh giới phải hỏi lại (toàn bộ văn bản đầu chưa được gán)")
	}
}

// TestSegmentClearsChunksOnResolveFailure bảo vệ "cổng tổng" của việc tái hiện xác định từ cache: khi tích hợp cuối thất bại,
// cache khối đã không còn giá trị (digest luôn khớp, chạy lại không gọi gì và đọc lại cùng loạt ranh giới rồi chết thêm lần nữa), phải xóa để đổi lấy cơ hội
// model chia lại lần sau; snapshot quyết định được đưa thống nhất qua errSemantic vào failures/.
func TestSegmentClearsChunksOnResolveFailure(t *testing.T) {
	norm, units := segFixture()
	// Model đánh dấu toàn bộ sách là front_matter: không có chương, Go không thể sửa xác định, bước cuối thất bại.
	m := &mockModel{responses: []string{boundariesJSON(boundaryFixture("L1", "", kindFrontMatter, "Lời nói đầu"))}}
	w := &Workspace{dir: t.TempDir()}
	_, err := Segment(context.Background(), m, "sys", norm, units, "", 0, 0, 4096, callProfile{}, w, "id-1")
	if err == nil {
		t.Fatal("không có chương phải thất bại ở bước cuối")
	}
	var se *errSemantic
	if !errors.As(err, &se) {
		t.Fatalf("thất bại cuối phải là errSemantic (thống nhất đưa vào failures/), nhận được %T", err)
	}
	if _, statErr := os.Stat(filepath.Join(w.dir, dirSegmentChunks)); !os.IsNotExist(statErr) {
		t.Fatalf("sau thất bại cuối, cache khối phải bị xóa: %v", statErr)
	}
}

// mockModel trả về các response đặt sẵn theo thứ tự, dùng cho test hợp đồng typed-call.
// stops có thể chỉ định stop reason cho từng lần gọi; mặc định dùng stop hoặc StopReasonStop.
type mockModel struct {
	responses []string
	stops     []agentcore.StopReason
	i         int
	stop      agentcore.StopReason
}

func (m *mockModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	idx := m.i
	r := m.responses[idx%len(m.responses)]
	sr := m.stop
	if idx < len(m.stops) {
		sr = m.stops[idx]
	}
	if sr == "" {
		sr = agentcore.StopReasonStop
	}
	m.i++
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock(r)},
		StopReason: sr,
	}}, nil
}

// TestResolveSegmentationSingleLineChapters bảo vệ #9: đoạn một dòng không xuống dòng (kịch bản cắt theo anchor) thì cả đoạn là nội dung,
// tiểu thuyết một dòng/một dòng nhiều chương không được bị phán nhầm là "nội dung rỗng" rồi từ chối.
func TestResolveSegmentationSingleLineChapters(t *testing.T) {
	normalized := []byte("Chương một chuyện của A Chương hai chuyện của B") // cả bài một dòng, không xuống dòng
	units := buildSourceUnits(normalized, 0)
	decisions := []BoundaryDecision{
		{UnitID: "L1", Kind: kindChapter, Title: "Chương một"},                       // không anchor -> byte 0
		{UnitID: "L1", Anchor: "Chương hai", Kind: kindChapter, Title: "Chương hai"}, // anchor trong dòng cắt ra chương hai
	}
	seg, err := resolveSegmentation(normalized, units, decisions)
	if err != nil {
		t.Fatalf("một dòng nhiều chương phải được chấp nhận: %v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("phải cắt ra 2 chương, nhận được %d", len(seg.Chapters))
	}
	if got := seg.Content(normalized, 0); got != "Chương một chuyện của A " {
		t.Fatalf("phạm vi nội dung chương đầu không đúng: %q", got)
	}
}

func TestSegmentWithMockModel(t *testing.T) {
	norm, units := segFixture()
	resp := boundariesJSON(
		boundaryFixture("L1", "", kindFrontMatter, "Lời nói đầu"),
		boundaryFixture("L3", "", kindChapter, "Chương một Gió nổi"),
		boundaryFixture("L5", "", kindGroup, "Quyển hai"),
		boundaryFixture("L6", "", kindChapter, "Chương hai Mây dâng"),
	)
	m := &mockModel{responses: []string{resp}}
	seg, err := Segment(context.Background(), m, "sys", norm, units, "", 0, 0, 4096, callProfile{}, nil, "")
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("phải có 2 chương, nhận được %d", len(seg.Chapters))
	}
}

// TestResolveSegmentationAbsorbsEmptyChapter bảo vệ khả năng chịu nguồn bẩn: nguồn tiểu thuyết mạng thực tế thường gặp tiêu đề giữ chỗ kiểu "đã khóa/chương trả phí"
// (có tiêu đề, thiếu nội dung). Những ranh giới này không được làm toàn bộ thất bại -- phủ quyết một phiếu ở bước cuối sẽ lãng phí toàn bộ model call của giai đoạn cắt;
// đoạn giữ chỗ được nhập vào đoạn trước (không mất một chữ văn bản nào), ghi vào Notes để phần xem trước xác nhận hiển thị cho con người kiểm tra.
func TestResolveSegmentationAbsorbsEmptyChapter(t *testing.T) {
	norm, units := segFixture()
	// Dòng L5 "Quyển hai" bị model đánh dấu là tiêu đề chương: span [L5,L6) không có nội dung -> nhập vào chương một.
	decisions := []BoundaryDecision{
		{UnitID: "L1", Kind: kindFrontMatter, Title: "Lời nói đầu"},
		{UnitID: "L3", Kind: kindChapter, Title: "Chương một Gió nổi"},
		{UnitID: "L5", Kind: kindChapter, Title: "Chương năm [chương này đã bị khóa]"},
		{UnitID: "L6", Kind: kindChapter, Title: "Chương hai Mây dâng"},
	}
	seg, err := resolveSegmentation(norm, units, decisions)
	if err != nil {
		t.Fatalf("chương giữ chỗ có nội dung rỗng phải được hấp thụ thay vì làm toàn bộ thất bại: %v", err)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("phải có 2 chương (đoạn giữ chỗ nhập vào đoạn trước), nhận được %d", len(seg.Chapters))
	}
	if got := seg.Content(norm, 0); !strings.Contains(got, "Quyển hai") {
		t.Fatalf("đoạn giữ chỗ phải nhập vào chương một (không mất văn bản): %q", got)
	}
	if len(seg.Notes) != 1 || !strings.Contains(seg.Notes[0], "đã bị khóa") {
		t.Fatalf("phải ghi một mô tả để con người kiểm tra: %v", seg.Notes)
	}
	// Nếu điểm đầu tiên là chương nội dung rỗng: không có đoạn trước để nhập -> chuyển thành front_matter, cũng không thất bại.
	seg, err = resolveSegmentation(norm, units, []BoundaryDecision{
		{UnitID: "L1", Kind: kindChapter, Title: "Giữ chỗ"}, // [L1,L2) một dòng tiêu đề không nội dung
		{UnitID: "L2", Kind: kindChapter, Title: "Chương một"},
	})
	if err != nil {
		t.Fatalf("điểm đầu nội dung rỗng phải chuyển thành front_matter: %v", err)
	}
	if len(seg.Matter) != 1 || seg.Matter[0].Kind != kindFrontMatter {
		t.Fatalf("điểm đầu nội dung rỗng phải là front_matter: %+v", seg.Matter)
	}
}

// TestSegmentClipsContextBoundaries bảo vệ việc thực thi kỷ luật tọa độ ở phía Go: ranh giới model trả về trong vùng ngữ cảnh
// không kích hoạt hỏi lại ngữ nghĩa (model yếu thường cạn 3 lần và kéo sập cả khối), mà được code cắt bỏ trực tiếp -- ranh giới đó thuộc quyền quản lý của khối liền kề,
// khối liền kề sẽ báo cáo nó trong khoảng owned của mình; giữ lại sẽ gây trùng/sai thứ tự xuyên khối.
func TestSegmentClipsContextBoundaries(t *testing.T) {
	// Mỗi khối có một dòng tiêu đề và một dòng nội dung. Mỗi cặp dài 13 byte, nên hai cặp nằm vừa ngân sách 27 byte
	// và mỗi khối đều bắt đầu ở tiêu đề có phần thân; đây là điều kiện cần của phép kiểm đếm ranh giới bên dưới.
	norm := []byte("M1\nNội dung\nM2\nNội dung\nM3\nNội dung\nM4\nNội dung\nM5\nNội dung\nM6\nNội dung")
	units := buildSourceUnits(norm, 0)
	chunks := planChunks(units, planningBudget(40, "sys", "")) // nhất quán với quy hoạch bên trong Segment
	if len(chunks) < 2 {
		t.Fatalf("fixture phải chia ra ít nhất 2 khối, nhận được %d", len(chunks))
	}
	// Response mỗi khối: một ranh giới chương ở unit đầu của owned (không tiêu đề thì dùng firstLine fallback, tránh kiểm tra phản chiếu tiêu đề --
	// ở đây kiểm thử kỷ luật tọa độ); khối đầu tiên kèm thêm một ranh giới của unit đầu khối kế tiếp (vùng ngữ cảnh).
	responses := make([]string, len(chunks))
	for ci, owned := range chunks {
		boundaries := []map[string]any{boundaryFixture(units[owned[0]].ID, "", kindChapter, "")}
		if ci == 0 {
			boundaries = append(boundaries, boundaryFixture(units[chunks[1][0]].ID, "", kindChapter, ""))
		}
		responses[ci] = boundariesJSON(boundaries...)
	}
	var clipNotes int
	prof := callProfile{progress: func(_, _ int, s string) {
		if strings.Contains(s, "đã cắt") {
			clipNotes++
		}
	}}
	seg, err := Segment(context.Background(), &mockModel{responses: responses}, "sys", norm, units, "", 40, 2, 4096, prof, nil, "")
	if err != nil {
		t.Fatalf("ranh giới vùng ngữ cảnh phải bị cắt bỏ thay vì thất bại: %v", err)
	}
	if len(seg.Chapters) != len(chunks) {
		t.Fatalf("phải có %d chương (ranh giới vượt biên không tính trùng), nhận được %d", len(chunks), len(seg.Chapters))
	}
	if clipNotes != 1 {
		t.Fatalf("phải echo 1 mô tả cắt xén, nhận được %d", clipNotes)
	}
}

// TestSegmentReusesChunkArtifacts bảo vệ điểm tiếp tục cấp khối: cắt phân đoạn ghi cache ranh giới từng khối ra đĩa,
// khi chạy lại, khối có digest khớp được tái sử dụng trực tiếp với không model call -- cắt phân đoạn là giai đoạn đắt nhất, bất kỳ khối nào thất bại không nên phải trả lại chi phí các khối đã hoàn tất (cùng triết lý với analyze/synthesize).
func TestSegmentReusesChunkArtifacts(t *testing.T) {
	norm, units := segFixture()
	chunks := planChunks(units, planningBudget(40, "sys", "")) // nhất quán với quy hoạch bên trong Segment
	responses := make([]string, len(chunks))
	for ci, owned := range chunks {
		responses[ci] = boundariesJSON(boundaryFixture(units[owned[0]].ID, "", kindChapter, ""))
	}
	w := &Workspace{dir: t.TempDir()}
	m1 := &mockModel{responses: responses}
	seg1, err := Segment(context.Background(), m1, "sys", norm, units, "", 40, 2, 4096, callProfile{}, w, "id-1")
	if err != nil {
		t.Fatalf("lần chạy đầu: %v", err)
	}
	if m1.i != len(chunks) {
		t.Fatalf("lần chạy đầu phải gọi %d lần, nhận được %d", len(chunks), m1.i)
	}
	m2 := &mockModel{responses: responses}
	seg2, err := Segment(context.Background(), m2, "sys", norm, units, "", 40, 2, 4096, callProfile{}, w, "id-1")
	if err != nil {
		t.Fatalf("chạy lại: %v", err)
	}
	if m2.i != 0 {
		t.Fatalf("khối có digest khớp phải tái sử dụng với không lần gọi, thực tế gọi %d lần", m2.i)
	}
	if len(seg2.Chapters) != len(seg1.Chapters) {
		t.Fatalf("kết quả tái sử dụng phải nhất quán: %d != %d", len(seg2.Chapters), len(seg1.Chapters))
	}
	// Đổi danh tính (đổi phiên bản prompt/hướng dẫn/nguồn) -> cache tự nhiên không khớp, làm lại toàn bộ.
	m3 := &mockModel{responses: responses}
	if _, err := Segment(context.Background(), m3, "sys", norm, units, "", 40, 2, 4096, callProfile{}, w, "id-2"); err != nil {
		t.Fatalf("chạy lại khi danh tính thay đổi: %v", err)
	}
	if m3.i != len(chunks) {
		t.Fatalf("danh tính thay đổi phải làm lại toàn bộ (%d lần gọi), nhận được %d", len(chunks), m3.i)
	}
}

// TestSegmentShrinksChunkOnTruncation bảo vệ vòng lặp ngân sách đầu ra: lượng lớn chương ngắn sẽ khiến JSON ranh giới một khối
// vượt quá đầu ra nhìn thấy được (stop=length), phải thử lại bằng cách thu nhỏ khối còn một nửa thay vì thất bại toàn bộ -- cùng triết lý thu nhỏ batch với analyze.
func TestSegmentShrinksChunkOnTruncation(t *testing.T) {
	norm, units := segFixture() // 7 unit, một khối [0,7), mid=3
	left := boundariesJSON(boundaryFixture("L1", "", kindChapter, ""))
	right := boundariesJSON(boundaryFixture("L6", "", kindChapter, "Chương hai Mây dâng"))
	m := &mockModel{
		responses: []string{`{"boundaries":[]}`, left, right},
		stops:     []agentcore.StopReason{agentcore.StopReasonLength}, // lần gọi đầu bị cắt cụt, hai nửa khối bình thường
	}
	seg, err := Segment(context.Background(), m, "sys", norm, units, "", 0, 0, 4096, callProfile{}, nil, "")
	if err != nil {
		t.Fatalf("bị cắt cụt phải thu nhỏ khối và thử lại thay vì thất bại: %v", err)
	}
	if m.i != 3 {
		t.Fatalf("phải là 1 lần cắt cụt + 2 lần gọi nửa khối, nhận được %d", m.i)
	}
	if len(seg.Chapters) != 2 {
		t.Fatalf("kết quả thu nhỏ khối phải phủ đầy đủ (2 chương), nhận được %d", len(seg.Chapters))
	}
}

// TestPlanningBudget bảo vệ phần trừ chi phí cấu trúc của ngân sách quy hoạch cắt phân đoạn: phần nội dung owned chỉ là một phần của request.
func TestPlanningBudget(t *testing.T) {
	if got := planningBudget(0, "sys", "g"); got != 0 {
		t.Fatalf("không có ngân sách thì phải truyền nguyên, nhận được %d", got)
	}
	if got := planningBudget(1000, strings.Repeat("s", 100), strings.Repeat("g", 100)); got != 600 {
		t.Fatalf("(1000-200)*3/4 phải là 600, nhận được %d", got)
	}
	if got := planningBudget(1000, strings.Repeat("s", 2000), ""); got != 250 {
		t.Fatalf("prompt siêu dài phải kích hoạt sàn chunkBytes/4=250, nhận được %d", got)
	}
}

// TestBuildProjectionContextByteCap bảo vệ giới hạn byte trên của vùng ngữ cảnh: phân mảnh ảo của dòng siêu dài (một mảnh có thể đạt
// MaxUnitBytes) sẽ nuốt ngân sách đầu vào; ngữ cảnh chỉ là thông tin tham khảo, phải co lại theo giới hạn byte thay vì nhận toàn bộ.
func TestBuildProjectionContextByteCap(t *testing.T) {
	_, units := segFixture()
	if _, ids := buildProjection(units, [2]int{2, 3}, 2, 1, ""); len(ids) != 1 || !ids["L3"] {
		t.Fatalf("giới hạn byte phải cắt bỏ unit ngữ cảnh, chỉ còn owned: %v", ids)
	}
	if _, ids := buildProjection(units, [2]int{2, 3}, 2, 0, ""); len(ids) != 5 {
		t.Fatalf("khi không có giới hạn byte phải chứa 2 unit ngữ cảnh trước và sau (tổng 5), nhận được %v", ids)
	}
}

func TestCallStructuredTruncation(t *testing.T) {
	m := &mockModel{responses: []string{`{"boundaries":[]}`}, stop: agentcore.StopReasonLength}
	_, err := callStructured[boundaryBatch](context.Background(), m, segmentContract, "s", "p", 16, callProfile{}, nil)
	var trunc *errTruncated
	if err == nil || !asTruncated(err, &trunc) {
		t.Fatalf("cắt cụt do độ dài phải trả về *errTruncated, nhận được %v", err)
	}
}

func asTruncated(err error, target **errTruncated) bool {
	t, ok := err.(*errTruncated)
	if ok {
		*target = t
	}
	return ok
}
