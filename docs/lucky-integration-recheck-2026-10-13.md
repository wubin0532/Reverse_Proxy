# 第二轮复核：五模块如何与当前项目结合

执行进展：本文件保留分析阶段的证据与当时状态；用户确认后完成的 A+B 开发及本地验收见 [交付记录](lucky-workspace-implementation-2026-10-13.md)。
日期：2026-10-13。用户确定：DDNS只保留**阿里云、Cloudflare、DNSPod**；参考Lucky页面排版；先完整方案再确定开发范围。本轮不修改业务代码、不保存在线配置。

## 1. 复核结果与覆盖

7个在线关键资源重新下载并校验SHA-256，全部与第一轮一致：主入口、DDNS/Web/SSL/IP过滤/安全组接口包装、总览。复用原REA保留证据，没有重复全量分析。当前在线页面仍显示Lucky 3.1.4。源项目基线仍为`180f365bd5031492a781c77bb141963aa20d9b05`。

| 证据 | 标识 | 范围 |
|---|---|---|
| Lucky主入口 | `ev_11222bc46c039482f1b7de9ef9196faa7b9b041c82895dffae6b4a500405cd80` | 已保留静态分析，语义图partial |
| Lucky接口包装 | `ev_d42b0946e0bfadde8d731de1e46e9109342d548baa9346c938bd146e2494bbca` | 5文件静态分析，动态调用/语义仍未知 |
| 第二轮模块页 | `ev_961135c93e1a4992411aba4ff6dcd3e182922e638cde4d500320dfe60726196b` | 第0页50项，共184项；不是全图阅读 |
| Web资源身份 | `ev_33c79b824d7b42b58c6e05c1d7b50aa062eea7de024ceb29ecde312230e89c7e` | 精确节点，文件SHA=`a1484d9d9ef5f0f3516f65742665964dbe7058c4b9b3939312449191784155a5` |
| 未知登记 | `unk_b3a400f50b3c254c5585604144bf922c9c2b50f48740a7baa98625415b348ac3` | Lucky匹配版本后端尚未取得；对应登记证据`ev_6fd19b71cb9cf9d18f35f57f6f12d07fb72d8bc98cafab102238fbd8726e192e` |
| 项目源码 | 文中file:line | 真实当前实现；不同于Lucky后端证据 |
| 隔离实验 | 下方测试结果、附属JSON | httptest与临时Go overlay，仅本机mock；不证明生产效果 |

以下明确区分：**已确认前端契约、已确认本项目源码、隔离复现、拟新增设计、未知后端**。Lucky的后端DNS/ACME/WAF/授权算法仍未反编译；不会把页面字段或接口存在当成后端算法已验证。

## 2. DDNS：三家适配器保留，记录和调度升级

### 实际流程与最小接入

现有链：`api.validateTask`→`Config.Update`→`Worker.Reload`→每任务goroutine→`runTask`任务锁→`GetIPDetail`→本地IP缓存→选择三家Provider→逐域名Upsert→最近状态/失败通知。

| 功能 | Lucky契约复核 | 我们结合方式与源码 |
|---|---|---|
| 服务商和凭据 | Lucky有大量provider，记录能力因provider不同 | 保留`ddns/factory.go:10`的三个分支与共享`DNSProviderConf`；不新增第四家，也不导入ESA/callback字段 |
| 任务/记录分层 | 新增/修改任务用POST/PUT；记录编辑先GET整任务再PUT整任务，单记录开关走option PUT | `config.go:38`任务增加records和双栈来源；旧Domains按原顺序迁成A或AAAA记录。新API独立更新记录、事务内版本检查，避免整任务旧草稿覆盖 |
| A/AAAA | TaskType可双栈；实际取源由两个enable字段决定 | 第一批只保留A/AAAA；同任务可引用v4/v6。DNS-01临时TXT是证书挑战，不是增加DDNS记录类型 |
| TTL | Lucky前端有自动/预设/自定义TTL；不证明后台全字段比较 | 当前三家同IP直接返回。扩展`provider.go:17`结构化RecordSpec/RecordState，读取TTL并比较；旧Upsert保留兼容包装 |
| 线路 | Lucky按provider显示线路 | DNSPod目前创建/修改硬编码“默认”（`dnspod.go:136/155`）；Aliyun不读取Line。只有完成读取、域名+type+line选择、更新与mock测试后开放；旧配置保持默认 |
| Cloudflare代理 | Lucky提供“是否指定代理状态”和proxied选项 | 当前`cloudflare.go:42`不读TTL/proxied，`:174`不写proxied。使用三态未指定/true/false，未指定应显式保持远端状态，而非假定PUT省略就保留 |
| 同名多记录 | Lucky前端可多记录，后端选择未知 | 当前三家findRecord取首次匹配。新结构需明确recordId/线路/重复记录冲突，不能静默修改第一条 |
| IP来源 | url支持HTTP/STUN/DNS，另有网卡/command/odhcpd；新建默认取决于设备接口 | 复用`ipsource.go:25`和`wan.go:13`；先保留网卡/HTTP，再补有界来源列表。STUN/DNS是单独protocol resolver；不执行任意command/脚本 |
| IPv6选择 | smart/filter/preference/verify/probe字段；实际排序未知 | 当前`:64`选择第一可用地址，不保证最佳地址。后续增加稳定地址偏好/过滤/候选预览，可达性probe默认按需，不复制设备默认 |
| 强制校验/重试 | ForceInterval字段有默认/限值，不能证明实际强制同步算法 | 调度增加独立检查/强制远端核查/失败退避；核查完整目标记录，不只看IP是否变；首次执行、来源变更和凭据变更使缓存失效 |
| 状态与历史 | “触发成功”仅请求被接受；有等待/同步/取消/暂停等状态 | `worker.go:171`已有LastTime/实际网卡/最近结果，直接显示；新增记录级状态与有界历史，手动触发返回operationId，轮询最终结果 |
| 通知 | LuckyWebhook使用另一套变量语法 | 继续使用现有`notify`事件总线/Telegram；已有DDNS失败，新增变化/恢复事件时做去重，不重新引入通用脚本Webhook |

源码注意：凭据test（`ddns/api.go:524`）只读QueryRecord，证明查询能力，不证明写入/ACME TXT权限。Provider自定义端点和IP API已有安全验证、重定向限制和秘密脱敏，应复用。正常公网IP与内网/CGNAT地址分开标识，不能把“成功获得IP”标成“公网已可访问”。

### 两个已隔离复现的问题

1. **相同IP下改域名/TTL，Reload后仍跳过。** `worker.go:223`缓存键仅taskID|IPType，`:228`直接返回；`:105`Reload不清缓存。mock首次查询2次，修改域名/TTL后仍2次，结果为“IP未变化，跳过更新”。
2. **绕过Worker缓存仍可能不更新TTL。** Cloudflare远端TTL=60、请求TTL=300且IP相同，mock观察写请求0次，返回“记录已是最新”。三家代码都有同值早退；Cloudflare已实验复现，Aliyun/DNSPod结论目前属于源码确认。

因此最先做完整配置/凭据版本fingerprint与Provider完整字段比较，然后才开放记录级TTL/线路/proxied。强制运行不能代替第二层修正。

另需回归：旧RunNow快照排队后任务编辑/删除、手动任务与Reload生命周期、失败不推进缓存、部分记录失败不抹掉其他记录成功；引用在最终Config.Update内再次校验，避免校验与提交之间provider被删除。上述并发风险为源码推导，尚未复现。

## 3. Web服务：保留代理核心，增加工作台与一致性

现有链：路径净化→选择启用规则→限流→强制HTTPS→IP/UA/BasicAuth→reverse/redirect/fileserver→状态计数。规则选择按最长路径前缀，同长度按配置顺序；**精确域名不会额外优先于空域名**（`dispatch.go:246`）。分组/搜索只改变显示，不能默默重排这些语义。

| 功能 | 已有/参考契约 | 结合方式 |
|---|---|---|
| 主规则/子规则排版 | Lucky监听器标签、DefaultProxy/ProxyList、分组；本项目已有站点/子规则行 | 复用`WebService.vue`，增加主规则标签导航、分组与搜索、紧凑摘要。现有子规则CRUD/开关/拖拽已完成，不列为新增引擎 |
| 默认规则 | Lucky默认项在主规则内编辑，携带主版本；真实匹配算法未知 | 当前固定404已存在（`dispatch.go:386`）。新增显式默认行为时默认仍404，命中规则不受影响；文本响应等新处理器另行选择 |
| 编辑冲突 | Lucky主ExpectedVersion、子Version；冲突响应码未知 | 本项目`mutationMu`串行当前请求，不能防旧草稿覆盖。增加revision，在Config.Update锁内检查；409使用明确错误码区分“版本冲突”与当前“监听启动失败回滚” |
| 规则+健康检查 | Lucky子配置可同时带BackendPoolConfig | 当前`RuleEditor.vue:528`规则保存后单独保存health，第二次失败仍提示成功。改为一次版本化提交+配置/运行回滚，或明确显示部分成功并保留草稿 |
| 多后端/健康 | 本项目已有轮询、主动探测、失败冷却 | 复用`reverse.go:274`；独立池/权重属于后续增量。保留仅无请求体GET/HEAD/OPTIONS可重放（`:259`），不得重试有效POST |
| 请求头/Host/改写 | 当前已有头配置、Host、Location/Cookie改写 | 只完善编辑分区、展示与能力说明；自定义请求头不计为安全防护数量（现`WebService.vue:308`会混计） |
| 认证/防护/高级 | Lucky有多个标签及身份来源 | 现有BasicAuth、IP/UA、RPS限制继续运行；新增共享policy绑定。身份安全组、响应体替换、gRPC/WebDAV/CGI不混进基础界面改造 |
| TLS热更 | `server.go:157`监听模式变化重建，普通规则快照热更新 | 新policy数组、group字段进入深拷贝（`:436`），变化比较保持旧缓存隔离，不因分组变动重建所有监听 |
| 统计 | 当前active与24h/1440分钟桶已实现 | 活动请求≠TCP/WS连接；字节目前请求结束后计数，未知ContentLength上传=0、SSE结束才计入、Hijack后不计。准确流量增强需另写读写计数，不能改文案就称真实网速 |

前后端建议提交对象：`{expectedRevision, rule, healthCheck}`。服务端先验证所有字段/引用和revision，再写配置、应用快照；失败回滚并返回可区分错误；只有全部成功才显示“保存成功”。新增revision更新条件只依据用户配置成功变更，不随运行统计变化。

## 4. 证书：沿现有安全管线接入导入与算法

现有链：API验证→Config.Update→Manager.Reload→ObtainAsync预留→同证书互斥→lego DNS-01→操作Context→全域名SAN覆盖→requestMatchesLocked再次核对配置/凭据→验证和替换文件→State→invalidate→GetCertificate→Web TLS握手。这里异步、取消、一致性与热供给均已实现，不需要从头建立。

| 功能 | 本项目实查 / Lucky前端契约 | 结合方式 |
|---|---|---|
| DNS-01/续期/手动申请 | `acme/api.go:313`已异步；`manager.go:626`扫描续期 | 保留3家共享凭据。导入source缺省acme，scan明确跳过uploaded；手动申请返回进行中，轮询结果，不标已完成 |
| 取消/停机 | `request.go:24/52/64`覆盖HTTP Body、预留与生命周期；Stop取消并等待 | 新字段/导入操作同样纳入生命周期。Lucky有取消UI，TXT清理和网络中止后端仍未知，不能当复刻保证 |
| 旧申请结果拒绝 | `request.go:77/107`核对enabled/domains/CA/provider及秘密/endpoint | 新source/keyType/solver/传播字段加入完整请求签名；申请期间改配置不能把旧结果提交为新状态 |
| 文件导入 | Lucky分开auto-upload文件转base64，然后POST/PUT证书对象；无法证明原子性 | 我们API直接接收有上限PEM pair，验证匹配/SAN/用途/有效期，再复用`coverage.go:22`与`writeCertificatePair:437`。配置列表只返回元数据，不回传私钥 |
| 一致发布 | 当前证书对写入先验证、临时文件、备份、分别rename、失败恢复 | 两文件不是同时原子可见，加载也没有共用写锁。新增验证完pair的内存原子发布或共享加载锁，保证握手不看到半套新文件；并发握手尚未实测 |
| 算法 | Lucky可选P256/P384/RSA2048/RSA4096；本项目未开放签发keyType | 账户account.key RSA2048保留；证书签发算法单独接lego设置，不能把账户算法当证书算法。默认维持现有行为，并纳入一致性签名 |
| CA/EAB | 当前自定义CA URL已实现；Lucky切换CA控制EAB字段 | 先加CA预设UI；EAB属于新增Kid/HMAC和lego注册接线，HMAC只写并加密，不复制特定CA产品提示为通用协议规则 |
| DNS传播 | 现固定公共resolver、任一返回目标TXT通过、2分钟、可取消（`propagation.go:15`） | 参数需实际传入resolver/precheck/timeout，不能只保存到表单。复用取消循环，但传播宽严策略明确选择，不默认绕过所有检查 |
| 凭据/端点 | DDNS REST Provider与ACME lego provider是不同实现；ACME忽略DNSProviderConf.Endpoint | 读查询测试与TXT写权限/签发验证分开；不得让用户以为DDNS自定义endpoint也控制ACME请求 |
| SNI/域名/文件缓存 | 精确SNI优先、单层泛域名、实际SAN核验、mtime缓存，`main.go:143`注入Web | 导入进入现有CertConf/状态/文件，即可复用热供给。key-only变化、禁用证书显式绑定、独立Web文件缓存需回归 |
| 状态/有效期/通知 | 已有CertState、Flush及成功/失败通知，后端邮箱可空但UI要求填 | 统一remainingSeconds/Days和页间口径，新增source/usingSites/operation阶段。状态Flush失败目前只写日志，应区分证书成功与状态持久化失败 |
| 映射/同步/部署 | Lucky有入口和平台capabilities，当前同步来源可能禁用 | 后续受控路径映射/固定reload适配器；任意脚本、厂商平台与远程同步单独选范围 |

配置导出只含用户配置，不含实际证书文件/私钥/独立状态。新增导入后，备份说明必须区分配置备份与证书材料备份，不能宣称现有配置导出已可完整迁移证书。

## 5. 防护中心：共享匹配引擎与身份组分开

Lucky的命名IP规则是静态准入策略；安全组关联本地/OAuth用户、会话和运行时IP授权，属于身份访问体系；UA只是可伪造字符串，不能当身份认证。共享IP策略绑定与安全组IP grant不能混为一个实体。

| 入口/功能 | 当前事实 | 最小接入 |
|---|---|---|
| IP/CIDR/UA | `guard.go:20`已有不可变编译匹配器；UA忽略大小写包含 | 在其上加Engine/Policy/TemporaryBan/Event，不重写基本CIDR匹配 |
| 直连Web | `dispatch.go:328`只取RemoteAddr，忽略传入XFF/CF头 | 全局拒绝挂在匹配前，规则共享policy挂在命中后；未绑定旧规则保留原有处理顺序 |
| Cloudflare Tunnel | `tunnel.go:90`校验绑定Host、CF头与协议，私有context注入同一个siteHandler；socket0700/0600 | 共享同一Web Engine，避免重复防护链；普通Web不能进入这个受信context，不能默认接受任意代理头 |
| TCP转发 | `forward.go:319`建立目标前检查实际源IP | 每个新连接查询当前Engine快照，策略热改不重启监听；新增封禁默认只约束后续新连接 |
| UDP转发 | `forward.go:421`每个入站包在会话复用前检查 | 每个入站包查询最新快照，已有session也执行；首批不承诺同时关闭返回流量 |
| Cookie/代理身份 | `reverse.go:175`剥离传入身份头与后台Cookie再重建 | 保留原边界，不把管理凭据传给后端 |
| 组合/过期 | 拟新增 | 全局强制拒绝优先，共享与本地均须允许；expireAt在请求判定时检查，定时清理只回收内存；无绑定保持旧行为 |
| 事件/计数 | Forward已有拒绝聚合和4096对端上限 | 在判定点记录reason/policy/site/rule/source/action，有界队列+IP基数限制；不把全部4xx当攻击 |
| 自动封禁 | Lucky文案含连续404/WAF/登录错误阈值；后端计数窗口未知 | 先做人工临时封禁/解除，再选明确窗口/阈值/时长的自动规则；不默认401即攻击，不把自动记录当攻击封禁 |
| 身份安全组 | 本项目暂无；Lucky有SessionTTL/IPBypass/grants | 后续独立AccessGroup/User/Session/IPGrant。默认跨域IP免登录关闭，考虑共享NAT、换网、Cookie作用域和注销撤销 |
| GeoIP/WAF/诱捕 | Lucky入口或产品文案可见，真实引擎未知 | 单独评估资源与数据源，不能以开关冒充功能完成。首批不增加诱捕监听、不扫描局域网 |
| 管理端 | session/origin/budget/TOTP已有 | 业务Engine与管理安全策略独立，业务误封不锁死管理入口 |

引擎快照采用不可变对象原子替换；临时封禁和事件缓存单独同步。默认不会主动断开既有TCP/WS；若将来增加“强制断开”，必须独立接口、明确连接范围与审计。

## 6. 总览：复用已有聚合与图表，增加指标口径

| 功能 | 复核结果 | 结合方式 |
|---|---|---|
| 服务状态 | `dashboard/api.go:22`已有多模块聚合 | 增加DDNS双栈与记录状态、最近证书到期、防护计数；复用已采集active（目前总览漏返回） |
| CPU/内存/主机网速 | Lucky使用gzip+protobuf WS，模块5秒/宿主10秒轮询；后端采样算法未知 | 新metrics单例采样：Linux/proc与runtime；程序/宿主/网卡区分，能力不可得用null/unsupported，不填假0 |
| 运行时间 | 现system/info从注册路由开始计时 | 显示程序运行时长，不标宿主开机时间；首CPU样本没有差分时显示待采样 |
| 历史 | Web24h分钟桶已有，CPU/网卡尚无 | 复用业务图表与桶结构，资源独立环形缓冲/降采样，重启清零明确标记；持久历史后选 |
| 流量 | 当前是HTTP完成计数差分 | 业务请求速率与网卡速率分开展示，SSE/WS补实时读写统计之后才改善该口径 |
| 刷新/局部失败 | `Dashboard.vue:285`Promise.all导致整页刷新失败；useIntervalFn不暂停隐藏或防重叠 | allSettled或独立加载状态、进行中排斥/取消、页面隐藏暂停、更新带时间戳。后台采样不随每个访客重复启动 |
| 卡片与导航 | Lucky资源→服务→网络→运行信息 | 借用排列与状态入口，保留当前维护与异常卡；点异常跳具体对象，空态/不支持/过时状态清楚显示 |
| 宿主进程/连接管理 | Lucky有明细与结束进程API | 首批不接进程终止；复杂宿主监控与高频二进制WS按需求后选 |

建议JSON批量资源接口配合原聚合先实现；没有证据表明当前路由器需要gzip/protobuf实时传输，不因参考实现采用该协议就一起复制。

## 7. 共同底座与迁移顺序

1. **修正既有一致性。** DDNS两层完整比较；Web规则+health统一提交；旧任务结果/引用竞态；证书一致发布。可以先只修这些而不引入全部新功能。
2. **扩展配置及版本。** 新records、source、policy、binding、group、revision；未配置字段保持旧行为。规范化复用Load/Restore同一入口，避免导入绕过迁移。新增根字段同步`config.go:344`rollback和`:380`Restore、备份校验；新slice/map同步`server.go:436`深拷贝。
3. **接单例服务。** `main.go:142–192`沿现有生命周期创建guard Engine/metrics；API挂原requireAuth组；备份恢复hook重载相关模块；Stop清理任务/采样与事件写入。
4. **各模块改最小入口。** DDNS三家Provider完整RecordState/Spec与record调度；证书source/算法入现有请求签名与TLS供给；Web/Forward只接policy+version+group；总览最后聚合状态。
5. **页面统一。** Layout/NavMenu/router/双语locales同步加防护；桌面折叠/手机抽屉复用现有结构；DDNS任务展开记录、Web主标签分组行、证书来源及到期、防护策略/事件、总览资源。

DDNS三家不变、Web三个已有处理器不变是兼容基线；其他服务商/记录类型/服务类型/身份登录方式不因Lucky支持而自动纳入。

## 8. 本轮实际验证与验收缺口

| 检查 | 结果 |
|---|---|
| 在线关键资源SHA | 7/7与第一轮一致 |
| 当前Lucky总览 | 页面仍为3.1.4，实际资源/模块状态可见；未提交更改 |
| `go test ./internal/guard ./internal/webproxy ./internal/forward ./internal/config ./internal/dashboard` | 退出0；4包通过，dashboard无测试文件 |
| `go test -json -overlay /tmp/lucky-ddns-recheck-overlay.json ./internal/ddns ./internal/acme` | 退出0；DDNS30个顶级测试（原28+2探针），ACME24个顶级测试/29个含子测试条目 |
| 2个隔离探针 | 均复现当前跳过行为；PASS表示成功观察到问题，**不是问题已修复** |
| 业务代码/在线配置/部署 | 无修改，无提交/推送/部署 |

临时overlay把新探针映射为虚拟test文件，真实项目目录没有新增测试文件。所有服务商/IP来源均是本机httptest假服务、测试token与保留示例IP；没有使用在线凭据或更新DNS。完整测试输出在本机临时JSONL，关键结果已归档到第二轮精简证据JSON。

未运行：新设计验收（尚未开发）、本轮全仓race/vet/前端build、真实OpenWrt热更/并发TLS握手、正式DNS/CA/WAF/授权请求、真实手机。已有测试通过不能覆盖新增方案。

必要后续验收：相同IP改任意记录字段；双栈/部分成功/旧任务；三家重复记录选择；引用删除竞态；旧草稿版本冲突；规则与health一次提交回滚；证书新旧pair并发握手；直连伪造代理头与Tunnel私有信任边界；UDP现有会话策略热改；临时封禁过期；事件容量；新增字段加密写失败/备份恢复；隐藏页和多页面采样；上传/SSE/WS统计口径。

## 9. 尚未解决的取证问题

五模块前端字段、接口与主要流程已复核；当前项目接入点和两个DDNS缺陷已有源码/隔离证据。**Lucky后端**强制核查、路由优先级、导入一致性、封禁计数窗口、grant撤销传播、WAF引擎实现仍未知。需匹配3.1.4部署包/后端源码，或在明确测试对象上受控验证，不能拿不匹配的官网版本替代。

开发范围仍待确定。推荐先做第7节第1项一致性修正，再推进总体方案A+B；DDNS继续固定三家。
