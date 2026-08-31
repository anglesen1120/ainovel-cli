## Tiêu chuẩn viết

Đây là các nguyên tắc chất lượng, đừng kiểm tra máy móc từng mục một. Trước hết chương phải tự nhiên và đứng vững, sau đó mới xét đến việc các tiêu chí có đầy đủ hay không.

- Mở đầu cần nhanh chóng thiết lập xung đột, hồi hộp, ham muốn hoặc cảm giác bất thường, hạn chế dùng hồi tưởng trừu tượng.
- Dùng hành động, đối thoại, chi tiết cảm quan để đẩy tình tiết tiến lên, hạn chế khái quát và tổng kết.
- Đối thoại của nhân vật cần có khác biệt thân phận, hàm ý và mục đích hành động, không thuyết giáo.
- Cảm xúc nên được thể hiện bằng phản ứng cơ thể và lựa chọn, không dán nhãn trực tiếp.
- Sự thay đổi quan hệ cần có sự kiện kích hoạt, không để trong một chương nhảy từ xa lạ sang tin tưởng tuyệt đối.
- Bí mật được hé lộ theo từng đợt, không giải thích trước những lời giải lớn mà dàn ý chưa yêu cầu.
- Móc câu cuối chương có thể là khủng hoảng, lựa chọn, dư âm cảm xúc, thay đổi quan hệ hoặc mục tiêu chưa hoàn thành, không nhất thiết chương nào cũng phải tạo hồi hộp phóng đại.
- **Khử mùi AI**: khi viết, tránh toàn bộ các mẫu được liệt kê trong `reference_pack.references.anti_ai_tone` (năm nhóm: cấu trúc/cách dùng từ/miêu tả/đối thoại/nhịp điệu). Trong đó, ngưỡng cho các từ gây mệt mỏi và câu khuôn sáo có thể liệt kê cơ học nằm ở `working_memory.user_rules.structured`, khi commit sẽ bắt buộc kiểm tra.
- **Đa dạng câu thức**: `episodic_memory.style_stats` (nếu có) là thống kê của mã đối với phần chính văn bạn đã viết — tấm gương phản chiếu thói quen diễn đạt của chính bạn. Chương này chủ động hạ tần suất các mục xuất hiện cao trong đó; nguồn cố định hóa thường gặp nhất là câu đính chính ("không phải… mà là…"), lượng từ thời gian đơn nhất ("vài hơi thở/mấy hơi thở") và chuỗi so sánh trực tiếp cùng kiểu. Hình thức kết thúc cuối chương (câu ngắn chém đứt/dư âm đối thoại/dư ảnh cảnh vật/câu hỏi hồi hộp) cần luân phiên với các chương gần đây, mở đầu tránh chương nào cũng bắt đầu kiểu thời gian như "ban đêm/sáng sớm/tỉnh dậy".
- **Không thuật lại tiền tình**: các tóm tắt, phục bút, trạng thái trong `episodic_memory` là ghi nhớ về nội dung đã viết vào chính văn, dùng để đối chiếu và nối tiếp, không phải tư liệu chờ viết của chương này; thông tin chương trước đã nói rõ, chương mới chỉ chạm tới bằng góc nhìn mới khi cốt truyện cần, cấm viết lại kiểu tóm tắt tiền tình (việc lặp nguyên văn xuyên chương sẽ bị ghi vào repeated_sentences của style_stats).