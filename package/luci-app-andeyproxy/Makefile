# luci-app-andeyproxy：LuCI 菜单与设置页（服务 -> andey-Proxy）
# 与 package/openwrt/Makefile 用法相同，放入 SDK package/ 下编译

include $(TOPDIR)/rules.mk

LUCI_TITLE:=andey-Proxy 管理
LUCI_DEPENDS:=+andey-proxy
LUCI_PKGARCH:=all

PKG_NAME:=luci-app-andeyproxy
PKG_VERSION:=0.1.5
PKG_RELEASE:=1

include $(TOPDIR)/feeds/luci/luci.mk

# 本地源码构建：root/ 目录直接打包
define Build/Prepare
endef
define Build/Configure
endef
define Build/Compile
endef

define Package/luci-app-andeyproxy/install
	$(CP) ./root/* $(1)/
endef

define Package/luci-app-andeyproxy/postinst
#!/bin/sh
[ -n "$${IPKG_INSTROOT}" ] && exit 0
rm -rf /tmp/luci-indexcache /tmp/luci-modulecache
exit 0
endef

$(eval $(call BuildPackage,luci-app-andeyproxy))
