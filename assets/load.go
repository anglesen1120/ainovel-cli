package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/voocel/ainovel-cli/internal/tools"
)

//go:embed prompts/*.md
var promptsFS embed.FS

//go:embed references
var referencesFS embed.FS

//go:embed styles/*.md
var stylesFS embed.FS

//go:embed voice.md
var voiceFS embed.FS

// Prompts là tập hợp prompt nhúng.
type Prompts struct {
	ArchitectShort   string
	ArchitectLong    string
	Writer           string // Mẫu giao thức, chứa placeholder {{VOICE}}; bản cuối được lắp bằng BuildWriterPrompt
	Editor           string
	ImportSegment    string // Chia đoạn ngữ nghĩa: nhận diện ranh giới chương/quyển/phần phụ
	ImportAnalyze    string // Trích xuất sự kiện theo từng chương qua các batch liên tiếp
	ImportSynthesize string // Tổng hợp phân tầng và chia cung quyển (BookSynthesis toàn sách)
	ImportRange      string // Tóm tắt khoảng liên tiếp ở giai đoạn Map cho sách dài (RangeDigest)
	SimulationSource string
	SimulationMerge  string
	RevisionAnalyze  string

	// Prompt phân xử Arbiter (LLM-as-function, không bọc simulation guidance).
	ArbiterPlanStart    string
	ArbiterIntervention string
	ArbiterFailure      string
}

// Bundle là tập tài nguyên tĩnh cần cho runtime.
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
	Voice      string // Chuẩn viết (lớp văn phong), đã lắp theo ba tầng; xem docs/voice-layer.md
}

// LoadOptions khai báo nguồn ghi đè của lớp văn phong. Thư mục rỗng = bỏ qua tầng đó
// (eval truyền zero value để có baseline quyết định chỉ dùng phần nhúng, không bị ghi đè cục bộ của người dùng làm nhiễu).
//
// Ngữ nghĩa đường dẫn: BookStyleDir gắn với thư mục sách (outputDir), không phải cwd — văn phong đi theo sách;
// đổi thư mục vẫn nạp cùng một văn phong khi khôi phục cùng sách. Khác với lớp rules (rules gắn cấp dự án theo cwd).
type LoadOptions struct {
	BookStyleDir string // <outputDir>/style
	HomeStyleDir string // ~/.ainovel/style
}

// DefaultLoadOptions dựng nguồn ghi đè môi trường production từ thư mục sách.
func DefaultLoadOptions(outputDir string) LoadOptions {
	var opts LoadOptions
	if outputDir != "" {
		opts.BookStyleDir = filepath.Join(outputDir, "style")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		opts.HomeStyleDir = filepath.Join(home, ".ainovel", "style")
	}
	return opts
}

// Load trả về tập tài nguyên ứng với style chỉ định. Tài nguyên văn phong (voice / anti-ai-tone / styles /
// style-references theo thể loại) được lắp ba tầng theo opts: nhúng < toàn cục < sách này.
func Load(style string, opts LoadOptions) Bundle {
	return Bundle{
		References: loadReferences(style, opts),
		Prompts:    loadPrompts(),
		Styles:     loadStyles(opts),
		Voice:      resolveAppendable(mustRead(voiceFS, "voice.md"), "voice.md", opts),
	}
}

// voicePlaceholder là vị trí chèn nguyên trạng của đoạn văn phong trong mẫu giao thức writer.
const voicePlaceholder = "{{VOICE}}"

// BuildWriterPrompt là điểm lắp duy nhất cho system prompt writer, dùng chung cho production / eval / test,
// bảo đảm hai nhánh A/B đi cùng một đường (bài học tiền lệ: WithSimulationGuidance).
// writerPrompt là mẫu giao thức chứa placeholder (có thể đã kèm hậu tố simulation guidance; placeholder nằm
// trong tiền tố nên việc thay thế không bị ảnh hưởng); nếu style rỗng thì không nối thêm.
func BuildWriterPrompt(writerPrompt, voice, style string) string {
	out := strings.Replace(writerPrompt, voicePlaceholder, strings.TrimSpace(voice), 1)
	if style != "" {
		out += "\n\n" + style
	}
	return out
}

// OverrideVoice thay toàn bộ đoạn văn phong đã lắp bằng raw (dùng cho voice A/B trong eval).
// variant và baseline vẫn được lắp qua cùng đường BuildWriterPrompt.
func (b *Bundle) OverrideVoice(raw string) {
	b.Voice = raw
}

// resolveAppendable lắp ba tầng có ngữ nghĩa nối thêm: giữ phần nhúng, nối phần toàn cục/sách này thành đoạn có marker.
// Khi không có ghi đè, trả về nguyên văn phần nhúng (bất biến từng byte — một tiêu chuẩn nghiệm thu của lớp văn phong).
// "Phần sau ưu tiên" là chỉ dẫn ưu tiên cho LLM, không phải bảo đảm cơ học; ràng buộc cần bảo đảm cơ học đi qua lớp rules.
func resolveAppendable(builtin, name string, opts LoadOptions) string {
	out := builtin
	if s := readOverride(opts.HomeStyleDir, name); s != "" {
		out += "\n\n## Ghi đè văn phong toàn cục của người dùng (các yêu cầu sau ưu tiên hơn mặc định dự án)\n\n" + s
	}
	if s := readOverride(opts.BookStyleDir, name); s != "" {
		out += "\n\n## Ghi đè văn phong của sách này (các yêu cầu sau ưu tiên hơn toàn bộ phần trên)\n\n" + s
	}
	return out
}

// readOverride đọc một tệp trong thư mục ghi đè; thư mục rỗng, tệp không tồn tại hoặc chỉ có khoảng trắng đều trả về "".
func readOverride(dir, name string) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// styleNameRe kiểm tra tên tệp style tùy chỉnh của người dùng (không gồm phần mở rộng), từ chối ký tự đường dẫn.
var styleNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

func loadReferences(style string, opts LoadOptions) tools.References {
	if style == "" {
		style = "default"
	}
	refs := tools.References{
		ChapterGuide:      mustRead(referencesFS, "references/chapter-guide.md"),
		HookTechniques:    mustRead(referencesFS, "references/hook-techniques.md"),
		QualityChecklist:  mustRead(referencesFS, "references/quality-checklist.md"),
		OutlineTemplate:   mustRead(referencesFS, "references/outline-template.md"),
		CharacterTemplate: mustRead(referencesFS, "references/character-template.md"),
		ChapterTemplate:   mustRead(referencesFS, "references/chapter-template.md"),
		Consistency:       mustRead(referencesFS, "references/consistency.md"),
		ContentExpansion:  mustRead(referencesFS, "references/content-expansion.md"),
		DialogueWriting:   mustRead(referencesFS, "references/dialogue-writing.md"),
		LongformPlanning:  mustRead(referencesFS, "references/longform-planning.md"),
		Differentiation:   mustRead(referencesFS, "references/differentiation.md"),
		AntiAITone:        resolveAppendable(mustRead(referencesFS, "references/anti-ai-tone.md"), "anti-ai-tone.md", opts),
	}
	if style != "" && style != "default" {
		genreDir := "references/genres/" + style + "/"
		if data, err := referencesFS.ReadFile(genreDir + "style-references.md"); err == nil {
			refs.StyleReference = string(data)
		}
		if data, err := referencesFS.ReadFile(genreDir + "arc-templates.md"); err == nil {
			refs.ArcTemplates = string(data)
		}
		// Tham khảo phong cách theo thể loại: tệp cùng tên thay toàn bộ (sách này > toàn cục); style tùy chỉnh
		// không có tham khảo nhúng thì được phép chỉ đến từ ghi đè, không fallback default (tham chiếu sai còn tệ hơn không có).
		relPath := filepath.Join("genres", style, "style-references.md")
		for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
			if s := readOverride(dir, relPath); s != "" {
				refs.StyleReference = s
			}
		}
	}
	return refs
}

func loadPrompts() Prompts {
	return Prompts{
		ArchitectShort:   WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-short.md"), "architect"),
		ArchitectLong:    WithSimulationGuidance(mustRead(promptsFS, "prompts/architect-long.md"), "architect"),
		Writer:           WithSimulationGuidance(mustRead(promptsFS, "prompts/writer.md"), "writer"),
		Editor:           WithSimulationGuidance(mustRead(promptsFS, "prompts/editor.md"), "editor"),
		ImportSegment:    mustRead(promptsFS, "prompts/import-segment.md"),
		ImportAnalyze:    mustRead(promptsFS, "prompts/import-analyze.md"),
		ImportSynthesize: mustRead(promptsFS, "prompts/import-synthesize.md"),
		ImportRange:      mustRead(promptsFS, "prompts/import-range.md"),
		SimulationSource: mustRead(promptsFS, "prompts/simulation-source.md"),
		SimulationMerge:  mustRead(promptsFS, "prompts/simulation-merge.md"),
		RevisionAnalyze:  mustRead(promptsFS, "prompts/revision-analyze.md"),

		ArbiterPlanStart:    mustRead(promptsFS, "prompts/arbiter-plan-start.md"),
		ArbiterIntervention: mustRead(promptsFS, "prompts/arbiter-intervention.md"),
		ArbiterFailure:      mustRead(promptsFS, "prompts/arbiter-failure.md"),
	}
}

// WithSimulationGuidance nối thêm chỉ dẫn hồ sơ mô phỏng vào prompt lõi. Export để các bối cảnh ngoài như eval
// tái dùng khi ghi đè variant, bảo đảm prompt sau ghi đè tương đương baseline do Load tạo ra (cùng một đường bọc).
func WithSimulationGuidance(prompt, role string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidance, "{{role}}", role)
}

// OverridePrompt dùng raw ghi đè prompt vai trò tương ứng với tệp prompt chỉ định trong bundle, rồi đi qua đúng
// cùng lớp bọc WithSimulationGuidance như Load — khi eval chạy A/B chỉ cần gọi hàm này, không phải sao chép logic bọc,
// nếu không baseline có hậu tố hồ sơ mô phỏng còn variant thì không, làm A/B không tương đương. file là tên tệp prompt.
// Lưu ý: khi ghi đè writer.md, raw phải tự chứa placeholder {{VOICE}} (ngữ nghĩa mẫu giao thức); nếu chỉ muốn A/B văn phong
// thì dùng OverrideVoice.
func (b *Bundle) OverridePrompt(file, raw string) error {
	role, ok := promptRole[file]
	if !ok {
		return fmt.Errorf("không hỗ trợ ghi đè tệp prompt: %s (chỉ prompt lõi mới được ghi đè)", file)
	}
	wrapped := WithSimulationGuidance(raw, role)
	switch file {
	case "architect-short.md":
		b.Prompts.ArchitectShort = wrapped
	case "architect-long.md":
		b.Prompts.ArchitectLong = wrapped
	case "writer.md":
		b.Prompts.Writer = wrapped
	case "editor.md":
		b.Prompts.Editor = wrapped
	}
	return nil
}

// promptRole ánh xạ tên tệp prompt lõi sang placeholder vai trò trong simulation guidance.
var promptRole = map[string]string{
	"architect-short.md": "architect",
	"architect-long.md":  "architect",
	"writer.md":          "writer",
	"editor.md":          "editor",
}

const simulationGuidance = `## Hồ sơ mô phỏng

Khi planning_memory hoặc working_memory trong novel_context có simulation_profile, phải xem nó là ràng buộc định hướng mô phỏng của tác phẩm hiện tại. {{role}} nên đọc các trường style, lexicon, plot_design, hook_design, pacing_density, reader_engagement và role_guidance trong đó.

Nguyên tắc sử dụng: học cấu trúc, nhịp độ, móc câu chuyện, cách hé lộ thông tin và thủ pháp thu hút độc giả; không sao chép câu văn gốc, nhân vật, địa danh, thiết lập riêng hoặc mô-típ cố định. Nếu simulation_profile xung đột với yêu cầu rõ ràng của người dùng, ưu tiên yêu cầu của người dùng.`

// loadStyles liệt kê preset style nhúng, rồi chồng ghi đè trong styles/*.md theo thứ tự toàn cục → sách này
// (tệp cùng tên thay toàn bộ, tên tệp mới là style mới; style là giọng tổng thể nên không merge).
func loadStyles(opts LoadOptions) map[string]string {
	styles := make(map[string]string)
	entries, err := stylesFS.ReadDir("styles")
	if err != nil {
		return styles
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		data, err := stylesFS.ReadFile("styles/" + e.Name())
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
	for _, dir := range []string{opts.HomeStyleDir, opts.BookStyleDir} {
		overlayStyles(styles, dir)
	}
	return styles
}

// overlayStyles chồng <dir>/styles/*.md vào tập styles; bỏ qua và cảnh báo tên tệp không hợp lệ.
func overlayStyles(styles map[string]string, dir string) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(dir, "styles"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if !styleNameRe.MatchString(name) {
			slog.Warn("bỏ qua tên tệp phong cách không hợp lệ", "module", "assets", "dir", dir, "file", e.Name())
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "styles", e.Name()))
		if err != nil {
			continue
		}
		styles[name] = string(data)
	}
}

func mustRead(fs embed.FS, path string) string {
	data, err := fs.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("embed read %s: %v", path, err))
	}
	return string(data)
}
