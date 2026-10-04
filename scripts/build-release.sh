#!/bin/bash
# andey-Proxy 发布包构建：交叉编译 + 生成 .ipk 与 .run（免 OpenWrt SDK）
set -e
cd "$(dirname "$0")/.."

# 版本默认取 git tag（与 build-all.sh 一致），发版前记得打 tag 或用 VERSION= 显式指定
VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null | sed 's/^v//' || true)}
VERSION=${VERSION:-dev}

# go:embed 依赖 internal/adminweb/dist；发版前确保前端已重建
if [ ! -f internal/adminweb/dist/index.html ]; then
  echo "错误：internal/adminweb/dist 缺失，请先执行 make web 构建前端" >&2
  exit 1
fi
if [ -n "$(find web/src -type f -newer internal/adminweb/dist/index.html -print -quit 2>/dev/null)" ]; then
  echo "警告：web/src 比 internal/adminweb/dist 新，建议先执行 make web 重建前端" >&2
fi
PKG_RELEASE=1
SIGNING_KEY=${RELEASE_SIGNING_KEY:-}
SIGNING_PUBKEY=${RELEASE_SIGNING_PUBLIC_KEY:-}
IPK_ONLY=${IPK_ONLY:-0}
NODE_BIN=${NODE_BIN:-node}
OUT="$(pwd)/release"
WORK="$OUT/work"
rm -rf "$OUT" && mkdir -p "$WORK"

# GNU tar (GitHub Actions/Linux) and bsdtar (macOS) use different option
# names for normalising archive ownership. Keep release archives reproducible
# on both builders instead of relying on one implementation's flags.
if tar --version 2>/dev/null | grep -q 'GNU tar'; then
  TAR_OWNER_OPTS=(--owner=0 --group=0 --numeric-owner)
else
  TAR_OWNER_OPTS=(--uid=0 --gid=0 --numeric-owner)
fi

make_tar_gz() {
  local source_dir=$1 output_file=$2
  shift 2
  (cd "$source_dir" && COPYFILE_DISABLE=1 tar --format=ustar "${TAR_OWNER_OPTS[@]}" -czf "$output_file" "$@")
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

# LuCI 翻译：po -> lmo（.lmo 是 LuCI 专有格式，msgfmt 的 .mo 不可用）。
# 文件名遵循 luci.mk 的 LC_ALIAS 约定（zh_Hans -> zh-cn），
# 运行时 LuCI 会加载 /usr/lib/lua/luci/i18n/*.<系统语言>.lmo 中的全部翻译。
# ipk 与 .run 都要用，抽成函数避免两边漂移。
build_luci_i18n() {
  local i18ndir=$1 po lang
  local po2lmo_cmd
  if command -v po2lmo >/dev/null 2>&1; then
    po2lmo_cmd=(po2lmo)
  elif command -v python3 >/dev/null 2>&1; then
    po2lmo_cmd=(python3 package/openwrt/po/po2lmo.py)
  else
    echo "错误：编译 LuCI 翻译需要 po2lmo 或 python3" >&2
    exit 1
  fi
  mkdir -p "$i18ndir"
  for po in package/openwrt/po/*/andeyproxy.po; do
    [ -f "$po" ] || continue
    lang=$(basename "$(dirname "$po")")
    case "$lang" in
      zh_Hans) lang=zh-cn ;;
      zh_Hant) lang=zh-tw ;;
    esac
    "${po2lmo_cmd[@]}" "$po" "$i18ndir/andeyproxy.$lang.lmo"
    chmod 644 "$i18ndir/andeyproxy.$lang.lmo"
  done
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

  # LuCI 界面（菜单/ACL/设置页）随主包一起安装
  cp -r package/openwrt/luci/. "$root/data/"
  chmod 644 "$root/data/usr/share/luci/menu.d/luci-app-andeyproxy.json" \
            "$root/data/usr/share/rpcd/acl.d/luci-app-andeyproxy.json" \
            "$root/data/www/luci-static/resources/view/andeyproxy/settings.js" \
            "$root/data/www/luci-static/resources/view/andeyproxy/panel.js"

  build_luci_i18n "$root/data/usr/lib/lua/luci/i18n"

  local size
  size=$(du -sk "$root/data" | cut -f1)
  cat > "$root/control/control" <<EOF
Package: luci-app-andeyproxy
Version: $VERSION-$PKG_RELEASE
Depends: ca-bundle
Conflicts: andey-proxy
Section: luci
Architecture: $opkgarch
Installed-Size: $size
Maintainer: andey
Description: andey-Proxy DDNS/反向代理/ACME证书一体工具（含 LuCI 界面）
 默认后台端口 16606，初始密码在首次启动时随机生成
EOF

  # conffiles：升级时保留用户的 UCI 配置（不声明会被包内默认配置覆盖）
  printf '/etc/config/andey-proxy\n' > "$root/control/conffiles"

  cat > "$root/control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] && exit 0
# 旧包名迁移（andey-proxy -> luci-app-andeyproxy）：旧包卸载会删除运行数据，
# 若按文档先备份到 /etc/andey-proxy.bak(.uci) 则在此自动恢复
if [ -f /etc/andey-proxy.bak/config.json ] && [ ! -s /etc/andey-proxy/config.json ]; then
  rm -rf /etc/andey-proxy
  cp -a /etc/andey-proxy.bak /etc/andey-proxy
fi
if [ -f /etc/andey-proxy.bak.uci ]; then
  cp -a /etc/andey-proxy.bak.uci /etc/config/andey-proxy
fi
rm -rf /etc/andey-proxy.bak /etc/andey-proxy.bak.uci
/etc/init.d/andey-proxy enable
# 升级场景：旧包 prerm 停了服务，若用户已启用则自动拉起
if [ "$(uci -q get andey-proxy.main.enabled)" = "1" ]; then
  /etc/init.d/andey-proxy restart 2>/dev/null
fi
# LuCI 界面随主包安装：清缓存并让 rpcd 重新加载 ACL
rm -rf /tmp/luci-indexcache /tmp/luci-indexcache.* /tmp/luci-modulecache /tmp/luci-modulecache.*
/etc/init.d/rpcd restart 2>/dev/null
echo "andey-Proxy 已安装，后台: https://<路由IP>:16606"
echo "LuCI 菜单位于 服务 -> andey-Proxy（强制刷新浏览器页面后可见）"
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
# LuCI 文件由 opkg 自动移除，这里清缓存让菜单立即消失
rm -rf /tmp/luci-indexcache /tmp/luci-indexcache.* /tmp/luci-modulecache /tmp/luci-modulecache.*
[ "$1" = "upgrade" ] && exit 0
rm -rf /etc/andey-proxy
rm -f /etc/andey-proxy.key
rm -f /etc/config/andey-proxy-opkg
exit 0
EOF
  chmod 755 "$root/control/postinst" "$root/control/prerm" "$root/control/postrm"

  make_tar_gz "$root/data" "$root/data.tar.gz" .
  make_tar_gz "$root/control" "$root/control.tar.gz" .
  echo "2.0" > "$root/debian-binary"
  make_tar_gz "$root" "$OUT/luci-app-andeyproxy_${VERSION}-${PKG_RELEASE}_${opkgarch}.ipk" ./debian-binary ./control.tar.gz ./data.tar.gz
  echo "==> IPK: luci-app-andeyproxy_${VERSION}-${PKG_RELEASE}_${opkgarch}.ipk"
}

make_run() {
  local suffix=$1
  local goarch=$2
  local root="$WORK/run_$suffix"
  mkdir -p "$root/payload"
  cp "$WORK/bin_$suffix" "$root/payload/andey-proxy"
  cp package/openwrt/files/andey-proxy.init "$root/payload/andey-proxy.init"

  # LuCI 界面（菜单/ACL/设置页/翻译）也打进 .run：ImmortalWrt 25.12 起包管理器
  # 换成 apk，.ipk 装不上，只能走 .run；而用户期望服务菜单里有入口。
  # 载荷放在 luci/ 前缀下，安装脚本只在目标机装了 LuCI 时才铺开，
  # 纯 Linux 服务器上这些文件原样忽略（升级解析器按文件名取件，也不受影响）。
  cp -r package/openwrt/luci/. "$root/payload/luci/"
  chmod 644 "$root/payload/luci/usr/share/luci/menu.d/luci-app-andeyproxy.json" \
            "$root/payload/luci/usr/share/rpcd/acl.d/luci-app-andeyproxy.json" \
            "$root/payload/luci/www/luci-static/resources/view/andeyproxy/settings.js" \
            "$root/payload/luci/www/luci-static/resources/view/andeyproxy/panel.js"
  build_luci_i18n "$root/payload/luci/usr/lib/lua/luci/i18n"

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

# 删除二进制、配置文件、运行数据（config.json、ACME 证书、日志缓存）与相邻密钥文件
rm -f /usr/bin/$BIN_NAME
rm -rf /etc/andey-proxy
rm -f /etc/andey-proxy.key
rm -f /etc/config/andey-proxy

# 移除随 .run 一起安装的 LuCI 界面（菜单/ACL/设置页/翻译）
rm -f /usr/share/luci/menu.d/luci-app-andeyproxy.json
rm -f /usr/share/rpcd/acl.d/luci-app-andeyproxy.json
rm -f /www/luci-static/resources/view/andeyproxy/settings.js
rm -f /www/luci-static/resources/view/andeyproxy/panel.js
rmdir /www/luci-static/resources/view/andeyproxy 2>/dev/null || true
rm -f /usr/lib/lua/luci/i18n/andeyproxy.*.lmo
# 清 LuCI 缓存并让 rpcd 重新加载 ACL，服务菜单里的入口立即消失
rm -rf /tmp/luci-indexcache /tmp/luci-indexcache.* /tmp/luci-modulecache /tmp/luci-modulecache.*
/etc/init.d/rpcd restart 2>/dev/null || true

echo "andey-Proxy 已完全卸载（配置与缓存已清空）"

# Shell 保持已打开的脚本文件；直接删除，避免延迟任务误删随后重装的新脚本。
rm -f /usr/bin/andey-proxy-uninstall
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
# 不依赖 install(1)：OpenWrt/ImmortalWrt 的 BusyBox 未必编入该 applet，
# 缺失时脚本会在 set -e 下直接中断，表现为"run 包装不上"
cp -f "$TMP/andey-proxy" "$INSTALL_DIR/$BIN_NAME"
chmod 755 "$INSTALL_DIR/$BIN_NAME"
cp -f "$TMP/andey-proxy-uninstall" "$INSTALL_DIR/andey-proxy-uninstall"
chmod 755 "$INSTALL_DIR/andey-proxy-uninstall"
mkdir -p "$CONF_DIR"
chmod 700 "$CONF_DIR"

if [ -d /etc/init.d ]; then
  cp -f "$TMP/andey-proxy.init" /etc/init.d/$BIN_NAME
  chmod 755 /etc/init.d/$BIN_NAME
  # OpenWrt 的 init 脚本走 procd + UCI：/etc/config/andey-proxy 缺失时
  # enabled 默认 0，即使 enable 过 start 也不会拉起进程。.run 包不释放
  # UCI 配置，这里补一份最小配置让安装后服务真正跑起来（ipk 由包内配置负责）
  if [ -x /sbin/uci ] && [ ! -f /etc/config/andey-proxy ]; then
    mkdir -p /etc/config
    cat > /etc/config/andey-proxy <<'UCIEOF'
config andey-proxy 'main'
	option enabled '1'
	option confdir '/etc/andey-proxy'
	option port '16606'
	option admin_http '0'
UCIEOF
    chmod 644 /etc/config/andey-proxy
  fi

  # LuCI 界面：仅在目标机确实装了 LuCI 时铺开（纯 Linux 服务器跳过）
  if [ -d /usr/share/luci ] && [ -d /www/luci-static ] && [ -d "$TMP/luci" ]; then
    mkdir -p /usr/share/luci/menu.d /usr/share/rpcd/acl.d \
             /www/luci-static/resources/view/andeyproxy /usr/lib/lua/luci/i18n
    cp -f "$TMP/luci/usr/share/luci/menu.d/luci-app-andeyproxy.json" /usr/share/luci/menu.d/
    cp -f "$TMP/luci/usr/share/rpcd/acl.d/luci-app-andeyproxy.json" /usr/share/rpcd/acl.d/
    cp -f "$TMP/luci/www/luci-static/resources/view/andeyproxy/"*.js /www/luci-static/resources/view/andeyproxy/
    cp -f "$TMP/luci/usr/lib/lua/luci/i18n/"andeyproxy.*.lmo /usr/lib/lua/luci/i18n/
    chmod 644 /usr/share/luci/menu.d/luci-app-andeyproxy.json \
              /usr/share/rpcd/acl.d/luci-app-andeyproxy.json
    for f in /www/luci-static/resources/view/andeyproxy/*.js /usr/lib/lua/luci/i18n/andeyproxy.*.lmo; do
      [ -f "$f" ] || continue
      chmod 644 "$f"
    done
    # 清 LuCI 缓存并让 rpcd 重新加载 ACL，菜单无需重启设备即可出现
    rm -rf /tmp/luci-indexcache /tmp/luci-indexcache.* /tmp/luci-modulecache /tmp/luci-modulecache.*
    /etc/init.d/rpcd restart 2>/dev/null || true
    echo "LuCI 界面已安装：服务 -> andey-Proxy（浏览器强制刷新后可见）"
  fi
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
echo "安装完成！后台管理: https://<本机IP>:16606"
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
  if [ -n "$SIGNING_PUBKEY" ]; then
    if [ ! -f "$SIGNING_PUBKEY" ]; then
      echo "错误：RELEASE_SIGNING_PUBLIC_KEY 指向的公钥文件不存在: $SIGNING_PUBKEY" >&2
      exit 1
    fi
    if ! "$NODE_BIN" -e 'const fs=require("fs"),c=require("crypto");const [m,s,k]=process.argv.slice(1);if(!c.verify(null,fs.readFileSync(m),fs.readFileSync(k),Buffer.from(fs.readFileSync(s,"utf8").trim(),"base64")))process.exit(1)' \
      "$root/payload/manifest.json" "$root/payload/manifest.sig" "$SIGNING_PUBKEY"; then
      echo "错误：manifest.sig 自验签失败，签名私钥与 RELEASE_SIGNING_PUBLIC_KEY 不匹配" >&2
      exit 1
    fi
    echo "==> 签名自验通过（manifest.sig 与公钥匹配）"
  else
    echo "警告：未设置 RELEASE_SIGNING_PUBLIC_KEY，跳过签名自验，建议设置以确认密钥匹配" >&2
  fi

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
  if [ "$IPK_ONLY" != "1" ]; then
    make_run "$suffix" "$goarch"
  fi
done

(cd "$OUT" && {
  for f in *.ipk; do printf '%s  %s\n' "$(sha256_file "$f")" "$f"; done
  if [ "$IPK_ONLY" != "1" ]; then
    for f in *.run; do printf '%s  %s\n' "$(sha256_file "$f")" "$f"; done
  fi
} > checksums.txt)
rm -rf "$WORK"
echo ""
echo "全部完成，产物："
ls -lh "$OUT/" | awk 'NR>1{print "  "$5"  "$9}'
