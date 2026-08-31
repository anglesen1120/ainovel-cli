# Bản đồ nội dung assets

Trước khi thêm vào hệ thống một đoạn văn, một tài liệu hoặc một quy tắc, hãy xác định đúng nơi chứa và cách nối dây.

| Thư mục | Chứa gì | Bên sử dụng | Cách nối dây |
|---|---|---|---|
| `prompts/` | Worker system prompt (writer / editor / architect x2), prompt điều phối Arbiter và prompt tác vụ một lần (import / simulation / revision) | `agents/build.go`, `internal/arbiter`, imp / sim / revision runner | Các field Prompts trong `load.go`. Lưu ý: simulation_guidance được `load.go` chèn khi nạp, nên không thấy trong file md |
| `references/` | Kiến thức viết không phụ thuộc thể loại. Không đi vào system prompt trực tiếp; `novel_context` cắt theo vai trò / chương rồi chèn vào `reference_pack` | writer / editor / architect | **Ba điểm nối**: thêm field trong `tools.References` + `load.go` đọc bằng loadReferences + `novel_context.go` chèn qua writerReferences / architectReferences. Chỉ đặt file vào thư mục không tự động nạp |
| `references/genres/<style>/` | Kiến thức riêng theo thể loại (style-references / arc-templates) | Như trên, nạp khi `style != default` | `load.go` loadReferences |
| `rules/` | Thư mục quy tắc nội bộ cũ đã bỏ; baseline cơ học đã chuyển vào code, quy tắc người dùng đến từ snapshot ngôn ngữ tự nhiên `~/.ainovel/rules/*.md` / `./.ainovel/rules/*.md` | `userrules.Service` chuẩn hóa thành `meta/user_rules.json`; `novel_context` chèn; `commit_chapter` kiểm tra | Baseline nội bộ xem `internal/rules/snapshot.go` `SystemDefaults()`; file `.md` người dùng không cần format hay YAML, được snapshot chuẩn hóa tiêu thụ |
| `styles/<style>.md` | Chỉ dẫn phong cách viết theo thể loại | Ghép vào system prompt của **writer** (`agents/build.go`) | Tên file chính là giá trị `config.style`. Đây cùng một khái niệm thể loại với `references/genres/<style>/`: styles là chỉ dẫn giọng văn, references là tài liệu kiến thức |

## Năm câu hỏi để đặt nội dung mới

1. Luồng này cần được **bảo đảm**? → Không viết prompt; viết ràng buộc code (StopAfterTools / tool guard / Flow Router).
2. Đây là tiêu chí điều phối? → Luồng tra bảng đặt trong `internal/flow/router.go`; phán đoán ngữ nghĩa đặt trong `prompts/arbiter-*.md`.
3. Đây là chuẩn thẩm mỹ / thực thi của một vai trò? → `prompts/<role>.md`.
4. Đây là quy tắc mặc định có thể liệt kê cơ học (từ cấm / ngưỡng)? → `internal/rules/snapshot.go` `SystemDefaults()`; quy tắc tùy chỉnh của người dùng đặt trong `.ainovel/rules/*.md` và được snapshot chuẩn hóa tiêu thụ (mong muốn số chữ / độ dài là ràng buộc mềm trong preferences, không là quy tắc cơ học).
5. Đây là tài liệu kiến thức viết? → `references/` (nhớ ba điểm nối).

## Bảo đảm nhất quán

Các đường dẫn envelope mà prompt nhắc tới (như `working_memory.*`) phải khớp với `novel_context`. Hình dạng tham số tool chỉ định nghĩa trong Tool Schema; prompt chỉ bổ sung ngữ nghĩa nghiệp vụ mà Schema không diễn đạt được, không sao chép danh sách tham số JSON hay ví dụ hình dạng.

Prompt có thể mô tả cách một Worker thực thi, nhưng định tuyến toàn cục, chuyển trạng thái và logic khôi phục phải lấy code làm chuẩn. Bước nào xác định được bằng dữ kiện Store thì đặt vào Router/Tool; chỉ giữ cho model những phán đoán cần hiểu nội dung tiểu thuyết hoặc ý định người dùng.
