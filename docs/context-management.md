# Hướng dẫn quản lý ngữ cảnh

Tài liệu này giải thích hệ thống quản lý ngữ cảnh hiện tại của `ainovel-cli`, bao gồm:

- Vì sao cần quản lý ngữ cảnh
- Ngữ cảnh đến từ đâu
- Khi chạy thì nén, khôi phục, bàn giao như thế nào
- Giá trị, điều kiện kích hoạt và kịch bản áp dụng của từng chiến lược
- Khi có sự cố thì nên xem gì trước

Mục tiêu không phải giới thiệu khái niệm trừu tượng, mà là để người bảo trì sau này mở tài liệu này ra là có thể nhanh chóng hiểu được cách triển khai hiện tại và điểm vào để xử lý sự cố.

## 1. Mục tiêu thiết kế

Quản lý ngữ cảnh của dự án này không phải cho kịch bản chat tổng quát, mà là cho bối cảnh sáng tác tiểu thuyết. Nó phải đồng thời giải quyết vài vấn đề:

1. Hội thoại dài sẽ vượt quá cửa sổ ngữ cảnh của mô hình.
2. Sáng tác tiểu thuyết cần giữ lại không phải “lịch sử chat” tự thân, mà là ký ức tường thuật có cấu trúc.
3. Writer sau khi nén không được làm mất trạng thái nhân vật, mầm truyện, kế hoạch chương, ràng buộc phong cách, các mục cần sửa khi duyệt.
4. Khi khôi phục viết tiếp không thể giả định rằng mô hình còn “nhớ đã nói gì trước đó”, mà phải ưu tiên dựa vào các hiện vật đã lưu bền vững.

Vì vậy chúng ta dùng một giải pháp “bộ nhớ nhiều tầng”:

- Bộ nhớ ngắn hạn: phần đuôi các tin nhắn gần nhất
- Bộ nhớ trung hạn: `ContextSummary` được tạo từ nén
- Bộ nhớ dài hạn: các hiện vật có cấu trúc trong project store
- Bộ nhớ khôi phục: handoff / restore pack / novel_context

## 2. Kiến trúc tổng thể

### 2.1 Các tầng chính

Hiện tại quản lý ngữ cảnh được chia thành bốn tầng:

1. `agentcore/context`
   Phụ trách ngân sách ngữ cảnh chung, pipeline chiến lược, khung nén/khôi phục.

2. `internal/tools/novel_context`
   Phụ trách ghép dữ liệu có cấu trúc trong project tiểu thuyết thành ngữ cảnh dùng được cho lượt hiện tại.

3. `internal/orchestrator/store_summary_*`
   Phụ trách nén nhanh dựa trên store chuyên cho Writer.

4. `internal/orchestrator/writer_restore.go`
   Phụ trách nối thêm một restore pack sau `FullSummary`, đảm bảo Writer có thể tiếp tục viết.

### 2.2 Luồng dữ liệu

Khi chạy có hai đường ngữ cảnh chính:

1. Đường làm việc bình thường
   - Agent gọi `novel_context`
   - `novel_context` đọc từ store các dữ liệu như tóm tắt chương, kế hoạch, nhân vật, dòng thời gian
   - Các dữ liệu này đi vào prompt của lượt hiện tại

2. Đường ngữ cảnh quá dài
   - `ContextManager` phát hiện áp lực token
   - Nén theo thứ tự chiến lược
   - Ưu tiên thử nén nhẹ và nén dựa trên store
   - Chỉ khi vẫn chưa đủ mới chuyển sang LLM `FullSummary`
   - Sau `FullSummary` thì chèn restore pack

## 3. Các file quan trọng

### 3.1 Bộ máy ngữ cảnh chung

- `../agentcore/context/strategy.go`
- `../agentcore/context/engine.go`
- `../agentcore/context/strategy_tool.go`
- `../agentcore/context/strategy_trim.go`
- `../agentcore/context/strategy_summary.go`
- `../agentcore/context/message.go`
- `../agentcore/context/summary_run.go`

Vai trò:

- Định nghĩa `Strategy` / `ForceCompactionStrategy`
- Phụ trách thực thi chuỗi chiến lược dựa trên ngân sách
- Phụ trách biểu diễn `ContextSummary` và chuyển đổi sang LLM
- Phụ trách nén tóm tắt LLM bằng `FullSummary`

### 3.2 Phần nối dây phía project

- `internal/orchestrator/agents.go`

Vai trò:

- Ghép `ContextManager` cho Writer (Coordinator đã nghỉ hưu vào 2026-07-12, xem docs/engine-arbiter.md)
- Gắn thêm `StoreSummaryCompact` cho Writer
- Cấu hình prompt `FullSummary` tùy biến cho tiểu thuyết
- Cấu hình `writerRestorePack` cho Writer

### 3.3 Nén và khôi phục phía project

- `internal/orchestrator/store_summary_strategy.go`
- `internal/orchestrator/store_summary_builder.go`
- `internal/orchestrator/writer_restore.go`

Vai trò:

- Trước khi tóm tắt bằng LLM, ưu tiên dùng dữ liệu store để nén nhanh
- Thống nhất xây dựng ngữ cảnh có cấu trúc cần cho nén và khôi phục của Writer
- Sau `FullSummary` nối thêm một restore message chỉ nằm trong bộ nhớ

### 3.4 Ghép ngữ cảnh có cấu trúc

- `internal/tools/novel_context.go`
- `internal/tools/novel_context_builders.go`
- `internal/domain/runtime.go`

Vai trò:

- Định nghĩa `ContextProfile` / `MemoryPolicy`
- Quyết định tải bao nhiêu tóm tắt chương, bao nhiêu dòng thời gian, có bật tóm tắt nhiều tầng hay không
- Ghép ra chương, nhân vật, mồi truyện, dòng thời gian, kinh nghiệm duyệt sửa, v.v. từ store

### 3.5 Bàn giao và khôi phục

- `internal/orchestrator/handoff_policy.go`
- `internal/orchestrator/recovery_engine.go`

Vai trò:

- Ở giai đoạn truyện dài / sửa lại / duyệt, ưu tiên dựa vào handoff
- Khi khôi phục thì ghép gói bàn giao có cấu trúc vào prompt

### 3.6 Khả năng quan sát

- `internal/orchestrator/run.go`
- `internal/orchestrator/runtime.go`
- `internal/entry/tui/panels.go`

Vai trò:

- Ghi lại sự kiện viết lại ngữ cảnh
- Xuất tên chiến lược, thay đổi token, số lượng tin nhắn được giữ
- Để TUI có thể thấy ngữ cảnh hiện tại là `projected` hay `compacted`

## 4. ContextManager được ghép như thế nào

Writer đi qua `newContextManager` (mỗi lần spawn đều được factory dựng lại theo cửa sổ mô hình hiện tại). Trước khi Coordinator nghỉ hưu cũng đi qua cùng factory này, cấu hình của nó được giữ trong bảng dưới đây để đối chiếu lịch sử.

Các tham số chính của `contextManagerConfig`:

- `ContextWindow`
  Cửa sổ ngữ cảnh tổng của mô hình.

- `ReserveTokens`
  Số token dành riêng cho đầu ra của mô hình.

- `KeepRecentTokens`
  Ngân sách dành cho phần đuôi tin nhắn gần nhất mà cố gắng giữ khi nén.

- `ToolMicrocompact`
  Cấu hình nén vi mô kết quả công cụ.

- `ExtraStrategies`
  Các chiến lược nén bổ sung phía project. Hiện Writer dùng để gắn `StoreSummaryCompact`.

- `Summary`
  Cấu hình `FullSummary`, gồm prompt tùy biến và post-summary hook.

Giá trị cấu hình thực tế hiện tại:

| Tham số | Writer | Coordinator（đã nghỉ hưu, đối chiếu lịch sử） |
|------|--------|-------------|
| ReserveTokens | 16,384 | 32,000 |
| KeepRecentTokens | 20,000 | 30,000 |
| CommitOnProject | false | true |
| IdleThreshold | 5min | không có |
| ExtraStrategies | StoreSummaryCompact | không có |
| Custom Summary Prompt | bản tường thuật tiểu thuyết | mặc định (bản trợ lý code) |

Ngưỡng kích hoạt nén = `ContextWindow - ReserveTokens`. Ví dụ cửa sổ 128K thì Writer kích hoạt ở khoảng ~112K.

Thứ tự pipeline chiến lược hiện tại của Writer là:

1. `ToolResultMicrocompact`
2. `LightTrim`
3. `StoreSummaryCompact`
4. `FullSummary`

Thứ tự này có ý nghĩa rõ ràng:

- Trước hết dùng cách rẻ nhất để dọn nhiễu từ công cụ
- Sau đó cắt bớt các khối văn bản quá dài
- Nếu dữ liệu store đủ thì nén có cấu trúc, không cần LLM
- Cuối cùng mới rơi về tóm tắt LLM

## 5. Tác dụng của từng chiến lược

### 5.1 ToolResultMicrocompact

Vị trí triển khai:

- `../agentcore/context/strategy_tool.go`

Tác dụng:

- Dọn `tool_result` lịch sử
- Thay các kết quả công cụ cũ bằng văn bản chỗ giữ ngắn

Giá trị:

- Kết quả trả về của công cụ thường rất lớn nhưng mật độ thông tin thấp
- Nhiều kết quả công cụ cũ chỉ là “nhiễu quy trình”, không phải ký ức tiểu thuyết

Đặc điểm cấu hình của Writer hiện tại:

- Đặt `IdleThreshold = 5m`

Điều này có nghĩa:

- Nếu tin nhắn assistant gần nhất đã nhàn rỗi quá ngưỡng
- Sẽ giảm số lượng kết quả công cụ cũ được giữ lại một cách mạnh hơn

Kịch bản áp dụng:

- Nhiều lượt `novel_context`
- Sau nhiều lượt read / check / draft bằng công cụ

### 5.2 LightTrim

Vị trí triển khai:

- `../agentcore/context/strategy_trim.go`

Tác dụng:

- Cắt các khối văn bản cực dài
- Giữ đầu và cuối, thay phần giữa bằng chỗ giữ

Giá trị:

- Giữ nguyên cấu trúc tin nhắn
- Chi phí thấp
- Rất phù hợp để xử lý nguyên văn chương quá dài hoặc các đoạn output lớn

Kịch bản áp dụng:

- Một tin nhắn quá dài, nhưng chưa cần tóm tắt toàn bộ lịch sử

### 5.3 StoreSummaryCompact

Vị trí triển khai:

- `internal/orchestrator/store_summary_strategy.go`
- `internal/orchestrator/store_summary_builder.go`

Tác dụng:

- Khi ngữ cảnh của Writer quá dài
- Ưu tiên dùng ký ức có cấu trúc từ store bền vững để thay thế các tin nhắn cũ
- Không gọi LLM

Đây không phải tóm tắt hội thoại, mà là “thay thế ký ức có cấu trúc”.

Các dữ liệu cốt lõi hiện được giữ gồm:

- Tiến độ hiện tại
- Tóm tắt các chương gần nhất
- Kế hoạch chương hiện tại
- Dàn ý chương hiện tại
- Tóm tắt arc hiện tại
- Tóm tắt volume hiện tại
- Snapshot nhân vật
- Mồi truyện đang hoạt động
- Vấn đề duyệt sửa cần xử lý
- Dòng thời gian gần nhất
- Quy tắc phong cách

Điều kiện kích hoạt:

- Chương hiện tại lớn hơn 1
- Store đã có đủ tóm tắt lịch sử
- Và chương hiện tại ít nhất có dữ liệu trạng thái công việc
  - `chapter_plan` hoặc `current_outline`

Giá trị:

- Giảm số lần nén bằng LLM
- Tránh làm trôi thông tin quan trọng của tiểu thuyết khi tóm tắt
- Để bộ nhớ dài hạn ưu tiên dựa trên sự thật đã ghi xuống đĩa, thay vì lịch sử chat

Vì sao chỉ cho Writer:

- Đây là chiến lược nghiệp vụ tiểu thuyết, không phải chiến lược framework tổng quát
- Ngữ cảnh của Editor / Architect khác (nhiệm vụ đơn lẻ, áp lực cửa sổ nhỏ)
- Trước hết kiểm chứng ở Writer, nơi cần trí nhớ viết liên tục nhất, là hợp lý nhất

### 5.4 FullSummary

Vị trí triển khai:

- `../agentcore/context/strategy_summary.go`
- `../agentcore/context/summary_run.go`

Tác dụng:

- Khi các tầng trên vẫn chưa đủ, dùng mô hình để tạo `ContextSummary`
- Giữ phần đuôi các tin nhắn gần nhất
- Biến ngữ cảnh cũ hơn thành checkpoint có cấu trúc

Điểm khác giữa Writer và trợ lý code mặc định:

- Writer dùng prompt tóm tắt tùy biến
- Nội dung tóm tắt được yêu cầu rõ ràng phải giữ:
  - Tiến độ hiện tại
  - Trạng thái tức thời của nhân vật
  - Mồi truyện và manh mối đang hoạt động
  - Phản hồi duyệt sửa và mục cần sửa
  - Phong cách và tiết tấu
  - Quyết định then chốt
  - Bước tiếp theo
  - Ngữ cảnh then chốt

Giá trị:

- Là chiến lược chốt hạ cuối cùng
- Ngay cả khi dữ liệu store không đủ, vẫn có thể dùng LLM để duy trì tính liên tục

### 5.5 Bộ ngắt mạch (Circuit Breaker)

Vị trí triển khai:

- `../agentcore/context/engine.go`

Tác dụng:

- Khi nén thất bại liên tiếp đến ngưỡng (mặc định 3 lần), bỏ qua nén ở lượt hiện tại
- Khi bỏ qua vẫn phát `RewriteEvent` (`Reason = “circuit_breaker”`)
- TUI sẽ hiển thị scope là “bỏ qua do ngắt mạch”
- Dùng chế độ bán mở: bỏ qua một lượt rồi lần sau sẽ thử lại, nếu thành công thì reset, nếu thất bại lại thì bỏ qua tiếp

Vì sao cần:

- Tóm tắt bằng LLM có thể thất bại liên tiếp do mạng, do mô hình từ chối, v.v.
- Không có ngắt mạch thì mỗi lượt Project đều sẽ thử và thất bại, lãng phí lời gọi API
- Trong phiên viết truyện dài, sự lãng phí này sẽ tích lũy

Cách xử lý sự cố:

- Nếu TUI liên tục hiển thị “bỏ qua do ngắt mạch”, nghĩa là đường tóm tắt LLM đang có vấn đề
- Kiểm tra các sự kiện viết lại ngữ cảnh trong slog với `reason=circuit_breaker`
- Ngắt mạch không ảnh hưởng đến `StoreSummaryCompact` (vì nó không gọi LLM)

### 5.6 Ước lượng token (nhận biết CJK)

Vị trí triển khai:

- `../agentcore/context/usage.go`

Tác dụng:

- Mọi điều khiển ngân sách và thời điểm kích hoạt nén đều phụ thuộc vào ước lượng token
- `estimateTextTokens` tự động phát hiện văn bản có chủ yếu là ký tự CJK hay không
- Văn bản chủ đạo CJK: `runes × 1.5`
- Văn bản chủ đạo ASCII: `bytes / 4`

Vì sao không thể dùng chuẩn `bytes/4`:

- Một ký tự tiếng Trung UTF-8 = 3 bytes
- `bytes/4` sẽ ước một chữ Hán là 0.75 token, trong thực tế khoảng 1.5 token
- Đánh giá thấp gấp 2 lần sẽ làm thời điểm kích hoạt nén bị trễ nghiêm trọng

Phạm vi ảnh hưởng:

- `EstimateTokens` (một tin nhắn)
- `EstimateTotal` (danh sách tin nhắn)
- `EstimateContextTokens` (ước lượng lai: Usage do LLM báo + ước lượng tin nhắn đuôi)
- Việc cắt ngân sách trong `store_summary_builder.go`

Lưu ý: args của ToolCall là JSON (chủ yếu ASCII), vẫn dùng `bytes/4`, không chịu điều chỉnh CJK.

## 6. Vì sao Writer có hai bộ “ký ức sau khi nén”

Hiện tại Writer có hai luồng nhìn khá giống nhau nhưng trách nhiệm khác nhau:

### 6.1 StoreSummaryCompact

Trách nhiệm:

- Trực tiếp thay thế các tin nhắn cũ trong quá trình nén

Đặc điểm:

- Xảy ra trước `FullSummary`
- Không dùng LLM
- Dùng store để thay thế lịch sử sớm hơn

### 6.2 writerRestorePack

Vị trí triển khai:

- `internal/orchestrator/writer_restore.go`

Trách nhiệm:

- Nối thêm một restore message sau `FullSummary`

Đặc điểm:

- Xảy ra sau khi LLM nén xong
- Chèn qua `PostSummaryHook`
- Dùng để bổ sung thông tin có cấu trúc mà Writer bắt buộc phải thấy khi khôi phục để viết tiếp

Vì sao cần cả hai:

- `StoreSummaryCompact` không phải lúc nào cũng khớp
  - Ví dụ ở chương đầu tiên hoặc khi dữ liệu store chưa đủ
- `FullSummary` dù tốt đến đâu cũng có thể bỏ sót thông tin chính xác trong store
- Vì vậy restore pack là lớp bảo hiểm cuối cùng

Hiện tại hai thứ này đã dùng chung `store_summary_builder.go`, tránh lệch cách diễn đạt.

## 7. Vai trò của novel_context

Vị trí triển khai:

- `internal/tools/novel_context.go`
- `internal/tools/novel_context_builders.go`

`novel_context` không phải chiến lược nén, mà là “bộ ghép ngữ cảnh có cấu trúc” khi chạy.

Nó chia dữ liệu trong store thành vài loại:

- `working_memory`
  - Kế hoạch chương hiện tại
  - Dàn ý chương hiện tại
  - Tóm tắt các chương gần nhất
  - Dòng thời gian
  - checkpoint
  - previous tail

- `episodic_memory`
  - Trạng thái nhân vật
  - Trạng thái quan hệ
  - Thay đổi trạng thái gần đây
  - Mồi truyện

- `reference_pack`
  - Các thiết lập và dữ liệu tham chiếu ổn định hơn

- `selected_memory`
  - Một lượng nhỏ ký ức quan trọng được chọn theo nhiệm vụ hiện tại

Giá trị:

- Nó quyết định ngữ cảnh tiểu thuyết có cấu trúc thực sự được “đưa cho mô hình” ở mỗi lượt
- `StoreSummaryCompact` không gọi chính nó, nhưng dùng lại cùng nguồn dữ liệu và cùng tư duy ghép lắp

## 8. `ContextProfile` và `MemoryPolicy`

Vị trí triển khai:

- `internal/domain/runtime.go`

### 8.1 `ContextProfile`

Tác dụng:

- Quyết định kích thước cửa sổ tải dựa trên tổng số chương

Quy tắc hiện tại:

- `<= 15` chương
  - 10 tóm tắt chương gần nhất
  - 10 dòng thời gian gần nhất

- `<= 50` chương
  - 5 tóm tắt chương gần nhất
  - 8 dòng thời gian gần nhất

- `> 50` chương
  - 3 tóm tắt chương gần nhất
  - 5 dòng thời gian gần nhất
  - bật tóm tắt nhiều tầng

Giá trị:

- Kiểm soát quy mô ngữ cảnh
- Tránh nhồi toàn bộ lịch sử vào prompt khi truyện dài

### 8.2 `MemoryPolicy`

Tác dụng:

- Viết rõ ràng chiến lược sử dụng ngữ cảnh hiện tại
- Cho `novel_context` xuất ra
- Cho handoff / reminder / logic chẩn đoán sử dụng

Các trường quan trọng:

- `SummaryWindow`
- `TimelineWindow`
- `LayeredSummaries`
- `SummaryStrategy`
- `HandoffPreferred`
- `ReadOnlyThreshold`

Giá trị:

- Biến “hệ thống hiện tại nên dùng bộ nhớ như thế nào” từ logic ẩn thành chính sách chạy rõ ràng

## 9. Vai trò của handoff

Vị trí triển khai:

- `internal/orchestrator/handoff_policy.go`

Khi tác phẩm đi vào giai đoạn dài hơn, phức tạp hơn, và phụ thuộc nhiều hơn vào hiện vật có cấu trúc, hệ thống sẽ nghiêng về handoff.

handoff pack sẽ ghi lại:

- Giai đoạn và flow hiện tại
- Vị trí chương tiếp theo
- Lần submit gần nhất
- Lần duyệt gần nhất
- Tóm tắt gần nhất
- `MemoryPolicy` hiện tại
- Câu hướng dẫn khôi phục

Giá trị:

- Khôi phục sau gián đoạn không phụ thuộc vào lịch sử chat
- Trong các kịch bản sửa lại, duyệt, truyện dài, ưu tiên dựa vào hiện vật có cấu trúc

## 10. Khả năng quan sát và xử lý sự cố

### 10.1 Sự kiện viết lại ngữ cảnh

Vị trí triển khai:

- `internal/orchestrator/run.go`

Mỗi lần viết lại ngữ cảnh đều sẽ phát qua `contextRewriteCallback`:

- `reason`
- `strategy`
- `committed`
- `tokens_before`
- `tokens_after`
- `messages_before`
- `messages_after`
- `compacted_count`
- `kept_count`
- `split_turn`
- `incremental`
- `summary_runes`
- `duration_ms`

Nó sẽ đồng thời đi vào:

- `slog`
- hàng đợi runtime boundary
- sự kiện TUI `COMPACT`

### 10.2 Trong TUI có thể thấy gì

TUI sẽ hiển thị:

- Token ngữ cảnh hiện tại (có màu chuyển dần theo tình trạng)
- `context window`
- `scope` ngữ cảnh hiện tại (có “bỏ qua do ngắt mạch”)
- Tên chiến lược cuối cùng gần nhất
- Số lượng summary

Ý nghĩa màu sắc của phần trăm ngữ cảnh (triển khai ở `internal/entry/tui/layout.go`):

| Màu | Điều kiện | Ý nghĩa |
|------|------|------|
| Xanh lá | < 70% | Dư dả, còn xa ngưỡng nén |
| Vàng | 70-85% | Gần ngưỡng nén |
| Đỏ | > 85% | Sắp hoặc đang nén |

Nhãn của Scope:

| Scope | Hiển thị | Ý nghĩa |
|-------|------|------|
| baseline | Cơ sở | Trạng thái bình thường |
| projected | Dự phóng | Xem trước nén tạm thời |
| compacted | Đã ghi nhận | Nén đã có hiệu lực |
| recovered | Khôi phục | Khôi phục sau tràn |
| skipped | Bỏ qua do ngắt mạch | Nén bị bộ ngắt mạch bỏ qua |

Giá trị:

- Có thể nhanh chóng đánh giá tình trạng ngữ cảnh
- Khi vàng/đỏ thì có thể dự đoán sắp có nén
- Thấy “bỏ qua do ngắt mạch” nghĩa là đường tóm tắt LLM đang có vấn đề### 10.3 Khi gặp sự cố thì xem ở đâu trước

#### Tình huống 1: Writer bị mất kế hoạch chương sau khi nén

Xem trước:

- `novel_context` có inject ổn định `chapter_plan` hay không
- `store_summary_builder.go` có lấy được `chapterPlan` hay không
- `writerRestorePack` có được refresh hay không

Các file trọng điểm:

- `internal/tools/novel_context_builders.go`
- `internal/orchestrator/store_summary_builder.go`
- `internal/orchestrator/session.go`

#### Tình huống 2: Mất trạng thái nhân vật/foreshadow sau khi nén

Xem trước:

- `LoadLatestSnapshots`
- `LoadActiveForeshadow`
- `store_summary_builder.go`
- Writer summary prompt có bị ghi đè hay không

#### Tình huống 3: Nén thường xuyên nhưng luôn không trúng store_summary

Xem trước:

- Chương hiện tại có phải `<= 1` hay không
- Đã có recent summaries / arc / volume summary hay chưa
- Có tồn tại `chapter_plan` hoặc `current_outline` hay không
- `writer.Context.Strategy` cuối cùng được ghi lại có phải `full_summary` hay không

#### Tình huống 4: Sau khi khôi phục, ngữ cảnh không đủ

Xem trước:

- handoff có được tạo hay không
- restore pack có được refresh hay không
- recovery prompt có inject handoff hay không

#### Tình huống 5: Kết quả công cụ quá nhiều khiến ngữ cảnh phình to

Xem trước:

- `ToolResultMicrocompact` có được kích hoạt hay không
- `IdleThreshold` có hiệu lực hay không

## 11. Những đánh đổi trong triển khai hiện tại

### Các hướng đã xác định rõ là sẽ kiên trì

1. Không nhồi logic nghiệp vụ tiểu thuyết vào `agentcore`
2. Ưu tiên dựa vào store có cấu trúc, thay vì lịch sử trò chuyện
3. Writer sử dụng prompt tóm tắt tiểu thuyết chuyên biệt
4. Nén và khôi phục cố gắng dùng chung builder, tránh lệch cách diễn đạt

### Các giới hạn hiện tại vẫn được chủ ý giữ lại

1. `StoreSummaryCompact` chỉ dùng cho Writer
2. Chương đầu tiên sẽ không trúng store-based compact
3. Khi dữ liệu store không đủ, vẫn fallback về `FullSummary`
4. `writerRestorePack` là phần bù đắp dạng bổ sung, không thay thế `FullSummary`

Những giới hạn này không phải là khiếm khuyết, mà là ranh giới được đặt ra ở giai đoạn hiện tại để kiểm soát độ phức tạp.

## 12. Tóm tắt trong một câu

Quản lý ngữ cảnh của dự án này không đơn giản chỉ là “nén cuộc trò chuyện dài thành ngắn”, mà là:

`Ưu tiên dùng ký ức tiểu thuyết có cấu trúc để duy trì tính liên tục, chỉ khi cần thiết mới để LLM tóm tắt cuộc trò chuyện; đồng thời trong cả ba khâu nén, khôi phục, bàn giao đều cố gắng dựa vào cùng một bộ artifact được lưu bền vững.`

Nếu sau này bạn cần sửa hệ thống này, hãy ưu tiên giữ vững ba điểm sau:

1. Đừng để ký ức then chốt của Writer lại chỉ phụ thuộc vào lịch sử trò chuyện.
2. Đừng để cách diễn đạt của `store_summary` và `writer_restore` rẽ nhánh.
3. Khi xuất hiện vấn đề về tính liên tục, trước hết hãy kiểm tra artifact có cấu trúc đã đi vào ngữ cảnh hay chưa, rồi mới quyết định có sửa prompt hay không.