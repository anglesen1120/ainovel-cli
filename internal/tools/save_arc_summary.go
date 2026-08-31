package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveArcSummaryTool lưu tóm tắt cung, ảnh chụp trạng thái nhân vật và quy tắc viết; Editor gọi khi cung kết thúc.
type SaveArcSummaryTool struct {
	store *store.Store
}

func NewSaveArcSummaryTool(store *store.Store) *SaveArcSummaryTool {
	return &SaveArcSummaryTool{store: store}
}

func (t *SaveArcSummaryTool) Name() string { return "save_arc_summary" }
func (t *SaveArcSummaryTool) Description() string {
	return "Lưu tóm tắt cung, ảnh chụp trạng thái nhân vật và quy tắc viết (chế độ truyện dài, gọi khi cung kết thúc)"
}
func (t *SaveArcSummaryTool) Label() string { return "Lưu tóm tắt cung" }

// Công cụ ghi, không cho phép chạy song song.
func (t *SaveArcSummaryTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveArcSummaryTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveArcSummaryTool) Schema() map[string]any {
	snapshotSchema := schema.Object(
		schema.Property("name", schema.String("Tên nhân vật")).Required(),
		schema.Property("status", schema.String("Trạng thái hiện tại (còn sống/bị thương/mất tích, v.v.)")).Required(),
		schema.Property("power", schema.String("Thay đổi năng lực")),
		schema.Property("motivation", schema.String("Động cơ hiện tại")).Required(),
		schema.Property("relations", schema.String("Thay đổi quan hệ chính")),
	)
	voiceSchema := schema.Object(
		schema.Property("name", schema.String("Tên nhân vật")).Required(),
		schema.Property("rules", schema.Array("2-3 quy tắc về đặc trưng ngôn ngữ (mỗi quy tắc ≤30 ký tự)", schema.String(""))).Required(),
	)
	styleRulesSchema := schema.Object(
		schema.Property("prose", schema.Array("3-5 quy tắc về phong cách trần thuật (mỗi quy tắc ≤50 ký tự, cụ thể và khả thi)", schema.String(""))).Required(),
		schema.Property("dialogue", schema.Array("Quy tắc đặc trưng lời thoại của nhân vật chính", voiceSchema)).Required(),
		schema.Property("taboos", schema.Array("Cách viết cần tránh cho truyện này", schema.String(""))),
	)
	return schema.Object(
		schema.Property("volume", schema.Int("Số tập")).Required(),
		schema.Property("arc", schema.Int("Số cung")).Required(),
		schema.Property("title", schema.String("Tiêu đề cung")).Required(),
		schema.Property("summary", schema.String("Tóm tắt cung (không quá 500 chữ)")).Required(),
		schema.Property("key_events", schema.Array("Sự kiện chính trong cung", schema.String(""))).Required(),
		schema.Property("character_snapshots", schema.Array("Ảnh chụp trạng thái nhân vật", snapshotSchema)).Required(),
		schema.Property("style_rules", styleRulesSchema).Required(),
	)
}

func (t *SaveArcSummaryTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Volume             int                        `json:"volume"`
		Arc                int                        `json:"arc"`
		Title              string                     `json:"title"`
		Summary            string                     `json:"summary"`
		KeyEvents          []string                   `json:"key_events"`
		CharacterSnapshots []domain.CharacterSnapshot `json:"character_snapshots"`
		StyleRules         *arcSummaryStyleRules      `json:"style_rules"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		if strings.Contains(err.Error(), "style_rules.dialogue") {
			return nil, fmt.Errorf("đối số không hợp lệ: style_rules.dialogue phải là mảng đối tượng {name, rules}, không phải chuỗi: %w: %w", errs.ErrToolArgs, err)
		}
		return nil, fmt.Errorf("đối số không hợp lệ: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Volume <= 0 || a.Arc <= 0 {
		return nil, fmt.Errorf("volume và arc phải lớn hơn 0: %w", errs.ErrToolArgs)
	}
	if strings.TrimSpace(a.Title) == "" || strings.TrimSpace(a.Summary) == "" {
		return nil, fmt.Errorf("bắt buộc có title và summary: %w", errs.ErrToolArgs)
	}
	if err := validateArcSummaryStyleRules(a.StyleRules); err != nil {
		return nil, err
	}
	for i := range a.CharacterSnapshots {
		a.CharacterSnapshots[i].Volume = a.Volume
		a.CharacterSnapshots[i].Arc = a.Arc
	}
	arcSummary := domain.ArcSummary{
		Volume: a.Volume, Arc: a.Arc, Title: a.Title, Summary: a.Summary, KeyEvents: a.KeyEvents,
	}
	rules := domain.WritingStyleRules{
		Volume:    a.Volume,
		Arc:       a.Arc,
		Prose:     a.StyleRules.Prose,
		Dialogue:  a.StyleRules.Dialogue,
		Taboos:    a.StyleRules.Taboos,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	replay, err := t.arcSummaryReplay(arcSummary, a.CharacterSnapshots, rules)
	if err != nil {
		return nil, err
	}
	if !replay {
		if err := requireAggregateTarget(t.store, flow.AggregateArcSummary, a.Volume, a.Arc, 0); err != nil {
			return nil, err
		}
		if len(a.CharacterSnapshots) > 0 {
			if err := t.store.Characters.SaveSnapshots(a.Volume, a.Arc, a.CharacterSnapshots); err != nil {
				return nil, fmt.Errorf("không thể lưu ảnh chụp nhân vật: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		if err := t.store.World.SaveStyleRules(rules); err != nil {
			return nil, fmt.Errorf("không thể lưu quy tắc văn phong: %w: %w", errs.ErrStoreWrite, err)
		}

		// Tóm tắt cung là dấu hoàn tất của Router, được ghi như công kiện ngữ nghĩa cuối cùng. Trước đó bất kỳ bước nào
		// thất bại thì tóm tắt vẫn còn thiếu; sau khi khôi phục, Router vẫn sẽ phân lại nhiệm vụ này.
		if err := t.store.Summaries.SaveArcSummary(arcSummary); err != nil {
			return nil, fmt.Errorf("không thể lưu tóm tắt cung: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	artifacts := []string{fmt.Sprintf("summaries/arc-v%02da%02d.json", a.Volume, a.Arc)}
	if len(a.CharacterSnapshots) > 0 {
		artifacts = append(artifacts, fmt.Sprintf("meta/snapshots/v%02da%02d.json", a.Volume, a.Arc))
	}
	artifacts = append(artifacts, "meta/style_rules.json")

	if _, err := t.store.Checkpoints.AppendArtifacts(
		domain.ArcScope(a.Volume, a.Arc), "arc_summary", artifacts...,
	); err != nil {
		return nil, fmt.Errorf("không thể tạo checkpoint tóm tắt cung: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(map[string]any{
		"saved": true, "type": "arc_summary",
		"volume": a.Volume, "arc": a.Arc,
		"snapshots":         len(a.CharacterSnapshots),
		"style_rules_saved": true,
	})
}

// arcSummaryReplay chỉ cho qua phần kết thúc idempotent có nội dung hoàn toàn giống nhau, dùng khi công kiện ngữ nghĩa đã được ghi xuống nhưng
// checkpoint thêm thất bại cần được thử lại. Mọi khác biệt đều là xung đột rõ ràng, không thể dùng thử lại để ghi đè các dữ kiện tổng hợp lịch sử.
func (t *SaveArcSummaryTool) arcSummaryReplay(
	summary domain.ArcSummary,
	snapshots []domain.CharacterSnapshot,
	rules domain.WritingStyleRules,
) (bool, error) {
	existing, err := t.store.Summaries.LoadArcSummary(summary.Volume, summary.Arc)
	if err != nil {
		return false, fmt.Errorf("không thể tải tóm tắt cung: %w: %w", errs.ErrStoreRead, err)
	}
	if existing == nil {
		return false, nil
	}
	storedSnapshots, err := t.store.Characters.LoadSnapshots(summary.Volume, summary.Arc)
	if err != nil {
		return false, fmt.Errorf("không thể tải ảnh chụp nhân vật: %w: %w", errs.ErrStoreRead, err)
	}
	storedRules, err := t.store.World.LoadStyleRules()
	if err != nil {
		return false, fmt.Errorf("không thể tải quy tắc văn phong: %w: %w", errs.ErrStoreRead, err)
	}
	if storedRules != nil {
		rules.UpdatedAt = storedRules.UpdatedAt
	}
	if !reflect.DeepEqual(*existing, summary) ||
		!slices.Equal(storedSnapshots, snapshots) ||
		storedRules == nil || !reflect.DeepEqual(*storedRules, rules) {
		return false, fmt.Errorf("Tóm tắt cung %d của tập %d đã tồn tại nhưng công kiện liên quan khác nhau, từ chối ghi đè: %w", summary.Volume, summary.Arc, errs.ErrToolConflict)
	}
	return true, nil
}

type arcSummaryStyleRules struct {
	Prose    []string                `json:"prose"`
	Dialogue []domain.CharacterVoice `json:"dialogue"`
	Taboos   []string                `json:"taboos"`
}

func validateArcSummaryStyleRules(rules *arcSummaryStyleRules) error {
	if rules == nil {
		return fmt.Errorf("bắt buộc có style_rules: %w", errs.ErrToolArgs)
	}
	if len(rules.Prose) == 0 {
		return fmt.Errorf("bắt buộc có style_rules.prose: %w", errs.ErrToolArgs)
	}
	if len(rules.Dialogue) == 0 {
		return fmt.Errorf("bắt buộc có style_rules.dialogue; cần mảng đối tượng {name, rules}: %w", errs.ErrToolArgs)
	}
	for i, voice := range rules.Dialogue {
		if strings.TrimSpace(voice.Name) == "" {
			return fmt.Errorf("bắt buộc có style_rules.dialogue[%d].name: %w", i, errs.ErrToolArgs)
		}
		if len(voice.Rules) == 0 {
			return fmt.Errorf("bắt buộc có style_rules.dialogue[%d].rules: %w", i, errs.ErrToolArgs)
		}
		for j, rule := range voice.Rules {
			if strings.TrimSpace(rule) == "" {
				return fmt.Errorf("style_rules.dialogue[%d].rules[%d] trống: %w", i, j, errs.ErrToolArgs)
			}
		}
	}
	return nil
}
