package webproxy

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"andey-proxy/internal/config"
)

func TestMatchRuleHostFilter(t *testing.T) {
	rules := []config.SubRule{
		{ID: "a", Name: "any", Type: "redirect", Enabled: true, FrontendPath: "/"},
		{ID: "b", Name: "host", Type: "redirect", Enabled: true, FrontendHost: "www.Example.com", FrontendPath: "/"},
	}
	// 域名精确匹配（忽略大小写与端口）优先于空 host 规则（同为 "/" 前缀时先定义者胜，
	// 因此这里把空 host 规则放前面，验证 host 过滤本身生效）
	got := matchRule(rules, "www.example.com:8080", "/x")
	if got == nil || got.ID != "a" {
		t.Fatalf("同级应先命中先定义的规则, got %+v", got)
	}
	// host 不匹配时 b 被过滤，回落到 a
	got = matchRule(rules, "other.com", "/x")
	if got == nil || got.ID != "a" {
		t.Fatalf("host 不匹配应回落到空 host 规则, got %+v", got)
	}
	// 只有 b 的 host 匹配场景
	rules2 := []config.SubRule{
		{ID: "b", Name: "host", Type: "redirect", Enabled: true, FrontendHost: "www.example.com", FrontendPath: "/"},
	}
	if got := matchRule(rules2, "WWW.EXAMPLE.COM", "/"); got == nil || got.ID != "b" {
		t.Fatalf("大小写不敏感匹配失败, got %+v", got)
	}
	if got := matchRule(rules2, "sub.example.com", "/"); got != nil {
		t.Fatalf("host 不匹配应返回 nil, got %+v", got)
	}
}

func TestMatchRuleLongestPrefix(t *testing.T) {
	rules := []config.SubRule{
		{ID: "root", Name: "root", Type: "redirect", Enabled: true, FrontendPath: "/"},
		{ID: "api", Name: "api", Type: "redirect", Enabled: true, FrontendPath: "/api"},
		{ID: "apiv2", Name: "apiv2", Type: "redirect", Enabled: true, FrontendPath: "/api/v2"},
		{ID: "off", Name: "off", Type: "redirect", Enabled: false, FrontendPath: "/api/v2/admin"},
	}
	cases := []struct {
		path string
		want string
	}{
		{"/", "root"},
		{"/other", "root"},
		{"/api", "api"},
		{"/api/users", "api"},
		{"/api/v2/list", "apiv2"},
		{"/api/v2/admin/x", "apiv2"}, // 更长规则被禁用，回落
		{"/apis", "root"},            // 前缀边界：/api 不应吃掉 /apis
	}
	for _, c := range cases {
		got := matchRule(rules, "h.com", c.path)
		if got == nil || got.ID != c.want {
			t.Errorf("path %s: want %s, got %+v", c.path, c.want, got)
		}
	}
	// 完全无匹配
	onlyAPI := []config.SubRule{{ID: "api", Enabled: true, Type: "redirect", FrontendPath: "/api"}}
	if got := matchRule(onlyAPI, "h.com", "/nope"); got != nil {
		t.Fatalf("无匹配应返回 nil, got %+v", got)
	}
}

func TestRedirectPlaceholders(t *testing.T) {
	rule := config.SubRule{
		Name:        "r",
		Type:        "redirect",
		RedirectURL: "https://target.example.com{path}?{query}",
	}
	h := redirectHandler(rule)
	req := newRequest("GET", "http://site.local/old/page?a=1&b=2", "")
	rec := newRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("默认状态码应为 302, got %d", rec.Code)
	}
	want := "https://target.example.com/old/page?a=1&b=2"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Fatalf("Location want %q, got %q", want, loc)
	}

	// 自定义状态码与非法状态码
	rule.RedirectCode = 301
	rec = newRecorder()
	redirectHandler(rule).ServeHTTP(rec, newRequest("GET", "http://site.local/", ""))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("应为 301, got %d", rec.Code)
	}
	rule.RedirectCode = 999
	rec = newRecorder()
	redirectHandler(rule).ServeHTTP(rec, newRequest("GET", "http://site.local/", ""))
	if rec.Code != http.StatusFound {
		t.Fatalf("非法状态码应回退 302, got %d", rec.Code)
	}

	// 含编码字符的路径应保持转义形态，避免解码后的 ?/# 改变目标 URL 语义
	rule.RedirectCode = 0
	rec = newRecorder()
	redirectHandler(rule).ServeHTTP(rec, newRequest("GET", "http://site.local/a%3Fb%23c?x=1", ""))
	want = "https://target.example.com/a%3Fb%23c?x=1"
	if loc := rec.Header().Get("Location"); loc != want {
		t.Fatalf("编码路径 Location want %q, got %q", want, loc)
	}
}

// TestForceHTTPSRedirect 强制 HTTPS：明文请求 301 跳转（host/path/query 保留），
// TLS 请求与未满足条件的站点不跳转，非法 Host 返回 400。
func TestForceHTTPSRedirect(t *testing.T) {
	newHandler := func(site config.Site) *siteHandler {
		return &siteHandler{ss: &siteServer{site: site, logs: newTestRingLog(), limiter: newRuleLimiter()}}
	}
	site := config.Site{
		ID: "s1", TLS: true, ForceHTTPS: true, CertID: "c1",
		Rules: []config.SubRule{{ID: "r1", Name: "跳转", Type: "redirect", Enabled: true, FrontendPath: "/", RedirectURL: "https://example.com{path}"}},
	}
	serve := func(h *siteHandler, req *http.Request) *httptest.ResponseRecorder {
		req.RemoteAddr = "127.0.0.1:1234"
		rec := newRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// 明文请求（r.TLS == nil）→ 301，Location 仅替换 scheme，host:port/path/query 保留
	rec := serve(newHandler(site), newRequest("GET", "http://site.local:8080/a/b?x=1&y=2", ""))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("明文请求应为 301, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://site.local:8080/a/b?x=1&y=2" {
		t.Fatalf("Location 应保留 host/path/query, got %q", loc)
	}

	// TLS 请求不跳转，继续按规则分发（命中规则的 302）
	req := newRequest("GET", "http://site.local:8080/a/b?x=1", "")
	req.TLS = &tls.ConnectionState{}
	rec = serve(newHandler(site), req)
	if rec.Code != http.StatusFound {
		t.Fatalf("TLS 请求不应被 301 短路, got %d", rec.Code)
	}

	// 未启用 forceHttps / 未绑定证书 / 非 TLS 站点 → 均不跳转
	for _, s := range []config.Site{
		{ID: "s2", TLS: true, CertID: "c1", Rules: site.Rules},
		{ID: "s3", TLS: true, ForceHTTPS: true, Rules: site.Rules},
		{ID: "s4", ForceHTTPS: true, CertID: "c1", Rules: site.Rules},
	} {
		if rec = serve(newHandler(s), newRequest("GET", "http://site.local/", "")); rec.Code == http.StatusMovedPermanently {
			t.Fatalf("站点 %s 不满足强制 HTTPS 条件，不应跳转", s.ID)
		}
	}

	// 空 Host 或含控制字符的 Host → 400，不构造 Location
	for _, host := range []string{"", "evil.com\r\nInjected: x", "evil host.com"} {
		req = newRequest("GET", "http://site.local/", "")
		req.Host = host
		rec = serve(newHandler(site), req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("非法 Host %q 应为 400, got %d", host, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Fatalf("非法 Host 不应产生 Location, got %q", loc)
		}
	}
}

func TestGuardIPUAAndBasicAuth(t *testing.T) {
	newReq := func(xff, ua string) *http.Request {
		r := newRequest("GET", "http://site.local/", ua)
		r.RemoteAddr = "127.0.0.1:12345"
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}

	// IP 白名单只使用直连地址，客户端伪造 XFF 不能绕过。
	rule := &config.SubRule{Name: "g", IPListMode: "whitelist", IPList: []string{"10.0.0.0/8"}}
	rec := newRecorder()
	if checkRuleGuard(rec, newReq("10.1.2.3, 1.1.1.1", ""), rule, newTestRingLog()) {
		t.Fatal("伪造 XFF 不应绕过直连 IP 白名单")
	}
	rule.IPList = []string{"127.0.0.1"}
	rec = newRecorder()
	if !checkRuleGuard(rec, newReq("203.0.113.9", ""), rule, newTestRingLog()) {
		t.Fatal("直连 IP 在白名单时应放行")
	}

	// UA 黑名单
	rule = &config.SubRule{Name: "g", UAListMode: "blacklist", UAList: []string{"badbot"}}
	rec = newRecorder()
	if checkRuleGuard(rec, newReq("", "BadBot/1.0"), rule, newTestRingLog()) {
		t.Fatal("黑名单 UA 应拦截")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("UA 拦截应为 403, got %d", rec.Code)
	}
	rec = newRecorder()
	if !checkRuleGuard(rec, newReq("", "Mozilla/5.0"), rule, newTestRingLog()) {
		t.Fatal("正常 UA 应放行")
	}

	// BasicAuth：失败 401 + WWW-Authenticate，成功放行
	rule = &config.SubRule{Name: "g", BasicAuth: true, AuthUser: "u", AuthPass: "p"}
	rec = newRecorder()
	if checkRuleGuard(rec, newReq("", ""), rule, newTestRingLog()) {
		t.Fatal("无凭据应拦截")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("BasicAuth 失败应为 401, got %d", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("缺少 WWW-Authenticate 头")
	}
	req := newReq("", "")
	req.SetBasicAuth("u", "p")
	rec = newRecorder()
	if !checkRuleGuard(rec, req, rule, newTestRingLog()) {
		t.Fatal("凭据正确应放行")
	}
	req = newReq("", "")
	req.SetBasicAuth("u", "wrong")
	rec = newRecorder()
	if checkRuleGuard(rec, req, rule, newTestRingLog()) {
		t.Fatal("密码错误应拦截")
	}
}
