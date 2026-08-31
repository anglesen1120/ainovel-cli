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
		domain.BookMetadata{Title: "Đốm sáng", Synopsis: "Cậu thiếu niên truy tìm sự thật ở ranh giới giữa ánh sáng và bóng tối."},
		[]int{1, 2},
		chapterTitleIndex{1: "Người về trong đêm mưa", 2: "Rạng đông"},
		nil,
		map[int]string{
			1: "# Chương 1 Người về trong đêm mưa\n\nCậu nhìn ra ngoài cửa sổ.\n\nĐoạn thứ hai.",
			2: "Cô đẩy cửa bước vào.",
		},
	)
	if err != nil {
		t.Fatalf("renderEPUB gặp lỗi: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("dữ liệu trống")
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
		t.Errorf("mục đầu tiên phải là mimetype, nhận được %q", first.Name)
	}
	if first.Method != zip.Store {
		t.Errorf("mimetype không được nén (Method=Store), nhận được %d", first.Method)
	}

	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("mở %s thất bại: %v", f.Name, err)
		}
		buf, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("đọc %s thất bại: %v", f.Name, err)
		}
		files[f.Name] = string(buf)
	}
	if files["mimetype"] != "application/epub+zip" {
		t.Errorf("nội dung mimetype = %q", files["mimetype"])
	}
	for _, want := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml", "OEBPS/style.css", "OEBPS/cover.xhtml", "OEBPS/chapter001.xhtml", "OEBPS/chapter002.xhtml"} {
		if _, ok := files[want]; !ok {
			t.Errorf("thiếu tệp bắt buộc %q", want)
		}
	}
	// container.xml trỏ tới OEBPS/content.opf.
	if !strings.Contains(files["META-INF/container.xml"], `full-path="OEBPS/content.opf"`) {
		t.Errorf("container.xml không trỏ tới content.opf")
	}
	// content.opf phải có đủ metadata, manifest và spine; thứ tự spine = thứ tự chương.
	opf := files["OEBPS/content.opf"]
	for _, want := range []string{"<metadata", "</metadata>", "<manifest>", "</manifest>", "<spine>", "</spine>", "urn:uuid:", `xml:lang="vi"`, "<dc:language>vi</dc:language>", "<dc:title>Đốm sáng</dc:title>", "<dc:description>Cậu thiếu niên truy tìm sự thật ở ranh giới giữa ánh sáng và bóng tối.</dc:description>", `href="chapter001.xhtml"`, `href="chapter002.xhtml"`, `idref="ch001"`, `idref="ch002"`} {
		if !strings.Contains(opf, want) {
			t.Errorf("OPF thiếu %q", want)
		}
	}
	if idx1, idx2 := strings.Index(opf, `idref="ch001"`), strings.Index(opf, `idref="ch002"`); idx1 < 0 || idx1 > idx2 {
		t.Errorf("thứ tự spine sai: ch001=%d ch002=%d", idx1, idx2)
	}

	// XHTML của chương gồm tiêu đề, đoạn văn và nội dung đã escape; tiêu đề markdown đầu tiên đã được loại bỏ.
	ch1 := files["OEBPS/chapter001.xhtml"]
	if !strings.Contains(ch1, "Chương 1 Người về trong đêm mưa") {
		t.Errorf("Chương 1 thiếu tiêu đề hiển thị")
	}
	if !strings.Contains(ch1, `xml:lang="vi"`) {
		t.Errorf("Chương 1 phải khai báo ngôn ngữ tiếng Việt: %s", ch1)
	}
	if !strings.Contains(ch1, "<p>Cậu nhìn ra ngoài cửa sổ.</p>") {
		t.Errorf("Chương 1 thiếu đoạn văn 1: %s", ch1)
	}
	if !strings.Contains(ch1, "<p>Đoạn thứ hai.</p>") {
		t.Errorf("Chương 1 thiếu đoạn văn 2: %s", ch1)
	}
	if strings.Contains(ch1, "# Chương 1") {
		t.Errorf("Chương 1 phải loại bỏ tiêu đề markdown: %s", ch1)
	}
	// nav.xhtml liệt kê tất cả chương.
	nav := files["OEBPS/nav.xhtml"]
	if !strings.Contains(nav, `epub:type="toc"`) {
		t.Errorf("nav thiếu epub:type=toc")
	}
	if !strings.Contains(nav, `href="chapter001.xhtml"`) || !strings.Contains(nav, `href="chapter002.xhtml"`) {
		t.Errorf("nav thiếu liên kết chương")
	}
	if !strings.Contains(nav, `xml:lang="vi"`) || !strings.Contains(nav, "Mục lục") || !strings.Contains(nav, ">Bìa<") {
		t.Errorf("nav phải dùng nhãn và ngôn ngữ tiếng Việt: %s", nav)
	}
	cover := files["OEBPS/cover.xhtml"]
	if !strings.Contains(cover, `xml:lang="vi"`) || !strings.Contains(cover, "<title>Bìa</title>") {
		t.Errorf("bìa phải dùng nhãn và ngôn ngữ tiếng Việt: %s", cover)
	}
}

func TestRenderEPUB_HTMLEscape(t *testing.T) {
	data, err := renderEPUB(domain.BookMetadata{Title: "A & B", Synopsis: "E < F & G"}, []int{1}, chapterTitleIndex{1: "C \"D\""}, nil, map[int]string{1: "Nội dung < & > văn bản."})
	if err != nil {
		t.Fatalf("renderEPUB gặp lỗi: %v", err)
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
	if !strings.Contains(files["OEBPS/chapter001.xhtml"], "Nội dung &lt; &amp; &gt; văn bản.") {
		t.Errorf("nội dung chương phải escape thực thể")
	}
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:title>A &amp; B</dc:title>") {
		t.Errorf("opf phải escape dấu & trong tiêu đề")
	}
	if !strings.Contains(files["OEBPS/content.opf"], "<dc:description>E &lt; F &amp; G</dc:description>") {
		t.Errorf("opf phải escape phần tóm tắt")
	}
}

// TestRenderEPUB_LayeredVolume xác minh dàn ý phân tầng chỉ chèn ngăn cách quyển ở đầu quyển, không bao giờ xuất hiện ngăn cách cung truyện.
func TestRenderEPUB_LayeredVolume(t *testing.T) {
	locs := map[int]chapterLocation{1: {VolumeIdx: 1, VolumeTitle: "Khởi nguyên", IsFirstOfVolume: true}, 2: {VolumeIdx: 1, VolumeTitle: "Khởi nguyên"}}
	data, err := renderEPUB(domain.BookMetadata{Title: "X", Synopsis: "Tóm tắt"}, []int{1, 2}, chapterTitleIndex{1: "A", 2: "B"}, locs, map[int]string{1: "Nội dung thứ nhất.", 2: "Nội dung thứ hai."})
	if err != nil {
		t.Fatalf("renderEPUB gặp lỗi: %v", err)
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
		t.Errorf("Chương 1 phải có đường phân cách quyển: %s", ch1)
	}
	if strings.Contains(ch1, `class="arc-divider"`) {
		t.Errorf("không được xuất hiện đường phân cách cung truyện: %s", ch1)
	}
	ch2 := files["OEBPS/chapter002.xhtml"]
	if strings.Contains(ch2, `class="volume-divider"`) {
		t.Errorf("Chương 2 không được có đường phân cách quyển")
	}
}

func TestRenderEPUB_NoCoverWhenNoTitle(t *testing.T) {
	data, err := renderEPUB(domain.BookMetadata{}, []int{1}, chapterTitleIndex{1: "Chương duy nhất"}, nil, map[int]string{1: "Nội dung."})
	if err != nil {
		t.Fatalf("renderEPUB gặp lỗi: %v", err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	for _, f := range zr.File {
		if f.Name == "OEBPS/cover.xhtml" {
			t.Errorf("không được có cover.xhtml khi tiêu đề trống")
		}
	}
	// content.opf không được tham chiếu tới bìa.
	for _, f := range zr.File {
		if f.Name != "OEBPS/content.opf" {
			continue
		}
		rc, _ := f.Open()
		buf, _ := io.ReadAll(rc)
		_ = rc.Close()
		if strings.Contains(string(buf), "cover.xhtml") || strings.Contains(string(buf), `idref="cover"`) {
			t.Errorf("OPF không được tham chiếu tới bìa khi không có bìa: %s", buf)
		}
	}
}

func TestSplitParagraphs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"a\n\nb", []string{"a", "b"}},
		{"a\n\n\n\nb", []string{"a", "b"}}, // Gộp nhiều dòng trống thành một dấu phân cách.
		{"a\nb", []string{"a b"}},          // Xuống dòng đơn trong đoạn được thay bằng khoảng trắng.
		{"  ", nil},                        // Chỉ có khoảng trắng trả về nil.
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
	// Cùng tên tác phẩm và các phạm vi xuất khác nhau phải trả về cùng ID — để trình đọc nhận diện là "phiên bản cập nhật".
	idFull := bookIdentifier("Đốm sáng")
	idAgain := bookIdentifier("Đốm sáng")
	if idFull != idAgain {
		t.Errorf("identifier không ổn định giữa các lần gọi: %s và %s", idFull, idAgain)
	}
	if id := bookIdentifier("Pha trăng"); id == idFull {
		t.Errorf("các tiêu đề khác nhau phải tạo identifier khác nhau")
	}
	if !strings.HasPrefix(idFull, "urn:uuid:") {
		t.Errorf("identifier phải có tiền tố urn:uuid:, nhận được %s", idFull)
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
