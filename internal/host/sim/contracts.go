package sim

import (
	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
)

func textList(description string) map[string]any {
	return schema.Array(description, schema.String(description))
}

var sourceReportContract = llmcontract.Contract{
	Name:        "simulation_source_report",
	Description: "Chắt lọc các phương pháp viết có thể tái sử dụng mà không sao chép nguyên văn từ một ngữ liệu đơn lẻ",
	Schema: schema.Object(
		schema.Property("title", llmcontract.Nullable(schema.String("Tiêu đề tùy chọn; khi không thể xác nhận thì là null"))).Required(),
		schema.Property("summary", schema.String("Khái quát giá trị về cách viết của văn bản mẫu")).Required(),
		schema.Property("style_observations", textList("Quan sát về ngôi kể, kiểu câu và chất liệu miêu tả")).Required(),
		schema.Property("common_words", textList("Các nhóm từ tần suất cao, hình ảnh biểu tượng và từ chuyển cảnh")).Required(),
		schema.Property("plot_patterns", textList("Mô thức thúc đẩy cốt truyện, bước ngoặt và leo thang xung đột")).Required(),
		schema.Property("hook_patterns", textList("Mô thức hook ở mở đầu, cuối chương và khoảng lệch thông tin")).Required(),
		schema.Property("pacing_notes", textList("Mật độ cảnh và nhịp độ giải phóng thông tin")).Required(),
		schema.Property("reader_appeal", textList("Phương pháp thu hút độc giả tiếp tục đọc")).Required(),
		schema.Property("reusable_techniques", textList("Kỹ thuật cấu trúc có thể tham khảo")).Required(),
		schema.Property("warnings", textList("Rủi ro sao chép và áp dụng rập khuôn bắt buộc phải tránh")).Required(),
	),
}

var synthesisContract = llmcontract.Contract{
	Name:        "simulation_synthesis",
	Description: "Tổng hợp chân dung hiện có và báo cáo ngữ liệu thành chân dung phương pháp mô phỏng lối viết có thể thực thi",
	Schema: schema.Object(
		schema.Property("style", schema.Object(
			schema.Property("narrative_voice", textList("Ngôi kể, khoảng cách trần thuật và kiểm soát thông tin")).Required(),
			schema.Property("sentence_rhythm", textList("Nhịp điệu câu")).Required(),
			schema.Property("prose_texture", textList("Chất liệu văn xuôi")).Required(),
			schema.Property("perspective", textList("Quy tắc góc nhìn")).Required(),
			schema.Property("mood", textList("Sắc thái cảm xúc")).Required(),
			schema.Property("do_not_copy", textList("Nội dung cấm sao chép")).Required(),
		)).Required(),
		schema.Property("lexicon", schema.Object(
			schema.Property("common_words", textList("Nhóm từ thường dùng")).Required(),
			schema.Property("emotion_words", textList("Nhóm từ cảm xúc")).Required(),
			schema.Property("scene_words", textList("Nhóm từ về bối cảnh")).Required(),
			schema.Property("transition_words", textList("Nhóm từ chuyển cảnh")).Required(),
			schema.Property("signature_phrases", textList("Đặc trưng giọng điệu đã được trừu tượng hóa, không chứa câu gốc")).Required(),
		)).Required(),
		schema.Property("plot_design", schema.Object(
			schema.Property("opening_patterns", textList("Cách mở đầu")).Required(),
			schema.Property("escalation_patterns", textList("Cách leo thang xung đột")).Required(),
			schema.Property("turning_point_patterns", textList("Thiết kế bước ngoặt")).Required(),
			schema.Property("payoff_patterns", textList("Cách thu hồi và thực hiện lời hứa truyện")).Required(),
		)).Required(),
		schema.Property("hook_design", schema.Object(
			schema.Property("hook_types", textList("Loại hook")).Required(),
			schema.Property("placement", textList("Vị trí hook")).Required(),
			schema.Property("cliffhanger_patterns", textList("Cách ngắt ở điểm hồi hộp")).Required(),
			schema.Property("payoff_rules", textList("Quy tắc thực hiện hook")).Required(),
		)).Required(),
		schema.Property("pacing_density", schema.Object(
			schema.Property("scene_density", textList("Mật độ thông tin trong một cảnh")).Required(),
			schema.Property("information_release", textList("Nhịp độ giải phóng thông tin")).Required(),
			schema.Property("dialogue_action_ratio", textList("Tỉ lệ giữa đối thoại, hành động và tâm lý")).Required(),
			schema.Property("compression_rules", textList("Quy tắc triển khai và nén nội dung")).Required(),
		)).Required(),
		schema.Property("reader_engagement", schema.Object(
			schema.Property("methods", textList("Phương pháp thu hút độc giả")).Required(),
			schema.Property("emotional_drivers", textList("Động lực cảm xúc")).Required(),
			schema.Property("progression_rewards", textList("Phần thưởng tiến triển theo giai đoạn")).Required(),
			schema.Property("anti_patterns", textList("Phản mô thức làm suy yếu sức hút")).Required(),
		)).Required(),
		schema.Property("role_guidance", schema.Object(
			schema.Property("architect", textList("Quy tắc để Architect sử dụng chân dung")).Required(),
			schema.Property("writer", textList("Quy tắc để Writer tham khảo nhưng không sao chép")).Required(),
			schema.Property("editor", textList("Quy tắc để Editor kiểm tra định hướng và rủi ro xâm phạm bản quyền")).Required(),
		)).Required(),
	),
}
