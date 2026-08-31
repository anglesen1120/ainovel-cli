package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildWriterPrompt_ByteIdenticalToPreSplit là tiêu chuẩn nghiệm thu tầng văn phong ①:
// khi không có tệp ghi đè, sản phẩm lắp ráp phải giống từng byte với pipeline writer.md trước khi tách.
// golden là ảnh chụp gốc của writer.md trước khi tách (testdata/writer-golden.md).
func TestBuildWriterPrompt_ByteIdenticalToPreSplit(t *testing.T) {
	golden, err := os.ReadFile("testdata/writer-golden.md")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	protocol := mustRead(promptsFS, "prompts/writer.md")
	voice := mustRead(voiceFS, "voice.md")

	// Cấp tệp: điền placeholder == nguyên văn trước khi tách
	if got := strings.Replace(protocol, voicePlaceholder, strings.TrimSpace(voice), 1); got != string(golden) {
		t.Fatalf("Điền placeholder không khớp bản trước khi tách:\n--- độ dài golden=%d got=%d", len(golden), len(got))
	}

	// Cấp pipeline: lắp ráp mới == pipeline cũ (writer.md → simGuidance → style)
	const style = "## Phong cách thử nghiệm\n\n- Kiểm thử"
	old := WithSimulationGuidance(string(golden), "writer") + "\n\n" + style
	got := BuildWriterPrompt(WithSimulationGuidance(protocol, "writer"), voice, style)
	if got != old {
		t.Fatal("Pipeline lắp ráp không tương đương bản trước khi tách")
	}

	// Khi không nối thêm style cũng tương đương
	if BuildWriterPrompt(WithSimulationGuidance(protocol, "writer"), voice, "") != WithSimulationGuidance(string(golden), "writer") {
		t.Fatal("Pipeline lắp ráp khi không có style không tương đương bản trước khi tách")
	}
}

// TestLoad_NoOverrides xác nhận Voice/AntiAITone khớp từng byte với bản nhúng khi không có ghi đè.
func TestLoad_NoOverrides(t *testing.T) {
	b := Load("default", LoadOptions{})
	if b.Voice != mustRead(voiceFS, "voice.md") {
		t.Fatal("Khi không ghi đè, Voice phải khớp từng byte với bản nhúng")
	}
	if b.References.AntiAITone != mustRead(referencesFS, "references/anti-ai-tone.md") {
		t.Fatal("Khi không ghi đè, AntiAITone phải khớp từng byte với bản nhúng")
	}
	if _, ok := b.Styles["default"]; !ok {
		t.Fatal("Tập phong cách nhúng phải có default")
	}
}

func TestInterventionPromptsKeepScopeContract(t *testing.T) {
	prompts := loadPrompts()
	for _, phrase := range []string{"ngữ cảnh không đồng nghĩa với ủy quyền sửa đổi", "phạm vi tối thiểu đủ", "phạm vi phân tích không đồng nghĩa với phạm vi sửa đổi"} {
		if !strings.Contains(prompts.ArbiterIntervention, phrase) {
			t.Fatalf("Prompt can thiệp Arbiter thiếu hợp đồng phạm vi %q", phrase)
		}
	}
	for _, phrase := range []string{"can thiệp nguyên thủy của người dùng", "phạm vi phân tích không đồng nghĩa với phạm vi chỉnh sửa", "tập hợp chương tối thiểu đủ dùng"} {
		if !strings.Contains(prompts.Editor, phrase) {
			t.Fatalf("Prompt Editor thiếu hợp đồng phạm vi %q", phrase)
		}
	}
}

func TestStructuredArbiterPromptsContainOnlySemantics(t *testing.T) {
	prompts := loadPrompts()
	for name, prompt := range map[string]string{
		"plan_start": prompts.ArbiterPlanStart,
		"failure":    prompts.ArbiterFailure,
	} {
		for _, duplicate := range []string{"```json", "Đừng dùng Markdown", "Xuất một đối tượng JSON"} {
			if strings.Contains(prompt, duplicate) {
				t.Fatalf("%s prompt vẫn bảo trì lặp định dạng đầu ra %q", name, duplicate)
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

// TestLoad_ThreeTierAppendAndReplace kiểm tra ưu tiên ghi đè ba tầng và ngữ nghĩa từng asset (tiêu chuẩn nghiệm thu ②).
func TestLoad_ThreeTierAppendAndReplace(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	opts := LoadOptions{HomeStyleDir: home, BookStyleDir: book}

	// voice / anti-ai-tone: nối thêm ngữ nghĩa, toàn cục trước, sách này sau, có marker ranh giới
	writeFile(t, filepath.Join(home, "voice.md"), "Toàn cục: ít dùng thành ngữ")
	writeFile(t, filepath.Join(book, "voice.md"), "Sách này: tăng đối thoại")
	writeFile(t, filepath.Join(book, "anti-ai-tone.md"), "Tiêu chí sách này: cấm liệt kê song hành")

	// styles: cùng tên thì thay toàn bộ tệp + tên mới thì thêm; tên không hợp lệ bị bỏ qua
	writeFile(t, filepath.Join(home, "styles", "fantasy.md"), "Fantasy viết lại toàn cục")
	writeFile(t, filepath.Join(book, "styles", "xianxia.md"), "Xianxia tùy chỉnh")
	writeFile(t, filepath.Join(book, "styles", "Bad Name!.md"), "Không hợp lệ")

	// Tham khảo đề tài: cùng tên thì thay toàn bộ tệp, sách này > toàn cục
	writeFile(t, filepath.Join(home, "genres", "fantasy", "style-references.md"), "Tham khảo toàn cục")
	writeFile(t, filepath.Join(book, "genres", "fantasy", "style-references.md"), "Tham khảo sách này")

	b := Load("fantasy", opts)

	builtinVoice := mustRead(voiceFS, "voice.md")
	if !strings.HasPrefix(b.Voice, builtinVoice) {
		t.Fatal("Ngữ nghĩa nối thêm phải giữ nguyên văn nhúng làm tiền tố")
	}
	giIdx := strings.Index(b.Voice, "## Ghi đè văn phong toàn cục của người dùng (các yêu cầu sau ưu tiên hơn mặc định dự án)")
	bkIdx := strings.Index(b.Voice, "## Ghi đè văn phong của sách này (các yêu cầu sau ưu tiên hơn toàn bộ phần trên)")
	if giIdx < 0 || bkIdx < 0 || giIdx > bkIdx {
		t.Fatalf("Thứ tự đoạn nối thêm sai: global=%d book=%d", giIdx, bkIdx)
	}
	if !strings.Contains(b.Voice, "Toàn cục: ít dùng thành ngữ") || !strings.Contains(b.Voice, "Sách này: tăng đối thoại") {
		t.Fatal("Thiếu nội dung ghi đè")
	}
	if !strings.Contains(b.References.AntiAITone, "Tiêu chí sách này: cấm liệt kê song hành") {
		t.Fatal("Thiếu đoạn nối thêm anti-ai-tone của sách này")
	}

	if b.Styles["fantasy"] != "Fantasy viết lại toàn cục" {
		t.Fatal("style cùng tên phải thay toàn bộ tệp")
	}
	if b.Styles["xianxia"] != "Xianxia tùy chỉnh" {
		t.Fatal("Phong cách tùy chỉnh mới phải dùng được ngay")
	}
	if _, ok := b.Styles["Bad Name!"]; ok {
		t.Fatal("Tên phong cách không hợp lệ phải bị bỏ qua")
	}

	if b.References.StyleReference != "Tham khảo sách này" {
		t.Fatalf("Tham khảo đề tài phải ưu tiên ghi đè của sách này, got %q", b.References.StyleReference)
	}
}

// TestLoad_BookOverridesHomeOnStyles xác nhận styles của sách này ghi đè bản toàn cục cùng tên.
func TestLoad_BookOverridesHomeOnStyles(t *testing.T) {
	home := t.TempDir()
	book := t.TempDir()
	writeFile(t, filepath.Join(home, "styles", "romance.md"), "Bản toàn cục")
	writeFile(t, filepath.Join(book, "styles", "romance.md"), "Bản sách này")
	b := Load("default", LoadOptions{HomeStyleDir: home, BookStyleDir: book})
	if b.Styles["romance"] != "Bản sách này" {
		t.Fatalf("Sách này phải ghi đè toàn cục, got %q", b.Styles["romance"])
	}
}

// TestOverrideVoice_SharesAssemblyPath xác nhận voice A/B trong eval dùng cùng đường lắp ráp với production (tiêu chuẩn nghiệm thu ④).
func TestOverrideVoice_SharesAssemblyPath(t *testing.T) {
	b := Load("default", LoadOptions{})
	b.OverrideVoice("## Văn phong thử nghiệm\n\n- Một câu")
	got := BuildWriterPrompt(b.Prompts.Writer, b.Voice, "")
	if !strings.Contains(got, "## Văn phong thử nghiệm") {
		t.Fatal("OverrideVoice chưa có hiệu lực")
	}
	if strings.Contains(got, voicePlaceholder) {
		t.Fatal("Placeholder phải được tiêu thụ")
	}
	// Phần giao thức không bị ghi đè voice ảnh hưởng
	if !strings.Contains(got, "## Giao thức thực thi") {
		t.Fatal("Template giao thức không được bị ghi đè voice phá vỡ")
	}
}
