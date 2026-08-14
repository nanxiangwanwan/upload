#!/bin/sh

set -e

BASE_URL="https://gitee.com/cocosnodejs/upload/raw/master/dist"

ARCH="$(uname -m)"

case "$ARCH" in
    x86_64|amd64)
        FILE="upload-linux-amd64"
        ;;
    aarch64|arm64)
        FILE="upload-linux-arm64"
        ;;
    *)
        echo "不支持的架构 / Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

URL="${BASE_URL}/${FILE}"

echo "正在下载 / Downloading:"
echo "$URL"

curl -fL "$URL" -o /tmp/upload

chmod +x /tmp/upload

if [ "$(id -u)" -eq 0 ]; then
    mv /tmp/upload /usr/local/bin/upload
else
    sudo mv /tmp/upload /usr/local/bin/upload
fi

echo
echo "安装完成 / Installed successfully"

upload version
