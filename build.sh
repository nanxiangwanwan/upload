#!/bin/sh
set -eu

VERSION="${VERSION:-1.2.0}"
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

echo "done: v${VERSION}"
