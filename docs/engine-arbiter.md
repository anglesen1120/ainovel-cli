# Sự tiến hóa của mặt phẳng điều khiển: Engine + Arbiter (loại bỏ vòng lặp dài Coordinator)

> Trạng thái (2026-07-14 v6): **triển khai mã đã hoàn tất** —— Engine/Arbiter đã được triển khai, Coordinator và toàn bộ phần phụ trợ đã bị xóa (danh sách §mười); xác minh đầu-cuối-đến-cuối đã viết sách hoàn chỉnh, phân xử thất bại, phân xử bế tắc, nghiệm thu làm lại (trình tự hold+editor), boundary hold dừng ngay, bảo toàn can thiệp tranh chấp thoát và một giấy phép một chương. Tất cả hạng mục chặn từ ba vòng đánh giá bên ngoài đã được xử lý (bao gồm vòng khép kín sự kiện phản hồi, bảo vệ sập PendingSteer, tranh chấp vòng đời).
> **Di trú tài liệu đã hoàn tất (2026-07-12)**: phần thân architecture.md đã được viết lại toàn bộ thành kiến trúc hiện hành Engine+Arbiter (bao gồm chiến lược/thư mục/kỷ luật xác minh mới); README, context-management, evaluation-system, observability, user-rules-runtime đã được dọn sạch tường thuật kiến trúc cũ (chỉ giữ lại đối chiếu lịch sử có đánh dấu). Cấu hình Coordinator và các đường tương thích phiên đều đã bị xóa; Arbiter hiện cố ý thống nhất dùng mô hình Default, không mở cấu hình vai trò độc lập.
> **Làm rõ ngữ nghĩa thiết kế (vòng đánh giá thứ tư/năm)**: ① điểm tiêu thụ phản hồi của writer **chính là** thao tác cấu trúc tiếp theo (expand_arc/append_volume/update_compass sau khi được novel_context tham chiếu thì xóa) —— nó là "khuyến nghị cho đề cương tiếp theo" (nguyên văn commit schema), không phải tín hiệu điều phối tức thời; lệch hướng nghiêm trọng giữa arc đi qua kênh đánh giá editor và can thiệp người dùng. Sách phi phân tầng không có thao tác cấu trúc, **commit không ghi phản hồi của nó xuống đĩa** (tránh sự kiện rác không bao giờ có consumer vĩnh viễn; mirror giá trị trả về được giữ lại để chẩn đoán). ② rule_violations đã khép vòng: commit ghi đĩa theo hai đường (**siêu dữ liệu chất lượng best-effort**, không có tính nhất quán mạnh cùng cấp với commit chương —— nếu sập ngay sau khi pending_commit được xóa sẽ thiếu một bản ghi, chấp nhận được) → novel_context(chapter=N) tiêm vào → editor tiêu thụ theo ánh xạ §kiểm tra cơ học. ③ Bảo vệ sập PendingSteer là **một lần persistent hóa đang thực hiện, theo cơ chế best-effort**: lần persistent đầu tiên thất bại sẽ dừng phân xử rõ ràng; được bảo vệ trong kỳ phân xử, khi áp dụng action thất bại, thoát bình thường/Abort; hai cửa sổ không đảm bảo được nêu rõ —— (a) sau khi chuyển dispatch vào hàng đợi thực thi trong bộ nhớ (e.next), trước khi worker khởi động bị buộc dừng tiến trình (cửa sổ mili-giây, defer không thực thi); (b) can thiệp đồng thời đang chờ interMu (chưa ghi vào slot). Người dùng có mặt có thể cảm nhận, chi phí gửi lại tính bằng giây, không vì vậy xây intent/FIFO persistent. ④ Thất bại phân xử lúc khởi động không phải ngõ cụt (bổ túc lỗi thực tế 2026-07-12: tài khoản provider mất hiệu lực khiến plan_start thất bại, mọi đường khôi phục đều không chạy được): StartPrompt (sự kiện đầu vào) đổi thành ghi đĩa **trước** phân xử; khi plan_start chưa hoàn thành, engine planStartFallback dựa vào nó để phân xử bù tại chỗ —— retry của lần phân xử đầu không vi phạm "khôi phục không làm lại phân xử đã có"; phân xử bù thất bại sẽ tạm dừng rõ ràng và echo, bản ghi kiểm toán của phân xử thất bại mang trường error (DecisionRecord.Error).
> Tài liệu này được giữ lại làm bản ghi quyết định thiết kế; kiến trúc hiện tại xem mục kiến trúc trong README và docs/engine-rfc.md. Liên quan: docs/voice-layer.md (đã triển khai).

## Một, động cơ: giả định lỗi thời bị bao quanh bởi các bản vá

Giả định nền tảng của dự án —— "một Prompt, một vòng lặp dài LLM thường trú điều khiển cả cuốn sách" —— đã lỗi thời. Sau tái cấu trúc Hybrid tháng 4, quyền quyết định thực tế nằm trong tay `flow.Route`, Coordinator trong 90% lời gọi của vòng lặp chính chỉ làm **chuyển tiếp nguyên trạng**. Hệ sinh thái vá lỗi phải trả để "duy trì một phiên LLM không nên dừng":

1. StopGuard + kỹ nghệ nội dung động blockMessage
2. Giao thức chỉ thị lặp lại Dispatcher ("ban hành lần thứ N")
3. Quy chuẩn hành vi coordinator.md (trường hợp đặc biệt khôi phục / loại truy vấn phải dispatch cùng vòng / không được dùng dừng máy để biểu đạt lập trường)
4. completePhaseGate / writerExpandedChapterGate
5. MaxTurns=100_000
6. FlowBoundaryHook
7. Trường hợp đặc biệt bất đồng kết thúc

**Tất cả hệ con mới trong nội bộ dự án (import/simulation/cocreate/userrules) đều không đi qua Coordinator, đã dùng mô thức "Host trực tiếp điều phối + LLM làm hàm"** —— phương án này thống nhất luồng chính về mô thức mà chính nó đã xác minh.

## Hai, hình thái mục tiêu

```
Entry
  ↓
Host(giữ tên package; bên trong thêm EngineLoop, không đổi tên thuần cơ học)
  ├─ đọc Store → flow.Route → chạy trực tiếp Worker
  ├─ cảnh ngữ nghĩa rõ ràng → gọi hàm Arbiter
  └─ chiếu sự kiện / ngân sách / điểm dừng / thông báo(giữ responsibility hiện tại)
  ↓
Workers(architect / writer / editor, tự chủ nội bộ, giữ checkpoint-delta guard)
  ↓
Tools → Store(nguồn sự thật duy nhất)
```

Trách nhiệm: **Route quản mọi bước tiếp theo có thể tra bảng; Arbiter quản phán đoán ngữ nghĩa có ranh giới rõ; Worker quản sáng tác mở; Engine thực thi quyết định, không tham gia phán đoán văn học; Observer/Diag chỉ quan sát.**

Tóm tắt trạng thái cuối bằng một câu: **một Engine xác định tuần tự, ba Worker tự chủ, một số ít hàm Arbiter theo nhu cầu, một lớp sự thật hệ thống tệp.**

### Đối xứng hai mặt phẳng (dự kiến ghi vào architecture.md như luật thép mới)

```
Mặt phẳng xác định:  flow.LoadState   → flow.Route     → Instruction   (kiểm thử đặc tả vét cạn)
Mặt phẳng ngữ nghĩa: arbiter.Collect* → arbiter.Decide* → XxxDecision   (quyết định ghi đĩa + hồi quy eval)
              └── thu thập sự kiện(IO) ──┘└── lõi quyết định(có thể phát lại ngoại tuyến) ──┘└── Engine thực thi ──┘
```

## Ba, cảnh Arbiter (tập cuối cùng, giữ tối thiểu)

| Cảnh | Kích hoạt | Ghi chú |
|------|------|------|
| `plan_start` | Khởi động sách mới | Chọn planner short/long + mở rộng yêu cầu quá ngắn |
| `intervention` | Can thiệp người dùng | Truy vấn / quy tắc dài hạn / điều chỉnh cấu trúc cốt truyện / làm lại phần đã viết / làm lại hoặc từ chối sau khi hoàn bản |
| `worker_failure` | Worker báo lỗi **và phân loại xác định không còn đường ra** | Mạng/tham số/thiếu artifact tiền đề v.v. do mã xác định phân loại trước, không gửi Arbiter |
| `deadlock` | Sau vòng trước vẫn sinh cùng một chỉ thị định tuyến | Ngữ nghĩa đếm và kết thúc xem §tám câu hỏi bắt buộc 5 |
| `completion_dispute` | **ứng viên, có bằng chứng mới thêm** | Phán định kết thúc cuối volume đã do Route dispatch architect (nhánh 10) đảm nhiệm; chỉ bất đồng giữa chừng "cấu trúc chưa tới biên nhưng câu chuyện nên kết" mới cần, tần suất thực tế chưa biết, không xây trước |

Tổng kết hoàn bản không phải phân xử, là nhiệm vụ sinh nội dung —— do Engine dispatch trực tiếp editor hoặc một lần gọi LLM thông thường hoàn thành, không chiếm cảnh Arbiter.

## Bốn, thiết kế Arbiter

### 4.1 Kiểu Decision theo từng cảnh (sửa đổi v3: từ bỏ cấu trúc vạn năng)

```go
// Kiểu con dùng chung, ngăn drift giữa các cảnh
type DispatchDecision struct {
    Instruction flow.Instruction
    Expect      DispatchExpect // xem §năm
}

type PlanStartDecision struct {
    Planner string // architect_long | architect_short
    Task    string // chứa yêu cầu đã mở rộng
    Reason  string
}

type InterventionDecision struct {
    Answer   string
    Rules    string
    Hold     *AdvanceHoldOp
    Reopen   *ReopenOp
    Dispatch *DispatchDecision
    Reason   string
}

type FailureDecision struct {
    Action   string // retry | reroute | abort
    Dispatch *DispatchDecision
    Reason   string
}
```

Bản ghi tiến hóa: danh sách action (kiểm tra thứ tự thừa, mảng đa hình dễ lỗi) → cấu trúc phẳng vạn năng (không biểu đạt được thứ tự bất hợp pháp, nhưng tổ hợp bất hợp pháp phải dựa vào ma trận cảnh × action để kiểm tra) → **kiểu theo từng cảnh (action không khớp cảnh không thể biểu đạt, kiểm tra ma trận biến mất, schema đơn cảnh nhỏ hơn, đầu ra LLM ổn hơn, eval có thể tách theo cảnh)**. Validate thu hẹp thành kiểm tra sự kiện theo từng kiểu (ràng buộc phase v.v.).

### 4.2 API: mỗi cảnh một cặp hàm rõ ràng

```go
func CollectInterventionFacts(st *store.Store) InterventionFacts        // biên IO, cùng kỷ luật với flow.LoadState
func DecideIntervention(ctx, model, facts, text) (InterventionDecision, error) // ngoài yêu cầu mô hình do executor thống nhất quản lý thì không IO, có thể phát lại ngoại tuyến
// Các cảnh còn lại cùng dạng một cặp; hình dạng Collect/Decide thống nhất, không xây framework Question/Decision chung
```

- **Đường thất bại**: executor cấu trúc hóa thống nhất chọn JSON Schema native hoặc contract prompt theo năng lực mô hình; lỗi định dạng/Schema ở chế độ prompt và lỗi kiểm tra nghiệp vụ ở cả hai chế độ sẽ mang nguyên nhân chính xác giao cho mô hình sửa, vòng đời chỉ do `context` kiểm soát. Vi phạm contract native, từ chối trả lời, bị cắt, kết thúc lỗi và lỗi yêu cầu không thể retry trả về rõ ràng ngay; can thiệp không sinh ghi, khởi động báo lỗi rõ ràng, failure/deadlock tạm dừng thận trọng
- **Bộ nhớ can thiệp**: decisions.jsonl kiêm lịch sử can thiệp, `CollectInterventionFacts` đưa vào tóm tắt N phán quyết gần nhất
- **Mô hình**: Arbiter thống nhất dùng Default, không phơi bày role độc lập; chỉ khi xuất hiện nhu cầu năng lực hoặc chi phí rõ ràng mới mở rộng contract cấu hình

### 4.3 Kiểm toán (nhỏ và ổn định; kiểm toán ≠ nguồn khôi phục)

```json
{"schema_version":1,"id":"...","kind":"intervention","checkpoint_seq":123,
 "input":"...","facts":{...},"decision":{...},"reason":"...","duration_ms":1200}
```

(token/chi phí không nằm trong bản ghi —— mô hình phân xử được bọc qua usageTrackedModel, usage thống nhất vào UsageTracker/ngân sách, cùng một hệ sổ sách với các Worker.)

- facts chỉ lưu sự kiện cấu trúc hóa + tóm tắt + tham chiếu artifact/checkpoint, **không sao chép chính văn, không lưu gói ngữ cảnh đầy đủ**; giới hạn kích thước từng bản ghi, vượt giới hạn thì cắt ngắn và đánh dấu
- **input giữ lại trong bản ghi** (cần cho phát lại ngoại tuyến `Decide*(facts, input)` —— kiểm toán không có input thì không thể hồi quy); khử nhạy cảm xảy ra tại **biên diag export**, không xảy ra khi ghi đĩa
- Nhật ký kiểm toán không phải event sourcing, cũng không phải nguồn dữ liệu khôi phục

## Năm, giao thức commit trạng thái (vòng lặp Engine tuần tự)

```
Đọc sự kiện → Route / Arbiter tạo quyết định → đối chiếu tiền điều kiện → thực thi action
       → Worker chạy → tính lại hậu điều kiện Route → vòng tiếp theo
```

- **Bất biến: trạng thái điều khiển chỉ thay đổi tuần tự tại biên Engine.** Can thiệp có thể tư vấn song song trong lúc Worker chạy (chỉ đọc an toàn, người dùng thấy echo Answer/Reason trong vài giây), nhưng **action thay đổi control state (hold/reopen/dispatch) đi vào hàng đợi Engine, sau khi đối chiếu ở biên mới commit**; answer (không trạng thái) và rules (mặt phẳng nội dung, chương này dùng rule cũ chương sau có hiệu lực chính là ngữ nghĩa) thực thi tức thì
- Mỗi Dispatch mang snapshot thời điểm Collect, đối sổ ở biên, không khớp → bỏ, ghi `decision_stale`, hỏi lại với sự kiện mới:

```go
type DispatchExpect struct {
    CheckpointSeq int64
    Phase         domain.Phase
    Flow          domain.FlowState
    QueueHead     int
}
```

- Tiền điều kiện rõ ràng ưu tiên hơn Store hash toàn cục (dễ đọc, dễ chẩn đoán); không làm global digest

## Sáu, mô hình khôi phục (chỉ khôi phục sự kiện, không khôi phục phiên)

```
Khởi động → đọc Progress → đọc Checkpoint mới nhất → tra PendingSteer/AdvanceHold/giấy phép chương → đối sổ Gate → Route → tiếp tục chạy Worker
```

Khôi phục plan_start phụ thuộc một sự kiện persistent duy nhất (trong RunMeta), **ghi sự kiện trước phân xử, rồi mới bắt đầu thực thi**:

```go
type PlanStartRecord struct {
    RawPrompt   string
    Planner     string
    PlannerTask string
    DecisionID  string // liên kết bản ghi kiểm toán
    Status      string // decided | dispatched | done —— hiển thị hóa trạng thái trung gian của transaction khởi động
}
```

Sập tại bất kỳ điểm nào: nếu Record tồn tại thì tiếp tục theo Status, không tư vấn lặp; nếu thiếu Record thì xem như sách mới hỏi lại (hỏi lại chấp nhận được, kiểm toán để lại hai bản ghi).

## Bảy, lộ trình di trú (v3 sắp xếp lại: Engine đi trước, Arbiter nối sau)

Căn cứ điều chỉnh thứ tự: "Arbiter đi trước" ban đầu cần một bộ pipeline chuyển tiếp (phán quyết giả trang thành chỉ thị Host qua steering để đút cho Coordinator); **nếu Engine hạ cánh trước thì toàn bộ pipeline đó không cần xây**, Arbiter nối trực tiếp với executor Engine. Mỗi bước đều xóa thứ gì đó, không xây cầu tạm; lo ngại "quyền chọn hai não" được thứ tự này triệt tiêu về mặt cấu trúc.

| # | Bước | Trạng thái |
|---|------|------|
| 0 | Hạng mục vô điều kiện: bổ sung planning vào Router (đặc tả vét cạn đi trước); kiểm toán decisions.jsonl. Cải tiến triển khai: danh tính planner suy ra từ `RunMeta.PlanningTier` sẵn có, không cần cơ chế bản ghi mới | ✅ 2026-07-12 |
| 1 | Giao lớp văn phong (docs/voice-layer.md) | ✅ 2026-07-12 |
| 2 | Chốt Step 2 RFC (docs/engine-rfc.md, bảy câu hỏi bắt buộc) | ✅ 2026-07-12 |
| 3 | WorkerRunner: gọi trực tiếp bằng chương trình qua subagent.Runner, sự kiện relay qua ctx ToolProgress | ✅ 2026-07-22 |
| 4-5 | Engine tiếp quản toàn bộ dispatch + nối bốn cảnh Arbiter (plan_start/intervention/failure/deadlock), kết nối trực tiếp executor Engine (trong triển khai phát hiện Engine đi trước khiến toàn bộ pipeline chuyển tiếp steering không cần xây, 4/5 hợp nhất hạ cánh) | ✅ 2026-07-12 |
| 6 | Xóa Coordinator và toàn bộ phụ trợ (thực thi toàn bộ danh sách §mười); kiểm thử tích hợp đầu-cuối-đến-cuối (tool thật viết sách hoàn chỉnh/phân xử thất bại/phân xử bế tắc) | ✅ 2026-07-12 |

## Tám, các câu hỏi bắt buộc của Step 2 RFC (chưa chốt thì không vào bước 3)

1. **Bề mặt trích xuất Worker**: WorkerRunner API; quyền sở hữu và vòng đời của toàn bộ linh kiện lắp ráp build.go —— mô hình vai trò/failover, prompt cache key, ThinkingLevel, UsageRecorder, SessionLogger, Writer ContextManagerFactory, RestorePack, StopGuardFactory, StopAfterTools, chiếu sự kiện lồng nhau của Observer
2. **Vòng đời Engine**: khởi động/tạm dừng/hủy/khôi phục; đảm bảo đơn Worker tuần tự; chuyển đổi runtime /model và thinking
3. **Hoàn thiện giao thức commit trạng thái**: tổng quát hóa đối sổ Expect của §năm cho mọi cảnh; danh sách tiền điều kiện Engine sau khi tháo Gate
4. **Phân loại học lỗi**: phân loại xác định (retry/reroute/terminal) đi trước, chỉ trường hợp không có đường ra mới gửi `worker_failure`; phân tầng với retry ở lớp agentcore
5. **Giao thức bế tắc**: cùng một `Agent+Task` liên tục tái xuất hiện thì chứng tỏ hậu điều kiện định tuyến chưa thỏa; checkpoint trung gian trong Worker không reset về 0; Arbiter quyết định retry không reset về 0; tư vấn 3 lần, hard fuse 5 lần.
6. **Ngữ nghĩa sập**: cách phán định Worker trước đã sinh sự kiện hữu hiệu hay chưa
7. **Nghiệm thu prototype**: đối chiếu từng vị trí với hiện trạng ở năm mục Observer/Usage/Context/chuyển đổi mô hình/khôi phục

## Chín, sổ giá trị

| Chiều | Hiện trạng | Trạng thái cuối |
|------|------|------|
| Chi phí LLM mỗi chương | Mỗi biên một lần gọi chuyển tiếp | Loại bỏ; lớp vấn đề chuyển tiếp thất bại biến mất |
| Khả năng kiểm thử phân xử | ~gần như không (lẫn trong phiên dài) | Phát lại ngoại tuyến theo cảnh + hồi quy eval |
| Phản hồi can thiệp | Đợi biên chương (cấp phút) | Tư vấn tức thời, control state commit ở biên |
| Độ phức tạp | Hệ sinh thái bảy bản vá | Giảm ròng 1500+ dòng, ba lớp vấn đề nghỉ hưu |
| Khôi phục sập | Phát lại phiên + giao thức khôi phục | Đọc store tiếp tục chạy |
| Rủi ro thời kỳ chuyển tiếp | — | Tập trung ở bước 3/4 (trích xuất Worker), được RFC + cổng prototype kiểm soát; bước 0/1 thành lập vô điều kiện |

## Mười, danh sách xóa ở trạng thái cuối

Coordinator và logic khôi phục phiên của nó, Coordinator StopGuard, giao thức Dispatcher steering và giao thức văn bản `[Host ban hành chỉ thị]`, FlowBoundaryHook, completePhaseGate / writerExpandedChapterGate (kiểm tra chuyển thành tiền điều kiện Engine), MaxTurns=100_000, toàn bộ quy chuẩn hành vi coordinator.md.

## Mười một, phản đối và bản ghi đánh giá

1. *"Độ đúng của phân xử sẽ không tăng"* —— đúng; khác biệt thực sự là tập trung/biên tập/tiền kiểm tra vs ký ức phiên, giá trị ròng hơi tốt hơn và lần đầu có thể đo được
2. *"Hiện trạng chạy được, động vào mặt phẳng điều khiển là mạo hiểm"* —— thừa nhận; nền móng được xây vì điều này, từng bước có thể dừng có thể lùi
3. *"Kiến trúc không phải nút thắt, chất lượng nội dung mới là"* —— đúng một phần, lớp văn phong đi trước
4. **Đánh giá một (2026-07-12)**: thiếu giao thức commit trạng thái → §năm; Step 2 mỏng → §tám câu hỏi bắt buộc + cổng prototype; trình tự khởi động → §sáu; tuyên bố "trạng thái bất hợp pháp không thể biểu đạt" quá mức → bản ghi tiến hóa 4.1; lỗi sự kiện whitelist vai trò arbiter → 4.2; vệ sinh kiểm toán → 4.3
5. **Đánh giá hai (2026-07-12)**: kiểu Decision theo từng cảnh (tiếp thu, 4.1); sắp xếp lại thứ tự di trú Engine đi trước (tiếp thu, §bảy); commit biên thống nhất (tiếp thu, §năm); PlanStartRecord (tiếp thu, §sáu); không đổi tên host (tiếp thu); đề xuất số chữ để lại file giao thức (tiếp thu, xem voice-layer). **Ý kiến bảo lưu**: kiểm toán phải giữ input nếu không không thể phát lại (4.3); completion_dispute hạ xuống thành cảnh ứng viên (§ba)

## Mười hai, kỷ luật và không làm

**Kỷ luật**: ① điểm quyết định mới trước hết đi qua phép phân loại ba phần §hai, cấm mặc định "thêm rule vào prompt"; ② mỗi điểm quyết định LLM bắt buộc có danh sách sự kiện/output cấu trúc hóa/đường degrade/kiểm toán ghi đĩa; ③ chỉ viết guardrail sự kiện không viết guardrail hành vi; ④ bất biến khai báo ưu tiên hơn script thủ tục; ⑤ thay đổi mặt phẳng điều khiển sửa đặc tả vét cạn trước rồi mới sửa triển khai.

**Không làm**: viết lại event sourcing; trừu tượng hóa Store cho multi-tenant giả tưởng; workflow DSL chung; State Digest toàn cục; đổi tên package host; xây trước completion_dispute.