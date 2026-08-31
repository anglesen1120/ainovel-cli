#!/bin/sh
# Tập lệnh cài đặt một lần cho ainovel-cli
#
#   curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/voocel/ainovel-cli/v1.2.3/scripts/install.sh | sh -s -- v1.2.3
#
# Thư mục cài đặt tùy chỉnh: AINOVEL_INSTALL_DIR=~/.local/bin curl -fsSL ... | sh
# Chỉ định phiên bản: AINOVEL_VERSION=v1.2.3 curl -fsSL ... | sh
set -e

REPO="voocel/ainovel-cli"
BIN="ainovel-cli"
DEST="${AINOVEL_INSTALL_DIR:-/usr/local/bin}"
VERSION="${AINOVEL_VERSION:-${1:-latest}}"

for cmd in curl tar; do
	command -v "$cmd" >/dev/null 2>&1 || { echo "Cần $cmd; hãy cài đặt rồi thử lại"; exit 1; }
done

if command -v sha256sum >/dev/null 2>&1; then
	sha256_file() { sha256sum "$1" | awk '{print tolower($1)}'; }
elif command -v shasum >/dev/null 2>&1; then
	sha256_file() { shasum -a 256 "$1" | awk '{print tolower($1)}'; }
else
	echo "Cần sha256sum hoặc shasum; hãy cài đặt rồi thử lại"
	exit 1
fi

curl_https() {
	curl -fsSL --proto '=https' --proto-redir '=https' --tlsv1.2 "$@"
}

case "$(uname -s)" in
	Darwin) OS="Darwin" ;;
	Linux)  OS="Linux" ;;
	*) echo "Hệ điều hành không được hỗ trợ: $(uname -s); người dùng Windows hãy tải thủ công tại https://github.com/$REPO/releases"; exit 1 ;;
esac

case "$(uname -m)" in
	x86_64|amd64)  ARCH="x86_64" ;;
	arm64|aarch64) ARCH="arm64" ;;
	*) echo "Kiến trúc không được hỗ trợ: $(uname -m)"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ] || [ -z "$VERSION" ]; then
	API="https://api.github.com/repos/$REPO/releases/latest"
	echo "Đang tìm phiên bản mới nhất..."
else
	case "$VERSION" in
		v*) TAG="$VERSION" ;;
		*) TAG="v$VERSION" ;;
	esac
	API="https://api.github.com/repos/$REPO/releases/tags/$TAG"
	echo "Đang tìm phiên bản $TAG..."
fi

RELEASE=$(curl_https "$API")
TAG=$(printf '%s\n' "$RELEASE" | grep '"tag_name"' | head -1 | cut -d '"' -f 4)
[ -n "$TAG" ] || { echo "Bản phát hành không có tag_name; từ chối cài đặt"; exit 1; }

RELEASE_VERSION=${TAG#v}
ASSET="${BIN}_${RELEASE_VERSION}_${OS}_${ARCH}.tar.gz"
CHECKSUMS="${BIN}_checksums.txt"
BASE_URL="https://github.com/$REPO/releases/download/$TAG"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Đang tải $ASSET"
curl_https -o "$TMP/$ASSET" "$BASE_URL/$ASSET"
curl_https -o "$TMP/$CHECKSUMS" "$BASE_URL/$CHECKSUMS"

EXPECTED=$(awk -v asset="$ASSET" '
	NF == 2 {
		name = $2
		sub(/^\*/, "", name)
		if (name == asset) {
			count++
			sum = tolower($1)
		}
	}
	END {
		if (count != 1) exit 1
		print sum
	}
' "$TMP/$CHECKSUMS") || { echo "Không tìm thấy duy nhất $ASSET trong danh sách checksum; từ chối cài đặt"; exit 1; }

case "$EXPECTED" in
	*[!0-9a-f]*|'') echo "Định dạng SHA256 của $ASSET không hợp lệ; từ chối cài đặt"; exit 1 ;;
esac
[ "${#EXPECTED}" -eq 64 ] || { echo "Độ dài SHA256 của $ASSET không hợp lệ; từ chối cài đặt"; exit 1; }

ACTUAL=$(sha256_file "$TMP/$ASSET")
[ "$ACTUAL" = "$EXPECTED" ] || {
	echo "$ASSET: xác minh SHA256 thất bại; từ chối cài đặt"
	exit 1
}
echo "Xác minh SHA256 thành công"

CONTENTS=$(tar -tzf "$TMP/$ASSET") || { echo "Không thể đọc gói cài đặt; từ chối cài đặt"; exit 1; }
BIN_COUNT=$(printf '%s\n' "$CONTENTS" | awk -v bin="$BIN" '$0 == bin { count++ } END { print count + 0 }')
[ "$BIN_COUNT" -eq 1 ] || { echo "Không tìm thấy duy nhất $BIN trong gói cài đặt; từ chối cài đặt"; exit 1; }

mkdir "$TMP/extract"
tar -xOzf "$TMP/$ASSET" "$BIN" > "$TMP/extract/$BIN" || {
	echo "Không thể trích xuất $BIN khỏi gói cài đặt; từ chối cài đặt"
	exit 1
}
[ -s "$TMP/extract/$BIN" ] || { echo "$BIN trong gói cài đặt trống; từ chối cài đặt"; exit 1; }
chmod 0755 "$TMP/extract/$BIN"

echo "Đang cài đặt vào $DEST"
[ -d "$DEST" ] || mkdir -p "$DEST" 2>/dev/null || sudo mkdir -p "$DEST"
if [ -w "$DEST" ]; then
	mv "$TMP/extract/$BIN" "$DEST/$BIN"
else
	echo "Cần quyền quản trị để ghi vào $DEST"
	sudo mv "$TMP/extract/$BIN" "$DEST/$BIN"
fi

echo "✓ Cài đặt hoàn tất: $DEST/$BIN"
[ -n "$TAG" ] && echo "Phiên bản: $TAG"
command -v "$BIN" >/dev/null 2>&1 || echo "Gợi ý: $DEST chưa có trong PATH; hãy thêm nó vào PATH"
echo "Chạy $BIN để bắt đầu sử dụng"
