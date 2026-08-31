#!/bin/sh
#
# Tạo ghi chú phát hành được AI tóm tắt từ các commit git.
# Cách dùng: .github/scripts/gen-changelog.sh [previous_tag]
#
# Yêu cầu GEMINI_API_KEY (ưu tiên), ANTHROPIC_API_KEY, hoặc OPENAI_API_KEY.
# Nếu không có API key thì sẽ chuyển sang danh sách commit thô.
#
set -e

PREV_TAG="${1:-$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo "")}"
CURR_TAG="$(git describe --tags --abbrev=0 HEAD 2>/dev/null || echo "HEAD")"

if [ -n "$PREV_TAG" ]; then
    COMMITS=$(git log "${PREV_TAG}..${CURR_TAG}" --pretty=format:"- %s" --no-merges)
    RANGE="${PREV_TAG}..${CURR_TAG}"
else
    COMMITS=$(git log --pretty=format:"- %s" --no-merges -50)
    RANGE="50 commit gần nhất"
fi

if [ -z "$COMMITS" ]; then
    echo "Không tìm thấy commit trong phạm vi ${RANGE}"
    exit 0
fi

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

cat > "$TMPDIR/prompt.txt" <<PROMPT_EOF
Bạn là người viết ghi chú phát hành cho công cụ dòng lệnh Go ainovel-cli (một công cụ tạo tiểu thuyết bằng AI).
Hãy dựa trên các commit Git dưới đây để tạo ghi chú phát hành bằng Markdown, ngắn gọn, rõ ràng và hướng đến người dùng, bằng tiếng Việt tự nhiên.

Quy tắc:
- Trả về bằng tiếng Việt
- Tổ chức nội dung theo các nhóm sau: Tính năng mới, Sửa lỗi, Tối ưu hiệu năng, Tái cấu trúc, Khác; không có nội dung thì không cần xuất nhóm đó
- Mỗi ý viết trên một dòng, giữ ngắn gọn, không bao gồm commit hash hoặc tên tác giả
- Loại bỏ tiền tố conventional commit, ví dụ feat:, fix:, perf:, refactor:, v.v.
- Gộp các commit gần giống hoặc trùng lặp, tránh liệt kê máy móc từng commit
- Dùng cách diễn đạt hướng đến người dùng, làm nổi bật thay đổi thực tế và ảnh hưởng của chúng
- Tập trung vào những thay đổi người dùng có thể cảm nhận được, ví dụ quy trình phát hành, đóng gói nhị phân, hành vi CLI/TUI, quy trình viết, hỗ trợ mô hình và tài liệu
- Chỉ xuất nội dung Markdown, không kèm lời mở đầu, giải thích hoặc tổng kết

Commit (nhật ký ${RANGE}):
${COMMITS}
PROMPT_EOF

# Tạo nội dung JSON bằng jq (đọc từ file để xử lý ký tự đặc biệt).
build_body() { jq -Rs "$1" < "$TMPDIR/prompt.txt" > "$TMPDIR/body.json"; }

# Trích xuất văn bản từ phản hồi JSON (python3 xử lý ký tự điều khiển ổn định).
extract() { python3 -c "import json,sys; d=json.load(open('$TMPDIR/result.json')); print($1)"; }

fallback() {
    echo "## Những thay đổi"
    echo ""
    echo "$COMMITS"
}

# Thử Gemini trước, rồi đến Anthropic, sau đó OpenAI.
if [ -n "$GEMINI_API_KEY" ]; then
    API_URL="${GEMINI_BASE_URL:-https://generativelanguage.googleapis.com}/v1beta/models/gemini-2.5-flash:generateContent?key=${GEMINI_API_KEY}"
    build_body '{contents: [{parts: [{text: .}]}]}'
    if curl -fsSL "$API_URL" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['candidates'][0]['content']['parts'][0]['text']"
    else
        fallback
    fi

elif [ -n "$ANTHROPIC_API_KEY" ]; then
    API_URL="${ANTHROPIC_BASE_URL:-https://api.anthropic.com}/v1/messages"
    build_body '{model: "claude-sonnet-4-5-20250514", max_tokens: 1024, messages: [{role: "user", content: .}]}'
    if curl -fsSL "$API_URL" -H "x-api-key: ${ANTHROPIC_API_KEY}" -H "anthropic-version: 2023-06-01" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['content'][0]['text']"
    else
        fallback
    fi

elif [ -n "$OPENAI_API_KEY" ]; then
    API_URL="${OPENAI_BASE_URL:-https://api.openai.com}/v1/chat/completions"
    build_body '{model: "gpt-4o-mini", messages: [{role: "user", content: .}]}'
    if curl -fsSL "$API_URL" -H "Authorization: Bearer ${OPENAI_API_KEY}" -H "content-type: application/json" -d @"$TMPDIR/body.json" -o "$TMPDIR/result.json"; then
        extract "d['choices'][0]['message']['content']"
    else
        fallback
    fi

else
    fallback
fi