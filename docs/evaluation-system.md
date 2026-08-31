# Hệ thống đánh giá ainovel-cli

> Đánh giá không phải là tạo mới một bộ script kiểm tra, mà là lấy **các bộ chẩn đoán sự thật đã có của dự án (`diag`), bộ thống kê văn phong toàn sách (`stylestat`), cơ chế thẩm định gốc bảy chiều (`ReviewEntry`) làm evaluator**, rồi bọc thêm một lớp harness batch offline. Một định nghĩa sự thật, hai nơi không còn trôi lệch.

---

## 0. Vì sao cần thiết kế lại

Độ ổn định đã chạy thông: truyện dài 235 chương / 1,27 triệu chữ viết xong trong một lần, vòng kín lập kế hoạch cuộn đã thành lập (xem `architecture.md` §9.1). Nút thắt đã chuyển dịch — **chất lượng có thể lặp cải tiến**:

- Sau khi sửa một prompt, quy trình có còn ổn định không? Toolchain, tiến trình trạng thái, sự thật được persistence có còn đúng không?
- Chất lượng chính văn, đại cương, thẩm định thực sự được cải thiện, hay chỉ là lần này ngẫu nhiên lấy được kết quả tốt?
- Trong truyện dài, nhân vật, timeline, foreshadow, context có tiếp tục đáng tin cậy không?
- **Sự cố định văn phong cấp toàn sách** (sentence-pattern tic trung bình mỗi chương hàng chục lần, hình thái cuối chương đồng cấu, lặp nguyên văn xuyên chương) có tốt lên hay xấu đi không? Đây là thủ phạm thật sự của kết quả thực chứng 196 chương 6.5/10, đánh giá đơn chương vốn dĩ mù với nó.

Hiện tại các phán đoán này dựa vào “cảm giác + đọc mẫu thủ công”. Hệ thống đánh giá cần biến thay đổi prompt từ dựa vào cảm giác thành một quy trình kỹ thuật **có hồi quy, có bằng chứng, có đọc mẫu thủ công**.

Nhưng dự án này không cần, và cũng không nên bê nguyên nền tảng eval thông dụng trong ngành (dataset / experiment / scorer / database / Web UI). Lý do rất đơn giản: **phần cốt lõi của những năng lực đó — kiểm tra xác định và tín hiệu chất lượng — đã tồn tại trong dự án, hơn nữa được viết bằng Go và chia sẻ cùng một mô hình sự thật với runtime.**

---

## 1. Luận điểm cốt lõi: evaluator đã tồn tại

Trong bốn loại evaluator của hệ thống đánh giá, ba loại đã được hiện thực trong codebase, chỉ là chưa từng được gọi như “evaluator”:

| Evaluator | Năng lực đã có của dự án | Điểm vào | Đầu ra |
|---|---|---|---|
| **Chẩn đoán sự thật xác định** | Một nhóm quy tắc artifact + quy tắc runtime của `internal/diag` | `diag.Diagnose(store)` | `Report{Stats, Findings}`, Finding có Severity/Evidence |
| **Hồi quy văn phong cấp toàn sách** | `internal/stylestat` | `stylestat.Compute(input)` | Số lần mẫu câu trung bình mỗi chương, câu lặp xuyên chương, tỷ lệ câu ngắn cuối chương, lẫn lộn tiêu đề |
| **Phán định chất lượng (rubric)** | rubric có version (ban đầu phái sinh từ bảy chiều của `editor.md`) | LLM Judge (thước đo cố định làm A/B) | consistency/character/pacing/continuity/foreshadow/hook/aesthetic |
| **Xuất hành vi đã khử nhạy cảm** | export của `internal/diag` | `diag.WriteExport(store, rep, rc)` | Khung hành vi, phục vụ đọc mẫu thủ công và lưu trữ |

`diag.Analyze(s *store.Store)` nhận một Store là có thể xuất ra `Report` đầy đủ — **bản thân nó vốn đã có thể chạy offline trên bất kỳ thư mục đầu ra nào**. `stylestat.Compute` là hàm thuần. Điều này có nghĩa hệ thống đánh giá cần làm không phải là hiện thực lại “chương có được ghi xuống disk không, progress có được đẩy tới không, checkpoint có tồn tại không, có pending sót lại không, quy trình có bị vòng lặp chết không” — những việc này diag đều đã làm, hơn nữa mỗi quy tắc đều tương ứng với một hố thật đã từng đạp phải (`PhaseFlowMismatch`, `OrphanedSteer`, `OutlineExhausted`, `repeatedErrors`/`stuckStep` tương ứng các lỗi lịch sử như idleResume / đại cương cạn kiệt livelock / tool call bị in như chữ thường).

> **Công việc của hệ thống đánh giá không phải là tạo kiểm tra, mà là: điều khiển batch + chạy các evaluator đã có trên đầu ra + ánh xạ Finding/thống kê thành gate + tổng hợp report.**

---

## 2. Nguyên tắc thiết kế

### 2.1 Evaluator chính là diagnostic, tuyệt đối không tạo lại kiểm tra xác định

Kiểm tra xác định chỉ gọi `diag.Diagnose`, không parse lại `progress.json` / `checkpoints.jsonl` / `sessions/*.jsonl` ở tầng đánh giá. Lý do là luật DRY thép của dự án này: **“thế nào là trạng thái hợp lệ” chỉ được có một định nghĩa.** Nếu đánh giá dùng Python parse lại checkpoint để phán đoán commit có thiếu không, sẽ có hai định nghĩa “commit hoàn thành”; runtime sửa rule diag mà đánh giá không sửa theo, gate sẽ lập tức méo lệch.

→ Evaluation harness dùng **Go**, gọi in-process `diag` và `stylestat`, chia sẻ `internal/domain` và `internal/store` với runtime. Đây là khác biệt căn bản nhất giữa thiết kế này và phiên bản trước.

### 2.2 Hồi quy văn phong cấp toàn sách là tín hiệu chất lượng đầu tiên

LLM Judge đơn chương nhìn chương nào cũng “bình thường”, nhưng nút thắt lại chính là sự cố định xuyên chương. Vì vậy **xương sống xác định của hồi quy chất lượng là `stylestat`, không phải LLM Judge**.

**Tiền đề: `stylestat.Compute` trực tiếp trả nil nếu ít hơn 5 chương** (`stylestat.go` `minChapters=5`, mẫu quá nhỏ thì tần suất vô nghĩa). Vì vậy hồi quy văn phong **chỉ có hiệu lực ở tầng Quality / Longform với ≥5 chương**, Smoke 1 chương không lấy được tín hiệu văn phong — điểm này quyết định chi phí và chiến lược mặc định ở phần sau. Chỉ số bao gồm:

- Số lần mẫu câu trung bình mỗi chương của variant vs baseline (`patterns[].per_chapter`)
- Tỷ lệ kết thúc bằng câu ngắn ở cuối chương (`ending.short_ratio` tiến gần 1 là bệnh)
- Số câu lặp nguyên văn xuyên chương (`repeated_sentences`)
- Lẫn lộn định dạng tiêu đề (`title_formats`)
- Tỷ lệ từ chỉ thời gian ở mở đầu (`opening_time_rate`)

Những chỉ số này không tốn chi phí LLM, xác định, và đánh thẳng vào nút thắt chất lượng. **LLM Judge là bổ sung, stylestat delta là tuyến chính.**

### 2.3 LLM Judge căn chỉnh với rubric gốc bảy chiều, không dựng lò riêng

Judge không phát minh chiều chấm điểm mới — các chiều nghiêm ngặt bằng bảy mục của `domain.DimensionScore`, thực hiện so sánh baseline/variant.

**Nhưng rubric phải được version hóa, có thể cố định**, lưu thành snapshot `evals/rubrics/*.json`, không đọc realtime từ `editor.md` khi runtime chạy. Lý do: khi đối tượng được test chính là `editor.md`, nếu trọng tài cũng thay đổi theo `editor.md`, benchmark đánh giá sẽ trôi — trọng tài và đối tượng được test đồng nguồn sẽ khiến không thể phán đoán “sửa editor là tốt hay xấu”. Vì vậy rubric ban đầu **phái sinh** từ bảy chiều của editor (đảm bảo cùng khẩu kính), sau đó **tiến hóa độc lập, bump version tường minh**; report ghi lại đang dùng version rubric nào.

### 2.4 Finding xác định quyết định gate, LLM và con người chỉ làm phán định chất lượng

Căn chỉnh với luật thép kiến trúc “thống kê thuộc về code, phán định thuộc về LLM”:

- **Chỉ bằng chứng xác định mới có thể chặn merge**: Finding `SevCritical` của `diag`, assertion hợp đồng do case khai báo thất bại.
- **LLM Judge và đọc mẫu thủ công tạo ra warning và manh mối sắp hạng**, không tự mình quyết định merge.
- Một câu: `Finding.Severity` ánh xạ trực tiếp sang cấp gate, không đưa vào hệ thống phân loại severity mới.

### 2.5 Đánh giá chỉ quan sát, không can thiệp control flow

Đánh giá tái sử dụng `diag`, nhưng **bỏ qua `Action` và `Planner` của diag** — đó là thứ thuộc control flow runtime. Trong ngữ cảnh đánh giá, `diag.Report` chỉ lấy `Stats` và `Findings`, Action luôn bị ignore. Đánh giá không tự sửa prompt, không tự rollback, không chạy tiếp. Đây là phần mở rộng của kỷ luật quan sát viên (`architecture.md` §2.3) trong ngữ cảnh đánh giá.

### 2.6 Phơi bày thất bại một cách tường minh

Không mock thành công, không nuốt lỗi, không dùng template giả vờ pass. Bất kỳ thất bại nào ở model, tool, config, filesystem, parsing, judge đều được ghi rõ nguyên nhân trong report. **Bản thân thất bại cũng là kết quả đánh giá** — một case chạy sập thì gate là FAIL, không phải “skip”.

### 2.7 Mỗi lần chỉ xác minh một biến

Ràng buộc cứng của A/B: cùng yêu cầu, cùng config, cùng model/provider, cùng style, thư mục output cách ly. Baseline = prompt chính thức hiện tại, Variant = chỉ thay prompt file cần xác minh lần này. Một experiment không đồng thời sửa Writer/Architect/Editor/Arbiter.

---

## 3. Toàn cảnh kiến trúc

```text
[Cases]  evals/cases/*.json —— tập assertion tầng sự thật, không phải các dòng dataset thông dụng
   │
[Runner]  internal/eval —— lắp ráp host driver in-process (dừng theo giới hạn số chương), bundle.Prompts override trong bộ nhớ để làm variant
   │       baseline run ┐
   │       variant  run ┘  mỗi bên cách ly thư mục output riêng
   ▼
[Collectors]  thu thập với từng thư mục đầu ra:
   ├── diag.Diagnose(store)      → Report{Stats, Findings}      (sự thật + runtime)
   ├── stylestat.Compute(input)  → thống kê văn phong toàn sách                 (xương sống hồi quy chất lượng)
   ├── assertion hợp đồng case   → checkpoint/phase/hợp đồng tool kỳ vọng (phần diag không bao phủ)
   ├── usage / cost / token      → đọc từ meta/usage.json
   └── tool_calls                → đọc tool call thật từ meta/sessions/*.jsonl
   ▼
[Graders]
   ├── gate xác định: Finding.Severity + assertion hợp đồng → hard_fail / regression
   ├── stylestat delta: chênh lệch chỉ số văn phong variant vs baseline
   ├── LLM Judge (tùy chọn): so sánh A/B theo rubric bảy chiều
   └── Human: con người đọc sản phẩm baseline/variant
   ▼
[Report]  report.json (máy đọc) + report.md (người đọc) + xuất hành vi đã khử nhạy cảm
   └── Gate: PASS / WARN / FAIL
```

Hướng phụ thuộc: `eval → host → agents → tools → store → domain`, tái sử dụng ngang `diag` / `stylestat`. Tầng đánh giá **không phụ thuộc ngược** vào control flow runtime, chỉ đọc Store và evaluator chỉ-đọc.

> **Hiện thực hiện tại bao phủ tuyến xác định chính**: khi không có `--variant` là `mode=single`; khi truyền `--variant` là `mode=ab`, cùng một case chạy baseline và variant cách ly, đồng thời tạo delta. Collectors đã nối `diag.Diagnose`, hợp đồng case, `stylestat.Compute`, `meta/usage.json`, đếm session tool call; Graders đã nối gate xác định, diag delta baseline/variant, cost/token/tool call delta, stylestat delta. Runner trực tiếp dùng `host.New` để lắp ráp và tự có cơ chế dừng theo giới hạn số chương, **không tái sử dụng `headless.Run` không có giới hạn số chương**. LLM Judge và Human vẫn là tầng tùy chọn sau này, không tham gia gate xác định hiện tại.

---

## 4. Vì sao là Go in-process, không phải shell + Python

| Chiều | shell copy source + Python parse (lối cũ) | Go in-process (thiết kế này) |
|---|---|---|
| Kiểm tra xác định | Python parse lại JSON, có hai định nghĩa với rule diag | Gọi trực tiếp `diag.Diagnose(store)`, một định nghĩa |
| Chuyển variant | Copy toàn bộ source tree + `go build` lại hai binary | Override trong bộ nhớ bằng `bundle.OverridePrompt(...)` rồi lắp host, zero-copy zero-recompile |
| Hồi quy văn phong | Cần viết lại logic tách câu tiếng Trung của stylestat bằng Python | Gọi trực tiếp `stylestat.Compute` |
| Judge rubric | Các chiều rải rác trong Python | Tái sử dụng `domain.DimensionScore`, đồng nguồn với online |
| Rủi ro trôi lệch | Cao: runtime sửa mô hình sự thật, eval không theo kịp | Thấp: compile-time sẽ phơi bày thay đổi field |

Lý do `prompt_ab.sh` cũ phải copy source và recompile là prompt được embed vào binary (`go:embed`). Nhưng `assets.Bundle.Prompts` là struct thông thường, **runner sửa một field trong bộ nhớ là có thể làm variant**, hoàn toàn không cần copy source. Đây là đơn giản hóa lớn nhất có được khi viết harness bằng Go.

> **Ràng buộc hiện thực**: `assets.Load` thông qua `loadPrompts` thêm thống nhất hậu tố `WithSimulationGuidance` vào Worker prompt (architect/writer/editor). Nếu variant chỉ nhét text thô vào `bundle.Prompts.Writer`, sẽ làm mất hậu tố chân dung mô phỏng mà baseline có, A/B không tương đương.
>
> Cách đúng là override thông qua `assets.OverridePrompt`, bên trong đi qua đúng cùng một wrapping với `Load`; eval không copy logic wrapping.

> Bản tài liệu trước giữ lại `prompt_ab.sh` / `prompt_ab_report.py` và “từng bước trích xuất năng lực”. Thiết kế này từ bỏ con đường đó: các vấn đề chúng giải quyết (chạy cách ly + tổng hợp chỉ số) là tập con trong Go harness in-process, cố tái sử dụng ngược lại còn phải gánh keo giao diện ba ngôn ngữ shell/Python/Go. **Go harness là tuyến chính duy nhất**; Go harness hiện tại đã bao phủ chạy cách ly baseline/variant, tổng hợp repeat và delta xác định. Script cũ (`scripts/prompt_ab.sh`, `scripts/prompt_ab_report.py`) và manual thao tác của chúng `docs/prompt-ab.md` đã được xóa cùng khi thiết kế này hạ cánh, không giữ lại nữa.

---

## 5. Case Manifest

Case là đơn vị tối thiểu của input đánh giá, đồng thời cũng là một nhóm **assertion tầng sự thật**. Mô tả bằng JSON để tránh rule rải rác trong tham số dòng lệnh.

```json
{
  "id": "writer_first_chapter_xianxia",
  "category": "smoke",
  "role": "writer",
  "description": "Xác minh chất lượng chính văn chương đầu tiên của Writer và độ ổn định của toolchain",
  "prompt": "Viết một truyện dài tu tiên, nhân vật chính khởi đầu là tạp dịch ở biên thành, dựa vào trí nhớ bất thường để phá giải hồ sơ cũ của tông môn rồi bị cuốn vào cục trường sinh.",
  "style": "fantasy",
  "max_chapters": 1,
  "target_prompts": ["writer.md"],
  "rubric": "writer_chapter",

  "expect": {
    "phase": "writing",
    "min_completed_chapters": 1,
    "required_checkpoints": ["chapter:1:plan", "chapter:1:draft", "chapter:1:commit"],
    "no_pending": ["pending_commit", "pending_steer"]
  },

  "gate": {
    "max_severity": "warning",
    "max_cost_delta_ratio": 0.3,
    "max_tool_call_delta_ratio": 0.3,
    "stylestat_regression": "warn"
  }
}
```

**Ngữ nghĩa field**:

- `expect`: assertion hợp đồng cấp case, **chỉ khai báo kỳ vọng liên quan mạnh tới case này mà rule chung của diag không bao phủ được** (ví dụ “smoke case này bắt buộc phải sinh ra đúng `chapter:1:commit`”). Các quy tắc chung như “không còn pending sót lại / phase-flow nhất quán / không có lỗ hổng chương” giao cho diag, không lặp lại trong case.
- `category`: tầng đánh giá ∈ `smoke` / `workflow` / `quality` / `longform` / `recovery` / `steering`. Quyết định chạy bộ gate nào và mặc định có bật stylestat/Judge hay không.
- `role`: vai trò được test ∈ `writer` / `architect` / `editor`. Trực giao với `category` — tầng quyết định “kiểm đến độ sâu nào”, vai trò quyết định “kiểm Worker nào”. Tầng Workflow chọn assertion set theo `role`.
- `max_severity`: severity cao nhất được phép của diag Finding. Vượt quá thì hard fail.
- `gate.max_cost_delta_ratio` / `gate.max_tool_call_delta_ratio`: ngưỡng tăng chi phí và tool call của variant so với baseline; mặc định `0.3` nếu bỏ qua, ghi rõ `0` nghĩa là không cho phép tăng, số âm nghĩa là tắt delta gate này.
- `rubric`: bật bảng chấm điểm LLM Judge được version hóa nào. Nếu thiếu thì không chạy Judge.
- `gate.stylestat_regression`: `block` / `warn` / `off`, kiểm soát hồi quy văn phong có chặn hay không (chỉ có hiệu lực với case ≥5 chương).

---

## 6. Phân tầng đánh giá

Mỗi tầng xác định rõ **dùng evaluator đã có nào**, tránh “tầng đánh giá tự viết thêm một lượt phán đoán”.

### 6.1 Smoke (mỗi lần sửa prompt bắt buộc chạy, tập tối thiểu)

Chỉ phán đoán hệ thống còn chạy ổn định hay không, không phán văn bút. 1 chương / giai đoạn lập kế hoạch là có thể phơi bày.

| case | Mục tiêu | Evaluator chính |
|---|---|---|
| `writer_first_chapter` | Writer hoàn thành chương đầu tiên và commit | `expect.required_checkpoints` + diag |
| `architect_short` | Lập kế hoạch truyện ngắn lưu đủ premise/outline/characters/world_rules | Kiểm tra foundation đồng nguồn với diag `MissingSummaries` + `expect` |
| `architect_long` | Lập kế hoạch truyện dài lưu layered_outline/compass, mở rộng arc đầu | diag `OutlineExhausted`/`CompassDrift` + `expect` |
| `editor_review` | Đến điểm thẩm định, Editor lưu review (đủ bảy chiều) | Assertion field `ReviewEntry` |

Chi phí: 1 chương × baseline+variant, cấp giây đến phút, không bật Judge, không chạy stylestat (số chương chưa đủ 5, `Compute` trả nil). CI mặc định chỉ chạy tầng này.

### 6.2 Workflow (xác minh hành vi Agent phù hợp hợp đồng kiến trúc)

**Kỷ luật then chốt: assert hợp đồng, không assert chuỗi tool chính xác.** Kiến trúc đặt cược vào quy trình tự chủ quyết định của LLM (`architecture.md` §2.1), nếu viết chết thứ tự tool thì ở tầng đánh giá lại đưa vào “hardcode cho hành vi LLM” vốn đã bị §10.13 từ chối. Vì vậy ở đây chỉ assert **sự thật tất yếu**:

- Writer: checkpoint `chapter:N:commit` tồn tại; sau commit thì sub-agent kết thúc vòng này (không có đoạn chính văn kéo đuôi quá dài); draft checkpoint đứng trước commit. **Không** assert “bắt buộc phải theo chính xác thứ tự novel_context→read_chapter→plan→draft→check→commit này”.
- Architect: trong kỳ viết, outline chỉ tăng thêm chứ không full overwrite (checkpoint `expand_arc`/`append_volume`, không có dòng `layered_outline` full thứ hai); sau khi mở rộng, flat outline và layered có số chương nhất quán.
- Editor: `ReviewEntry.Verdict` hợp lệ (`accept`/`polish`/`rewrite`); `rewrite`/`polish` bắt buộc sinh ra `affected chapters`; cuối arc có checkpoint `arc_summary`, cuối volume có checkpoint `volume_summary`.
- Engine dispatch: Route chỉ thị phải khớp với Worker thực thi (đọc từ session trace, diag `repeatedErrors` dùng làm vòng lặp dự phòng); phán định ngữ nghĩa đối chiếu `meta/decisions.jsonl`.

Phần lớn những điều này có thể được bao phủ trực tiếp bằng rules diag + assert checkpoint, số ít trường hợp (nội dung theo sau sau commit) cần thêm một kiểm tra trace nhẹ trong collector.

### 6.3 Chất lượng (chỉ chạy sau khi quy trình đã qua, để đánh giá chất lượng nội dung)

Hai chân:

1. **stylestat delta (xác định, tuyến chính)**: chênh lệch chỉ số văn phong giữa variant và baseline. Đây là bằng chứng cứng của regression chất lượng. **Yêu cầu case chạy đủ ≥5 chương** (nếu không `Compute` trả về nil, mục này sẽ được gắn `insufficient_sample`), nên Quality case chỉ có 1 chương không thể lấy được regression văn phong; cần đặt `max_chapters` lên trên 5.
2. **LLM Judge (phụ trợ)**: rubric 7 chiều A/B (xem §8).

Chỉ những case qua §6.1/§6.2 mới được vào Quality — quy trình còn sai thì nói chất lượng cũng vô nghĩa.

### 6.4 Longform & Recovery (thay đổi lớn / nightly)

Không cần chạy mỗi lần. Bao phủ tính ổn định của truyện dài và khả năng khôi phục, chính là sân nhà của rules runtime diag và rules context:

- Viết liên tục 3 chương / 5 chương đầu → diag `GhostCharacter`/`TimelineGaps`/`RelationshipStagnation`/`ChapterGaps` + stylestat trùng lặp xuyên chương.
- Review cuối arc + triển khai arc tiếp theo → `OutlineExhausted`/`StaleForeshadow`/`CompassDrift`.
- Người dùng can thiệp giữa chừng (steering case) → user_rules có ghi vào `meta/user_rules.json` không, có được các chương sau tuân thủ không.
- Khôi phục sau crash: chạy đến draft chương N rồi kill → Resume → diag xác nhận `checkpoints.jsonl` không trùng step, không ghi đè draft đã flush ra đĩa, `pending_commit` cuối cùng về 0.
- Tool call phình to / chi phí bất thường → diag `repeatedErrors`/`stuckStep`/`streamIdleStorm` + usage delta.

---

## 7. Cổng xác định

Cấp độ gate được dẫn trực tiếp từ **Severity của Finding trong diag** + **assert contract của case**, không tự lập thêm hệ phân loại khác.

### 7.1 Hard Fail (chặn merge)

- Process panic / headless trả về error.
- diag sinh ra Finding `SevCritical` (`InvalidPendingRewrites` / `PhaseFlowMismatch` v.v.).
- Assert contract `expect` của case thất bại: thiếu commit checkpoint, phase không đạt kỳ vọng, pending đã khai báo không về 0.
- Số lỗi / số critical Finding của variant nhiều hơn baseline (regression tệ hơn).

### 7.2 Regression (mặc định warning, có chặn hay không do case gate quyết định)

- diag thêm Finding `SevWarning` mới (variant nhiều hơn baseline).
- tool calls / cost / input token / output token tăng vượt ngưỡng của case (mặc định 30%).
- **stylestat regression**: số lần trung bình mỗi chương của pattern câu tăng lên, tỷ lệ câu ngắn ở cuối chương tăng lên, trùng lặp xuyên chương tăng, lẫn lộn tiêu đề xuất hiện — do `gate.stylestat_regression` quyết định warn/block.
- Số từ của chương thấp hơn baseline 60% hoặc cao hơn 180% (cùng nguồn ngưỡng với diag `WordCountAnomaly`).

### 7.3 Quality Gate (phương án dự phòng thủ công)

- LLM Judge chỉ làm phụ trợ và xếp hạng.
- Judge kết luận variant rõ ràng tệ hơn → bắt buộc người đọc mẫu xác nhận thủ công.
- Người đọc mẫu thủ công xác định bị suy giảm → chặn.
- Judge kết luận variant tốt hơn nhưng có hard fail xác định → vẫn chặn.

### 7.4 Điều kiện merge khuyến nghị

Chỉnh prompt hằng ngày: Smoke qua hết + Workflow của role mục tiêu qua hết (Smoke 1 chương không có regression văn phong; nếu đã chạy Quality case ≥5 chương thì stylestat không có regression rõ rệt).
Thay đổi lớn: thêm 2-3 case Quality + 1-2 case Longform + đọc mẫu thủ công.

---

## 8. LLM Judge

Judge là phụ trợ cho chất lượng, bản chất là **dùng rubric đã version hóa (ban đầu suy ra từ 7 chiều của editor.md) để so sánh baseline/variant offline**. Rubric là thước đo cố định, độc lập với tiến hóa online của `editor.md` (lý do xem §2.3), report ghi lại version rubric đã dùng.

### 8.1 Đầu vào (kiểm soát kích thước, tuyệt đối không nhét cả cuốn sách)

- Yêu cầu gốc của người dùng + dàn ý/contract chương hiện tại.
- Văn bản **cùng một chương** của baseline và variant.
- Tóm tắt 1-2 chương gần nhất + tóm tắt trạng thái nhân vật (đọc từ store).
- Phân đoạn stylestat liên quan của chương đó (để Judge thấy các dữ kiện như "câu này đã lặp 7 lần trong toàn sách").

### 8.2 Đầu ra (cấu trúc hóa, khớp 7 chiều)

```json
{
  "scores": {
    "consistency": 8, "character": 7, "pacing": 8, "continuity": 8,
    "foreshadow": 7, "hook": 7, "aesthetic": 6
  },
  "winner": "variant",
  "confidence": "medium",
  "reasons": ["variant đẩy tiến triển hành động tập trung hơn", "baseline nặng phần nhắc lại bối cảnh trước"],
  "risks": ["variant hơi thiếu chuẩn bị động cơ cho vai phụ"]
}
```

- Các chiều nghiêm ngặt bằng 7 mục của `domain.DimensionScore`, mỗi mục 0-10.
- `winner` ∈ baseline/variant/tie; `confidence` ∈ low/medium/high.
- Mỗi mục trong `reasons`/`risks` dài ≤ 80 ký tự, trích dẫn nguyên văn phải ngắn.

### 8.3 Ranh giới

Judge **không thể**: quyết định quy trình có qua hay không, sửa artifact, tự động sửa prompt, làm căn cứ merge duy nhất, sinh trích đoạn nguyên văn dài.
Judge **có thể**: xếp hạng cho người review, đánh dấu suy giảm rõ rệt, tóm tắt khác biệt A/B, bộc lộ tác dụng phụ của thay đổi prompt.

---

## 9. Báo cáo

Mỗi lần thử nghiệm tạo `report.json` (máy đọc, có thể sinh lại markdown) + `report.md` (người đọc) + `artifacts/{case_id}/{baseline,variant}/` (artifact gốc). Khi `--repeat N` thì đường dẫn là `artifacts/{case_id}/rN/{baseline,variant}/`.

### 9.1 Delta chỉ số

Report hiển thị chênh lệch của variant so với baseline, giá trị tuyệt đối và tỷ lệ cùng hiển thị:

```text
completed: baseline=5 variant=5   ← ≥5 chương thì chỉ số văn phong mới có ý nghĩa
tool_calls: baseline=12 variant=16  +4 (+33.3%)
cost_usd: baseline=0.42 variant=0.55  +0.13 (+31.0%)
output_tokens: baseline=8200 variant=9100  +900 (+11.0%)
critical_findings: baseline=0 variant=0
warning_findings: baseline=1 variant=2  +1
stylestat.pattern_top_per_chapter: baseline=3.1 variant=5.4  +2.3   ← regression văn phong
stylestat.ending_short_ratio: baseline=0.42 variant=0.71  +0.29     ← đồng dạng ở cuối chương nặng hơn
```

### 9.2 Tổng hợp Repeat

Khi `--repeat N` thì không chỉ nhìn lần cuối; triển khai hiện tại hiển thị pass rate, số lần hard fail, số lần warning, min/avg/max của cost/tool_calls. Sau khi tích hợp Judge sẽ thêm phân bố winner, để tránh trộn nhiễu của bộ phán mô hình vào report xác định mặc định.

```text
writer_first_chapter_xianxia repeat=3
- pass_rate: 3/3
- cost_usd: avg=0.41 min=0.38 max=0.44
- tool_calls: avg=13 min=12 max=15
- stylestat.pattern_top_per_chapter: avg delta=+0.4 (không có regression rõ rệt)
```

### 9.3 Báo cáo tối thiểu khả dụng

```text
Gate: FAIL

Hard Fail:
- writer_first_chapter_xianxia: thiếu checkpoint chapter:1:commit

Warnings:
- writer_dialogue_density: tool_calls +35%
- writer_anti_ai_tone: ending_short_ratio +0.28 (regression văn phong)

Quality:
- writer_anti_ai_tone: judge ưu tiên variant, confidence=medium

Artifacts:
- workspace/evals/20260629-120000/report.json
```

---

## 10. Cấu trúc thư mục và lệnh

```text
internal/eval/
  case.go        Cấu trúc manifest của Case + tải
  eval.go        Orchestration CLI: single / A/B / repeat
  runner.go      Ghép host driver (dừng theo giới hạn số chương + drain đến Done), override bộ nhớ bằng bundle.OverridePrompt
  collect.go     Chạy diag.Diagnose + stylestat.Compute + usage/tool_calls + assert contract trên thư mục output
  grade.go       Ánh xạ Finding→gate + delta baseline/variant + quyết định gate stylestat
  report.go      report.json + report.md

cmd/ainovel-cli  entrypoint subcommand eval

evals/
  cases/         smoke/ workflow/ quality/ longform/ recovery/ steering/
  rubrics/       writer_chapter.json / architect_outline.json / editor_review.json
  variants/      writer-anti-ai-tone/writer.md v.v. (mỗi thư mục chỉ để prompt cần thay thế)
  reports/       lưu trữ report lịch sử
```

Lệnh:

```bash
# Batch nhiều case (CI mặc định chỉ chạy smoke, không bật judge)
ainovel-cli eval --cases evals/cases/smoke \
  --variant evals/variants/writer-anti-ai-tone \
  --out workspace/evals/writer-anti-ai-tone --ci
```

**Tham số đã triển khai ở bản này**: `--cases` (thư mục hoặc manifest đơn), `--variant` (thư mục prompt biến thể; sau khi truyền vào sẽ tự chạy baseline+variant A/B), `--repeat N` (mỗi case chạy lặp N lần), `--config`, `--out`, `--max-chapters N` (ghi đè mặc định của case), `--timeout` (giới hạn wall-clock cho một case), `--ci` (giảm xuất theo từng sự kiện; exit code khác 0 tức hard fail, không truyền cũng có hiệu lực).

**Đang quy hoạch (chưa triển khai, đừng dùng trên command line, nếu không sẽ báo flag chưa định nghĩa)**: `--judge`/`--no-judge` (Phase 3 LLM Judge). Thay đổi prompt lớn hiện tại có thể dùng A/B xác định + repeat trước:

```bash
# Thay đổi prompt lớn: A/B + repeat để giảm ngẫu nhiên
ainovel-cli eval --cases evals/cases/quality \
  --variant evals/variants/writer-anti-ai-tone \
  --repeat 3 --ci
```

---

## 11. Những việc rõ ràng không làm

Vi phạm tức là đánh giá đã lệch khỏi định vị.

1. **Không sao chép logic chẩn đoán chung của diag ở tầng evaluation** — phán đoán chung (pending còn sót, nhất quán phase/flow, thiếu chapter, vòng lặp chết) đều đi qua `diag`, định nghĩa sự thật chỉ có một bản. Assert contract cấp case (`expect.required_checkpoints` v.v.) được phép đọc trực tiếp `store`/checkpoint API, nhưng chỉ làm **thin assert** — xác minh kỳ vọng cụ thể liên quan sát với case này, tuyệt đối không viết lại toàn bộ rules chung đã có trong diag.
2. **Không tự triển khai lại rules xác định** — diag đã có một bộ rules artifact + runtime. Thiếu rule thì thêm vào diag, tầng evaluation chỉ tiêu thụ.
3. **Không viết lại logic văn phong tiếng Trung của stylestat trong Python** — gọi trực tiếp package Go.
4. **Không để LLM Judge quyết định quy trình có qua hay không** — gate chỉ công nhận bằng chứng xác định.
5. **Không để evaluation can thiệp vào control flow** — bỏ qua Action/Planner của diag, không tự sửa prompt, không rollback, không tiếp tục chạy, không phát hành.
6. **Không assert chuỗi tool call chính xác** — chỉ assert contract (commit có xảy ra, checkpoint có tồn tại), giữ nguyên cược vào "quy trình do LLM điều khiển".
7. **Không đưa database / Web UI / nền tảng đánh giá online vào** — giai đoạn hiện tại cần là regression local có thể tái lập, triển khai thấp, chi phí thấp.
8. **Không copy source rồi biên dịch lại để làm variant** — override trong bộ nhớ `bundle.Prompts`.
9. **Không mock thành công, không nuốt lỗi** — mọi lỗi ở bất kỳ khâu nào đều phải ghi rõ, case chạy vỡ là FAIL.
10. **Case không được thay đổi thường xuyên theo prompt** — case là bộ test ổn định; để variant qua được mà sửa case là gian lận.

---

## 12. Lộ trình triển khai theo giai đoạn

### Phase 1 · Runner + gate xác định (MVP, xác minh giả thuyết trước)

- `internal/eval`: Cấu trúc Case + runner (in-process headless + override `bundle`) + collect (gọi `diag.Diagnose`) + grade (Finding→gate + contract `expect`).
- Đặt 3-4 case trong `evals/cases/smoke/`.
- Report trước tiên xuất `report.json` + markdown tối thiểu.

**Tiêu chí nghiệm thu**: Một lệnh chạy xong smoke; Writer bỏ qua commit, pending còn sót, thiếu checkpoint, phase không khớp **đều phải bị gate chặn lại** (những cái này vốn diag đã kiểm tra được, kiểm nghiệm là harness nối nó đúng chưa).

### Phase 2 · A/B + repeat + regression stylestat (đã triển khai)

- `--variant` tự chạy baseline và variant, output artifacts tách biệt.
- `--repeat N` tổng hợp pass rate, hard fail runs, warning runs, min/avg/max của cost/tool_calls.
- collect thêm `stylestat.Compute`, grade thêm delta văn phong.
- Report hiển thị so sánh baseline-variant của số lần mẫu câu trung bình mỗi chương / tỷ lệ câu ngắn cuối chương / trùng lặp xuyên chương / lẫn lộn tiêu đề.

**Tiêu chí nghiệm thu**: Dùng một case ≥5 chương + một variant "làm nặng tic câu" thì có thể bị regression văn phong gắn warning; case thiếu số chương phải hiển thị rõ `insufficient_sample` chứ không bị kết luận nhầm là qua.

### Phase 3 · LLM Judge

- `evals/rubrics/` + `judge.go`, rubric A/B 7 chiều.
- Judge thất bại (JSON không hợp lệ) → report ghi là thất bại, không ảnh hưởng kết quả xác định.

**Tiêu chí nghiệm thu**: Đầu ra của Judge đi vào json+md, và không làm ô nhiễm gate xác định.

### Phase 4 · Longform & Recovery

- Case 3-5 chương liên tục / review cuối arc / can thiệp người dùng / replay `pending_commit` / áp lực nén ngữ cảnh.
- Tái sử dụng rules context + runtime của diag.

**Tiêu chí nghiệm thu**: Có thể phát hiện timeline trùng, pending còn sót, thiếu tóm tắt cuối arc, vòng lặp tool.

---

## 13. Quy chuẩn bảo trì Case

- **Số lượng tiết chế**: Smoke 3-5, Workflow mỗi role 3-5, Quality 2-4, Longform/Recovery mỗi loại 2-3. Quá nhiều sẽ không ai muốn chạy.
- **Case tốt**: input ngắn và rõ, bao phủ rủi ro thực tế, lộ lỗi trong ít chương, không phụ thuộc vào câu cố định do model sinh, không viết sở thích phong cách quá chi tiết.
- **Case kém**: input quá dài, nhiều mục tiêu cùng lúc, phải chạy hàng chục chương mới phán đoán được, chỉ có thể dựa vào cảm nhận chủ quan.
- **Đặt tên Variant**: `writer-anti-ai-tone` / `architect-rolling-outline` / `editor-strict-review`, mỗi thư mục chỉ để prompt cần thay thế.

---

## 14. Rủi ro và ranh giới

- **Tính ngẫu nhiên của model**: cùng prompt chạy nhiều lần cũng sẽ khác. Thay đổi quan trọng thì dùng `--repeat 3` để xem xu hướng.
- **Chi phí**: Judge và longform đốt tiền. Mặc định local chỉ chạy **smoke** (1 chương × baseline+variant, gate xác định diag, không bật Judge, không chạy stylestat); **stylestat chỉ bật ở Quality/Longform ≥5 chương** (smoke số chương không đủ, `Compute` trả nil, report gắn `insufficient_sample`); suite đầy đủ để dành cho thay đổi lớn.
- **Sai lệch của Judge**: Judge cũng là model, có thể thích văn bản giải thích gọn gàng, chưa chắc tương đương tiểu thuyết hay — vì vậy chỉ làm phụ trợ, stylestat là tuyến xác định chính.
- **Quá mức hóa chỉ số**: số từ / số lần tool / chi phí / thống kê văn phong đều là tín hiệu chứ không phải mục tiêu. Việc số liệu stylestat có thành bệnh hay không phải do con người xét theo thể loại, **ngưỡng không được cố định** (giống editor.md).
- **Không làm rollback tự động online**: đây là công cụ regression offline, không chịu trách nhiệm tự sửa prompt / phát hành online.

---

## 15. Tổng kết

Giá trị của hệ thống đánh giá này không phải là tự động phán đoán chất lượng văn học, mà là biến thay đổi prompt từ "dựa vào cảm giác" thành "có regression, có bằng chứng, có đọc mẫu thủ công".

Khác biệt cốt lõi so với thiết kế bản trước chỉ có một câu: **Evaluator đã ở trong codebase rồi.** `diag` là bộ chẩn đoán sự thật xác định, `stylestat` là bộ phát hiện regression văn phong toàn sách, 7 chiều của `ReviewEntry` là rubric gốc. Việc hệ thống đánh giá cần làm là một lớp Go harness mỏng — điều khiển hàng loạt, thu thập, ánh xạ Finding và thống kê thành gate, tổng hợp report — chứ không phải viết lại các phán đoán sự thật này bằng một ngôn ngữ khác.

Một bộ định nghĩa sự thật, không bao giờ trôi. Đó chính là kỷ luật xuyên suốt của dự án này từ kiến trúc đến đánh giá: **harness tối thiểu, tái sử dụng tối đa, sự thật xác định thuộc về code, phán quyết thuộc về LLM và con người.**

---

## 16. Tham khảo

Cấu trúc phổ biến của LLM eval trong ngành (dataset / experiment / scorer / trace / regression gate) là nguồn cảm hứng cho thiết kế này, nhưng **cố ý không sao chép nguyên xi** — "scorer" của dự án này là `diag`/`stylestat` hiện có, "trace" là tầng sự kiện checkpoint/session hiện có, "dataset" là case gắn với các assertion của tầng sự kiện.

- OpenAI Evals · https://developers.openai.com/api/docs/guides/evals (chú ý: nền tảng Evals được quản lý của họ đã công bố lộ trình ngừng hoạt động, chỉ tham khảo **ý tưởng** về kiểm thử có cấu trúc/chấm điểm tự động/hiệu chuẩn thủ công của nó, không dùng làm phụ thuộc trong tương lai)
- Braintrust · https://www.braintrust.dev/foundations/what-is-an-eval
- LangSmith · https://docs.langchain.com/langsmith/evaluation-concepts