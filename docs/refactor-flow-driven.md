# Đề xuất tái cấu trúc: Hybrid Coordinator — Định tuyến Host × Phán định LLM

> **Tài liệu lịch sử, đã bỏ.** Hybrid Coordinator đã được kiến trúc Engine + Arbiter thay thế vào 2026-07-12; thiết kế hiện hành xem `docs/architecture.md`, `docs/engine-rfc.md`. Bài viết này chỉ giữ lại hồ sơ diễn tiến quyết định, không được dùng làm căn cứ triển khai.
>
> Trạng thái ban đầu: **đã chấp thuận và triển khai** (2026-04-20)
> Thời gian nghiên cứu: 2026-04-20
> Tài liệu hiện hành tương ứng: `docs/architecture.md` §2 / §3 / §7 / §8 / §13 đã được cập nhật đồng bộ
>
> **Tài liệu này là bản thảo thứ hai.** Vấn đề của phương án cấp tiến trong bản thảo đầu tiên (xóa hoàn toàn Coordinator) được trình bày chi tiết ở Phụ lục A; giữ lại phần này để tránh đi lại đường vòng.
>
> Kết quả triển khai:
> - Tạo mới `internal/host/flow/` (router.go / state.go / dispatcher.go / router_test.go, 15 nhánh unit test đều qua)
> - `internal/host/reminder/` xóa `flow.go` / `queue_guard.go` / `book_complete.go`; giữ lại StopGuard và Guard của sub-agent
> - `assets/prompts/coordinator.md` rút từ 88 dòng xuống ~45 dòng (thu hẹp trách nhiệm còn thực thi chỉ thị Host + phán định + chọn kiểu khi khởi động)
> - `internal/host/resume.go` được đơn giản hóa mạnh, chỉ sinh label và prompt ngắn; bước tiếp theo cụ thể do Router điều phối sau TurnEnd đầu tiên
> - `internal/store/` thêm các phương thức hỗ trợ `HasArcReview` / `HasArcSummary` / `HasVolumeSummary` / `CheckConsistency`
> - Sửa luôn bug agent state trong `observer.go` không còn bị kẹt ở working

---

## 1. Bối cảnh

### 1.1 Định vị dự án

```
agentcore       — framework agent phổ dụng
litellm         — cổng LLM phổ dụng
ainovel-cli     — agent dọc cho sáng tác tiểu thuyết (dự án này)
```

Không gian quyết định của agent dọc là **đóng**: lưu đồ cố định, nhánh hữu hạn, được dẫn dắt bởi sự kiện. Triết lý thiết kế của agent phổ dụng ("đặt cược vào năng lực mô hình") khi áp vào kịch bản dọc có dấu hiệu quá thuần túy.

### 1.2 Mục tiêu người dùng (theo mức ưu tiên)

1. **Ổn định** — tiếp tục viết liên tục, không gián đoạn vì lỗi định tuyến
2. **Hưởng lợi từ nâng cấp LLM** — kiến trúc không đối kháng với năng lực mô hình
3. **Tận dụng đầy đủ năng lực đa agent** — phân công chức năng rõ ràng

Đề xuất này tạo **cải thiện Pareto** giữa ba mục tiêu (không hy sinh mục tiêu nào để đổi lấy mục tiêu khác).

---

## 2. Khảo sát hiện trạng

### 2.1 Phân loại các điểm quyết định của Coordinator

Trích xuất từng điểm quyết định trong `coordinator.md`:

| # | Điểm quyết định | Tính chất | Tần suất |
|---|---|---|---|
| 1 | Khi khởi động chọn architect_long / short | Phán định (hiểu ngữ nghĩa) | 1 lần mỗi cuốn |
| 2 | Mở rộng đầu vào (<20 chữ tự động bổ sung) | Phán định (sáng tạo) | 0-1 lần mỗi cuốn |
| 3 | Vòng lặp bổ sung quy hoạch | Định tuyến (dẫn dắt bởi sự kiện) | 1-3 lần |
| 4 | Bước tiếp theo sau mỗi chương commit | **Định tuyến** | **1-2 lần mỗi chương** |
| 5 | Thực thi từng bước đánh giá cuối arc | Định tuyến | 3-5 lần mỗi arc |
| 6 | Rẽ nhánh verdict đánh giá | Định tuyến (đã mã hóa, xem §2.3) | 1 lần mỗi arc |
| 7 | Xử lý can thiệp người dùng | Phán định (bắt buộc LLM) | Bất kỳ |
| 8 | Điều phối lại khi sub-agent báo lỗi | Định tuyến | Thỉnh thoảng |
| 9 | Xuất tổng kết khi hoàn thành toàn sách | Định tuyến | 1 lần |

**Kết luận**: Trong 9 điểm quyết định, 6 điểm là định tuyến thuần túy (tra bảng), 3 điểm mới thực sự cần LLM phán định. **Tần suất định tuyến cao hơn phán định rất nhiều** (1-2 lần mỗi chương vs vài lần mỗi cuốn).

### 2.2 Kênh Reminder đã là bán thành phẩm mã hóa quy trình

Các generator dưới `internal/host/reminder/` mỗi vòng đều sinh ra **chỉ thị cụ thể đến hành động** dựa trên sự kiện:

- `flow.go` → `"Hiện tại flow=writing, next_chapter=37. Hãy gọi trực tiếp subagent(writer, \"viết chương 37\")..."`
- `queue_guard.go` → `"Hiện tại flow=rewriting, hàng đợi cần xử lý: [3,5]. Hãy lập tức gọi writer viết lại từng chương..."`
- `book_complete.go` → `"Toàn sách đã hoàn thành. Hãy xuất tổng kết toàn sách..."`

**Kiến trúc hiện tại tồn tại double dispatch**:
```
Tầng quy tắc: coordinator.md định nghĩa "nếu A thì B"
  ↓
Tầng Reminder: mỗi vòng cụ thể hóa quy tắc theo sự kiện → sinh "bây giờ hãy làm B"
  ↓
Tầng LLM: đọc reminder sinh tool_call (về cơ bản chỉ là nhắc lại reminder)
  ↓
SubAgent thực thi
```

**LLM thực tế chỉ đang "thực thi" chỉ thị mà Reminder đưa cho nó**. Khâu trung gian này vừa tiêu tốn tokens, vừa đưa vào tính bất định (LLM có thể không hoàn toàn tuân thủ reminder, ví dụ lỗi định tuyến mid quan sát được).

### 2.3 Tầng công cụ từng gánh quá nhiều phán đoán ngữ nghĩa

- Triển khai cũ của `save_review` từng ghi đè Editor verdict theo ngưỡng điểm cố định và trạng thái contract; nay đã xóa, phán định văn học trả về Editor, công cụ chỉ làm kiểm tra giao thức và ánh xạ trạng thái nguyên tử
- `commit_chapter.CheckArcBoundary()`: tính tức thời `arc_end / needs_expansion / needs_new_volume`
- `commit_chapter.applyCompletion()`: phán định tức thời `book_complete`
- `CommitResult` trả về 17 trường sự kiện

**Kết luận**: Các bất biến lưu trữ và giai đoạn mang tính xác định ở lại tầng công cụ, phán đoán văn học và ngữ nghĩa giao cho mô hình.

### 2.4 Chi phí thực tế hiện tại

Số lượt LLM Coordinator mỗi chương:
- **1-2 turns mỗi chương** (đọc system prompt ~3000 tokens + reminder ~200 tokens + lịch sử + CommitResult ~500 tokens → sinh tool_call ~50 tokens)
- Tiểu thuyết dài 200 chương khoảng **200-400 turns** gọi Coordinator LLM
- Trong đó **~90% là định tuyến thuần túy** (LLM nhắc lại reminder), **~10% là phán định**

**Mỗi chương có ~3500-7000 tokens tiêu tốn cho quyết định Coordinator, 95% là dư thừa** (Reminder đã tính ra đáp án).

---

## 3. Phương án thiết kế: Hybrid Coordinator

### 3.1 Ý tưởng cốt lõi

**Chuyển quyết định quy trình từ LLM sang Host, nhưng giữ Coordinator làm nút phán định và kênh thực thi chỉ thị**.

```
┌──────────────────────────────────────────────────────────┐
│                   Điểm vào (TUI / headless)               │
└────────────────────────────────┬─────────────────────────┘
                                 │ Start / Resume / Steer
┌────────────────────────────────▼─────────────────────────┐
│                            Host                            │
│                                                             │
│   ┌──────────────────────────────────────────────────┐     │
│   │  Flow Router (lõi mới thêm)                       │     │
│   │  ───────────                                      │     │
│   │  Đăng ký sự kiện Coordinator: kích hoạt khi subagent tool trả về │     │
│   │  Hàm thuần: route(Progress, Checkpoint, Boundary) │     │
│   │      → NextInstruction                             │     │
│   │  Có chỉ thị → coordinator.FollowUp(chỉ thị)        │     │
│   │  Không có chỉ thị (kịch bản phán định) → không can thiệp, để LLM tự chủ │     │
│   └──────────────────────────────────────────────────┘     │
│                                                             │
│   Giữ lại: Lifecycle API / Observer / Usage Tracker         │
│   Giữ lại: resume.go (đơn giản hóa, logic lõi không đổi)    │
└────────────────────────────────┬─────────────────────────┘
                                 │
┌────────────────────────────────▼─────────────────────────┐
│                    Coordinator Agent (LLM)                  │
│                                                             │
│   Trách nhiệm thu hẹp còn hai loại:                          │
│   1. Nhận chỉ thị Host FollowUp → sinh tool_call tương ứng   │
│   2. Khi user Steer đến thì tự chủ phán định (truy vấn/sửa đổi/đánh giá) │
│                                                             │
│   coordinator.md: 88 dòng → ~25 dòng                         │
│   MaxTurns: 1000 giữ lại (phản hồi user steer + thực thi chỉ thị Host) │
└────────────────────────────────┬─────────────────────────┘
                                 │
                                 ▼
         ┌──────────────────────┼───────────────────────┐
         ▼                      ▼                       ▼
    ┌────────┐             ┌────────┐             ┌────────┐
    │Architect│             │ Writer │             │ Editor │
    └────────┘             └────────┘             └────────┘
```

### 3.2 Phân chia lại trách nhiệm

| Tầng | Làm gì | Không làm gì |
|---|---|---|
| **Host / Flow Router** | Đọc sự kiện → định tuyến bằng hàm thuần → chỉ thị FollowUp | Tự gọi SubAgent (vẫn thông qua Coordinator) |
| **Coordinator** | Thực thi chỉ thị Host + phán định can thiệp người dùng + chọn planner khi khởi động | Tự chủ quyết định "bước tiếp theo làm gì" |
| **SubAgent (A/W/E)** | Công việc chuyên môn của từng bên | Không đổi |
| **Tầng công cụ** | Ghi đĩa nguyên tử + trả về sự kiện | Không đổi |

**Bất biến then chốt**:
- ✅ Coordinator vẫn là một agent run liên tục, giữ lại "cảm nhận liên tục" đối với toàn sách
- ✅ User Steer vẫn thông qua `coordinator.Inject()`, giữ lại khả năng ngắt ngay lập tức
- ✅ SubAgentTool vẫn do LLM gọi (đi theo đường nguyên sinh agentcore), event stream / ContextManager / chuyển đổi mô hình đều không đổi
- ✅ Không sửa agentcore

### 3.3 Logic cụ thể của Flow Router

```go
// internal/host/flow/router.go

type NextInstruction struct {
    Agent  string   // architect_long / architect_short / writer / editor
    Task   string   // Mô tả nhiệm vụ giao cho sub-agent
    Reason string   // Lý do cho Coordinator xem (tùy chọn, tiện debug)
}

type RouterState struct {
    Progress        *domain.Progress
    LatestCheckpoint *domain.Checkpoint
    // Ranh giới arc của chế độ phân tầng (tính khi chương trước đã hoàn thành)
    LastCompleted   int
    ArcBoundary     *store.ArcBoundary
    HasArcReview    bool
    HasArcSummary   bool
    // Các mục thiếu trong thiết lập nền tảng
    FoundationMissing []string
}

// Route trả về chỉ thị bước tiếp theo. Trả về nil nghĩa là để Coordinator tự chủ phán định (kịch bản phán định).
func Route(s RouterState) *NextInstruction {
    p := s.Progress

    // 0. Trạng thái kết thúc: để LLM xuất tổng kết, không định tuyến
    if p.Phase == domain.PhaseComplete {
        return nil
    }

    // 1. Giai đoạn quy hoạch: phán định (chọn planner) do LLM làm, không định tuyến
    if p.Phase != domain.PhaseWriting {
        return nil
    }

    // 2. Giai đoạn viết
    // 2a. Ưu tiên hàng đợi viết lại/đánh bóng
    if len(p.PendingRewrites) > 0 {
        ch := p.PendingRewrites[0]
        verb := "viết lại"
        if p.Flow == domain.FlowPolishing {
            verb = "đánh bóng"
        }
        return &NextInstruction{
            Agent:  "writer",
            Task:   fmt.Sprintf("%schương %d", verb, ch),
            Reason: fmt.Sprintf("Hàng đợi PendingRewrites còn %d chương", len(p.PendingRewrites)),
        }
    }

    // 2b. Đang review: không định tuyến, để Coordinator rẽ nhánh verdict theo kết quả save_review
    if p.Flow == domain.FlowReviewing {
        return nil
    }

    // 2c. Hậu xử lý cuối arc trong chế độ phân tầng
    if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
        b := s.ArcBoundary
        if !s.HasArcReview {
            return &NextInstruction{
                Agent:  "editor",
                Task:   fmt.Sprintf("Thực hiện đánh giá cấp arc cho quyển %d arc %d", b.Volume, b.Arc),
                Reason: "Đánh giá cuối arc chưa hoàn thành",
            }
        }
        if !s.HasArcSummary {
            return &NextInstruction{
                Agent:  "editor",
                Task:   fmt.Sprintf("Sinh tóm tắt arc cho quyển %d arc %d", b.Volume, b.Arc),
                Reason: "Tóm tắt arc chưa hoàn thành",
            }
        }
        if b.NeedsExpansion {
            return &NextInstruction{
                Agent:  "architect_long",
                Task:   fmt.Sprintf("Mở rộng quyển %d arc %d (save_foundation type=expand_arc)", b.NextVolume, b.NextArc),
                Reason: "Khung xương arc tiếp theo cần mở rộng",
            }
        }
        if b.NeedsNewVolume {
            return &NextInstruction{
                Agent:  "architect_long",
                Task:   "Đánh giá và thực thi save_foundation(type=append_volume) hoặc mark_final",
                Reason: "Kết thúc quyển cần quyết định có thêm quyển mới hay không",
            }
        }
    }

    // 2d. Tiếp tục viết bình thường
    next := p.NextChapter()
    return &NextInstruction{
        Agent:  "writer",
        Task:   fmt.Sprintf("Viết chương %d", next),
        Reason: "Tiếp tục viết",
    }
}
```

**Đặc tính hàm**:
- Hàm thuần (nhập RouterState, xuất NextInstruction)
- Có thể unit test (cho trạng thái xác định, assert kết quả định tuyến)
- **Trả về nil là hợp lệ** — nghĩa là "đây là kịch bản phán định, hãy để LLM tự chủ"

### 3.4 Thời điểm kích hoạt

Host đăng ký sự kiện `agentcore.EventToolExecEnd`:

```go
coordinator.Subscribe(func(ev agentcore.Event) {
    if ev.Type == agentcore.EventToolExecEnd && ev.Tool == "subagent" && !ev.IsError {
        // SubAgent vừa trả về → đọc trạng thái mới nhất → định tuyến
        h.flowRouter.Dispatch()
    }
})
```

```gofunc (r *FlowRouter) Dispatch() {
    state := r.loadState()
    instruction := Route(state)
    if instruction == nil {
        return // Cảnh phán định, để LLM tự chủ
    }
    msg := formatInstruction(instruction)
    _ = r.coordinator.FollowUp(agentcore.UserMsg(msg))
}

func formatInstruction(i *NextInstruction) string {
    return fmt.Sprintf(
        "[Host ban hành chỉ thị] Bước tiếp theo: gọi subagent(%s, %q)\n"+
        "Lý do: %s\n"+
        "Đây là chỉ thị rõ ràng của tầng quy trình, hãy thực hiện ngay, không gọi novel_context trước, không xuất suy luận trước.",
        i.Agent, i.Task, i.Reason,
    )
}
```

### 3.5 Tính phản hồi và đồng thời

**Đường dẫn Steer của người dùng** (không thay đổi):
```
Steer → coordinator.Inject(UserMsg("[người dùng can thiệp] xxx"))
```

- Đang chạy: thông điệp được chèn vào hàng đợi run hiện tại
- Idle: resume run
- Paused: xếp hàng

**Tính đồng thời của chỉ thị định tuyến + Steer**:
- Cả hai đều đi vào hàng đợi thông điệp của Coordinator, xử lý theo thứ tự nguyên sinh của agentcore
- Nếu Host vừa gửi `FollowUp("[chỉ thị Host] viết chương 37")`, ngay sau đó người dùng Steer `"dừng một chút, chỉnh phong cách"`
  - Coordinator xử lý chỉ thị Host trước? Hay xử lý Steer trước?
  - **Ngữ nghĩa của `Inject` là chen vào đầu hàng đợi hiện tại**, vì vậy Steer được xử lý trước
  - Đây là hành vi mong muốn: can thiệp của người dùng có độ ưu tiên cao hơn điều phối định kỳ của Host

**Tránh xung đột giữa chỉ thị Host và Steer**:
- Flow Router sau khi nhận tín hiệu "Steer đã được inject" sẽ **tạm dừng ngắn** vài turn (để Coordinator xử lý xong Steer rồi mới định tuyến)
- Cảm nhận kết quả xử lý Steer bằng cách subscribe `agentcore.EventMessageEnd` + kiểm tra thay đổi trạng thái Progress

### 3.6 Ví dụ rút gọn coordinator.md

Cắt từ 88 dòng xuống khoảng 25 dòng:

```markdown
Bạn là tổng điều phối viên sáng tác tiểu thuyết.

## Chế độ làm việc của bạn

**Tuyến chính**: Host sẽ gửi thông điệp `[Host ban hành chỉ thị]` sau mỗi lần tác tử con trả về, cho bạn biết bước tiếp theo gọi tác tử con nào làm gì. Khi nhận được chỉ thị, lập tức tạo tool_call tương ứng, không gọi novel_context để suy luận trước, không thuật lại.

**Phán định**: Khi gặp các tình huống sau, bạn cần tự chủ phán đoán (Host sẽ không ban hành chỉ thị, bạn phải chủ động hành động):

### Khi khởi động: chọn người lập kế hoạch

- Mặc định → `architect_long`
- Chỉ khi người dùng yêu cầu rõ ràng truyện ngắn/một tập/tiểu phẩm và giới hạn độ dài trong 25 chương → `architect_short`

Nếu đầu vào của người dùng < 20 chữ, trước tiên bổ sung hướng khác biệt hóa, độc giả mục tiêu, ít nhất một móc câu chuyện phi thông thường trong mô tả task, rồi mới phái phát.

### Steer của người dùng

Định dạng: `[người dùng can thiệp] xxx`

- **Loại truy vấn** (hỏi trạng thái/thiết lập): xuất trực tiếp câu trả lời văn bản, không cần gọi thêm công cụ; Host sẽ tiếp tục phái phát.
- **Loại sửa đổi** (yêu cầu sửa thiết lập/viết lại/điều chỉnh phong cách): đánh giá phạm vi ảnh hưởng:
  - Liên quan thay đổi thiết lập → gọi architect_* làm `save_foundation(type=...)`
  - Liên quan chương đã viết → để công cụ tự động ghi chương mục tiêu vào `PendingRewrites` (có thể nêu ý định viết lại khi gọi writer lần nữa)
  - Chỉ ảnh hưởng phong cách về sau → mô tả ngắn gọn yêu cầu, rồi đính kèm vào mô tả task của writer khi lần sau nhận chỉ thị Host

## Công cụ

- `subagent(agent, task)`: gọi tác tử con
- `novel_context`: chỉ dùng khi truy vấn của người dùng cần, không gọi trước sau khi chỉ thị Host đến

## Tác tử con

- `architect_long` / `architect_short` / `writer` / `editor`

## Cấm

- Khi chỉ thị Host đến, gọi novel_context trước rồi mới hành động
- Tự quyết định bước tiếp theo khi không có Steer của người dùng và không có chỉ thị Host
```

### 3.7 Kênh Reminder tinh gọn đáng kể

**Xóa**:
- `flow.go` (Host FollowUp đã ban hành chỉ thị cụ thể, nhắc nhở định tuyến của Reminder mất giá trị)
- `queue_guard.go` (hàng đợi do Host Router bảo đảm)
- `book_complete.go` (khi Phase=Complete, Host FollowUp chỉ thị xuất tổng kết)

**Giữ lại**:
- `subagent_guards.go` (StopGuard của Writer/Architect/Editor, đảm bảo tác tử con không kết thúc tay trắng)
- Thêm mới một `foundation_reminder.go` nhẹ: giai đoạn lập kế hoạch thông báo cho Coordinator các mục còn thiếu (đây là **thông tin cần cho phán định** chứ không phải chỉ thị định tuyến)

**StopGuard giữ lại**:
- Giữ StopGuard của Coordinator (khi `Phase != Complete` thì chặn end_turn làm tuyến phòng thủ)
- Thêm mới việc inject nhắc nhở khi "đã nhận chỉ thị Host nhưng vòng này chưa gọi subagent tương ứng"

### 3.8 resume.go rút gọn nhẹ

Hiện tại `buildResumePrompt` sinh chỉ thị ngôn ngữ tự nhiên chính xác đến step theo checkpoint (121 dòng).

Kiến trúc mới:
- Khi Resume, trước tiên đọc Progress, Flow Router tính ra `NextInstruction`
- Coordinator nhận một resume prompt **rất ngắn**, rồi chờ chỉ thị FollowUp của Host

```
[khôi phục] Cuốn sách “xxx” đã hoàn thành N chương, bước vào giai đoạn XX.
Vui lòng chờ chỉ thị bước tiếp theo của Host, hoặc xử lý can thiệp của người dùng có thể đã để lại trong thời gian dừng máy.
```

Hầu như toàn bộ logic nhánh được hạ xuống Flow Router (Router vốn đã cần định tuyến theo trạng thái, Resume không cần đường dẫn đặc biệt).

---

## 4. Đánh giá mức độ đạt mục tiêu

### 4.1 Tính ổn định

| Rủi ro | Hiện tại | Kiến trúc mới |
|---|---|---|
| Coordinator chọn sai architect | Đã từng xảy ra (mid định tuyến sai) | Khi khởi động vẫn là phán định, nhưng prompt từ ba mức thành nhị nguyên (đã làm), bề mặt lỗi thu hẹp đáng kể |
| Coordinator không tuân thủ "chỉ nói viết chương N" | Đã từng xảy ra | Host ban hành chỉ thị định dạng cố định, không còn cần LLM sinh mô tả task |
| Coordinator bỏ sót kiểm tra queue_drained | Đã từng xảy ra | Host Router cưỡng chế đi theo thứ tự |
| Sau commit cuối arc, Coordinator quên gọi editor | Có thể | Host Router phát hiện IsArcEnd && !HasArcReview thì trực tiếp phái phát |
| Bỏ sót nhánh khôi phục sau crash | Lỗ hổng đã biết | Máy trạng thái của Flow Router tự nhiên bao phủ mọi nhánh |
| StopGuard chặn liên tiếp 5 lần rồi nâng cấp fatal | Tồn tại | Sau khi chỉ thị Host rõ ràng, LLM rất khó bị chặn liên tục (trừ khi prompt lỗi nghiêm trọng) |

### 4.2 Lợi ích từ nâng cấp LLM

| Chiều | Mức giữ lại |
|---|---|
| Nâng cấp mô hình Writer → chất lượng viết | 100% |
| Nâng cấp mô hình Editor → độ chính xác đánh giá | 100% |
| Nâng cấp mô hình Architect → kế hoạch tinh tế | 100% |
| **Nâng cấp mô hình Coordinator → phán định chuẩn hơn** | **100%** (giữ lại cảnh phán định) |
| ~~Nâng cấp mô hình Coordinator → định tuyến chuẩn hơn~~ | Từ bỏ (tỷ lệ lỗi định tuyến vốn nên là 0, không cần LLM thông minh hơn) |

**Giữ lại quan trọng**: Các cảnh phán định như đánh giá can thiệp người dùng, chọn kiểu người lập kế hoạch, phán đoán ranh giới verdict vẫn do LLM xử lý, trực tiếp hưởng lợi từ nâng cấp mô hình.

### 4.3 Năng lực đa agent

- Số lượng, chức năng, cách lắp ráp SubAgent **hoàn toàn không đổi**
- Dị thể mô hình (coordinator/architect/writer/editor cấu hình độc lập) **hoàn toàn không đổi**
- Coordinator vẫn là run liên tục, giữ lại "góc nhìn toàn bộ sách"
- Phương tiện cộng tác (sản phẩm trong Store) không đổi

### 4.4 Tính phản hồi

- Năng lực ngắt bằng Steer của người dùng thông qua `coordinator.Inject` **được giữ lại hoàn toàn**
- Host Router phái phát chỉ thị khi SubAgent trả về, đi cùng một kênh thông điệp với Steer của người dùng
- Độ ưu tiên của Inject cao hơn FollowUp (ngữ nghĩa của `Inject` là chen hàng), Steer sẽ không bị chỉ thị Host chen mất

### 4.5 Chi phí Token

Hiện tại mỗi chương: Coordinator ~3500-7000 tokens × 1-2 turns = 3500-14000 tokens

Kiến trúc mới mỗi chương:
- Coordinator prompt giảm từ ~3000 tokens xuống ~800 tokens
- Mỗi chương vẫn cần 1 turn (Coordinator đọc chỉ thị FollowUp + sinh tool_call)
- Tổng cộng ~1000-1500 tokens

**Tiết kiệm 60-80%**. Tiểu thuyết dài 200 chương tiết kiệm khoảng 400k-1M tokens (không bằng 100% của phương án cấp tiến, nhưng không hy sinh tính phản hồi và góc nhìn toàn bộ sách).

---

## 5. Ảnh hưởng tới docs/architecture.md

### 5.1 Điều chỉnh nguyên tắc cốt lõi §2

**Nguyên tắc một** (LLM dẫn động vòng lặp chính) → điều chỉnh thành:
```
LLM dẫn động sáng tác và phán định, Host dẫn động định tuyến quy trình.

- Sáng tác và phán định (quyết định cần hiểu ngữ nghĩa, đánh giá chất lượng, nhận diện ý đồ) vẫn để cho LLM
- Định tuyến quy trình (đọc sự thật → tra bảng → phát chỉ thị) do mã Host đảm nhận
- Host không bỏ qua Coordinator để gọi trực tiếp SubAgent, mà ban hành chỉ thị rõ ràng thông qua FollowUp,
  giữ Coordinator làm kênh thực thi chỉ thị và nút phán định
```

**Nguyên tắc hai** (đặt cược vào năng lực mô hình, không đặt cược vào hardcode) → điều chỉnh thành:
```
Đặt cược vào mô hình ở chiều sáng tác và phán định (năng lực phán định của Writer/Editor/Architect/Coordinator),
ở chiều định tuyến quy trình thì biểu đạt bằng code (không gian quyết định của agent theo ngành dọc là đóng, tác vụ tra bảng không có lợi ích từ LLM).
```

### 5.2 Điều chỉnh danh sách cấm §13

- §13.13 "không làm mặt phẳng điều khiển xác định kiểu Host đọc file tín hiệu → inject chỉ thị bước tiếp theo" →
  **sửa cách diễn đạt**: "không dùng file tín hiệu làm IPC (đọc trực tiếp Progress + Checkpoint là đủ), sau khi Host đọc sự thật, ban hành chỉ thị gọi tác tử con rõ ràng thông qua `coordinator.FollowUp`, là định tuyến ngành dọc hợp lý"
- §13.14 "không hardcode trạng thái máy cho chuyển dịch Flow" →
  **sửa cách diễn đạt**: "Nhãn Flow vẫn chỉ do công cụ cập nhật (không viết máy trạng thái 'nếu A thì SetFlow(B)' trong Host), nhưng Flow Router có thể dựa trên Flow và các sự thật khác để quyết định bước tiếp theo gọi ai"

### 5.3 Điều chỉnh lắp ráp Agent §7

- Giữ lại lắp ráp Coordinator
- `coordinator.md` cắt từ 88 dòng xuống ~25 dòng
- Thu gọn kênh Reminder (xóa flow/queue_guard/book_complete, giữ foundation/subagent_guards)
- Thêm mới package `internal/host/flow/`

---

## 6. Điểm yếu đã biết (liệt kê trung thực)

### 6.1 Tiến hóa dài hạn của Flow Router

- Khi thêm cảnh mới (trạng thái flow mới, xử lý hậu kỳ cuối arc mới), switch-case của Router sẽ dài ra
- Cần ràng buộc nghiêm ngặt: **chỉ xử lý định tuyến, không xử lý logic nghiệp vụ**; quy tắc quyết định viết unit test
- Cảnh báo giống v0.0.1 `handleSubAgentDone` luôn còn hiệu lực; nhưng phương án này tránh trượt thành đối tượng thần thánh bằng "hàm thuần + unit test + chỉ gọi sự thật thuần"

### 6.2 Độ phức tạp của can thiệp người dùng

- Thiết kế hiện tại giao hoàn toàn Steer cho LLM của Coordinator phán định
- Nhưng một số Steer vượt qua nhiều loại (như "sửa rõ nhân vật A ở vài chương trước + về sau thêm tuyến phụ cho anh ta")
- Cần dựa vào năng lực của LLM để phân tách, prompt cần đưa ra chỉ dẫn rõ ràng
- **Phần này trực tiếp hưởng lợi từ nâng cấp mô hình** (so với việc hardcode phân loại enum của InterventionAgent, LLM phán định linh hoạt phù hợp hơn với cảnh thật)

### 6.3 Tiền đề phụ thuộc của tính nhất quán tầng sự thật

- Router ra quyết định dựa trên Progress + Checkpoint, tầng sự thật phải đáng tin cậy
- Một file đơn `withWriteLock` + tmp/rename bảo đảm thay thế nguyên tử; các bước liên file của `commit_chapter` được khôi phục bằng payload đầy đủ của PendingCommit, snapshot chính văn và phát lại lũy đẳng theo giai đoạn, thao tác cấu trúc thì sửa chữa derived view theo cùng tham số; đều không tuyên bố giao dịch nguyên tử kiểu cơ sở dữ liệu
- Nhưng nếu tầng sự thật xuất hiện không nhất quán (như Progress nói chương 3 đã hoàn thành nhưng không có trong chapters/), Router sẽ ra quyết định sai
- Khuyến nghị: khi khởi động thêm một lần **kiểm tra tính nhất quán tầng sự thật** (nếu phát hiện Progress.CompletedChapters không khớp với thư mục chapters/, báo warning)

### 6.4 Coordinator vẫn giữ khả năng định tuyến bằng LLM

- Dù chỉ thị rõ ràng, LLM có thể "sáng tạo" không thực hiện (ví dụ sinh một đoạn suy nghĩ rồi mới gọi công cụ)
- StopGuard làm tuyến phòng thủ: nhận chỉ thị Host nhưng vòng này chưa gọi subagent thì inject nhắc nhở
- Đây là tuyến phòng thủ, không phải cấm đoán — "thêm một bước suy nghĩ" thỉnh thoảng của mô hình mạnh cũng không phải chuyện xấu

### 6.5 Yêu cầu độ phủ kiểm thử tăng

- Flow Router là hàm thuần, phải có unit test hoàn chỉnh (bao phủ mọi tổ hợp Phase × Flow × Boundary)
- Kiểm thử tích hợp: mô phỏng chuỗi hoàn chỉnh "commit → router → FollowUp → coordinator phản hồi → subagent"
- Kiểm thử khôi phục crash: kill tiến trình rồi resume, assert Router suy ra đúng bước tiếp theo

---

## 7. Lộ trình triển khai

### Giai đoạn 1: Củng cố tầng sự thật (khoảng 0.5 ngày)

- Bổ sung kiểm tra nhất quán của §6.3: quét một lần khi khởi động/Resume, sinh warning
- Đảm bảo API `store.HasArcReview(vol, arc)` và `HasArcSummary(vol, arc)` khả dụng (nếu chưa có thì thêm)

### Giai đoạn 2: Đưa vào khung Flow Router (khoảng 1 ngày)

- Tạo mới package `internal/host/flow/`:
  - `route.go` — hàm thuần `Route(state) → *NextInstruction`
  - `dispatcher.go` — subscribe event + FollowUp ban hành
  - `route_test.go` — unit test bao phủ mọi nhánh
- Dùng công tắc config `flow_driven: true/false` để kiểm soát có kích hoạt hay không
- Mặc định tắt (false), trước tiên chạy đối chiếu

### Giai đoạn 3: Kích hoạt và xác minh (khoảng 1 ngày)

- Bật `flow_driven: true`
- Chạy một tiểu thuyết 30-50 chương, so sánh chỉ số:
  - Số lần gọi LLM của Coordinator
  - Số lỗi định tuyến (nên là 0)
  - Tính phản hồi (steer ngắt có bình thường không)
- Sửa bug, điều chỉnh quy tắc Router

### Giai đoạn 4: Rút gọn coordinator.md + tinh gọn Reminder (khoảng 0.5 ngày)

- Sửa coordinator.md theo §3.6
- Xóa `reminder/flow.go / queue_guard.go / book_complete.go`
- Giữ foundation reminder cần thiết
- Cập nhật subagent StopGuard nếu cần (thường không cần)

### Giai đoạn 5: Rút gọn resume.go (khoảng 0.5 ngày)

- Xóa phần lớn nhánh của `buildResumePrompt`
- Thay bằng thông điệp ngắn gọn chung "[khôi phục] vui lòng chờ chỉ thị Host"
- Sau Resume, Router tự nhiên suy ra hành động tiếp tục

### Giai đoạn 6: Cập nhật tài liệu kiến trúc (khoảng 0.5 ngày)

- Sửa `docs/architecture.md` §2 / §13 / §7 theo §5
- Đổi trạng thái tài liệu đề xuất này thành "đã chấp nhận", lưu trữ vào `docs/history/`

### Giai đoạn 7: Giai đoạn quan sát (2-4 tuần)

- Chạy liên tục 2-3 bộ dài kỳ (mỗi bộ 100+ chương)
- Ghi lại mọi lỗi định tuyến (nếu có), vấn đề phản hồi, hành vi ngoài dự kiến của Coordinator
- Dựa trên quan sát tinh chỉnh quy tắc Router và coordinator.md

**Tổng cộng khoảng 4 ngày triển khai + giai đoạn quan sát**.

---

## 8. Bảng so sánh

| Chiều | Kiến trúc hiện tại | Hybrid (phương án này) | Phương án cấp tiến (Phụ lục A) |
|---|---|---|---|
| Tính ổn định | Trung bình (LLM thỉnh thoảng định tuyến sai) | **Cao** | Cao |
| Tính phản hồi | Cao | **Cao** | **Thấp** (Host gọi trực tiếp SubAgent không thể ngắt) |
| Lợi ích LLM | 100% | **100%** | 85% (từ bỏ chiều định tuyến) |
| Tiết kiệm Token | 0 | ~70% | ~95% |
| Góc nhìn toàn bộ sách | Có | **Có** | Không (mỗi lần SubAgent độc lập) |
| Chi phí triển khai | - | Trung bình (khoảng 4 ngày) | Cao (khoảng 1 tuần + sửa agentcore) |
| Cập nhật tài liệu | - | Nhỏ (tinh chỉnh §2/§13) | Lớn (viết lại nguyên tắc §2) |
| Cần sửa agentcore | - | Không | Có thể (gọi trực tiếp SubAgent) |
| Độ khó rollback | - | Thấp (công tắc config) | Cao |

---

## 9. Điểm quyết định

1. **Có chấp nhận đề xuất này (Hybrid Coordinator) không?** [ ] Chấp nhận · [ ] Chấp nhận sau khi sửa · [ ] Không chấp nhận
2. Giai đoạn 3 có triển khai xác minh trước như một PR độc lập không? [ ]
3. Có xử lý luôn điều chỉnh `docs/architecture.md` §2 / §13 trong lần này không? [ ]
4. Độ dài giai đoạn quan sát: [ ] 2 tuần · [ ] 4 tuần · [ ] Dài hơn

---

## Phụ lục A: Phương án cấp tiến đã đánh giá (xóa hoàn toàn Coordinator)

> Phương án bản thảo đầu tiên. Do tính phản hồi thụt lùi, tính khả thi kỹ thuật còn nghi vấn, mất góc nhìn toàn bộ sách của Coordinator và các vấn đề khác, hạ cấp thành tham khảo.

Cốt lõi của phương án cấp tiến: Host gọi trực tiếp `SubAgentTool.Execute`, không đi qua Coordinator LLM.

**Các vấn đề đã nhận diện**:

1. **Tính phản hồi thụt lùi**: `SubAgentTool.Execute` là lời gọi đồng bộ chặn, Steer của người dùng phải chờ SubAgent hiện tại trả về mới xử lý được. `Inject` của kiến trúc hiện tại có thể ngắt ngay.
2. **Tính khả thi kỹ thuật còn nghi vấn**:
   - Host gọi trực tiếp SubAgentTool vi phạm thông lệ sử dụng agentcore
   - Luồng sự kiện (Event của `Subscribe`) có thể sẽ không nổi bọt chính xác cho observer
   - Đường dẫn callback `ContextManagerFactory` / `OnMessage` của SubAgent chưa rõ
   - Cần sửa agentcore hoặc sửa lớn observer
3. **Mất góc nhìn toàn bộ sách của Coordinator**: mỗi lần SubAgent run độc lập, không có "người canh gác LLM liên tục". Trong chạy dài, các vấn đề như trôi phong cách, nhân vật rời rạc mất đi một tầng bảo vệ vô hình.
4. **InterventionAgent đơn giản hóa quá mức**: phương án cấp tiến dùng enum (query/modify_setting/rewrite_chapters/adjust_style/noop) để phân loại ý đồ người dùng, Steer thật có thể bắc qua nhiều loại, schema cưỡng chế sẽ phân loại sai.
5. **Khối lượng viết lại tài liệu kiến trúc lớn**: lật đổ nguyên tắc cốt lõi §2, 30% lập luận tài liệu bị ảnh hưởng.
6. **FlowDriver sẽ phát triển thành đối tượng thần thánh**: một vòng lặp nhồi toàn bộ logic định tuyến, mỗi khi thêm cảnh đều phải sửa, đồng cấu với v0.0.1 `handleSubAgentDone`.

Phương án Hybrid tránh được 4 vấn đề đầu, vấn đề thứ 5 giảm thành tinh chỉnh, vấn đề thứ 6 được kiểm soát bằng "hàm thuần + unit test".

---

## Phụ lục B: Chi tiết vị trí đặt điểm quyết định

| Điểm quyết định | Vị trí hiện tại | Vị trí kiến trúc mới | Loại |
|---|---|---|---|| Chọn người lập kế hoạch | coordinator.md L26-29 | Coordinator LLM phán định (khi khởi động) | Phán định |
| Mở rộng đầu vào | coordinator.md L31 | Coordinator LLM phán định (khi khởi động) | Phán định |
| Vòng lặp bổ sung kế hoạch | coordinator.md L36-38 | Nhánh Host Router Phase=Premise/Outline (trả về nil để LLM tự chủ hoặc FollowUp architect tường minh) | Hỗn hợp |
| Bước tiếp theo cho mỗi chương | coordinator.md L46-51 + reminder/flow | **Nhánh Host Router 2d** (FollowUp writer) | Định tuyến |
| Đánh giá cuối arc | coordinator.md L78-82 | **Nhánh Host Router 2c** (FollowUp editor/architect) | Định tuyến |
| Nhánh verdict | coordinator.md L59-61 + công cụ save_review | Lớp công cụ đã được mã hóa, Router chỉ đọc Flow | Định tuyến (đã hoàn thành) |
| Can thiệp của người dùng | coordinator.md L67-70 | Coordinator LLM phán định (khi nhận tin nhắn Inject) | Phán định |
| Phái lại khi người lập kế hoạch báo lỗi | coordinator.md L40 | Host Router phát hiện FoundationMissing không thay đổi, bộ đếm thử lại | Định tuyến |
| Tổng kết hoàn thành toàn sách | coordinator.md L63-65 + reminder/book_complete | Host Router phát hiện Phase=Complete → FollowUp "xuất tổng kết" | Định tuyến |

---

## Phụ lục C: Vị trí mã nguồn tham khảo

- `assets/prompts/coordinator.md` — chờ đơn giản hóa
- `internal/host/reminder/flow.go` / `queue_guard.go` / `book_complete.go` — chờ xóa
- `internal/host/reminder/subagent_guards.go` — giữ lại
- `internal/host/reminder/stop_guard.go` — giữ lại + thêm kiểm tra "nhận được chỉ thị Host thì phải thực thi"
- `internal/host/resume.go` — đơn giản hóa đáng kể
- `internal/host/observer.go` — đăng ký mới EventToolExecEnd để kích hoạt Router
- `internal/host/flow/` — thêm package mới
- `internal/tools/commit_chapter.go` L220-280 — 17 trường CommitResult đã đầy đủ
- `internal/tools/save_review.go` — ánh xạ nguyên tử từ verdict của Editor đến Flow/hàng đợi làm lại
- `internal/store/outline.go` `CheckArcBoundary` — API sự thật ranh giới arc