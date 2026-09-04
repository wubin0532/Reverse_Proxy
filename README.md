# luckyx 反向代理

一款轻量的多合一网络工具，专为路由器和低功耗设备设计，单二进制运行，内置 Web 管理后台。

## 功能

- **Web 服务 / 反向代理**:多站点监听，按域名 + 路径前缀分发子规则
  - 反向代理(多后端轮询、透传 Host、自定义请求头、Basic Auth)
  - 301/302 跳转
  - 静态文件服务
- **HTTPS 证书**:ACME 自动申请与续签(DNS-01,支持泛域名),按 SNI 自动供给证书;无证书时回退自签
  - 支持阿里云、Cloudflare、DNSPod
- **端口转发**:TCP / UDP 四层转发,带实时日志
- **DDNS 动态域名**:定时检测 IP 变化并更新 DNS 记录
  - IP 来源:网卡 / 自定义 API / Webhook
  - 支持 IPv4 / IPv6,阿里云、Cloudflare、DNSPod
- **安全防护**:IP 黑白名单(支持 CIDR)、User-Agent 黑白名单,转发与 Web 服务共用
- **管理后台**:Vue 3 + Element Plus,通过 `go:embed` 嵌入二进制,无需额外部署

## 快速开始

### 下载

从 [Releases](https://github.com/wubin0532/Reverse_Proxy/releases) 下载对应架构的二进制,支持:

| 架构 | 适用设备 |
|------|---------|
| x86_64 | 普通 Linux 服务器 / 软路由 |
| arm64 | ARM64 路由器、树莓派 4+ |
| armv7 | 32 位 ARM 设备 |
| mips / mipsle | MT7621 等 MIPS 路由器(softfloat) |

### 运行

```bash
./luckyx              # 默认后台端口 16601,配置目录 ./luckyxconf
./luckyx -p 8080      # 指定后台端口
./luckyx -cd /etc/luckyx  # 指定配置目录
```

启动后访问 `http://<设备IP>:16601`,**默认账号 `666` / 密码 `666`**(请登录后立即修改)。

### 从源码构建

需要 Go 1.22+ 和 Node.js:

```bash
make build          # 构建前端 + 本机二进制(输出 luckyx)
./scripts/build-all.sh  # 交叉编译全部架构(输出 dist/)
```

前端产物通过 `go:embed` 嵌入二进制,构建 Go 代码前需先执行 `make web`(或 `cd web && npm install && npm run build`)。

### OpenWrt 安装

`package/openwrt/` 提供了 OpenWrt 包定义,可用 OpenWrt SDK 编译为 ipk,详见该目录下 Makefile 头部注释。默认配置目录 `/etc/luckyx`。

## 目录结构

```
├── main.go              # 入口:加载配置、启动各模块与后台 HTTP 服务
├── internal/
│   ├── webproxy/        # Web 服务/反向代理核心(站点监听、子规则分发、访问日志)
│   ├── forward/         # TCP/UDP 端口转发
│   ├── ddns/            # DDNS 调度器与 DNS 服务商实现
│   ├── acme/            # ACME 证书申请、续签、SNI 供给(基于 lego)
│   ├── guard/           # IP / UA 黑白名单
│   ├── adminweb/        # 嵌入的前端静态资源
│   ├── api/             # 后台 REST API
│   ├── auth/            # 登录认证
│   └── config/          # 配置读写(JSON,带锁)
├── web/                 # 前端源码(Vue 3 + Vite + Element Plus)
├── package/openwrt/     # OpenWrt ipk 打包
└── scripts/build-all.sh # 多架构交叉编译
```

## 配置

所有配置保存在配置目录(默认 `./luckyxconf`)下的 JSON 文件中,通过 Web 后台管理,修改后即时生效,无需重启。DNS 服务商凭据在 DDNS 与 ACME 模块间共用。

## CI

推送到 `main` 或打 `v*` 标签时,GitHub Actions 自动运行 `go vet`、`go test` 并构建全部 5 种架构的二进制(见 [Actions](https://github.com/wubin0532/Reverse_Proxy/actions))。

## License

[MIT](LICENSE)
