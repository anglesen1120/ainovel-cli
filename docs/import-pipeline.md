# Pipeline nhập ngữ nghĩa tiểu thuyết bên ngoài

> Trạng thái: đã triển khai (v1, `internal/host/imp`; cứu vớt tiền tố bị cắt ngắn kèm giai đoạn ba · bổ sung)
> Ngày: 2026-07-15
> Mục tiêu: để việc nhập tiểu thuyết bên ngoài vừa có thể liên tục hưởng lợi từ nâng cấp năng lực mô hình, vừa có bảo đảm kỹ thuật rằng toàn văn không mất, lỗi có thể chẩn đoán, sự cố có thể khôi phục và phát hành có thể xác minh.
> Sửa đổi: thứ tự SourceUnit theo thứ tự số `(Line, Part)` (§7.3/§8.3); cứu vớt tiền tố bị cắt ngắn được hạ xuống thành tối ưu hiệu suất có thể để sau và phải quan sát được (§9.5/§13.3/§19); mô hình chức năng ngữ nghĩa mở thành núm điều chỉnh (§13.1/§17).
> Sửa đổi 2026-07-16: núm điều chỉnh cấp mô hình được hiện thực hóa thành cấu hình roles `import_segment/import_analyze/import_synthesize` (§13.1); tái phân đoạn bằng ngôn ngữ tự nhiên được hiện thực hóa thành `--guide` và đầu vào ngữ nghĩa `guidance.txt` của workspace (§18.3); thất bại ngữ nghĩa thống nhất lưu phản hồi gốc vào failures/ (§14.2); xác nhận phân đoạn hỗ trợ phím `y` trong panel để cho qua một lần (§8.4); nhập chưa hoàn tất sẽ chủ động nhắc khi khởi động (§18.2). Chế độ JSON Schema (§13.2 cấp 1) tạm thời chưa triển khai, đánh dấu TODO để cải tạo thống nhất với các điểm gọi mô hình khác trong toàn kho.

## 1. Một câu

Nhập không phải là “dùng regex cắt văn bản, rồi để mô hình một lần nhả ra JSON cả quyển sách”, cũng không phải một Import Agent chạy tự do; nó là một **pipeline biên dịch ngữ nghĩa theo giai đoạn**:

> Mô hình chịu trách nhiệm hiểu ngữ nghĩa mở, code chịu trách nhiệm tọa độ, phủ, kiểu, hash, thứ tự và tính idempotent; tất cả sản phẩm ngữ nghĩa chỉ sau khi đã xác minh xong trong workspace độc lập mới được phát hành sang trạng thái sách chính thức.

```text
Văn bản bên ngoài
  → Đọc và chuẩn hóa xác định
  → LLM nhận diện ranh giới chương/ quyển/ văn bản phụ trợ
  → Code xác minh toàn văn được phủ hết
  → Người dùng xác nhận phân đoạn (có thể ủy quyền chấp nhận tự động rõ ràng)
  → LLM trích xuất sự thật theo từng lô chương liên tiếp
  → LLM tổng hợp ngữ nghĩa toàn sách theo tầng
  → Code ghép và xác minh Foundation
  → Phát hành Foundation và chương theo kiểu idempotent
  → Mặc định tạm dừng một lần; chỉ khi có --continue rõ ràng mới nối tiếp theo cổng kiểm chuẩn bình thường
```

## 2. Vì sao bắt buộc phải tái cấu trúc

Triển khai hiện tại là:

```text
Cắt chương bằng regex
  → Toàn bộ chính văn chương được đưa một lần vào ReverseFoundation
  → Mô hình xuất một lần premise / characters / world_rules / toàn bộ dàn ý chương / compass
  → Ghi ngay vào Foundation chính thức
  → Sau đó lần lượt đọc lại cùng một chính văn, phân tích và commit từng chương
```

Nó có bốn vấn đề mang tính cấu trúc.

### 2.1 Cắt chương đang enumerate ngữ nghĩa mở

Tiêu đề chương không có ngữ pháp đóng. Tiếp tục thêm regex “Chương N”, “Quyển N”, “Chapter N” chỉ có thể bao phủ những dạng đã thấy, không thể bao phủ tiêu đề do tác giả tự đặt, bố cục trộn lẫn, cấp bậc quyển/chương và định dạng tương lai.

Nghiêm trọng hơn, hiện tại việc cắt sẽ khiến các ranh giới không khớp bị biến mất ngay trong kết quả, và có thể âm thầm loại bỏ đoạn văn trước tiêu đề đầu tiên, chương rỗng và nội dung bị đánh giá là nhiễu cuối. Code không thể chứng minh những nội dung này đáng bị loại bỏ.

### 2.2 Input và output của lời gọi Foundation đều tăng tuyến tính theo số chương

`ReverseFoundation` đồng thời đảm nhiệm hiểu toàn bộ sách và sinh dàn ý đầy đủ cho tất cả chương: input chứa toàn bộ chính văn, output chứa cấu trúc chi tiết của từng chương. 54 chương đã có thể làm JSON bị cắt ngắn; tăng `max_tokens` chỉ đẩy điểm lỗi sang những cuốn dài hơn.

### 2.3 Trước khi thất bại đã sửa trạng thái chính thức

Foundation và từng chương được phân tích rồi phát hành song song. Khi bước sau thất bại, người dùng nhận được một trạng thái sách chính thức đã nhập một nửa, một nửa còn chưa phân tích. `from=N` hiện tại chỉ là giả định người dùng biết nên khôi phục từ đâu, không thể chứng minh file nguồn, kết quả phân đoạn và các chương sẵn có vẫn nhất quán.

### 2.4 Nhiều kết luận ngữ nghĩa bị hard-code

Phương án hiện tại còn cố định:

- phần chính văn nhập chỉ có thể là một quyển;
- chỉ có thể chia thành 1～3 arc;
- dùng ngưỡng 25/80 theo số chương đã nhập để chọn short/mid/long;
- để cho phép tiếp viết mà thiên về ép sinh `open_threads`;
- mỗi chương bắt buộc phải có nhân vật, số lượng sự kiện cố định và loại hook cố định.

Tất cả những điều này không phải là факт có thể chứng minh cơ học từ định dạng file; chúng phải do mô hình phán đoán dựa trên chính văn, hoặc do người dùng biểu đạt rõ ý định.

## 3. Mục tiêu và ngoài phạm vi

### 3.1 Mục tiêu

1. **Định dạng mở có thể hiểu**: không yêu cầu người dùng đổi tiểu thuyết sang định dạng tiêu đề tích hợp, cũng không yêu cầu người dùng tự viết regex.
2. **Toàn văn có thể bàn giao**: mỗi đoạn nguồn không rỗng đều phải thuộc về một chương hoặc vùng phụ trợ rõ ràng, cấm mất mát âm thầm.
3. **Quy mô có thể kiểm soát**: không còn một lần gọi đọc toàn bộ chính văn và xuất toàn bộ đối tượng chương; phân đoạn, batch chương hai ngân sách và tổng hợp theo khoảng đều có ranh giới input/output cục bộ, output toàn cục chỉ tăng theo độ phức tạp ngữ nghĩa thực như nhân vật, quyển, arc.
4. **Thất bại không làm ô nhiễm**: trước khi phân tích ngữ nghĩa và xác minh Foundation hoàn tất, không ghi trạng thái sáng tác chính thức.
5. **Khôi phục chính xác**: khôi phục dựa trên snapshot nguồn và `InputDigest` của artifact, không phụ thuộc `from=N` hay trí nhớ của người dùng.
6. **Hưởng lợi trực tiếp từ mô hình**: mô hình mạnh hơn cải thiện trực tiếp nhận diện ranh giới, trích xuất факт, chia arc của quyển và phán đoán tiếp viết, không cần thêm quy tắc Go.
7. **Tái sử dụng ngữ nghĩa commit chính thức**: phát hành chương tiếp tục dùng năng lực idempotent của `PendingCommit`, checkpoint và digest trong `commit_chapter`.
8. **Quan sát đầy đủ**: tiến độ, danh tính mô hình, mức dùng, phản hồi gốc khi thất bại và lỗi cuối cùng đều có điểm ghi nhận rõ ràng.
9. **Tương tác và tự động hóa song hành**: mặc định để người dùng xác nhận các ranh giới ngữ nghĩa có rủi ro cao, đồng thời cung cấp ủy quyền rõ ràng không người giám sát; đường tự động không dựa vào đoán ngầm im lặng.

### 3.2 Ngoài phạm vi

- Không xây Coordinator hay vòng lặp dài của agent tổng quát.
- Không xây framework Workflow/PolicyEngine/đồ thị tác vụ tổng quát.
- Không tự sửa hay viết lại nguyên văn của người dùng.
- Không hiện thực database, truy hồi vector hay song song phân tán cho nhập.
- Không hỗ trợ nhập mơ hồ và gộp tiểu thuyết khác vào sách hiện có.
- Không hiện thực di trú trạng thái kiểu cũ `from=N` hay tương thích hai luồng.
- Không mở rộng EPUB/PDF trong RFC này; bản đầu vẫn chỉ nhận txt/md, lớp đọc vẫn cục bộ, tương lai có thể thay thế mà không đổi hợp đồng phía sau.

## 4. Ranh giới trách nhiệm

| Vấn đề | Thuộc về | Lý do |
|---|---|---|
| Giải mã byte, chuẩn hóa xuống dòng | Go | Định dạng file và chuyển đổi xác định |
| Vị trí nguồn nào là tiêu đề chương, tiêu đề quyển hay văn bản phụ trợ | LLM | Ngữ nghĩa mở, không thể liệt kê hết |
| Tiêu đề tương ứng với vị trí nguồn ổn định nào | Go | Có thể xác minh cơ học SourceUnit, neo nguyên văn và phạm vi byte |
| Một chương nói về điều gì | LLM | Hiểu ngữ nghĩa văn học |
| Nhân vật, quy tắc thế giới, mồi câu và quan hệ được khái quát thế nào | LLM | Khái quát ngữ nghĩa xuyên chương |
| Ranh giới arc, câu chuyện đã khép hay chưa, mức độ lập kế hoạch | LLM | Phụ thuộc hình thái tự sự, không phụ thuộc ngưỡng cố định |
| Phạm vi chương có tăng dần, không chồng lấn, phủ hết hay không | Go | Bất biến có thể chứng minh |
| Kiểu JSON, enum đóng, số chương được tham chiếu có hợp lệ không | Go | Hợp đồng được kiểu hóa |
| Có thể tái sử dụng phân tích sẵn có hay không | Host/Workspace | Chỉ khi input ngữ nghĩa thực có thể tái tạo cùng `InputDigest` |
| Khi nào ghi trạng thái sách chính thức | Host/Store | Giao thức phát hành và khôi phục sau sự cố |
| Có cho phép tiếp tục theo cách phân đoạn hiện tại hay không | Người dùng/Intent | Xác nhận tương tác hoặc `--yes` rõ ràng, không để code âm thầm trả lời thay |

Các lời gọi LLM ở đây không phải control plane của Arbiter, cũng không phải vòng sáng tạo của Worker. Chúng là **hàm ngữ nghĩa** với ranh giới rõ ràng: input là факты đã kiểu hóa, output là kết quả ngữ nghĩa đã kiểu hóa, Host kiểm tra rồi mới thực thi.

## 5. Kiến trúc tổng thể

```text
[TUI / Headless]
       │ /import <path> / ủy quyền tự động / xác nhận / hủy
[Host]
       │ vòng đời nhập, sự kiện, runtime mô hình độc quyền
[imp.Runner]
       ├── LoadState → NextAction (chỉ suy ra từ факт workspace)
       ├── Source     đọc, giải mã, chuẩn hóa, snapshot
       ├── Segment    projection cấu trúc → LLM nhận diện ranh giới → kiểm tra phủ
       ├── Analyze    batch liên tiếp với hai ngân sách → tạm lưu факт từng chương
       ├── Synthesize tổng hợp theo tầng → BookSynthesis
       ├── Validate   ghép và xác minh Foundation đầy đủ
       └── Publish    Foundation chính thức → commit_chapter
               │
[workspace meta/import]          [Store chính thức]
snapshot nguồn/phân đoạn/phân tích/tổng hợp      Progress/Checkpoint/Artifact/PendingCommit
```

Runner là một bộ điều phối giai đoạn xác định bình thường, không có năng lực quyết định tự do. Mỗi lần nó chỉ thực thi một hành động được suy ra từ `NextAction`, xong hành động lại đọc факт.

## 6. Workspace và suy diễn trạng thái

Các факт trong quá trình nhập được lưu dưới thư mục sách:

```text
meta/import/
├── manifest.json
├── intent.json
├── source.txt
├── guidance.txt          # nếu tồn tại: chỉ dẫn phân đoạn bằng ngôn ngữ tự nhiên của người dùng (--guide), là input ngữ nghĩa của segmentation
├── segmentation.json
├── confirmation.json
├── analyses/
│   ├── 000001.json
│   ├── 000002.json
│   └── ...
├── range-digests/
│   ├── 000001-000050.json
│   └── ...
├── synthesis.json
├── story-resolution.json
└── failures/
    ├── last.json
    └── last-response.txt
```

Bản đầu giữ lại workspace. Nó vừa là căn cứ khôi phục, vừa là hồ sơ kiểm toán nhập; không thêm cơ chế dọn dẹp tự động hay lưu trữ lịch sử.

`intent.json` lưu ủy quyền rõ ràng của người dùng khi khởi động nhập (tự xác nhận, preselect trạng thái câu chuyện uncertain, có bỏ qua Hold hoàn tất hay không). Đây là ý định người dùng vẫn phải tuân theo sau khi khôi phục, không phải trạng thái giai đoạn có thể suy từ artifact; sau khi tạo xong, Runner không được âm thầm ghi đè.

### 6.1 Manifest

```go
type ImportManifest struct {
	Version          int    `json:"version"`
	SourceName       string `json:"source_name"`
	RawSHA256        string `json:"raw_sha256"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Encoding         string `json:"encoding"`
	SizeBytes        int64  `json:"size_bytes"`
	CreatedAt        string `json:"created_at"`
}

type ImportIntent struct {
	Version             int    `json:"version"`
	AutoConfirm         bool   `json:"auto_confirm,omitempty"`
	StoryResolution     string `json:"story_resolution,omitempty"` // open / closed
	ContinueAfterImport bool   `json:"continue_after_import,omitempty"`
}
```

- `source.txt` là snapshot cục bộ đã chuẩn hóa, khi khôi phục không còn phụ thuộc vào việc đường dẫn gốc có còn tồn tại hay không;
- Manifest không lưu đường dẫn nguồn tuyệt đối, tránh lộ thư mục máy và loại bỏ vấn đề khôi phục do file bị di chuyển;
- Intent chỉ nhận giá trị đóng, lưu chính xác ủy quyền của người dùng trong lệnh khởi động; khi khôi phục không suy ngược ý định cũ từ advance mode hiện tại;
- Khi schema version không khớp thì yêu cầu rõ ràng dùng phiên bản khớp để tiếp tục hoặc nhập lại, không đoán di trú.

Lúc tạo lần đầu, hãy ghi đủ và kiểm tra manifest, intent, source trong thư mục tạm cùng cấp trước, rồi rename thư mục để phát hành thành `meta/import/`; không có `meta/import/` thì chưa tính là workspace hoạt động. Như vậy bộ ba khởi đầu sẽ không vào `NextAction` dưới trạng thái khởi tạo nửa chừng, và cũng không cần thêm `stage=initializing` cho quá trình tạo. Khi khởi động phát hiện thư mục khởi tạo còn sót lại thì phải nhắc rõ và giữ thông tin chẩn đoán, không tự động coi là workspace thành công, cũng không âm thầm xóa.

### 6.2 Không lưu enum giai đoạn có thể trôi

Trạng thái bền không ghi các trường điều khiển kiểu `stage=analyzing`, `current=37`. Hành động tiếp theo được suy từ artifact:

```text
không có manifest/intent/source   → ingest
không có segmentation            → segment
không có confirmation khớp với digest input của segmentation → await_confirmation / auto_confirm
có phân tích chương thiếu hoặc không khớp digest input             → analyze_first_missing
thiếu RangeDigest hoặc synthesis khớp input                         → synthesize_first_missing
story_status=uncertain và không có lựa chọn người dùng khớp         → await_story_resolution
artifact chính thức và synthesis không nhất quán                 → publish
tất cả artifact chính thức đều nhất quán                          → done
```

`Stage` trong sự kiện chỉ dùng để hiển thị UI, không phải nguồn sự thật để khôi phục.

### 6.3 Định danh artifact thống nhất

Không hiện thực dependency graph. Mọi artifact ngữ nghĩa trong workspace đều dùng cùng một quy tắc định danh:

```go
type Artifact[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	InputDigest   string `json:"input_digest"`
	Payload       T      `json:"payload"`
}
```

`InputDigest` bao trùm toàn bộ **input ngữ nghĩa** mà hành động thực sự tiêu thụ, được mã hóa theo thứ tự cố định rồi tính toán:

- segmentation: nội dung nguồn đã chuẩn hóa, projection SourceUnit, hướng dẫn của người dùng và prompt/schema version của phân đoạn;
- confirmation: nội dung segmentation và cách xác nhận;
- phân tích chương: phạm vi batch chương và chính văn, ledger tính liên tục trước khi vào batch, prompt/schema version và hướng dẫn của người dùng;
- RangeDigest/BookSynthesis: nội dung phân tích đã có thứ tự hoặc digest tầng dưới mà mỗi bên tiêu thụ, prompt/schema version của tổng hợp;
- story resolution: nội dung synthesis và lựa chọn người dùng;
- phát hành: nội dung chuẩn hóa của đối tượng miền sắp phát hành.

provider/model, usage, thinking và các факт thực thi được ghi vào provenance/session, không làm mất hiệu lực tự động của phân tích đã thành công chỉ vì cấu hình mô hình thay đổi; nếu người dùng yêu cầu phân tích lại thì phải xóa rõ artifact tương ứng. Quyết định tái sử dụng cache chỉ nhìn xem hành động hiện tại có thể tái tạo cùng một `InputDigest` hay không.

`NextAction` đi theo pipeline tuyến tính cố định để tìm artifact đầu tiên bị thiếu, lỗi phân tích hoặc không khớp `InputDigest`. Khi phân đoạn lại, sửa hướng dẫn người dùng hoặc thay đổi факты đầu vào, các tầng sau sẽ tự lệch khớp; không viết quy tắc mất hiệu lực kiểu “khi phân đoạn đổi thì phải xóa tay những file nào”.

Khi phát hành, so sánh từng mục giữa artifact chính thức và kết quả tổng hợp; nếu giống thì bỏ qua idempotent, nếu khác thì báo xung đột, không ghi đè đoán mò. Vì vậy loại bỏ `ResumeFrom`. Khôi phục chỉ cần chạy lại `/import`; Runner sẽ tiếp tục từ факт đầu tiên còn thiếu.

## 7. Đọc file nguồn

### 7.1 Giải mã

Bản đầu hỗ trợ:

- UTF-8 / UTF-8 BOM;
- GB18030 (bao phủ các văn bản tiểu thuyết GBK phổ biến).

Kết quả giải mã phải trả về encoding đã chọn, và ghi vào Manifest cùng event tiến độ. Không được giấu “thử GB18030” thành fallback im lặng. Nếu không giải mã đáng tin cậy hoặc xuất hiện ký tự thay thế không chấp nhận được thì thất bại ngay, lỗi phải bao gồm kết quả phát hiện.

### 7.2 Chuẩn hóa

Chỉ làm các chuyển đổi không thay đổi nội dung văn học:

- loại bỏ BOM;
- CRLF/CR thống nhất thành LF;
- giữ nguyên dòng trống, thụt đầu dòng, dòng tiêu đề và ký tự chính văn;
- không xóa văn bản đầu file, chương rỗng, quảng cáo, thông tin bản quyền hay cái gọi là nhiễu cuối.

Mọi quyết định loại trừ đều để lại cho kết quả ngữ nghĩa của phân đoạn và hiển thị trong bản xem trước.

### 7.3 Tọa độ ổn định

Văn bản đã chuẩn hóa dựng một bảng `SourceUnit` thống nhất:

```go
type SourceUnit struct {
	ID        string // L1257; line vượt ngân sách sẽ tách thành L1257.1, L1257.2
	Line      int
	Part      int
	StartByte int
	EndByte   int
	Text      string
}
```

- `ID` chỉ dùng để hiển thị và mô hình tham chiếu; mọi quyết định về thứ tự, bao hàm và tăng dần đều phải so sánh theo tuple số `(Line, Part)`, cấm so sánh thứ tự từ điển trên chuỗi ID (`"L900"` theo thứ tự từ điển sẽ lớn hơn `"L1000"`); JSON projection giữ id dạng chuỗi, phía Go phân tích thành `(Line, Part)` rồi mới so sánh;
- line bình thường tương ứng một unit, đường đi thường gặp vẫn là tọa độ theo số dòng trực quan;
- khi một line dài vượt ngân sách projection cấu trúc, Go chỉ tạo nhiều **virtual unit** tại ranh giới ký tự UTF-8;
- các mảnh ảo không ghi ngược vào `source.txt`, không chèn soft newline, không thay đổi bất kỳ ký tự nguồn nào;
- khi có ranh giới bên trong cùng một unit, mô hình trả về unit ID và một neo nguyên văn được sao chép từng chữ; Go yêu cầu neo phải duy nhất trong unit đó rồi mới ánh xạ thành vị trí byte chính xác;
- nếu neo không tồn tại hoặc không duy nhất, trả lỗi cụ thể về cho mô hình, cấm đoán offset, cắt ngắn văn bản hoặc yêu cầu người dùng sửa nguyên tác trước.

Vì vậy văn bản phân chương bình thường vẫn giữ mô hình số dòng, đoạn dài không có xuống dòng, một dòng chứa nhiều chương hoặc line quá dài cũng xử lý bằng cùng một kiểu tọa độ.

## 8. Phân đoạn ngữ nghĩa

### 8.1 Projection cấu trúc

Mô hình nhìn thấy projection cấu trúc được chia theo ngân sách ngữ cảnh:

```json
{
  "owned_units": {"start": "L1200", "end": "L1800"},
  "context_units": {"start": "L1180", "end": "L1820"},
  "units": [
    {"id": "L1200", "line": 1200, "text": "Gió từ ngoài cổng thành thổi tới.", "blank_before": true},
    {"id": "L1257", "line": 1257, "text": "Quyển hai·Bắc cảnh", "blank_before": true, "blank_after": true}
  ],
  "user_guidance": ""
}
```

Vùng ngữ cảnh có thể chồng lấn, nhưng mỗi lần gọi chỉ được trả kết quả cho `owned_units`, vì vậy không tồn tại bỏ phiếu giữa các khối chồng lấn hay hợp nhất xung đột. Kỷ luật tọa độ do Go thực thi (sửa đổi 2026-07-16): ranh giới do mô hình trả về trong vùng ngữ cảnh sẽ không kích hoạt hỏi lại ngữ nghĩa — ranh giới đó thuộc quyền quản của khối kề bên (nó sẽ báo lại trong khoảng owned của nó), code cắt bỏ trực tiếp và trả mô tả; retry ngữ nghĩa chỉ dành cho lỗi ngữ nghĩa thật sự (ID ảo nằm ngoài projection, kind không hợp lệ, v.v.). Hành vi cũ hỏi lại khi nhận phản hồi vượt biên khiến mô hình yếu thường tiêu hết cả 3 lần thử và kéo sập cả khối.

Kích thước khối được tính theo context window và ngân sách giữ lại của architect model hiện tại, không chia khối theo số dòng hay số chương cố định. Khi ngữ cảnh mô hình lớn hơn, số lần gọi tự nhiên giảm. Ngân sách lập kế hoạch không dùng hết toàn bộ (sửa đổi 2026-07-16): chính văn owned chỉ là một phần của yêu cầu, khi lập kế hoạch phải trừ chiều dài thực của system prompt và hướng dẫn, rồi lại nhân 3/4 để bù phần phình của bao JSON projection; vùng ngữ cảnh còn có giới hạn byte riêng (chunkBytes/8, tối thiểu 4096), chặn mảnh ảo của line quá dài nuốt ngân sách input. Phía output có cơ chế dự phòng đối xứng: khi JSON ranh giới của một khối bị cắt theo độ dài (nhiều chương ngắn) thì sẽ đệ quy thử lại với khối nửa nhỏ hơn — khối nửa có đường cache riêng, kết quả thử lại không phải trả phí lại; nếu đến cấp unit vẫn bị cắt thì mới là thiếu dung lượng thật sự. Quyết định biên giới của từng khối được ghi xuống đĩa dưới dạng artifact (`segment-chunks/chunk-*.json`, danh tính = danh tính cắt + MaxUnitBytes + phạm vi owned của khối — bảng unit được xác định duy nhất bởi “nguồn đã chuẩn hóa + MaxUnitBytes”; khi đổi mức và tái tạo phân mảnh cho dòng siêu dài, cache tự nhiên không khớp, không thể tái sử dụng nhầm biên giới cũ bị lệch), bất kỳ khối nào thất bại hoặc bị gián đoạn thì khi chạy lại, các khối đã hoàn thành sẽ được tái sử dụng không gọi lại — cùng một triết lý với analyze theo từng chương và synthesize theo từng khoảng; sau khi segmentation cuối cùng được ghi xuống đĩa thì cache cấp khối bị xóa. Khi hợp nhất cuối (resolve) thất bại cũng xóa cache khối và ghi snapshot quyết định vào `failures/`: lúc này digest của cache luôn khớp, giữ lại nó sẽ khiến chạy lại không gọi nào nhưng đọc lại đúng cùng một nhóm biên giới, tái hiện quyết định cùng một thất bại một cách xác định. Biên giới chương có chính văn rỗng (thường gặp ở nguồn tiểu thuyết mạng thật với tiêu đề giữ chỗ kiểu "đã khóa/chương trả phí") không thất bại toàn cục: được gộp vào đoạn trước (không mất một chữ nào), ghi vào `Segmentation.Notes` để hiển thị trong bản xem trước xác nhận, nếu người dùng không chấp nhận có thể dùng `--guide` để phân xử.

### 8.2 Đầu ra của mô hình

```go
type BoundaryDecision struct {
	UnitID    string   `json:"unit_id"`
	Anchor    string   `json:"anchor,omitempty"`
	Kind      string   `json:"kind"` // chapter / group / front_matter / back_matter
	Title     string   `json:"title,omitempty"`
	Uncertain bool     `json:"uncertain,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}
```

- `chapter` là đơn vị chính văn có thể nộp, bao gồm cả quyết định ngữ nghĩa về việc có tính phần mở đầu, mồi câu, truyện ngoài hay không;
- `group` là bằng chứng cấu trúc cấp trên như quyển, bộ, thiên, không trực tiếp tính là chương;
- `front_matter` / `back_matter` đánh dấu các vùng phụ trợ rõ ràng không đi vào chính văn chương;
- `anchor` bắt buộc phải từng chữ lấy từ unit tương ứng; nếu biên giới nằm ngay ở đầu unit thì có thể lược bỏ;
- `uncertain` chỉ dùng để nhắc nhở trong bản xem trước, không phải do mã đặt ngưỡng tin cậy.

Không cho mô hình sinh regex. Regex vẫn sẽ ép ngữ nghĩa mở về cú pháp hữu hạn, đồng thời đưa vào giả định về escape, khớp cục bộ và định dạng thống nhất.

### 8.3 Kiểm tra bằng code

Go chỉ kiểm tra:

1. Tất cả unit ID đều tồn tại và nằm trong phép chiếu của lần gọi (owned + vùng ngữ cảnh; ngoài phép chiếu là ảo giác, kèm phản hồi để hỏi lại)；
2. Biên giới của vùng owned có `kind` thuộc tập đóng, `anchor` không rỗng phải duy nhất trong unit tương ứng và ánh xạ tới biên giới byte UTF-8, không xung đột ngữ nghĩa ở cùng vị trí (nếu `kind`/tiêu đề khác nhau thì không do Go quyết định giữ cái nào, sẽ hỏi lại mô hình; các bản trùng lặp hoàn toàn giống hệt chỉ là dư thừa cơ học, cho qua rồi khử trùng lặp im lặng), khối đầu tiên phải có biên giới che được điểm bắt đầu văn bản (đầu sách có phải phần mở đầu hay không do mô hình quyết định, Go không trả lời thay) — đều được kiểm tra ngay trong thời điểm gọi (sửa ngày 2026-07-16): giá trị lỗi bị đưa vào cache khối rồi đến tận cuối mới phát hiện thì digest luôn khớp, khiến lỗi được tái hiện một cách xác định; biên giới vùng ngữ cảnh vốn sẽ bị cắt bỏ, không cần hỏi lại vì chúng；
2a. **Nhắc lại tiêu đề** (sửa ngày 2026-07-16, seg-v2): `title` của biên giới chapter/group sau khi chuẩn hóa (xóa khoảng trắng) bắt buộc phải thật sự tồn tại trong văn bản gốc của đơn vị biên giới, nếu không sẽ hỏi lại ngay lúc gọi — thực nghiệm trên một nguồn bắt trang có 157 chương cho thấy 67 chương là biên giới và tiêu đề do mô hình bịa ra trên phần văn bản nối tiếp trong chương (kỷ luật phủ định phạm vi buộc mỗi khối phải đặt biên giới ở đầu khối), tất cả đều bị mục này chặn bằng kiểm tra sự thật. Quyền quyết định ngữ nghĩa vẫn thuộc về mô hình: những nguồn thật sự không có quy ước tiêu đề có thể đặt `uncertain=true` để giữ tiêu đề quy nạp (bản xem trước hiển thị dấu hiệu đáng ngờ); tiêu đề mô tả của front/back matter có rủi ro thấp nên không kiểm tra. prompt cũng được siết lại: biên giới chỉ đặt ở chỗ phân tách cấu trúc thật, khi đầu khối là phần nội dung nối tiếp của chương trước thì trả về boundaries rỗng là đầu ra đúng (ngoại trừ phần đầu của khối đầu tiên)；
3. Trật tự biên giới và bản sao được Go sửa xác định thay vì bác bỏ (sửa ngày 2026-07-16): khôi phục trật tự thật bằng cách sắp xếp ổn định theo byte sau khi phân tích — trật tự giữa các khối được bảo đảm bởi các khoảng owned không chồng lấn, lộn xộn chỉ có thể xảy ra trong nội bộ khối, sắp xếp không mất thông tin; các byte trùng nhau giữ mục xuất hiện trước và ghi vào `Notes`. Hành vi cũ yêu cầu tăng nghiêm ngặt nếu không là thất bại toàn cục, thực nghiệm 319 biên giới đã gục trước 1 chỗ đảo thứ tự trong khối, và cache khối sẽ khiến thất bại này tái hiện xác định. Việc xác định thứ tự luôn theo số của `(Line, Part)`, không so sánh ID theo thứ tự từ điển；
4. Mỗi phạm vi chính văn chương được tạo ra phải không rỗng (tiêu đề giữ chỗ cho chính văn rỗng sẽ nhập vào đoạn trước, xem §8.1)；
5. Tất cả văn bản nguồn không rỗng phải thuộc đúng một chương, một tiêu đề group hoặc một front/back matter rõ ràng (văn bản không được quy thuộc ở đầu sách — như giới thiệu/quảng cáo bị bỏ sót biên giới — sẽ được Go thu về `front_matter` một cách xác định và ghi `Notes` để chuyển sang bản xem trước xác nhận, không bác bỏ ở cuối)；
5a. Những chương trùng tên (sau khi xóa khoảng trắng ở tiêu đề thì giống nhau) được ghi vào `Notes` để người dùng kiểm tra thủ công (sửa ngày 2026-07-16) — với nguồn có quy ước tiêu đề, tên chương không nên lặp; lặp là tín hiệu xác định của “cùng chương bị cắt nhầm”; việc có gộp hay không không do Go quyết định, hễ `Notes` không rỗng là chặn `--yes`；
6. Không có chồng lấn, vượt biên hoặc phạm vi chưa được quy thuộc；
7. group không bị tính nhầm vào tổng số chương.

“L1257 về mặt ngữ nghĩa có phải là tiêu đề chương hay không” không do Go phán xét lại.

### 8.4 Xác nhận của người dùng

Trong chế độ tương tác, trước khi xác nhận sẽ không gọi phân tích chương, cũng không ghi Store chính thức. Bản xem trước tối thiểu hiển thị:

- số lượng quyển/group và số chương；
- toàn bộ tiêu đề chương, có thể cuộn để xem；
- phạm vi và tóm tắt của văn bản phụ trợ đầu/cuối；
- biên giới của chương rỗng, chương quá dài bất thường và các biên giới mà mô hình đánh dấu `uncertain`；
- dòng bắt đầu và kết thúc của từng chương để người dùng đối chiếu với bản gốc.

Người dùng có thể:

- xác nhận (trong bảng xem trước TUI nhấn `y`, nội bộ sẽ chạy lại bằng AcceptSegmentation; cho qua lần cắt hiện tại một lần, không ghi intent, confirmation ghi `method=user_confirmed`)；
- nhập mô tả ngôn ngữ tự nhiên rồi nhận diện lại, ví dụ `/import --guide=Ngoại truyện X cũng là chương độc lập`；
- hủy và giữ nguyên workspace (Esc).

`/import <path> --yes` là quyền ủy quyền không giám sát rõ ràng: Runner sau khi vượt qua kiểm tra ghi đè sẽ ghi cùng artifact confirmation đó, ghi nhận `method=auto_authorized`, rồi tiếp tục phân tích. `--yes` ngay cả khi có biên giới `uncertain` vẫn có nghĩa là người dùng chọn tin vào lần cắt này, nhưng `uncertain` vẫn được giữ trong artifact và log. **Ngoại lệ (sửa ngày 2026-07-16)**: khi segmentation có ghi chú chịu lỗi (`Notes` không rỗng — đã xảy ra hấp thụ chương rỗng, phương án dự phòng ở đầu, khử trùng lặp chồng lặp), `--yes` không tự động cho qua mà vẫn dừng ở bản xem trước xác nhận — cấu trúc đã bị viết lại theo cách xác định, ủy quyền mù mà chưa xem preview không nên nuốt mất nó; `y` (AcceptSegmentation) sau khi đã xem preview không bị giới hạn này.

`--yes` chỉ bỏ qua xác nhận segmentation, không tự quyết `story_status=uncertain`, cũng không bỏ qua Hold khi import hoàn tất. Người dùng không cần viết regex hay nhập tay `from=N`.

## 9. Trích xuất sự thật theo từng chương của các batch liên tiếp

Sau khi xác nhận, từ phân tích thiếu đầu tiên, gom các chương liên tiếp thành batch theo **ngân sách nhập và xuất kép** của mô hình hiện tại. Phiên bản đầu tiên chạy tuần tự giữa các batch, không làm song song giữa các cửa sổ: ID của foreshadow, biệt danh nhân vật và biến đổi trạng thái có tính thứ tự thời gian, ledger gọn do batch trước tạo ra là đầu vào của batch sau.

Tuần tự chỉ ràng buộc chiến lược thực thi của phiên bản đầu tiên, không phải giới hạn kiến trúc vĩnh viễn; artifact phân tích vẫn được ghi xuống đĩa độc lập theo từng chương, về sau nếu có chứng cứ cho thấy hợp nhất song song vẫn giữ được chất lượng ngữ nghĩa thì chỉ cần thay bộ lập lịch batch.

### 9.1 Đầu ra batch, artifact theo từng chương

Bãi bỏ envelope hỗn hợp `=== TAG ===`. Mỗi lần gọi trả về một đối tượng batch có cấu trúc, mỗi phần tử mảng vẫn là sự thật của một chương:

```go
type ImportedChapterFacts struct {
	Chapter             int                        `json:"chapter"`
	Title               string                     `json:"title"`
	Summary             string                     `json:"summary"`
	KeyEvents           []string                   `json:"key_events"`
	CoreEvent           string                     `json:"core_event"`
	Hook                string                     `json:"hook"`
	Scenes              []string                   `json:"scenes"`
	Characters          []string                   `json:"characters"`
	CharacterEvidence   []ImportedCharacterFact    `json:"character_evidence,omitempty"`
	WorldEvidence       []ImportedWorldFact        `json:"world_evidence,omitempty"`
	TimelineEvents      []domain.TimelineEvent      `json:"timeline_events,omitempty"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates,omitempty"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes,omitempty"`
	StateChanges        []domain.StateChange       `json:"state_changes,omitempty"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
}

type AnalysisBatchResult struct {
	Chapters []ImportedChapterFacts `json:"chapters"`
}

type ChapterAnalysisPayload struct {
	BatchStart int                  `json:"batch_start"`
	BatchEnd   int                  `json:"batch_end"`
	Facts      ImportedChapterFacts `json:"facts"`
}
```

Mỗi `analyses/NNNNNN.json` đều là `Artifact[ChapterAnalysisPayload]`. Các bản ghi chương được ghi ra trong cùng một batch có cùng `BatchStart/BatchEnd`; `InputDigest` của chúng dùng **ràng buộc theo từng chương**: danh tính cắt (InputDigest của artifact segmentation) + phiên bản prompt/schema + số chương + chính văn của riêng chương đó. Sở dĩ dùng ràng buộc theo từng chương thay vì theo batch là vì ranh giới batch thay đổi theo năng lực đầu vào/đầu ra của mô hình (đổi sang mô hình mạnh hơn thì batch tự nhiên lớn hơn); nếu ghi cả cách chia batch vào danh tính thì sau khi đổi mô hình, các phân tích đã thành công sẽ bị lệch khớp toàn bộ, buộc phải tính lại và trả phí lặp lại. Ràng buộc theo danh tính cắt đảm bảo rằng khi “cắt lại”, “đổi prompt/schema version”, “đổi nguồn” thì phân tích phía sau tự nhiên không khớp, còn chỉ đổi mô hình thì không làm hỏng — đó mới là ngữ nghĩa hỏng đúng mà phục hồi thực sự cần.

`ImportedCharacterFact` và `ImportedWorldFact` là quan sát cô đọng dùng cho tổng hợp toàn sách, không trực tiếp ghi thành nhân vật hay quy tắc thế giới chính thức. Chúng ít nhất phải mang số chương để kết quả tổng hợp có nguồn gốc ổn định.

### 9.2 Gom batch theo ngân sách kép

Lập kế hoạch batch đồng thời thỏa:

```text
ước tính input + system/prompt/ledger + chừa chỗ suy luận + ước tính output nhìn thấy ≤ context window
ước tính output nhìn thấy ≤ giới hạn completion khả dụng của provider/model
```

- Ước tính input bao gồm tiêu đề, chính văn mỗi chương và ledger trước batch；
- Ước tính output được tạo từ chi phí cấu trúc cố định của analyzer schema và phần dự trữ sự thật bảo thủ cho từng chương, chỉ quyết định lần này nhét được bao nhiêu chương, không cắt bất kỳ trường nào；
- với mô hình mà reasoning token và JSON nhìn thấy cùng dùng chung ngân sách completion, phải trừ phần dự trữ suy luận trước；
- provider/model càng mạnh thì batch tự nhiên càng lớn; không thể viết quy tắc cố định “mỗi batch 10/20 chương”；
- nếu bản thân input của một chương không thể vào context, hoặc cấu trúc output tối thiểu của một chương cũng không thể vào completion, thì phải báo rõ chương đó và dung lượng mô hình, không cắt chính văn hay bịa ra thành công tinh giản.

Vì vậy khi tổng số chương tăng lên chỉ tăng số batch, không còn để một phản hồi bất kỳ phình vô hạn theo quy mô cả cuốn; đồng thời cũng không kéo #83 từ cấp độ toàn sách sang một cấp độ batch không bị ràng buộc bởi output.

### 9.3 Context của batch

Một lần gọi batch đơn lẻ chỉ bao gồm:

- văn bản gốc và tiêu đề của phạm vi chương liên tiếp hiện tại；
- bảng biệt danh nhân vật cô đọng suy ra từ các chương trước；
- ID foreshadow đang hoạt động và trạng thái một câu；
- phần tóm tắt ngắn về trạng thái gần nhất khi cần thiết.

Mô hình xử lý các chương trong batch theo thứ tự mảng, có thể tiếp nối biệt danh, foreshadow và trạng thái bên trong batch; sau khi batch kết thúc, Go cập nhật ledger cô đọng theo thứ tự các sự thật đã được xác minh. Nó không phụ thuộc vào Premise toàn sách chưa sinh ra, cũng không đọc lại toàn bộ phần trước. Sự thật chương là đầu vào của Foundation, chứ không phải ngược lại để tạo thành phụ thuộc vòng.

### 9.4 Kiểm tra phản hồi đầy đủ

Mã kiểm tra hai lớp: cấu trúc, miền giá trị và tham chiếu, không mã hóa cứng chất lượng văn học:

- cấp batch: mảng chapters liên tục theo số chương dự kiến, không trùng, không thiếu, phạm vi batch, `InputDigest` và version schema khớp；
- cấp từng chương: `chapter`/`title` khớp với phân đoạn nguồn, `summary`/`core_event` không rỗng, `hook type`, `strand` và các trường domain đóng chính thức hợp lệ, các trường timeline, foreshadow và biến đổi trạng thái có kiểu hợp lệ.

Mã không yêu cầu “phải có 3～6 sự kiện”, “phải có nhân vật xuất hiện”, “phải có ba cảnh”. Chương yên ắng, thư tín, chương tả cảnh hay chương không có nhân vật tên riêng đều là hình thái văn học hợp lệ.

Khi phản hồi đầy đủ gặp lỗi JSON hoặc lỗi kiểm tra ngữ nghĩa, không gửi bất kỳ chương mới nào trong đó; phản hồi lỗi cụ thể cho cùng mô hình, đi theo thử lại tầng đầu ra ở §13.3. Mô hình có thể sửa rồi viết lại cả các đối tượng phía trước, vì vậy khi kiểm tra thất bại không được tự ý lưu một phần mảng.

### 9.5 Prefix liên tục khi bị cắt độ dài

> Định vị triển khai: phần này là **tối ưu token trên đường lỗi**, không phải phụ thuộc cho tính đúng đắn khi khôi phục. v1 (giai đoạn ba) khi bị cắt nghĩa là “thất bại + thu nhỏ batch tái tổ chức”, bản thân nó đã đúng và có thể khôi phục; cứu prefix liên tục được triển khai ở phân giai đoạn riêng (giai đoạn ba · bổ sung), có thể bật/tắt và nghiệm thu riêng.

Chỉ khi phản hồi được gắn rõ `StopReasonLength` và trả về được một phần văn bản có thể phân tích, mới cho phép lưu **tiền tố hợp lệ liên tục lớn nhất** từ phản hồi lỗi:

1. Dùng streaming JSON decoder để vào mảng `chapters` ở cấp trên；
2. Từ chương đầu của batch đọc từng đối tượng JSON đã đóng hoàn chỉnh；
3. Mỗi đối tượng kiểm tra độc lập theo §9.4, và sau khi ghép với các đối tượng trước đó thành một chuỗi liên tục bắt đầu từ chương đầu batch thì ghi nguyên tử ngay artifact phân tích của chương tương ứng；
4. Gặp đối tượng đầu tiên không hoàn chỉnh, không hợp lệ, nhảy số hoặc trùng lặp thì dừng ngay, toàn bộ byte phía sau không giải thích nữa；
5. Cấm vá ngoặc, viết tiếp nửa JSON, đoán trường bị thiếu hoặc vớt đối tượng không liên tục từ vị trí sau đó；
6. Phản hồi thô, StopReason, phạm vi prefix đã lưu và chương đầu tiên thất bại đều phải ghi vào failure artifact, sự kiện và log；
7. `NextAction` sẽ gom batch lại từ phân tích thiếu đầu tiên, không làm lại prefix hợp lệ đã được nộp.

typed-call phải ghi nhận lần này có lấy được một phần văn bản hữu dụng hay không: chế độ cấu trúc không streaming như JSON Schema có thể không cho prefix có thể phân tích khi dừng vì độ dài. Nếu provider không trả về phần văn bản, không thể chứng minh rõ là bị cắt độ dài, hoặc thậm chí chưa hoàn thành được một đối tượng hợp lệ nào thì không lưu bất kỳ kết quả nào, phát sự kiện/log `prefix_salvage=unavailable` và rơi về “thất bại + thu nhỏ batch tái tổ chức”, thay vì im lặng chạy rỗng. Batch chỉ có một chương mà vẫn bị cắt thì báo thẳng dung lượng output của mô hình không đủ, không lặp giảm hoặc tạo fact rỗng.

Cắt độ dài là lỗi dung lượng, không đi vào vòng tự sửa ngữ nghĩa “đưa lỗi kiểm tra lại cho cùng mô hình”, cũng không thử lại nguyên xi batch đó.

### 9.6 Khôi phục

Mỗi chương phân tích thành công sẽ được ghi nguyên tử vào `analyses/NNNNNN.json`. Sau khi sập:

- phân tích có `InputDigest` khớp thì tái sử dụng trực tiếp, không thu phí lại；
- phân tích thiếu đầu tiên hoặc không khớp sẽ thành điểm bắt đầu batch tiếp theo；
- sau khi đầu vào ngữ nghĩa upstream thay đổi, các phân tích không thể dựng lại cùng `InputDigest` sẽ tự nhiên mất hiệu lực；
- prefix hợp lệ liên tục đã được nộp do cắt độ dài và artifact hoàn thành bình thường đều dùng chung hoàn toàn cùng quy tắc khôi phục；
- không cho phép người dùng vượt qua một chương thất bại để tiếp tục sinh ra các sự thật ngữ nghĩa phía sau không liên tục.

## 10. Tổng hợp theo tầng

### 10.1 Vì sao không thể xuất cả sách trong một lần nữa

Tổng hợp toàn sách cần hiểu xuyên chương, nhưng không cần đọc lại toàn bộ chính văn, cũng không nên xuất lại đối tượng chi tiết của từng chương. Sự thật theo từng chương đã chứa ngữ nghĩa ở cấp chương; tổng hợp chỉ xử lý những sự thật cô đọng này.

### 10.2 Hình dạng Map/Reduce

```text
ImportedChapterFacts × N
        ↓ chia thành các khoảng liên tục theo context window hiện tại
RangeDigest × M
        ↓ nếu cần thì tiếp tục hợp nhất
BookSynthesis
```

Nếu sách ngắn có thể chứa toàn bộ sự thật chương trong một lần, thì sinh trực tiếp `BookSynthesis`; chỉ sách dài mới sinh `RangeDigest`. Việc có chia tầng hay không do ngân sách token quyết định một cách cơ học, không do ngưỡng số chương.

`RangeDigest` chứa tiến triển cốt truyện, biến đổi nhân vật, sự thật thế giới, foreshadow đã mở/đã thu và các biên cấu trúc ứng viên của khoảng liên tiếp đó. Kích thước đầu ra của nó bị ràng buộc bởi từng khoảng; tổng hợp cuối không còn lặp lại N đối tượng chi tiết của chương, mà chỉ xuất sự thật toàn cục và phạm vi cung truyện.

### 10.3 Kết quả tổng hợp cuối cùng

```go
type BookSynthesis struct {
	Title         *string                `json:"title"`    // khi không thể xác nhận từ chính văn thì là null, suy ra từ tên file
	Synopsis      string                 `json:"synopsis"` // giới thiệu không spoiler dành cho độc giả
	Premise       string                 `json:"premise"`
	Characters    []domain.Character     `json:"characters"`
	WorldRules    []domain.WorldRule     `json:"world_rules"`
	Structure     []ImportedVolumeRange  `json:"structure"`
	Compass       domain.StoryCompass    `json:"compass"`
	PlanningTier  domain.PlanningTier    `json:"planning_tier"`
	StoryStatus   string                 `json:"story_status"` // open / closed / uncertain
	StatusReason  string                 `json:"status_reason"`
}
```

Cấu trúc chỉ trả về các phạm vi, không lặp lại toàn bộ chương:

```go
type ImportedVolumeRange struct {
	Title string             `json:"title"`
	Theme string             `json:"theme"`
	Arcs  []ImportedArcRange `json:"arcs"`
}

type ImportedArcRange struct {
	Title        string `json:"title"`
	Goal         string `json:"goal"`
	StartChapter int    `json:"start_chapter"`
	EndChapter   int    `json:"end_chapter"`
}
```

Mô hình tự quyết định số quyển và số arc, có thể tham khảo tiêu đề group trong tệp nguồn, nhưng không bị giới hạn bởi “một quyển” hay “1～3 arc”. Go dùng title/core_event/hook/scenes của `ImportedChapterFacts` để ghép thành `OutlineEntry` chính thức.

### 10.4 Tình trạng truyện

Nhập chỉ tái tạo sự thật của chính văn, không bịa ra các tuyến dài chưa khép lại chỉ để cho Engine tiếp tục:

- `open`: trong phần thân có mục tiêu chưa khép hoặc độ căng thật sự, sinh Compass bình thường;
- `closed`: phát hành như tác phẩm đã hoàn tất, tập cuối được đánh dấu Final; nếu cần viết tiếp thì người dùng phải reopen rõ ràng và đưa ra hướng mới;
- `uncertain`: trước khi phát hành yêu cầu người dùng chọn xử lý theo chưa hoàn hay đã hoàn; nếu Intent đã lưu lựa chọn qua `--story=open|closed` thì dùng ngay, nếu không thì vào trạng thái chờ tương tác. Lựa chọn được lưu thành artifact `story-resolution.json` với synthesis hiện tại làm input.

Không được lén đoán ý định người dùng bằng cách xem `open_threads` có rỗng hay không.

## 11. Lắp ráp và xác minh Foundation

Mô hình xuất ngữ nghĩa tổng hợp, Go chịu trách nhiệm lắp ráp đối tượng miền chính thức. Trước khi phát hành phải thỏa:

1. Premise có tiêu đề sách hợp lệ; khi không xác nhận được tên sách trong phần thân thì dùng basename của tệp nguồn, và đánh dấu nguồn là filename, không để mô hình tuyên bố đó là “tên sách thật”;
2. Tất cả phạm vi tập và arc phải liên tục theo thứ tự;
3. Phạm vi đầu tiên bắt đầu từ chương 1, phạm vi cuối cùng kết thúc ở chương N;
4. Mỗi chương chỉ thuộc đúng một arc;
5. Sau `FlattenOutline`, số chương là N, tiêu đề và факт từng chương khớp nhau;
6. Tên nhân vật, quy tắc thế giới và Compass phải thỏa các ràng buộc kiểu domain hiện có;
7. PlanningTier là một giá trị đóng hợp lệ, nhưng lý do lựa chọn phải đến từ mô hình chứ không phải ngưỡng số chương;
8. Trạng thái closed/open phải khớp với Final và hình dạng phát hành của Compass;
9. Artifact Synthesis có `InputDigest` có thể được tái dựng từ tập phân tích có thứ tự hiện tại.

Khi vi phạm ràng buộc cấu trúc, trả lỗi cụ thể về mô hình để sinh lại, lặp cho đến khi thành công hoặc context bị hủy; không ghi xuống đĩa các bản dang dở.

## 12. Phát hành chính thức

### 12.1 Điều kiện trước khi phát hành

Import mới chỉ được phép đi vào khi:

- không có chương đã hoàn tất;
- không có chương đang xử lý hoặc PendingCommit;
- không có một workspace import không cùng nguồn nào khác;
- Foundation chính thức rỗng, hoặc digest trùng khớp hoàn toàn với digest đã phát hành của workspace hiện tại.

Ý nghĩa gộp giữa một tiểu thuyết đã có và văn bản ngoài mới không rõ ràng; phiên bản đầu tiên từ chối rõ ràng, không đoán là ghi đè hay nối thêm.

### 12.2 Phát hành Foundation

Phát hành theo thứ tự phụ thuộc chính thức:

```text
planning tier
→ premise
→ characters
→ world rules
→ layered outline + flat outline
→ compass
→ progress đối soát
```

Mỗi bước:

1. Tính digest của nội dung chuẩn bị phát hành;
2. Nếu artifact chính thức chưa tồn tại thì ghi nguyên tử và thêm checkpoint;
3. Nếu đã tồn tại và digest giống nhau thì bỏ qua theo kiểu idempotent;
4. Nếu đã tồn tại nhưng khác thì trả lỗi xung đột, không ghi đè.

Sau khi sập giữa chừng, chỉ cần đối soát lại từ mục đầu tiên; không cần giao dịch xuyên tệp hay máy trạng thái Foundation Pending.

### 12.3 Phát hành chương

Tái sử dụng luồng hiện có theo thứ tự chương:

```text
Lưu draft
→ Progress.StartChapter
→ commit_chapter(sự thật theo từng chương)
```

`commit_chapter` đã có saga PendingCommit, checkpoint và kiểm tra idempotent cho chương đã hoàn tất. Import không sao chép một bộ logic commit thứ hai.

Cửa sổ sập:

| Cửa sổ | Hành vi khôi phục |
|---|---|
| trước draft | lưu lại đúng cùng một phần thân |
| sau draft, trước StartChapter | đối soát digest rồi tiếp tục |
| sau StartChapter, trước PendingCommit | thực thi lại commit của cùng chương |
| đang ở PendingCommit | khôi phục bằng saga commit hiện có |
| sau khi chapter complete | nếu digest/checkpoint khớp thì bỏ qua |
| nội dung chính thức xung đột với digest nguồn | dừng rõ ràng, báo chương xung đột |

### 12.4 Ranh giới hoàn tất import

Sau khi tất cả chương được commit ổn định, đặt một lần `AdvanceHoldAtBoundary`, với lý do rõ là “Nhập tiểu thuyết bên ngoài đã hoàn tất, chờ nghiệm thu rồi viết tiếp”. Nó chỉ bảo vệ lần import xuyên hệ thống này, không thay đổi chế độ `auto/review` dài hạn của người dùng.

`--yes` chỉ cấp quyền chấp nhận tự động việc chia tách, không được ngầm bỏ qua Hold lần này. Chỉ khi người dùng đồng thời truyền thêm `--continue` riêng biệt thì Runner mới không tạo Hold chuyên cho import; sau đó vẫn tuân thủ advance mode bình thường: `auto` có thể tiếp tục viết tiếp, `review` vẫn chờ `/next`.

Mặc định TUI không còn tự động tiếp tay trong im lặng. Sau khi người dùng kiểm tra trạng thái Foundation và chương, hãy dùng điểm tiếp tục hiện có để khôi phục sáng tác.

**Điểm đóng bảng điều khiển**: sau khi import khởi phát từ trang chào kết thúc thành công, việc nhấn Esc để đóng panel sẽ chạy thêm một lần `Resume()` (cổng phục hồi của bootstrap chỉ chạy một lần lúc khởi động), người dùng sẽ rơi thẳng vào workspace, bị Hold hoàn tất import chặn ở ranh giới chương tiếp theo để chờ nghiệm thu — chứ không ở lại trang chào nơi bấm nhầm Enter sẽ “tạo sách mới”. Với trạng thái lỗi cuối cùng và cảnh workspace, chỉ đóng panel.

**Hàng rào tạo mới**: `PrepareUserRules` / `StartPrepared` sẽ từ chối tạo mới khi thư mục sách đã có chương hoàn tất (`CompletedChapters` không rỗng) — vì ngay đầu `StartPrepared` sẽ reset checkpoints và progress, một lần chạm nhầm là xóa âm thầm cả cuốn sách (bao gồm toàn bộ chương vừa import). Các tàn dư quy hoạch không có chương hoàn tất vẫn được cho qua, giữ lại đường tự phục hồi qua Ctrl+S cùng phiên và khôi phục cắt bù của co-create.

### 12.5 Cổng phát hành khi qua nhiều lần khởi động lại

Khi workspace tồn tại và `NextAction != done`, `Host.New/Resume` phải nhận diện sách là import chưa hoàn tất:

- cho phép xem, chẩn đoán và thực hiện khôi phục `/import`;
- cấm khởi động Engine bình thường, Continue hoặc phát Writer;
- hiển thị rõ hành động khôi phục hiện tại, không xem Foundation/chương đã phát hành một phần như một cuốn sách có ngữ nghĩa hoàn chỉnh có thể viết tiếp.

Cổng này đọc trực tiếp từ workspace và Store chính thức để suy ra, không thêm `published bool`. Như vậy sau khi bất kỳ cửa sổ phát hành nào bị sập, trạng thái bán phát hành cũng sẽ không bị luồng sáng tác bình thường tiêu thụ khi Runner chưa khôi phục.

## 13. Lõi gọi mô hình

Bên trong `imp` giữ một helper typed-call nhỏ và chuyên dụng, không xây một framework workflow LLM tổng quát.

### 13.1 Chọn mô hình

- Mặc định dùng mô hình vai trò architect;
- Mức mô hình của các hàm ngữ nghĩa là một núm mở: segment/analyze/synthesize có thể tự khai báo mức, mặc định rơi về architect, lớp cấu hình có thể chỉ định segment cơ học hơn sang mức rẻ hơn. Đây là cấu hình gọi, không đổi bất kỳ hợp đồng ngữ nghĩa nào, cũng không đóng đinh “một vai trò duy nhất” thành tiền đề kiến trúc — mục đích là để cả phần lợi ích chi phí khi “mô hình mức rẻ trở nên mạnh hơn” cũng đi vào được;
  - Điểm triển khai: cấu hình roles hỗ trợ ba key vai trò `import_segment` / `import_analyze` / `import_synthesize`; nếu không cấu hình thì rơi về architect. Hai ngân sách và tùy chọn thinking/structured của từng hàm được suy ra độc lập theo năng lực thật của mức tương ứng (cửa sổ nhỏ của mức nhỏ chỉ ràng buộc chính hàm của nó), và lượng dùng được hạch toán theo vai trò mức thực tế;
- Kích hoạt failover đã cấu hình của vai trò được chọn;
- Dùng reasoning effort của vai trò được chọn, và quyết định có gửi tham số thinking hay không bằng cách dò năng lực;
- Ghi metadata session và usage theo provider/model thật;
- Bao gồm trong sentinel ngân sách hiện có (triển khai 2026-07-16): trước khi chạy qua `Refuse()` và tuân theo cùng kỷ luật với Start/Resume/Continue; trong lúc chạy, chặn cứng ngân sách bằng cách dùng `abortWithEvent` để hủy context riêng của import (Host đăng ký cancel cho job độc quyền, không còn chỉ tạm dừng Engine chưa chạy).

### 13.2 Khả năng xuất cấu trúc

Bốn loại artifact import cùng dùng `llmcontract.Execute`: khi mô hình hoặc cấu hình người dùng cho biết rõ là hỗ trợ thì gửi JSON Schema gốc; khi năng lực không rõ hoặc được chỉ rõ là không hỗ trợ thì tự sinh Prompt Contract từ cùng một Schema. Chế độ gốc kiểm tra toàn bộ phản hồi, còn chế độ tương thích chỉ tách ra đối tượng JSON cân bằng; cả hai đường đều chạy cùng một bộ kiểm tra Schema trước, rồi mới giải mã DTO và chạy kiểm tra nghiệp vụ. Sau lỗi yêu cầu không được lặng lẽ xóa schema để thử lại; phải bộc lộ lỗi nếu nhận diện năng lực sai hoặc Provider từ chối.

### 13.3 Tách bạch lỗi request, ngữ nghĩa và năng lực

- Lỗi tầng request: chỉ retry với timeout, rate limit, lỗi mạng mà adapter gắn cờ retryable rõ ràng, tiếp tục dùng ngữ nghĩa backoff hiện có cho đến khi thành công hoặc context bị hủy;
- Lỗi tầng đầu ra: đưa lỗi cụ thể của JSON parse hoặc Validate trở lại cùng mô hình đó, tiếp tục tự sửa cho đến khi thành công hoặc context bị người dùng/hệ thống ngân sách hủy; vi phạm hợp đồng Schema gốc, từ chối trả lời và bị cắt cụt sẽ không được hỏi lại mù quáng.
- Retry không được im lặng: mỗi lần backoff ở tầng request ("đang retry lần thứ N · retry sau Xs") và mỗi lần hỏi lại ở tầng đầu ra đều phải được hiển thị thành sự kiện tiến độ vào bảng điều khiển import — nếu không có phản hồi người dùng sẽ tưởng bị treo. Sự kiện backoff chỉ mang thời điểm chốt (`RetryAt`), số giây còn lại do lớp render tính theo từng tick để tạo bộ đếm ngược thời gian thực (dùng chung cơ chế với bảng sự kiện ở workspace sáng tác); trong thời gian chạy của panel có spinner thường trực ở trên cùng + thời gian đã dùng, còn cuối log có con trỏ sao kiểu streaming tương tự.
- Không được ghi lỗi một cách chung chung: message của gateway thường chỉ có một câu "Provider returned error"; phần phản hồi và văn bản lỗi phải luôn kèm sự thật có cấu trúc từ adapter (phân loại lỗi/HTTP status/provider/model, `modelErrDetail` được trích từ chuỗi lỗi litellm qua errors.As), đưa sự thật lên trước, ưu tiên giữ lại khi bị cắt.
- Không được im lặng ở các giai đoạn dài: tách theo khối, tổng hợp theo từng khoảng đều gọi mô hình bên trong hàm (một khối có thể mất vài phút), và qua `callProfile.step` để phản hồi tiến độ theo từng khối/từng khoảng ("đang tách khối thứ N/M, đã nhận diện K ranh giới"). Key sự kiện chỉ dành cho backoff của request (tạm thời trong cùng một lần gọi, nhấp nháy tại chỗ); hỏi lại do kiểm tra là sự kiện ngữ nghĩa xuyên lần gọi, mỗi cái đứng một dòng để giữ lịch sử — dùng chung Key sẽ khiến khối sau ghi đè khối trước, mất sạch manh mối điều tra.
- Ghi log toàn phần: mọi sự kiện tiến độ (kể cả các dòng retry bị panel ghi đè ngay tại chỗ) đều được ghi vào **log chuyên cho import** `<gốc-sách>/logs/import.log` (không trộn với tui.log, một lần import xem toàn bộ transcript trong một file); backoff của request và hỏi lại ngữ nghĩa cũng ghi cùng một log dưới dạng toàn bộ chuỗi lỗi.
- Phản hồi phải biểu đạt ngữ nghĩa của mô hình chứ không chỉ là đếm máy móc: khi tách, phản hồi từng khối phải cho thấy tiêu đề mô hình nhận ra ("mô hình nhận diện được: Chương 12 Đêm tuyết gió / … (tổng cộng N chỗ)"), khi phân tích thì phản hồi các sự kiện cốt lõi theo từng chương ("Chương 12〈Đêm tuyết gió〉: …"), khi tổng hợp xong thì phản hồi tóm tắt toàn cuốn (premise summary) — người dùng phải thấy mô hình đã hiểu gì.
- Lỗi năng lực: `StopReasonLength` không được retry nguyên dạng, cũng không đi vào vòng tự sửa ngữ nghĩa; analysis batch, khi một phần văn bản có thể phân tích được, sẽ lưu tiền tố hợp lệ liên tục dài nhất theo §9.5, nếu không thì ghi `prefix_salvage=unavailable` và thu nhỏ batch tái tổ hợp; các hàm ngữ nghĩa còn lại sẽ thất bại rõ ràng và giữ nguyên phản hồi gốc.

Xác thực, quyền hạn, không hỗ trợ mô hình và xung đột trạng thái phải thất bại ngay. Không có success giả, không có fallback đối tượng rỗng, và không bỏ qua các chương thất bại.

### 13.4 Ngân sách đầu vào và đầu ra

Mỗi hàm ngữ nghĩa có schema, ngân sách đầu vào, phần dự trữ suy luận và ngân sách đầu ra nhìn thấy riêng:

- đầu ra của segment chỉ chứa ranh giới của owned range hiện tại;
- analysis batch đồng thời bị ràng buộc bởi context window và giới hạn completion, đầu ra là các факт từng chương trong một phạm vi liên tục hữu hạn;
- RangeDigest chỉ chứa một đoạn liên tục;
- BookSynthesis chỉ chứa факт toàn cục và phạm vi tập/arc, không lặp lại đối tượng chương.

Mỗi request trước khi gửi đều ghi lại đầu vào ước tính, phần dự trữ suy luận, max tokens được xin và đầu ra nhìn thấy dự kiến. Ước tính chỉ quyết định cách chia khối/gộp nhóm, không xóa các trường thân hoặc факт. Vì vậy không tồn tại cấu trúc kiểu “tổng số chương càng nhiều thì một response nào đó nhất định càng dài”, và cũng không thể chỉ vì đầu vào nhét vừa mà bỏ qua rủi ro cắt cụt đầu ra.

## 14. Sự kiện, log và chẩn đoán

### 14.1 Giai đoạn sự kiện

```go
const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)
```

Mỗi sự kiện gồm action, chương/khoảng hiện tại, tổng số, thời lượng và lỗi tùy chọn. Sự kiện analysis batch còn bao gồm phạm vi batch, ước tính ngân sách, StopReason và phạm vi prefix đã commit. Event là projection, không tham gia khôi phục.

### 14.2 Lỗi phải đi tới ba nơi cùng lúc

1. Bảng điều khiển import trong TUI: tự xuống dòng, giữ nguyên toàn bộ chuỗi lỗi;
2. `tui.log`: ghi cấu trúc stage, chapter/range, model, attempt và error;
3. `meta/import/failures/`: lưu metadata của lần thất bại cuối cùng và phản hồi mô hình chưa bị cắt gọt.

Thân truyện gốc không được ghi vào log thông thường, cũng không đi vào xuất chẩn đoán ẩn danh mặc định. Phản hồi lỗi nằm trong thư mục sách của chính người dùng, và thông tin lỗi phải chỉ rõ đường dẫn.

### 14.3 Session và Usage

Mỗi lần gọi ngữ nghĩa ghi:

- tên task ổn định, như `import/segment/0003`, `import/analyze/0054-0061`;
- phản hồi gốc của assistant;
- provider/model và usage;
- structured mode, mức thinking và kết quả kiểm tra đầu ra.

Usage được quy về vai trò architect, để ngân sách nhìn thấy được chi phí import.

## 15. Vòng đời và đồng thời

- Import và Engine, co-create theo giai đoạn, và các thao tác ghi của simulation loại trừ lẫn nhau;
- Trong lúc import, mỗi cuốn sách chỉ được phép có một Runner;
- Người dùng hủy sẽ hủy các lời gọi mô hình đang chạy, còn các факты workspace đã ghi nguyên tử vẫn được giữ;
- Hủy trước khi xác nhận sẽ không sửa Store chính thức;
- Sau khi bắt đầu phát hành, việc hủy không thực hiện rollback suy đoán; lần sau chỉ có thể khôi phục chính xác việc phát hành;
- Ở phiên bản đầu, các analysis batch chạy tuần tự giữa các batch, trong batch thì một lần gọi mô hình trả về факты theo thứ tự chương; phát hành chính thức vẫn tuần tự theo chương;
- `Host.New/Resume` khi import chưa hoàn tất sẽ thực thi cổng kiểm soát ở §12.5; ý nghĩa loại trừ lẫn nhau vẫn đúng qua khởi động lại tiến trình;
- Việc export có cho phép đồng thời hay không vẫn giữ ngữ nghĩa chỉ-đọc hiện có, nhưng nó chỉ thấy những chương đã được phát hành chính thức.

## 16. Bất biến cốt lõi

1. Mỗi artifact workspace đều được xác định bởi `SchemaVersion + InputDigest + Payload`; chỉ có thể tái sử dụng nếu có thể dựng lại cùng `InputDigest` từ input ngữ nghĩa thật hiện tại.
2. Manifest tương ứng với đúng một snapshot nguồn chuẩn hóa; mỗi đoạn văn bản nguồn không rỗng phải có đúng một nơi thuộc về.
3. Mô hình chỉ được tham chiếu SourceUnit, anchor văn bản gốc và số chương do Host cung cấp; Go chỉ chấp nhận tọa độ có thể ánh xạ duy nhất về byte nguồn.
4. analysis batch chỉ được commit phản hồi đầy đủ, hoặc với `StopReasonLength` thì commit tiền tố hợp lệ liên tục lớn nhất bắt đầu từ chương đầu; thiếu bất kỳ chương nào sẽ chặn phân tích và tổng hợp tiếp theo.
5. Phạm vi tập/arc phải liên tục, không chồng lấn và phủ đầy `1..N`; Foundation chính thức chỉ có thể được phát hành từ Synthesis đã qua xác minh đầy đủ.
6. Chương chính thức chỉ có thể phát hành theo thứ tự qua `commit_chapter`; artifact chính thức đã tồn tại chỉ có thể tái sử dụng idempotent khi digest nội dung giống nhau, khác thì lỗi xung đột.
7. Mọi lỗi mô hình đều không được diễn giải là “không có nội dung” hay “tiếp chương sau”, không được sửa một nửa JSON hay bỏ qua chương thất bại.
8. `done` phải được chứng minh đồng thời bởi workspace artifact, artifact chính thức, Progress, PendingCommit và checkpoint; trước `done` thì Engine bình thường không được khởi động.

## 17. Cấu trúc gói và interface hẹp

Giữ `internal/host/imp`, tách theo trách nhiệm:

```text
imp/
├── types.go       Expose Options/Event và semantic DTO
├── source.go      Đọc, giải mã, chuẩn hóa, SourceUnit/anchor
├── workspace.go   meta/import artifact nguyên tử và InputDigest
├── call.go        typed LLM call chuyên cho import
├── segment.go     projection cấu trúc, hàm ngữ nghĩa biên giới, xác minh bao phủ
├── analyze.go     batch liên tục hai ngân sách, факты từng chương và prefix bị cắt
├── synthesize.go  RangeDigest và BookSynthesis
├── publish.go     đối soát Foundation và phát hành commit_chapter
└── runner.go      LoadState → NextAction → thực thi
```

Không thêm `ImportEngine`, `Task`, `WorkflowInstance`, Repository tổng quát hay registry plugin.

Các dependency do Host tiêm vào giữ ở mức hẹp:

```go
type Deps struct {
	Store         *store.Store
	CommitChapter ChapterCommitter
	Model         agentcore.ChatModel
	Runtime       ModelRuntime
	Prompts       Prompts
	Emit          func(Event)
}
```

`ModelRuntime` chỉ mang các факт gọi như context window, giới hạn completion, thinking, callback session/usage, và dành sẵn vị trí chọn mức mô hình cho từng hàm ngữ nghĩa (mặc định architect); không để `imp` phụ thuộc ngược vào toàn bộ Host, cũng không hàn chết một vai trò duy nhất thành tiền đề kiến trúc.

## 18. Giao diện người dùng

### 18.1 Import mới

```text
/import <path> [--yes] [--story=open|closed] [--continue] [--guide=<hướng-dẫn-chia-tách>]
```

Hành vi mặc định: tạo source snapshot, chia ngữ nghĩa và mở preview xác nhận, sau khi phát hành xong thì đặt một Hold chuyên cho import. Xóa `from=N`.

Ba tùy chọn đầu là các quyền cấp rõ ràng, độc lập với nhau, và được ghi vào `intent.json`:

- `--yes`: sau khi vượt qua kiểm tra, tự động chấp nhận chia tách; không quyết định trạng thái câu chuyện uncertain, không bỏ qua Hold hoàn tất;
- `--story=open|closed`: chỉ khi synthesis trả về uncertain thì mới cung cấp sẵn lựa chọn của người dùng; khi mô hình đã xác định rõ open/closed thì không ghi đè факт của mô hình;
- `--continue`: không tạo Hold chuyên cho import; không vượt qua advance mode bình thường, trong `review` vẫn cần `/next`.

`--guide` khác với ba mục trên: nó không phải quyền khởi động mà là input ngữ nghĩa cho việc chia tách, được ghi xuống `guidance.txt` của workspace (có thể chứa khoảng trắng, phải đặt ở cuối lệnh). Xem §18.3.

Vì vậy `/import book.txt --yes` vẫn sẽ dừng sau khi import hoàn tất; chỉ khi truyền thêm `--continue` thì mới cấp quyền để luồng sáng tác tiếp tục khi cửa kiểm soát bình thường cho phép.

### 18.2 Khôi phục

Khi cùng một cuốn sách có workspace đang hoạt động, thực thi `/import` không tham số sẽ trực tiếp suy ra bước tiếp theo từ факты và intent đã lưu; đường dẫn tệp nguồn và tham số khởi động không phải điều kiện bắt buộc để khôi phục. `/import <path>` có đường dẫn mới không được ghi đè workspace đang hoạt động.

Import chưa hoàn tất phải hiển thị chủ động, không thể đợi tới lúc người dùng sáng tác bị cổng kiểm soát từ chối mới lộ ra. Thực hiện bằng ba lớp nhắc nhở tăng dần:

1. Lúc khởi động TUI kiểm tra một lần (`imp.ResumeSummary`, tạo mô tả theo giai đoạn dựa trên `NextAction`), giao diện chào nổi bật hiển thị “phát hiện import chưa hoàn tất (đã phân tích N/M chương), nhập /import để khôi phục từ điểm ngắt”;
2. Khi người dùng bỏ qua nhắc nhở và cố sáng tác, cổng kiểm soát xuyên khởi động lại (§12.5) từ chối khởi động Engine và phát cảnh báo qua sự kiện;
3. Trong khi đang chạy khôi phục, bảng điều khiển import hiển thị thời gian thực giai đoạn hiện tại và tiến độ.

### 18.3 Phân chia lại

Sau khi kiểm tra preview, người dùng dùng `/import --guide=<mô tả tự nhiên>` để nhận diện lại, ví dụ `--guide=Ngoại truyện X cũng là một chương độc lập`. Hướng dẫn được ghi vào `guidance.txt` của workspace và đưa vào `InputDigest` của segmentation: khi guidance thay đổi, các lần chia cũ cùng confirmation, analysis và synthesis cũ không thể dựng lại cùng `InputDigest`, nên tự động làm lại toàn bộ; không cung cấp trình chỉnh sửa regex.

### 18.4 Hủy

Việc hủy trước khi xác nhận chỉ giữ lại workspace; trước khi publish có thể bỏ rõ ràng toàn bộ workspace. Sau khi publish bắt đầu, không cung cấp thao tác bỏ kiểu “giả như chưa từng có gì xảy ra”, chỉ cho phép khôi phục hoàn tất hoặc để người dùng xử lý sách chính thức theo cách khác.

## 19. Thứ tự triển khai

### Giai đoạn một: workspace và suy diễn trạng thái thuần

- Manifest, Intent, snapshot nguồn, `Artifact/InputDigest`, đọc/ghi nguyên tử;
- `LoadState/NextAction`;
- kiểm tra trước cho sách rỗng và khôi phục cùng nguồn;
- xóa phụ thuộc thiết kế `ResumeFrom`.

Giai đoạn này không gọi model, trước hết chứng minh sự kiện khôi phục không có mơ hồ.

### Giai đoạn hai: phân đoạn ngữ nghĩa và xác nhận

- SourceUnit, phân mảnh ảo cho dòng quá dài, anchor nguyên văn và chia khối theo ngân sách ngữ cảnh;
- BoundaryDecision typed call;
- xác minh bao phủ toàn văn;
- xem trước TUI, nhận diện lại bằng ngôn ngữ tự nhiên, `--yes` và artifact confirmation.

Trước hết dùng tiêu đề phi chuẩn, tiêu đề quyển, lời nói đầu và chú thích cuối để xác minh “không sót một chữ”.

### Giai đoạn ba: sự kiện từng chương theo batch liên tục

- `ImportedChapterFacts`;
- lập kế hoạch batch theo hai ngân sách context/completion;
- phân tích thứ tự giữa các batch và ledger tính liên tục gọn nhẹ;
- khôi phục artifact `InputDigest` của từng chương;
- nếu bị cắt cụt thì là “thất bại + thu nhỏ và gom lại batch”, đồng thời ghi lại văn bản một phần có khả dụng hay không;
- nối session, usage, failover, thinking, lỗi dung lượng và retry phản hồi cấu trúc.

### Giai đoạn ba · bổ sung: vớt tiền tố bị cắt cụt (tối ưu hiệu suất, có thể để sau)

- phân tích tiền tố hợp lệ liên tục `StopReasonLength` (§9.5);
- chỉ bật khi văn bản một phần có thể phân tích được, không thay đổi tính đúng đắn của khôi phục; công tắc riêng, nghiệm thu riêng.

### Giai đoạn bốn: tổng hợp phân tầng và Foundation

- RangeDigest có nhận biết ngữ cảnh;
- BookSynthesis;
- cấu trúc arc theo quyển dạng phạm vi;
- StoryStatus;
- lắp ráp và kiểm tra đầy đủ Foundation.

### Giai đoạn năm: publish và bàn giao

- đối soát digest từng artifact của Foundation;
- tái sử dụng `commit_chapter` để publish;
- khôi phục khi hủy/crash;
- cổng Engine xuyên restart;
- sau khi import mặc định hoàn tất thì AdvanceHold và `--continue` rõ ràng;
- artifact TUI/log/failure đầy đủ.

### Giai đoạn sáu: xóa triển khai cũ

- xóa phán quyết định dạng chương của `splitter.go`;
- xóa tagged envelope;
- xóa lệnh gọi toàn sách `ReverseFoundation`;
- xóa ngưỡng số chương `pickScale`;
- xóa `ResumeFrom/from=N`;
- xóa ràng buộc prompt “một quyển cố định, 1～3 arc, bắt buộc open threads”;
- sau khi triển khai xong mới cập nhật mô tả quy trình cũ trong README và architecture.

## 20. Kiểm thử và nghiệm thu

### 20.1 Kiểm thử hàm thuần và thuộc tính

- mọi segmentation hợp lệ đều thỏa mãn phạm vi toàn văn không chồng lấn, không có khoảng trống;
- SourceUnit bất hợp lệ, anchor nguyên văn không duy nhất, biên đảo thứ tự và trùng lặp chắc chắn bị từ chối;
- thứ tự biên được xét theo thứ tự số của `(Line, Part)`; xây dựng một tập unit mà “thứ tự từ điển và thứ tự số cho kết luận ngược nhau”, assert rằng thứ tự số được chấp nhận;
- dòng bình thường và phân mảnh ảo đều có thể ánh xạ ngược không mất mát về cùng một phần byte nguồn đã chuẩn hóa;
- mọi ranges arc quyển hợp lệ đều vừa khít bao phủ `1..N`;
- đầu vào ngữ nghĩa giống nhau ổn định sinh cùng `InputDigest`, bất kỳ thay đổi đầu vào thực nào cũng khiến artifact tương ứng không khớp;
- gom batch theo hai ngân sách không vượt quá ràng buộc context/completion đã cho;
- NextAction bất biến đối với cùng một snapshot sự kiện.

Làm fuzz/property test cho ánh xạ tọa độ, lắp ráp phạm vi, ngân sách batch và `InputDigest`, không assert rằng model sẽ xuất một tiêu đề cố định nào đó.

### 20.2 Kiểm thử hợp đồng model

- tên chương phi chuẩn và cấu trúc quyển/chương hỗn hợp;
- prologue/dẫn nhập/ngoại truyện được model phán đoán ngữ nghĩa là chương;
- front/back matter được hiển thị rõ ràng thay vì bị vứt bỏ;
- cả sách một dòng, một dòng nhiều chương và dòng vượt ngân sách được cắt chính xác thông qua SourceUnit + anchor;
- chương yên tĩnh cho phép characters rỗng;
- JSON bất hợp lệ, thiếu trường, phạm vi vượt biên đi vào retry phản hồi;
- analysis batch trả về các object từng chương liên tục, không được nhảy số hoặc lặp;
- `StopReasonLength` chỉ lưu tiền tố hợp lệ liên tục lớn nhất, không lưu nửa object và object phía sau không liên tục;
- khi structured mode không tạo ra văn bản một phần có thể phân tích, assert đi theo “thất bại + thu nhỏ và gom lại batch” và log đánh dấu `prefix_salvage=unavailable`;
- JSON hỏng dưới `StopReasonStop` thông thường không đi vào đường tiền tố cắt cụt;
- khi một chương vẫn bị cắt cụt thì thất bại rõ ràng, không sinh sự kiện rỗng;
- lỗi phân tích/nghiệp vụ của Prompt Contract liên tục phản hồi để tự sửa cho tới khi thành công hoặc context bị hủy; vi phạm hợp đồng Schema native, từ chối trả lời và cắt cụt lập tức giữ lại phản hồi gốc và dừng;
- model không hỗ trợ thinking/JSON Schema không nhận tham số bất hợp lệ.

Kiểm thử model assert hợp đồng và bất biến, không assert phán đoán văn học chính xác.

### 20.3 Ma trận crash

Ít nhất bao phủ:

- sau snapshot nguồn;
- sau segmentation, trước xác nhận;
- trước và sau khi object thứ N của analysis batch được ghi xuống đĩa;
- trước và sau khi chương cuối cùng của tiền tố cắt cụt do độ dài được ghi xuống đĩa;
- giữa RangeDigest;
- sau Synthesis, trước Foundation;
- trước và sau mỗi artifact Foundation;
- từng cửa sổ draft/StartChapter/PendingCommit/progress/checkpoint;
- sau khi submit chương cuối cùng, trước và sau AdvanceHold;
- sau khi một phần Foundation/chương đã publish thì restart và thử Host.Resume thông thường.

Sau khi restart ở mỗi cửa sổ, chỉ được tiếp tục hành động hiện tại, không được tiêu thụ lặp lại model call đã thành công, cũng không được vượt qua artifact thất bại. Khi `NextAction != done`, Engine thông thường bắt buộc phải bị cổng chặn cho tới khi khôi phục import hoàn tất.

### 20.4 Dạng hồi quy #83

Dựng đầu vào 54 chương và dài hơn, xác minh:

1. Không có lệnh gọi đơn nào yêu cầu xuất dàn ý chi tiết 54 chương;
2. analysis batch được gom theo cả ngân sách context đầu vào và ngân sách completion đầu ra khả kiến, không nhồi quá nhiều chương chỉ vì đầu vào chứa được;
3. Giai đoạn ba · bổ sung: mô phỏng `StopReasonLength` “13 chương đầu hoàn chỉnh, chương 14 bị cắt cụt”, chỉ submit 13 chương đầu, hành động tiếp theo bắt đầu từ chương 14; khi chưa triển khai vớt tiền tố, cả batch thất bại rồi gom lại batch từ chương đầu của batch;
4. Mô phỏng phản hồi cắt cụt không có object hoàn chỉnh, hiển thị đầy đủ lỗi, ghi log, lưu phản hồi gốc và không ghi artifact phân tích;
5. Mô phỏng JSON hỏng thông thường, đi theo retry phản hồi cấu trúc thay vì vớt tiền tố;
6. Sau khi sửa chỉ chạy lại hành động thiếu đầu tiên, không làm lại các chương đã hoàn thành;
7. Tiêu đề phi chuẩn đi vào xem trước thông qua phân đoạn ngữ nghĩa, không sửa bằng cách thêm regex mới.

### 20.5 Tiêu chuẩn nghiệm thu cuối cùng

1. Chế độ tương tác mặc định cho phép người dùng nhìn thấy và xác nhận toàn bộ biên chương trước khi ghi chính thức xuống đĩa; `--yes` có thể tự động chấp nhận rõ ràng và để lại artifact kiểm toán tương đương.
2. Bất kỳ văn bản nguồn không rỗng nào cũng có thể tìm được thuộc về duy nhất từ segmentation.
3. 200～500 chương sẽ không tạo thành một model call đọc toàn bộ chính văn cả sách và xuất toàn bộ object chương; mỗi analysis batch đồng thời bị ràng buộc bởi ngân sách đầu vào và đầu ra, đầu ra toàn cục chỉ biểu đạt sự kiện toàn cục và phạm vi arc quyển.
4. Sau khi crash ở bất kỳ giai đoạn nào, không cần `from=N` vẫn có thể khôi phục chính xác.
5. Trạng thái chính thức giữ nguyên trước khi xác minh ngữ nghĩa đầy đủ.
6. Sau khi publish bị gián đoạn, commit saga hiện có có thể khôi phục, và sẽ không submit chương lặp.
7. Import chưa hoàn tất sau restart không được khởi động Engine thông thường; chỉ được xem, chẩn đoán hoặc khôi phục import.
8. `--yes` không bỏ qua Hold hoàn tất; chỉ `--continue` độc lập mới bỏ qua, và sẽ không vòng qua cổng review.
9. Năng lực, usage, StopReason, ước lượng ngân sách và lỗi của model và provider đều có thể quan sát.
10. Chỉ cần đổi sang model mạnh hơn là có thể cải thiện chất lượng cắt đoạn, phân tích và tổng hợp, đồng thời tự nhiên mở rộng batch an toàn, giảm số lần gọi, không sửa quy tắc văn học trong Go.

## 21. Khả năng mở rộng hướng tương lai

Khả năng mở rộng của phương án này đến từ biên ổn định, không phải abstraction dựng sẵn:

- hiểu biết của model tăng cường: ba loại hàm ngữ nghĩa Boundary/Chapter/Synthesis trực tiếp trở nên chính xác hơn;
- cửa sổ ngữ cảnh hoặc đầu ra mở rộng: bộ tính hai ngân sách tự động mở rộng analysis batch an toàn, đồng thời giảm số khối và số tầng Reduce;
- đầu ra có cấu trúc tăng cường: typed-call tự động chọn ràng buộc provider mạnh hơn;
- model phân khúc rẻ trở nên mạnh hơn: segment có tính cơ giới mạnh hơn có thể chuyển sang phân khúc rẻ hơn, lợi ích chi phí theo đó đi vào, không đổi hợp đồng ngữ nghĩa;
- định dạng đầu vào mới: chỉ cần chuyển EPUB v.v. thành cùng một văn bản chuẩn hóa và tọa độ SourceUnit;
- ngữ nghĩa toàn sách mới: thêm trường có consumer rõ ràng vào `ImportedChapterFacts` hoặc `BookSynthesis`, không đổi giao thức khôi phục và publish;
- đồng sáng tạo với người dùng tăng cường: thêm chỉnh sửa bằng ngôn ngữ tự nhiên khi xác nhận biên, không viết tri thức định dạng vào code.

Phần bất biến là bao phủ toàn văn, danh tính `InputDigest`, kiểm tra phạm vi và publish lũy đẳng. Đây là bookkeeping mà dù model mạnh hơn cũng không đáng giao cho model; toàn bộ ngữ nghĩa có thể thay đổi đều để trong các hàm model, vì vậy lợi ích nâng cấp model có thể xuyên thấu tới kết quả sản phẩm.

## 22. Quyết định cuối cùng

Áp dụng **pipeline import ngữ nghĩa theo giai đoạn**, từ chối hai hướng:

1. Tiếp tục mở rộng regex chương và ngưỡng số chương/số arc;
2. Dùng một Agent vòng lặp dài tự do tiếp quản toàn bộ import.

Biên cuối cùng là:

> **Model quyết định văn bản có nghĩa là gì; code bảo đảm từng chữ đi đâu, mỗi kết quả tương ứng với đầu vào nào, đầu vào/đầu ra của mỗi lần gọi đều chứa vừa, sau thất bại tiếp tục từ đâu, và khi nào đủ tư cách trở thành sự kiện chính thức.**

Điều này vừa giữ lại năng lực tự chủ và lợi ích tương lai của model, vừa duy trì kiến trúc gọn nhẹ hiện tại của ainovel-cli: Engine + hàm ngữ nghĩa typed + lớp sự kiện file.