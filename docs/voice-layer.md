# Thiết kế lớp văn phong

> Trạng thái: thiết kế chốt v2 (2026-07-12 tiếp thu đánh giá bên ngoài: bổ sung ngữ nghĩa phủ, ngữ nghĩa đường dẫn, thứ tự lắp ghép, lối vào eval, giao thức thống kê đánh giá), **có thể triển khai**.
> Ưu tiên: làm trước phần tiến hóa mặt điều khiển (docs/engine-arbiter.md) —— “mùi AI” là điểm đau hiện hữu của người dùng.

## Một, bối cảnh và định nghĩa vấn đề

Người dùng phản hồi rằng nội dung sinh ra “nặng mùi AI”. Sau khi rà soát, kết luận là: **vấn đề không nằm ở việc tri thức văn phong và quy trình bị ghép quá sâu, mà nằm ở việc vòng lặp cải tiến bị đứt ở hai chỗ**:

1. **Sửa một lần là phải biên dịch lại** —— toàn bộ tài sản ngữ nghĩa văn phong (anti-ai-tone.md, chuẩn viết writer.md, styles/*.md) đều dùng `go:embed`; chỉ chỉnh một cách diễn đạt cũng phải build lại và phát hành;
2. **Không có vòng đo lường chuyên dụng cho văn phong** —— sửa xong chỉ có thể dựa vào cảm giác đọc của con người, không có đối chiếu trước/sau khách quan, khiến việc tối ưu hóa trở thành cảm tính.

## Hai, kiểm kê hiện trạng (tổng cộng năm lớp tài sản liên quan đến văn phong)

| Lớp | Vị trí | Hiện trạng | Người dùng có thể chỉnh |
|----|------|------|---------|
| Tiêu chí ngữ nghĩa | `assets/references/anti-ai-tone.md` | writer né tránh + editor đưa chứng cứ dùng chung, năm loại: cấu trúc/từ ngữ/miêu tả/đối thoại/nhịp | ❌ Nhúng sẵn |
| Chuẩn viết | `assets/prompts/writer.md` §Chuẩn viết | Trộn cùng giao thức thực thi trong một file nhúng | ❌ |
| Preset phong cách | `assets/styles/*.md` (4 cái) | cfg.Style chọn một preset, nối thêm vào writer prompt | ❌ và không thể thêm mới |
| Quy tắc cơ học | `internal/rules` | Từ gây mệt/cụm cấm/số chữ, commit bắt buộc kiểm tra | ✅ Đã có phủ ba tầng (lưu ý: “cấp dự án” của nó gắn với **cwd**, xem 3.4) |
| Thiết lập ưu tiên runtime | Hành động `rules` của Arbiter | Ngôn ngữ tự nhiên → có cấu trúc, có hiệu lực sau khi khởi động lại | ✅ |

Ngoài ra còn hai hạ tầng then chốt: **stylestat** (thống kê tic câu cú cấp toàn sách, đưa ngược cho writer làm “gương thói quen diễn đạt”, thuần code, không ảo giác) và **`OverridePrompt` của eval** (hạ tầng prompt A/B đã tồn tại).

Kết luận: khả năng tùy chỉnh và nguyên liệu đo lường của lớp cơ học đã sẵn sàng; khoảng trống tập trung ở **lớp ngữ nghĩa chưa thể phủ** và **vòng đo lường chưa căn đúng vào văn phong**.

## Ba, thiết kế

### 3.1 Nguyên tắc cốt lõi

**Tách “viết như thế nào” (văn phong) khỏi “phối hợp như thế nào” (giao thức): phần trước dữ liệu hóa và có thể phủ; phần sau tiếp tục nhúng khi biên dịch.**

### 3.2 Tách writer.md: điền lại tại chỗ bằng placeholder

Mục chuẩn viết của writer.md nằm ở **giữa** file (sau giao thức thực thi, trước tính liên tục của nhân vật phụ), nên không thể đơn giản nối vào cuối. Dùng phương án placeholder:

- `writer.md` (giao thức, nhúng): giữ giao thức thực thi / tiếp tục từ điểm ngắt / viết lại và đánh bóng / hợp đồng chương / mô tả cơ chế ưu tiên người dùng / **toàn bộ mục số chữ (bao gồm gợi ý cách viết)** / tính liên tục của nhân vật phụ / tham số commit; vị trí mục chuẩn viết ban đầu được thay bằng placeholder **duy nhất** `{{VOICE}}`
- `voice.md` (văn phong, có thể phủ): toàn bộ mục chuẩn viết (khử mùi AI / đa dạng câu cú / không kể lại phần trước)

Gợi ý cách viết về số chữ được giữ trong file giao thức (tiếp thu đánh giá 2026-07-12): nó gắn chặt với việc thực thi hợp đồng số chữ; nếu tách ra sẽ cần placeholder thứ hai, biến Voice thành định dạng nhiều mảnh —— không đáng chỉ vì một đoạn mẹo rất ít người muốn phủ; ưu tiên của người dùng về số chữ đi qua user_rules. Giữ nguyên tên file `writer.md` không đổi (`OverridePrompt` của eval lấy tên file làm khóa, đổi tên chỉ làm tăng công việc nối dây).

**Thứ tự lắp ghép phải tương thích từng byte với hiện trạng**. Hiện trạng là `writer.md → simulationGuidance → style` (assets/load.go:84 + agents/build.go:247), nên hàm lắp ghép duy nhất là:

```go
// Lối vào duy nhất cho production, eval, test; {{VOICE}} điền lại tại chỗ để bảo đảm tách không hao hụt
func BuildWriterPrompt(protocolTemplate, voice, simulationGuidance, style string) string
// = replace(protocolTemplate, "{{VOICE}}", voice) + simulationGuidance + style
```

Bài học từ tiền lệ: chú thích của `WithSimulationGuidance` từng ghi lại cái bẫy “baseline có bọc, variant không bọc → A/B không tương đương”; đường lắp ghép phân nhánh là nơi dễ sinh ra cùng loại sự cố, nên thu về một hàm duy nhất.

### 3.3 Mô hình phủ: ngữ nghĩa theo từng tài sản (không mơ hồ)

| Tài sản | Ngữ nghĩa phủ | Lý do |
|------|---------|------|
| `voice.md` | **Nối thêm**: giữ bản tích hợp, toàn cục/quyển hiện tại được nối thêm như các đoạn có đánh dấu | Thay toàn file sẽ khiến người dùng mãi kẹt ở bản tích hợp cũ; nhu cầu phổ biến là tinh chỉnh chứ không phải viết lại |
| `anti-ai-tone.md` | **Nối thêm** (như trên) | Nhu cầu phổ biến là bổ sung tiêu chí; người dùng muốn lật đổ tiêu chí tích hợp là cực thiểu số, không thiết kế riêng cho họ |
| `styles/<name>.md` | **Thay toàn file cùng tên**; tên file mới đồng nghĩa với thêm phong cách mới | Phong cách là tiếng nói tổng thể, gộp hai phong cách không có ý nghĩa |
| `genres/<name>/style-references.md` | Thay toàn file cùng tên; khi style tùy chỉnh không có reference thì **cho phép thiếu, không fallback về default** (tham chiếu sai còn tệ hơn không có) | Như trên |
| user_rules | Ưu tiên cao nhất ở runtime (không đổi hiện trạng) | — |

Lắp ghép ngữ nghĩa nối thêm có đánh dấu ranh giới rõ ràng:

```
## Văn phong mặc định của dự án
...
## Phủ văn phong toàn cục của người dùng (các yêu cầu dưới đây ưu tiên hơn mặc định dự án)
...
## Phủ văn phong của quyển hiện tại (các yêu cầu dưới đây ưu tiên hơn toàn bộ phía trên)
...
```

**Ranh giới trung thực**: dưới ngữ nghĩa nối thêm, “cái sau thắng” là chỉ dẫn ưu tiên cho LLM, không phải bảo đảm cơ học —— văn phong là nội dung mang tính gợi ý, có thể chấp nhận; các ràng buộc cần bảo đảm cơ học phải đi qua lớp rules (ở đó mới là phủ thật). Ranh giới này được ghi vào tài liệu người dùng.

`arc-templates.md` thuộc mặt phẳng quy hoạch (định hình cấu trúc câu chuyện chứ không phải giọng văn), **không vào whitelist v1**, ghi nhận để bàn sau.

### 3.4 Ngữ nghĩa đường dẫn: cấp quyển hiện tại gắn với outputDir, không gắn cwd

```
Cấp quyển hiện tại   <outputDir>/style/     >   Toàn cục   ~/.ainovel/style/   >   Mặc định tích hợp (embed dự phòng)
```

- Gắn với outputDir khiến Voice **đi theo sách**: đổi thư mục vẫn khôi phục cùng một quyển sách và nạp cùng một văn phong; phân giải đường dẫn nhất quán giữa Docker/headless/TUI; khi nhiều sách dùng chung cwd thì không nhiễu nhau
- Chữ ký `assets.Load` nhận rõ root phân giải (outputDir), **bên trong không đọc cwd**
- Lưu ý khác biệt với lớp rules: `./.ainovel/rules` của rules gắn với cwd (quy ước sẵn có trong internal/rules/loader.go, thiết kế này không động vào); tài liệu người dùng nói rõ ngữ nghĩa hai bên khác nhau —— rules là “cấp dự án”, voice là “cấp quyển hiện tại”

Cấu trúc thư mục người dùng đầy đủ:

```
<outputDir>/style/            (đồng cấu với ~/.ainovel/style/)
  voice.md                    Đoạn nối thêm
  anti-ai-tone.md             Đoạn nối thêm
  styles/
    xianxia.md                Thêm mới hoặc thay cùng tên
  genres/
    xianxia/
      style-references.md     Tùy chọn
```

Tên style chính là tên file, kiểm tra `[a-z0-9-]+`, từ chối ký tự đường dẫn.

### 3.5 Vì sao mở cho người dùng là an toàn

Toàn bộ bất biến giao thức đều nằm ở **lớp sự kiện**: draft trước check, commit bắt buộc kiểm tra quy tắc cơ học, chặn vượt biên số chữ, checkpoint idempotent —— không nằm trong prompt. Dù người dùng sửa voice.md vô lý đến đâu, guard và tiền điều kiện của tool vẫn có hiệu lực như thường; kết quả xấu nhất là văn tệ, máy trạng thái không hỏng.

### 3.6 Thời điểm có hiệu lực và lối vào eval

- v1 phân giải khi khởi động, **khởi động lại để có hiệu lực** (khôi phục điểm ngắt chính xác đến bước, chi phí khởi động lại gần như bằng không; không làm tải lại nóng)
- eval thêm **lối vào variant độc lập cho voice** (như `Bundle.OverrideVoice(raw)`), bên trong đi cùng đường `BuildWriterPrompt` —— cấm làm A/B văn phong bằng cách phủ toàn bộ writer.md (sẽ kéo theo giao thức, và giao thức baseline/variant có thể không bằng nhau)

## Bốn, vòng đo lường: bộ đánh giá văn phong

```
Sửa voice/anti-ai-tone
  → Bộ đánh giá văn phong (case cố định, eval voice-variant A/B)      ← Phần thêm mới duy nhất
  → Đối chiếu chỉ số stylestat (chỉ số cứng xác định)
  + LLM judge chấm điểm và đưa chứng cứ từng mục theo tiêu chí anti-ai-tone (giai đoạn đầu chỉ báo cáo, không làm hard gate)
```

Giao thức thống kê (đầu vào cố định chỉ bảo đảm **có thể so sánh**, không bảo đảm tái lập):

- baseline/variant khóa cùng một model và tham số suy luận
- Mỗi case lặp N≥3 lần, báo cáo trung bình, phương sai và mẫu thô
- judge đánh giá mù (không lộ danh tính baseline/variant)
- case phủ thể loại × kiểu chương (mở đầu/đẩy tiến đời thường/cao trào/khép lại)

## Năm, nêu rõ không làm (chống thiết kế quá mức)

- Không mở prompt giao thức cho người dùng cuối (`OverridePrompt` giữ làm năng lực nội bộ của eval)
- Không tải lại nóng trong lúc chạy
- Không mở pattern regex của stylestat thành cấu hình người dùng (lớp cơ học đã có lối mở rộng: fatigue_words/forbidden_phrases của rules)
- Không làm chợ phong cách/cơ chế chia sẻ (copy thư mục style là tự nhiên có thể chia sẻ)
- arc-templates không vào whitelist v1

## Sáu, bước triển khai và nghiệm thu

1. Tách writer.md (placeholder `{{VOICE}}`) + hàm lắp ghép duy nhất `BuildWriterPrompt`
2. Bộ phân giải ba tầng: `assets.Load(outputDir, style)` + ngữ nghĩa theo từng tài sản (bảng 3.3) + hợp nhất danh sách styles; unit test phủ ưu tiên/fallback khi thiếu/đánh dấu ranh giới nối thêm
3. Lối vào eval `OverrideVoice`
4. Tài liệu người dùng: cấu trúc thư mục, ngữ nghĩa theo từng tài sản, khác biệt ngữ nghĩa đường dẫn của rules và voice, ví dụ
5. Bộ đánh giá văn phong (có thể để sau thành tác vụ độc lập)

**Tiêu chuẩn nghiệm thu**: ① Khi không có bất kỳ file phủ nào, kết quả của `BuildWriterPrompt` **giống từng byte** với trước khi tách; ② Ưu tiên ba tầng và ngữ nghĩa nối thêm/thay thế có unit test table-driven; ③ Sau khi thêm `styles/xianxia.md`, `style: xianxia` đặt vào là dùng được; ④ eval voice A/B và production dùng cùng đường lắp ghép (có test chứng minh); ⑤ Toàn bộ test và hồi quy sim xanh.

## Bảy, quan hệ với tiến hóa mặt điều khiển

Hoàn toàn trực giao (mặt phẳng nội dung vs mặt phẳng điều khiển), không có phụ thuộc triển khai. Thứ tự quy ước: **lớp văn phong → bộ đánh giá văn phong → Engine/Arbiter (thúc đẩy theo nghị quyết §Tám trong tài liệu của nó)**.