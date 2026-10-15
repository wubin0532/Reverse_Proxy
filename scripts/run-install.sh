#!/bin/sh
# Template embedded in each signed .run package. No Go/Node runtime required.
set -eu

VERSION="__VERSION__"
PACKAGE_ARCH="__ARCH__"
BINARY_SIZE="__BINARY_SIZE__"
BINARY_SHA256="__BINARY_SHA256__"
BIN_NAME=andey-proxy
INSTALL_DIR=/usr/bin
CONF_DIR=/etc/andey-proxy
MODE=install
PUBKEY_FILE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --check) MODE=check ;;
    --pubkey)
      shift
      [ -n "${1:-}" ] || { echo "用法: sh $0 [--check] [--pubkey <公钥文件>]" >&2; exit 1; }
      PUBKEY_FILE=${1:-}
      ;;
    --help|-h) echo "用法: sh $0 [--check] [--pubkey <公钥文件>]  --check只检查环境与载荷，不安装或修改配置"; exit 0 ;;
    *) echo "未知参数: $1" >&2; exit 1 ;;
  esac
  shift
done
fail() { echo "检查失败: $*" >&2; exit 1; }
for cmd in uname id awk grep tail tar gzip cp chmod mkdir mv rm mktemp df sha256sum; do
  command -v "$cmd" >/dev/null 2>&1 || fail "缺少命令 ${cmd}，请安装相应系统工具后重试"
done
[ "$(uname -s)" = Linux ] || fail "仅支持Linux；当前系统为$(uname -s)"
MACHINE=$(uname -m)
case "$PACKAGE_ARCH:$MACHINE" in
  x86_64:x86_64|x86_64:amd64|arm64:aarch64|arm64:arm64|armv7:armv7l|armv7:armv8l) ;;
  mips:mips|mipsle:mips|mipsle:mipsel)
    command -v hexdump >/dev/null 2>&1 || fail "MIPS架构检查需要hexdump"
    ENDIAN=$(hexdump -s 5 -n 1 -e '1/1 "%u"' /bin/sh)
    case "$PACKAGE_ARCH:$ENDIAN" in mips:2|mipsle:1) ;; *) fail "MIPS大小端不匹配，请下载正确的mips/mipsle安装包" ;; esac ;;
  *) fail "安装包架构${PACKAGE_ARCH}与设备${MACHINE}不匹配" ;;
esac
HAS_CA=0
for ca in /etc/ssl/certs/ca-certificates.crt /etc/ssl/cert.pem /etc/pki/tls/certs/ca-bundle.crt /etc/ssl/certs/*.crt /etc/ssl/certs/*.pem /etc/ssl/certs/*.0; do
  if [ -s "$ca" ]; then HAS_CA=1; break; fi
done
[ "$HAS_CA" = 1 ] || fail "缺少系统CA证书。OpenWrt请安装ca-bundle；Debian/Ubuntu请安装ca-certificates，再重试"
SERVICE=manual
if [ -x /sbin/uci ] && [ -f /etc/rc.common ]; then
  SERVICE=procd
elif command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  SERVICE=systemd
fi
available_kib() { df -Pk "$1" | awk 'NR==2 {print $4}'; }
check_space() {
  FREE_KIB=$(available_kib "$1")
  case "$FREE_KIB" in ""|*[!0-9]*) fail "无法读取$1的可用空间" ;; esac
  [ "$FREE_KIB" -ge "$2" ] || fail "$1可用${FREE_KIB}KiB，至少需要${2}KiB；请释放空间后重试"
}
NEEDED_KIB=$(( (BINARY_SIZE + 1023) / 1024 + 4096 ))
TMP_BASE=${TMPDIR:-/tmp}
check_space "$TMP_BASE" "$((NEEDED_KIB * 2))"
check_space "$INSTALL_DIR" "$NEEDED_KIB"
echo "环境检查通过：Linux/${MACHINE}，安装包${PACKAGE_ARCH}，CA证书已安装，服务管理$SERVICE"
[ "$SERVICE" != manual ] || echo "提示：未检测到procd/systemd，安装后需要手动启动"

TMP=$(mktemp -d "$TMP_BASE/andey-install.XXXXXX")
STAGED=""
OLD_BINARY=""
REPLACED=0
DONE=0
HAD_SERVICE=0
STOPPED=0
cleanup() {
  if [ "$DONE" != 1 ]; then
    if [ -n "$OLD_BINARY" ] && [ -f "$OLD_BINARY" ]; then
      echo "安装未完成，正在恢复原程序" >&2
      mv -f "$OLD_BINARY" "$INSTALL_DIR/$BIN_NAME"
    elif [ "$REPLACED" = 1 ] && [ -z "$OLD_BINARY" ]; then
      rm -f "$INSTALL_DIR/$BIN_NAME"
    fi
    if [ -f "$TMP/previous.init" ]; then cp -p "$TMP/previous.init" /etc/init.d/$BIN_NAME; fi
    if [ -f "$TMP/previous.service" ]; then cp -p "$TMP/previous.service" /etc/systemd/system/$BIN_NAME.service; fi
    if [ "$HAD_SERVICE" = 1 ] && [ "$STOPPED" = 1 ]; then
      case "$SERVICE" in procd) /etc/init.d/$BIN_NAME start || true ;; systemd) systemctl daemon-reload; systemctl start "$BIN_NAME" || true ;; esac
    fi
  fi
  [ -z "$STAGED" ] || rm -f "$STAGED"
  rm -rf "$TMP"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
LINE=$(awk '/^__PAYLOAD_BELOW__$/ {print NR + 1; exit}' "$0")
[ -n "$LINE" ] || fail "安装包载荷标记缺失"
tail -n +"$LINE" "$0" > "$TMP/payload.tar.gz"

# 解包前只把清单文件取到 stdout 再自行落盘，不写入归档内的任意路径。
for manifest_file in manifest.json manifest.sig files.sha256; do
  # BusyBox tar matches ./manifest.json literally; release archives use ./.
  if ! tar -xOzf "$TMP/payload.tar.gz" "$manifest_file" > "$TMP/$manifest_file" 2>/dev/null; then
    tar -xOzf "$TMP/payload.tar.gz" "./$manifest_file" > "$TMP/$manifest_file" 2>/dev/null || fail "安装包缺少 $manifest_file"
  fi
  [ -s "$TMP/$manifest_file" ] || fail "安装包清单文件为空: $manifest_file"
done

# 签名验证：公钥必须经带外渠道获取后用 --pubkey 传入。安装脚本自身无法
# 成为信任根（脚本与公钥都在包内，可被一起替换），因此不内置公钥。
if [ -n "$PUBKEY_FILE" ]; then
  [ -f "$PUBKEY_FILE" ] || fail "公钥文件不存在: $PUBKEY_FILE"
  command -v openssl >/dev/null 2>&1 || fail "已指定 --pubkey 但系统缺少 openssl，无法验证签名"
  command -v base64 >/dev/null 2>&1 || fail "已指定 --pubkey 但系统缺少 base64"
  base64 -d "$TMP/manifest.sig" > "$TMP/manifest.sig.bin" 2>/dev/null || fail "签名内容不是有效的 base64"
  openssl pkeyutl -verify -rawin -pubin -inkey "$PUBKEY_FILE" -in "$TMP/manifest.json" -sigfile "$TMP/manifest.sig.bin" >/dev/null 2>&1 \
    || fail "安装包签名验证失败（公钥不匹配或清单被篡改）"
  echo "签名验证通过"
else
  echo "提示：未指定 --pubkey，本次只校验摘要、未验证签名真实性；如需验证请从官网获取公钥并加 --pubkey <公钥文件>"
fi

# 拒绝归档中的绝对路径与包含 .. 的成员，避免未验签载荷写出解包目录。
tar -tzf "$TMP/payload.tar.gz" > "$TMP/members.txt" || fail "无法读取安装包内容"
while IFS= read -r member; do
  case "$member" in
    ""|/*|..|../*|*/..|*/../*) fail "安装包包含不安全的成员路径: $member" ;;
  esac
done < "$TMP/members.txt"

# Links and special files can redirect extraction outside the temporary tree.
# Both GNU and BusyBox tar put the entry type first in verbose listings.
tar -tvzf "$TMP/payload.tar.gz" > "$TMP/member-types.txt" || fail "无法读取安装包文件类型"
while read -r mode rest; do
  case "$mode" in -*|d*) ;; *) fail "安装包不允许链接或特殊文件" ;; esac
done < "$TMP/member-types.txt"

tar xzf "$TMP/payload.tar.gz" -C "$TMP" || fail "解包失败"

# 逐文件核对被签名清单覆盖的内容摘要。
FILES_SHA=$(sha256sum "$TMP/files.sha256" | awk '{print $1}')
grep -q "\"filesSha256\":\"$FILES_SHA\"" "$TMP/manifest.json" || fail "文件清单与签名清单不一致"
while read -r digest file; do
  [ -n "$digest" ] || continue
  [ -f "$TMP/$file" ] || fail "载荷缺少文件: $file"
  [ "$(sha256sum "$TMP/$file" | awk '{print $1}')" = "$digest" ] || fail "文件摘要不匹配: $file"
done < "$TMP/files.sha256"

# Every extracted file that may be installed must belong to the signed list;
# otherwise an extra LuCI script could accompany an authentic manifest.
while IFS= read -r member; do
  file=${member#./}
  case "$file" in ""|manifest.json|manifest.sig|files.sha256) continue ;; esac
  [ ! -d "$TMP/$file" ] || continue
  digest=$(sha256sum "$TMP/$file" | awk '{print $1}')
  grep -Fxq "$digest  $file" "$TMP/files.sha256" || fail "载荷文件未列入已验证清单: $file"
done < "$TMP/members.txt"

[ -f "$TMP/$BIN_NAME" ] || fail "载荷不完整"
ACTUAL_SHA=$(sha256sum "$TMP/$BIN_NAME" | awk '{print $1}')
[ "$ACTUAL_SHA" = "$BINARY_SHA256" ] || fail "程序SHA256不匹配，安装包可能损坏"
echo "载荷检查通过：v${VERSION}，清单与摘要一致，架构匹配（--check 不执行载荷）"
if [ "$MODE" = check ]; then DONE=1; echo "检查完成；未安装、未修改配置、未启停服务"; exit 0; fi
[ "$(id -u)" = 0 ] || fail "安装须使用root：sudo sh $0"
chmod 755 "$TMP/$BIN_NAME"
[ "$("$TMP/$BIN_NAME" -v)" = "$VERSION" ] || fail "程序版本与安装包版本不一致"
[ -w "$INSTALL_DIR" ] || fail "${INSTALL_DIR}不可写"

STAGED="$INSTALL_DIR/.$BIN_NAME.new.$$"
cp -f "$TMP/$BIN_NAME" "$STAGED"
chmod 755 "$STAGED"
case "$SERVICE" in
  procd)
    if [ -f /etc/init.d/$BIN_NAME ]; then
      cp -p /etc/init.d/$BIN_NAME "$TMP/previous.init"
      if /etc/init.d/$BIN_NAME status >/dev/null 2>&1; then HAD_SERVICE=1; fi
      STOPPED=1
      /etc/init.d/$BIN_NAME stop
    fi ;;
  systemd)
    if [ -f /etc/systemd/system/$BIN_NAME.service ]; then
      cp -p /etc/systemd/system/$BIN_NAME.service "$TMP/previous.service"
      if systemctl is-active --quiet "$BIN_NAME"; then HAD_SERVICE=1; fi
      STOPPED=1
      systemctl stop "$BIN_NAME"
    fi ;;
esac
if [ -f "$INSTALL_DIR/$BIN_NAME" ]; then
  OLD_BINARY="$INSTALL_DIR/.$BIN_NAME.previous.$$"
  mv "$INSTALL_DIR/$BIN_NAME" "$OLD_BINARY"
fi
REPLACED=1
mv "$STAGED" "$INSTALL_DIR/$BIN_NAME"
STAGED=""
cp -f "$TMP/andey-proxy-uninstall" "$INSTALL_DIR/andey-proxy-uninstall"
chmod 755 "$INSTALL_DIR/andey-proxy-uninstall"
mkdir -p "$CONF_DIR"
chmod 700 "$CONF_DIR"
if [ -d "$TMP/notices" ]; then
  mkdir -p /usr/share/doc/andey-proxy/third-party
  cp -R "$TMP/notices/." /usr/share/doc/andey-proxy/third-party/
fi
case "$SERVICE" in
  procd)
    cp -f "$TMP/andey-proxy.init" /etc/init.d/$BIN_NAME
    chmod 755 /etc/init.d/$BIN_NAME
    if [ ! -f /etc/config/andey-proxy ]; then
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
    if [ -d /usr/share/luci ] && [ -d /www/luci-static ] && [ -d "$TMP/luci" ]; then
      mkdir -p /usr/share/luci/menu.d /usr/share/rpcd/acl.d /www/luci-static/resources/view/andeyproxy /usr/lib/lua/luci/i18n
      cp -f "$TMP/luci/usr/share/luci/menu.d/luci-app-andeyproxy.json" /usr/share/luci/menu.d/
      cp -f "$TMP/luci/usr/share/rpcd/acl.d/luci-app-andeyproxy.json" /usr/share/rpcd/acl.d/
      cp -f "$TMP/luci/www/luci-static/resources/view/andeyproxy/"*.js /www/luci-static/resources/view/andeyproxy/
      cp -f "$TMP/luci/usr/lib/lua/luci/i18n/"andeyproxy.*.lmo /usr/lib/lua/luci/i18n/
      rm -rf /tmp/luci-indexcache /tmp/luci-indexcache.* /tmp/luci-modulecache /tmp/luci-modulecache.*
      /etc/init.d/rpcd restart || true
    fi
    /etc/init.d/$BIN_NAME enable
    /etc/init.d/$BIN_NAME start ;;
  systemd)
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
    systemctl enable --now "$BIN_NAME" ;;
  manual) echo "手动启动: $INSTALL_DIR/$BIN_NAME -cd $CONF_DIR" ;;
esac
if [ -n "$OLD_BINARY" ]; then mv -f "$OLD_BINARY" "$INSTALL_DIR/$BIN_NAME.previous"; OLD_BINARY=""; fi
DONE=1
echo "v${VERSION}安装完成；原账号、配置、设备密钥及证书保留"
echo "后台：https://<设备IP>:16606；首次密码见LuCI设置页或$CONF_DIR/initial-password"
echo "卸载：sudo andey-proxy-uninstall（会删除配置与证书）"
exit 0
__PAYLOAD_BELOW__
