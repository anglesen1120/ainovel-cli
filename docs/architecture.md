# Kiến trúc runtime của ainovel-cli

> Lớp sự kiện xác định, lớp ngữ nghĩa tự chủ: một Engine tuần tự xác định, ba Worker tự chủ, một số ít hàm Arbiter theo nhu cầu, một lớp sự kiện hệ thống tệp.
>
> Ngày 2026-07-12 hoàn tất thay thế mặt phẳng điều khiển: vòng lặp dài Coordinator LLM nghỉ hưu, do Engine (vòng lặp xác định) + Arbiter (hàm phán quyết ngữ nghĩa) tiếp quản. Quyết định thiết kế và biên bản review xem `docs/engine-arbiter.md`, RFC xem `docs/engine-rfc.md`.

---

## 1. Mục tiêu (theo thứ tự ưu tiên)

1. **Tính ổn định**: nhập một câu, ổn định viết trọn một tiểu thuyết (200~500 chương). Không tự ngắt giữa chừng do vấn đề kiến trúc.
2. **Chất lượng có thể lặp lại và cải tiến**: prompt / tài liệu tham khảo / chiều review / chiến lược ngữ cảnh có thể điều chỉnh độc lập, không kéo theo kiến trúc.
3. **Có thể khôi phục**: sau crash, mất mạng, tạm dừng có thể tiếp tục từ checkpoint gần nhất.
4. **Có thể quan sát**: có thể tra tiến độ, sản phẩm, thời lượng của từng step trong từng chương.

"Ổn định" là tiền đề, "chất lượng" là tầng trên. Mỗi quyết định kiến trúc ưu tiên phục vụ tính ổn định.

---

## 2. Nguyên tắc cốt lõi

### 2.1 Phép chia ba: quyết định quy về đúng nơi theo tính chất

- **Chuyển trạng thái có thể liệt kê → mã**. "Viết xong một chương thì phái ai" là đọc sự kiện tra bảng: hàm thuần `flow.Route` + kiểm thử đặc tả vét cạn tổ hợp cấp vạn, tỷ lệ lỗi tiệm cận 0, chi phí LLM bằng 0.
- **Phán đoán ngữ nghĩa có ranh giới rõ → hàm LLM (Arbiter)**. Chọn planner, phân luồng can thiệp người dùng, lối ra khi thất bại/bế tắc: sự kiện vào, quyết định có cấu trúc ra, kiểm tra cơ học dự phòng, mỗi lần phán quyết đều ghi xuống đĩa để replay.
- **Sáng tác mở → vòng lặp LLM (Worker)**. Trong phạm vi một chương, một lần review, một lần lập kế hoạch, architect/writer/editor hoàn toàn tự chủ.

Hai mặt phẳng đối xứng là kỷ luật xuyên suốt — mọi điểm quyết định mới trong tương lai đều theo hình dạng này, không phát minh mẫu mới:

```
Mặt phẳng xác định:  flow.LoadState   → flow.Route     → Instruction   (kiểm thử đặc tả vét cạn)
Mặt phẳng ngữ nghĩa: arbiter.Collect* → arbiter.Decide* → XxxDecision   (decisions.jsonl + hồi quy eval)
              └── thu thập sự kiện(IO) ──┘└── lõi(có thể replay offline) ──┘└── Engine thực thi ──┘
```

### 2.2 Công cụ là giao diện duy nhất của lớp sự kiện

Mọi tương tác với hệ thống tệp, Progress, Checkpoint đều do công cụ hoàn thành. Tệp đơn dùng `temp + fsync + rename` để thay thế nguyên tử; ghi nhiều tệp theo thứ tự không giả mạo giao dịch cơ sở dữ liệu: commit chương dùng Saga `PendingCommit` đã bền vững hóa, ghi cấu trúc dùng replay tất định lũy đẳng và phơi bày lỗi tường minh. Mỗi bước đều phải kiểm tra lỗi; chỉ những luồng đã bền vững hóa ý định khôi phục mới được cam kết khôi phục theo payload gốc qua restart.

### 2.3 Lớp quan sát chỉ quan sát

UI, chẩn đoán, nhật ký sự kiện đều là consumer thụ động được chiếu ra từ dòng sự kiện / artifact chỉ đọc. Đọc sự kiện, không tạo sự kiện, không ảnh hưởng luồng điều khiển.

Dữ liệu quan sát chia nghiêm ngặt thành ba lớp: `agentcore.ProgressPayload` là lớp truyền tải, văn bản lỗi phải đầy đủ và không được chứa chiến lược cắt ngắn của UI; `host.Event.Summary` là ngữ nghĩa hiển thị ngắn, `Detail` là chẩn đoán đầy đủ; nhật ký tệp ưu tiên ghi `Detail` đầy đủ, TUI chỉ đọc `Summary` và chỉ cắt ngắn theo độ rộng terminal lúc render cuối cùng. File logger do `Host` sở hữu: trước tiên lấy lease thư mục tiểu thuyết, rồi thiết lập phiên nhật ký, sau đó mới lắp Store, mô hình và Engine; như vậy vừa không vòng qua độc quyền một sách, vừa bao phủ toàn bộ quá trình lắp ráp và đóng nhật ký. Cái gọi là “nhật ký đầy đủ” nghĩa là không mất chuỗi lỗi, tham số bất hợp lệ gốc và metadata vòng đời, không phải lặp lại việc chép phần chính văn tiểu thuyết đã sinh thành công vào `tui.log`; nội dung lớn vẫn do artifact của Store và `meta/sessions` gánh vác.

**`internal/diag` là hệ con quan sát duy nhất của engine** — hạ tầng hỗ trợ hạng nhất, nhưng không phải lõi sản phẩm. Nó đọc chéo gần như mọi artifact + session + log + checkpoint, đảm nhiệm hai việc: ① **chẩn đoán chất lượng sáng tác** (quy tắc → Finding, báo cáo trên màn hình `/diag`); ② **gỡ lỗi runtime + xuất đã khử nhạy cảm** (khung hành vi bóc chính văn + tổng hợp vòng lặp → `meta/diag-export.md` ghi đè).

**Kỷ luật observer (không được nới lỏng)**: diag có thể chẩn đoán, có thể đề xuất, nhưng **không bao giờ tự ra tay** — không tự động sửa, không tiếp tục chạy, không đổi luồng (bài học lịch sử xem §10 điều 5).

### 2.4 Lớp sự kiện phẳng

Chỉ có ba loại sự kiện:

- **Progress** — chỉ mục tiến độ (đã viết đến chương mấy, danh sách chờ viết lại)
- **Checkpoint** — bản ghi tiến triển cấp step (plan / draft / commit / review / arc_summary)
- **Artifact** — sản phẩm như chính văn chương, đề cương, nhân vật, tóm tắt

Không đưa vào các trừu tượng WorkflowInstance / TaskInstance / Command. Sự kiện phụ thuộc (pool phản hồi đề cương, bản ghi vi phạm cơ học, audit phán quyết) cũng là jsonl phẳng, mỗi loại có producer và consumer duy nhất.

### 2.5 Bốn thiết luật

**Thiết luật một: công cụ chỉ trả về sự kiện, không trả về chỉ lệnh điều phối chéo**. `commit_chapter` trả về các trường có cấu trúc như `arc_end` / `needs_expansion`; không kèm chuỗi chỉ lệnh kiểu `[hệ thống]`. Trường `next_step` trong sub-agent là chỉ dẫn nội tuyến mang tính tường thuật sự kiện ("tôi vừa lưu plan, bước tiếp theo là draft"), không tính là vi phạm — xem §6.3.

**Thiết luật hai: định tuyến luồng do Flow Router đảm nhiệm, thực thi do Engine đảm nhiệm**. `Route(state) → *Instruction` trong `internal/flow/router.go` là hàm thuần (được đóng đinh bằng kiểm thử đặc tả vét cạn tổ hợp cấp vạn); mỗi vòng Engine đọc sự kiện từ store, Route suy ra chỉ lệnh, **trực tiếp chạy Worker theo chương trình** (`subagent.Runner.Run`, tham số/kết quả/chuỗi lỗi có kiểu), không có lớp chuyển tiếp công cụ LLM. Trả về nil biểu thị cảnh ngữ nghĩa (kết thúc hoàn tất sách / chờ can thiệp) hoặc dừng tự nhiên. **Bế tắc có giới hạn tường minh** (RFC §5): sau vòng trước, Route vẫn sinh cùng một `Agent+Task`, tức hậu điều kiện định tuyến chưa được thỏa; tham vấn Arbiter 3 lần, hard fuse tạm dừng 5 lần. Checkpoint trung gian bên trong Worker không reset bộ đếm, Engine xác định không cho phép quay rỗng vô hạn.

**Thiết luật ba: phán quyết ngữ nghĩa đi qua Arbiter, mỗi lần phán quyết ghi xuống đĩa**. Chọn planner lúc khởi động, phân luồng can thiệp người dùng, lối ra thất bại/bế tắc do các hàm Decide theo từng cảnh trong `internal/arbiter` phán quyết: sự kiện vào, quyết định có cấu trúc ra, kiểm tra cơ học dự phòng, audit decisions.jsonl (có thể replay hồi quy offline). Ba Worker giữ lại `CheckpointDeltaGuard` riêng (lan can sự kiện: sản phẩm chưa xuống đĩa thì không được kết thúc công việc).

**Thiết luật bốn: hard-code ranh giới, không hard-code phán đoán ngữ nghĩa không thể liệt kê**. Mã chỉ cố định các bất biến có thể chứng minh (quyền hạn, giai đoạn, thứ tự, lũy đẳng, tính toàn vẹn cấu trúc) và cung cấp cho mô hình sự kiện đầy đủ cùng không gian thao tác đủ rộng; các vấn đề mở như lựa chọn sáng tác, phán đoán chất lượng, kế hoạch thích ứng với chính văn ra sao phải để lại cho Worker / Arbiter. Cấm dùng từ khóa, ngưỡng điểm, enum lệch hướng hoặc bảng quy tắc để thay thế hiểu biết của mô hình, cũng cấm vì lo mô hình mắc lỗi mà thu hẹp không gian quyết định hợp pháp của nó. Trước khi thêm quy tắc mã mới, phải chứng minh trước rằng không gian quyết định đóng và kết quả có thể kiểm chứng cơ học; nếu không nên cải thiện ngữ cảnh và năng lực biểu đạt của công cụ, để lợi ích từ việc nâng cấp mô hình có thể được hiện thực hóa mà không cần sửa vỏ ngoài.

---

## 3. Toàn cảnh kiến trúc

```
[Điểm vào: TUI / headless]
        │ prompt / steer
[Vỏ Host]
   ├── observer            Chuyển tiếp tiến độ Worker + sự kiện phái phát Engine → phép chiếu UI/nhật ký
   ├── engine              Vòng lặp xác định: LoadState → Route → tiền kiểm → chạy Worker → ranh giới sentinel
   ├── đường can thiệp      Steer/Continue → Arbiter phán quyết → thực thi hành động(tức thời/submit tại ranh giới)
   └── usage / ngân sách / điểm dừng / quản lý mô hình
        │ gọi subagent.Runner.Run theo chương trình (tiến độ chuyển tiếp qua ctx ToolProgress)
[architect_short/long · writer · editor](run + context + mô hình độc lập cho từng bên)
        │ gọi công cụ
[Tools]  novel_context · read_chapter · plan_chapter · draft_chapter · edit_chapter
         check_consistency · commit_chapter · save_review · save_arc_summary
         save_volume_summary · save_foundation
        │ tệp đơn nguyên tử + replay lũy đẳng(commit dùng Saga bền vững hóa)
[Store: hệ thống tệp (tmp + rename)]
   Progress · Checkpoints · Outline · Drafts · Summaries · Characters · World
   · Signals · Decisions(audit phán quyết) · pool phản hồi · bản ghi vi phạm
```

| Lớp | Làm gì | Không làm gì |
|---|---|---|
| Entry | Hiển thị, nhận input | Quyết định nghiệp vụ |
| Host/Engine | Vòng đời, thực thi Route, chạy Worker, ranh giới sentinel, biên phối can thiệp | Phán đoán văn học; viết sự kiện sáng tác (hành động trạng thái điều khiển qua kernel công cụ) |
| Arbiter | Phán quyết ngữ nghĩa (quyết định có cấu trúc) | Tự mình sáng tác; thực thi hành động |
| Workers | Suy nghĩ, viết, review | Đọc ghi Store trực tiếp (bắt buộc qua công cụ) |
| Tools | IO tệp đơn nguyên tử + lỗi tường minh + lũy đẳng; commit dùng Saga | Chỉ lệnh điều phối chéo sub-agent |
| Store | Ghi xuống đĩa hệ thống tệp | Logic nghiệp vụ |

Phụ thuộc một chiều: `entry → host → agents/arbiter → tools → store → domain`; `flow` là package chiến lược thuần cấp trên (trên store, dưới host). Độc lập ngang: `errs/` có thể được mọi lớp tham chiếu, `diag/` subscribe dòng sự kiện host + `store/` chỉ đọc.

---

## 4. Mô hình dữ liệu

### 4.1 BookMetadata và Progress

`BookMetadata` là nguồn sự kiện duy nhất cho tên sách và giới thiệu hướng tới độc giả, bền vững hóa vào `meta/book.json`; `book.md` chỉ là phép chiếu dễ đọc. Premise không lưu lặp tên sách, Progress cũng không mang thông tin tác phẩm.

```go
type BookMetadata struct {
    Title    string
    Synopsis string
}
```

Progress(`internal/domain/runtime.go`) chỉ ghi trạng thái chạy:

```go
type Progress struct {
    Phase             Phase           // init / premise / outline / writing / complete
    CurrentChapter    int
    TotalChapters     int
    CompletedChapters []int
    TotalWordCount    int
    ChapterWordCounts map[int]int
    InProgressChapter int             // Chương đang được viết
    Flow              FlowState       // writing / reviewing / rewriting / polishing / steering
    PendingRewrites   []int
    StrandHistory     []string        // Chuỗi dominant_strand
    HookHistory       []string        // Chuỗi hook_type
    CurrentVolume, CurrentArc int     // Phân tầng truyện dài
    Layered           bool
}
```

Logic điều khiển chỉ đọc các trường sự kiện trên, không phụ thuộc bất kỳ "timestamp cập nhật" nào — thông tin thời gian do `OccurredAt` của checkpoint mang.

RunMeta(`meta/run.json`) mang **ý định chạy của người dùng** (không phải sự kiện sáng tác): PlanningTier, PlanStart (phán quyết khởi động được cố định, căn cứ duy nhất để khôi phục crash trong kỳ lập kế hoạch), PendingSteer (bảo vệ crash can thiệp, một slot đang xử lý), AdvanceMode / AdvancePermitChapter (chính sách nghiệm thu từng chương và giấy phép chương chính xác), AdvanceHold (tạm dừng dùng một lần do can thiệp ký). `RunMeta.Init` giữ lại toàn bộ trường ý định qua restart.

### 4.2 Checkpoint(`internal/domain/checkpoint.go`)

```go
type Scope      struct { Kind ScopeKind; Chapter, Volume, Arc int }
type Checkpoint struct {
    Seq        int64       // Tăng đơn điệu
    Scope      Scope       // chapter / arc / volume / global
    Step       string      // plan / draft / commit / review / arc_summary / ...
    Artifact   string
    Digest     string
    OccurredAt time.Time
}
```

Lưu trữ: `meta/checkpoints.jsonl`, chỉ append. Ghi lặp cùng `Scope+Step+Digest` được xem là lũy đẳng, không sinh dòng mới.

### 4.3 Artifact và sự kiện phụ thuộc

Artifact nằm trong `store/outline.go` `drafts.go` `summaries.go` `characters.go` `world.go`.

- **Signals**: `PendingCommit` (khôi phục gián đoạn commit). Đọc khi khởi động/khôi phục, không đọc lúc runtime.
- **Decisions**(`meta/decisions.jsonl`): bản ghi audit mỗi lần Arbiter phán quyết (facts+input+decision), có thể replay offline; **không phải nguồn dữ liệu khôi phục** (khôi phục chỉ phụ thuộc Progress/Checkpoint/RunMeta).
- **Sự kiện thế giới tăng trưởng**: timeline và thay đổi trạng thái nhân vật lần lượt append bằng `timeline.jsonl`, `meta/state_changes.jsonl`; trong tiến trình duy trì chỉ mục khử trùng lặp, commit bình thường chỉ ghi delta của chương này. Mảng JSON phiên bản cũ sẽ di trú theo giao thức lũy đẳng “trước tiên ghi nguyên tử log mới, sau đó xóa tệp cũ” ở lần append tiếp theo, `timeline.md` là phép chiếu con người đọc được có thể tái dựng.
- **Pool phản hồi đề cương**(`meta/outline_feedback.jsonl`): phản hồi thông thường của writer được tiêu thụ trong thao tác cấu trúc kế tiếp; nếu sửa chính văn bên ngoài ảnh hưởng cốt truyện, thì trước khi tiếp tục viết sẽ ưu tiên giao cho architect, xử lý xong thì xóa sạch.
- **Bản ghi vi phạm cơ học**(`meta/rule_violations.jsonl`): kết quả kiểm tra theo user_rules lúc commit, review của editor tiêu thụ qua `novel_context(chapter=N)`; metadata chất lượng best-effort, không nhất quán mạnh cùng cấp với commit.

### 4.4 Đề cương phân tầng và hội tụ hoàn tất sách (quyển kết)

Lập kế hoạch cuốn chiếu (mỏ neo compass + khung quyển + mở rộng arc theo nhu cầu) giải quyết "mở và cuốn", nhưng khiến "khi nào kết thúc" từ một con số biến thành phán quyết mở ở cuối mỗi quyển — hội tụ hoàn tất sách phải được thiết kế tường minh, nếu không sẽ xuất hiện hai loại bế tắc: sổ sách viết xong nhưng không kết được (vòng lặp chết viết tiếp vượt biên, đã được dự phòng cấu trúc sửa) và tự sự viết xong nhưng sổ sách không cho dừng (estimated_scale ước quá cao + ngưỡng kết thúc phủ quyết cứng → bơm nước hoặc fuse).

**Quyển kết là khái niệm hạng nhất của hội tụ**, hoàn tất sách = một lần phán quyết phương hướng + một đoạn trượt xác định:

- **Tuyên bố (phán quyết ngữ nghĩa LLM)**: kiến trúc sư ở cuối quyển chọn một trong ba — append_volume (tiếp tục) / append_volume kèm `"final": true` (quyển kết) / complete_book (điều kiện hiện tại đều thỏa). estimated_scale trong phán định hoàn tất là **bằng chứng chứ không phải quyền phủ quyết**.
- **Thực thi (mã tra bảng sự kiện)**: sự kiện kết = `domain.FinaleVolume`. Cấu trúc quyển cuối viết xong (`layeredStructurallyComplete`) **và đầy đủ bộ ba kết cuối quyển (review arc / tóm tắt arc / tóm tắt quyển)** thì tự động MarkComplete — kết thúc không chen trước cổng chất lượng của editor. Sách chưa tuyên bố vẫn đi theo `layeredBookComplete` cấp chất lượng (điểm treo + tuyến dài về 0).
- **Gỡ bỏ (suy dẫn dữ liệu, không có công cụ hủy)**: sau khi tuyên bố lại append quyển mới không đánh dấu → trạng thái khép lại tự nhiên được gỡ. Trạng thái luôn có thể suy dẫn từ layered_outline.
- **Phái phát phán định kết thúc**: cuối quyển do nhánh Route 10 phái architect_long chạy checklist phán định kết thúc — quyền phán quyết kết thúc thuộc về kiến trúc sư (một Worker), không ở mặt phẳng điều khiển.

---

## 5. Quy ước công cụ

Công cụ là điểm tương tác duy nhất giữa lớp sự kiện và Agent.

### 5.1 Công cụ đọc

`novel_context(scope)` / `read_chapter(n)` — có thể gọi bất cứ lúc nào, không phụ thuộc trạng thái tiền đề, trả về dữ liệu đủ để LLM quyết định độc lập. `novel_context(chapter=N)` bổ sung vi phạm cơ học của chương đó (nếu có); đường architect bổ sung tóm tắt arc của quyển đã hoàn thành/quyển hiện tại, snapshot nhân vật, pool phản hồi đề cương và trạng thái foundation. Khi expand arc, nội dung đã xảy ra là sự kiện, khung chỉ là kế hoạch; Architect có thể đồng bộ sửa title/goal của arc mục tiêu trong `expand_arc` và mở rộng chương.

### 5.2 Công cụ ghi (tệp đơn nguyên tử + ngữ nghĩa khôi phục phân cấp)

Ghi tệp đơn là nguyên tử; bước nhiều tệp không cam kết tính nguyên tử kiểu cơ sở dữ liệu. Submit thông thường và submit làm lại của `commit_chapter` dùng chung `PendingCommit`, tiến triển theo “ý định đầy đủ → artifact/trạng thái → Progress → checkpoint → xóa ý định”; khôi phục chỉ dùng payload đã chuẩn hóa và snapshot chính văn được ghi xuống đĩa lần đầu, cấm dùng tham số mô hình sinh lại sau restart hoặc draft đã bị ghi đè. Các thao tác cấu trúc như `expand_arc` / `append_volume` không có ý định bền vững hóa, chỉ cam kết replay lũy đẳng với cùng tham số, sửa view phái sinh và trả lỗi tường minh.

| Công cụ | Artifact | Step |
|---|---|---|
| `save_book` | meta/book.json + book.md | book |
| `plan_chapter` | drafts/chXX.plan.json | plan |
| `draft_chapter` | drafts/chXX.draft.md | draft |
| `edit_chapter` | drafts/chXX.draft.md | edit |
| `check_consistency` | Không có (chỉ đọc, trả về inline) | consistency_check |
| `commit_chapter` | chapters/chXX.md + Progress(+ pool phản hồi/bản ghi vi phạm best-effort) | commit |
| `save_review` | reviews/chXX.json(global là chXX-global.json) | review |
| `save_arc_summary` | summaries/arc-vNNaNN.json | arc_summary |
| `save_volume_summary` | summaries/vol-vNN.json | volume_summary |
| `save_foundation` | foundation/*.json(expand_arc/append_volume/update_compass thành công thì tiêu thụ pool phản hồi) | premise / outline / layered_outline / characters / world_rules / expand_arc / append_volume / update_compass / complete_book |

`commit_chapter` chịu trách nhiệm phát hiện hoàn tất arc/volume/toàn bộ sách và trả về các sự kiện có cấu trúc; `save_review` không phán định ngưỡng văn học, chỉ kiểm tra sự kiện review và ánh xạ nguyên tử verdict do Editor đưa ra thành Flow và hàng đợi làm lại.

`edit_chapter` là lớp bọc mỏng của `agentcore.EditTool`, chỉ cho phép chỉnh sửa các chương đã hoàn tất và nằm trong `PendingRewrites`; bản nháp đầu tiên của chương mới phải dùng `draft_chapter(mode="write")` để ghi đè toàn chương.

### 5.3 Phân tầng lỗi

| Loại lỗi | Tầng xử lý | Hành động |
|---|---|---|
| Hết thời gian mạng / EOF streaming | Tools | Thử lại 3 lần |
| provider 429/503 | litellm | failover sang provider dự phòng |
| Xác thực / mô hình không tồn tại | Tools | đẩy lên terminal |
| Thiếu artifact tiền đề | Tools | đẩy lên conflict, LLM gọi `novel_context` rồi thử lại |
| Tham số tool không hợp lệ | Tools | đẩy lên validation, LLM sửa tham số |
| retryable(stream-idle v.v.) | tầng subagent | MaxRetries=7 thử lại tại chỗ, không thoát Worker |
| Worker thất bại(guard nâng cấp/hard_stop v.v.) | Engine | lỗi xác định thì tạm dừng trực tiếp; còn lại thử lại cùng chỉ thị một lần → Arbiter phán định retry/reroute/abort |
| Bế tắc(cùng một chỉ thị định tuyến tái hiện liên tục) | Engine | hỏi Arbiter 3 lần, hard fuse tạm dừng ở lần 5 |
| Phản hồi streaming rỗng / suy nghĩ dài | litellm (`StreamIdleTimeout=5min`) | watchdog kích hoạt thử lại |

### 5.4 Tính lũy đẳng

Trước khi mỗi tool loại ghi thực thi, kiểm tra checkpoint trước: nếu `Step+Digest` của checkpoint mới nhất trong scope hiện tại giống lần này, trả về trực tiếp sản phẩm đã có. Việc phát lặp do thử lại và khôi phục sau crash đều an toàn — đây cũng là nền tảng để mô hình khôi phục của Engine(đọc store rồi chạy tiếp) đứng vững.

---

## 6. Lắp ráp Worker

> Một Prompt siêu lớn duy nhất + một Agent duy nhất chạy hết một cuốn sách về lý thuyết là khả thi, nhưng ba việc sẽ chặn độ ổn định: **bùng nổ ngữ cảnh**(200 chương dù nén mạnh cũng suy giảm), **nhiễu trách nhiệm**(lập kế hoạch nghiêm cẩn / viết sáng tạo / review phê phán trong cùng một prompt sẽ làm loãng nhau), **mất lợi thế mô hình dị thể**(chọn mô hình độc lập cho lập kế hoạch/viết/review là không gian tối ưu chi phí/chất lượng đáng kể). Vì vậy topology nhiều Worker là cần thiết.

### 6.1 Lắp ráp và vận hành

`agents.BuildWorkers`(`internal/agents/build.go`) lắp ráp ba loại Worker thành một `subagent.Runner`: Engine gọi trực tiếp `Run(agent, task)`, mỗi lần gọi là một `agentcore.AgentLoop` hoàn chỉnh(context độc lập, mô hình độc lập, retry độc lập). Toàn bộ lắp ráp có hiệu lực một lần: mô hình theo vai trò + failover, prompt cache key(mỗi spawn tự tăng #seq), ThinkingLevel, UsageRecorder/SessionLogger(OnMessage), Writer ContextManagerFactory(cửa sổ tự động dựng lại theo chuyển đổi /model), RestorePack, StopGuardFactory, StopAfterTools.

Chuyển tiếp tiến độ Worker đi qua callback ToolProgress của **ctx**: Engine dùng `agentcore.WithToolProgress(ctx, relay)` để gọi `Runner.Run`, các sự kiện gọi tool/thân văn bản streaming/thinking/retry/context của subagent đi qua relay vào observer — cùng hình thái ProgressPayload như thời Coordinator, tầng quan sát được tái sử dụng.

```
Engine ── Runner.Run(agent, task) ──▶ architect_short/long · writer · editor
                                          │ Gọi tool
                                        Store(môi giới cộng tác, các Worker không giao tiếp trực tiếp)
```

`bootstrap.ModelSet` hỗ trợ mô hình cấp vai trò: architect/writer/editor mỗi bên cấu hình độc lập + provider failover. Writer chạy Sonnet thay vì Opus trên trường thiên 200 chương có thể tiết kiệm chi phí một bậc độ lớn. Arbiter thống nhất dùng mô hình Default(tính phí qua usageTrackedModel), hiện chưa mở cấu hình vai trò độc lập.

### 6.2 Ba kiểu cộng tác

Các Worker không giao tiếp trực tiếp, mọi luồng thông tin đi qua artifact có cấu trúc trong Store:

**Mẫu A · Bàn giao tuần tự(trục chính)**: Route phái Architect lập kế hoạch → Writer chương 1..N → Editor review cuối arc → Writer viết lại. Mỗi bước "tiếp theo phái ai" do Route suy luận từ sự kiện.

**Mẫu B · Vòng phản hồi**: Writer báo cáo lệch đề cương trong commit → pool phản hồi ghi xuống đĩa(chỉ sách phân tầng) → Architect thao tác cấu trúc lần sau tham chiếu qua novel_context → thao tác thành công thì tiêu thụ và xóa. Writer không gọi trực tiếp Architect, phản hồi luân chuyển qua tầng sự kiện.

**Mẫu C · Mở rộng khung xương(lập kế hoạch cuốn chiếu)**: sau commit, sự kiện cho thấy arc tiếp theo vẫn là khung xương → Route(hoặc Engine precheck) phái architect_long mở rộng → Writer tiếp tục. Khả năng "lập kế hoạch cuốn chiếu" cho trường thiên chính là vòng khép kín này.

### 6.3 Ràng buộc mã của quy trình Worker(không dựa vào gậy chống prompt)

> Quy trình writer thời kỳ đầu dựa vào ràng buộc "nghiêm ngặt tiến hành theo thứ tự sau" trong `writer.md`. LLM thường xuyên vi phạm — bỏ qua plan mà draft trực tiếp, hoặc chỉ viết chính văn vào chat mà không ghi xuống đĩa. **Dùng prompt để ràng buộc quy trình là không ổn định**, nâng cấp mô hình thậm chí có thể khiến nó "sáng tạo không tuân thủ".

Bốn tầng ràng buộc bằng mã(có hiệu lực đồng thời):

| Tầng | Điểm đặt | Tác dụng |
|---|---|---|
| `StopAfterTools` / `StopAfterToolResult` | `agents/build.go` SubAgentConfig | Tool then chốt thành công thì thoát Worker run(thoát trạng thái cuối vẫn hỏi StopGuard, xem kiểm thử hợp đồng). Writer dừng ngay khi `commit_chapter` khớp; `save_review`/`save_arc_summary`/`save_volume_summary` của Editor, kết thúc arc/volume của Architect đi qua `StopAfterToolResult` |
| `CheckpointDeltaGuard` | `agents/guard/subagent_guards.go` | Lấy checkpoint baseline làm ranh giới, trước khi vòng này kết thúc bắt buộc phải thấy checkpoint mới của step tương ứng, nếu không từ chối `end_turn`; chặn liên tiếp 3 lần thì nâng cấp terminate(dự phòng vòng lặp chết của mô hình yếu). Guard của Editor nhận biết nhiệm vụ: khi được phái tạo tóm tắt thì chỉ kiểm tra lại không tính là hoàn tất |
| `next_step` nội tuyến trong tool | Trường giá trị trả về của từng tool | Mỗi sự kiện tự mang "gợi ý bước tiếp theo", LLM nhìn thấy sự kiện là biết bước tiếp theo |
| Kiểm tra quyền sở hữu/tiền đề trong tool | `edit_chapter` `commit_chapter` v.v. | Chặn vật lý ở tầng dữ liệu: chỉnh sửa điểm định vị bản nháp đầu, sửa chương đã hoàn tất nhưng chưa vào hàng đợi, commit rỗng đều bị từ chối, `ConcurrencySafe=false` ngăn race song song |

writer.md chỉ chịu trách nhiệm: giao thức thực thi, mô hình nhận thức chạy tiếp từ breakpoint, diễn giải hợp đồng chương; tiêu chuẩn viết nằm ở tầng văn phong(điền lại placeholder `{{VOICE}}`, người dùng có thể ghi đè, xem `docs/voice-layer.md`). **Đây chính là tiền đề để tầng văn phong dám mở cho người dùng: bất biến nằm ở tầng tool, prompt có sửa hỏng tùy ý cũng không phá được state machine.**

### 6.4 Phụ thuộc agentcore

`../agentcore` là thư viện Agent tổng quát tự có của dự án này(liên kết bằng go.work). Các primitive Engine dùng: `subagent.Runner.Run`(gọi trực tiếp theo chương trình, kết quả và chuỗi lỗi được type hóa — phân loại như `errors.Is(err, subagent.ErrUnknownAgent)` không phụ thuộc thông điệp lỗi), ctx `ToolProgress`(chuyển tiếp sự kiện), `subagent.Config`, `StopGuard`/`StopAfterTools`. `subagent.Tool` chỉ dùng cho host cần phơi `Runner` cho mô hình qua `Runner.AsTool()`, AINovel không đi qua tầng này.

**Ranh giới sửa đổi**: có thể vào agentcore — chiến lược ContextManager mới, adapter provider mới, loại sự kiện mới; không vào agentcore — mô hình nghiệp vụ và tool nghiệp vụ. Tiêu chí phán đoán: giả sử tương lai agentcore sẽ được coding agent / agent chăm sóc khách hàng đưa vào dùng, năng lực mới vẫn có ý nghĩa trong bối cảnh đó thì mới cho phép vào. **Cấm viết patch dự phòng ở tầng ứng dụng** — thiếu năng lực thì sửa trực tiếp thượng nguồn.

**Kiểm thử hợp đồng**(`internal/agents/agentcore_contract_test.go`, 6 mục, toàn bộ được drive qua `Runner.Run`): đóng đinh hành vi framework mà dự án này phụ thuộc thành assertion có thể thực thi(thoát trạng thái cuối hỏi StopGuard, Error/Aborted không chạm guard, chuỗi lỗi Escalate có thể match bằng `errors.Is`, `ErrUnknownAgent` được type hóa của `Run`, tiến độ lỗi tool đầy đủ và là plain text). **Trước khi bump agentcore bắt buộc tất cả xanh** — chú thích sẽ lỗi thời, test thì không(kỷ luật này đã từng bắt được một giả định mất hiệu lực và tiết kiệm một workaround).

### 6.5 Prompt cache

Đòn bẩy thứ hai cho chi phí chạy dài(thứ nhất là chọn mô hình). Bản giải thích đầy đủ xem `docs/prompt-cache-design.md`. Phân công ba tầng: **litellm chỉ làm dịch giao thức**, **agentcore quyết định vị trí đặt cache và danh tính**, **ainovel cấu hình một dòng để tích hợp**.

Tiền đề để cache có lợi là **byte tiền tố request ổn định**, được đảm bảo bởi ba kỷ luật(đều ở agentcore):

1. **byte tools xác định** — Description/Schema dựng lại mỗi lần, mọi phép lặp map đều sắp xếp trước
2. **lịch sử append-only** — message chỉ append không viết lại; nén ngữ cảnh là giao dịch tường minh "trả một lần full miss để đổi cửa sổ", projection bắt buộc `CommitOnProject`
3. **nội dung động đi vào đuôi** — envelope/instruction đều append ở đuôi, tuyệt đối không viết ngược về message sớm

Cấu hình là “một sách một nền, một vai trò một tên, một session một key”: hệ OpenAI dùng `PromptCacheKey = nvl-<hash-sách>-<role>#<số-thứ-tự-spawn>` để tạo affinity định tuyến(mặc định chỉ gửi cho endpoint chính thức, relay có thể bật tường minh); hệ Claude dùng `CacheLastMessage: "ephemeral"` breakpoint cuốn chiếu + breakpoint sàn system. **Lằn đỏ chốt khóa**: mọi lượng đi vào cache key sau khi tính lần đầu trong session sẽ đóng băng, thà cũ còn hơn phá cache. Phát hiện đứt gãy(`host/usage.go noteCacheBreak`) chỉ quan sát không sửa, đếm vào `usage.json cache_breaks` và bảng cache của TUI.

---

## 7. Engine và Arbiter

### 7.1 Vòng lặp Engine(`internal/host/engine.go`)

```
for {
    Áp dụng hành động trạng thái điều khiển can thiệp(xả rỗng; hold+dispatch trước hết lập sự kiện làm lại)
    advanceGate.HandleBoundary() // hold consumption + review permit reconciliation
    inst := dispatch can thiệp ?? Route(LoadState) ?? planStartFallback
    inst == nil → return          // Hoàn sách / dừng ngữ nghĩa, chờ Continue
    precheck(inst)                // Hiện thân xác định của ToolGate cũ: giai đoạn hoàn sách thì bỏ dispatch;
                                  // chương mục tiêu của writer chưa được mở rộng → đổi phái architect mở rộng
    advanceGate.Allow(inst)       // Chỉ chặn chương mới tiến về phía trước chưa được phép
    trackDeadlock(inst)           // Cùng Agent+Task tái hiện liên tục: 3 lần hỏi Arbiter, 5 lần fuse
    runWorker(inst)               // subagent.Runner.Run + chuyển tiếp tiến độ + sự kiện DISPATCH
    Phân loại lỗi: lỗi xác định → tạm dừng; thất bại đầu thử lại một lần; thất bại nữa → Arbiter(retry/reroute/abort)
    Ranh giới chính sách: budget → advanceGate
}
```

Goroutine đơn chạy tuần tự; cancel `ctx` = tạm dừng(checkpoint đảm bảo không mất mát). **Trạng thái điều khiển chỉ thay đổi tại biên vòng lặp**: hold/reopen/dispatch của can thiệp được xếp hàng đến biên rồi commit(tổ hợp hold+dispatch trước thực thi dispatch để lập hàng đợi, rồi mới cho Gate tiêu thụ hold); answer/rules thực thi tức thời. Chế độ `review` chỉ ràng buộc chương mới tiến về phía trước, không chặn làm lại, review, bảo trì cấu trúc và khôi phục commit. Trước khi Arbiter dispatch thực thi, làm đối soát Expect(các trường ngữ nghĩa Phase/Flow/QueueHead; CheckpointSeq chỉ audit không đối soát — khi can thiệp worker thường đang chạy, seq chắc chắn sẽ đổi), không khớp thì bỏ và đưa can thiệp gốc **đồng bộ** quay lại đường phán định đầy đủ để hỏi lại.

### 7.2 Arbiter(`internal/arbiter/`)

Bốn bối cảnh, mỗi bối cảnh một cặp `Collect*Facts`(ranh giới IO) / `Decide*`(ngoài request mô hình do executor thống nhất quản lý thì không có IO, có thể replay offline) + loại Decision chuyên biệt(hành động không khớp bối cảnh không thể biểu đạt ở cấp kiểu):

| Bối cảnh | Kích hoạt | Loại quyết định |
|---|---|---|
| `plan_start` | Khởi động sách mới | Chọn planner short/long + mở rộng yêu cầu quá ngắn |
| `intervention` | Can thiệp người dùng | tổ hợp answer/rules/hold/reopen/dispatch(thứ tự thực thi do Engine cố định) |
| `worker_failure` | Worker báo lỗi và phân loại xác định không có lối ra | retry / reroute / abort |
| `deadlock` | Cùng chỉ thị lặp lại không có tiến triển | retry / reroute / abort |

Đường thất bại: executor có cấu trúc thống nhất chọn JSON Schema native hoặc hợp đồng prompt theo năng lực; lỗi định dạng/Schema của chế độ prompt và lỗi kiểm tra nghiệp vụ của cả hai chế độ sẽ phản hồi nguyên nhân chính xác cho mô hình tiếp tục sửa, cho đến khi thành công hoặc `context` kết thúc, không đặt giới hạn số lần. Vi phạm hợp đồng native, từ chối trả lời, bị cắt, kết thúc lỗi và lỗi request không thể thử lại sẽ trả về tường minh ngay; can thiệp không phát sinh ghi, khởi động báo lỗi tường minh, failure/deadlock tạm dừng bảo thủ. **Đầu ra Arbiter cũng không đáng tin như mọi đầu ra LLM** — sau kiểm tra JSON Schema, `Validate` tiếp tục kiểm tra máy móc theo sự kiện(ràng buộc phase, reopen chỉ giới hạn hoàn sách, chương vượt biên). Usage đi qua `usageTrackedModel` vào ngân sách và hệ thống usage.

### 7.3 Vỏ Host(`internal/host/host.go`)

Vòng đời(`StartPrepared`/`Resume`/`Continue`/`Steer`/`Abort`/`Close`), điều phối can thiệp(FIFO tuần tự + bảo vệ crash PendingSteer), projection sự kiện, quản lý mô hình. Kênh quan sát `Events`/`Stream`/`Done`, UI tổng hợp `Snapshot()`, cổng mở rộng(import/export/đồng sáng tác/nhại phong cách/chuyển mô hình).

`runEnded`(callback engine.onDone) định trạng thái cuối theo sự kiện store: Phase=Complete → completed + tóm tắt hoàn sách xác định(không tốn gọi LLM); còn lại → idle/paused. **Cấm mọi logic "tự động chạy tiếp" xuất hiện ở đây**(bài học lịch sử §10 mục 5).

---

## 8. Khởi động, khôi phục và can thiệp

### 8.1 Tạo mới

```
User: "Yêu cầu một câu"
  → StartPrepared(raw)
    → Progress.Init / Checkpoints.Reset
    → StartPrompt cố định vào RunMeta(sự kiện đầu vào ghi xuống đĩa trước phán định)
    → Arbiter plan_start phán định(chọn planner+mở rộng yêu cầu) → thất bại báo lỗi tường minh(audit kèm error)
    → PlanStartRecord cố định vào RunMeta(phán định rơi xuống sự kiện trước, rồi mới khởi chạy thực thi)
    → engine.start(chỉ thị dispatch đầu tiên)
```

Phán định thất bại không phải thế chết: StartPrompt đã có, mọi lần khôi phục/tiếp tục sau đó đều sẽ được engine bổ sung phán định(xem §8.2).

### 8.2 Khôi phục(khởi động lại sau crash)

```
Tiến trình khởi động → resumeLabel(nhãn UI thuần) → cảnh báo nhất quán → AdvanceGate đối soát
  → PendingSteer tồn tại → đồng bộ đi đường phán định can thiệp(can thiệp có hiệu lực trước chạy tiếp) rồi kéo engine lên
  → nếu không engine.start(nil): chỉ khôi phục sự kiện, Route tính lại từ store để chạy tiếp
```

Không có session nào cần khôi phục. Crash trong giai đoạn lập kế hoạch(phán định đã ghi xuống đĩa, foundation đầu tiên chưa ghi) do `planStartFallback` tiếp tục phái theo PlanStartRecord, không làm lại phán định đã có. Nếu phán định khởi động **chưa từng hoàn tất**(lỗi mô hình lúc khởi động), `planStartFallback` dựa vào StartPrompt để bổ sung phán định tại chỗ — đây là lần thử lại của phán định đầu tiên, không vi phạm "khôi phục không phán định lại"; bổ sung phán định thất bại thì tạm dừng tường minh báo cho biết, không cho phép dừng im lặng. Phát lặp an toàn được đảm bảo bởi tính lũy đẳng của tool(§5.4).

### 8.3 Can thiệp người dùng

`Steer`/`Continue` thống nhất đi đường phán định Arbiter(`doIntervention`):

```
Bền vững hóa PendingSteer(bảo vệ crash) → Collect facts → Decide(cấp giây)
  → ghi decisions.jsonl → answer vọng lại / rules ghi xuống đĩa tức thời
  → hold/reopen/dispatch vào hàng đợi để commit ở biên(khi engine dừng thì thực thi ngay và kéo engine lên theo ý định)
  → mọi hành động thành công → xóa nguyên tử PendingSteer(ClearHandledSteer)
```

Bảo vệ crash là **best-effort bền vững hóa một mục đang xử lý**: nếu `SetPendingSteer` lần đầu thất bại sẽ báo lỗi tường minh và dừng phán định, tuyệt đối không tiếp tục thực thi khi không có bản ghi khôi phục; được bảo vệ trong giai đoạn phán định, khi hành động thất bại(giữ lại để replay), khi thoát bình thường/Abort(defer ghi lại dispatch tồn dư). Vẫn có hai cửa sổ không đảm bảo được nêu rõ — bị hard kill sau khi dispatch chuyển vào hàng đợi thực thi trong bộ nhớ(cấp mili giây), input song song đang chờ `interMu`. Người dùng đang có mặt có thể cảm nhận, chi phí gửi lại cấp giây.

**Tầng bền vững của can thiệp dài hạn**: phong cách viết/quy tắc chất lượng do action `rules` đã phán định được chuẩn hóa qua `userrules.Service` vào snapshot quy tắc của sách này, `novel_context` tiêm vào `working_memory.user_rules` — có hiệu lực xuyên nén, xuyên khởi động lại(xem chi tiết [snapshot quy tắc người dùng](user-rules-runtime.md)). Các lối ra còn lại vốn đã rơi vào store(độ dài/cốt truyện → dispatch architect, sửa chương cũ → editor đưa vào hàng đợi PendingRewrites, làm lại sau hoàn sách → reopen).

### 8.4 Kiểm soát tiến chương

`ChapterAdvanceGate` thống nhất thực thi hai loại ý định người dùng ở thang thời gian khác nhau:

| Ý định | Nguồn | Ngữ nghĩa |
|---|---|---|
| `AdvanceMode=review` + permit chính xác | `/review on`, `/next` | Chính sách bền vững: mỗi chương mới tiến về phía trước phải được cho phép riêng |
| `AdvanceHold` | Arbiter intervention | Ý định dùng một lần: tạm dừng sau khi biên hiện tại, làm lại được xả rỗng, hoặc chương mục tiêu commit ổn định |

Số chương được ràng buộc với giấy phép. Chỉ khi chương đích đã vào `CompletedChapters`, `PendingCommit` được xóa sạch và `commit checkpoint` tồn tại thì mới tiêu thụ, nên bất kỳ cửa sổ nào của saga commit bị crash cũng sẽ không dùng cùng một giấy phép cho chương tiếp theo. Bất biến chi tiết xem [Chapter Advance Gate](chapter-advance-gate.md).

---

## 9. Cấu trúc thư mục

```
internal/
  domain/         dữ liệu thuần: Phase / FlowState / Progress / Checkpoint / Scope / Story / Plan /
                  Review / StateChange / quy tắc chuyển đổi Phase-Flow
  store/          lưu trữ bền vững hệ thống tệp (tmp+rename + điều phối idempotent; commit có sự thật các giai đoạn Saga): progress / checkpoints / outline /
                  drafts / summaries / characters / world / signals / run_meta / runtime /
                  session / decisions(audit phán quyết)
  tools/          11 công cụ Agent, ghi kiểu đơn tệp nguyên tử + lỗi tường minh + idempotent; commit dùng thêm Saga bền vững
  flow/           chiến lược định tuyến (pure function + biên IO): router.go (bảng quyết định Route) + state.go (LoadState)
                  + pause.go (phán quyết điểm dừng)
  arbiter/        lớp phán quyết ngữ nghĩa (LLM-as-function): plan_start / intervention / failure(deadlock)
                  cặp hàm Collect/Decide theo từng kịch bản + kiểu Decision theo từng kịch bản + kiểm tra cơ học
  agents/         build.go lắp ráp ba Worker(subagent.Runner, Engine gọi trực tiếp theo chương trình); chiến lược nén ngữ cảnh ctxpack/ Writer
    guard/        subagent_guards.go (CheckpointDeltaGuard ×3, Worker hàng rào sự thật)
  host/           host.go (điều phối vòng đời/can thiệp) + engine.go (vòng lặp thực thi xác định) + observer*.go
                  + events.go + usage*.go + budget.go + advance_gate.go + resume.go + cocreate.go
    imp/          nhập biên dịch ngữ nghĩa tiểu thuyết bên ngoài: ingest → segment → analyze → synthesize → publish (suy diễn trạng thái thuần + LLM làm hàm)
    exp/          xuất chương đã hoàn thành: TXT / EPUB 3; chỉ đọc thuần
  entry/          tui (Bubble Tea) / headless / startup
  bootstrap/      config + ModelSet + provider failover + trình hướng dẫn setup
  eval/           đánh giá offline (prompt/voice A/B, hồi quy)
  diag/ errs/ models/ notify/ rules/ userrules/ stylestat/ ...

assets/
  prompts/        arbiter-plan-start / arbiter-intervention / arbiter-failure / architect-short|long
                  / writer(mẫu giao thức, chỗ giữ {{VOICE}}) / editor / import-* / simulation-*
  voice.md        tiêu chuẩn viết (mặc định tích hợp trong tầng văn phong; xem ghi đè ba tầng tại docs/voice-layer.md)
  references/     kỹ thuật viết + anti-ai-tone + mẫu thể loại, v.v.
  styles/         mặc định/fantasy/ngôn tình/trinh thám (người dùng có thể ghi đè/thêm mới)

../agentcore     khung Agent dùng chung (thư mục anh em trong go.work, có thể thêm năng lực dùng chung, không thêm nghiệp vụ)
../litellm       cổng LLM
```

### 9.1 Mốc tiến hóa

| Thời điểm | Tái cấu trúc | Hiệu quả ròng |
|---|---|---|
| 2026-04-10 | `internal/orchestrator/` (6342 dòng) → `host/` + `agents/` | lõi runtime -74% |
| 2026-04-20 | Hybrid Coordinator: tạo mới `host/flow/`, thu hồi định tuyến về pure function | tỷ lệ lỗi định tuyến tiến gần 0 |
| 2026-05-02 | agentcore sửa slow-thinking/streaming; xóa patch tiếp tục chạy `idleResumeCount` | mimo / slow-thinking streaming chạy thông |
| 2026-06-05 | vòng khép kín lập kế hoạch cuộn + `/import` suy ngược để viết tiếp | 200+ chương lần đầu chạy thông |
| 2026-07-12 | **Engine + Arbiter thay thế mặt điều khiển**: vòng lặp dài của Coordinator và hệ sinh thái bảy patch về hưu; văn phong tầng ba lớp ghi đè; cố kết đánh giá đối kháng năm vòng | tiết kiệm một lần chuyển tiếp LLM ở mỗi biên; mặt điều khiển 100% có thể kiểm thử offline; phán quyết ngữ nghĩa có thể phát lại |
| 2026-07-15 | **pipeline nhập `/import` biên dịch ngữ nghĩa**: nghỉ hưu quy tắc cắt cứng mã hóa, đổi thành biên dịch theo giai đoạn ingest→segment→analyze→synthesize→publish; suy diễn trạng thái thuần (`NextAction(Facts)`) + băm đầu vào ràng buộc tạo phẩm, toàn trình có thể phục hồi idempotent | việc cắt tách tăng tự nhiên theo năng lực mô hình; không còn enum giai đoạn lệch; gián đoạn có thể tiếp tục, mặt điều khiển có thể kiểm thử offline |

Thực đo: hy3-preview free 12 chương / 73 phút, mimo-v2.5-pro 10 chương / 8.4 vạn chữ, đều chạy xong một lần; trường thiên gpt-5.4《Phàm Cốt》235 chương / 127 vạn chữ vòng khép kín lập kế hoạch đã chạy thông (dữ liệu thời Coordinator, số liệu thời Engine lần chạy đầu còn cần bổ sung).

---

## 10. Những việc rõ ràng không làm

Vi phạm tức là lệch kiến trúc.

1. **Không đưa khái niệm Task / Job / WorkItem vào**. "Nhiệm vụ hiện tại" hiển thị trên UI là phép chiếu của luồng sự kiện, không phải sự thật.
2. **Không phát minh bộ điều phối thứ hai ngoài Route**. Mọi "bước tiếp theo giao cho ai" đều phải qua bảng quyết định Route (đặc tả đóng đinh bằng liệt kê đầy đủ) hoặc phán quyết của Arbiter (ghi audit xuống đĩa), không cho phép phân phát if-else rải rác.
3. **Không làm cơ chế "tiếp tục chạy khi rảnh"**. Vòng Engine kết thúc = Host vào trạng thái cuối; muốn động lại chỉ có người dùng `Continue` hoặc khởi động lại `Resume`.
4. **Không nhét quy huấn hành vi vào prompt**. Cần mô tả hàng rào hành vi tức là phân tầng sai rồi — bất biến đi vào tiền điều kiện của tool, phán đoán đi vào Arbiter, luồng đi vào Route.
5. **Không vá Host bằng tự động tiếp tục chạy cho dừng bất thường**. `idleResumeCount` trước đây trong lần chạy dài thực sự bị kích hoạt duy nhất đã 100% không cứu được trận, ngược lại còn che mất căn nguyên thật ở tầng agentcore (xem `feedback_no_host_resilience.md`).
6. **Không suy diễn hoàn thành nhiệm vụ từ "tool exec end"**. Bằng chứng duy nhất của hoàn thành là checkpoint được ghi.
7. **Không làm mô hình bốn tầng kiểu WorkflowInstance / Command + Apply, v.v.**. Tầng sự thật chỉ có Progress + Checkpoint + Artifact.
8. **Không hỗ trợ Worker song song**. Một vòng Engine hoạt động đơn, một cuốn sách tiến tuần tự. Muốn nhiều tiểu thuyết thì dùng nhiều tiến trình.
9. **Không gọi LLM ở tầng tool** (trừ chính Agent tool). Chỉ IO thuần + kiểm tra + idempotent.
10. **Không để UI đọc Store trực tiếp**. Chỉ được subscribe sự kiện hoặc đọc `Snapshot()` của Host.
11. **Không viết máy trạng thái Flow ở phía Host**. Nhãn Flow chỉ do tool cập nhật, Route chỉ đọc không ghi.
12. **Không viết hardcode dự phòng cho "LLM ảo giác"**. Hãy tối ưu prompt, cải tiến giá trị trả về của tool, khiến `novel_context` trình bày sự thật rõ hơn.
13. **Không để diag / tầng quan sát can dự vào luồng điều khiển**. Chẩn đoán chỉ đọc; tự sửa / tiếp tục chạy / đổi luồng đều không làm.
14. **Chính sách ngân sách và tiến triển chương không đưa vào Route/tầng tool**. `BudgetSentinel` / `ChapterAdvanceGate` là các thành phần chính sách ở biên Engine (thi hành chỉ thị do người dùng ký trước, không đánh giá hành vi văn học); `notify` chỉ quan sát.
15. **Thay đổi mặt điều khiển phải sửa đặc tả liệt kê đầy đủ trước rồi mới sửa hiện thực**; **trước khi bump agentcore phải qua test hợp đồng**.
16. **Không làm DSL workflow tổng quát, event sourcing, Global State Digest**. Route là một bảng cho một miền, khái quát hóa tức là thiết kế quá đà.

---

## 11. Chiến lược xác minh

### 11.1 Danh mục tài sản kiểm thử

| Tầng | Tài sản | Phạm vi |
|---|---|---|
| Đặc tả mặt điều khiển | `flow/router_exhaustive_test.go` | 120 nghìn tổ hợp của bảng quyết định Route được liệt kê đầy đủ + tính chất pure function/xác định/bảo toàn |
| Hợp đồng khung | `agents/agentcore_contract_test.go` | 5 giả định hành vi của agentcore, được điều khiển qua `Runner.Run` (phải chạy trước khi nâng cấp) |
| End-to-end của Engine | `host/engine_test.go` | mô hình fake + tool thật: viết xong cả sách / phán quyết thất bại / phán quyết bế tắc / trình tự chấp nhận rework / boundary hold thì dừng / bảo toàn cạnh tranh thoát / một giấy phép một chương |
| Phán quyết | `arbiter/arbiter_test.go` | ma trận parse/feedback retry/xác minh theo từng kịch bản/thu thập sự thật |
| Hợp đồng pipeline sự thật | test của store/tools | feedback pool qua khởi động lại, ghi vi phạm latest-wins / rewrite clear / tiêm novel_context, PlanStart giữ qua Init |
| Tầng văn phong | `assets/load_test.go` | tách vẫn nhất quán từng byte / ngữ nghĩa ghi đè ba tầng / cùng đường lắp ráp eval |
| Chất lượng ngữ nghĩa | `internal/eval` + decisions.jsonl | prompt/voice A/B, phát lại offline phán quyết (đang xây tập hồi quy) |

### 11.2 Kịch bản ổn định

- **A chạy dài**: 80~200 chương chạy xong một lần, Phase=complete. Cho phép provider failover, retry; cấm mọi tự động tiếp tục chạy.
- **B phục hồi sau crash**: sau bất kỳ step nào thì kill tiến trình → Resume → Route tiếp tục từ sự thật, không ghi lại artefact đã lưu, checkpoints không có step trùng. Crash trong kỳ lập kế hoạch đi theo PlanStartRecord.
- **C dao động provider**: thỉnh thoảng 503 → litellm failover, Worker không cảm nhận.
- **D can thiệp người dùng**: Steer trong lúc chạy → phản hồi phán quyết mức giây, gửi ranh giới hành động; Steer khi dừng máy → sau phán quyết thì kéo dậy theo ý định; crash → PendingSteer phát lại.

### 11.3 Tuân thủ (có thể viết thành linter / test)

- `flow.Route` phải là pure function: cấm đọc Store / mọi IO
- trong thân hàm `runEnded` không được phép có bất kỳ lời gọi khởi động engine nào
- cảnh phán quyết mới phải thêm theo cặp Collect/Decide + kiểu Decision + ghi xuống đĩa
- mã liên quan recovery chỉ được xuất hiện trong `host/resume.go` và `engine.planStartFallback`

### 11.4 Lặp chất lượng

Đổi văn phong → đổi `<thư mục sách>/style/` (cấp người dùng) hoặc assets/voice.md (tích hợp sẵn), xác nhận A/B bằng bộ đánh giá văn phong; thêm chiều đánh giá mới → sửa editor.md (save_review nhận cấu trúc); thêm tài liệu tham chiếu mới → đấu dây tường minh ở ba nơi (`tools.References` + `loadReferences` + ánh xạ tiêm novel_context).

**Thống kê phong cách toàn sách (`internal/stylestat`)**: Host tạo một `StyleStatsIndex` duy nhất cho mỗi cuốn sách, và tiêm tường minh vào `novel_context` và `commit_chapter`. Khi khởi động lần đầu sẽ khôi phục chỉ mục từ tất cả các chương đã hoàn thành, sau đó cập nhật tăng dần cho chương mới/viết lại (mẫu câu / cụm từ tần suất cao / câu lặp xuyên chương / hình thái cuối chương), tái dùng snapshot dưới cùng trạng thái sách và tiêm `episodic_memory.style_stats`: editor ra quyết định theo số, writer nhờ đó tự tránh. eval offline vẫn có thể gọi trực tiếp pure function `Compute`. **Thống kê thuộc về code, phán quyết thuộc về LLM**.

---

## 12. Tổng kết

> **Tầng sự thật là xác định, tầng ngữ nghĩa là tự trị.** Tự do của mô hình nằm ở nơi không thể xác minh được (viết gì, viết thế nào, phán thế nào), và bị ràng buộc ở nơi có thể xác minh được (thứ tự, idempotent, giai đoạn).

Không có task queue, không có policy engine, không có session thường trú. Chỉ có:

- Một vòng lặp Engine xác định, tuần tự (~500 dòng, sáu đường end-to-end được đóng đinh)
- Một bảng quyết định Route (pure function, đặc tả liệt kê đầy đủ 120 nghìn tổ hợp)
- Bốn hàm phán quyết Arbiter (sự thật đi vào, quyết định có cấu trúc đi ra, có thể phát lại sau khi ghi)
- Ba Worker chức năng (ngữ cảnh và mô hình độc lập, hàng rào sự thật không quấy rầy)
- 11 công cụ đơn tệp nguyên tử, phục hồi lỗi/idempotent tường minh xuyên file; trong đó commit dùng Saga bền vững + một file checkpoint jsonl

Lợi ích từ nâng cấp mô hình chảy về đâu là rất rõ: sáng tạo tốt hơn (mọi đầu ra của Writer/Architect/Editor), phán quyết chuẩn hơn (bốn kịch bản Arbiter), tóm tắt tốt hơn (ctxpack) — chỉ cần thay mô hình là có, vỏ ngoài không đổi một dòng. Mặt điều khiển không ăn lợi tức từ mô hình, vì **tra bảng không cần trí lực**; nó cần thứ đã được chứng minh đúng, và nó đã được chứng minh rồi.

Tính cứng của luồng là có chủ đích, có định giá, có chừa cửa: muốn nới thứ tự tool của writer → nới một đoạn prompt giao thức (bất biến được bọc ở tầng tool); muốn phân phát theo cung truyện → thêm một nhánh vào Route; muốn mở rộng năng lực phán quyết → thêm một cặp Collect/Decide. Mỗi lần nới lỏng đều có trọng tài (đặc tả liệt kê đầy đủ, đánh giá văn phong, phát lại decisions) — **dùng bằng chứng để quyết định nên cho mô hình bao nhiêu dây, chứ không dùng đức tin**.

Kỷ luật duy nhất: **khi có ai muốn thêm một điểm quyết định, hãy qua tam phân trước — cái nào có thể liệt kê thì đưa vào Route, cái nào ranh giới rõ thì đưa vào Arbiter, cái nào mở thì đưa vào Worker**. Nếu không thuộc cả ba, hãy nghĩ lại xem nó có thực sự tồn tại hay không.