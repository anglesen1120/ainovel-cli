package tools

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/stylestat"
)

func TestStyleStatsIndexAppendRewriteAndRemove(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}

	chapters := map[int]string{
		1: "# Phong khởi\nĐêm khuya, anh không chần chừ mà là sợ hãi.\nĐời này chưa từng đi xa, xin hãy thay tôi nhìn núi biển phía xa.\nAnh đi rồi.",
		2: "# Vân dâng\nSáng sớm, cô im lặng vài nhịp.\nĐời này chưa từng đi xa, xin hãy thay tôi nhìn núi biển phía xa.\nTrời sáng rồi.",
		3: "# Lôi động\nTrong mắt Lục Cửu Uyên lóe lên vẻ lạnh.\nĐời này chưa từng đi xa, xin hãy thay tôi nhìn núi biển phía xa.\nKhông ai đáp lời.",
		4: "# Ám triều\nMọi người cảm thấy giông gió sắp tới.\nCuối phố dài vang lên tiếng chuông.\nCửa mở rồi.",
		5: "# Hồi trình\nNhư thể một giấc mộng cũ đè lên đỉnh núi.\nMột cảm giác lạnh khó tả lan ra.\nĐèn tắt rồi.",
		6: "# Sơn môn\nTrong lòng anh siết lại, nhưng anh không quay đầu.\nCâu chuyện vẫn phải tiếp tục tiến về phía trước.",
	}
	for chapter, text := range chapters {
		if err := st.Drafts.SaveFinalChapter(chapter, text); err != nil {
			t.Fatal(err)
		}
	}

	titles := []string{"Chương một Phong khởi", "Vân dâng", "Chương 3 Lôi động", "Ám triều", "Hồi trình", "Sơn môn"}
	stopwords := []string{"Lục Cửu Uyên"}
	index := NewStyleStatsIndex(st)
	completed := []int{1, 2, 3, 4, 5, 6}
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)

	chapters[7] = "# Sương tan\nNhư tỉnh khỏi giấc mộng cũ, anh không nói gì.\nGió ngừng rồi."
	if err := st.Drafts.SaveFinalChapter(7, chapters[7]); err != nil {
		t.Fatal(err)
	}
	index.ChapterCommitted(7, chapters[7])
	completed = append(completed, 7)
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)

	chapters[2] = "# Vân dâng\nKhi bình minh lên, cô thấy nặng lòng.\nCâu dài đã sửa chỉ xuất hiện trong chương này, không nên bị lặp sang chương khác.\nGió ngừng rồi."
	if err := st.Drafts.SaveFinalChapter(2, chapters[2]); err != nil {
		t.Fatal(err)
	}
	index.ChapterCommitted(2, chapters[2])
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)

	delete(chapters, 4)
	completed = []int{1, 2, 3, 5, 6, 7}
	assertStyleStatsIndexMatchesCompute(t, index, chapters, completed, titles, stopwords)
}

func TestStyleStatsIndexSurfacesMissingCompletedChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	_, err := NewStyleStatsIndex(st).Snapshot([]int{1}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Chương 1 đã được đánh dấu hoàn thành nhưng không có bản cuối") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStyleStatsDependencyIsRequired(t *testing.T) {
	st := store.NewStore(t.TempDir())
	tests := []struct {
		name string
		new  func()
	}{
		{
			name: "context",
			new: func() {
				NewContextTool(st, References{}, "default", nil)
			},
		},
		{
			name: "commit",
			new: func() {
				NewCommitChapterTool(st, nil)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("nil StyleStatsIndex must fail immediately")
				}
			}()
			tt.new()
		})
	}
}

func assertStyleStatsIndexMatchesCompute(
	t *testing.T,
	index *StyleStatsIndex,
	chapters map[int]string,
	completed []int,
	titles, stopwords []string,
) {
	t.Helper()
	ids := append([]int(nil), completed...)
	sort.Ints(ids)
	texts := make([]string, 0, len(ids))
	for _, chapter := range ids {
		texts = append(texts, chapters[chapter])
	}
	want := stylestat.Compute(stylestat.Input{
		Chapters:  texts,
		Titles:    titles,
		Stopwords: stopwords,
	})
	got, err := index.Snapshot(completed, titles, stopwords)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("index mismatch\n got: %+v\nwant: %+v", got, want)
	}
}
