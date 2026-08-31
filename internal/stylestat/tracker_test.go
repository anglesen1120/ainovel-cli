package stylestat

import (
	"reflect"
	"sort"
	"sync"
	"testing"
)

func TestTrackerMatchesComputeAcrossUpdates(t *testing.T) {
	allChapters := map[int]string{
		1: chapterWith("Trong đêm, anh không phải do dự mà là sợ hãi. Gió trên đỉnh Núi Xanh dần gấp.\nĐời này chưa thể đi xa, mong con thay ta ngắm núi biển phương xa.\nAnh rời đi."),
		2: chapterWith("Sáng sớm, cô im lặng vài nhịp thở, biển mây trên đỉnh Núi Xanh cuồn cuộn.\nĐời này chưa thể đi xa, mong con thay ta ngắm núi biển phương xa.\nTrời dần sáng."),
		3: chapterWith("Lục Cửu Uyên đứng trên đỉnh Núi Xanh, ánh mắt thoáng qua hơi lạnh.\nĐời này chưa thể đi xa, mong con thay ta ngắm núi biển phương xa.\nKhông ai đáp."),
		4: chapterWith("Mọi người nhìn về đỉnh Núi Xanh, cảm thấy giông bão sắp tới.\nCuối phố dài vang tiếng chuông.\nCửa mở."),
		5: chapterWith("Như thể một giấc mộng cũ đè lên đỉnh Núi Xanh.\nNỗi lạnh khó nói thành lời lan theo bậc đá.\nĐèn tắt."),
		6: chapterWith("Tim anh thắt lại nhưng không quay đầu.\nĐỉnh Núi Xanh vẫn chìm trong mây.\nCâu chuyện vẫn phải tiếp tục tiến về phía trước."),
	}
	titles := []string{"Chương 1 Gió nổi", "Mây cuộn", "chapter 3 Sấm động", "Sóng ngầm", "Đường về", "Cổng núi"}
	stopwords := []string{"Lục Cửu Uyên"}

	tracker := NewTracker()
	chapters := make(map[int]string)
	for chapter := 1; chapter <= 6; chapter++ {
		chapters[chapter] = allChapters[chapter]
		tracker.Upsert(chapter, allChapters[chapter])
		assertTrackerMatchesCompute(t, tracker, chapters, titles, stopwords)
	}

	chapters[2] = chapterWith("Lúc bình minh, tim cô chùng xuống, hệt như tỉnh khỏi mộng cũ.\nCâu dài sau khi viết lại chỉ xuất hiện trong chương này, không nên thành lặp lại qua chương.\nGió ngừng.")
	tracker.Upsert(2, chapters[2])
	assertTrackerMatchesCompute(t, tracker, chapters, titles, stopwords)

	delete(chapters, 4)
	tracker.Remove(4)
	assertTrackerMatchesCompute(t, tracker, chapters, titles, stopwords)
}

func TestTrackerSnapshotReturnsIndependentCopy(t *testing.T) {
	tracker := NewTracker()
	for chapter := 1; chapter <= 5; chapter++ {
		tracker.Upsert(chapter, chapterWith("Anh không phải lùi bước mà là đang chờ đợi."))
	}

	first := tracker.Snapshot(nil, nil)
	if first == nil || len(first.Patterns) == 0 {
		t.Fatalf("unexpected first snapshot: %+v", first)
	}
	first.Patterns[0].Total = 999

	second := tracker.Snapshot(nil, nil)
	if second.Patterns[0].Total == 999 {
		t.Fatal("cached snapshot was mutated through caller result")
	}
}

func TestTrackerConcurrentSnapshotAndUpdate(t *testing.T) {
	tracker := NewTracker()
	for chapter := 1; chapter <= 8; chapter++ {
		tracker.Upsert(chapter, chapterWith("Anh không phải lùi bước mà là đang chờ đợi."))
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if worker%2 == 0 {
					tracker.Upsert(8, chapterWith("Anh không phải lùi bước mà là đang chờ đợi."))
				} else {
					_ = tracker.Snapshot([]string{"Chương 1"}, []string{"Lâm Nghiên"})
				}
			}
		}(worker)
	}
	wg.Wait()
}

func assertTrackerMatchesCompute(
	t *testing.T,
	tracker *Tracker,
	chapters map[int]string,
	titles, stopwords []string,
) {
	t.Helper()
	ids := make([]int, 0, len(chapters))
	for chapter := range chapters {
		ids = append(ids, chapter)
	}
	sort.Ints(ids)
	texts := make([]string, 0, len(ids))
	for _, chapter := range ids {
		texts = append(texts, chapters[chapter])
	}

	want := Compute(Input{Chapters: texts, Titles: titles, Stopwords: stopwords})
	got := tracker.Snapshot(titles, stopwords)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tracker mismatch\n got: %+v\nwant: %+v", got, want)
	}
}
