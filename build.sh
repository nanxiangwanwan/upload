#!/bin/sh
set -eu

VERSION="${VERSION:-1.4.0}"
rm -rf dist
mkdir -p dist

build() {
  goos="$1"
  goarch="$2"
  out="dist/upload-${goos}-${goarch}"
  echo "building ${out}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$out" ./cmd/upload
}

build linux amd64
build linux arm64
build darwin amd64
build darwin arm64
build windows amd64
mv dist/upload-windows-amd64 dist/upload-windows-amd64.exe

(
  cd dist
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum upload-* > SHA256SUMS
  else
    shasum -a 256 upload-* > SHA256SUMS
  fi
)

echo "done: v${VERSION}"
