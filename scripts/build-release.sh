#!/bin/bash
# andey-Proxy 发布包构建：交叉编译 + 生成 .ipk 与 .run（免 OpenWrt SDK）
set -e
cd "$(dirname "$0")/.."
export PATH="/opt/homebrew/bin:$PATH"

VERSION=${VERSION:-0.1.0}
PKG_RELEASE=1
OUT="$(pwd)/release"
WORK="$OUT/work"
rm -rf "$OUT" && mkdir -p "$WORK"

# goarch 后缀|GOARCH|opkg 架构|GOARM|GOMIPS
TARGETS=(
  "x86_64|amd64|x86_64||"
  "arm64|arm64|aarch64_generic||"
  "armv7|arm|arm_cortex-a7_vfpv4|7|"
  "mips|mips|mips_24kc||softfloat"
  "mipsle|mipsle|mipsel_24kc||softfloat"
)

LDFLAGS="-s -w -X main.version=$VERSION"

build_binary() {
  local suffix=$1 goarch=$2 goarm=$3 gomips=$4
  echo "==> 编译 linux/$suffix"
  env CGO_ENABLED=0 GOOS=linux GOARCH=$goarch GOARM=$goarm GOMIPS=$gomips \
    go build -ldflags "$LDFLAGS" -o "$WORK/bin_$suffix" .
}

make_ipk() {
  local suffix=$1 opkgarch=$2
  local root="$WORK/ipk_$suffix"
  mkdir -p "$root/data/usr/bin" "$root/data/etc/init.d" "$root/data/etc/config" \
           "$root/data/etc/andeyproxy" "$root/control"

  cp "$WORK/bin_$suffix" "$root/data/usr/bin/andeyproxy"
  chmod 755 "$root/data/usr/bin/andeyproxy"
  cp package/openwrt/files/andeyproxy.init "$root/data/etc/init.d/andeyproxy"
  chmod 755 "$root/data/etc/init.d/andeyproxy"
  cp package/openwrt/files/andeyproxy.config "$root/data/etc/config/andeyproxy"
  chmod 644 "$root/data/etc/config/andeyproxy"

  local size
  size=$(du -sk "$root/data" | cut -f1)
  cat > "$root/control/control" <<EOF
Package: andeyproxy
Version: $VERSION-$PKG_RELEASE
Depends: ca-bundle
Section: net
Architecture: $opkgarch
Installed-Size: $size
Maintainer: andey
Description: andey-Proxy DDNS/反向代理/ACME证书一体工具
 默认后台端口 16601，默认账号密码 666/666，首次登录请修改
EOF

  cat > "$root/control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
/etc/init.d/andeyproxy enable
echo "andey-Proxy 已安装，后台: http://<路由IP>:16601  默认账号密码 666/666"
echo "启动: /etc/init.d/andeyproxy start"
exit 0
EOF
  cat > "$root/control/prerm" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
/etc/init.d/andeyproxy stop 2>/dev/null
/etc/init.d/andeyproxy disable 2>/dev/null
exit 0
EOF
  cat > "$root/control/postrm" <<'EOF'
#!/bin/sh
# 卸载后清理：运行数据（config.json/证书/缓存）与 opkg 留下的 conffile 备份
[ -n "${IPKG_INSTROOT}" ] && exit 0
rm -rf /etc/andeyproxy
rm -f /etc/config/andeyproxy-opkg
exit 0
EOF
  chmod 755 "$root/control/postinst" "$root/control/prerm" "$root/control/postrm"

  local taropt="--uid=0 --gid=0 --numeric-owner"
  (cd "$root/data" && COPYFILE_DISABLE=1 tar $taropt -czf "$root/data.tar.gz" .)
  (cd "$root/control" && COPYFILE_DISABLE=1 tar $taropt -czf "$root/control.tar.gz" .)
  echo "2.0" > "$root/debian-binary"
  (cd "$root" && COPYFILE_DISABLE=1 tar $taropt -czf "$OUT/andeyproxy_${VERSION}-${PKG_RELEASE}_${opkgarch}.ipk" ./debian-binary ./control.tar.gz ./data.tar.gz)
  echo "==> IPK: andeyproxy_${VERSION}-${PKG_RELEASE}_${opkgarch}.ipk"
}

make_run() {
  local suffix=$1
  local root="$WORK/run_$suffix"
  mkdir -p "$root/payload"
  cp "$WORK/bin_$suffix" "$root/payload/andeyproxy"
  cp package/openwrt/files/andeyproxy.init "$root/payload/andeyproxy.init"

  cat > "$root/payload/andeyproxy-uninstall" <<'UNEOF'
#!/bin/sh
# andey-Proxy 卸载程序：停止并删除服务、二进制、配置文件与全部运行数据（证书/缓存）
set -e
BIN_NAME=andeyproxy

if [ "$(id -u)" != "0" ]; then
  echo "请使用 root 运行: sudo $0"; exit 1
fi

echo "正在卸载 andey-Proxy ..."

# 停止并移除服务
if [ -f /etc/init.d/$BIN_NAME ]; then
  /etc/init.d/$BIN_NAME stop 2>/dev/null || true
  /etc/init.d/$BIN_NAME disable 2>/dev/null || true
  rm -f /etc/init.d/$BIN_NAME
fi
if command -v systemctl >/dev/null 2>&1 && [ -f /etc/systemd/system/$BIN_NAME.service ]; then
  systemctl stop $BIN_NAME 2>/dev/null || true
  systemctl disable $BIN_NAME 2>/dev/null || true
  rm -f /etc/systemd/system/$BIN_NAME.service
  systemctl daemon-reload 2>/dev/null || true
fi

# 删除二进制、配置文件、运行数据（config.json、ACME 证书、日志缓存）
rm -f /usr/bin/$BIN_NAME
rm -rf /etc/andeyproxy
rm -f /etc/config/andeyproxy

echo "andey-Proxy 已完全卸载（配置与缓存已清空）"

# 自删除（延迟执行，避免 shell 正在读取脚本）
(sleep 1; rm -f /usr/bin/andeyproxy-uninstall) &
exit 0
UNEOF
  chmod 755 "$root/payload/andeyproxy-uninstall"

  cat > "$root/install.sh" <<'INSTEOF'
#!/bin/sh
# andey-Proxy 自解压安装程序
set -e

VERSION="__VERSION__"
BIN_NAME=andeyproxy
INSTALL_DIR=/usr/bin
CONF_DIR=/etc/andeyproxy

if [ "$(id -u)" != "0" ]; then
  echo "请使用 root 运行: sudo sh $0"; exit 1
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

LINE=$(awk '/^__PAYLOAD_BELOW__$/ {print NR + 1; exit 0}' "$0")
tail -n +"$LINE" "$0" | tar xz -C "$TMP"

echo "安装 andey-Proxy $VERSION ..."
install -m 755 "$TMP/andeyproxy" "$INSTALL_DIR/$BIN_NAME"
install -m 755 "$TMP/andeyproxy-uninstall" "$INSTALL_DIR/andeyproxy-uninstall"
mkdir -p "$CONF_DIR"

if [ -d /etc/init.d ]; then
  install -m 755 "$TMP/andeyproxy.init" /etc/init.d/$BIN_NAME
  /etc/init.d/$BIN_NAME enable 2>/dev/null || true
  /etc/init.d/$BIN_NAME start 2>/dev/null || true
elif command -v systemctl >/dev/null 2>&1; then
  cat > /etc/systemd/system/$BIN_NAME.service <<EOF
[Unit]
Description=andey-Proxy
After=network-online.target

[Service]
ExecStart=$INSTALL_DIR/$BIN_NAME -cd $CONF_DIR
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable --now $BIN_NAME
else
  echo "未检测到 init 系统，请手动运行: $INSTALL_DIR/$BIN_NAME -cd $CONF_DIR"
fi

echo ""
echo "安装完成！后台管理: http://<本机IP>:16601  默认账号密码 666/666（首次登录请修改）"
echo "卸载: sudo andeyproxy-uninstall"
exit 0
__PAYLOAD_BELOW__
INSTEOF
  sed -i '' "s/__VERSION__/$VERSION/" "$root/install.sh"

  local out="$OUT/andey-proxy_${VERSION}_linux_${suffix}.run"
  (cd "$root/payload" && COPYFILE_DISABLE=1 tar czf "$root/payload.tar.gz" .)
  cat "$root/install.sh" "$root/payload.tar.gz" > "$out"
  chmod +x "$out"
  echo "==> RUN: $(basename "$out")"
}

for t in "${TARGETS[@]}"; do
  IFS='|' read -r suffix goarch opkgarch goarm gomips <<< "$t"
  build_binary "$suffix" "$goarch" "$goarm" "$gomips"
  make_ipk "$suffix" "$opkgarch"
  make_run "$suffix"
done

(cd "$OUT" && for f in *.ipk *.run; do shasum -a 256 "$f"; done > checksums.txt)
rm -rf "$WORK"
echo ""
echo "全部完成，产物："
ls -lh "$OUT/" | awk 'NR>1{print "  "$5"  "$9}'
