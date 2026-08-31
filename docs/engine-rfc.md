# Bước 2 RFC: Engine trực tiếp chạy Worker (đáp án cho bảy câu hỏi bắt buộc)

> Trạng thái: bản chính thức (2026-07-12). Dựa trên trinh sát mã nguồn của host/observer/subagent/usage/cocreate.
> Kết luận: cả bảy câu đều có đáp án rủi ro thấp, chuyển sang triển khai. Liên quan: `docs/engine-arbiter.md`.

## 1. Mặt thực thi của Worker: gọi theo chương trình `subagent.Runner`

> Ghi chú sau (2026-07-22): `agentcore` đã tách thực thi có kiểu và giao thức công cụ của model. `Runner.Run` là entry của host;
> `Runner.AsTool()` chỉ dành cho host nào cần để LLM tự phân phát subagent. AINovel chỉ phụ thuộc vào Runner.

`Runner.Run(agent, task)` mỗi lần khởi động một `agentcore.AgentLoop` hoàn chỉnh. Engine gọi trực tiếp nó, toàn bộ
**lắp ghép nguyên trạng trong `build.go` đều có hiệu lực**: model vai trò + failover, prompt cache key (#seq tăng dần mỗi lần),
`ThinkingLevel`, `UsageRecorder/SessionLogger(OnMessage)`, `Writer ContextManagerFactory`, `RestorePack`, `StopGuardFactory`,
`StopAfterTools`. Kết quả có kiểu và chuỗi lỗi được trả về trực tiếp, không qua mã hóa/giải mã JSON hay ngửi kết quả công cụ.

**Chiếu sự kiện**: trung kế tiến độ subagent đọc **callback `ToolProgress` trong `ctx`** (`agentcore.ReportToolProgress(ctx,...)`).
Engine dùng `agentcore.WithToolProgress(ctx, relay)` để gọi `Runner.Run`, trung kế vẫn hoạt động như cũ; `relay` hợp nhất
`ProgressPayload` thành `EventToolExecUpdate` rồi bơm vào `observer.handleToolUpdate` hiện có — xử lý phía worker của observer
(dòng TOOL / nội dung streaming / thinking/retry/context) **tái sử dụng ~95%**. Dòng DISPATCH do Engine trực tiếp khởi phát/kết thúc
(thêm hai entry mới cho observer). Luồng kể chuyện ở cột trái của Coordinator biến mất, được thay bằng sự kiện kể chuyện của Engine.

**/model và cường độ suy luận**: chuyển model qua `ModelSet` swap (configs giữ wrapper failover, cơ chế gốc); cường độ suy luận qua
`runner.SetThinkingLevel` (`applyThinking` giữ lại, xóa nhánh coordinator).

## 2. Vòng đời Engine

Một vòng lặp tuần tự trong một goroutine; `ctx` cancel = tạm dừng/chấm dứt (lan truyền vào worker loop, checkpoint bảo đảm không mất dữ liệu);
Resume/Continue = bắt đầu một vòng lặp mới. Worker đơn tuần tự được bảo đảm tự nhiên bởi cấu trúc vòng lặp. Các sentinel ngân sách/điểm dừng
được Engine gọi trực tiếp ở ranh giới mỗi vòng (thay cho đăng ký sự kiện và `FlowBoundaryHook`).

## 3. Giao thức commit trạng thái → tuần tự hóa khiến nó gần như biến mất

Trước khi mỗi vòng spawn, Engine mới `LoadState+Route`, nên chỉ thị luôn dựa trên thực tế mới nhất — chỉ thị Route không có TOCTOU,
không cần đối soát Expect. Snapshot Expect chỉ dùng cho **dispatch trong quyết định của Arbiter** (giữa tư vấn và thực thi có
worker chạy ở giữa): trước khi thực thi biên giới sẽ so sánh `{Phase, QueueHead}`, không khớp → loại bỏ + hỏi lại theo thực tế mới.
Kiểm tra tiền điều kiện (trách nhiệm gốc của Gate) trở thành mã bình thường của Engine: `phase=complete` thì không phân phát;
đích của writer là chương chưa bung ra → đổi sang `architect_long expand` (quyết định tất định, không cần văn bản hướng dẫn).
Các hành động trạng thái điều khiển can thiệp (hold/reopen/dispatch) được commit ở biên của hàng đợi Engine; answer/rules là tức thời.

## 4. Phân loại lỗi (ưu tiên tính tất định)

- retryable (mạng/rate limit/stream-idle): `subagent` tự tiêu hóa trong `MaxRetries=7` ở gần nguồn, không thoát vòng lặp
- worker trả về `error` (escalate/hard_stop/lỗi công cụ cứng): cùng chỉ thị đó Engine thử lại 1 lần → vẫn thất bại → Arbiter
  tư vấn `worker_failure` (retry/reroute/abort) → abort hoặc Arbiter tự thất bại → tạm dừng + notify
- lỗi tham số/lỗi tất định như agent không xác định, v.v.: trực tiếp tạm dừng + notify (bug code, thử lại vô ích)

## 5. Giao thức bế tắc

Mỗi vòng ghi lại khóa chỉ thị `Agent+Task`. Sau khi vòng trước thực thi, Route vẫn sinh ra cùng khóa, điều đó cho thấy hậu điều kiện của nhiệm vụ
chưa thỏa; `repeat++`; nếu chỉ thị đổi thì đặt lại về 0. Các checkpoint trung gian như `plan/draft/edit` bên trong Worker không tính là tiến
triển cấp Engine. `repeat==3` → Arbiter tư vấn `deadlock`; nếu Arbiter đề nghị retry thì **không đặt lại**; `repeat==5` → ngắt cứng:
tạm dừng + notify. (Thời Coordinator, “không đặt ngưỡng” là dựa vào tính tự chủ của nó; Engine tất định thì bắt buộc phải có giới hạn.)

## 6. Ngữ nghĩa crash → miễn phí

Không cần phán đoán “Worker trước có sinh ra sự thật hữu ích hay không”: checkpoint + digest ở tầng công cụ là idempotent, Route tính lại từ store,
phân phát lặp lại là an toàn. Việc retry luồng model của `agentcore` sẽ không vượt qua ranh giới thực thi công cụ. Khôi phục = đi thẳng vào vòng lặp.
`PendingSteer` trước khi vòng lặp khởi động được xử lý như can thiệp qua Arbiter.

## 7. Chấp nhận nguyên mẫu

Kiểm thử tích hợp đầu-cuối (fake ChatModel): lập kế hoạch → bổ sung → viết chương → đánh giá/tóm tắt cuối arc → bung ra → hoàn tất toàn bộ chuỗi;
phân luồng can thiệp được ghi vào store; tạm dừng/phục hồi; ngắt bế tắc; ghi `usage`; hình dạng sự kiện của observer (dòng `DISPATCH`/`TOOL`,
delta streaming). Kết hợp với đặc tả Route 60k hiện có, hợp đồng `agentcore`, và các kiểm thử luồng editor làm lưới hồi quy.

## Tóm tắt giai đoạn hoàn tất (quyết định thiết kế)

Tóm tắt hoàn tất được đổi thành **sinh ra tất định**: store đã có toàn bộ sự thật (tóm tắt chương/danh sách nhân vật/sổ theo dõi foreshadow/số từ),
Engine trực tiếp dựng báo cáo, không còn tốn thêm một lần gọi LLM để tạo văn bản mang tính nghi thức. Phần tóm tắt bằng LLM của coordinator gốc bị hủy
(`engine-arbiter.md` §ba: tóm tắt không phải phán quyết).