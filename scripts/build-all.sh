#!/bin/bash
# 多架构交叉编译脚本
set -e
cd "$(dirname "$0")/.."

# go:embed 依赖 internal/adminweb/dist；前端源码有更新但未重建时提示，避免打出旧界面
if [ ! -f internal/adminweb/dist/index.html ]; then
  echo "错误：internal/adminweb/dist 缺失，请先执行 make web 构建前端" >&2
  exit 1
fi
if [ -n "$(find web/src -type f -newer internal/adminweb/dist/index.html -print -quit 2>/dev/null)" ]; then
  echo "警告：web/src 比 internal/adminweb/dist 新，建议先执行 make web 重建前端" >&2
fi

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
