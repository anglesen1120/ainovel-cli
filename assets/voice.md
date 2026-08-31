## Chuẩn viết

Đây là chuẩn chất lượng, không phải bảng checklist để gạch máy móc. Chương trước hết phải tự nhiên và đứng vững, sau đó mới đủ các điểm kiểm tra.

- Mở đầu nhanh chóng tạo xung đột, bí ẩn, ham muốn hoặc cảm giác bất thường; hạn chế hồi tưởng trừu tượng.
- Dùng hành động, đối thoại và chi tiết giác quan để đẩy cốt truyện; hạn chế tóm lược và kết luận thay người đọc.
- Đối thoại cần khác biệt theo thân phận, có ẩn ý và mục đích hành động; không thuyết giáo.
- Cảm xúc thể hiện qua phản ứng cơ thể và lựa chọn, không dán nhãn trực tiếp.
- Biến chuyển quan hệ phải có sự kiện kích hoạt; đừng để người lạ nhảy sang tin tưởng tuyệt đối trong một chương.
- Bí mật nhả theo từng đợt; không giải thích sớm những bí mật lớn mà outline chưa yêu cầu.
- Móc cuối chương có thể là nguy cơ, lựa chọn, dư âm cảm xúc, biến đổi quan hệ hoặc mục tiêu chưa xong; không cần chương nào cũng phóng đại suspense.
- **Giảm mùi AI**: khi viết, tránh toàn bộ mẫu trong `reference_pack.references.anti_ai_tone` (cấu trúc / từ ngữ / miêu tả / đối thoại / nhịp). Những phần đếm được bằng máy như từ mòn, câu khuôn và ngưỡng lặp nằm trong `working_memory.user_rules.structured`, được kiểm tra bắt buộc lúc commit.
- **Đa dạng câu**: `episodic_memory.style_stats` (nếu có) là thống kê code rút ra từ văn bản bạn đã viết — tấm gương phản chiếu thói quen câu chữ. Chủ động giảm các mục tần suất cao; nguồn đông cứng thường gặp là câu chỉnh lý ("không phải... mà là..."), lượng từ thời gian đơn điệu và so sánh cùng dạng. Cách khép chương (câu ngắn / dư âm đối thoại / dư ảnh cảnh / câu hỏi móc) nên luân phiên với chương gần đây; mở đầu tránh lần nào cũng dùng kiểu thời điểm như đêm, sáng sớm, thức dậy.
- **Không tóm tắt tiền truyện**: summary, foreshadow và state trong `episodic_memory` là ghi nhớ của nội dung đã xuất hiện, dùng để nối mạch chứ không phải chất liệu chờ viết lại. Thông tin chương trước đã trình bày thì chương mới chỉ chạm lại khi cốt truyện cần và từ góc nhìn mới; cấm viết lại kiểu recap đầu chương (lặp nguyên câu xuyên chương sẽ bị `style_stats.repeated_sentences` ghi nhận).
