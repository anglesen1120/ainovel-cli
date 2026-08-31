# Mẫu lập kế hoạch dàn ý

Mẫu này không nhằm ép mọi tác phẩm vào cùng một độ dài cố định, mà là giúp xác định trước cấp độ của tác phẩm, rồi chọn độ hạt của dàn ý cho phù hợp.

## Bước đầu tiên: xác định trước cấp độ độ dài của tác phẩm

### Truyện ngắn / Câu chuyện một tập

- Áp dụng cho: một xung đột, một mục tiêu, ít nhân vật, kết thúc tập trung
- Độ dài tham khảo: 8-25 chương
- Định dạng đề xuất: `outline` phẳng

### Trung truyện / Câu chuyện nhiều giai đoạn

- Áp dụng cho: có nâng cấp theo giai đoạn, nhiều tuyến phụ, quan hệ nhân vật sẽ thay đổi
- Độ dài tham khảo: 25-60 chương
- Định dạng đề xuất: `outline` phẳng hoặc phân tầng nhẹ

### Truyện dài đăng nhiều kỳ / Câu chuyện kiểu web novel

- Áp dụng cho: đề tài vốn có không gian nâng cấp liên tục, lực căng quan hệ dài hạn, nhiều mục tiêu theo giai đoạn, thế giới có thể mở rộng, bí ẩn dài hạn hoặc tuyến trưởng thành dài hạn
- Độ dài tham khảo: 80-200+ chương
- Định dạng đề xuất: `layered_outline` phân tầng

## Bước thứ hai: xác định có bắt buộc dùng dàn ý phân tầng hay không

Chỉ cần thỏa mãn bất kỳ 2 điều dưới đây thì ưu tiên dùng `layered_outline`:

- Thế giới quan cần được triển khai dần, chứ không phải kể xong một lần
- Sự trưởng thành của nhân vật chính không phải một bước nhảy, mà là nhiều giai đoạn nâng cấp
- Quan hệ nhân vật sẽ thay đổi liên tục qua nhiều giai đoạn
- Ở trung kỳ và hậu kỳ tồn tại các loại mâu thuẫn chính khác nhau
- Cần nhiều lần chuyển đổi bản đồ / thế lực / thân phận / mục tiêu
- Đề tài rõ ràng giống tiểu thuyết thương mại kiểu đăng dài kỳ hơn là truyện một tập

## Bước thứ ba: khi là truyện dài thì đừng trực tiếp làm “bảng sự kiện chương truyện toàn bộ sách”

Trình tự lập kế hoạch cho truyện dài nên là:

1. Điểm bán hàng và sự khác biệt của tác phẩm
2. Động cơ kể chuyện dài hạn
3. Chủ đề và nâng cấp theo từng cuốn
4. Mục tiêu theo cung truyện và bước ngoặt giai đoạn
5. Sự kiện và móc câu ở cấp chương

Cách làm sai:

- Viết tóm tắt 20 chương trước, rồi cưỡng ép kéo dài
- Mỗi cuốn đều lặp lại “gặp địch - mạnh lên - đổi bản đồ”
- Chỉ có nâng cấp tuyến chính, không có nâng cấp quan hệ
- Tiêu xài hết mọi bí mật lớn ở giai đoạn đầu, khiến trung hậu kỳ chỉ còn lặp mô-típ

## Mẫu dàn ý phẳng (ngắn / trung)

```json
[
  {
    "chapter": 1,
    "title": "Tiêu đề chương",
    "core_event": "Sự kiện cốt lõi của chương này",
    "hook": "Móc câu cuối chương",
    "scenes": ["Cảnh 1", "Cảnh 2", "Cảnh 3"]
  }
]
```

## Mẫu dàn ý phân tầng (truyện dài - triển khai xoay vòng hai tầng cuốn và cung truyện)

Kế hoạch ban đầu áp dụng xoay vòng hai tầng: 2 cuốn đầu có khung cung truyện, các cuốn còn lại là cuốn khung; cung truyện đầu tiên có chi tiết chương.

```json
[
  {
    "index": 1,
    "title": "Tiêu đề cuốn thứ nhất",
    "theme": "Xung đột/chủ đề cốt lõi mới được thêm vào ở cuốn này",
    "arcs": [
      {
        "index": 1,
        "title": "Cung truyện thứ nhất (đã triển khai)",
        "goal": "Mục tiêu cục bộ, trở lực và bước ngoặt",
        "chapters": [
          {"chapter": 1, "title": "Tiêu đề chương", "core_event": "Sự kiện cốt lõi", "hook": "Móc câu cuối chương", "scenes": ["Cảnh 1", "Cảnh 2"]}
        ]
      },
      {
        "index": 2,
        "title": "Cung truyện thứ hai (cung khung)",
        "goal": "Khái quát mục tiêu của cung truyện này",
        "estimated_chapters": 12,
        "chapters": []
      }
    ]
  },
  {
    "index": 2,
    "title": "Tiêu đề cuốn thứ hai",
    "theme": "Chủ đề của cuốn thứ hai",
    "arcs": [
      {"index": 1, "title": "Tiêu đề cung truyện", "goal": "Mục tiêu cung truyện", "estimated_chapters": 15, "chapters": []},
      {"index": 2, "title": "Tiêu đề cung truyện", "goal": "Mục tiêu cung truyện", "estimated_chapters": 10, "chapters": []}
    ]
  },
  {
    "index": 3,
    "title": "Tiêu đề cuốn thứ ba (cuốn khung)",
    "theme": "Định hướng chủ đề của cuốn thứ ba",
    "estimated_chapters": 60,
    "arcs": []
  }
]
```

- Triển khai ở cấp cung truyện: khi tiến độ viết đi tới cung truyện khung, Architect sẽ triển khai chi tiết chương của cung đó
- Triển khai ở cấp cuốn: khi tiến độ viết đi tới cuốn khung, Architect sẽ triển khai cấu trúc cung truyện của cuốn đó + chương của cung đầu tiên

## Danh sách kiểm tra cấp cuốn cho truyện dài

Mỗi cuốn đều phải trả lời:

- Cuốn này bổ sung thông tin thế giới nào?
- Cuốn này nâng cấp mâu thuẫn cốt lõi nào?
- Cuốn này khiến nhân vật chính 얻 được gì, mất gì?
- Cuốn này thay đổi quan hệ giữa các nhân vật chính như thế nào?
- Sau khi cuốn này kết thúc, vì sao câu chuyện nhất định phải bước sang cuốn tiếp theo?

## Danh sách kiểm tra cấp cung truyện cho truyện dài

Mỗi cung truyện đều phải trả lời:

- Mục tiêu rõ ràng của cung truyện này là gì?
- Trở lực đến từ ai, quy tắc gì, cái giá nào?
- Bước ngoặt là gì?
- Sau khi cung truyện này kết thúc, những trạng thái nào thay đổi không thể đảo ngược?

## Danh sách kiểm tra cấp chương

- Mỗi chương phải phục vụ mục tiêu của cung truyện mà nó thuộc về
- Mỗi chương phải chứa một sự đẩy tiến triển không thể xóa bỏ
- Móc câu cần đa dạng, đừng chỉ dựa vào một kiểu “phát hiện bí mật”
- Các chương đầu không được chỉ là “giới thiệu thế giới”, mà phải đồng thời đẩy tiến nhân vật và xung đột