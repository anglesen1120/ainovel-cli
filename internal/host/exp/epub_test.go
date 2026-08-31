package exp

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
)

func TestRenderEPUB_StructuralInvariants(t *testing.T) {
	data, err := renderEPUB(
		domain.BookMetadata{Title: "Đốm sáng", Synopsis: "Thiếu niên truy tìm sự thật nơi ranh giới sáng tối."},
		[]int{1, 2},
		chapterTitleIndex{1: "Người về trong mưa đêm", 2: "Rạng đông"},
		nil,
		map[int]string{
			1: "# Chương 1 Người về trong mưa đêm\n\nAnh nhìn ra ngoài cửa sổ.\n\nĐoạn thứ hai.",
			2: "Cô đẩy cửa bước vào.",
		},
	)
	if err != nil {
		t.Fatalf("renderEPUB: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("dữ liệu rỗng")
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("không phải zip hợp lệ: %v", err)
	}

	if len(zr.File) == 0 {
		t.Fatal("zip không có tệp")
	}
	first := zr.File[0]
	if first.Name != "mimetype" {
		t.Errorf("mục đầu tiên phải là mimetype, nhận %q", first.Name)
	}
	if first.Method != zip.Store {
		t.Errorf("mimetype phải không nén (Method=Store), nhận %d", first.Method)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("mở %s: %v", f.Name, err)
		}
		buf, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("đọc %s: %v", f.Name, err)
		}
		files[f.Name] = string(buf)
	}

	if files["mimetype"] != "application/epub+zip" {
		t.Errorf("nội dung mimetype = %q", files["mimetype"])
	}

	for _, want := range []string{
		"META-INF/container.xml",
		"OEBPS/content.opf",
		"OEBPS/nav.xhtml",
		"OEBPS/style.css",
		"OEBPS/cover.xhtml",
		"OEBPS/chapter001.xhtml",
		"OEBPS/chapter002.xhtml",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("thiếu tệp bắt buộc %q", want)
		}
	}

	// container.xml trỏ tới OEBPS/content.opf.
	if !strings.Contains(files["META-INF/container.xml"], `full-path="OEBPS/content.opf"`) {
			t.Errorf("container.xml không trỏ tới content.opf")
	}

	// content.opf phải có ba khối metadata + manifest + spine; thứ tự spine bằng thứ tự chương.
	opf := files["OEBPS/content.opf"]
	for _, want := range []string{
		"<metadata", "</metadata>",
		"<manifest>", "</manifest>",
		"<spine>", "</spine>",
		"urn:uuid:",
		"<dc:title>Đốm sáng</dc:title>",
		"<dc:language>vi</dc:language>",
		`xml:lang="vi"`,
		"<dc:description>Thiếu niên truy tìm sự thật nơi ranh giới sáng tối.</dc:description>",
		`href="chapter001.xhtml"`,
		`href="chapter002.xhtml"`,
		`idref="ch001"`,
		`idref="ch002"`,
	} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF thiếu %q", want)
		}
	}
	if idx1, idx2 := strings.Index(opf, `idref="ch001"`), strings.Index(opf, `idref="ch002"`); idx1 < 0 || idx1 > idx2 {
			t.Errorf("thứ tự spine sai: ch001=%d ch002=%d", idx1, idx2)
	}

	// XHTML chương có tiêu đề, đoạn văn và escaping; tiêu đề markdown ở dòng đầu đã được bóc.
	ch1 := files["OEBPS/chapter001.xhtml"]
	if !strings.Contains(ch1, "Chương 1 Người về trong mưa đêm") {
		t.Errorf("chương 1 thiếu tiêu đề hiển thị")
	}
	if !strings.Contains(ch1, "<p>Anh nhìn ra ngoài cửa sổ.</p>") {
		t.Errorf("chương 1 thiếu đoạn văn 1: %s", ch1)
	}
	if !strings.Contains(ch1, "<p>Đoạn thứ hai.</p>") {
		t.Errorf("chương 1 thiếu đoạn văn 2: %s", ch1)
	}
	if strings.Contains(ch1, "# Chương 1") {
		t.Errorf("chương 1 phải loại bỏ tiêu đề markdown: %s", ch1)
	}

	// nav.xhtml liệt kê mọi chương.
	nav := files["OEBPS/nav.xhtml"]
	if !strings.Contains(nav, "Mục lục") || !strings.Contains(nav, "Bìa") {
		t.Errorf("nav thiếu nhãn tiếng Việt")
	}
	if !strings.Contains(nav, `epub:type="toc"`) {
		t.Errorf("nav thiếu epub:type=toc")
	}
	if !strings.Contains(nav, `href="chapter001.xhtml"`) || !strings.Contains(nav, `href="chapter002.xhtml"`) {
		t.Errorf("nav thiếu liên kết chương")
	}
}

func TestRenderEPUB_HTMLEscape(t *testing.T) {
	data, err := renderEPUB(
		domain.BookMetadata{Title: "A & B", Synopsis: "E < F & G"}, // Ký tự đặc biệt phải được escape.
		[]int{1},
		chapterTitleIndex{1: "C \"D\""},
		nil,
		map[int]string{1: "Nội dung < & > cần escape."},
	)
	if err != nil {
		t.Fatalf("renderEPUB: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		buf, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(buf)
	}

	if !strings.Contains(files["OEBPS/cover.xhtml"], "A &amp; B") {
		t.Errorf("bìa phải escape &: %s", files["OEBPS/cover.xhtml"])
	}
	if !strings.Contains(files["OEBPS/chapter001.xhtml"], "Nội dung &lt; &amp; &gt; cần escape.") {
		t.Errorf("thân chương phải escape các thực thể")
	}
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>A &amp; B</dc:title>") {
		t.Errorf("opf phải escape & trong tiêu đề")
	}
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:description>E &lt; F &amp; G</dc:description>") {
		t.Errorf("opf phải escape & trong tóm tắt")
	}
}

// TestRenderEPUB_LayeredVolume xác nhận dàn ý phân tầng chỉ chèn vạch phân quyển ở đầu quyển, không bao giờ chèn vạch phân hồi.
func TestRenderEPUB_LayeredVolume(t *testing.T) {
	locs := map[int]chapterLocation{
		1: {VolumeIdx: 1, VolumeTitle: "Khởi nguyên", IsFirstOfVolume: true},
		2: {VolumeIdx: 1, VolumeTitle: "Khởi nguyên"},
	}
	data, err := renderEPUB(
		domain.BookMetadata{Title: "X", Synopsis: "Giới thiệu"},
		[]int{1, 2},
		chapterTitleIndex{1: "A", 2: "B"},
		locs,
		map[int]string{1: "Nội dung một.", 2: "Nội dung hai."},
	)
	if err != nil {
		t.Fatalf("renderEPUB: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		buf, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(buf)
	}

	ch1 := files["OEBPS/chapter001.xhtml"]
	if !strings.Contains(ch1, `class="volume-divider"`) || !strings.Contains(ch1, "Quyển 1 Khởi nguyên") {
		t.Errorf("ch1 phải có vạch phân quyển: %s", ch1)
	}
	if strings.Contains(ch1, `class="arc-divider"`) {
		t.Errorf("không được xuất hiện vạch phân hồi: %s", ch1)
	}

	ch2 := files["OEBPS/chapter002.xhtml"]
	if strings.Contains(ch2, `class="volume-divider"`) {
		t.Errorf("ch2 không được có vạch phân quyển (cùng quyển)")
	}
}

func TestRenderEPUB_NoCoverWhenNoTitle(t *testing.T) {
	data, err := renderEPUB(
		domain.BookMetadata{}, []int{1},
		chapterTitleIndex{1: "Chương duy nhất"},
		nil,
		map[int]string{1: "Nội dung."},
	)
	if err != nil {
		t.Fatalf("renderEPUB: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	for _, f := range zr.File {
		if f.Name == "OEBPS/cover.xhtml" {
			t.Errorf("cover.xhtml không được tồn tại khi tiêu đề rỗng")
		}
	}
	// content.opf không được tham chiếu cover.
	for _, f := range zr.File {
		if f.Name != "OEBPS/content.opf" {
			continue
		}
		rc, _ := f.Open()
		buf, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.Contains(string(buf), "cover.xhtml") || strings.Contains(string(buf), `idref="cover"`) {
			t.Errorf("OPF không được tham chiếu cover khi không có bìa: %s", buf)
		}
	}
}

func TestSplitParagraphs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a\n\nb", []string{"a", "b"}},
		{"a\n\n\n\nb", []string{"a", "b"}}, // Nhiều dòng trống gộp thành một điểm tách.
		{"a\nb", []string{"a b"}},          // Một dòng mới trong đoạn đổi thành dấu cách.
		{"  ", nil},                        // Toàn khoảng trắng trả về nil.
		{"a\r\n\r\nb", []string{"a", "b"}}, // Tương thích CRLF.
	}
	for _, c := range cases {
		got := splitParagraphs(c.in)
		if !equalStrings(got, c.want) {
			t.Errorf("splitParagraphs(%q) = %v, mong đợi %v", c.in, got, c.want)
		}
	}
}

func TestBookIdentifier_StableAcrossChapterRanges(t *testing.T) {
	// Cùng tên tác phẩm và khác phạm vi xuất phải trả về cùng ID để trình đọc nhận ra đây là "bản cập nhật".
	idFull := bookIdentifier("Đốm sáng")
	idAgain := bookIdentifier("Đốm sáng")
	if idFull != idAgain {
		t.Errorf("identifier không ổn định giữa các lần gọi: %s và %s", idFull, idAgain)
	}
	if id := bookIdentifier("Pha trăng"); id == idFull {
		t.Errorf("các tiêu đề khác nhau phải tạo identifier khác nhau")
	}
	if !strings.HasPrefix(idFull, "urn:uuid:") {
		t.Errorf("identifier phải có tiền tố urn:uuid:, nhận %s", idFull)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
