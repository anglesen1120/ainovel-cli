package tui

import (
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/host"
)

type fakeModelRuntime struct {
	providers   []string
	models      map[string][]host.ConfiguredModel
	curProvider string
	curModel    string
	thinking    map[string]string // role -> ý định gốc đã lưu
	available   []agentcore.ThinkingLevel
	setCalls    []struct{ role, level string }
	switchCalls int
}

func (f *fakeModelRuntime) ConfiguredProviders() []string { return f.providers }
func (f *fakeModelRuntime) ConfiguredModelOptions(provider string) []host.ConfiguredModel {
	return f.models[provider]
}
func (f *fakeModelRuntime) CurrentModelSelection(role string) (string, string, bool) {
	return f.curProvider, f.curModel, true
}
func (f *fakeModelRuntime) AvailableThinking(role string) []agentcore.ThinkingLevel {
	return f.available
}
func (f *fakeModelRuntime) CurrentThinking(role string) string { return f.thinking[role] }
func (f *fakeModelRuntime) SwitchModel(role, provider, model string) error {
	f.switchCalls++
	f.curProvider, f.curModel = provider, model
	return nil
}
func (f *fakeModelRuntime) SetRoleThinking(role, level string) error {
	f.setCalls = append(f.setCalls, struct{ role, level string }{role, level})
	if f.thinking == nil {
		f.thinking = map[string]string{}
	}
	f.thinking[role] = level
	return nil
}

// Khi ý định về mức độ đã lưu cao hơn năng lực Mô hình hiện tại và bảng điều khiển không thể hiển thị, nếu người dùng không chỉnh trường mức độ mà áp dụng trực tiếp,
// thì không được xóa nhầm ý định thành giá trị mặc định ban đầu.
func TestModelSwitchKeepsUnrepresentableThinkingIntent(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "chat-only"}}},
		curProvider: "proxy", curModel: "chat-only",
		thinking:  map[string]string{"writer": "high"},
		available: nil, // Mô hình hiện tại chỉ có một mức “kế thừa”
	}
	st := newModelSwitchState(rt, "writer")
	if st.thinkingKey() != "" {
		t.Fatalf("khi không thể hiển thị high, bảng điều khiển phải rơi vào mức kế thừa, nhận được %q", st.thinkingKey())
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 0 {
		t.Fatalf("không chỉnh mức độ thì không được ghi ngược: %+v", rt.setCalls)
	}
	if rt.thinking["writer"] != "high" {
		t.Fatalf("ý định bị xóa thành %q, phải giữ lại high", rt.thinking["writer"])
	}
}

// Nếu người dùng chỉnh rõ ràng mức độ trong bảng điều khiển, thì phải ghi ngược thành giá trị mới.
func TestModelSwitchAppliesExplicitThinkingChange(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "m"}}},
		curProvider: "proxy", curModel: "m",
		thinking:  map[string]string{"writer": ""},
		available: []agentcore.ThinkingLevel{"low", "high"},
	}
	st := newModelSwitchState(rt, "writer")
	st.focus = modelFocusThinking
	st.cycle(1, rt) // di chuyển trường mức độ
	want := st.thinkingKey()
	if want == "" {
		t.Fatal("điều kiện trước của kiểm thử: phải đã di chuyển tới một mức độ không rỗng nào đó")
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 1 || rt.setCalls[0].level != want {
		t.Fatalf("chỉnh rõ ràng thì phải ghi ngược %q, nhận được %+v", want, rt.setCalls)
	}
}
