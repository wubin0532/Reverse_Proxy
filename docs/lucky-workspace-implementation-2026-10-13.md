# Lucky 工作台功能扩展交付记录

> 此为首轮记录；“完成 A+B”不等于最终验收。后续审查发现并修复 DDNS 请求、并发和排版缺陷，当前结果以[第二轮审查与修正](lucky-second-review-2026-10-13.md)为准。

2026-10-13；基线 `180f365bd5031492a781c77bb141963aa20d9b05`；分支 `codex/lucky-workspaces`。本次完成方案 A+B 的本地实现及隔离验收，代码尚未提交、推送或部署到路由器。Lucky 页面与发布前端的分析记录保留在同目录；没有取得 Lucky 后端源码，因此本项目后端是结合现有架构实现的功能，不代表已经确认 Lucky 后端的内部算法。

## 功能与接入

| 模块 | 已实现 | 主要入口 |
|---|---|---|
| 动态域名 | 仍只有阿里云、Cloudflare、DNSPod；任务下管理 A/AAAA 记录、独立启停/TTL/备注、IPv4/IPv6 网卡或 HTTP API 取源；记录状态及有界历史；立即执行、强制远端核查；同 IP 下配置或 TTL 变化重新核查；部分失败分别展示 | `internal/ddns/task.go`、`worker.go`、`records_api.go`、三家适配器、`web/src/views/DDNS.vue` |
| Web 服务 | 站点标签、规则搜索/状态/分组、触屏上下排序；站点和子规则引用共享策略；自定义未匹配响应；站点及规则版本冲突保护；规则与健康检查一次提交、失败回滚 | `internal/webproxy/workspace.go`、`api.go`、`dispatch.go`、`WebService.vue`、`SiteDialog.vue`、`RuleEditor.vue` |
| 证书 | ACME/PEM 来源；导入及按原 ID 替换，校验密钥匹配、SAN、用途、有效期及链顺序；P256/P384/RSA2048/RSA4096 签发算法；统一剩余天数和绑定站点；SNI/显式绑定供给使用同一证书对锁；失败保存恢复原材料与状态 | `internal/acme/import.go`、`bound.go`、`request.go`、`manager.go`、`Certs.vue` |
| 防护中心 | 命名 IP/CIDR/UA 策略、可选全局约束、引用删除保护；临时封禁/解除/到期、事件及计数；策略热改；直连 Web、既有 Tunnel 入口、TCP 新连接、UDP 每个入站包接入；转发编辑器可选择共享策略 | `internal/guard/engine.go`、`api.go`、`webproxy/dispatch.go`、`forward/forward.go`、`Protection.vue`、`Forward.vue` |
| 总览 | Linux 主机 CPU/内存、默认 IPv4 路由网卡计数差分；程序堆内存/协程；1h/24h 有界资源历史；DDNS、最近证书到期、防护拒绝、活动请求；卡片局部失败保留最近数据；隐藏页暂停、轮询防重叠 | `internal/metrics/`、`internal/dashboard/workspace.go`、`Dashboard.vue`、`ResourceChart.vue`、`useIntervalFn.js` |

全部新接口通过原管理认证路由挂载。管理账户安全独立于业务防护，业务误封不会封死管理入口。没有绑定的旧业务配置保持原有本地准入行为；共享策略、本地名单均需允许。UA 仅作用于 HTTP，不作为身份认证。

新增接口包括 `/api/protection/{policies,bans,events,summary}`、`/api/metrics`、`/api/metrics/history`、`/api/dashboard/workspace`、`/api/certs/import`、`/api/ddns/tasks/{id}/records` 与任务历史。

## 数据与兼容

- 新根配置 `schemaVersion=1`、共享策略、绑定、记录、证书来源/算法、Web 分组/默认响应/版本，经加密配置持久化；加载、修改、恢复统一校验，写入失败保留原配置，拒绝未知未来版本。
- 旧单栈 DDNS 用稳定虚拟记录 ID 展示；首次记录编辑转换为新模型。未实现的历史 webhook 来源仍迁移为停用，不启用新外部调用。
- 记录修改使用任务 revision；Web 子规则修改使用规则 revision，新建与重排使用站点 revision；开关及删除用 `If-Match`。旧 revision=0 配置保留首次兼容，随后要求版本。
- DNSPod 既有线路、Cloudflare 既有代理状态在更新时保留，本轮不开放线路/代理模式配置。阿里云及 DNSPod TTL=0 保留服务商默认行为；Cloudflare 使用自动 TTL。
- PEM 材料不进入普通配置响应；私钥文件权限 0600。导入证书不执行自动 ACME 续签，需要人工导入新材料替换。
- 任务同步历史最多 100 条/任务；临时封禁最多 2048 条、有效期最长 30 天；防护事件最多 512 条。它们和拒绝计数、资源历史均为进程内存数据，重启清空。
- 资源历史实时样本最多 720 条、分钟样本最多 1440 条，查询同时按 1h/24h 时间过滤；首个差分或不可用指标显示 null。当前非 Linux 主机仅显示可取得的程序指标；仅 IPv6 默认路由时网卡速率显示不可用。
- 现有配置备份不包含 `certs/` 的证书/私钥或独立运行状态；不能将配置导出当作完整证书迁移包。文件级迁移时须同时保存配置目录、其同级的 `.key` 加密密钥与证书材料，避免用旧程序覆盖新字段。

## 验收结果

| 检查 | 最终结果 |
|---|---|
| 前端 `npm test` | 12 文件、35 测试通过；无未处理错误 |
| `npm run build` | 成功；嵌入前端已更新 |
| `go vet ./...` | 成功 |
| `go test -race -json ./... -timeout 120s` | 17 测试包、290 顶级测试通过，含子测试共 376 条 PASS；无 FAIL |
| 嵌入资源引用 | `TestAllBuiltAssetReferencesAreEmbedded` 通过，覆盖构建后资源 |
| Linux 交叉编译 | amd64、arm64、armv7、mipsle softfloat、mips softfloat，全部成功 |
| macOS arm64 原生构建 | 成功；正式代码构建不含临时预览初始化文件 |
| `git diff --check` | 通过 |
| 隔离实例完整 HTTP API | 24 个认证调用按预期返回，业务封禁 403、解除 200；管理指标仍 200 |
| 真实 TLS 请求 | 证书导入替换期间 80 个带 SNI/不带 SNI 请求，均校验证书并成功握手；验证器信任两张本地测试证书，验证域名 `home.example.com` |
| 真实 UDP | 同一个 socket 和已有会话，策略热改/临时封禁后后续入站包被拒绝，解除后回显恢复；拒绝计数为 2 |
| 浏览器 UI | 五模块 390px 与 1440px 布局检查，页面 scrollWidth 等于 clientWidth；Web 编辑器显示绑定策略并成功保存；通过防护页面新增/解除本机测试封禁 |

关键回归还覆盖：旧 DDNS 兼容、同 IP 改域名/TTL、远端漂移、双栈部分失败、三家 TTL-only 更新、失败不推进 IP 缓存、配置写失败回滚、规则/健康原子保存、旧版本 409、伪造代理头不能绕过站点策略、未匹配响应也执行站点防护、非法/过期/CA/CN-only/用途不符证书拒绝、导入 SAN 覆盖保护、并发读取替换与 key-only 缓存失效、旧 ACME 结果因来源/算法变化被丢弃、指标差分/计数复位/睡眠间隔过滤及有界历史、页面隐藏和轮询防重叠。

前端 PEM 流程单测隔离与提交无关的 Select 组件；真实浏览器另外验证原组件及替换表单，无页面错误。所有 DNS、IP 来源、CA/TLS、转发对象为本机假服务或保留测试地址；没有使用正式 DNS 凭据、申请公网站点证书或更改现网防护。

机器可读摘要：[验收证据](lucky-workspace-validation-2026-10-13.json)。截图均为独立测试实例：

- [桌面总览](screenshots/lucky-workspace/dashboard-desktop.jpg)
- [DDNS 手机布局](screenshots/lucky-workspace/ddns-mobile.jpg)
- [Web 手机布局](screenshots/lucky-workspace/web-mobile.jpg)
- [证书手机布局](screenshots/lucky-workspace/certs-mobile.jpg)
- [防护封禁手机布局](screenshots/lucky-workspace/protection-ban-mobile.jpg)

构建脚本沿用基线 Git 标签，产物文件名仍含 `v0.3.7`；这是本地验证产物，没有创建新的版本或发布。预览初始化仅通过 `/tmp` overlay 注入，正式源码、正式构建、交叉编译均不含预览账户/配置。

## 尚未验收与本轮边界

未部署到真实 OpenWrt/LibWrt，也没有做真实手机、Linux `/proc` 实机资源曲线、正式 DNS 写入、公网 CA 签发、现网 Tunnel/防火墙验收。源码和交叉编译通过不能替代这些验收。Linux 指标解析与 Tunnel 既有信任边界通过测试，不宣称已经完成真实设备验收。

临时封禁不主动断开已有 TCP/WebSocket；UDP 返回流量不在本轮关闭承诺内。Web 业务字节统计延续现有口径，未扩展为完整未知长度上传、SSE/WebSocket 实时字节统计。

C/D 尚未开发：STUN/DNS 来源回退、更多记录/服务商、EAB/传播参数/文件映射、独立后端池、持久资源历史、身份安全组、GeoIP、WAF/CC、诱捕、服务发现、WebDAV/CGI、远程证书同步。它们不会因参考页面展示过入口而被标记完成。
