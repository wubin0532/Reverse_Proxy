# andey-Proxy OpenWrt 包定义
#
# 使用方法（需要 Linux x86_64 主机 + OpenWrt SDK）：
#   1. 下载解压对应目标平台的 OpenWrt SDK
#   2. ./scripts/feeds update -a && ./scripts/feeds install -a（至少需要 packages feed 的 golang）
#   3. mkdir -p package/luci-app-andeyproxy && cp -r <本目录>/* package/luci-app-andeyproxy/
#      ln -sfn <仓库根目录> package/luci-app-andeyproxy/src
#   4. 先构建前端：仓库根目录执行 make web（go:embed 需要 dist 产物）
#   5. make package/luci-app-andeyproxy/compile V=s
# 产物在 bin/packages/<arch>/base/luci-app-andeyproxy_<version>_<arch>.ipk
#
# 注：Release 中已提供免 SDK 构建的 .ipk，可直接 opkg install

include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-andeyproxy
PKG_VERSION:=0.2.2
PKG_RELEASE:=1

PKG_LICENSE:=MIT
PKG_MAINTAINER:=andey

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

GO_PKG:=andey-proxy
GO_PKG_BUILD_PKG:=andey-proxy
GO_PKG_LDFLAGS_X:=main.version=$(PKG_VERSION)
GO_PKG_LDFLAGS:=-s -w

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

# 本地源码构建：src 为指向仓库根目录的符号链接（或副本）
define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) $(CURDIR)/src/. $(PKG_BUILD_DIR)/
endef

define Package/luci-app-andeyproxy
  SECTION:=luci
  CATEGORY:=LuCI
  TITLE:=andey-Proxy DDNS/反向代理/ACME证书一体工具
  DEPENDS:=$(GO_ARCH_DEPENDS) +ca-bundle
  CONFLICTS:=andey-proxy
endef

define Package/luci-app-andeyproxy/description
  参考 lucky 的软硬路由公网工具：动态域名解析（阿里云/Cloudflare/DNSPod）、
  Web服务（反向代理/重定向/文件服务）、ACME 自动证书（DNS-01）、TCP/UDP 端口转发。
  默认后台端口 16606 并启用 HTTPS，首次密码随机生成。
endef

define Package/luci-app-andeyproxy/conffiles
/etc/config/andey-proxy
/etc/andey-proxy/
endef

define Package/luci-app-andeyproxy/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/andey-proxy $(1)/usr/bin/andey-proxy
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/andey-proxy.init $(1)/etc/init.d/andey-proxy
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/andey-proxy.config $(1)/etc/config/andey-proxy
	$(INSTALL_DIR) $(1)/etc/andey-proxy
	$(CP) ./luci/* $(1)/
endef

define Package/luci-app-andeyproxy/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0
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
if [ "$$(uci -q get andey-proxy.main.enabled)" = "1" ]; then
	/etc/init.d/andey-proxy restart 2>/dev/null
fi
rm -rf /tmp/luci-indexcache /tmp/luci-modulecache
/etc/init.d/rpcd restart 2>/dev/null
echo "andey-Proxy 已安装，后台: https://<路由IP>:16606（首次密码见启动日志）"
echo "启动: /etc/init.d/andey-proxy start（或将 /etc/config/andey-proxy 中 enabled 置 1 后 reload）"
exit 0
endef

define Package/luci-app-andeyproxy/postrm
#!/bin/sh
# 升级时 opkg 也会执行旧包 postrm（参数 upgrade），绝不能删数据
[ -n "$${IPKG_INSTROOT}" ] && exit 0
rm -rf /tmp/luci-indexcache /tmp/luci-modulecache
[ "$$1" = "upgrade" ] && exit 0
rm -rf /etc/andey-proxy
rm -f /etc/config/andey-proxy-opkg
exit 0
endef

$(eval $(call GoBinPackage,luci-app-andeyproxy))
$(eval $(call BuildPackage,luci-app-andeyproxy))
