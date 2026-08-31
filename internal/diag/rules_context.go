package diag

import (
	"fmt"
	"strings"
)

// GhostCharacter phát hiện nhân vật core/important vắng mặt quá lâu.
func GhostCharacter(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Characters) == 0 || len(snap.Summaries) == 0 {
		return nil
	}
	completed := snap.CompletedCount()
	if completed < 5 {
		return nil
	}

	// Tính số chương cuối cùng xuất hiện của từng nhân vật
	lastSeen := make(map[string]int)
	for ch, s := range snap.Summaries {
		for _, name := range s.Characters {
			if ch > lastSeen[name] {
				lastSeen[name] = ch
			}
		}
	}

	threshold := completed / 3
	if threshold < 5 {
		threshold = 5
	}
	latest := snap.LatestCompleted()

	var ghosts []string
	for _, c := range snap.Characters {
		if c.Tier != "core" && c.Tier != "important" {
			continue
		}
		seen, ok := lastSeen[c.Name]
		if !ok {
			// Cũng kiểm tra bí danh
			for _, alias := range c.Aliases {
				if s, exists := lastSeen[alias]; exists && s > seen {
					seen = s
					ok = true
				}
			}
		}
		gap := latest - seen
		if !ok {
			ghosts = append(ghosts, fmt.Sprintf("%s(chưa từng xuất hiện trong tóm tắt)", c.Name))
		} else if gap > threshold {
			ghosts = append(ghosts, fmt.Sprintf("%s(xuất hiện lần cuối ở ch%d, đã vắng mặt %d chương)", c.Name, seen, gap))
		}
	}
	if len(ghosts) == 0 {
		return nil
	}
	return []Finding{{
		Rule:       "GhostCharacter",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.characters",
		Title:      fmt.Sprintf("Nhân vật biến mất: %d nhân vật cốt lõi vắng mặt kéo dài", len(ghosts)),
		Evidence:   strings.Join(ghosts, "; "),
		Suggestion: "Writer có thể đã mất dấu nhân vật này. Hãy cân nhắc gửi chỉ thị can thiệp trực tiếp trong ô nhập để đưa nhân vật trở lại, hoặc hạ cấp tier của họ trong characters.json.",
	}}
}

// TimelineGaps phát hiện các chương đã hoàn thành nhưng thiếu sự kiện timeline.
func TimelineGaps(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Progress.CompletedChapters) == 0 {
		return nil
	}
	if len(snap.Timeline) == 0 && snap.CompletedCount() > 0 {
		return []Finding{{
			Rule:       "TimelineGaps",
			Category:   CatContext,
			Severity:   SevInfo,
			Confidence: ConfMedium,
			AutoLevel:  AutoNone,
			Target:     "context.timeline",
			Title:      "Timeline trống",
			Evidence:   fmt.Sprintf("completed=%d, timeline_events=0", snap.CompletedCount()),
			Suggestion: "Việc trích xuất timeline của commit_chapter có thể chưa hoạt động. Hãy kiểm tra đầu ra của Writer xem có chứa trường timeline hay không.",
		}}
	}

	// Tạo ánh xạ chương→sự kiện
	chaptersWithEvents := make(map[int]bool)
	for _, e := range snap.Timeline {
		chaptersWithEvents[e.Chapter] = true
	}

	var missing []int
	for _, ch := range snap.Progress.CompletedChapters {
		if !chaptersWithEvents[ch] {
			missing = append(missing, ch)
		}
	}
	// Cho phép thiếu ít (một số chương chuyển tiếp có thể thực sự không có sự kiện lớn)
	if len(missing) == 0 || float64(len(missing))/float64(snap.CompletedCount()) < ThresholdTimelineGapRate {
		return nil
	}
	return []Finding{{
		Rule:       "TimelineGaps",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfMedium,
		AutoLevel:  AutoNone,
		Target:     "context.timeline",
		Title:      fmt.Sprintf("Khoảng trống timeline: %d chương không có bản ghi sự kiện", len(missing)),
		Evidence:   fmt.Sprintf("missing=[%s]", intsToStr(missing)),
		Suggestion: "Việc trích xuất timeline của commit_chapter có thể đã hỏng một phần. Hãy kiểm tra định dạng trường timeline trong đầu ra của Writer.",
	}}
}

// RelationshipStagnation phát hiện dữ liệu quan hệ ngừng cập nhật.
func RelationshipStagnation(snap *Snapshot) []Finding {
	if snap.Progress == nil || len(snap.Relationships) == 0 {
		return nil
	}
	completed := snap.CompletedCount()
	if completed < 6 {
		return nil
	}

	// Tìm chương mới nhất của dữ liệu quan hệ
	latestRelCh := 0
	for _, r := range snap.Relationships {
		if r.Chapter > latestRelCh {
			latestRelCh = r.Chapter
		}
	}

	// Nếu dữ liệu quan hệ mới nhất vẫn nằm trong 1/3 đầu, xem là trì trệ
	cutoff := snap.LatestCompleted() - completed/3
	if latestRelCh >= cutoff {
		return nil
	}
	return []Finding{{
		Rule:       "RelationshipStagnation",
		Category:   CatContext,
		Severity:   SevInfo,
		Confidence: ConfLow,
		AutoLevel:  AutoNone,
		Target:     "context.relationships",
		Title:      fmt.Sprintf("Dữ liệu quan hệ trì trệ: cập nhật mới nhất ở chương %d", latestRelCh),
		Evidence:   fmt.Sprintf("relationship_entries=%d, latest_update=ch%d, latest_completed=ch%d", len(snap.Relationships), latestRelCh, snap.LatestCompleted()),
		Suggestion: "Phần cập nhật quan hệ của commit_chapter có thể đã ngừng hoạt động, hoặc quan hệ trong truyện thực sự không đổi. Hãy kiểm tra trường relationships trong đầu ra của Writer.",
	}}
}
