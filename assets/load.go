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

// Prompts biểu thị tập hợp prompt được nhúng sẵn.
type Prompts struct {
	ArchitectShort   string
	ArchitectLong    string
	Writer           string // Mẫu giao thức, chứa chỗ giữ {{VOICE}}; bản cuối do BuildWriterPrompt ghép.
	Editor           string
	ImportSegment    string // Tách ngữ nghĩa: nhận diện ranh giới chương / quyển / phần phụ
	ImportAnalyze    string // Trích xuất dữ kiện theo từng chương từ các lô liên tiếp
	ImportSynthesize string // Tổng hợp nhiều lớp và chia phạm vi quyển / arc (BookSynthesis toàn sách)
	ImportRange      string // Tóm tắt đoạn liên tiếp ở giai đoạn Map cho sách dài (RangeDigest)
	SimulationSource string
	SimulationMerge  string
	RevisionAnalyze  string

	// Prompt điều phối của Arbiter (LLM-as-function, không bọc simulation guidance).
	ArbiterPlanStart    string
	ArbiterIntervention string
	ArbiterFailure      string
}

// Bundle biểu thị tập hợp tài nguyên tĩnh cần cho chạy.
type Bundle struct {
	References tools.References
	Prompts    Prompts
	Styles     map[string]string
	Voice      string // Chuẩn viết (lớp văn phong), đã được ghép theo ba tầng; xem docs/voice-layer.md
}

// LoadOptions khai báo nguồn ghi đè cho lớp văn phong. Thư mục trống = bỏ qua tầng đó
// (eval truyền giá trị rỗng để lấy baseline chỉ từ nội bộ, không bị ghi đè cục bộ của máy).
//
// Ý nghĩa đường dẫn: BookStyleDir gắn với thư mục sách (outputDir) thay vì cwd — văn phong đi theo
// sách, đổi thư mục vẫn nạp lại cùng một bộ văn phong cho cùng cuốn sách. Khác với lớp rules
// (gắn theo cwd cấp dự án).
type LoadOptions struct {
	BookStyleDir string // <outputDir>/style
	HomeStyleDir string // ~/.ainovel/style
}

// DefaultLoadOptions tạo nguồn ghi đè cho môi trường chạy thật dựa trên thư mục sách.
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

// Load trả về tập tài nguyên tương ứng với style đã chọn. Các tài sản văn phong (voice /
// anti-ai-tone / styles / style-references của thể loại) được ghép theo ba tầng:
// nội bộ < toàn cục < theo sách.
func Load(style string, opts LoadOptions) Bundle {
	return Bundle{
		References: loadReferences(style, opts),
		Prompts:    loadPrompts(),
		Styles:     loadStyles(opts),
		Voice:      resolveAppendable(mustRead(voiceFS, "voice.md"), "voice.md", opts),
	}
}

// voicePlaceholder là điểm chèn nguyên vị cho phần văn phong trong mẫu writer.
const voicePlaceholder = "{{VOICE}}"

// BuildWriterPrompt là điểm ghép duy nhất của system prompt writer, dùng chung cho production /
// eval / test để bảo đảm hai nhánh A/B đi cùng một đường (kinh nghiệm trước đó xem
// WithSimulationGuidance).
// writerPrompt là mẫu giao thức có chỗ giữ (có thể đã kèm hậu tố simulation guidance,
// vì chỗ giữ nằm trong tiền tố nên phần thay thế không bị ảnh hưởng); style rỗng thì không nối thêm.
func BuildWriterPrompt(writerPrompt, voice, style string) string {
	out := strings.Replace(writerPrompt, voicePlaceholder, strings.TrimSpace(voice), 1)
	if style != "" {
		out += "\n\n" + style
	}
	return out
}

// OverrideVoice dùng raw để thay toàn bộ phần văn phong đã ghép (dùng cho voice A/B trong eval).
// variant và baseline vẫn được ghép qua cùng đường BuildWriterPrompt.
func (b *Bundle) OverrideVoice(raw string) {
	b.Voice = raw
}

// resolveAppendable ghép theo nghĩa nối ba tầng: giữ nguyên nội bộ, rồi nối global / book theo
// các đoạn đánh dấu.
// Không có ghi đè thì trả lại nguyên văn nội bộ (không đổi từng byte — một tiêu chuẩn của lớp văn phong).
// "Tầng sau ưu tiên" là chỉ dẫn ưu tiên cho LLM chứ không phải bảo đảm cơ học; những ràng buộc cần
// bảo đảm cơ học phải đi qua lớp rules.
func resolveAppendable(builtin, name string, opts LoadOptions) string {
	out := builtin
	if s := readOverride(opts.HomeStyleDir, name); s != "" {
		out += "\n\n## Ghi đè văn phong toàn cục (ưu tiên hơn mặc định dự án)\n\n" + s
	}
	if s := readOverride(opts.BookStyleDir, name); s != "" {
		out += "\n\n## Ghi đè văn phong của sách này (ưu tiên hơn tất cả ở trên)\n\n" + s
	}
	return out
}

// readOverride đọc một file đơn lẻ trong thư mục ghi đè; thư mục trống, file không tồn tại hoặc
// chỉ là khoảng trắng đều trả về "".
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

// styleNameRe kiểm tra tên file style tự chọn (không có phần mở rộng), từ chối ký tự đường dẫn.
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
		// Tài liệu tham chiếu thể loại: cùng tên thì file đầy đủ ghi đè (book > global);
		// với style tự chọn mà không có tham chiếu nội bộ thì cho phép chỉ dùng nội dung ghi đè,
		// không rơi về default (tham chiếu sai còn tệ hơn không có).
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

// WithSimulationGuidance thêm chỉ dẫn về ảnh mô phỏng vào prompt cốt lõi. Hàm này được xuất để
// eval và các ngữ cảnh bên ngoài tái dùng khi cần ghi đè variant, bảo đảm prompt đã ghi đè vẫn
// trùng đường ghép với baseline do Load tạo ra (cùng một đường bao).
func WithSimulationGuidance(prompt, role string) string {
	return prompt + "\n\n" + strings.ReplaceAll(simulationGuidance, "{{role}}", role)
}

// OverridePrompt ghi đè prompt tương ứng với file đã chỉ định trong bundle và bọc nó bằng
// WithSimulationGuidance giống hệt Load — dùng cho A/B của eval, không cần chép lại logic bọc.
// Nếu không, baseline có hậu tố ảnh mô phỏng còn variant thì không, làm A/B không tương đương.
// file là tên file prompt.
// Lưu ý: khi ghi đè writer.md thì raw phải tự chứa chỗ giữ {{VOICE}} (ngữ nghĩa của mẫu giao thức);
// chỉ muốn đổi A/B văn phong thì dùng OverrideVoice.
func (b *Bundle) OverridePrompt(file, raw string) error {
	role, ok := promptRole[file]
	if !ok {
		return fmt.Errorf("không hỗ trợ ghi đè prompt: %s (chỉ các prompt cốt lõi mới được phép)", file)
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

// promptRole ánh xạ tên file prompt cốt lõi sang chỗ giữ vai trò cho simulation guidance.
var promptRole = map[string]string{
	"architect-short.md": "architect",
	"architect-long.md":  "architect",
	"writer.md":          "writer",
	"editor.md":          "editor",
}

const simulationGuidance = `## Ảnh mô phỏng

Khi novel_context có simulation_profile trong planning_memory hoặc working_memory, phải xem đó là ràng buộc hướng mô phỏng hiện tại. {{role}} cần đọc style, lexicon, plot_design, hook_design, pacing_density, reader_engagement và role_guidance.

Nguyên tắc sử dụng: học cách tổ chức, nhịp điệu, móc câu, nhả thông tin và cách giữ người đọc; không sao chép câu chữ gốc, nhân vật, địa danh, thiết lập riêng hay các đoạn xử lý cố định. Nếu simulation_profile xung đột với yêu cầu rõ ràng của người dùng, ưu tiên yêu cầu của người dùng.`

// loadStyles liệt kê các style có sẵn rồi chồng ghi đè từ Global → Book trong các file styles/*.md
// (cùng tên thì thay toàn bộ file, tên mới thì thêm style mới; style là một tiếng nói tổng thể,
// không gộp trộn nội dung).
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

// overlayStyles chồng <dir>/styles/*.md vào tập styles; file có tên không hợp lệ sẽ bị bỏ qua
// và ghi log cảnh báo.
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
			slog.Warn("bỏ qua tên file style không hợp lệ", "module", "assets", "dir", dir, "file", e.Name())
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
