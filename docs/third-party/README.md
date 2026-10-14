# C/D 新增运行依赖

- Coraza v3.8.1：Apache-2.0，许可证位于 coraza/LICENSE；源码 https://github.com/corazawaf/coraza/tree/v3.8.1。
- coraza-coreruleset/v4 v4.25.0：SDK 与内嵌 OWASP CRS 许可证分别保留于 crs/SDK-LICENSE 与 crs/RULES-LICENSE；源码 https://github.com/corazawaf/coraza-coreruleset/tree/v4.25.0。

WebDAV 复用已有 golang.org/x/net/webdav。此文件仅记录本次新增 WAF 运行组件，并不替代项目全部依赖清单；完整版本以 go.mod/go.sum 为准。
