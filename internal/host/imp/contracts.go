package imp

import (
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

func nullableString(description string) map[string]any {
	return llmcontract.Nullable(schema.String(description))
}

func stringList(description string) map[string]any {
	return schema.Array(description, schema.String(description))
}

var segmentContract = llmcontract.Contract{
	Name:        "import_segment",
	Description: "Nhận diện ranh giới chương, quyển/phần và văn bản phụ trong văn bản nhập",
	Schema: schema.Object(
		schema.Property("boundaries", schema.Array("Các ranh giới được sắp theo thứ tự nguyên văn", schema.Object(
			schema.Property("unit_id", schema.String("unit id trong khoảng owned")).Required(),
			schema.Property("anchor", nullableString("Đoạn định vị trong nguyên văn khi cùng một unit có nhiều ranh giới; nếu không thì null")).Required(),
			schema.Property("kind", schema.Enum("Loại ranh giới", kindChapter, kindGroup, kindFrontMatter, kindBackMatter)).Required(),
			schema.Property("title", nullableString("Nguyên văn tiêu đề; khi không có tiêu đề thì null")).Required(),
			schema.Property("uncertain", schema.Bool("Có cần người dùng xác nhận hay không")).Required(),
			schema.Property("reason", nullableString("Lý do không chắc chắn; khi không cần giải thích thì null")).Required(),
		))).Required(),
	),
}

var analysisContract = llmcontract.Contract{
	Name:        "import_chapter_analysis",
	Description: "Trích xuất các sự kiện truyện có thể truy vết từ các chương liên tiếp",
	Schema: schema.Object(
		schema.Property("chapters", schema.Array("Sự kiện theo từng chương, khớp với thứ tự số chương đầu vào", chapterFactsSchema())).Required(),
	),
}

func chapterFactsSchema() map[string]any {
	characterEvidence := schema.Object(
		schema.Property("chapter", schema.Int("Chương chứa bằng chứng")).Required(),
		schema.Property("name", schema.String("Tên nhân vật")).Required(),
		schema.Property("note", nullableString("Sự kiện về nhân vật; nếu không có thì null")).Required(),
	)
	worldEvidence := schema.Object(
		schema.Property("chapter", schema.Int("Chương chứa bằng chứng")).Required(),
		schema.Property("category", nullableString("Loại sự kiện thế giới; khi không thể phân loại thì null")).Required(),
		schema.Property("fact", schema.String("Sự kiện thế giới được phần nội dung chính bộc lộ rõ ràng")).Required(),
	)
	timelineEvent := schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("time", schema.String("Thời gian trong truyện")).Required(),
		schema.Property("event", schema.String("Sự kiện")).Required(),
		schema.Property("characters", stringList("Nhân vật liên quan")).Required(),
	)
	foreshadow := schema.Object(
		schema.Property("id", schema.String("Tái sử dụng ID chi tiết gài trước trong ledger")).Required(),
		schema.Property("action", schema.Enum("Hành động với chi tiết gài trước", "plant", "advance", "resolve")).Required(),
		schema.Property("description", nullableString("Mô tả chi tiết gài trước khi plant; các trường hợp khác có thể là null")).Required(),
	)
	relationship := schema.Object(
		schema.Property("character_a", schema.String("Nhân vật A")).Required(),
		schema.Property("character_b", schema.String("Nhân vật B")).Required(),
		schema.Property("relation", schema.String("Thay đổi quan hệ")).Required(),
		schema.Property("chapter", schema.Int("Số chương")).Required(),
	)
	stateChange := schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("entity", schema.String("Nhân vật hoặc thực thể")).Required(),
		schema.Property("field", schema.String("Thuộc tính đã thay đổi")).Required(),
		schema.Property("old_value", nullableString("Trạng thái trước khi thay đổi; khi xuất hiện lần đầu thì null")).Required(),
		schema.Property("new_value", schema.String("Trạng thái sau khi thay đổi")).Required(),
		schema.Property("reason", nullableString("Lý do thay đổi; khi phần nội dung chính chưa giải thích thì null")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("title", schema.String("Tiêu đề chương")).Required(),
		schema.Property("summary", schema.String("Tóm tắt chương này")).Required(),
		schema.Property("key_events", stringList("Sự kiện then chốt")).Required(),
		schema.Property("core_event", schema.String("Một việc quan trọng nhất trong chương này")).Required(),
		schema.Property("hook", nullableString("Móc câu cuối chương; nếu không có thì null")).Required(),
		schema.Property("scenes", stringList("Chuỗi cảnh")).Required(),
		schema.Property("characters", stringList("Nhân vật xuất hiện")).Required(),
		schema.Property("character_evidence", schema.Array("Bằng chứng về nhân vật", characterEvidence)).Required(),
		schema.Property("world_evidence", schema.Array("Bằng chứng về sự kiện thế giới", worldEvidence)).Required(),
		schema.Property("timeline_events", schema.Array("Sự kiện trên dòng thời gian", timelineEvent)).Required(),
		schema.Property("foreshadow_updates", schema.Array("Phần tăng thêm của chi tiết gài trước", foreshadow)).Required(),
		schema.Property("relationship_changes", schema.Array("Thay đổi quan hệ", relationship)).Required(),
		schema.Property("state_changes", schema.Array("Thay đổi trạng thái", stateChange)).Required(),
		schema.Property("hook_type", schema.Enum("Loại móc câu cuối chương", domain.HookTypes()...)).Required(),
		schema.Property("dominant_strand", schema.Enum("Tuyến tự sự chủ đạo", domain.DominantStrands()...)).Required(),
	)
}

var rangeContract = llmcontract.Contract{
	Name:        "import_range_digest",
	Description: "Tóm lược tình tiết và sự kiện của một khoảng chương liên tiếp",
	Schema: schema.Object(
		schema.Property("start_chapter", schema.Int("Chương đầu của khoảng")).Required(),
		schema.Property("end_chapter", schema.Int("Chương cuối của khoảng")).Required(),
		schema.Property("plot", schema.String("Tuyến tình tiết chính xuyên chương tiếp tục triển khai")).Required(),
		schema.Property("characters", stringList("Nhân vật có tiến triển thực chất")).Required(),
		schema.Property("world_facts", stringList("Sự kiện thế giới đã được xác lập")).Required(),
		schema.Property("opened_threads", stringList("Tuyến dài mới mở trong khoảng này")).Required(),
		schema.Property("resolved_threads", stringList("Tuyến dài được khép lại trong khoảng này")).Required(),
	),
}

var synthesisContract = llmcontract.Contract{
	Name:        "import_book_synthesis",
	Description: "Tổng hợp sự kiện toàn sách và đưa ra phạm vi cung truyện theo quyển liên tục, hoàn chỉnh",
	Schema: schema.Object(
		schema.Property("title", nullableString("Tên sách chính thức trong phần nội dung chính; khi không thể xác nhận thì null")).Required(),
		schema.Property("synopsis", schema.String("Giới thiệu tiểu thuyết không tiết lộ nội dung chính, hướng tới độc giả")).Required(),
		schema.Property("premise", schema.String("Mô tả Markdown về tiền đề câu chuyện")).Required(),
		schema.Property("characters", schema.Array("Nhân vật chính", schema.Object(
			schema.Property("name", schema.String("Tên nhân vật")).Required(),
			schema.Property("aliases", stringList("Tên khác và danh xưng")).Required(),
			schema.Property("role", schema.String("Vai trò tự sự")).Required(),
			schema.Property("description", schema.String("Mô tả nhân vật")).Required(),
			schema.Property("arc", schema.String("Cung nhân vật")).Required(),
			schema.Property("traits", stringList("Đặc điểm nhân vật")).Required(),
			schema.Property("tier", nullableString("Tầng bậc nhân vật; khi không thể phán đoán thì null")).Required(),
		))).Required(),
		schema.Property("world_rules", schema.Array("Quy tắc thế giới do phần nội dung chính xác lập", schema.Object(
			schema.Property("category", schema.String("Loại quy tắc")).Required(),
			schema.Property("rule", schema.String("Mô tả quy tắc")).Required(),
			schema.Property("boundary", schema.String("Ranh giới không được vi phạm")).Required(),
		))).Required(),
		schema.Property("structure", schema.Array("Phạm vi chương liên tục của quyển và cung", schema.Object(
			schema.Property("title", schema.String("Tiêu đề quyển")).Required(),
			schema.Property("theme", schema.String("Xung đột cốt lõi hoặc chủ đề của quyển")).Required(),
			schema.Property("arcs", schema.Array("Cung truyện trong quyển", schema.Object(
				schema.Property("title", schema.String("Tiêu đề cung")).Required(),
				schema.Property("goal", schema.String("Mục tiêu của cung")).Required(),
				schema.Property("start_chapter", schema.Int("Chương bắt đầu")).Required(),
				schema.Property("end_chapter", schema.Int("Chương kết thúc")).Required(),
			))).Required(),
		))).Required(),
		schema.Property("compass", schema.Object(
			schema.Property("ending_direction", schema.String("Hướng đi của hồi kết")).Required(),
			schema.Property("open_threads", stringList("Tuyến dài vẫn chưa khép lại")).Required(),
			schema.Property("estimated_scale", nullableString("Quy mô ước chừng; khi không thể phán đoán thì null")).Required(),
			schema.Property("last_updated", llmcontract.Nullable(schema.Int("Số chương mới nhất làm căn cứ; khi không cần điền thì null"))).Required(),
		)).Required(),
		schema.Property("planning_tier", schema.Enum("Tầng quy hoạch", "short", "mid", "long")).Required(),
		schema.Property("story_status", schema.Enum("Truyện đã hoàn kết hay chưa", storyOpen, storyClosed, storyUncertain)).Required(),
		schema.Property("status_reason", nullableString("Lý do phán đoán trạng thái")).Required(),
	),
}
