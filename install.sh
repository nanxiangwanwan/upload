#!/bin/sh
set -eu

BASE_URL="https://gitee.com/cocosnodejs/upload/raw/master/dist"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)
    FILE="upload-linux-amd64"
    ;;
  aarch64|arm64)
    FILE="upload-linux-arm64"
    ;;
  *)
    echo "不支持的架构 / Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

URL="${BASE_URL}/${FILE}"
TMP="${TMPDIR:-/tmp}/upload.$$"

echo "正在下载 / Downloading:"
echo "$URL"

if command -v curl >/dev/null 2>&1; then
  curl -fL "$URL" -o "$TMP"
elif command -v wget >/dev/null 2>&1; then
  wget -O "$TMP" "$URL"
else
  echo "需要 curl 或 wget / curl or wget is required" >&2
  exit 1
fi

chmod +x "$TMP"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP" "$INSTALL_DIR/upload"
elif command -v sudo >/dev/null 2>&1; then
  sudo mv "$TMP" "$INSTALL_DIR/upload"
else
  echo "没有权限写入 / No permission to write: $INSTALL_DIR" >&2
  rm -f "$TMP"
  exit 1
fi

echo
echo "安装完成 / Installed successfully: $INSTALL_DIR/upload"
"$INSTALL_DIR/upload" version
