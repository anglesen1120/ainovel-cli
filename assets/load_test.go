package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildWriterPrompt_ByteIdenticalToPreSplit là tiêu chí nghiệm thu của lớp văn phong ①:
// khi không có file ghi đè nào, kết quả ghép phải trùng byte với pipeline writer.md trước khi tách.
// golden là ảnh chụp nguyên gốc của writer.md trước khi tách (testdata/writer-golden.md).
func TestBuildWriterPrompt_ByteIdenticalToPreSplit(t *testing.T) {
	golden, err := os.ReadFile("testdata/writer-golden.md")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	protocol := mustRead(promptsFS, "prompts/writer.md")
	voice := mustRead(voiceFS, "voice.md")

	// Mức file: điền chỗ giữ = văn bản trước khi tách
	if got := strings.Replace(protocol, voicePlaceholder, strings.TrimSpace(voice), 1); got != string(golden) {
		t.Fatalf("điền chỗ giữ không khớp bản trước khi tách:\n--- độ dài golden=%d got=%d", len(golden), len(got))
	}

	// Mức pipeline: ghép mới == pipeline cũ (writer.md → simGuidance → style)
	const style = "## Một phong cách\n\n- Thử nghiệm"
	old := WithSimulationGuidance(string(golden), "writer") + "\n\n" + style
	got := BuildWriterPrompt(WithSimulationGuidance(protocol, "writer"), voice, style)
	if got != old {
		t.Fatal("pipeline ghép không tương đương với bản trước khi tách")
	}

	// Không thêm style thì cũng phải tương đương
	if BuildWriterPrompt(WithSimulationGuidance(protocol, "writer"), voice, "") != WithSimulationGuidance(string(golden), "writer") {
		t.Fatal("khi style rỗng, pipeline ghép không tương đương với bản trước khi tách")
	}
}

// TestLoad_NoOverrides khi không có ghi đè thì Voice/AntiAITone phải trùng byte với bản nội bộ.
func TestLoad_NoOverrides(t *testing.T) {
	b := Load("default", LoadOptions{})
	if b.Voice != mustRead(voiceFS, "voice.md") {
		t.Fatal("khi không có ghi đè, Voice phải trùng byte với bản nội bộ")
	}
	if b.References.AntiAITone != mustRead(referencesFS, "references/anti-ai-tone.md") {
		t.Fatal("khi không có ghi đè, AntiAITone phải trùng byte với bản nội bộ")
	}
	if _, ok := b.Styles["default"]; !ok {
		t.Fatal("bộ style nội bộ phải có default")
	}
}

func TestInterventionPromptsKeepScopeContract(t *testing.T) {
	prompts := loadPrompts()
	for _, phrase := range []string{"ngữ cảnh không đồng nghĩa với quyền sửa đổi", "phạm vi tối thiểu đủ dùng", "phạm vi phân tích không đồng nghĩa với phạm vi sửa đổi"} {
		if !strings.Contains(prompts.ArbiterIntervention, phrase) {
			t.Fatalf("prompt can thiệp của Arbiter thiếu hợp đồng phạm vi %q", phrase)
		}
	}
	for _, phrase := range []string{"can thiệp gốc của người dùng", "phạm vi phân tích không đồng nghĩa với phạm vi sửa đổi", "tập chương tối thiểu đủ dùng"} {
		if !strings.Contains(prompts.Editor, phrase) {
			t.Fatalf("prompt Editor thiếu hợp đồng phạm vi %q", phrase)
		}
	}
}

func TestStructuredArbiterPromptsContainOnlySemantics(t *testing.T) {
	prompts := loadPrompts()
	for name, prompt := range map[string]string{
		"plan_start": prompts.ArbiterPlanStart,
		"failure":    prompts.ArbiterFailure,
	} {
		for _, duplicate := range []string{"```json", "không dùng Markdown", "xuất ra một đối tượng JSON"} {
			if strings.Contains(prompt, duplicate) {
				t.Fatalf("prompt %s vẫn lặp lại định dạng đầu ra %q", name, duplicate)
			}
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestLoad_ThreeTierAppendAndReplace kiểm tra ưu tiên ba tầng và từng tài sản riêng lẻ (tiêu chí ②).
func TestLoad_ThreeTierAppendAndReplace(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	opts := LoadOptions{HomeStyleDir: home, BookStyleDir: book}

	// voice / anti-ai-tone: nghĩa nối thêm, global đứng trước, book đứng sau, có nhãn phân cách
	writeFile(t, filepath.Join(home, "voice.md"), "toàn cục: ít dùng thành ngữ")
	writeFile(t, filepath.Join(book, "voice.md"), "cuốn này: viết nhiều đối thoại")
	writeFile(t, filepath.Join(book, "anti-ai-tone.md"), "điểm cấm của cuốn này: không dùng phép liệt kê song song")

	// styles: cùng tên thì thay toàn bộ file + tên mới thì thêm; tên không hợp lệ thì bỏ qua
	writeFile(t, filepath.Join(home, "styles", "fantasy.md"), "bản fantasy được viết lại ở toàn cục")
	writeFile(t, filepath.Join(book, "styles", "xianxia.md"), "tiên hiệp tự chọn")
	writeFile(t, filepath.Join(book, "styles", "Bad Name!.md"), "không hợp lệ")

	// tham chiếu thể loại: cùng tên thì thay toàn bộ file, book > global
	writeFile(t, filepath.Join(home, "genres", "fantasy", "style-references.md"), "tham chiếu toàn cục")
	writeFile(t, filepath.Join(book, "genres", "fantasy", "style-references.md"), "tham chiếu của cuốn này")

	b := Load("fantasy", opts)

	builtinVoice := mustRead(voiceFS, "voice.md")
	if !strings.HasPrefix(b.Voice, builtinVoice) {
		t.Fatal("nghĩa nối thêm phải giữ nguyên văn bản nội bộ làm tiền tố")
	}
	giIdx := strings.Index(b.Voice, "## Ghi đè văn phong toàn cục")
	bkIdx := strings.Index(b.Voice, "## Ghi đè văn phong của sách này")
	if giIdx < 0 || bkIdx < 0 || giIdx > bkIdx {
		t.Fatalf("thứ tự đoạn ghi đè sai: global=%d book=%d", giIdx, bkIdx)
	}
	if !strings.Contains(b.Voice, "toàn cục: ít dùng thành ngữ") || !strings.Contains(b.Voice, "cuốn này: viết nhiều đối thoại") {
		t.Fatal("thiếu nội dung ghi đè")
	}
	if !strings.Contains(b.References.AntiAITone, "điểm cấm của cuốn này: không dùng phép liệt kê song song") {
		t.Fatal("thiếu phần thêm của anti-ai-tone ở cấp book")
	}

	if b.Styles["fantasy"] != "bản fantasy được viết lại ở toàn cục" {
		t.Fatal("styles cùng tên phải thay toàn bộ file")
	}
	if b.Styles["xianxia"] != "tiên hiệp tự chọn" {
		t.Fatal("style tự chọn mới phải dùng được ngay")
	}
	if _, ok := b.Styles["Bad Name!"]; ok {
		t.Fatal("style có tên không hợp lệ phải bị bỏ qua")
	}

	if b.References.StyleReference != "tham chiếu của cuốn này" {
		t.Fatalf("tham chiếu thể loại phải ưu tiên ghi đè ở cấp book, got %q", b.References.StyleReference)
	}
}

// TestLoad_BookOverridesHomeOnStyles kiểm tra styles ở cấp book ghi đè cùng tên ở global.
func TestLoad_BookOverridesHomeOnStyles(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	writeFile(t, filepath.Join(home, "styles", "romance.md"), "bản toàn cục")
	writeFile(t, filepath.Join(book, "styles", "romance.md"), "bản của cuốn này")
	b := Load("default", LoadOptions{HomeStyleDir: home, BookStyleDir: book})
	if b.Styles["romance"] != "bản của cuốn này" {
		t.Fatalf("book phải ghi đè global, got %q", b.Styles["romance"])
	}
}

// TestOverrideVoice_SharesAssemblyPath bảo đảm voice A/B của eval và production đi cùng đường ghép.
func TestOverrideVoice_SharesAssemblyPath(t *testing.T) {
	b := Load("default", LoadOptions{})
	b.OverrideVoice("## Văn phong thử nghiệm\n\n- Một câu")
	got := BuildWriterPrompt(b.Prompts.Writer, b.Voice, "")
	if !strings.Contains(got, "## Văn phong thử nghiệm") {
		t.Fatal("OverrideVoice chưa có tác dụng")
	}
	if strings.Contains(got, voicePlaceholder) {
		t.Fatal("chỗ giữ phải được tiêu thụ hết")
	}
	// Phần giao thức không bị ghi đè voice làm hỏng
	if !strings.Contains(got, "## Giao thức thực thi") {
		t.Fatal("mẫu giao thức không được phép bị phá bởi voice override")
	}
}
