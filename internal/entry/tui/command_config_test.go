package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
)

func hubFieldIDs(fields []hubField) []string {
	ids := make([]string, len(fields))
	for i, f := range fields {
		ids[i] = f.id
	}
	return ids
}

func hubFieldIndex(fields []hubField, id string) int {
	for i, field := range fields {
		if field.id == id {
			return i
		}
	}
	return -1
}

// Chọn Provider trung bình đã có sẵn phải vào hub Chi tiết (xem thông tin trước, rồi chỉnh từng mục), không nhảy thẳng vào “đổi Giao thức”.
func TestSelectingProviderOpensHub(t *testing.T) {
	st := &modelConfigState{editModelIdx: -1}
	st.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "openrouter", BaseURL: "u", HasAPIKey: true,
		Models: []bootstrap.ModelConfig{{Name: "m"}},
	}})
	if st.step != configStepHub {
		t.Fatalf("Chọn Provider trung bình đã có sẵn phải vào hub, nhận step=%d", st.step)
	}
	ids := hubFieldIDs(st.hubFields())
	// Provider nội bộ (type trống) không hiển thị ồn ào về Giao thức/Endpoint, nhưng vẫn giữ key/models/save.
	if slices.Contains(ids, "protocol") || slices.Contains(ids, "api") {
		t.Fatalf("hub của Provider nội bộ không nên có Giao thức/Endpoint, nhận %v", ids)
	}
	for _, want := range []string{"key", "baseurl", "models", "save"} {
		if !slices.Contains(ids, want) {
			t.Fatalf("hub thiếu %q, nhận %v", want, ids)
		}
	}
}

// Hub của Provider tùy chỉnh (openai Giao thức tường minh) mới hiển thị Giao thức và Endpoint.
func TestCustomProviderHubShowsProtocolAndEndpoint(t *testing.T) {
	st := &modelConfigState{editModelIdx: -1}
	st.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "proxy", Type: "openai", API: "responses", HasAPIKey: true,
		Models: []bootstrap.ModelConfig{{Name: "m"}},
	}})
	ids := hubFieldIDs(st.hubFields())
	if !slices.Contains(ids, "protocol") || !slices.Contains(ids, "api") {
		t.Fatalf("hub của provider openai tùy chỉnh phải có Giao thức/Endpoint, nhận %v", ids)
	}
}

// Esc quay lại theo từng cấp: chỉnh sửa nội tuyến ở hub → hub → danh sách Provider → Tắt.
func TestEscapeBackHierarchy(t *testing.T) {
	st := &modelConfigState{step: configStepHub, provider: "proxy"}
	st.beginInlineEdit("baseurl")
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if st.step != configStepHub || st.editingField != "" {
		t.Fatalf("Esc ở chỉnh sửa nội tuyến phải ở lại hub và hủy Nhập, nhận step=%d field=%q", st.step, st.editingField)
	}
	if got, ok := st.escapeBack(); !ok || got != configStepProvider {
		t.Fatalf("Esc ở hub phải quay về danh sách, nhận %d,%v", got, ok)
	}
	st.step = configStepProvider
	if _, ok := st.escapeBack(); ok {
		t.Fatal("Esc ở danh sách phải Tắt toàn bộ bảng")
	}
}

func TestModelListAddsAndEditsInPlace(t *testing.T) {
	st := &modelConfigState{step: configStepModels, editModelIdx: -1,
		models: []bootstrap.ModelConfig{{Name: "m1"}}, modelOrigins: []string{"m1"}}
	st.cursor = len(st.models)
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.step != configStepModels || st.editingField != configModelNameField || !st.addingModel {
		t.Fatalf("Thêm mới phải ở lại chỉnh sửa nội tuyến của hàng danh sách, step=%d field=%q adding=%v", st.step, st.editingField, st.addingModel)
	}
	st.input.SetValue("  m2  ")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.editingField != configModelWindowField || st.models[1].Name != "m2" {
		t.Fatalf("Sau khi gửi tên, phải vào cột cửa sổ ngay trên cùng một hàng, field=%q models=%#v", st.editingField, st.models)
	}
	st.input.SetValue("128K")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.editingField != "" || st.addingModel || st.models[1].ContextWindow != 128000 {
		t.Fatalf("Thêm Mô hình mới chưa hoàn tất trên cùng một trang: %#v", st)
	}
}

func TestModelListEditsSelectedCellAndCancels(t *testing.T) {
	st := &modelConfigState{step: configStepModels, editModelIdx: -1,
		models: []bootstrap.ModelConfig{{Name: "m1", ContextWindow: 1000}}, modelOrigins: []string{"m1"}}
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyRight})
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if st.editingField != configModelWindowField || st.step != configStepModels {
		t.Fatalf("Enter ở cột bên phải phải vào chỉnh sửa nội tuyến trong cửa sổ, step=%d field=%q", st.step, st.editingField)
	}
	st.input.SetValue("200K")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if st.editingField != "" || st.models[0].ContextWindow != 1000 {
		t.Fatalf("Esc phải hủy ô hiện tại và không đổi giá trị: %#v", st.models[0])
	}
}

func TestModelRenameProducesExplicitDraftAndReferenceNotice(t *testing.T) {
	st := &modelConfigState{
		step: configStepModels, provider: "proxy", models: []bootstrap.ModelConfig{{Name: "old"}},
		modelOrigins: []string{"old"}, snapshot: host.ModelConfigurationSnapshot{
			References: map[string][]string{"proxy\x00old": {"default", "writer fallback[0]"}},
		},
	}
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	st.input.SetValue("renamed")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	draft := st.draft()
	if len(draft.Renames) != 1 || draft.Renames[0] != (host.ModelRename{From: "old", To: "renamed"}) {
		t.Fatalf("Đổi tên Mô hình phải giữ quan hệ định danh tường minh, renames=%#v", draft.Renames)
	}
	if !strings.Contains(st.message, "Khi lưu sẽ đồng bộ cập nhật tham chiếu") || !strings.Contains(st.message, "default") {
		t.Fatalf("Đổi tên Mô hình được tham chiếu phải nhắc rõ hành vi lưu, message=%q", st.message)
	}
}

func TestModelListRendersEditableColumnsAndReferences(t *testing.T) {
	st := &modelConfigState{
		step: configStepModels, provider: "proxy", models: []bootstrap.ModelConfig{{Name: "deepseek-chat", ContextWindow: 128000}},
		modelOrigins: []string{"deepseek-chat"}, snapshot: host.ModelConfigurationSnapshot{
			References: map[string][]string{"proxy\x00deepseek-chat": {"default"}},
		},
	}
	plain := ansi.Strip(renderModelConfigModal(120, st))
	for _, want := range []string{"ID mô hình", "Cửa sổ ngữ cảnh", "Tham chiếu", "deepseek-chat", "128K", "default", "+ Thêm mô hình"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("Bảng Mô hình một trang thiếu %q:\n%s", want, plain)
		}
	}
}

func TestModelNameInlineEditorKeepsModalRowsIntact(t *testing.T) {
	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })
	st := &modelConfigState{
		step: configStepModels, provider: "deepseek",
		models:       []bootstrap.ModelConfig{{Name: "deepseek-v4-pro"}, {Name: "deepseek-v4-flash"}},
		modelOrigins: []string{"deepseek-v4-pro", "deepseek-v4-flash"},
	}
	st.beginModelEdit(0, 0)
	view := renderModelConfigModal(120, st)
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		if width := lipgloss.Width(line); width != 72 {
			t.Fatalf("Chỉnh sửa tên Mô hình nội tuyến làm hỏng độ rộng dòng %d: width=%d line=%q\n%s", i, width, ansi.Strip(line), ansi.Strip(view))
		}
	}
	if len(lines) != 7 {
		t.Fatalf("Chỉnh sửa nội tuyến không được tạo ngắt dòng vật lý, nhận %d dòng:\n%s", len(lines), ansi.Strip(view))
	}
}

func TestRenamedReferencedModelStillCannotBeDeleted(t *testing.T) {
	st := &modelConfigState{
		provider: "proxy", currentModel: "old", models: []bootstrap.ModelConfig{{Name: "renamed"}},
		modelOrigins: []string{"old"}, snapshot: host.ModelConfigurationSnapshot{
			References: map[string][]string{"proxy\x00old": {"default"}},
		},
	}
	if st.deleteModel(0) || len(st.models) != 1 || !strings.Contains(st.message, "đang được dùng") {
		t.Fatalf("Khi chưa lưu việc đổi tên, vẫn phải bảo vệ xóa theo định danh gốc, models=%#v message=%q", st.models, st.message)
	}
}

func TestCancellingNewModelNameRemovesTemporaryRow(t *testing.T) {
	st := &modelConfigState{step: configStepModels, models: []bootstrap.ModelConfig{{Name: "m1"}}, modelOrigins: []string{"m1"}}
	st.cursor = 1
	m := Model{modelConfig: st}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if len(st.models) != 1 || len(st.modelOrigins) != 1 || st.cursor != 1 {
		t.Fatalf("Hủy thêm mới phải dọn dòng tạm, models=%#v origins=%#v cursor=%d", st.models, st.modelOrigins, st.cursor)
	}
}

func TestParseContextWindowInput(t *testing.T) {
	cases := map[string]int{
		"": 0, "0": 0, "auto": 0, "128K": 128000, "1M": 1000000,
		"1.5m": 1500000, "200000": 200000,
	}
	for input, want := range cases {
		got, err := parseContextWindowInput(input)
		if err != nil || got != want {
			t.Errorf("parseContextWindowInput(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"-1", "abc", "0.5"} {
		if _, err := parseContextWindowInput(input); err == nil {
			t.Errorf("parseContextWindowInput(%q) should fail", input)
		}
	}
}

func TestModelConfigModalDoesNotRenderAPIKey(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", apiKeyOptional: true}
	state.beginInlineEdit("key")
	state.input.SetValue("sk-super-secret")
	view := renderModelConfigModal(120, state)
	if strings.Contains(view, "sk-super-secret") {
		t.Fatal("API key leaked into rendered modal")
	}
}

func TestProviderHubEditsAPIKeyInlineAndTrims(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", existing: true,
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: true, apiKeyAction: host.APIKeyKeep}
	state.cursor = hubFieldIndex(state.hubFields(), "key")
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.step != configStepHub || state.editingField != "key" {
		t.Fatalf("API Key phải chỉnh sửa ngay trên hàng gốc trong hub, nhận step=%d field=%q", state.step, state.editingField)
	}
	state.input.SetValue("  sk-new-secret-1234567890  ")
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.editingField != "" || state.apiKey != "sk-new-secret-1234567890" || state.apiKeyAction != host.APIKeyReplace {
		t.Fatalf("Kết quả gửi nội tuyến API Key sai: field=%q key=%q action=%q", state.editingField, state.apiKey, state.apiKeyAction)
	}
	if got := state.keyStatus(); got != "sk-n******7890" {
		t.Fatalf("API Key mới phải hiển thị gợi ý che bớt, nhận %q", got)
	}
}

func TestProviderHubEditsBaseURLInlineAndKeepsLongTailVisible(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", existing: true,
		apiKeyOptional: true, baseURL: "https://old.example/v1"}
	state.cursor = hubFieldIndex(state.hubFields(), "baseurl")
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.editingField != "baseurl" || state.input.Value() != "https://old.example/v1" {
		t.Fatalf("Base URL phải được điền sẵn để chỉnh sửa ngay trên hàng gốc, field=%q value=%q", state.editingField, state.input.Value())
	}
	state.input.SetValue("  https://example.com/a/very/long/provider/path/UNIQUE-END  ")
	state.input.CursorEnd()
	view := renderModelConfigModal(76, state)
	if !strings.Contains(view, "UNIQUE-END") {
		t.Fatalf("Khi chỉnh sửa Base URL dài, phải hiển thị phần đuôi gần con trỏ:\n%s", view)
	}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if state.baseURL != "https://example.com/a/very/long/provider/path/UNIQUE-END" {
		t.Fatalf("Base URL chưa TrimSpace, nhận %q", state.baseURL)
	}
}

func TestSaveConfigHighlightsOnlyWhenDirty(t *testing.T) {
	state := &modelConfigState{editModelIdx: -1}
	state.applyProviderChoice(configProviderChoice{existing: &host.ProviderSnapshot{
		Name: "proxy", Type: "openai", BaseURL: "https://old.example/v1", HasAPIKey: true,
		APIKeyHint: "sk-o******7890", Models: []bootstrap.ModelConfig{{Name: "m1"}},
	}})
	if state.isDirty() {
		t.Fatal("Vừa vào Provider đã có sẵn thì không được đánh dấu là đã sửa")
	}
	state.baseURL = "https://new.example/v1"
	if !state.isDirty() {
		t.Fatal("Sau khi Base URL thay đổi thì phải được đánh dấu là đã sửa")
	}
	state.baseURL = "https://old.example/v1"
	if state.isDirty() {
		t.Fatal("Khi đổi lại giá trị nền thì phải tự quay về trạng thái chưa sửa")
	}
	state.beginInlineEdit("baseurl")
	state.input.SetValue("https://editing.example/v1")
	if !state.isDirty() {
		t.Fatal("Khi Nhập giá trị mới cho Base URL thì phải được đánh dấu đã sửa theo thời gian thực")
	}
	state.input.SetValue(" https://old.example/v1 ")
	if state.isDirty() {
		t.Fatal("Khi Nhập nội tuyến bằng giá trị nền thì không được báo sai là đã sửa")
	}
	state.editingField = ""
	state.apiKeyAction = host.APIKeyReplace
	state.apiKey = "sk-new-secret"
	if !state.isDirty() {
		t.Fatal("Sau khi thay API Key thì phải được đánh dấu là đã sửa")
	}

	oldProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(oldProfile) })
	lines := renderProviderHubFields(state, 68)
	want := lipgloss.NewStyle().Foreground(colorSuccess).Render("Lưu cấu hình")
	found := false
	for _, line := range lines {
		if strings.Contains(line, want) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Khi có thay đổi, mục lưu phải dùng màu thành công, lines=%q", lines)
	}

	newProvider := &modelConfigState{}
	if !newProvider.isDirty() {
		t.Fatal("Thêm Provider phải luôn được xem là thay đổi chưa lưu")
	}
}

func TestStyledBaseURLLineKeepsANSIAndFillsModalWidth(t *testing.T) {
	plain := "› Base URL  https://api.deepseek.com"
	styled := "\x1b[38;2;255;200;0m› \x1b[0m" +
		"\x1b[1;38;2;255;200;0mBase URL\x1b[0m  " +
		"\x1b[4;38;2;220;220;220mhttps://api.deepseek.com\x1b[0m"
	if got := ansi.Strip(truncateStyledWidth(styled, 56)); got != plain {
		t.Fatalf("Cắt ngắn có nhận biết ANSI đã làm hỏng dòng Nhập: %q", got)
	}

	modal := renderPaddedModalFrame(60, 3, "/config", "", []string{styled})
	lines := strings.Split(modal, "\n")
	if len(lines) != 3 || lipgloss.Width(lines[1]) != 60 {
		t.Fatalf("Dòng Nhập của lớp phủ không lấp đầy độ rộng cố định: width=%d\n%s", lipgloss.Width(lines[1]), modal)
	}
	if !strings.Contains(ansi.Strip(lines[1]), "https://api.deepseek.com") {
		t.Fatalf("Lớp phủ bị mất Base URL:\n%s", modal)
	}
}

func TestProviderHubDeleteClearsOnlyOptionalAPIKey(t *testing.T) {
	optional := &modelConfigState{step: configStepHub, provider: "proxy", providerType: "openai",
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: true, apiKeyAction: host.APIKeyKeep}
	optional.cursor = hubFieldIndex(optional.hubFields(), "key")
	m := Model{modelConfig: optional}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyDelete})
	if optional.apiKeyAction != host.APIKeyClear || optional.keyStatus() != "Đã xóa" {
		t.Fatalf("Delete của Key tùy chọn phải đánh dấu là đã xóa, action=%q status=%q", optional.apiKeyAction, optional.keyStatus())
	}

	required := &modelConfigState{step: configStepHub, provider: "openrouter",
		hasAPIKey: true, apiKeyHint: "sk-o******7890", apiKeyOptional: false, apiKeyAction: host.APIKeyKeep}
	required.cursor = hubFieldIndex(required.hubFields(), "key")
	m = Model{modelConfig: required}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyDelete})
	if required.apiKeyAction != host.APIKeyKeep || !strings.Contains(required.message, "không thể xóa") {
		t.Fatalf("Key bắt buộc không được bị xóa, action=%q message=%q", required.apiKeyAction, required.message)
	}
}

func TestConfigTextInputSupportsCursorEditing(t *testing.T) {
	state := &modelConfigState{step: configStepCustomName}
	state.startTextInput("ac", "Tên Provider", false)
	state.input.SetCursor(1)
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if got := state.input.Value(); got != "abc" {
		t.Fatalf("Hộp Nhập dùng chung phải hỗ trợ chèn tại vị trí con trỏ, nhận %q", got)
	}
}

func TestProviderHubShowsConfigPathAndConnectionAction(t *testing.T) {
	state := &modelConfigState{
		step: configStepHub, provider: "proxy", apiKeyOptional: true, currentModel: "m2",
		models:   []bootstrap.ModelConfig{{Name: "m1"}, {Name: "m2"}},
		snapshot: host.ModelConfigurationSnapshot{ConfigPath: `C:\work\.ainovel\config.json`},
	}
	fields := state.hubFields()
	idx := hubFieldIndex(fields, "test")
	if idx < 0 || fields[idx].value != "m2" {
		t.Fatalf("Kiểm tra kết nối phải ưu tiên Mô hình hiện tại, fields=%#v", fields)
	}
	view := renderModelConfigModal(120, state)
	for _, want := range []string{"Cấu hình nâng cao", "extra_body"} {
		if !strings.Contains(view, want) {
			t.Fatalf("Hub cấu hình thiếu %q:\n%s", want, view)
		}
	}
	compact := strings.NewReplacer("\r", "", "\n", "", " ", "", "│", "").Replace(view)
	if !strings.Contains(compact, `C:\work\.ainovel\config.json`) {
		t.Fatalf("Hub cấu hình chưa hiển thị đầy đủ đường dẫn cấu hình:\n%s", view)
	}
}

func TestModelConfigMessageWrapKeepsErrorTail(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", apiKeyOptional: true,
		message: "Kết nối thất bại：" + strings.Repeat("phía trên trả về lỗi rất dài", 8) + " UNIQUE-ERROR-TAIL"}
	view := renderModelConfigModal(64, state)
	compact := strings.NewReplacer("\r", "", "\n", "", " ", "", "│", "").Replace(view)
	if !strings.Contains(compact, "UNIQUE-ERROR-TAIL") {
		t.Fatalf("Lỗi dài không được cắt mất phần đuôi:\n%s", view)
	}
}

func TestConnectionActionStartsAsyncTestWithoutLeavingHub(t *testing.T) {
	state := &modelConfigState{step: configStepHub, provider: "proxy", providerType: "openai",
		apiKeyOptional: true, models: []bootstrap.ModelConfig{{Name: "m1"}}}
	state.cursor = hubFieldIndex(state.hubFields(), "test")
	m := Model{modelConfig: state}
	_, cmd := m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !state.testing || state.step != configStepHub {
		t.Fatalf("Kiểm tra kết nối phải ở lại hub một cách bất đồng bộ, cmd=%v testing=%v step=%d", cmd != nil, state.testing, state.step)
	}
}

func TestConnectionTestCanBeCancelled(t *testing.T) {
	cancelled := false
	state := &modelConfigState{step: configStepHub, provider: "proxy", testing: true,
		testCancel: func() { cancelled = true }}
	m := Model{modelConfig: state}
	m.handleModelConfigKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !cancelled || !state.testing || state.message != "Đang hủy kiểm tra kết nối..." {
		t.Fatalf("Esc phải hủy kiểm tra đang diễn ra và chờ kết quả, cancelled=%v testing=%v message=%q", cancelled, state.testing, state.message)
	}

	updated, _, handled := m.handleRuntimeMsg(modelConfigConnectionMsg{err: context.Canceled})
	m = updated.(Model)
	if !handled || m.modelConfig.testing || m.modelConfig.message != "Kiểm tra kết nối đã bị hủy" {
		t.Fatalf("Kết quả hủy chưa được quy tụ đúng: handled=%v testing=%v message=%q", handled, m.modelConfig.testing, m.modelConfig.message)
	}
}

func TestConfigCommandIsRegistered(t *testing.T) {
	spec, ok := commandRegistryInstance().Find("config")
	if !ok {
		t.Fatal("/config is not registered")
	}
	if spec.Usage != "/config" || !spec.AutoExecute {
		t.Fatalf("config spec = %#v", spec)
	}
}

func TestModelSwitchLabelIncludesContextWindow(t *testing.T) {
	state := modelSwitchState{models: []host.ConfiguredModel{{Name: "gpt-test", ContextWindow: 400000}}}
	if got := state.modelLabel(); got != "gpt-test · 400K" {
		t.Fatalf("modelLabel = %q", got)
	}
}

// Nhất quán với /model: /config được render thành floating layer có khung với chiều cao theo nội dung (không còn kéo giãn thành overlay căn giữa cao 3/4 màn hình).
func TestModelConfigModalIsCompactOverlay(t *testing.T) {
	state := &modelConfigState{step: configStepProvider, providerChoices: []configProviderChoice{
		{label: "Chỉnh sửa openrouter", existing: &host.ProviderSnapshot{Name: "openrouter"}},
		{label: "+ Thêm Provider…", add: true},
	}}
	lines := strings.Split(renderModelConfigModal(120, state), "\n")

	// 1 dòng tiêu đề + 2 tùy chọn + viền trên dưới = 5 dòng; chiều cao theo nội dung, sẽ không phình ra vì chiều cao màn hình.
	if len(lines) != 5 {
		t.Fatalf("Floating layer gọn phải là 5 dòng (chiều cao nội dung), nhận được %d dòng:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	if !strings.Contains(lines[0], "┌") || !strings.Contains(lines[0], "/config") {
		t.Fatalf("Dòng đầu phải là viền trên có tiêu đề /config, nhận được %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-1], "└") {
		t.Fatalf("Dòng cuối phải là viền dưới, nhận được %q", lines[len(lines)-1])
	}
}

// Menu cấp một chỉ liệt kê “chỉnh sửa mục đã có + mục thêm mới”, không trải toàn bộ thư mục Provider dựng sẵn; thư mục chỉ xuất hiện ở menu cấp hai.
func TestProviderMenuIsTwoLevel(t *testing.T) {
	state := &modelConfigState{snapshot: host.ModelConfigurationSnapshot{
		Providers:       []host.ProviderSnapshot{{Name: "openrouter"}, {Name: "anthropic"}},
		DefaultProvider: "openrouter",
	}}
	state.buildProviderMenus()

	// Cấp một = 2 mục chỉnh sửa + 1 mục thêm mới; mục cuối là “Thêm mới”, và không có add/preset nào khác lẫn vào.
	if len(state.providerChoices) != 3 {
		t.Fatalf("Menu cấp một phải là 2 mục chỉnh sửa + 1 mục thêm mới, nhận được %d mục", len(state.providerChoices))
	}
	if !state.providerChoices[len(state.providerChoices)-1].add {
		t.Fatal("Mục cuối của menu cấp một phải là lối vào “Thêm Provider…”")
	}
	for i, c := range state.providerChoices[:2] {
		if c.existing == nil || c.add {
			t.Fatalf("Mục thứ %d của menu cấp một phải là chỉnh sửa Provider đã có, nhận được %#v", i, c)
		}
	}

	// Cấp hai = thư mục có thể thêm mới: không rỗng, và các mục dựng sẵn đã cấu hình (openrouter/anthropic) không xuất hiện lặp lại nữa.
	if len(state.presetChoices) == 0 {
		t.Fatal("Menu cấp hai phải liệt kê thư mục Provider có thể thêm mới")
	}
	if len(state.presetChoices) >= len(bootstrap.ProviderPresets()) {
		t.Fatalf("Provider dựng sẵn đã cấu hình phải được loại khỏi thư mục thêm mới, presets=%d tổng=%d",
			len(state.presetChoices), len(bootstrap.ProviderPresets()))
	}
	for _, c := range state.presetChoices {
		if c.preset != nil && (c.preset.Name == "openrouter" || c.preset.Name == "anthropic") {
			t.Fatalf("Thư mục thêm mới không được chứa %q đã cấu hình", c.preset.Name)
		}
	}
}
