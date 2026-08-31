# Thiết kế thống nhất cho quy tắc người dùng

## Một câu

Tất cả quy tắc viết dài hạn đều được chuẩn hóa vào cùng một bản chụp quy tắc của cuốn sách; khi chạy chỉ tiêm bản chụp này qua `novel_context`, không còn nhét lặp đi lặp lại văn bản quy tắc gốc vào prompt.

```text
prompt khởi động / tệp rules của người dùng / yêu cầu dài hạn trong lúc chạy
        ↓
LLM chuẩn hóa ngữ nghĩa (theo nguồn)
        ↓
Go hợp nhất xác định (theo độ ưu tiên)  ←  quy tắc mặc định hệ thống (tích hợp trong mã, đi thẳng vào hợp nhất, không qua LLM)
        ↓
output/novel/meta/user_rules.json
        ↓
tiêm novel_context
        ↓
Architect / Writer / Editor / kiểm tra commit dùng chung
```

## Trạng thái triển khai (2026-07-19, đã hoàn tất + đã sửa thiếu sót sau review)

Thiết kế này đã được triển khai, 24 package `go build` / `go vet` / `go test` đều xanh. Sau một vòng code review đã sửa 4 lỗ hổng (đều đã sửa): ① quy tắc prompt khởi động chỉ nối vào phương thức chết `Host.Start`, trong khi entry thực tế đi qua `StartPrepared` nên bỏ sót tạo bản chụp — đã truyền thẳng prompt gốc từ hai entry quick/cocreate, thống nhất gọi `Host.PrepareUserRules`; ② lỗi ghi bản chụp xuống đĩa bị nuốt — `PrepareUserRules` đổi thành nếu ghi xuống đĩa thất bại thì trả error và dừng mở sách (đường resume giữ best-effort, tránh đưa mode lỗi mới vào sách cũ); ③ lỗi đọc tệp rules bị lặng lẽ bỏ qua — `raw.go` ghi log với lỗi không phải "không tồn tại" (quyền hạn v.v.); ④ README vẫn dạy YAML/front matter cũ và trỏ tới tệp đã xóa — đã viết lại.

Phần triển khai về cơ bản nhất quán với tài liệu này, lựa chọn triển khai sau khi nâng cấp structured output như sau:

1. **Chuẩn hóa chỉ có một `Contract.Schema`, không duy trì hai bộ prompt.**
   Khi model tuyên bố hỗ trợ thì phát JSON Schema nguyên sinh; khi không hỗ trợ hoặc năng lực chưa biết, lớp contract thống nhất sẽ tiêm cùng một Schema vào prompt.
   Cả hai chế độ đều được phía Go kiểm tra lại Schema, sau đó thực hiện kiểm tra miền giá trị và nghiệp vụ xuyên trường.
2. **Khi một giá trị trường riêng lẻ bất hợp lệ thì hạ cấp thành "trường đó bị thiếu", thay vì hạ cấp toàn bộ nguồn.**
   Ví dụ một trường là placeholder rỗng hoặc sai kiểu, sanitize sẽ loại bỏ trường đó (xem như chưa khai báo), giữ lại các trường hợp lệ còn lại của nguồn đó;
   chỉ khi "toàn bộ lần chuẩn hóa thất bại" (mạng/model/JSON bất hợp lệ/phân tích thất bại) mới hạ cấp toàn bộ nguồn thành raw preferences,
   đặt `status=degraded`. Như vậy một trường xấu sẽ không kéo theo các quy tắc hợp lệ khác cùng nguồn. Lỗi output mà model có thể sửa sẽ mang theo
   nguyên nhân chính xác để tiếp tục tự chữa, vòng đời do `context` kiểm soát; lỗi kết thúc rõ ràng đi vào log và hạ cấp theo nguồn.

Vị trí mã: `internal/rules` (dữ liệu thuần + hợp nhất xác định: snapshot.go / raw.go / types.go), `internal/userrules`
(chuẩn hóa LLM + điều phối + ghi đĩa: normalize.go / service.go), `internal/store/user_rules.go` (lưu trữ bản chụp),
`internal/userrules/service.go` (ghi quy tắc khi chạy), `assets/prompts/arbiter-intervention.md` (phân luồng ba loại).
Đường cơ sở cơ học mặc định của hệ thống đã chuyển từ `assets/rules/default.md` sang tích hợp trong mã `rules.SystemDefaults()`, đường phân tích YAML và
phụ thuộc yaml.v3 đã bị xóa. **Chưa kiểm chứng**: toàn bộ chuỗi mở sách LLM thực / hành động Arbiter rules trong lúc chạy (nguyên mẫu normalizer offline đã kiểm 10/10).

## Vì sao

Writer ở mỗi chương không ổn định nhận được prompt đầy đủ ban đầu của người dùng. Nó chủ yếu dựa vào nhiệm vụ chương hiện tại và `novel_context(chapter=N)`.

Vì vậy quy tắc dài hạn không thể dựa vào trí nhớ lịch sử hội thoại, cũng không nên dựa vào regex để lén đoán từ ngôn ngữ tự nhiên. Cách đúng là: chuẩn hóa rõ ràng quy tắc dài hạn thành trạng thái, rồi phân phát thống nhất qua `novel_context`.

“Chuẩn hóa” ở đây phải tận dụng năng lực hiểu ngôn ngữ tự nhiên của mô hình lớn, chứ không liệt kê các cách diễn đạt trong Go. Chương trình chỉ định nghĩa một số ít trường có thể kiểm tra cơ học, chịu trách nhiệm schema, hợp nhất xác định, kiểm tra, ghi đĩa và kiểm tra commit; những cách diễn đạt như “mỗi chương khoảng một nghìn rưỡi chữ”, “một chương đừng quá hai nghìn”, “đừng viết kiểu bánh răng định mệnh nữa” do LLM hiểu ngữ nghĩa.

## Trạng thái thống nhất

Khi chạy, cuốn sách chỉ duy trì một nguồn sự thật quy tắc người dùng:

```text
output/novel/meta/user_rules.json
```

Hình dạng giữ đơn giản:

```json
{
  "version": 1,
  "status": "ready",
  "structured": {
    "genre": "tu tiên",
    "forbidden_chars": [],
    "forbidden_phrases": ["ở một mức độ nào đó"],
    "fatigue_words": {}
  },
  "preferences": "Nhân vật chính bình tĩnh và kiềm chế；ít giải thích, dùng hành động và đối thoại nhiều hơn.",
  "sources": [
    "startup_prompt",
    ".ainovel/rules/style.md"
  ],
  "uncertain": [
    "Ít dùng ẩn dụ：không có ngưỡng rõ ràng, xử lý như thiên hướng phong cách"
  ]
}
```

Ranh giới trường:

- `version`: phiên bản schema bản chụp, tiện cho di trú tương lai.
- `status`: `ready` / `degraded`, đánh dấu chuẩn hóa có thành công đầy đủ hay không; chỉ dùng để hiển thị lại và chẩn đoán, không tham gia phán đoán sáng tác.
- `structured`: quy tắc mà mã có thể kiểm tra cơ học hoặc tiêu thụ ổn định.
- `preferences`: thiên hướng ngôn ngữ tự nhiên không thể kiểm tra cơ học, nhưng có hiệu lực dài hạn với sáng tác.
- `sources`: kiểm toán nguồn, không tham gia phán đoán sáng tác.
- `uncertain`: chẩn đoán chuẩn hóa, chỉ dùng để hiển thị lại và điều tra, không tham gia phán đoán sáng tác.

Chỉ tiêm `structured` và `preferences` cho model; `version` / `status` / `sources` / `uncertain` là siêu dữ liệu vận hành và chẩn đoán, không vào `working_memory.user_rules`. Lỗi kỹ thuật không vào bản chụp, chỉ vào log (xem §Thất bại và hạ cấp).

## Nguồn đầu vào

Quy tắc dài hạn có bốn nguồn đầu vào:

1. **prompt khởi động**: yêu cầu dài hạn người dùng viết khi mở sách.
2. **tệp rules của người dùng**: thiên hướng dài hạn cấp global hoặc project, đọc như ngôn ngữ tự nhiên thông thường.
3. **quy tắc mặc định hệ thống**: đường cơ sở cơ học tích hợp trong mã.
4. **yêu cầu dài hạn trong lúc chạy**: người dùng giữa chừng nói “từ nay về sau hãy thế nào”, Arbiter trích xuất hành động `rules`, Host gọi `AddRuntimeRule`.

Các nguồn đầu vào này không trực tiếp đi vào Writer prompt, cũng không bị đọc lặp lại trong lúc chạy. Chúng chỉ tham gia chuẩn hóa khi tạo hoặc cập nhật bản chụp, kết quả được hợp nhất vào `meta/user_rules.json`.

## Tệp rules

Tệp rules là prompt dài hạn thông thường, không phải prompt khi chạy, cũng không phải tệp cấu hình. Nó chỉ làm đầu vào chuẩn hóa, không hỗ trợ YAML:

```md
# Sở thích viết

Mỗi chương 1200-1600 chữ.
Nhân vật chính bình tĩnh và kiềm chế, đừng thánh mẫu.
Ít giải thích, dùng hành động và đối thoại để đẩy tiến truyện nhiều hơn.
Đừng xuất hiện “ở một mức độ nào đó”.
```

Sau khi hệ thống đọc, chuẩn hóa thành:

```json
{
  "structured": {
    "forbidden_phrases": ["ở một mức độ nào đó"]
  },
  "preferences": "Mỗi chương 1200-1600 chữ；nhân vật chính bình tĩnh và kiềm chế, đừng thánh mẫu；ít giải thích, dùng hành động và đối thoại để đẩy tiến truyện nhiều hơn."
}
```

Nếu trong tệp xuất hiện YAML front matter, cũng xử lý như văn bản thông thường, không xem là khai báo có cấu trúc. Kết quả có cấu trúc chỉ đến từ quy trình chuẩn hóa thống nhất.

Sau khi khởi động, nếu người dùng sửa tệp rules, cuốn sách hiện tại sẽ không tự động thay đổi; cần tạo lại bản chụp. Như vậy sách cũ sẽ không bị hành vi trôi dạt do tệp rules global thay đổi.

## Chuẩn hóa ngữ nghĩa

Chuẩn hóa là một lần gọi LLM độc lập, bị ràng buộc bởi schema — mỗi nguồn tự chuẩn hóa một lần, không trộn vào sinh nội dung sáng tác, cũng không dựa vào biểu thức chính quy hoặc bảng từ khóa để phân tích cứng.

Đầu vào:

- Nguyên văn của một nguồn đơn lẻ (prompt khởi động / một tệp rules / một yêu cầu trong lúc chạy)
- Mô tả các trường `structured` mà hệ thống hiện hỗ trợ

Quy tắc mặc định hệ thống không nằm trong danh sách này — chúng là quy tắc có cấu trúc đã biên dịch, tích hợp trong mã, đi thẳng vào §Quy tắc hợp nhất, không qua normalizer.

Đầu ra:

- `structured` ứng viên của nguồn đó
- `preferences` ứng viên của nguồn đó
- `sources`
- `uncertain`

Trách nhiệm phía Go:

- Cung cấp schema.
- Kiểm tra kiểu trường và miền giá trị.
- Theo độ ưu tiên trong §Quy tắc hợp nhất, hợp nhất xác định các nguồn (LLM không quyết định độ ưu tiên nguồn).
- Lưu bản chụp.
- Tiêm bản chụp trong `novel_context`.
- Dùng cùng một bản chụp để kiểm tra cơ học trong `commit_chapter`.

Trách nhiệm phía LLM:

- Hiểu quy tắc ngôn ngữ tự nhiên của một nguồn đơn lẻ.
- Nâng các quy tắc rõ ràng, có thể kiểm tra cơ học lên `structured`.
- Giữ các thiên hướng thẩm mỹ, phong cách, nhân vật trong `preferences`.
- Giữ bảo thủ với nội dung không chắc chắn, không tự bịa ngưỡng.

### Nâng bảo thủ

`structured` là quy tắc cứng hoặc tham số ổn định, không phải “vùng phỏng đoán của model”. Việc nâng quy tắc phải bảo thủ:

- Chỉ khi người dùng diễn đạt rõ ràng, không mơ hồ mới ghi vào `structured`.
- `forbidden_chars` / `forbidden_phrases` là trường cấp error, phải đặc biệt bảo thủ; chỉ các cấm đoán rõ ràng kiểu “đừng xuất hiện X”, “cấm dùng X”, “đừng viết X” mới nâng.
- `fatigue_words` chỉ nâng khi người dùng đưa ra từ và ngưỡng rõ ràng; các yêu cầu không có ngưỡng như “ít dùng ẩn dụ”, “đừng quá văn viết”, “giảm câu cửa miệng” đưa vào `preferences`.
- Các ý muốn về số chữ/độ dài (“mỗi chương 3000 chữ”, “ngắn hơn chút”) đều đưa vào `preferences`: độ dài chương là phán đoán ngữ nghĩa của nhịp tự sự, không kiểm tra cơ học — biến con số thành ranh giới cứng sẽ dụ model bơm nước để vượt/không vượt ranh giới.
- Các yêu cầu không thể cơ học hóa, không có ngưỡng rõ ràng, phụ thuộc ngữ cảnh đều đưa vào `preferences`.

Nguyên tắc:

```text
Thà bỏ sót không đưa vào structured, hạ thành thiên hướng mềm;
không được đưa nhầm vào structured, tạo báo lỗi cứng mỗi chương.
```

Chi phí của trích lọc thiếu là thiên hướng phong cách yếu hơn một chút; chi phí của trích lọc sai là mỗi chương sinh ra sự thật quy tắc lỗi.

## Thất bại và hạ cấp

Chuẩn hóa là đường tăng cường, không phải điều kiện tiên quyết của sáng tác chính. Model hiểu thất bại tuyệt đối không được chặn việc viết sách.

- **Hạ cấp theo nguồn**: một nguồn nào đó chuẩn hóa thất bại (mạng / model / JSON bất hợp lệ / kiểm tra schema thất bại), nguồn đó hạ cấp thành raw preferences, không sinh `structured`; các nguồn thành công khác vẫn đóng góp `structured` bình thường.
- **Tự chữa do context kiểm soát**: lỗi request có thể retry, lỗi định dạng/Schema ở chế độ prompt và lỗi kiểm tra nghiệp vụ tiếp tục tự chữa, cho đến khi thành công hoặc `context` kết thúc; không đặt số lần cố định. Vi phạm contract nguyên sinh, từ chối trả lời, bị cắt cụt, kết thúc lỗi và lỗi request không thể retry lập tức lộ ra và hạ cấp theo nguồn.
- **Lỗi kỹ thuật vào log**: lỗi JSON / schema / mạng v.v. ghi vào log, không vào `working_memory.user_rules`, không làm đầu vào sáng tác.
- **Đánh dấu bản chụp**: khi bất kỳ nguồn nào hạ cấp, bản chụp `status=degraded`.
- **Ghi được thì tiếp tục**: miễn là `meta/user_rules.json` ghi được, sáng tác chính phải tiếp tục.
- **Chỉ ghi đĩa thất bại mới dừng**: chỉ khi bản chụp không thể ghi vào đĩa mới dừng, vì các lần chạy sau không có nguồn sự thật ổn định.

Contract `AddRuntimeRule` (trong lúc chạy): khi normalizer thất bại thì lưu bản chụp degraded,
không tiêm lỗi chuẩn hóa như JSON/schema/mạng vào quy trình sáng tác; chỉ khi ghi đĩa thất bại mới trả về error.

## Quy tắc mặc định hệ thống

`System defaults` là đường cơ sở cơ học tích hợp trong mã, không phải tệp rules của người dùng, cũng không dùng YAML.

Nó không qua LLM chuẩn hóa — đã ở dạng có cấu trúc, trực tiếp làm nguồn độ ưu tiên thấp nhất đi vào hợp nhất Go của §Quy tắc hợp nhất. Như vậy quy tắc mặc định không có vấn đề LLM thất bại, trôi dạt, chi phí.

Quy tắc cơ học mặc định hệ thống trước đây tạm nằm trong `assets/rules/default.md` (chi tiết triển khai cũ, không phải YAML người dùng cần tương thích); khi triển khai thiết kế này đã chuyển vào tích hợp mã `rules.SystemDefaults()`, đường phân tích YAML đã bị xóa (xem §Trạng thái triển khai).

Khi di trú, giữ lại chú thích cần thiết giải thích nguồn gốc ngưỡng, ví dụ một số ngưỡng từ mệt từ đến từ thực chứng chạy dài. Điều này không phải để tương thích YAML cũ, mà để người bảo trì tương lai biết vì sao ngưỡng mặc định tồn tại, khi nào nên điều chỉnh.

## Quy tắc hợp nhất

Thứ tự hợp nhất theo “càng cụ thể càng ưu tiên”:

```text
System defaults
→ Kết quả biên dịch Global rules
→ Kết quả biên dịch Project rules
→ Kết quả biên dịch Startup prompt
→ Runtime user update
```

Nguồn có độ ưu tiên cao ghi đè nguồn có độ ưu tiên thấp.

Hợp nhất do Go thực hiện xác định: LLM chỉ chuẩn hóa ngôn ngữ tự nhiên của một nguồn đơn lẻ thành `structured`/`preferences` ứng viên, Go làm ghi đè trường và nối văn bản theo thứ tự trên, độ ưu tiên không giao cho LLM quyết định.

- `structured`: ghi đè theo trường, trường cùng tên của nguồn sau ghi đè nguồn trước.
- `preferences`: không ghi đè lẫn nhau, ghép thành văn bản dễ đọc theo thứ tự ưu tiên (nguồn độ ưu tiên cao ở sau), để LLM thấy được thứ tự nguồn.

Giới hạn đã biết: `preferences` được sắp theo độ ưu tiên, nhưng Go không hóa giải xung đột. Trong chạy dài, nếu người dùng lần lượt đưa ra thiên hướng mềm mâu thuẫn nhau (ví dụ trước “bình tĩnh kiềm chế” rồi sau “nói nhiều”), cả hai đều được giữ trong văn bản, do LLM cân nhắc theo thứ tự và ngữ cảnh; nếu cần ghi đè cứng xác định, nên diễn đạt thành trường `structured` có thể cơ học hóa.

## Entry ghi đĩa

Chuẩn hóa, hợp nhất, ghi đĩa là cùng một bộ logic, nhưng có hai bên gọi, bắt buộc phân biệt rõ, nếu không sẽ trộn chuẩn bị khởi động vào ngữ cảnh sáng tác chính:

- **Mở sách / làm mới (phía khởi động, xác định)**: do Host / quy trình khởi động trực tiếp gọi bộ logic này để tạo bản chụp ban đầu, không đi vào vòng lặp sáng tác chính. Đây là tác vụ chuẩn bị khởi động xác định.
- **Cập nhật trong lúc chạy (hành động phán định can thiệp)**: hành động `rules` do Arbiter phân loại được Host trực tiếp gọi `userrules.Service.AddRuntimeRule`, tái sử dụng cùng một bộ logic kiểm tra / hợp nhất / ghi đĩa, lấy quy tắc mới không có điểm tiến độ làm `Runtime user update` để hợp nhất vào bản chụp.

(Về triển khai, khuyến nghị thu gom bộ logic này thành một service nội bộ, hai bên gọi dùng chung; tên cụ thể để triển khai quyết định.)

Bất kể bên gọi nào, cuối cùng đều ghi vào cùng một `meta/user_rules.json`. Logic ghi đĩa chỉ làm ba việc:

1. Kiểm tra trường có cấu trúc.
2. Hợp nhất vào bản chụp cuốn sách hiện tại theo độ ưu tiên của §Quy tắc hợp nhất.
3. Trả về sự thật quy tắc đầy đủ sau khi lưu.

Không làm:

- Không phái sub-agent.
- Không sửa outline.
- Không lặng lẽ nuốt trường bất hợp lệ (ghi nhận và hạ cấp, xem §Thất bại và hạ cấp).
- Không trực tiếp tiêm văn bản gốc như prompt cuối cùng.

Ví dụ cập nhật trong lúc chạy: người dùng nói “từ nay về sau hãy thế nào” (không có điểm tiến độ) → Arbiter phán định là hành động `rules` → Host qua `AddRuntimeRule` chuẩn hóa mục này → hợp nhất vào bản chụp với độ ưu tiên cao nhất dưới dạng `Runtime user update` → event stream hiển thị lại.

## Hiển thị lại

Mỗi lần tạo hoặc cập nhật bản chụp `user_rules`, đều phải hiển thị lại kết quả chuẩn hóa cho người dùng:

```text
Đã tạo bản chụp quy tắc của cuốn sách này:
- Quy tắc cơ học: mỗi chương 1200-1600 chữ; cấm cụm từ “ở một mức độ nào đó”
- Thiên hướng phong cách: nhân vật chính bình tĩnh và kiềm chế; ít giải thích, dùng hành động và đối thoại để đẩy tiến truyện nhiều hơn
- Chưa nâng thành quy tắc cơ học: ít dùng ẩn dụ (không có ngưỡng rõ ràng, xử lý như thiên hướng phong cách)
```

- Khởi động / làm mới: tái sử dụng năng lực log quy tắc khởi động hiện có để in bản chụp, không thêm cơ chế mới; trong kịch bản đồng sáng tác có thể gộp hiển thị lại vào bước xác nhận đồng sáng tác.
- Trong lúc chạy: sau khi `AddRuntimeRule` thành công, hiển thị lại qua event stream ("quy tắc viết đã được cập nhật và lưu bền vững").
- Hạ cấp: khi `status=degraded`, hiển thị lại cần nói rõ nguồn nào không thể phân tích, hiện đang chạy theo raw preferences, có thể tạo lại bản chụp.

Hiển thị lại không phải cổng phê duyệt lần hai; tác dụng của nó là cho người dùng biết hệ thống đã hiểu thành gì, sau khi phát hiện lỗi có thể tạo lại bản chụp.

## Cách Agent tiêu thụ

Tất cả agent chỉ xem:

```json
working_memory.user_rules
```

Phân công trách nhiệm:

- Architect: theo ý muốn về số chữ trong `preferences` để điều chỉnh mật độ cốt truyện mỗi chương và số lượng chia chương.
- Writer: viết theo quy tắc cứng trong `structured`, điều chỉnh phong cách theo `preferences`.
- Editor: duyệt theo cùng một bộ quy tắc.
- `commit_chapter`: dùng `structured` để kiểm tra cơ học và trả về violations.

Writer không hiểu lại prompt khởi động gốc, cũng không đọc tệp rules gốc.

## Phân loại can thiệp: ba hướng đi

Can thiệp trong lúc chạy được chia ba loại theo "muốn sửa gì":

- **Viết như thế nào** (bút pháp / phong cách / chất lượng: số chữ, cách dùng từ, từ cấm, kiểu câu, tỷ lệ đối thoại, định dạng tiêu đề v.v.) → hành động Arbiter `rules`, chuẩn hóa hợp nhất vào `meta/user_rules.json`. Ví dụ: “mỗi chương 1500 chữ”, “tiêu đề chỉ dùng tiếng Trung”, “nhân vật chính tổng thể bình tĩnh và kiềm chế”, “tỷ lệ đối thoại cao hơn một chút”.
- **Viết cái gì** (cốt truyện / cấu trúc / hướng đi nhân vật / độ dài) → architect, rơi vào compass / outline / hồ sơ nhân vật. Ví dụ: “quyển này viết tuyến chiến đấu nhiều hơn”, “từ chương 30 trở đi giọng điệu nhân vật chính chuyển lạnh”, “tăng lên 40 chương”.
- **Sửa phần đã viết** (viết lại / chỉnh sửa chương chỉ định) → editor, đưa vào hàng đợi PendingRewrites.

Tiêu chí: **“viết như thế nào” → rules; “viết cái gì” → architect; “sửa phần đã viết” → editor**.

## Các bước triển khai

1. Thêm store `meta/user_rules.json`.
2. Thêm pass chuẩn hóa LLM độc lập (theo nguồn), dùng schema ràng buộc output ứng viên `structured/preferences/sources/uncertain`.
3. Thêm hợp nhất xác định phía Go: theo độ ưu tiên ghi đè trường và nối văn bản cho từng nguồn, tạo bản chụp.
4. Thu gom chuẩn hóa / hợp nhất / ghi đĩa thành một bộ logic, hai bên gọi dùng chung: phía khởi động gọi trực tiếp để tạo bản chụp ban đầu; trong lúc chạy, hành động `rules` do can thiệp phán định tái sử dụng qua `AddRuntimeRule`. Khi thất bại xử lý theo §Thất bại và hạ cấp: nguồn hạ cấp thành raw preferences, bản chụp `status=degraded`, sáng tác chính tiếp tục.
5. Chuyển quy tắc cơ học mặc định hệ thống hiện tại trong `assets/rules/default.md` vào cấu trúc tích hợp mã hoặc JSON asset, giữ chú thích nguồn gốc ngưỡng; xóa đường phân tích YAML của user rules, không làm lớp tương thích.
6. Sau khi đọc tệp rules, không còn trực tiếp tiêm phần thân như prompt, mà chuẩn hóa rồi hợp nhất vào bản chụp `user_rules`.
7. `novel_context` chỉ tiêm `working_memory.user_rules` trong `meta/user_rules.json`.
8. `commit_chapter` dùng cùng một `user_rules.structured` để kiểm tra.
10. Phân loại can thiệp (hiện do Arbiter đảm nhiệm, arbiter-intervention.md) rõ ràng phân luồng theo ba loại "muốn sửa gì": yêu cầu dài hạn về phong cách / chất lượng viết đi hành động `rules` ghi vào bản chụp; cốt truyện / cấu trúc / nhân vật / độ dài đi architect; làm lại chương đã viết đi editor (xem chi tiết §Phân loại can thiệp: ba hướng đi).

## Tiêu chuẩn nghiệm thu

- Prompt khởi động của người dùng viết “mỗi chương 1200-1600 chữ”, `novel_context` của chương đầu Writer có thể thấy nguyên văn ý muốn này trong `preferences`.
- Tệp rules chỉ viết ngôn ngữ tự nhiên, cũng có thể được chuẩn hóa vào cùng một `user_rules` khi tạo bản chụp.
- Tệp rules không cần và cũng không hỗ trợ YAML; tất cả được chuẩn hóa như quy tắc ngôn ngữ tự nhiên.
- Khi chạy không còn đọc tệp rules; chỉ đọc `meta/user_rules.json`.
- Quy tắc cơ học mặc định không còn đến từ tệp YAML rules, user rules cũng không có lớp tương thích YAML.
- Chuẩn hóa không dùng regex/từ khóa hard-code; hiểu ngôn ngữ tự nhiên do LLM hoàn thành.
- Quy tắc mơ hồ sẽ không được nâng thành trường `structured` cấp error.
- Quy tắc mặc định hệ thống không qua LLM, đi thẳng vào hợp nhất Go.
- Độ ưu tiên nguồn và ghi đè trường do Go thực hiện xác định, cùng input tạo cùng bản chụp.
- Trong lúc chạy, người dùng nói “từ nay về sau hãy thế nào”, qua hành động Arbiter rules hợp nhất vào bản chụp, `novel_context` của các chương sau có thể thấy cập nhật.
- Chuẩn hóa thất bại không chặn viết sách: nguồn thất bại hạ cấp thành raw preferences, bản chụp `status=degraded`, sáng tác chính tiếp tục; chỉ khi bản chụp không thể ghi xuống đĩa mới dừng.
- Lỗi chuẩn hóa trả về `status=degraded`, không đẩy lỗi kỹ thuật lên làm nhiễu luồng chính.
- Sau khi tạo hoặc cập nhật snapshot, sẽ hiển thị lại `structured` / `preferences` / các mục chưa được nâng cấp; khi bị degrade thì hiển thị lại mô tả nguồn degrade.
- Mở một cuốn sách mới sẽ không kế thừa `user_rules` của cuốn sách trước.
- Các trường có cấu trúc không hợp lệ sẽ không bị âm thầm bỏ qua: ghi nhận và degrade nguồn đó, không chặn luồng chính.

## Rõ ràng không làm (xác định là không cần, không phải cắt giai đoạn)

Các năng lực sau không mang lại lợi ích trong nhu cầu hiện tại, không đưa vào thiết kế, để tránh thiết kế quá mức:

- Ngữ nghĩa xóa / hoàn tác cấp trường như `clear_fields`.
- Tự động làm mới khi lắng nghe thay đổi của file rules (sửa file thì cứ tạo lại snapshot một cách tường minh là được).
- Mốc thời gian / giải quyết ghi đè của `preferences` (nếu cần ghi đè cứng, vui lòng dùng `structured`).
- Lưu bền vững mảng `diagnostics` trong snapshot (lỗi kỹ thuật cứ đưa vào log, snapshot chỉ giữ `status`).
- Tự động sinh mô tả trường schema từ kiểu Go (duy trì thủ công một bản mô tả ngắn gọn là đủ).

Nguyên tắc thiết kế không đổi: LLM chịu trách nhiệm hiểu ngôn ngữ tự nhiên, Go chịu trách nhiệm hợp nhất xác định, kiểm tra hợp lệ, ghi xuống đĩa và kiểm tra.