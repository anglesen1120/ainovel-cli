package domain

import (
	"reflect"
	"testing"
)

func TestRestoreOwnPlants(t *testing.T) {
	cases := []struct {
		name string
		prev []ForeshadowUpdate
		next []ForeshadowUpdate
		want []ForeshadowUpdate
	}{
		{
			name: "Việc viết lại biến plant của chương này thành advance: plant được bổ sung trở lại đầu hàng đợi",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "ảnh cũ của kênh xả lũ"}},
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "advance"}},
			want: []ForeshadowUpdate{
				{ID: "f_photo", Action: "plant", Description: "ảnh cũ của kênh xả lũ"},
				{ID: "f_photo", Action: "advance"},
			},
		},
		{
			name: "Việc viết lại làm mất toàn bộ plant của chương này: vẫn bổ sung trở lại",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "ảnh cũ của kênh xả lũ"}},
			next: nil,
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "ảnh cũ của kênh xả lũ"}},
		},
		{
			name: "Bản ghi mới đã tự khai báo plant: không bổ sung trùng lặp",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "mô tả cũ"}},
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "mô tả mới"}},
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "mô tả mới"}},
		},
		{
			name: "Bản ghi cũ chỉ có advance/resolve: không có plant để bổ sung",
			prev: []ForeshadowUpdate{{ID: "f_photo", Action: "advance"}, {ID: "f_key", Action: "resolve"}},
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "resolve"}},
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "resolve"}},
		},
		{
			name: "Nhiều plant được bổ sung lại theo thứ tự gốc, và đều đứng trước advance",
			prev: []ForeshadowUpdate{
				{ID: "f_a", Action: "plant", Description: "A"},
				{ID: "f_b", Action: "plant", Description: "B"},
			},
			next: []ForeshadowUpdate{{ID: "f_a", Action: "advance"}},
			want: []ForeshadowUpdate{
				{ID: "f_a", Action: "plant", Description: "A"},
				{ID: "f_b", Action: "plant", Description: "B"},
				{ID: "f_a", Action: "advance"},
			},
		},
		{
			name: "Lần commit đầu tiên không có bản ghi cũ: trả về nguyên bản",
			prev: nil,
			next: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "ảnh cũ của kênh xả lũ"}},
			want: []ForeshadowUpdate{{ID: "f_photo", Action: "plant", Description: "ảnh cũ của kênh xả lũ"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RestoreOwnPlants(tc.prev, tc.next)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("RestoreOwnPlants() = %+v, muốn %+v", got, tc.want)
			}
		})
	}
}
