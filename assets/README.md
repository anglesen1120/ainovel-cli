# Bản đồ nội dung assets

Trước khi thêm "một đoạn văn / một tài liệu / một quy tắc" vào hệ thống, hãy xem bảng dưới đây để xác định nó thuộc về đâu, rồi xem cách đấu nối.

| Thư mục | Chứa gì | Ai tiêu thụ | Cách đấu nối |
|---|---|---|---|
| `prompts/` | Worker system prompt (writer / editor / architect×2), prompt đánh giá của Arbiter và prompt tác vụ dùng một lần (import / simulation / revision) | `agents/build.go`, `internal/arbiter`, imp / sim / revision runner | Trường Prompts trong `load.go`. Lưu ý: `simulation_guidance` được chèn khi `load.go` tải, nên không thấy trong file md |
| `references/` | Tài liệu kiến thức viết lách không phụ thuộc chủ đề. Không đưa vào system prompt, mà `novel_context` cắt theo vai trò / chương rồi chèn vào `reference_pack` | writer / editor / architect | **Ba chỗ đấu nối**: thêm trường vào `tools.References` + `load.go` đọc `loadReferences` + `novel_context.go` chèn `writerReferences` / `architectReferences`. Chỉ đặt vào thư mục sẽ không tự động tải |
| `references/genres/<style>/` | Kiến thức chuyên biệt theo chủ đề (style-references / arc-templates) | Như trên, tải khi `style != default` | `load.go` `loadReferences` |
| `rules/` | Thư mục quy tắc tích hợp cũ đã bỏ; baseline cơ học đã chuyển sang code, quy tắc người dùng đến từ snapshot ngôn ngữ tự nhiên của `~/.ainovel/rules/*.md` / `./.ainovel/rules/*.md` | `userrules.Service` chuẩn hóa thành `meta/user_rules.json`; `novel_context` chèn; `commit_chapter` kiểm tra | Xem baseline tích hợp trong `SystemDefaults()` ở `internal/rules/snapshot.go`; `.md` của người dùng không định dạng, không YAML, được chuẩn hóa theo ngôn ngữ tự nhiên |
| `styles/<style>.md` | Chỉ thị phong cách viết theo chủ đề | Ghép vào system prompt của **writer** (`agents/build.go`) | Tên file chính là giá trị của `config.style`. Cùng với `references/genres/<style>/` là hai vật mang của cùng một khái niệm chủ đề: cái trước là chỉ thị phong cách, cái sau là tài liệu kiến thức |

## Phán đoán nội dung mới thuộc về đâu (năm câu hỏi)

1. Quy trình này có bắt buộc phải được **đảm bảo** không? → Không viết prompt, hãy viết ràng buộc bằng code (StopAfterTools / bảo vệ công cụ / Flow Router)
2. Đây có phải là tiêu chí phán đoán không? → Quy trình dạng tra bảng viết vào `internal/flow/router.go`; phán đoán ngữ nghĩa viết vào `prompts/arbiter-*.md`
3. Đây có phải là thẩm mỹ / tiêu chuẩn thực thi của một vai trò nào đó không? → `prompts/<role>.md`
4. Đây có phải là quy tắc mặc định có thể liệt kê cơ học (từ cấm / ngưỡng) không? → `SystemDefaults()` trong `internal/rules/snapshot.go`; quy tắc tùy chỉnh của người dùng viết vào `.ainovel/rules/*.md`, được snapshot chuẩn hóa để tiêu thụ (số chữ/độ dài là ràng buộc mềm về ngữ nghĩa, đi theo preferences, không phải quy tắc cơ học)
5. Đây có phải là tài liệu kiến thức viết lách không? → `references/` (nhớ đấu nối ở ba chỗ)

## Bảo đảm nhất quán

Đường dẫn phong bì mà prompt tham chiếu (`working_memory.*` v.v.) phải nhất quán với `novel_context`. Hình dạng tham số công cụ chỉ được định nghĩa trong Schema của công cụ; prompt chỉ bổ sung ngữ nghĩa nghiệp vụ mà Schema không thể biểu đạt, không sao chép lại danh sách tham số JSON và ví dụ hình dạng nữa.

Prompt có thể mô tả phương pháp thực thi của một Worker riêng lẻ, nhưng định tuyến toàn cục, chuyển dịch trạng thái và logic khôi phục chỉ lấy code làm chuẩn. Những bước có thể xác định từ sự kiện Store thì đưa vào Router/Tool; chỉ những phán đoán cần hiểu nội dung tiểu thuyết hoặc ý định người dùng mới để lại cho mô hình.