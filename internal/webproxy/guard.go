package webproxy

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"luckyx/internal/config"
	"luckyx/internal/forward"
	"luckyx/internal/guard"
)

// checkRuleGuard 子规则安全组件，依次检查：IP 名单 → UA 名单 → BasicAuth。
// 任一不过即写入响应、记录日志并返回 false。
func checkRuleGuard(w http.ResponseWriter, r *http.Request, rule *config.SubRule, logs *forward.RingLog) bool {
	ip := clientIP(r)
	if !guard.AllowIP(rule.IPListMode, rule.IPList, ip) {
		logs.Add(fmt.Sprintf("%s 规则[%s] 被 IP 名单拦截", ip, rule.Name))
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return false
	}
	if !guard.AllowUA(rule.UAListMode, rule.UAList, r.UserAgent()) {
		logs.Add(fmt.Sprintf("%s 规则[%s] 被 UA 名单拦截 UA=%q", ip, rule.Name, r.UserAgent()))
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return false
	}
	if rule.BasicAuth {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(rule.AuthUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(rule.AuthPass)) != 1 {
			logs.Add(fmt.Sprintf("%s 规则[%s] BasicAuth 校验失败", ip, rule.Name))
			w.Header().Set("WWW-Authenticate", `Basic realm="luckyx"`)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return false
		}
	}
	return true
}
