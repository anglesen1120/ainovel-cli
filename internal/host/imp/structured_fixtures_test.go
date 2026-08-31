package imp

import "encoding/json"

func boundaryFixture(unitID, anchor, kind, title string) map[string]any {
	var anchorValue, titleValue any
	if anchor != "" {
		anchorValue = anchor
	}
	if title != "" {
		titleValue = title
	}
	return map[string]any{
		"unit_id": unitID, "anchor": anchorValue, "kind": kind, "title": titleValue,
		"uncertain": false, "reason": nil,
	}
}

func boundariesJSON(boundaries ...map[string]any) string {
	data, err := json.Marshal(map[string]any{"boundaries": boundaries})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func rangeDigestJSON(start, end int, plot string) string {
	data, err := json.Marshal(map[string]any{
		"start_chapter":    start,
		"end_chapter":      end,
		"plot":             plot,
		"characters":       []string{},
		"world_facts":      []string{},
		"opened_threads":   []string{},
		"resolved_threads": []string{},
	})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func synthesisFixtureJSON(endChapter int, status string) string {
	data, err := json.Marshal(map[string]any{
		"title":    "Sách thử nghiệm",
		"synopsis": "A lên đường, tìm kiếm câu trả lời để thay đổi số phận.",
		"premise":  "# Tiền đề câu chuyện\nTiền đề",
		"characters": []any{map[string]any{
			"name": "A", "aliases": []string{}, "role": "protagonist", "description": "d",
			"arc": "a", "traits": []string{"kiên cường"}, "tier": nil,
		}},
		"world_rules": []any{},
		"structure": []any{map[string]any{
			"title": "Quyển một", "theme": "Chủ đề", "arcs": []any{map[string]any{
				"title": "Cung một", "goal": "Mục tiêu", "start_chapter": 1, "end_chapter": endChapter,
			}},
		}},
		"compass": map[string]any{
			"ending_direction": "Kết cục", "open_threads": []string{},
			"estimated_scale": nil, "last_updated": nil,
		},
		"planning_tier": "short",
		"story_status":  status,
		"status_reason": "Dựa trên nội dung chính để nhận định",
	})
	if err != nil {
		panic(err)
	}
	return string(data)
}
