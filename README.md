# andey-proxy 反向代理

一款轻量的多合一网络工具，专为路由器和低功耗设备设计，单二进制运行，内置 Web 管理后台。

## 功能

- **Web 服务 / 反向代理**:多站点监听，按域名 + 路径前缀分发子规则
  - 独立 HTTP/2 后端连接池、多后端轮询、WebSocket、SSE 与长连接
  - 自动可信代理头、透传 Host、只写自定义请求头、Basic Auth、后端自签 TLS
  - 子规则热更新、连接/响应超时、路径前缀移除及后端连接测试
  - 可选按直接客户端 IP 限速、请求体上限、Location 与 Cookie Domain/Path 改写
  - 站点级"强制 HTTPS"开关：同端口明文/TLS 嗅探分流，明文请求 301 跳转
  - 站点流量统计面板：请求数、状态码分布与出入流量（内存统计，重启清零）
  - 301/302 跳转
  - 静态文件服务
- **HTTPS 证书**:ACME 自动申请与续签(DNS-01,支持泛域名),按 SNI 自动供给证书;无证书时回退自签
  - 支持阿里云、Cloudflare、DNSPod
- **端口转发**:TCP / UDP 四层转发,带实时日志
  - 规则级 `idleTimeout` TCP 空闲超时（默认 600 秒，仅 API/配置字段）
- **DDNS 动态域名**:定时检测 IP 变化并更新 DNS 记录
  - IP 来源:网卡 / 自定义 API
  - 支持 IPv4 / IPv6,阿里云、Cloudflare、DNSPod
- **安全防护**:IP 黑白名单(支持 CIDR)、User-Agent 黑白名单,转发与 Web 服务共用
- **管理后台**:Vue 3 + Element Plus,通过 `go:embed` 嵌入二进制,无需额外部署
- **运维控制台**:健康概览、防火墙、手动更新和最近错误统一管理
- **日志中心**:结构化查询、下载与审计，磁盘占用上限约 5 MiB
- **通知推送**:通用 Webhook，支持证书 / DDNS / 站点 / 转发事件，Dashboard 中配置订阅类型与推送地址
- **配置备份**:Dashboard 导出/导入加密备份文件（口令派生密钥，可跨设备迁移）
- **账户安全**:可选 Google Authenticator 双重验证、一次性恢复码和设备本机重置

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
./andey-proxy              # 默认后台端口 16601,配置目录 ./andey-proxy-conf
./andey-proxy -p 8080      # 指定后台端口
./andey-proxy -listen 192.168.1.1  # 只绑定指定网卡地址（默认监听全部网卡）
./andey-proxy -cd /etc/andey-proxy  # 指定配置目录
```

启动后访问 `https://<设备IP>:16601`。无匹配的 ACME 证书时会使用自签证书，浏览器首次需确认。账号默认为 `admin`，一次性随机密码只在首次启动的控制台输出一次。

如必须兼容无法使用 HTTPS 的旧客户端，可显式传入 `-admin-http`；该模式会在首页持续显示高风险警告。

### 从源码构建

需要 `go.mod` 指定的 Go 版本和 Node.js:

```bash
make build          # 构建前端 + 本机二进制(输出 andey-proxy)
./scripts/build-all.sh  # 交叉编译全部架构(输出 dist/)
```

前端产物通过 `go:embed` 嵌入二进制,构建 Go 代码前需先执行 `make web`(或 `cd web && npm ci && npm run build`)。

### OpenWrt 安装

`package/openwrt/` 提供了 OpenWrt 包定义,可用 OpenWrt SDK 编译为 ipk,详见该目录下 Makefile 头部注释。默认配置目录 `/etc/andey-proxy`。

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
│   ├── logcenter/       # 结构化日志、轮转、下载与审计
│   └── config/          # AES-256-GCM 加密配置与原子事务
├── web/                 # 前端源码(Vue 3 + Vite + Element Plus)
├── package/openwrt/     # OpenWrt ipk 打包
└── scripts/build-all.sh # 多架构交叉编译
```

## 配置

所有配置使用 AES-256-GCM 整体加密保存。设备密钥独立保存；OpenWrt 默认为 `/etc/andey-proxy.key`，权限 `0600`。DNS Token、Basic Auth 密码和自定义请求头都是只写字段，API 不返回明文。

安全注意事项：备份或迁移配置目录时，`.key` 文件（配置加密密钥）与配置本体同等敏感，须一并保护、切勿随备份外泄；丢失 `.key` 则既有加密配置无法解密。Dashboard 的备份导出使用独立口令派生密钥，不受 `.key` 影响。使用自签证书首次访问管理后台时，请对照启动日志中输出的 SHA-256 指纹核对浏览器提示的证书指纹后再信任。

### 代理行为与资源限制

- 后端故障自动重试仅适用于无请求体的 GET、HEAD、OPTIONS，最多切换一次；POST、PUT、PATCH、DELETE 等请求失败时直接返回错误，避免重复执行操作。重试保留原始路径、查询参数和公网 Host 信息。
- 在匹配子规则与鉴权之前拒绝含 `.` / `..` 路径段、重复斜杠或反斜杠的歧义路径（包括解码后出现这些内容的请求），返回 400。移除前缀时保留剩余路径的百分号编码。
- 静态文件服务将文件访问限制在配置的根目录内，拒绝指向根目录外的符号链接。根目录内的符号链接、隐藏文件及目录列表仍可访问，因此根目录应只放准备公开的内容。
- 同端口 HTTP/TLS 分流每站点最多保留 128 个待嗅探连接，首字节等待上限 10 秒；达到上限后关闭新的连接。
- 四层端口转发在所有规则间共享 256 条 TCP 连接、256 个活跃 UDP 会话的上限。达到上限时关闭新 TCP 连接、丢弃新 UDP 会话的数据包，已有会话继续运行；每条 UDP 规则的会话索引也限制为 256 项。这些是当前版本的固定资源上限。
- 规则热更新只重建发生变化的处理器和连接池。持有旧规则快照、尚未取得处理器的请求可能收到 503，随后新请求使用新配置。
- 配置备份 v1 只接受程序导出时使用的固定 scrypt 参数（约 32 MiB 派生内存）。导入与导出互斥，已有操作进行时返回 409；畸形加密字段在派生密钥前被拒绝。

### Google Authenticator 双重验证

在管理面板右上角打开“账户安全”，输入当前密码后即可绑定 Google Authenticator 或其他兼容 RFC 6238 的验证器。双重验证默认关闭；启用、关闭或重新生成恢复码后，全部已有会话都会失效并要求重新登录。

绑定时生成的 10 个恢复码只显示一次，请下载或离线妥善保存。管理后台使用明文 HTTP 时，为避免验证码和绑定密钥被窃听，登录第二步及所有双重验证管理接口都会被拒绝。

如果验证器和恢复码均丢失，可在设备本机先停止服务，再执行一次性重置：

```bash
/etc/init.d/andey-proxy stop
/usr/bin/andey-proxy -cd /etc/andey-proxy -reset-totp
/etc/init.d/andey-proxy start
```

重置命令通过配置锁确认服务已经停止；服务仍在运行时会拒绝修改配置。该命令仅关闭双重验证，不会修改管理员密码或其他业务配置。

## 手动更新

程序不会主动连网检查版本，也不会从 GitHub 或其他站点下载并执行脚本。请自行下载对应架构的签名 `.run` 包，在首页“上传包手动更新”中先检查签名、摘要、Linux/CPU/ELF 架构和版本，再输入管理密码安装。

发布包使用 Ed25519 签名。构建 `.run` 前必须通过 `RELEASE_SIGNING_KEY` 指定发布私钥；GitHub Actions 使用名为 `RELEASE_SIGNING_PRIVATE_KEY` 的受保护 Secret。私钥不得提交到仓库或写入产物。

## CI

推送到 `main` 或打 `v*` 标签时,GitHub Actions 从 `go.mod` 读取 Go 版本，运行前端测试与构建、`go vet ./...`、`go test -race ./...`、依赖漏洞扫描并构建全部 5 种架构的二进制。标签构建还会生成五架构签名 `.run` 与 OpenWrt `.ipk` 产物(见 [Actions](https://github.com/wubin0532/Reverse_Proxy/actions))。

## License

[MIT](LICENSE)
