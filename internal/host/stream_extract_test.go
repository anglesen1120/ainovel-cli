package host

import (
	"strings"
	"testing"
)

// feedAll nạp một lần, trả về đầu ra tích lũy.
func feedAll(t *testing.T, tool, input string) string {
	t.Helper()
	e := newToolExtractor(tool)
	if e == nil {
		t.Fatalf("no extractor for tool %q", tool)
	}
	return e.Feed(input)
}

// feedChunked nạp theo từng mảnh với số byte chỉ định, xác minh kết quả luồng khớp với nạp một lần.
func feedChunked(t *testing.T, tool, input string, chunk int) string {
	t.Helper()
	e := newToolExtractor(tool)
	if e == nil {
		t.Fatalf("no extractor for tool %q", tool)
	}
	var b strings.Builder
	for i := 0; i < len(input); i += chunk {
		end := min(i+chunk, len(input))
		b.WriteString(e.Feed(input[i:end]))
	}
	return b.String()
}

func mustContain(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("expected substring %q in:\n---\n%s\n---", want, got)
	}
}

func mustNotContain(t *testing.T, got, want string) {
	t.Helper()
	if strings.Contains(got, want) {
		t.Errorf("unexpected substring %q in:\n---\n%s\n---", want, got)
	}
}

// ── Mau chung: obj phang ──

func TestExtract_PlanChapter(t *testing.T) {
	in := `{"chapter":1,"title":"Khế bán thân","goal":"Thiết lập nền tảng mỏ","conflict":"Nợ của cha","hook":"Mỏ xám","emotion_arc":"Bị đè nén"}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "✻ Lập kế hoạch")
	mustContain(t, out, "chapter: 1")
	mustContain(t, out, "title: Khế bán thân")
	mustContain(t, out, "goal: Thiết lập nền tảng mỏ")
	mustContain(t, out, "conflict: Nợ của cha")
	mustContain(t, out, "hook: Mỏ xám")
	mustContain(t, out, "emotion_arc: Bị đè nén")
}

// ── Mau chung: obj long nhau + mang ──

func TestExtract_FoundationCharacters(t *testing.T) {
	in := `{"type":"characters","scale":"long","content":[` +
		`{"name":"Thẩm Lệ","role":"nhân vật chính","aliases":["Mạch Xám","Thẩm Bảy"],"description":"Thiếu niên vùng biên hoang.","traits":["kiềm chế","đa nghi"]},` +
		`{"name":"Cố Tiểu Đăng","role":"nhân vật phụ quan trọng","description":"Bé gái thử thuốc trong hiệu thuốc."}` +
		`]}`
	out := feedAll(t, "save_foundation", in)
	mustContain(t, out, "✻ Thiết lập")
	mustContain(t, out, "type: characters")
	mustContain(t, out, "scale: long")
	// Ket xuat chung: hien thi tat ca truong, bao gom aliases / traits tung bi bo qua boi whitelist
	mustContain(t, out, "name: Thẩm Lệ")
	mustContain(t, out, "role: nhân vật chính")
	mustContain(t, out, "aliases:")
	mustContain(t, out, "- Mạch Xám")
	mustContain(t, out, "- Thẩm Bảy")
	mustContain(t, out, "description: Thiếu niên vùng biên hoang.")
	mustContain(t, out, "traits:")
	mustContain(t, out, "- kiềm chế")
	mustContain(t, out, "- đa nghi")
	mustContain(t, out, "name: Cố Tiểu Đăng")
	mustContain(t, out, "role: nhân vật phụ quan trọng")
}

func TestExtract_FoundationLayeredOutline(t *testing.T) {
	in := `{"type":"layered_outline","content":[` +
		`{"index":1,"title":"Lửa mỏ le lói","arcs":[` +
		`{"index":1,"title":"Phu mỏ Vảy Đen","goal":"cầu sống","chapters":[` +
		`{"chapter":1,"title":"Khế bán thân","core_event":"Bị bán vào mỏ."}` +
		`]}]}]}`
	out := feedAll(t, "save_foundation", in)
	mustContain(t, out, "type: layered_outline")
	// Quyen
	mustContain(t, out, "index: 1")
	mustContain(t, out, "title: Lửa mỏ le lói")
	// Arc
	mustContain(t, out, "title: Phu mỏ Vảy Đen")
	mustContain(t, out, "goal: cầu sống")
	// Chuong
	mustContain(t, out, "chapter: 1")
	mustContain(t, out, "title: Khế bán thân")
	mustContain(t, out, "core_event: Bị bán vào mỏ.")
	// Thut le long nhau the hien cap bac
	mustContain(t, out, "arcs:\n")
	mustContain(t, out, "chapters:\n")
}

func TestExtract_FoundationUpdateCompass(t *testing.T) {
	in := `{"type":"update_compass","content":{"ending_direction":"một mình phi thăng vs cắt đứt huyết tế","open_threads":["chìa khóa Mạch Xám","sổ vé người sống"],"estimated_scale":"5-6 quyển"}}`
	out := feedAll(t, "save_foundation", in)
	mustContain(t, out, "type: update_compass")
	mustContain(t, out, "ending_direction: một mình phi thăng vs cắt đứt huyết tế")
	mustContain(t, out, "estimated_scale: 5-6 quyển")
	mustContain(t, out, "open_threads:")
	mustContain(t, out, "- chìa khóa Mạch Xám")
	mustContain(t, out, "- sổ vé người sống")
}

// ── save_review: gom obj trong mang + mang so ──

func TestExtract_SaveReview(t *testing.T) {
	in := `{"chapter":3,"scope":"chapter","verdict":"polish","summary":"Nhịp hơi chậm.","dimensions":[{"dimension":"hook","score":55,"verdict":"fail"}],"issues":[{"type":"hook","severity":"error","description":"Cuối chương thiếu móc câu."}],"affected_chapters":[3,4]}`
	out := feedAll(t, "save_review", in)
	mustContain(t, out, "✻ Duyệt")
	mustContain(t, out, "verdict: polish")
	mustContain(t, out, "summary: Nhịp hơi chậm.")
	mustContain(t, out, "dimension: hook")
	mustContain(t, out, "score: 55")
	mustContain(t, out, "verdict: fail")
	mustContain(t, out, "type: hook")
	mustContain(t, out, "severity: error")
	mustContain(t, out, "description: Cuối chương thiếu móc câu.")
	mustContain(t, out, "- 3")
	mustContain(t, out, "- 4")
}

// ── commit_chapter: long nhau phuc tap ──

func TestExtract_CommitChapter(t *testing.T) {
	in := `{"chapter":1,"summary":"Bị bán vào mỏ.","characters":["Thẩm Lệ","mẹ"],"key_events":["ký khế bán thân"],"foreshadow_updates":[{"id":"f1","action":"plant","description":"Mỏ xám nóng lên."}],"state_changes":[{"entity":"Thẩm Lệ","field":"thân phận","old_value":"thiếu niên hái thuốc","new_value":"tạp dịch mỏ"}]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "✻ Nộp chương")
	mustContain(t, out, "summary: Bị bán vào mỏ.")
	mustContain(t, out, "- Thẩm Lệ")
	mustContain(t, out, "- mẹ")
	mustContain(t, out, "- ký khế bán thân")
	mustContain(t, out, "id: f1")
	mustContain(t, out, "action: plant")
	mustContain(t, out, "description: Mỏ xám nóng lên.")
	mustContain(t, out, "entity: Thẩm Lệ")
	mustContain(t, out, "field: thân phận")
	mustContain(t, out, "old_value: thiếu niên hái thuốc")
	mustContain(t, out, "new_value: tạp dịch mỏ")
}

// ── edit_chapter: mau chung + string nhieu dong ──

func TestExtract_EditChapter(t *testing.T) {
	in := `{"chapter":24,"old_string":"Thẩm Lệ cúi đầu im lặng.\nCậu siết chặt nắm tay.","new_string":"Thẩm Lệ không ngẩng đầu, yết hầu khẽ động.\nCác khớp ngón tay siết đến trắng bệch.","replace_all":false}`
	out := feedAll(t, "edit_chapter", in)
	mustContain(t, out, "✻ Mài giũa")
	mustContain(t, out, "chapter: 24")
	mustContain(t, out, "old_string: Thẩm Lệ cúi đầu im lặng.\nCậu siết chặt nắm tay.")
	mustContain(t, out, "new_string: Thẩm Lệ không ngẩng đầu, yết hầu khẽ động.\nCác khớp ngón tay siết đến trắng bệch.")
	mustContain(t, out, "replace_all: false")
}

// ── Cong cu doc: args mat do thong tin thap nhung header + truong chinh van phai thay duoc ──

func TestExtract_ReadChapter(t *testing.T) {
	in := `{"chapter":234,"source":"final"}`
	out := feedAll(t, "read_chapter", in)
	mustContain(t, out, "✻ Đọc chương")
	mustContain(t, out, "chapter: 234")
	mustContain(t, out, "source: final")
}

func TestExtract_CheckConsistency(t *testing.T) {
	out := feedAll(t, "check_consistency", `{"chapter":234}`)
	mustContain(t, out, "✻ Kiểm tra nhất quán")
	mustContain(t, out, "chapter: 234")
}

// Fallback args rong: khi architect goi novel_context ma khong truyen tham so thi args la {},
// khong duoc im lang hoan toan, it nhat phai xuat header de nguoi dung cam nhan duoc loi goi.
func TestExtract_NovelContextEmptyArgs(t *testing.T) {
	out := feedAll(t, "novel_context", `{}`)
	mustContain(t, out, "✻ Truy vấn ngữ cảnh")
}

func TestExtract_NovelContextWithChapter(t *testing.T) {
	out := feedAll(t, "novel_context", `{"chapter":234}`)
	mustContain(t, out, "✻ Truy vấn ngữ cảnh")
	mustContain(t, out, "chapter: 234")
}

// ── Che do luong tho ──

func TestExtract_DraftChapterRawMarkdown(t *testing.T) {
	in := `{"chapter":1,"content":"# Chương một\n\nThẩm Lệ đứng ở cửa mỏ.\n"}`
	out := feedAll(t, "draft_chapter", in)
	// Luong tho: khong trang tri, khong co key prefix
	mustNotContain(t, out, "【")
	mustNotContain(t, out, "content:")
	mustNotContain(t, out, "chapter:")
	mustContain(t, out, "# Chương một")
	mustContain(t, out, "Thẩm Lệ đứng ở cửa mỏ.")
}

func TestExtract_DraftChapterIgnoresOtherFields(t *testing.T) {
	// Cac truong ngoai content phai bi bo qua im lang, khong lam nhiem dau ra
	in := `{"chapter":7,"summary":"meta","content":"chính văn","extra_array":[1,2,3]}`
	out := feedAll(t, "draft_chapter", in)
	mustContain(t, out, "chính văn")
	mustNotContain(t, out, "meta")
	mustNotContain(t, out, "summary")
	mustNotContain(t, out, "7")
	mustNotContain(t, out, "1")
}

// ── Bat bien hanh vi ──

func TestExtract_UnknownTool(t *testing.T) {
	if e := newToolExtractor("nonexistent_tool"); e != nil {
		t.Errorf("expected nil for unknown tool")
	}
}

func TestExtract_DoneAfterClose(t *testing.T) {
	e := newToolExtractor("plan_chapter")
	e.Feed(`{"title":"x"}`)
	if !e.Done() {
		t.Error("expected Done after closing brace")
	}
}

// ── Bat bien phan manh luong ──

// Cung mot dau vao duoc chia theo 1/3/7/13 byte, dau ra phai hoan toan giong voi nap mot lan.
func TestExtract_ChunkedEqualsWhole(t *testing.T) {
	cases := []struct {
		tool  string
		input string
	}{
		{"plan_chapter", `{"title":"Khế bán thân","goal":"mục tiêu","conflict":"nợ của cha","hook":"mỏ xám","emotion_arc":"bị đè nén"}`},
		{"save_foundation", `{"type":"characters","content":[{"name":"Thẩm Lệ","role":"nhân vật chính","aliases":["Mạch Xám","Thẩm Bảy"]}]}`},
		{"save_foundation", `{"type":"layered_outline","content":[{"index":1,"title":"lửa mỏ","arcs":[{"index":1,"title":"phu mỏ","goal":"cầu sống","chapters":[{"chapter":1,"title":"Khế bán thân"}]}]}]}`},
		{"save_review", `{"verdict":"accept","summary":"good","dimensions":[{"dimension":"hook","score":85,"verdict":"pass"}],"issues":[]}`},
		{"draft_chapter", `{"chapter":1,"content":"# Chương một\n\nChính văn.\n"}`},
	}
	for _, tc := range cases {
		whole := feedAll(t, tc.tool, tc.input)
		for _, chunk := range []int{1, 3, 7, 13} {
			got := feedChunked(t, tc.tool, tc.input, chunk)
			if got != whole {
				t.Errorf("tool=%s chunk=%d differs from whole\n--- whole ---\n%s\n--- chunked ---\n%s", tc.tool, chunk, whole, got)
			}
		}
	}
}

// ── Escape va Unicode ──

func TestExtract_EscapeSequences(t *testing.T) {
	in := `{"goal":"dòng1\ndòng2 \"dấu nháy\" \\dấu gạch chéo ngược chữ Việt"}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "dòng1\ndòng2")
	mustContain(t, out, `"dấu nháy"`)
	mustContain(t, out, `\dấu gạch chéo ngược`)
	mustContain(t, out, "chữ Việt")
}

func TestExtract_UnicodeEscape(t *testing.T) {
	// Kiem tra chuoi Unicode tieng Viet
	in := `{"goal":"tiếng Việt"}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "tiếng Việt")
}

// ── Container rong / cau truc don gian ──

func TestExtract_EmptyArrays(t *testing.T) {
	in := `{"key_events":[],"characters":["Thẩm Lệ"]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "key_events:")
	mustContain(t, out, "characters:")
	mustContain(t, out, "- Thẩm Lệ")
}

func TestExtract_BoolAndNull(t *testing.T) {
	in := `{"foreshadow_updates":[{"id":"f1","action":"plant","description":null}],"chapter":1,"summary":"x","characters":["a"],"key_events":["b"]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "id: f1")
	mustContain(t, out, "action: plant")
	mustContain(t, out, "description: null")
}

// ── Tinh huong goc canh: mang long mang, long nhau sau ──

func TestExtract_NestedArrays(t *testing.T) {
	// affected_chapters la mang int; o day doi thanh mang long mang de xac minh
	in := `{"summary":"x","key_events":[],"characters":["a"],"foreshadow_updates":[],"relationship_changes":[]}`
	out := feedAll(t, "commit_chapter", in)
	mustContain(t, out, "summary: x")
	mustContain(t, out, "key_events:")
	mustContain(t, out, "- a")
}

func TestExtract_DeeplyNested(t *testing.T) {
	in := `{"a":{"b":{"c":{"d":"deep"}}}}`
	e := newToolExtractor("plan_chapter")
	out := e.Feed(in)
	mustContain(t, out, "a:")
	mustContain(t, out, "b:")
	mustContain(t, out, "c:")
	mustContain(t, out, "d: deep")
	if !e.Done() {
		t.Error("expected Done after final closing brace")
	}
}

// ── chunk cat giua ky tu nhieu byte utf-8 ──

func TestExtract_ChunkSplitInUTF8(t *testing.T) {
	// "ệ" la 3 byte trong UTF-8. Dat kich thuoc lat cat la 1 de dam bao tung byte duoc nap rieng.
	in := `{"goal":"tiếng Việt thử nghiệm"}`
	whole := feedAll(t, "plan_chapter", in)
	chunked := feedChunked(t, "plan_chapter", in, 1)
	if whole != chunked {
		t.Errorf("byte-by-byte chunked output differs from whole:\n--- whole ---\n%s\n--- chunked ---\n%s", whole, chunked)
	}
	mustContain(t, chunked, "tiếng Việt thử nghiệm")
}

// ── Che do luong tho: key trung ten trong obj long nhau khong duoc bi nhan nham ──

func TestExtract_NakedKeyOnlyTopLevel(t *testing.T) {
	// "content" xuat hien o hai noi: trong doi tuong long nhau + cap dinh. Chi cai o cap dinh moi duoc day ra luong.
	in := `{"meta":{"content":"nội dung lồng nhau không nên xuất ra"},"content":"nội dung cấp đỉnh nên xuất ra"}`
	out := feedAll(t, "draft_chapter", in)
	mustContain(t, out, "nội dung cấp đỉnh nên xuất ra")
	mustNotContain(t, out, "nội dung lồng nhau không nên xuất ra")
}

// ── Che do luong tho: khi content khong phai string thi bo qua tat ca ──

func TestExtract_NakedKeyNonStringValue(t *testing.T) {
	// content bi viet nham thanh doi tuong (khong nen xay ra nhung phai dung nap)
	in := `{"content":{"unexpected":true}}`
	out := feedAll(t, "draft_chapter", in)
	if out != "" {
		t.Errorf("expected empty output, got: %q", out)
	}
}

// ── Sau khi cap dinh dong, Feed nua khong tao dau ra ──

func TestExtract_FeedAfterDone(t *testing.T) {
	e := newToolExtractor("plan_chapter")
	e.Feed(`{"title":"x"}`)
	if !e.Done() {
		t.Fatal("expected Done")
	}
	if got := e.Feed(`junk`); got != "" {
		t.Errorf("expected empty output after Done, got: %q", got)
	}
}

// ── Chunk rong / input rong ──

func TestExtract_EmptyFeed(t *testing.T) {
	e := newToolExtractor("plan_chapter")
	if got := e.Feed(""); got != "" {
		t.Errorf("expected empty output for empty feed, got: %q", got)
	}
	if e.Done() {
		t.Error("Done should be false before any input")
	}
}

// ── Mang truc tiep long mang (khong qua obj) ──

func TestExtract_ArrayOfArrays(t *testing.T) {
	in := `{"matrix":[[1,2],[3,4]]}`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "matrix:")
	mustContain(t, out, "- 1")
	mustContain(t, out, "- 2")
	mustContain(t, out, "- 3")
	mustContain(t, out, "- 4")
}

// ── Sau number co khoang trang roi moi den dau phan cach ──

func TestExtract_NumberWithTrailingSpace(t *testing.T) {
	// "chapter": 1 ,  ← co them khoang trang truoc va sau so
	in := `{ "chapter" : 1 , "title" : "x" }`
	out := feedAll(t, "plan_chapter", in)
	mustContain(t, out, "chapter: 1")
	mustContain(t, out, "title: x")
}
