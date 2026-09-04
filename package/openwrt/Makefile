# luckyx OpenWrt 包定义
#
# 使用方法（需要 Linux x86_64 主机 + OpenWrt SDK）：
#   1. 下载解压对应目标平台的 OpenWrt SDK
#   2. ./scripts/feeds update -a && ./scripts/feeds install -a（至少需要 packages feed 的 golang）
#   3. mkdir -p package/luckyx && cp -r <本目录>/* package/luckyx/
#      ln -sfn <仓库根目录> package/luckyx/src
#   4. 先构建前端：仓库根目录执行 make web（go:embed 需要 dist 产物）
#   5. make package/luckyx/compile V=s
# 产物在 bin/packages/<arch>/base/luckyx_<version>_<arch>.ipk

include $(TOPDIR)/rules.mk

PKG_NAME:=luckyx
PKG_VERSION:=0.1.0
PKG_RELEASE:=1

PKG_LICENSE:=MIT
PKG_MAINTAINER:=luckyx

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

GO_PKG:=luckyx
GO_PKG_BUILD_PKG:=luckyx
GO_PKG_LDFLAGS_X:=main.version=$(PKG_VERSION)
GO_PKG_LDFLAGS:=-s -w

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

# 本地源码构建：src 为指向 luckyx 仓库根目录的符号链接（或副本）
define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) $(CURDIR)/src/. $(PKG_BUILD_DIR)/
endef

define Package/luckyx
  SECTION:=net
  CATEGORY:=Network
  TITLE:=DDNS/反向代理/ACME证书一体工具
  DEPENDS:=$(GO_ARCH_DEPENDS) +ca-bundle
endef

define Package/luckyx/description
  参考 lucky 的软硬路由公网工具：动态域名解析（阿里云/Cloudflare/DNSPod）、
  Web服务（反向代理/重定向/文件服务）、ACME 自动证书（DNS-01）、TCP/UDP 端口转发。
  默认后台端口 16601，默认账号密码 666/666，首次登录请修改。
endef

define Package/luckyx/conffiles
/etc/config/luckyx
/etc/luckyx/
endef

define Package/luckyx/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/luckyx $(1)/usr/bin/luckyx
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/luckyx.init $(1)/etc/init.d/luckyx
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/luckyx.config $(1)/etc/config/luckyx
	$(INSTALL_DIR) $(1)/etc/luckyx
endef

define Package/luckyx/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0
/etc/init.d/luckyx enable
echo "luckyx 已安装，后台: http://<路由IP>:16601  默认账号密码 666/666"
echo "启动: /etc/init.d/luckyx start（或将 /etc/config/luckyx 中 enabled 置 1 后 reload）"
exit 0
endef

$(eval $(call GoBinPackage,luckyx))
$(eval $(call BuildPackage,luckyx))
