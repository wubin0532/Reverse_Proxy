# Cloudflare Tunnel

从管理后台的“Cloudflare 隧道”页面管理实例，支持后台新建、已有 Token 接入、启停与重启、域名路由、日志及通知。主程序通过子进程管理外部 cloudflared，未引入 Cloudflare SDK、Prometheus 客户端或新的前端组件库。

## 准备环境

在“cloudflared 环境”点击“检查最新版本”，程序会按当前 Linux 架构匹配官方稳定版本。输入管理密码后点击安装；下载到配置目录的 `runtime/cloudflared`，优先使用受管理版本，系统已有程序保持原样。也可通过系统包或手动安装，将可执行文件放在服务用户的 `PATH` 中。需要 **2025.4.0 或更新版本**；页面显示来源、路径、架构、版本、大小和安装进度。

安装和更新均由管理员点击触发，不会自行升级，也不会创建另一个系统 cloudflared 服务。版本检查缓存一小时，支持主动刷新；只下载官方 HTTPS 资源，校验 SHA-256、ELF 架构和版本，单次最高 80 MiB，并预检磁盘空间。更新前列出启用实例，要求确认重启；校验通过才切换，失败恢复旧程序和原启用状态，重启后恢复安装日志并清理中断下载。若同一隧道已由系统服务运行，先停用该连接器，或使用独立隧道。cloudflared 子进程与主程序使用相同用户，从而访问权限为 `0600` 的本机 Socket。

Linux/OpenWrt 的 x86_64、ARM64、ARMv7 为首版目标平台；MIPS/MIPSLE 保留主项目构建，Tunnel 显示未验证状态，不承诺存在可用的官方 cloudflared。交叉编译成功不等于在真实设备上完成运行验证。

## 后台新建

1. 打开“账户凭据”，添加名称、32 位 Account ID 和 Cloudflare API Token。
2. Token 需要目标账户的 **Cloudflare Tunnel 编辑**、所选 Zone 的 **DNS 编辑**和 **Zone 读取**权限。“验证读取权限”只检查账户及域名的读取权限，实际写权限在云端操作时检查。这些凭据独立保存，不会扩大已有 DDNS/ACME 凭据权限。
3. “添加实例”选择“后台新建”并绑定账户，默认连接协议和出口 IP 版本均为 `auto`。保存后后台创建远程管理隧道并取得 Tunnel Token；失败时页面保留阶段及“检查并重试”入口。
4. 打开“域名路由”，填写完整公网域名和所属 Zone，选择已启用的本项目站点，或无内嵌凭据的 HTTP/HTTPS 服务地址。路径分发继续使用站点原有子规则。
5. 保存本机路由后，刷新云端配置并执行“同步路由与 DNS”。同步成功后启用实例，分别确认“运行中”“连接已就绪”和“已同步”，再访问公网域名。

源站 HTTPS 默认验证证书；只有明确使用自签证书时才开启“跳过源站 TLS 验证”。直连服务的请求不会经过本项目站点的鉴权、限流或流量统计。禁止把本机管理监听端口设为直连目标。

发布本项目站点时，站点子规则的域名匹配也需要包含该公网域名，或使用原有的不限域名规则；隧道不会自动改写站点子规则。

## 已有 Token 接入

选择“已有 Token”，填写 Tunnel Token 即可保存并启用；系统从 Token 读取 Tunnel ID，没有 API 凭据也能运行。若另行填写 Tunnel ID 或绑定账户，必须与 Token 内的身份一致；保存、启动和云端操作都会检查。云端路由和 DNS 此时由 Cloudflare 控制台管理。

如果要发布本项目站点，在本机先保存对应站点路由，复制路由列表显示的 `unix:/tmp/ap-tunnel-…/….sock` 源站地址到 Cloudflare 的完整域名规则，并将源站 `httpHostHeader` 设置为该公网域名。启用实例和站点后才会创建 Socket。配置目录变化会改变 Socket 地址，迁移后需要重新配置云端源站。

要在本项目编辑云端路由，为实例绑定匹配的 API 账户。刷新后选择“导入此路由”，只接管选中的完整域名 HTTP/HTTPS 规则；其他规则、顺序、路径规则及未识别字段保留。首版不导入路径规则、带凭据的服务或 SSH/TCP 规则。

如果还有其他活跃连接器，允许接入运行，但路由同步保持只读。请使用独立隧道管理本机源站，避免其他设备收到仅适用于这台机器的 Unix Socket 地址。Cloudflare 可能短暂保留刚断开的连接器信息，此时稍后刷新重试。

## 变更、删除与恢复

- 云端配置提交前再次检查摘要；发现变更时要求刷新后重新提交。Cloudflare 配置接口没有原子比较并更新能力，无法完全消除检查与写入之间的外部并发窗口；不要同时在多个控制台修改同一隧道。
- 已管理路由的云端源站被修改后，刷新可查看当前源站和下次发布的目标（包括待删除路由）。逐条点击“确认云端变更”，仅更新本机管理记录，保留本机目标；随后执行同步才会更新或删除云端规则。确认时同样检查配置摘要和其他活跃连接器；重复完整域名、带凭据或不支持的服务需先在 Cloudflare 检查并调整。
- 自动创建开启代理的 CNAME，指向 `<Tunnel ID>.cfargotunnel.com`。已有 A、AAAA、其他 CNAME 或未开启代理的同名记录会产生冲突，不会被覆盖。启用的 DDNS 任务使用同一域名时，拒绝发布。
- DNS 记录以 `andey-proxy:<实例 ID>:<路由 ID>` 注释标记归属。删除只清理记录 ID、归属标记和指向均仍匹配的记录；借用的同目标 CNAME 会保留，外部修改过的记录也会保留。同步清理旧路由时，还会保留当前路由或未选云端路径规则仍在使用的 DNS，改名再恢复不会误删解析。
- 保存草稿（域名更名、删除或切换目标）不会立刻撤销已发布入口。旧域名、Socket 和站点引用保护保留至云端确认移除，同步失败也保留。被期望路由或仍发布路由引用的站点不能删除；停用站点会关闭专用入口及长连接并显示不可用状态。
- “解除本地接入”仅移除导入实例；已有云端隧道和 DNS 保留。本项目创建的实例使用独立的“删除云端隧道”操作，先停用本机进程，再清理归属明确的 DNS 并删除隧道。
- 云端创建、同步和删除是后台任务。进度、资源 ID 和路由提交记录会持久化；超时先查询远端结果。部分成功显示失败阶段，重新打开页面可查看最新操作；重启后未完成操作标记为“已中断”，显式检查并重试。
- 配置及两种 Token 均保存到原有加密配置，读取接口只返回是否已设置。编辑时 Token 留空保留原值。配置备份包含隧道，导入后所有实例默认停用，不会自动覆盖云端配置；检查本机源站和云端配置后再启用。
- 站点主动健康检查配置也纳入加密主配置和备份；升级时自动迁移旧 `webproxy-health.json`，后续不再从旧文件覆盖主配置。旧备份没有这些字段时，恢复为默认关闭；保存失败回滚，恢复后立即更新已有探测器。

## 状态与日志

每个启用实例最多运行一个受管理进程。异常退出按 1～30 秒退避重启，显式停用后不再恢复；cloudflared 自动更新关闭。Token 从权限为 `0600` 的临时文件传入，参数中不携带 Token，停止后清理。

就绪状态从实例的本机 loopback `/ready` 读取，不把进程存在当作发布成功。“查看日志”进入日志中心并过滤该实例；日志行限制为 8 KiB，密钥脱敏，沿用原有磁盘上限。通知中心可订阅隧道连接恢复、中断和云端操作失败，Dashboard 显示实例数、就绪数和异常。

仍有旧路由待清理时显示“待同步”。本机站点的 TCP 状态和隧道专用 Socket 分别检查；Socket 创建或监听失败会进入实例目标错误及 Dashboard 异常，修复并重新同步入口后清除，不影响其他站点入口。

本项目站点入口只在专用 Socket 上信任 Cloudflare 访客 IP 和公网协议，并将可信信息用于日志、IP 名单、限流、代理头与 HTTPS 跳转。普通 TCP 站点入口仍使用直接连接地址，伪造 Cloudflare 请求头不会改变访客身份。

首版不包含 Access 客户端、SSH/TCP、WARP、Quick Tunnel 或专门发布管理后台的能力。

## 接口与验证

所有接口在登录认证下，沿用 `{code,msg,data}` 响应格式，入口为 `/api/tunnels`：`/runtime`、`/accounts`（验证和 Zones）、`/instances`（启停、创建、同步、删除）、`/instances/{id}/routes` 和 `/operations/{id}`。云端操作和程序安装返回 HTTP 202 和操作 ID。路由保存只接受可编辑字段；DNS 归属及提交状态由服务端维护。

`GET /api/tunnels/runtime/release?refresh=1` 查询官方稳定版本；`POST /api/tunnels/runtime/install` 接收 `releaseId`、`password`、`restartRunning` 和 `affectedInstances`，确认列表须与当前启用实例一致。通过 `/operations/{id}` 读取阶段、字节进度和错误，关闭页面不取消任务。

`POST /api/tunnels/instances/{id}/routes/reconcile` 接收当前 `digest` 和选定的 `hostnames`，用于确认已显示的云端变更；它不写云端路由或 DNS。

回归检查：`cd web && npm run test && npm run build`、`go vet ./...`、`go test -race ./...`、`./scripts/build-all.sh`。模拟 Cloudflare API 覆盖配置保留、权限/限流失败、DNS 冲突、丢失响应及部分成功重试；模拟 cloudflared 子进程覆盖 Token 文件、就绪检测、崩溃恢复和停用。专用入口测试覆盖 WebSocket、SSE、Basic Auth、真实 IP、限流、HTTPS 跳转和普通入口伪造头。

体积以相同 Go/Node 版本、前端构建和 Go 发布参数比较：目标为主程序增量不超过 1 MiB、所有前端资源逐文件 gzip 总增量不超过 50 KiB。构建不会捆绑 cloudflared。已在 ImmortalWrt x86_64 上运行官方 cloudflared 并完成设备侧检查；真实 Cloudflare 公网访问及 ARM64/ARMv7 运行仍需环境验收。见 [本轮上线验收记录](release-readiness.md)；此前集成的历史数据保留在 [验证记录](tunnel-validation.md)。

参考：[Lucky 实例与路由流程](https://lucky666.cn/docs/modules/cloudflared/)、[官方运行参数](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/configure-tunnels/run-parameters/)、[官方连接器 API](https://developers.cloudflare.com/api/resources/zero_trust/subresources/tunnels/subresources/cloudflared/subresources/connections/methods/get/)。
