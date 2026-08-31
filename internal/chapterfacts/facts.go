package chapterfacts

import (
	"fmt"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

// Properties trả về các trường JSON Schema dùng chung cho toàn bộ dữ kiện chương.
func Properties(includeFeedback bool) []schema.Prop {
	textList := func(description string) map[string]any {
		return schema.Array(description, schema.String(description))
	}
	timeline := schema.Object(
		schema.Property("time", schema.String("Thời điểm trong truyện")).Required(),
		schema.Property("event", schema.String("Sự kiện")).Required(),
		schema.Property("characters", textList("Nhân vật liên quan")).Required(),
	)
	foreshadow := schema.Object(
		schema.Property("id", schema.String("ID tình tiết gài trước")).Required(),
		schema.Property("action", schema.Enum("Thao tác", "plant", "advance", "resolve")).Required(),
		schema.Property("description", llmcontract.Nullable(schema.String("Mô tả khi plant; null cho các thao tác khác"))).Required(),
	)
	relationship := schema.Object(
		schema.Property("character_a", schema.String("Nhân vật A")).Required(),
		schema.Property("character_b", schema.String("Nhân vật B")).Required(),
		schema.Property("relation", schema.String("Mối quan hệ khi chương kết thúc")).Required(),
	)
	stateChange := schema.Object(
		schema.Property("entity", schema.String("Thực thể")).Required(),
		schema.Property("field", schema.String("Thuộc tính")).Required(),
		schema.Property("old_value", llmcontract.Nullable(schema.String("Giá trị trước khi thay đổi"))).Required(),
		schema.Property("new_value", schema.String("Giá trị sau khi thay đổi")).Required(),
		schema.Property("reason", llmcontract.Nullable(schema.String("Lý do"))).Required(),
	)
	props := []schema.Prop{
		schema.Property("title", schema.String("Tiêu đề cuối cùng")).Required(),
		schema.Property("summary", schema.String("Tóm tắt chương")).Required(),
		schema.Property("characters", textList("Nhân vật xuất hiện")).Required(),
		schema.Property("key_events", textList("Sự kiện then chốt")).Required(),
		schema.Property("timeline_events", schema.Array("Sự kiện dòng thời gian", timeline)).Required(),
		schema.Property("foreshadow_updates", schema.Array("Thao tác tình tiết gài trước", foreshadow)).Required(),
		schema.Property("relationship_changes", schema.Array("Thay đổi mối quan hệ", relationship)).Required(),
		schema.Property("state_changes", schema.Array("Thay đổi trạng thái", stateChange)).Required(),
		schema.Property("cast_intros", schema.Array("Nhân vật phụ mới", schema.Object(
			schema.Property("name", schema.String("Tên")).Required(),
			schema.Property("brief_role", schema.String("Vai trò")).Required(),
		))).Required(),
		schema.Property("hook_type", llmcontract.Nullable(schema.Enum("Móc câu cuối chương", domain.HookTypes()...))).Required(),
		schema.Property("dominant_strand", llmcontract.Nullable(schema.Enum("Tuyến tự sự chủ đạo", domain.DominantStrands()...))).Required(),
	}
	if includeFeedback {
		feedback := schema.Object(
			schema.Property("deviation", schema.String("Mô tả điểm lệch khỏi dàn ý")).Required(),
			schema.Property("suggestion", schema.String("Đề xuất điều chỉnh dàn ý tiếp theo")).Required(),
		)
		feedback["description"] = "Đối tượng đề xuất cho dàn ý tiếp theo; phải truyền trực tiếp JSON object, không truyền JSON đã chuỗi hóa"
		props = append(props, schema.Property("feedback", llmcontract.Nullable(feedback)).Required())
	}
	return props
}

// Validate kiểm tra các ràng buộc xác định dùng chung cho lần gửi thông thường và lần chỉnh sửa thủ công.
func Validate(facts domain.ChapterFacts) error {
	if strings.TrimSpace(facts.Title) == "" {
		return fmt.Errorf("title là bắt buộc")
	}
	if strings.TrimSpace(facts.Summary) == "" {
		return fmt.Errorf("summary là bắt buộc")
	}
	if len(facts.KeyEvents) == 0 {
		return fmt.Errorf("key_events phải chứa ít nhất một sự kiện")
	}
	if err := validateTextItems("characters", facts.Characters); err != nil {
		return err
	}
	if err := validateTextItems("key_events", facts.KeyEvents); err != nil {
		return err
	}
	for i, event := range facts.TimelineEvents {
		if strings.TrimSpace(event.Time) == "" || strings.TrimSpace(event.Event) == "" {
			return fmt.Errorf("timeline_events[%d] yêu cầu time và event", i)
		}
		if err := validateTextItems(fmt.Sprintf("timeline_events[%d].characters", i), event.Characters); err != nil {
			return err
		}
	}
	for i, update := range facts.ForeshadowUpdates {
		if strings.TrimSpace(update.ID) == "" {
			return fmt.Errorf("foreshadow_updates[%d].id là bắt buộc", i)
		}
		switch update.Action {
		case "plant":
			if strings.TrimSpace(update.Description) == "" {
				return fmt.Errorf("foreshadow_updates[%d] với plant yêu cầu description", i)
			}
		case "advance", "resolve":
		default:
			return fmt.Errorf("foreshadow_updates[%d].action không hợp lệ: %q", i, update.Action)
		}
	}
	for i, change := range facts.RelationshipChanges {
		if strings.TrimSpace(change.CharacterA) == "" || strings.TrimSpace(change.CharacterB) == "" || strings.TrimSpace(change.Relation) == "" {
			return fmt.Errorf("relationship_changes[%d] yêu cầu character_a, character_b và relation", i)
		}
		if change.CharacterA == change.CharacterB {
			return fmt.Errorf("relationship_changes[%d] không thể liên hệ một nhân vật với chính mình", i)
		}
	}
	for i, change := range facts.StateChanges {
		if strings.TrimSpace(change.Entity) == "" || strings.TrimSpace(change.Field) == "" || strings.TrimSpace(change.NewValue) == "" {
			return fmt.Errorf("state_changes[%d] yêu cầu entity, field và new_value", i)
		}
	}
	for i, intro := range facts.CastIntros {
		if strings.TrimSpace(intro.Name) == "" || strings.TrimSpace(intro.BriefRole) == "" {
			return fmt.Errorf("cast_intros[%d] yêu cầu name và brief_role", i)
		}
	}
	if facts.HookType != "" && !domain.ValidHookType(facts.HookType) {
		return fmt.Errorf("hook_type không hợp lệ %q", facts.HookType)
	}
	if facts.DominantStrand != "" && !domain.ValidDominantStrand(facts.DominantStrand) {
		return fmt.Errorf("dominant_strand không hợp lệ %q", facts.DominantStrand)
	}
	if facts.Feedback != nil && (strings.TrimSpace(facts.Feedback.Deviation) == "" || strings.TrimSpace(facts.Feedback.Suggestion) == "") {
		return fmt.Errorf("feedback yêu cầu deviation và suggestion")
	}
	return nil
}

func validateTextItems(name string, items []string) error {
	for i, item := range items {
		if strings.TrimSpace(item) == "" {
			return fmt.Errorf("%s[%d] không được để trống", name, i)
		}
	}
	return nil
}
