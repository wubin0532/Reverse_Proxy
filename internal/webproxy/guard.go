package webproxy

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"andey-proxy/internal/config"
	"andey-proxy/internal/forward"
	"andey-proxy/internal/guard"
)

// checkRuleGuard 子规则安全组件，依次检查：IP 名单 → UA 名单 → BasicAuth。
// 任一不过即写入响应、记录日志并返回 false。
func checkRuleGuard(w http.ResponseWriter, r *http.Request, rule *config.SubRule, logs *forward.RingLog, matchers ...*guard.IPMatcher) bool {
	ip := clientIP(r)
	var matcher *guard.IPMatcher
	if len(matchers) > 0 {
		matcher = matchers[0]
	} else {
		matcher = guard.CompileIP(rule.IPListMode, rule.IPList)
	}
	if !matcher.Allow(ip) {
		logs.Add(fmt.Sprintf("%s 规则[%s] 被 IP 名单拦截", ip, rule.Name))
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return false
	}
	if !guard.AllowUA(rule.UAListMode, rule.UAList, r.UserAgent()) {
		logs.Add(fmt.Sprintf("%s 规则[%s] 被 UA 名单拦截", ip, rule.Name))
		http.Error(w, "403 Forbidden", http.StatusForbidden)
		return false
	}
	if rule.BasicAuth {
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(rule.AuthUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(rule.AuthPass)) != 1 {
			logs.Add(fmt.Sprintf("%s 规则[%s] BasicAuth 校验失败", ip, rule.Name))
			w.Header().Set("WWW-Authenticate", `Basic realm="andey-proxy"`)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
			return false
		}
	}
	return true
}

func (ss *siteServer) ipGuardFor(rule *config.SubRule) *guard.IPMatcher {
	ss.handlerMu.Lock()
	defer ss.handlerMu.Unlock()
	if !ss.currentRuleLocked(rule) {
		return guard.CompileIP(rule.IPListMode, rule.IPList)
	}
	if ss.ipGuards == nil {
		ss.ipGuards = make(map[string]*guard.IPMatcher)
	}
	if matcher := ss.ipGuards[rule.ID]; matcher != nil {
		return matcher
	}
	matcher := guard.CompileIP(rule.IPListMode, rule.IPList)
	ss.ipGuards[rule.ID] = matcher
	return matcher
}
