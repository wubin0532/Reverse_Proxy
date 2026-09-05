#!/bin/bash
# andey-Proxy 发布包构建：交叉编译 + 生成 .ipk 与 .run（免 OpenWrt SDK）
set -e
cd "$(dirname "$0")/.."
export PATH="/opt/homebrew/bin:$PATH"

VERSION=${VERSION:-0.2.0}
PKG_RELEASE=1
SIGNING_KEY=${RELEASE_SIGNING_KEY:-}
NODE_BIN=${NODE_BIN:-node}
OUT="$(pwd)/release"
WORK="$OUT/work"
rm -rf "$OUT" && mkdir -p "$WORK"

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# goarch 后缀|GOARCH|opkg 架构|GOARM|GOMIPS
TARGETS=(
  "x86_64|amd64|x86_64||"
  "arm64|arm64|aarch64_cortex-a53||"
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
           "$root/data/etc/andey-proxy" "$root/control"

  cp "$WORK/bin_$suffix" "$root/data/usr/bin/andey-proxy"
  chmod 755 "$root/data/usr/bin/andey-proxy"
  cp package/openwrt/files/andey-proxy.init "$root/data/etc/init.d/andey-proxy"
  chmod 755 "$root/data/etc/init.d/andey-proxy"
  cp package/openwrt/files/andey-proxy.config "$root/data/etc/config/andey-proxy"
  chmod 644 "$root/data/etc/config/andey-proxy"
	chmod 700 "$root/data/etc/andey-proxy"

  local size
  size=$(du -sk "$root/data" | cut -f1)
  cat > "$root/control/control" <<EOF
Package: andey-proxy
Version: $VERSION-$PKG_RELEASE
Depends: ca-bundle
Section: net
Architecture: $opkgarch
Installed-Size: $size
Maintainer: andey
Description: andey-Proxy DDNS/反向代理/ACME证书一体工具
 默认后台端口 16601，初始密码在首次启动时随机生成
EOF

  # conffiles：升级时保留用户的 UCI 配置（不声明会被包内默认配置覆盖）
  printf '/etc/config/andey-proxy\n' > "$root/control/conffiles"

  cat > "$root/control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
/etc/init.d/andey-proxy enable
# 升级场景：旧包 prerm 停了服务，若用户已启用则自动拉起
if [ "$(uci -q get andey-proxy.main.enabled)" = "1" ]; then
  /etc/init.d/andey-proxy restart 2>/dev/null
fi
echo "andey-Proxy 已安装，后台: https://<路由IP>:16601"
echo "启动: /etc/init.d/andey-proxy start"
exit 0
EOF
  cat > "$root/control/prerm" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
/etc/init.d/andey-proxy stop 2>/dev/null
[ "$1" = "upgrade" ] && exit 0
/etc/init.d/andey-proxy disable 2>/dev/null
exit 0
EOF
  cat > "$root/control/postrm" <<'EOF'
#!/bin/sh
# 卸载后清理：运行数据（config.json/证书/缓存）与 opkg 留下的 conffile 备份
# 升级时 opkg 也会执行旧包 postrm（参数 upgrade），绝不能删数据
[ -n "${IPKG_INSTROOT}" ] && exit 0
[ "$1" = "upgrade" ] && exit 0
rm -rf /etc/andey-proxy
rm -f /etc/config/andey-proxy-opkg
exit 0
EOF
  chmod 755 "$root/control/postinst" "$root/control/prerm" "$root/control/postrm"

  local taropt="--format=ustar --uid=0 --gid=0 --numeric-owner"
  (cd "$root/data" && COPYFILE_DISABLE=1 tar $taropt -czf "$root/data.tar.gz" .)
  (cd "$root/control" && COPYFILE_DISABLE=1 tar $taropt -czf "$root/control.tar.gz" .)
  echo "2.0" > "$root/debian-binary"
  (cd "$root" && COPYFILE_DISABLE=1 tar $taropt -czf "$OUT/andey-proxy_${VERSION}-${PKG_RELEASE}_${opkgarch}.ipk" ./debian-binary ./control.tar.gz ./data.tar.gz)
  echo "==> IPK: andey-proxy_${VERSION}-${PKG_RELEASE}_${opkgarch}.ipk"
}

make_luci_ipk() {
  local root="$WORK/ipk_luci"
  mkdir -p "$root/data" "$root/control"
  cp -r package/luci-app-andeyproxy/root/. "$root/data/"
  chmod 644 "$root/data/usr/share/luci/menu.d/luci-app-andeyproxy.json" \
            "$root/data/usr/share/rpcd/acl.d/luci-app-andeyproxy.json" \
            "$root/data/www/luci-static/resources/view/andeyproxy/settings.js" \
            "$root/data/www/luci-static/resources/view/andeyproxy/panel.js"

  local size
  size=$(du -sk "$root/data" | cut -f1)
  cat > "$root/control/control" <<EOF
Package: luci-app-andeyproxy
Version: $VERSION-$PKG_RELEASE
Depends: andey-proxy, luci-base
Section: luci
Architecture: all
Installed-Size: $size
Maintainer: andey
Description: LuCI support for andey-Proxy
 LuCI 菜单入口（服务 -> andey-Proxy）与基本设置页
EOF

  cat > "$root/control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
rm -rf /tmp/luci-indexcache /tmp/luci-modulecache
echo "andey-Proxy LuCI 菜单已安装，刷新 LuCI 页面后在 服务 菜单查看"
exit 0
EOF
  cat > "$root/control/postrm" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
rm -rf /tmp/luci-indexcache /tmp/luci-modulecache
exit 0
EOF
  chmod 755 "$root/control/postinst" "$root/control/postrm"

  local taropt="--format=ustar --uid=0 --gid=0 --numeric-owner"
  (cd "$root/data" && COPYFILE_DISABLE=1 tar $taropt -czf "$root/data.tar.gz" .)
  (cd "$root/control" && COPYFILE_DISABLE=1 tar $taropt -czf "$root/control.tar.gz" .)
  echo "2.0" > "$root/debian-binary"
  (cd "$root" && COPYFILE_DISABLE=1 tar $taropt -czf "$OUT/luci-app-andeyproxy_${VERSION}-${PKG_RELEASE}_all.ipk" ./debian-binary ./control.tar.gz ./data.tar.gz)
  echo "==> IPK: luci-app-andeyproxy_${VERSION}-${PKG_RELEASE}_all.ipk"
}

make_run() {
  local suffix=$1
  local goarch=$2
  local root="$WORK/run_$suffix"
  mkdir -p "$root/payload"
  cp "$WORK/bin_$suffix" "$root/payload/andey-proxy"
  cp package/openwrt/files/andey-proxy.init "$root/payload/andey-proxy.init"

  cat > "$root/payload/andey-proxy-uninstall" <<'UNEOF'
#!/bin/sh
# andey-Proxy 卸载程序：停止并删除服务、二进制、配置文件与全部运行数据（证书/缓存）
set -e
BIN_NAME=andey-proxy

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
rm -rf /etc/andey-proxy
rm -f /etc/config/andey-proxy

echo "andey-Proxy 已完全卸载（配置与缓存已清空）"

# 自删除（延迟执行，避免 shell 正在读取脚本）
(sleep 1; rm -f /usr/bin/andey-proxy-uninstall) &
exit 0
UNEOF
  chmod 755 "$root/payload/andey-proxy-uninstall"

  cat > "$root/install.sh" <<'INSTEOF'
#!/bin/sh
# andey-Proxy 自解压安装程序
set -e

VERSION="__VERSION__"
BIN_NAME=andey-proxy
INSTALL_DIR=/usr/bin
CONF_DIR=/etc/andey-proxy

if [ "$(id -u)" != "0" ]; then
  echo "请使用 root 运行: sudo sh $0"; exit 1
fi

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

LINE=$(awk '/^__PAYLOAD_BELOW__$/ {print NR + 1; exit 0}' "$0")
tail -n +"$LINE" "$0" | tar xz -C "$TMP"

echo "安装 andey-Proxy $VERSION ..."
install -m 755 "$TMP/andey-proxy" "$INSTALL_DIR/$BIN_NAME"
install -m 755 "$TMP/andey-proxy-uninstall" "$INSTALL_DIR/andey-proxy-uninstall"
install -d -m 700 "$CONF_DIR"

if [ -d /etc/init.d ]; then
  install -m 755 "$TMP/andey-proxy.init" /etc/init.d/$BIN_NAME
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
echo "安装完成！后台管理: https://<本机IP>:16601"
echo "首次启动会在控制台输出一次性随机密码。"
echo "卸载: sudo andey-proxy-uninstall"
exit 0
__PAYLOAD_BELOW__
INSTEOF
  sed "s/__VERSION__/$VERSION/" "$root/install.sh" > "$root/install.sh.tmp"
  mv "$root/install.sh.tmp" "$root/install.sh"

  if [ -z "$SIGNING_KEY" ] || [ ! -f "$SIGNING_KEY" ]; then
    echo "错误：构建 .run 需要 RELEASE_SIGNING_KEY 指向 Ed25519 私钥" >&2
    exit 1
  fi
  local size digest
  size=$(wc -c < "$root/payload/andey-proxy" | tr -d ' ')
  digest=$(sha256_file "$root/payload/andey-proxy")
  printf '{"version":"%s","goos":"linux","goarch":"%s","size":%s,"sha256":"%s"}' \
    "$VERSION" "$goarch" "$size" "$digest" > "$root/payload/manifest.json"
  "$NODE_BIN" -e 'const fs=require("fs"),c=require("crypto");const [m,k,o]=process.argv.slice(1);fs.writeFileSync(o,c.sign(null,fs.readFileSync(m),fs.readFileSync(k)).toString("base64"))' \
    "$root/payload/manifest.json" "$SIGNING_KEY" "$root/payload/manifest.sig"

  local out="$OUT/andey-proxy_${VERSION}_linux_${suffix}.run"
  (cd "$root/payload" && COPYFILE_DISABLE=1 tar --format=ustar -czf "$root/payload.tar.gz" .)
  cat "$root/install.sh" "$root/payload.tar.gz" > "$out"
  chmod +x "$out"
  echo "==> RUN: $(basename "$out")"
}

for t in "${TARGETS[@]}"; do
  IFS='|' read -r suffix goarch opkgarch goarm gomips <<< "$t"
  build_binary "$suffix" "$goarch" "$goarm" "$gomips"
  make_ipk "$suffix" "$opkgarch"
  make_run "$suffix" "$goarch"
done
make_luci_ipk

(cd "$OUT" && for f in *.ipk *.run; do printf '%s  %s\n' "$(sha256_file "$f")" "$f"; done > checksums.txt)
rm -rf "$WORK"
echo ""
echo "全部完成，产物："
ls -lh "$OUT/" | awk 'NR>1{print "  "$5"  "$9}'
