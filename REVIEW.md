# andey-proxy 项目审查报告

> 审查日期：2026-09-05
> 审查范围：安全性、Bug 与代码质量、功能优化方向、UI/UX
> 审查方式：全程只读，未修改任何代码

## 实施状态（2026-09-06 更新）

本报告所列问题已全部实施修复，验证 `go build` / `go vet` / `go test -race` 全部通过：

- **安全性**：M1（`-listen` 绑定参数）、M2（自签证书 SHA-256 指纹首启提示）、M3（敏感操作密码确认限速）、L1（登录时序侧信道）、L2（token 7 天绝对有效期）均已修复；L3/L4/L5 为设计折衷，已在 README 补充说明。
- **Bug**：B1（UDP 回包黑洞）、B2（日志双写）、B3（TCP 半关闭截断）、B4（forward API 失败回滚）均已修复并补测试。
- **隐患**：R1（ACME 可取消退出）、R2（upgrade 暂存竞态）、R3（上传流式化 + 去 ReadTimeout）、R4（后端被动故障摘除 + 切换重试）、R5（Hijack 连接登记关闭）、R6（redirect 转义）、R8（TOTP 计数器持久化）均已修复。
- **代码质量**：Q1（`internal/ids`）、Q2（static handler 缓存）、Q3（`internal/netutil`）、Q4（toggle 响应统一）、Q5（锁外 firewall 同步）、Q6（Clear 防死锁）、Q7（ACME 账户串行化）、Q8（dist 清理 + `.gitignore`）均已完成。
- **前端**：高优 1-5、中优 6-14 全部修复；低优项中对比度（WCAG AA 达标）、缩进混用、退出确认、重签确认也已处理。
- **功能增强**：通知事件总线 + Webhook（`internal/notify`）、站点流量统计面板、强制 HTTPS（TLS 嗅探同端口分流）、配置备份导出/导入（scrypt + AES-GCM，可跨设备迁移）、构建脚本修正均已完成；另新增**中英文双语界面**（vue-i18n，默认中文，顶栏可切换）。
- **明确不做**：多用户/RBAC、HTTP/3、DDNS/ACME provider 抽象合并。

## 总体评价

工程质量高于同类开源路由器管理面板。认证（bcrypt + 随机首启密码 + 强制改密）、配置加密落盘（AES-256-GCM + 原子写 + flock）、升级包 ed25519 签名校验、日志脱敏、CSRF/XSS 防护都做得扎实。

**未发现严重/高危漏洞**。`go vet ./...`、`go build ./...`、`go test -race ./internal/...` 均干净通过。

---

## 一、安全性

### 中风险

#### M1. 管理后台默认监听全部网卡（0.0.0.0）

- 位置：`main.go:157` — `addr := fmt.Sprintf(":%d", *port)`，默认 `:16601` 绑定所有接口。
- 利用场景：OpenWrt 上 WAN 入站默认被防火墙拦截，风险可控；但项目同时发布通用 Linux 二进制，若部署在带公网 IP 的普通 Linux 主机上，管理界面将直接暴露公网，仅由口令（可选 TOTP）+ 自签证书保护，成为爆破/钓鱼目标。
- 修复建议：增加 `-listen` 参数允许绑定地址（如 `127.0.0.1` 或内网网卡 IP）；非 OpenWrt 环境若检测到公网 IP，在日志/dashboard 给出强警告。

#### M2. 管理后台 TLS 默认使用无信任锚的自签证书

- 位置：`internal/webproxy/selfsigned.go:15`、`internal/webproxy/server.go:418`。
- 利用场景：管理员首次通过 `https://<设备IP>:16601` 登录时，路径上的攻击者可中间人替换证书，窃取管理密码乃至 TOTP 验证码（TOTP 在主动 MITM 下可被实时转发）。
- 修复建议：首次启动时将自签证书指纹（SHA-256）打印到 stderr 并提示用户核对；或支持导入自有证书；至少在 README 明确提示该风险。

#### M3. 已认证会话内的密码校验接口无速率限制

- 位置：`internal/api/router.go:214`（改密旧密码校验）、`internal/upgrade/api.go:39`（更新安装密码）、`internal/logcenter/api.go:36`（清空日志密码）、`internal/api/totp.go:187/352`（TOTP 设置/管理密码校验）。
- 利用场景：登录接口有 5 次/5 分钟/IP 限速，但上述接口一旦攻击者获得有效会话，可无限在线爆破旧密码，进而完成改密、关闭 TOTP、清空日志等高危操作。
- 修复建议：对带 `password` 字段的敏感操作复用登录限速机制（按会话或 IP），或连续失败后强制撤销会话。

### 低风险

| 编号 | 问题 | 位置 | 说明 |
|------|------|------|------|
| L1 | 登录用户名时序侧信道 | `internal/api/router.go:154` | 用户名错误时跳过 bcrypt，响应时间差可探测用户名。修复：对固定 dummy hash 执行一次 bcrypt 比较。 |
| L2 | 会话 token 滑动续期、无绝对有效期 | `internal/auth/auth.go:77-88` | 被盗 token 在非浏览器客户端可永久保活。建议增加绝对过期上限（如签发后 7 天）。 |
| L3 | DDNS IP 查询/后端测试是已认证 SSRF 原语 | `internal/ddns/api.go:472`、`internal/ddns/ipsource.go:150`、`internal/webproxy/api.go:52` | 均要求管理员会话，属管理工具固有功能，记录为已知面。 |
| L4 | 配置加密密钥与配置文件相邻存放 | `internal/config/config.go:168` | 只能防御"仅泄露 config.json"场景。路由器场景的合理折衷，README 应注明备份时同时保护 `.key` 文件。 |
| L5 | redirect 子规则 `{path}`/`{query}` 占位符 | `internal/webproxy/redirect.go:23-24` | Go 会拒绝含控制字符的 header，无响应拆分注入；结果最多是异常跳转地址。属预期行为。 |

### 检查通过项（无问题）

- **密码存储**：bcrypt（DefaultCost）；无默认凭据——首次启动生成随机一次性密码、仅打印到 stderr、强制改密（`main.go:93-114`）。
- **会话**：256-bit crypto/rand token，服务端只存 SHA-256 摘要；cookie 为 `HttpOnly + SameSite=Strict + Secure`（HTTPS 模式）；改密/开关 TOTP 后 `RevokeAll`；内存存储不落盘。
- **暴力破解**：登录与 TOTP 登录按直连 IP（不信任 XFF，防伪造）限速 5 次/5 分钟；TOTP 挑战绑定 IP、5 次尝试上限、5 分钟过期、带重放计数器防护；恢复码仅存 SHA-256 哈希。
- **认证绕过**：除 `/api/login`、`/api/login/totp`、`/api/logout` 与静态 SPA 外，全部 API 均在 `requireAuth` 分组内。
- **CSRF**：SameSite=Strict cookie + `verifyOrigin` 中间件（`internal/api/router.go:92`）。
- **XSS**：前端无 `v-html`/`innerHTML`，Vue 默认转义；CSP 为 `script-src 'self'`（无 unsafe-inline/unsafe-eval），另有 `frame-ancestors 'none'`、`X-Frame-Options: DENY`、`nosniff`、HSTS（仅 HTTPS）。
- **路径穿越**：证书文件路径由内部 ID 经 `filepath.Base` 派生；fileserver 用 `http.Dir`；adminweb 用 embed FS。
- **命令执行**：全部 `exec.Command` 均为固定命令+固定参数，无用户输入拼接，不经 shell。
- **注入**：无数据库、无模板引擎；JSON 解码带 1 MiB 限制与 `DisallowUnknownFields`。
- **TLS**：管理面与站点 TLS 均 `MinVersion: TLS1.2`；`InsecureSkipVerify` 仅出现在管理员显式逐规则开启的场景（有 `#nosec` 标注）；ACME CA 地址强制 HTTPS。
- **ACME**：DNS-01 用 lego 官方库；账户私钥 0600 原子写入；错误信息经 `sanitizeError` 剔除服务商凭据后才落配置。
- **敏感信息**：日志写盘前统一 `Redact`；服务商凭据、BasicAuth 密码、自定义请求头在 API 输出中全部脱敏；硬编码 `releasePublicKey` 是 ed25519 公钥，属正常做法。
- **更新机制**：上传包需 ed25519 签名 + SHA256 + ELF 架构校验 + 版本格式校验 + 密码二次确认，安装/回滚原子化——全项目做得最好的部分之一。
- **文件权限**：配置目录 0700、config.json/.key/证书私钥/锁文件均 0600，配置写盘 tmp+rename 原子操作并 fsync，另有 flock 防多实例并发写。

---

## 二、Bug 与代码质量

### Bug

#### B1. UDP 转发回包通道可能永久中断

- 位置：`internal/forward/forward.go:351-366`
- 问题：每个 UDP 会话的回包 goroutine 用 `SetReadDeadline(90s)` 读目标端，目标端 90 秒无响应即退出。但只要客户端持续发包，`lastSeen` 一直刷新，cleanup 不会删除会话，死掉的回包 goroutine 也不会重建——此后目标端回包永远丢失（黑洞）。
- 触发条件：目标服务静默超 90 秒、客户端持续发包的 UDP 业务（游戏/NAT 保活场景）。
- 修复建议：读超时后不直接退出，检查 `sess.lastSeen` 仍活跃则续期继续读；或给 session 加 `done` 标记，主循环发现后重建。

#### B2. forward 模块事件在日志中心重复记录

- 位置：`internal/forward/forward.go:183-190` 与 `main.go:122`
- 问题：`logf` 先 `log.Printf`（main 已把标准日志导入日志中心），紧接着又显式 `logcenter.Add`——同一条事件在内存条目和磁盘日志各出现两次。
- 修复建议：去掉 `log.Printf`（`logcenter.Add` 已够，且保留 entityID 结构化字段）。

#### B3. TCP 转发在一侧结束时立即双关，可能截断数据

- 位置：`internal/forward/forward.go:261-268`
- 问题：任一方向 `io.Copy` 返回，`select` 即返回，defer 同时关掉两条连接。客户端 `shutdown(WR)` 半关闭时，响应还没读回来连接就被拆。
- 修复建议：一端 copy 结束后对另一端 `CloseWrite`（`*net.TCPConn`），再等另一方向完成。

#### B4. forward API 不回检监听启动结果，端口冲突静默落盘

- 位置：`internal/forward/api.go:62, 96, 120, 146`
- 问题：webproxy 的 create/update/toggle 在 `Reload` 失败时回滚配置并返回 409；forward 的 `svc.Reload()` 无返回值，监听失败只在 dashboard 显示 error，API 仍返回成功——行为不一致，用户以为保存成功实际没生效。
- 修复建议：`forward.Service.Reload` 返回每规则最近一次监听错误，API 层对齐 webproxy 回滚语义。

### 隐患

| 编号 | 问题 | 位置 | 说明与建议 |
|------|------|------|-----------|
| R1 | 优雅退出可能被 ACME 申请卡住最长 10 分钟 | `internal/acme/manager.go:511-561`、`main.go:192` | `scan()` 的 `Obtain` ctx 超时 10 分钟且不监听 `stopCh`，`Stop()` 会一直阻塞，SIGTERM 下会被强杀。建议 scan 的 ctx 派生自 manager 级 context，`Stop()` 时 cancel。 |
| R2 | upgrade 暂存包过期清理与安装竞态 | `internal/upgrade/manager.go:197, 212-234` | `Install` 解锁后 `expire()` 可能删掉 staged 二进制。建议 `expire` 跳过 `StateInstalling`，或 `Install` 在锁内转移所有权。 |
| R3 | `ReadTimeout: 30s` 与 100 MiB 升级包上传冲突 | `main.go:158`、`internal/upgrade/manager.go:148-149` | 慢链路上传必然超时；multipart 临时文件在 tmpfs，小内存路由器有 OOM 风险。 |
| R4 | 反向代理无故障转移 | `internal/webproxy/reverse.go:171-179` | 多后端纯轮询，后端宕机后每 N 个请求固定 502。建议 ErrorHandler 对连接类错误换下一个 backend 重试，或维护失败冷却名单。 |
| R5 | WebSocket/Hijack 连接在站点停止后不被关闭 | `internal/webproxy/server.go:275-285` | `http.Server.Shutdown` 不跟踪 Hijack 连接。Go 常见取舍，优先级低。 |
| R6 | redirect 的 `{path}` 占位符注入未转义 | `internal/webproxy/redirect.go:23` | 解码后路径含 `?`、`#` 会改变目标 URL 语义。建议 `url.PathEscape` 逐段处理。 |
| R7 | 登录用户名计时侧信道 | `internal/api/router.go:154` | 同安全 L1。 |
| R8 | TOTP 防重放计数器只在内存 | `internal/api/router.go:33-34, 132-138` | 重启后 30~90 秒窗口内同一动态码可重用一次，风险低。 |

### 代码质量问题

| 编号 | 问题 | 位置 |
|------|------|------|
| Q1 | `newID` 重复实现 3 处且忽略 `rand.Read` 错误 | `internal/forward/api.go:156`、`internal/acme/api.go:48`、`internal/ddns/api.go:54`（webproxy 的 `genID` 则 panic）——建议收敛到公共函数 |
| Q2 | redirect/fileserver 每请求重建 handler | `internal/webproxy/dispatch.go:75-77`——可在 siteServer 上缓存 |
| Q3 | `listenPort` 重复实现 | `internal/forward/forward.go:149` 与 `internal/webproxy/server.go:217` |
| Q4 | toggle 响应不一致：forward 返回 `OK(w, nil)`，webproxy/acme 返回 `{"enabled": bool}` | `internal/forward/api.go:148` |
| Q5 | 过期注释与持锁不一致；`Start` 持锁执行 firewall 外部命令会阻塞整个 Service 锁 | `internal/forward/forward.go:116, 121` |
| Q6 | `logcenter.Clear` 在 `Close` 之后会死锁 | `internal/logcenter/logcenter.go:278`——可加 `select { case c.barrier <- ack: case <-c.done: }` 防御 |
| Q7 | ACME 账户私钥并发首建竞态（后果轻微：多注册一个账户） | `internal/acme/manager.go:181-220` |
| Q8 | `internal/adminweb/dist/assets/` 下有 23 个 macOS " 2.js" 重复文件（git untracked），会被 go:embed 打进二进制 | 建议删除并加 `.gitignore` |

### 做得好的地方

- 配置加密落盘 + 原子写 + 失败回滚设计完整；
- webproxy 的 `cloneSite` 深拷贝避免热重载共享切片的数据竞争；
- 限流器有界（4096 桶 + 淘汰）、登录限速有硬上限；
- 升级包签名校验 + 架构核对 + 原子替换 + 备份回滚链路完整。

---

## 三、功能优化方向

### 缺失功能建议（按价值排序）

| # | 功能 | 价值 | 难度 | 说明 |
|---|------|------|------|------|
| 1 | 通知/Webhook 推送 | 极高 | 低 | `internal/notify/` 是空目录，预留未实现。证书续签失败、DDNS 失败、站点异常对"没人盯的路由器"是刚需。通用 Webhook + Bark/Server酱/Telegram。事件源现成（logcenter 已有 source/level 结构）。 |
| 2 | 后端健康检查与故障摘除 | 高 | 中 | 后端宕机后一半请求 502。先做被动健康检查（连续 N 次失败标记下线、间隔探测恢复）。TCP 转发 `pickTarget` 同理。 |
| 3 | 流量统计 | 高 | 低 | `dispatch.go` 的 statusWriter 已在每个请求出口，挂 atomic 计数器（按站点/规则：请求数、出入字节、状态码分布），Dashboard 加面板即可。 |
| 4 | 配置备份/导入导出 | 高 | 中 | 密钥绑定设备导致换机无法迁移配置。导出时用用户口令重加密，导入时解密+重加密为本机密钥。 |
| 5 | HTTP/3 (QUIC) | 中 | 低 | 注意 mips 二进制已 14MB，quic-go 再增 1.5~2MB，建议构建 tag 或仅大 flash 架构启用。 |
| 6 | DNS 服务商扩展 | 中 | 低 | ACME 已基于 lego（原生 100+ provider），目前只接 3 家，边际成本极低；DDNS 可加自定义 Webhook 通用 provider。 |
| 7 | HTTP→HTTPS 一键跳转 | 中 | 低 | 站点级 `forceHttps` 开关，替代手动建 redirect 规则。 |
| 8 | 管理后台监听地址可配置 | 中（安全） | 极低 | 同安全 M1。 |

**明确不建议做**：多用户/RBAC（家用单管理员足够）、访问日志查看（已有 per-site ring log + logcenter）、限流/IP名单/Basic Auth（均已实现）。

### 现有功能优化点

**可观测性（最值得做）**

- `dispatch.go:37` 每条访问日志同时写 logcenter（约 5MiB 上限，与错误日志混存），站点繁忙时错误历史很快被冲刷。建议访问日志走独立流或采样/聚合写入。
- TCP 转发每建立一条连接就写 ring log + logcenter（`forward.go:259`），高并发扫描会刷爆日志。建议按来源 IP 聚合或限频。
- logcenter 的 `Redact` 每条日志跑 5+ 个正则（`logcenter.go:64-68`），MIPS 软浮点 CPU 上开销可感知，可做快速路径预检。

**性能**

- `server.go:293` 的 `updateSite`：任何非监听配置变更都清空全站 handler 缓存并 `CloseIdleConnections`，改一条规则导致全站连接池重建。建议按规则 ID diff，只重建变更规则。
- TCP 转发双向 `io.Copy` 无空闲超时（`forward.go:261-267`），对端半开会永久挂住 goroutine 和 conntrack 条目。建议加可配置空闲超时（默认 10 分钟）。

**部署体验**

- `Makefile:7`、`build-release.sh:5` 硬编码 `export PATH="/opt/homebrew/bin:$PATH"`——开发者本机路径，应移除。
- `build-release.sh:7` 的 `VERSION` 默认硬编码 `0.2.0`，而 `build-all.sh` 用 `git describe`——版本来源不一致，建议统一从 git tag 取。
- `build-all.sh` 不构建前端（go:embed 依赖 `adminweb/dist`），干净克隆直接跑会得到占位页面二进制。应前置 `make web` 或显式报错。

### 架构建议

1. **落实 `internal/notify` 为事件总线，而非又一个模块**。定义小的 `Event{Type, Entity, Message}` 发布接口，notify 订阅后分发到 webhook/日志/dashboard 待办——一处改动同时解决通知、Dashboard 实时性和后续扩展。当前投入产出比最高的结构性改动。
2. **运行时状态与配置彻底分离**。`Config` 目前既存配置又存运行态（如 `CertConf.NotAfter/LastError`），运行事件触发加密配置落盘，造成 flash 磨损。高频事件应写独立状态文件/内存，配置只在用户编辑时落盘。
3. **站点级中间件链显式化**。`dispatch.go` 里限流→guard→类型分发是硬编码顺序；若加流量统计、强制 HTTPS 等，这段会迅速膨胀。趁规模小抽成显式 middleware 切片。
4. **DDNS 与 ACME 的 DNS provider 抽象合并**。统一成 `DNSProvider` 接口（SetRecord/GetRecord），lego 适配器做默认实现，DDNS 自动获得全部 lego provider 支持，删掉手写实现。

其余架构（config 原子事务、模块注册式路由装配、embed 前端）合理，不建议动。

---

## 四、UI/UX

技术栈：Vue 3.5 + Vite 6 + Element Plus 2.9 + Pinia + vue-router，构建产物内嵌进 Go 二进制（`internal/adminweb`），无 i18n 框架（纯中文硬编码）。

### 高优先级

1. **`web/src/views/Logs.vue:37,41` — 自动刷新丢弃已加载分页**：5 秒轮询整体替换 `entries.value`，"加载更多"追加的历史最多 5 秒后被清掉。建议轮询只拉增量，或翻页后暂停刷新并提示。
2. **`web/src/api/index.js:6` + `web/src/views/DDNS.vue:463` — 15s 全局超时误报长操作失败**：DDNS"执行一次"、证书申请可能超 15s，超时弹"网络错误"但后台其实成功。Dashboard 更新接口已放宽到 120s，建议给 `/ddns/tasks/:id/run`、`/certs/:id/obtain` 同样放宽。
3. **启用开关失败时 UI 状态不回滚**（`Forward.vue:22`、`Certs.vue:40`、`WebService.vue:31`、`DDNS.vue:75`）：`el-switch` 单向绑定 + `@change` 调接口，失败后 catch 不重新 `load()`，开关停留在错误位置。建议失败时重新加载或本地回滚。
4. **`web/src/views/WebService.vue:180,481-482,532-535` — 编辑已有"附加请求头"会清空其值**：打开规则时 header 值置空显示"留空保持不变"，但 `confirmRule` 直接把空值提交。除非后端做了"空值=保留"合并逻辑，否则编辑一次规则就清空所有请求头。需核实后端行为；前端至少不应传空值 key。
5. **`internal/adminweb/dist` 存在 23 个 macOS " 2" 重复副本文件**：会被 `go:embed dist` 打进二进制，建议删除并加 `.gitignore`。

### 中优先级

| # | 问题 | 位置 | 建议 |
|---|------|------|------|
| 6 | 私钥下载无任何确认/提示 | `Certs.vue:48-49` | 弹确认框说明风险，或要求验证密码（参考清日志做法） |
| 7 | 安装更新后缺少明确结果反馈 | `Dashboard.vue:70` | done/failed 时弹 Notification，并重新加载页面数据 |
| 8 | 健康轮询把网络抖动显示为"需要处理" | `Layout.vue:117` | 区分"请求失败"（显示"状态未知"）与"有 issue"；Layout 与 Dashboard 轮询同一接口可共享 |
| 9 | 登录表单缺 `autocomplete` 属性 | `Login.vue:11,14` | 补 `username` / `current-password` / `one-time-code` |
| 10 | 子规则表单裸 `required` 星号无校验规则 | `WebService.vue:133,148,226,243` | 改为标准 rules 校验，错误定位到字段 |
| 11 | 401 跳转丢失回跳地址 | `api/index.js:37` | 拼接当前路径为 redirect 参数 |
| 12 | `validate()` 拒绝时未处理 Promise rejection | `Login.vue:70` | try/catch 或 `.catch(()=>{})` |
| 13 | 硬编码颜色与主题变量并存 | `Certs.vue:293`、`WebService.vue:632`（`#f56c6c`）、大量 `#999`/`#333` | 统一到 `--ap-*` CSS 变量 |
| 14 | 时间格式不一致 | `Certs.vue:123`（`zh-CN`）vs `Dashboard.vue:73` 等（无参 `toLocaleString()`） | 抽 `formatTime` 工具函数全局复用 |

### 低优先级

- **可访问性**：Login 表单只有 placeholder 无可见 label；`--ap-muted: #6d8287` 对比度约 3.9:1 低于 WCAG AA 4.5:1；表格操作列 `link` 按钮移动端触控目标偏小。
- **代码风格**：`DDNS.vue`、`store/auth.js`、`WebService.vue` tab/空格缩进混用；`Layout.vue:123`、`Dashboard.vue:79` 整段 CSS 压成单行。
- 退出登录无确认（`Layout.vue:104`）；证书"重签"按钮无确认易误点消耗 CA 限额。

### 做得好的地方

- 危险操作确认体系分层清晰：删除用 `el-popconfirm`，清日志/装更新/关 2FA 要求密码，恢复码强制勾选"已保存"——超过同类项目平均水平；
- 空状态（`el-empty`）覆盖全面且文案带引导；
- 安全意识融入 UI：明文 HTTP 警告、HTTPS 才允许绑定 TOTP、降级包需显式勾选；
- 响应式有实际处理：900px 抽屉导航、767px 对话框全宽、599px 表单单列；
- 无 XSS 风险：全项目无 `v-html`/`innerHTML`；
- axios 拦截器统一错误提示，模式一致。

### 整体 UX 改进方向（按价值排序）

1. **修正状态同步类 bug**（日志分页被吞、开关不回滚、header 值清空）——管理工具的状态不可信是致命的；
2. **统一反馈规范**：长操作超时、更新完成通知、401 回跳，让"操作是否成功"100% 可感知；
3. **设计令牌收口**：散落色值收进 `--ap-*` 变量，时间格式抽工具函数；
4. **清理 dist 重复文件**并防止再次混入；
5. 若未来有多语言需求，尽早引入 `vue-i18n`，越晚成本越高。

---

## 五、行动优先级总结

投入产出比最高的 6 件事：

1. 加 `-listen` 绑定参数（安全 M1，难度极低）
2. 敏感操作复用登录限速（安全 M3）
3. 修 UDP 回包黑洞（B1）和 TCP 半关闭截断（B3）
4. 删除 `adminweb/dist` 重复文件并加 `.gitignore`（Q8）
5. 修前端开关不回滚 + 日志轮询吞分页（UI 高优 1、3）
6. 实现 `internal/notify` 事件总线（功能建议 #1 + 架构建议 #1）
