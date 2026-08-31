# Sổ tay quan sát

Khi chạy tiểu thuyết dài, làm sao biết các cơ chế có thật sự đang hoạt động hay không?

Tài liệu này không sao chép nguyên xi các quy tắc diag, mà hướng đến **vận hành thực tế**: bạn đã chạy đến chương N, vậy nên mở file nào, xem trường nào, và phán đoán thế nào là khỏe mạnh hay bất thường.

---

## 1. Quy trình rà soát chung

```
1. /diag                       # Chẩn đoán tự động, xem phần Kết quả
2. cd output/{novel}/meta/     # cat trực tiếp các artifact then chốt
3. tail decisions.jsonl                # Xem các phán quyết Arbiter gần đây
4. ls -lt sessions/agents/             # Định vị phiên Worker gần nhất rồi tail
```

Những sự thật mà `/diag` không bao phủ được (bao gồm các mục "chờ bổ sung chẩn đoán" được liệt kê trong tài liệu này) cần kiểm tra thủ công ở bước 2-4.

### Báo issue: xuất chẩn đoán đã khử nhạy cảm

Mỗi lần `/diag` sẽ ghi thêm `output/{novel}/meta/diag-export.md`——một bản chẩn đoán **đã khử nhạy cảm** (chính văn tiểu thuyết / prompt / suy nghĩ đã bị loại bỏ, chỉ giữ lại khung hành vi: tên công cụ, chuỗi lỗi, số lần lặp, phase/flow, step bị kẹt, phân loại lỗi log). Khi gặp vấn đề kiểu vòng lặp chết / gián đoạn, chỉ cần dán file này vào GitHub issue, maintainer có thể dựa vào đó để định vị, không cần dữ liệu `output/` của người dùng.

---

## 2. Bảng tra nhanh artifact then chốt

Sắp xếp theo "đường rà soát thường gặp nhất khi có vấn đề":

| Artifact | Đường dẫn | Xem gì | Khỏe mạnh | Không khỏe |
|---|---|---|---|---|
| Tiến độ | `meta/progress.json` | `phase` / `flow` / `completed_chapters` | phase tiến đơn điệu, flow nằm trong tập hợp hợp lệ | phase lùi / flow kẹt ở một trạng thái |
| La bàn | `meta/compass.json` | Chênh lệch giữa `last_updated` và chương mới nhất | gap < 15 chương | gap > 15 chương (trúng CompassDrift) |
| Sổ nhân vật phụ | `meta/cast_ledger.json` | Số mục / tỷ lệ điền brief_role / tính nhất quán tên | Xem §4 | Xem §4 |
| Sổ phục bút | `meta/foreshadow.json` | Số chương đình trệ dài nhất của `status="planted"` | < số chương/3 | > số chương/3 (trúng StaleForeshadow) |
| Đại cương | `meta/layered_outline.json` | Số chương chưa viết còn lại trong quyển hiện tại | Đã mở rộng trước 1-2 chương | Viết đến chương hiện tại nhưng chương sau không có outline (OutlineExhausted) |
| Hồ sơ nhân vật | `meta/characters.json` | Có tìm thấy nhân vật core/important trong tóm tắt N chương gần đây không | Đều tìm thấy | Vắng mặt (trúng GhostCharacter) |
| Checkpoint | `meta/checkpoints.jsonl` | `step` của dòng gần nhất có khớp progress không | Nhất quán | Không nhất quán (phục hồi sau crash chưa tự lành) |
| Audit phán quyết | `meta/decisions.jsonl` | facts/decision của vài phán quyết gần nhất | Phân loại chính xác, hành động hợp lý | Can thiệp cùng loại liên tục bị phán quyết thất bại |

---

## 3. Quan sát la bàn (compass)

**Thời gian sửa**: 2026-05-08（commit `fix: update_compass công cụ tự điền last_updated`）

### Xem gì

```bash
cat output/{novel}/meta/compass.json
```

Ý nghĩa trường:
- `ending_direction`: hướng kết cục (nên nhất quán với đoạn "hướng kết cục" trong `premise.md`)
- `open_threads`: các tuyến dài đang hoạt động (architect thêm/xóa ở ranh giới mỗi quyển)
- `estimated_scale`: quy mô ước tính (ví dụ "4-6 quyển", cập nhật ở ranh giới mỗi quyển)
- `last_updated`: **công cụ tự động điền** thành số chương lớn nhất đã hoàn thành tại thời điểm cập nhật (không còn phụ thuộc LLM tự điền)

### Phán đoán mức khỏe mạnh

| Tín hiệu | Phán đoán |
|---|---|
| `last_updated` nằm trong phạm vi `[latest-15, latest]` | Khỏe mạnh |
| `last_updated` tụt sau latest quá 15 chương | architect không cập nhật ở ranh giới arc/quyển——kiểm tra prompt architect-long.md |
| `last_updated == 0` | **Dữ liệu bẩn trước lần sửa này**, lần update_compass tiếp theo sẽ tự lành |
| `ending_direction` không khớp đoạn "hướng kết cục" trong premise.md | architect đã âm thầm sửa ý định người dùng——ghi lại, quyết định có cần đóng băng trường hay không (chủ đề thiết kế, xem todo.md) |

### Cách xác minh sửa lỗi có hiệu lực

So sánh trước và sau khi chạy truyện dài:
- **Trước khi sửa**: sau khi chạy 30+ chương, `compass.last_updated` nhiều khả năng là `0` hoặc một số chương rất sớm
- **Sau khi sửa**: mỗi lần architect gọi `update_compass`, `last_updated` đều được tầng công cụ ghi đè thành latest hiện tại

---

## 4. Quan sát sổ nhân vật phụ (cast_ledger)

**Tính năng hoàn tất**: 2026-05-08（commit `feat: thêm sổ vai phụ tự động theo dõi vai phụ`）

### Xem gì

```bash
cat output/{novel}/meta/cast_ledger.json | jq 'length'                     # Tổng số mục
cat output/{novel}/meta/cast_ledger.json | jq '[.[] | select(.brief_role == "" or .brief_role == null)] | length'  # Số mục thiếu brief_role
cat output/{novel}/meta/cast_ledger.json | jq '[.[] | select(.appearance_count >= 3)] | length'   # Số nhân vật xuất hiện thường xuyên (≥3 lần)
cat output/{novel}/meta/cast_ledger.json | jq 'sort_by(-.appearance_count) | .[:10]'  # 10 nhân vật xuất hiện nhiều nhất
```

### Phán đoán mức khỏe mạnh

| Chiều | Khỏe mạnh | Bất thường | Ứng phó |
|---|---|---|---|
| **Số mục vs số chương đã hoàn thành** | Số mục ledger ≈ số chương đã hoàn thành × 0.3-0.6 | > số chương × 0.8 (nhân vật lướt qua bị ghi nhầm vào sổ) | Kiểm tra đoạn `cast_intros` trong writer.md có đủ rõ không |
| **Tỷ lệ điền brief_role** | Thiếu < 30% | Thiếu > 50% | Writer bỏ sót nghiêm trọng——prompt dẫn dắt chưa đủ |
| **Độ tương tự tên trùng** | Không có nghi vấn một người nhiều tên | Đồng thời xuất hiện "Ly X" / "ông Lý" / "chưởng quầy X" | LLM trôi tên——thêm ràng buộc "dùng tên nhất quán" vào prompt hoặc thêm công cụ steer hợp nhất của người dùng |
| **Nhân vật xuất hiện thường xuyên** | Ít mục có `appearance_count >= 5` | Nhiều mục xuất hiện tần suất cao xuyên arc | Nên cân nhắc thăng cấp vào hồ sơ core (kênh thăng cấp giai đoạn 3) |
| **Recall có được tiêu thụ không** | Khi Writer viết nhân vật cũ, trường characters của commit_chapter chứa tên đã có trong ledger | Writer phát minh lặp lại cùng một tên (xuất hiện "ông Chu A" và "ông Chu B") | recent_cast recall chưa được tiêu thụ——kiểm tra đoạn "tính liên tục nhân vật phụ" trong writer.md |

### Xác minh luồng dữ liệu (end-to-end)

Sau khi chạy 5 chương:
1. `cat meta/cast_ledger.json` nên không rỗng (trừ khi mỗi chương đều chỉ dùng nhân vật core)
2. Nếu Writer giới thiệu "ông Chu" ở chương 1:
   - Trong `cast_ledger` nên có mục `ông Chu`, `appearance_count=1`
3. Nếu chương 5 lại viết về ông Chu:
   - `ông Chu.appearance_count=2`, `last_seen_chapter=5`
4. Trong `meta/sessions/agents/writer-*.jsonl`, giá trị trả về novel_context của chương 5 nên thấy ông Chu trong `episodic_memory.recent_cast`
5. Nếu bước trên thấy rồi nhưng Writer không tiêu thụ (ông Chu được viết ra không khớp chương 1)——đây là vấn đề prompt

### Hiện chưa có chẩn đoán tự động (nhưng snapshot đã load)

`diag.Snapshot.CastLedger` đã được đọc trong `Load()`, có thể được rule tiêu thụ trực tiếp——nhưng hiện chưa viết rule nào. Việc xác minh vẫn dựa vào các lệnh `jq` ở trên để kiểm tra thủ công.

Nếu sau này muốn bổ sung rule chẩn đoán (ứng viên):
- `CastBriefRoleMissing`: cảnh báo khi tỷ lệ thiếu > 50%
- `CastBloat`: cảnh báo khi số mục > số chương × 0.8
- `CastPromotionCandidate`: appearance_count ≥ 5 và xuyên arc → đề xuất thăng cấp

Đừng chốt ngưỡng ngay bây giờ——đợi dữ liệu truyện dài xuất hiện, xem phân bố thật rồi quyết định. Bản thân mã rule chỉ cần 30-50 dòng.

---

## 5. Writer có đang làm việc đúng kỳ vọng không

Khi chạy truyện dài, điều đáng quan tâm nhất là **Writer có thật sự hành động theo prompt không**. Quan sát trực tiếp nhất là session log:

```bash
ls output/{novel}/meta/sessions/agents/    # Mỗi sub-agent một file jsonl
tail -50 output/{novel}/meta/sessions/agents/writer-*.jsonl
```

Xem vài hành vi cụ thể:

| Hành vi kỳ vọng | Thể hiện trong jsonl |
|---|---|
| Writer đã xem recent_cast | Trường `episodic_memory.recent_cast` trong giá trị trả về của công cụ novel_context không rỗng |
| Writer đã điền cast_intros trong commit_chapter | Mảng `cast_intros` của tham số tool_call không rỗng (chỉ ở các chương giới thiệu nhân vật mới) |
| Writer đã dùng đề xuất chương liên quan | Số lần gọi `read_chapter` > 1 (mặc định 1 lần, vượt quá nghĩa là đã tra lại) |
| Writer không vi phạm thứ tự công cụ | Chuỗi tool_call nghiêm ngặt `novel_context → read_chapter → plan_chapter → draft_chapter → check_consistency → commit_chapter` |

Nếu trong jsonl thấy Writer nhiều lần gọi novel_context rỗng, hoặc sau commit_chapter lại gọi công cụ khác——tức là prompt chưa kiểm soát được.

---

## 6. Lằn ranh đỏ cho chạy dài

Khi chạy truyện dài 100+ chương, chỉ cần trúng bất kỳ mục nào dưới đây thì nên dừng lại rà soát:

- [ ] CompassDrift trúng và kéo dài 2 arc vẫn chưa được loại bỏ
- [ ] Số mục cast_ledger > số chương đã hoàn thành × 0.8
- [ ] Tỷ lệ điền brief_role trong cast_ledger < 30%
- [ ] Cùng một nhân vật xuất hiện nghi vấn nhiều tên ("ông Lý" / "chưởng quầy Lý" cùng tồn tại)
- [ ] Khi Writer viết chương mới, không đọc nhân vật cũ đã có trong recent_cast (phát minh lặp lại)
- [ ] Trong Worker session xuất hiện liên tiếp ≥ 5 lần gọi novel_context rỗng
- [ ] Sau khi commit bất kỳ chương nào, `meta/checkpoints.jsonl` không có step `commit_chapter` tương ứng

4 mục đầu là mức khỏe mạnh của cơ chế mới lần này; 3 mục sau là độ ổn định của cơ chế hiện có.

---

## 7. Quy chuẩn bảo trì tài liệu

**Khi thêm artifact tầng sự thật mới (tạo một `meta/*.json` / `meta/*.jsonl` mới), hãy đồng bộ:**

1. Thêm một dòng tra nhanh vào §2 của tài liệu này
2. Nếu artifact cần quan sát chuyên biệt (không phải phán đoán đơn giản "tồn tại/không tồn tại"), thêm đoạn chuyên đề §X
3. Nếu muốn chẩn đoán tự động, load trong `internal/diag/snapshot.go::Load`, và thêm rule trong `internal/diag/rules_*.go`

**Đừng:**
- Đừng sao chép toàn bộ rule trong `internal/diag/` vào tài liệu này (đây là tham chiếu rule, không phải sổ tay quan sát)
- Đừng viết rule chẩn đoán cho mọi cơ chế——ngưỡng đoán mò sẽ sai, hãy quan sát trước rồi bổ sung sau