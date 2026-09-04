#!/bin/bash
# 多架构交叉编译脚本
set -e
cd "$(dirname "$0")/.."

VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null || echo dev)}
OUT=dist
LDFLAGS="-s -w -X main.version=$VERSION"
mkdir -p $OUT

build() {
  local goos=$1 goarch=$2 suffix=$3 gomips=$4
  local name="andey-proxy_${VERSION}_${goos}_${suffix}"
  echo "==> $name"
  env GOOS=$goos GOARCH=$goarch GOARM=${GOARM:-} GOMIPS=${gomips} CGO_ENABLED=0 \
    go build -tags "adminweb" -ldflags "$LDFLAGS" -o "$OUT/$name" .
}

build linux amd64 x86_64 ""
build linux arm64 arm64 ""
GOARM=7 build linux arm armv7 ""
build linux mipsle mipsle softfloat
build linux mips mips softfloat

echo "完成，产物在 $OUT/"
