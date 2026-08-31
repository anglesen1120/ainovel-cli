# Cổng chuyển chương

> Trạng thái: đã triển khai
> Ngày: 2026-07-14
> Giải quyết: nghiệm thu theo từng chương, tạm dừng an toàn sau can thiệp, cấp phép chương chính xác khi khôi phục sau sự cố

## 1. Vì sao cần nó

Rủi ro cốt lõi của sáng tác tự động truyện dài không phải là tiêu tốn thêm một lần gọi, mà là trong lúc người dùng đọc duyệt, hệ thống vẫn tiếp tục ghi chương mới, rồi đưa các tóm tắt, trạng thái nhân vật và phản hồi dàn ý được xây dựng trên cốt truyện cũ vào nguồn sự thật tiếp theo. Xóa chương viết thừa không thể tự động hoàn tác các trạng thái phái sinh này, khiến người dùng mất niềm tin vào quá trình sáng tác.

Dự án vẫn mặc định định vị là “sau khi đưa ra mục tiêu thì tiếp tục tự chủ hoàn thành”, vì vậy không biến xác nhận từng chương thành mặc định toàn cục. Hệ thống cung cấp hai chính sách rõ ràng:

- `auto`: chế độ mặc định, tiếp tục tự chủ tiến hành;
- `review`: chế độ nghiệm thu từng chương do người dùng chủ động chọn, mỗi chương mới theo chiều tiến đều cần một lần cấp phép chính xác.

Điều này không phải là giao workflow lại cho Coordinator LLM. Khi nào cần người dùng xác nhận là chính sách người dùng; quy trình xác định bước tiếp theo vẫn do Route suy luận; chỉ việc có cần tạm dừng một lần để nghiệm thu kết quả của một can thiệp nào đó hay không mới do Arbiter phán đoán về ngữ nghĩa.

## 2. Phân định ranh giới

| Vấn đề | Thuộc về | Lý do |
|---|---|---|
| Hiện tại có phải chế độ nghiệm thu từng chương không | RunMeta / Host | Ý định chạy bền vững của người dùng |
| Chương nào đã được cấp phép | RunMeta / Gate | Sự thật cơ học có thể xác minh, có thể khôi phục |
| Tiếp theo chạy Worker nào | `flow.Route` | Suy luận bằng hàm thuần từ sự thật sáng tác |
| Chỉ thị có bắt đầu một chương mới theo chiều tiến hay không | `flow.StartsForwardChapter` | Phán đoán cơ học có kiểu |
| “sửa xong cho tôi xem” có cần tạm dừng không | Arbiter | Phán đoán ngữ nghĩa ngôn ngữ tự nhiên |
| Khi nào kích hoạt tạm dừng | `ChapterAdvanceGate` | Thực thi xác định đối với ý định một lần |
| Ngân sách có cho phép tiếp tục không | `BudgetSentinel` | Chính sách Host độc lập |

`AdvanceMode`, cấp phép chương và hold một lần không đi vào bảng quyết định Route, cũng không cho phép mô hình sửa đổi. Máy trạng thái sáng tác của Route giữ trực giao với chính sách nghiệm thu từng chương.

## 3. Mô hình trạng thái tối thiểu

Trong `meta/run.json` chỉ thêm ba mục ý định chạy:

```go
type RunMeta struct {
	AdvanceMode          ChapterAdvanceMode `json:"advance_mode"`
	AdvancePermitChapter int                `json:"advance_permit_chapter,omitempty"`
	AdvanceHold          *AdvanceHold       `json:"advance_hold,omitempty"`
}

const (
	ChapterAdvanceAuto   ChapterAdvanceMode = "auto"
	ChapterAdvanceReview ChapterAdvanceMode = "review"
)

const (
	AdvanceHoldAtBoundary           AdvanceHoldAfter = "boundary"
	AdvanceHoldAfterRewritesDrained AdvanceHoldAfter = "rewrites_drained"
	AdvanceHoldAtChapter            AdvanceHoldAfter = "chapter"
)

type AdvanceHold struct {
	After         AdvanceHoldAfter `json:"after"`
	TargetChapter int              `json:"target_chapter,omitempty"`
	Reason        string           `json:"reason"`
}
```

Không có PolicyEngine tổng quát, mảng điều kiện, hàng đợi cấp phép, thời gian hết hạn hay phiên bản chiến lược. Chuyển chương chỉ giữ một chế độ bền vững, một cấp phép chính xác và một hold một lần có kiểu.

### 3.1 Bất biến

1. `AdvanceMode` chỉ có thể là `auto` hoặc `review`; giá trị chưa biết trả về `UnsupportedAdvanceModeError`.
2. Chế độ chưa biết không được khởi động Host, cũng không được ghi lại RunMeta.
3. Trong `auto`, cấp phép phải là `0`.
4. Trong `review`, cấp phép chỉ có thể là `0` hoặc một số chương nguyên dương.
5. Cấp phép lặp lại cùng mục tiêu là lũy đẳng, mục tiêu khác không được ghi đè cấp phép đang diễn ra.
6. Cấp phép chỉ ràng buộc “bắt đầu chương mới theo chiều tiến chưa hoàn thành”; lập kế hoạch, đánh giá, làm lại, đánh bóng và khôi phục commit không bị chặn.
7. Cấp phép gắn với số chương, không gắn với một lần chạy tiến trình hay một lần gọi Worker nào đó.
8. Chỉ khi chương mục tiêu đã vào `CompletedChapters`, `PendingCommit` tương ứng đã được xóa, và tồn tại checkpoint `commit` của chương đó, cấp phép mới được coi là đã tiêu thụ ổn định.
9. Chương mục tiêu đã hoàn thành nhưng thiếu commit checkpoint là trạng thái hỏng: báo lỗi rõ ràng và tạm dừng, không đoán cách sửa.
10. Cấp phép chưa hoàn thành phải bằng `Progress.NextChapter()`. `PendingRewrites` không thay đổi `NextChapter()`, vì vậy làm lại và cấp phép theo chiều tiến đang diễn ra có thể cùng tồn tại một cách cơ học.
11. `AdvanceHold` chỉ có thể dùng `boundary`, `rewrites_drained` hoặc `chapter`, và phải mang lý do không rỗng; `chapter` phải mang chương mục tiêu là số dương, các điều kiện khác bị cấm mang theo.
12. hold và cấp phép dùng compare-and-clear; khi trạng thái bị thay thế bởi hành động mới, không được xóa nhầm.
13. hold chương mục tiêu trong chế độ `review` cấu thành một ủy quyền theo khoảng một lần; sau khi tạm dừng, chính sách nghiệm thu từng chương ban đầu vẫn giữ nguyên.

## 4. Store API

RunMetaStore cung cấp các thao tác nguyên tử hẹp và có kiểu:

```go
SetAdvanceMode(mode domain.ChapterAdvanceMode) error
GrantAdvancePermit(chapter int) error
ClearAdvancePermit(chapter int) error
SetAdvanceHold(hold domain.AdvanceHold) error
ClearAdvanceHold(expected domain.AdvanceHold) error
```

- Khi chuyển lại `auto`, xóa cấp phép chương trong cùng một write lock, nhưng không xóa hold do một can thiệp người dùng khác tạo ra;
- Cấp phép chỉ hợp lệ trong `review`;
- Thao tác xóa chỉ tiêu thụ đúng cùng mục tiêu mà bên gọi vừa đọc;
- Khi khởi tạo RunMeta, chế độ mặc định là `auto`, đồng thời giữ lại chế độ, cấp phép và hold đã ghi xuống đĩa.

Dự án hiện không có dữ liệu lịch sử cần migrate, vì vậy triển khai không bao gồm đọc trường cũ, ghi kép hay nhánh hạ cấp.

## 5. Ngữ nghĩa hàm thuần

### 5.1 Nhận diện chương mới theo chiều tiến

```go
func StartsForwardChapter(
	inst *Instruction,
	progress *domain.Progress,
	pending *domain.PendingCommit,
) bool
```

Chỉ khi các điều kiện sau đồng thời thỏa mãn mới trả về true:

- Worker là `writer`;
- phase là `writing`;
- Không có `PendingCommit`;
- Không có hàng đợi làm lại;
- Không có `InProgressChapter`;
- Chương mục tiêu bằng `NextChapter()`.

Phán đoán chỉ đọc các trường có kiểu, không phân tích văn bản Task hoặc Reason.

### 5.2 hold một lần

`ResolveAdvanceHold` trả về theo hold và Progress:

- `keep`: điều kiện chưa thỏa mãn;
- `consume`: trạng thái hoàn tất sách chỉ cần dọn ý định;
- `consume-and-stop`: dọn ý định và tạm dừng.

`boundary` kích hoạt tại ranh giới Worker hiện tại; `rewrites_drained` kích hoạt sau khi hàng đợi làm lại được xả hết; `chapter` kích hoạt sau khi chương mục tiêu vào danh sách hoàn thành, `PendingCommit` được xóa và commit checkpoint tồn tại. Điều kiện chưa biết và sự thật thiếu đều báo lỗi trực tiếp.

## 6. ChapterAdvanceGate

Gate là thành phần chính sách tiến sáng tác duy nhất ngoài ngân sách, chỉ có hai trách nhiệm:

1. Phân tích và tiêu thụ hold một lần tại ranh giới vòng lặp;
2. Kiểm tra cấp phép từng chương trước khi phái writer, và đối soát xem cấp phép đã được tiêu thụ ổn định tại ranh giới hay chưa.

Thứ tự Engine là:

```text
Gửi can thiệp đang chờ xử lý
→ Kiểm tra ranh giới Gate
→ Route / lấy phiếu phái việc của Arbiter
→ precheck
→ Kiểm tra cấp phép phái việc Gate
→ Worker
→ Kiểm tra ranh giới Budget
→ Kiểm tra ranh giới Gate
→ Vòng tiếp theo
```

Khi `auto && hold == nil`, kiểm tra ranh giới đọc RunMeta rồi trả về ngay, không đọc Progress, PendingCommit hay checkpoint.

### 6.1 hold + dispatch

Arbiter có thể cắt “viết lại chương 3, sửa xong cho tôi xem” thành:

```json
{
  "hold": {
    "after": "rewrites_drained",
    "reason": "Chờ người dùng nghiệm thu sau khi viết lại hoàn tất"
  },
  "dispatch": {
    "agent": "editor",
    "task": "Rà soát chương 3 và thiết lập hàng đợi làm lại theo kết quả"
  }
}
```

Nhóm hành động này phải thực thi phiếu phái việc đi kèm trước, để Editor thiết lập sự thật làm lại, rồi Gate mới phán đoán hàng đợi đã xả hết hay chưa. Engine gắn “lần phái việc này hoãn Gate” với chỉ thị trong bộ nhớ đó, và xóa cùng lúc khi lấy chỉ thị; phiếu phái việc Arbiter thông thường không thể đi vòng Gate.

### 6.2 permit và làm lại

`reopen` khi sách hoàn tất chỉ có thể xảy ra ở `complete`, còn `/next` chỉ có thể xảy ra ở `writing`, hai điều này loại trừ nhau về mặt cơ học. `PendingRewrites` đã tồn tại trong giai đoạn viết không thay đổi chương đã hoàn thành lớn nhất, vì vậy cấp phép vẫn căn chỉnh với cùng một `NextChapter()`; Worker làm lại có thể chạy, nhưng sẽ không tiêu thụ cấp phép theo chiều tiến.

## 7. Khôi phục sau sự cố

Commit chương là saga nhiều bước, cấp phép không thể biểu diễn bằng một giá trị boolean “lần run tiếp theo được viết một chương”. Khi khôi phục, Gate đối soát dựa trên ba loại sự thật:

| Cửa sổ sự thật | Hành vi Gate |
|---|---|
| Chương mục tiêu chưa hoàn thành, không có PendingCommit | Giữ cấp phép, cho phép bắt đầu/khôi phục chương đó |
| PendingCommit thuộc về chương mục tiêu | Giữ cấp phép, để khôi phục commit hoàn tất |
| Chương mục tiêu hoàn thành, PendingCommit đã xóa, commit checkpoint tồn tại | Tiêu thụ cấp phép |
| Chương mục tiêu hoàn thành nhưng checkpoint thiếu | Báo lỗi và tạm dừng |
| Cấp phép trỏ tới chương chưa hoàn thành không phải NextChapter | Báo lỗi và tạm dừng |

Vì vậy, nếu tiến trình sập tại bất kỳ cửa sổ nào trong bản nháp, ghi trạng thái, đánh dấu tiến độ hoặc ghi tín hiệu, cũng sẽ không dùng sai cùng một cấp phép cho chương tiếp theo.

## 8. Arbiter

Schema can thiệp dùng `AdvanceHoldOp`:

```go
type AdvanceHoldOp struct {
	Cancel        bool                    `json:"cancel,omitempty"`
	After         domain.AdvanceHoldAfter `json:"after,omitempty"`
	TargetChapter int                     `json:"target_chapter,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
}
```

Quy tắc:

- “tạm dừng trước đã” rõ ràng dùng `boundary`;
- Trong `auto`, “sửa chương đã viết, sửa xong để tôi nghiệm thu” dùng `rewrites_drained`;
- “viết đến chương N” dùng `chapter`, phân biệt nghiêm ngặt với điều chỉnh độ dài “toàn bộ sách có N chương”;
- `review` đã dừng từng chương, không tạo hold đồng nghĩa lặp lại;
- hold chương mục tiêu trong `review` là ủy quyền theo lô một lần do người dùng ký rõ ràng;
- “tiếp tục” có thể hủy hold hiện có, nhưng không thể cấp phát cấp phép chương;
- Chuyển chế độ chỉ có thể dùng `/review on|off`, cho qua chỉ có thể dùng `/next`.

Engine gọi trực tiếp RunMetaStore để áp dụng hành động có cấu trúc, không ngụy trang nó thành LLM Tool.

## 9. Giao diện người dùng

### 9.1 `/review on|off`

- `/review on`: ngay lập tức lưu bền vững chính sách nghiệm thu từng chương; nếu Worker đang chạy, sau khi công việc hiện tại hoàn tất sẽ dừng trước chương mới theo chiều tiến tiếp theo;
- `/review off`: chuyển về tự động tiến hành và xóa nguyên tử cấp phép; sẽ không ngầm khởi động Engine đã tạm dừng, sự kiện sẽ nhắc rõ người dùng nhập chỉ thị tiếp tục.

### 9.2 `/next`

Chỉ khả dụng khi các điều kiện sau đồng thời thỏa mãn:

- Engine chưa chạy;
- Không phải đồng sáng tác theo giai đoạn;
- Chế độ là `review`;
- Không có hold đang chờ xử lý;
- Ngân sách cho phép;
- phase là `writing`.

Lệnh cấp phát cấp phép chính xác cho `NextChapter()` và khởi động Engine. Thông báo sẽ nêu rõ: sau khi chương này được commit, đánh giá cần thiết và bảo trì cấu trúc arc/volume vẫn sẽ được hoàn thành, rồi lại chờ cho phép tiếp.

### 9.3 Hiển thị trạng thái

`UISnapshot` là nguồn sự thật duy nhất của TUI, bao gồm:

- `AdvanceMode`;
- `AdvancePermitChapter`;
- `HasAdvanceHold`;
- `AdvanceHoldReason`.

Thanh bên hiển thị trạng thái tự động/nghiệm thu từng chương và chương đã cho phép; khi chờ, ô nhập nhắc “nhập ý kiến sửa đổi, hoặc `/next` cho phép chương tiếp theo”. Notification kind là `advance_gate`.

## 10. Xác minh

Kiểm thử bao phủ:

- Chuyển đổi trạng thái nguyên tử và compare-and-clear của chế độ RunMeta, cấp phép, hold;
- Chế độ chưa biết thất bại rõ ràng và không ghi lại RunMeta;
- Nhận diện bằng hàm thuần đối với chương mới theo chiều tiến và làm lại/khôi phục;
- Ngữ nghĩa hold của boundary, làm lại chưa xả hết, làm lại đã xả hết và hoàn tất sách;
- Chặn khi không có cấp phép, cho qua bằng cấp phép chính xác, báo lỗi cấp phép sai chương;
- Giữ cấp phép trong thời gian PendingCommit, tiêu thụ sau commit ổn định;
- Tạm dừng khi đánh dấu hoàn thành xung đột với checkpoint;
- permit và PendingRewrites xen kẽ không báo nhầm;
- Engine end-to-end chứng minh một cấp phép vừa đúng chỉ ổn định một chương mới;
- Khi Gate đã đánh dấu tạm dừng nhưng goroutine Engine cũ vẫn đang thoát, `/next` từ chối rõ ràng việc vào lại, thử lại sau đó khôi phục lũy đẳng theo cấp phép cùng chương;
- Hồi quy hold-only, hold+dispatch và race khi thoát.

## 11. Rõ ràng không làm

- Không để mô hình quyết định chế độ chạy hoặc cấp phát cấp phép;
- Không sửa Route để thích ứng với chiến lược xác nhận người dùng;
- Không biến làm lại, lập kế hoạch, đánh giá và bảo trì cấu trúc đều thành xác nhận từng bước;
- Không thêm PolicyEngine tổng quát, danh sách StopCondition hay DSL chiến lược;
- Không cung cấp tiền cấp phép nhiều chương hoặc hàng đợi cấp phép;
- Không giữ mô hình tạm dừng cũ, trường tương thích, DTO migrate hay chuỗi ghi kép;
- Không hạ cấp im lặng cho chế độ tương lai chưa biết.

Trong tương lai nếu xuất hiện nhu cầu ranh giới tự trị mới, được xác minh lặp lại, thì sẽ mở rộng chế độ dựa trên bằng chứng; hiện tại chi phí hối tiếc thấp chính là khả năng tương thích tương lai.