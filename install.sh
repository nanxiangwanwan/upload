#!/bin/sh
set -eu

REPO_OWNER="${REPO_OWNER:-cocosnodejs}"
REPO_NAME="${REPO_NAME:-upload}"
VERSION="${VERSION:-}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch_raw="$(uname -m)"
case "$arch_raw" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "unsupported architecture: $arch_raw" >&2; exit 1 ;;
esac

case "$os" in
  linux|darwin) ;;
  *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac

if [ -z "$VERSION" ]; then
  echo "VERSION is required for release install, example:" >&2
  echo "  curl -fsSL https://gitee.com/${REPO_OWNER}/${REPO_NAME}/raw/master/install.sh | VERSION=v1.0.0 sh" >&2
  exit 1
fi

asset="upload-${os}-${arch}"
url="https://gitee.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${VERSION}/${asset}"
tmp="${TMPDIR:-/tmp}/${asset}.$$"

echo "downloading ${url}"
if command -v curl >/dev/null 2>&1; then
  curl -fL "$url" -o "$tmp"
elif command -v wget >/dev/null 2>&1; then
  wget -O "$tmp" "$url"
else
  echo "curl or wget is required" >&2
  exit 1
fi
chmod +x "$tmp"

if [ -w "$INSTALL_DIR" ]; then
  mv "$tmp" "$INSTALL_DIR/upload"
elif command -v sudo >/dev/null 2>&1; then
  sudo mv "$tmp" "$INSTALL_DIR/upload"
else
  echo "need permission to write $INSTALL_DIR" >&2
  rm -f "$tmp"
  exit 1
fi

echo "installed: $INSTALL_DIR/upload"
"$INSTALL_DIR/upload" version
