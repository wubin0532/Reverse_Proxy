package webproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"golang.org/x/net/http/httpguts"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
)

// apiHandler 站点管理 API。
type apiHandler struct {
	cfg *config.Config
	svc *Service
}

var errSiteNotFound = errors.New("站点不存在")

// RegisterRoutes 挂载 Web 服务相关路由（由主控在鉴权分组内调用）。
func RegisterRoutes(r chi.Router, cfg *config.Config, svc *Service) {
	h := &apiHandler{cfg: cfg, svc: svc}
	r.Get("/api/sites", h.list)
	r.Post("/api/sites", h.create)
	r.Put("/api/sites/{id}", h.update)
	r.Delete("/api/sites/{id}", h.delete)
	r.Post("/api/sites/{id}/toggle", h.toggle)
	r.Get("/api/sites/{id}/logs", h.logs)
	r.Post("/api/sites/backend-test", h.testBackend)
}

type backendTestResponse struct {
	Addresses []string `json:"addresses"`
	TCP       bool     `json:"tcp"`
	TLS       bool     `json:"tls"`
	LatencyMs int64    `json:"latencyMs"`
}

func (h *apiHandler) testBackend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL           string `json:"url"`
		Timeout       int    `json:"connectTimeoutSeconds"`
		SkipTLSVerify bool   `json:"skipBackendTlsVerify"`
	}
	if err := api.DecodeBody(r, &body); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	u, err := url.Parse(strings.TrimSpace(body.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		api.Fail(w, 400, "后端地址必须是无凭据的 HTTP 或 HTTPS URL")
		return
	}
	if body.Timeout == 0 {
		body.Timeout = 5
	}
	if body.Timeout < 1 || body.Timeout > 30 {
		api.Fail(w, 400, "连接超时必须为 1 到 30 秒")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(body.Timeout)*time.Second)
	defer cancel()
	start := time.Now()
	addresses, err := net.DefaultResolver.LookupHost(ctx, u.Hostname())
	if err != nil || len(addresses) == 0 {
		api.Fail(w, 502, "DNS 解析失败")
		return
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	dialer := &net.Dialer{Timeout: time.Duration(body.Timeout) * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		api.Fail(w, 502, "TCP 连接失败")
		return
	}
	_ = conn.Close()
	result := backendTestResponse{Addresses: addresses, TCP: true, LatencyMs: time.Since(start).Milliseconds()}
	if u.Scheme == "https" {
		tlsDialer := &tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12, InsecureSkipVerify: body.SkipTLSVerify}} // #nosec G402: explicit test-only opt-in
		tlsConn, err := tlsDialer.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
		if err != nil {
			api.Fail(w, 502, "TLS 握手失败或证书无效")
			return
		}
		_ = tlsConn.Close()
		result.TLS = true
		result.LatencyMs = time.Since(start).Milliseconds()
	}
	log.Printf("[security] 后端连接测试成功: %s", backendURLForLog(u))
	api.OK(w, result)
}

// siteView 站点 + 运行状态。
type siteView struct {
	config.Site
	Status string `json:"status"` // listening / error / stopped
	Error  string `json:"error,omitempty"`
}

func (h *apiHandler) list(w http.ResponseWriter, r *http.Request) {
	h.cfg.RLock()
	sites := make([]config.Site, len(h.cfg.Sites))
	copy(sites, h.cfg.Sites)
	h.cfg.RUnlock()
	redactSites(sites)

	views := make([]siteView, 0, len(sites))
	for _, site := range sites {
		status, errMsg := h.svc.SiteStatus(site.ID)
		views = append(views, siteView{Site: site, Status: status, Error: errMsg})
	}
	api.OK(w, views)
}

func (h *apiHandler) create(w http.ResponseWriter, r *http.Request) {
	var site config.Site
	if err := api.DecodeBody(r, &site); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	ensureRuleIDs(&site)
	if err := h.validateSite(&site, false); err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
	site.ID = ids.New()

	if err := h.cfg.Update(func(c *config.Config) error {
		c.Sites = append(c.Sites, site)
		return nil
	}); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	if err := h.svc.Reload(); err != nil {
		_ = h.cfg.Update(func(c *config.Config) error {
			for i := range c.Sites {
				if c.Sites[i].ID == site.ID {
					c.Sites = append(c.Sites[:i], c.Sites[i+1:]...)
					break
				}
			}
			return nil
		})
		_ = h.svc.Reload()
		api.Fail(w, 409, "站点启动失败，配置未保存: "+err.Error())
		return
	}
	log.Printf("[security] 新增 Web 站点，ID: %s", site.ID)
	redacted := []config.Site{site}
	redactSites(redacted)
	api.OK(w, redacted[0])
}

func (h *apiHandler) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var site config.Site
	if err := api.DecodeBody(r, &site); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	site.ID = id
	ensureRuleIDs(&site)
	if err := h.validateSite(&site, true); err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
	h.cfg.RLock()
	var previous config.Site
	found := false
	for i := range h.cfg.Sites {
		if h.cfg.Sites[i].ID == id {
			previous = h.cfg.Sites[i]
			found = true
			break
		}
	}
	h.cfg.RUnlock()
	if !found {
		api.Fail(w, 404, "站点不存在")
		return
	}
	mergeRuleSecrets(&site, previous)
	if err := h.validateSite(&site, false); err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
	err := h.cfg.Update(func(c *config.Config) error {
		for i := range c.Sites {
			if c.Sites[i].ID == id {
				c.Sites[i] = site
				return nil
			}
		}
		return errSiteNotFound
	})
	if errors.Is(err, errSiteNotFound) {
		api.Fail(w, 404, errSiteNotFound.Error())
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	if reloadErr := h.svc.Reload(); reloadErr != nil {
		rollbackErr := h.cfg.Update(func(c *config.Config) error {
			for i := range c.Sites {
				if c.Sites[i].ID == id {
					c.Sites[i] = previous
					return nil
				}
			}
			return errSiteNotFound
		})
		_ = h.svc.Reload()
		if rollbackErr != nil {
			api.Fail(w, 500, "站点启动失败且恢复旧配置失败")
		} else {
			api.Fail(w, 409, "站点启动失败，已恢复旧配置: "+reloadErr.Error())
		}
		return
	}
	log.Printf("[security] 修改 Web 站点，ID: %s", id)
	redacted := []config.Site{site}
	redactSites(redacted)
	api.OK(w, redacted[0])
}

func redactSites(sites []config.Site) {
	for si := range sites {
		sites[si].Rules = append([]config.SubRule(nil), sites[si].Rules...)
		for ri := range sites[si].Rules {
			r := &sites[si].Rules[ri]
			r.AuthPass = ""
			if r.Headers != nil {
				m := make(map[string]string, len(r.Headers))
				for k := range r.Headers {
					m[k] = ""
				}
				r.Headers = m
			}
		}
	}
}

func mergeRuleSecrets(next *config.Site, old config.Site) {
	byID := make(map[string]config.SubRule)
	for _, r := range old.Rules {
		byID[r.ID] = r
	}
	for i := range next.Rules {
		prev, ok := byID[next.Rules[i].ID]
		if !ok {
			continue
		}
		if !next.Rules[i].BasicAuth {
			next.Rules[i].AuthPass = ""
		} else if next.Rules[i].AuthPass == "" {
			next.Rules[i].AuthPass = prev.AuthPass
		}
		for k, v := range next.Rules[i].Headers {
			if v == "" {
				if pv, exists := prev.Headers[k]; exists {
					next.Rules[i].Headers[k] = pv
				}
			}
		}
	}
}

func (h *apiHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := h.cfg.Update(func(c *config.Config) error {
		for i := range c.Sites {
			if c.Sites[i].ID == id {
				c.Sites = append(c.Sites[:i], c.Sites[i+1:]...)
				return nil
			}
		}
		return errSiteNotFound
	})
	if errors.Is(err, errSiteNotFound) {
		api.Fail(w, 404, errSiteNotFound.Error())
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.svc.Reload()
	log.Printf("[security] 删除 Web 站点，ID: %s", id)
	api.OK(w, nil)
}

func (h *apiHandler) toggle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	enabled := false
	previousEnabled := false
	err := h.cfg.Update(func(c *config.Config) error {
		for i := range c.Sites {
			if c.Sites[i].ID == id {
				previousEnabled = c.Sites[i].Enabled
				c.Sites[i].Enabled = !c.Sites[i].Enabled
				enabled = c.Sites[i].Enabled
				return nil
			}
		}
		return errSiteNotFound
	})
	if errors.Is(err, errSiteNotFound) {
		api.Fail(w, 404, errSiteNotFound.Error())
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	if reloadErr := h.svc.Reload(); reloadErr != nil {
		rollbackErr := h.cfg.Update(func(c *config.Config) error {
			for i := range c.Sites {
				if c.Sites[i].ID == id {
					c.Sites[i].Enabled = previousEnabled
					return nil
				}
			}
			return errSiteNotFound
		})
		_ = h.svc.Reload()
		if rollbackErr != nil {
			api.Fail(w, 500, "站点启动失败且恢复启用状态失败")
		} else {
			api.Fail(w, 409, "站点启动失败，已恢复启用状态: "+reloadErr.Error())
		}
		return
	}
	log.Printf("[security] 切换 Web 站点，ID: %s，启用: %t", id, enabled)
	api.OK(w, map[string]bool{"enabled": enabled})
}

func (h *apiHandler) logs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.cfg.RLock()
	exists := false
	for i := range h.cfg.Sites {
		if h.cfg.Sites[i].ID == id {
			exists = true
			break
		}
	}
	h.cfg.RUnlock()
	if !exists {
		api.Fail(w, 404, "站点不存在")
		return
	}
	api.OK(w, h.svc.Logs(id))
}

// ensureRuleIDs 给没有 ID 的子规则补 ID。
func ensureRuleIDs(site *config.Site) {
	seen := make(map[string]bool, len(site.Rules))
	for i := range site.Rules {
		if site.Rules[i].ID == "" || seen[site.Rules[i].ID] {
			site.Rules[i].ID = ids.New()
		}
		seen[site.Rules[i].ID] = true
	}
}

func (h *apiHandler) validateSite(site *config.Site, allowOmittedSecrets bool) error {
	site.Name = strings.TrimSpace(site.Name)
	if site.Name == "" {
		return fmt.Errorf("站点名称不能为空")
	}
	listen, err := normalizeSiteListen(site.Listen)
	if err != nil {
		return fmt.Errorf("监听地址无效: %w", err)
	}
	site.Listen = listen
	if len(site.Rules) > 100 {
		return fmt.Errorf("每个站点最多 100 条子规则")
	}
	if site.CertID != "" {
		h.cfg.RLock()
		found := false
		for _, cert := range h.cfg.Certs {
			if cert.ID == site.CertID {
				found = true
				break
			}
		}
		h.cfg.RUnlock()
		if !found {
			return fmt.Errorf("引用的证书不存在")
		}
	}
	for i := range site.Rules {
		rule := &site.Rules[i]
		rule.Name = strings.TrimSpace(rule.Name)
		if rule.Name == "" {
			return fmt.Errorf("子规则名称不能为空")
		}
		if strings.ContainsAny(rule.FrontendHost, "/\\\r\n\t ") {
			return fmt.Errorf("前端域名格式无效: %s", rule.FrontendHost)
		}
		if rule.FrontendPath != "" && !strings.HasPrefix(rule.FrontendPath, "/") {
			rule.FrontendPath = "/" + rule.FrontendPath
		}
		if err := validateRuleLists(rule); err != nil {
			return err
		}
		if len(rule.Headers) > 100 {
			return fmt.Errorf("每条子规则最多 100 个自定义请求头")
		}
		for key, value := range rule.Headers {
			if !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) {
				return fmt.Errorf("自定义请求头无效: %s", key)
			}
		}
		if rule.BasicAuth && (strings.TrimSpace(rule.AuthUser) == "" || (!allowOmittedSecrets && rule.AuthPass == "")) {
			return fmt.Errorf("basic auth 必须配置用户名和密码")
		}
		switch rule.Type {
		case "reverse":
			if rule.ConnectTimeout < 0 || rule.ConnectTimeout > 30 {
				return fmt.Errorf("连接超时必须为 0 到 30 秒")
			}
			if rule.ResponseHeaderTimeout < 0 || rule.ResponseHeaderTimeout > 600 {
				return fmt.Errorf("响应头超时必须为 0 到 600 秒")
			}
			if rule.RateLimitRPS < 0 || rule.RateLimitRPS > 100000 {
				return fmt.Errorf("每秒请求限制必须为 0 到 100000")
			}
			if rule.RateLimitRPS == 0 {
				rule.RateLimitBurst = 0
			} else if rule.RateLimitBurst == 0 {
				rule.RateLimitBurst = rule.RateLimitRPS * 2
			}
			if rule.RateLimitBurst < 0 || rule.RateLimitBurst > 200000 {
				return fmt.Errorf("突发请求上限必须为 0 到 200000")
			}
			if rule.MaxRequestBodyMiB < 0 || rule.MaxRequestBodyMiB > 10240 {
				return fmt.Errorf("请求体上限必须为 0 到 10240 MiB")
			}
			rule.CookieDomainFrom = strings.TrimSpace(rule.CookieDomainFrom)
			rule.CookieDomainTo = strings.TrimSpace(rule.CookieDomainTo)
			if !validCookieDomain(rule.CookieDomainFrom) || !validCookieDomain(rule.CookieDomainTo) {
				return fmt.Errorf("Cookie Domain 改写值无效")
			}
			if rule.CookieDomainFrom == "" {
				rule.CookieDomainTo = ""
			}
			if rule.CookiePathFrom != "" && !strings.HasPrefix(rule.CookiePathFrom, "/") {
				return fmt.Errorf("Cookie 原路径必须以 / 开头")
			}
			if rule.CookiePathTo != "" && !strings.HasPrefix(rule.CookiePathTo, "/") {
				return fmt.Errorf("Cookie 新路径必须以 / 开头")
			}
			if rule.CookiePathFrom == "" {
				rule.CookiePathTo = ""
			}
			if len(rule.Backends) == 0 || len(rule.Backends) > 32 {
				return fmt.Errorf("反向代理后端数量必须为 1 到 32 个")
			}
			for j, raw := range rule.Backends {
				raw = strings.TrimSpace(raw)
				u, err := url.Parse(raw)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("后端地址无效: %s", raw)
				}
				if u.User != nil {
					return fmt.Errorf("后端 URL 禁止携带 user:password，请使用只写请求头")
				}
				rule.Backends[j] = raw
			}
		case "redirect":
			u, err := url.Parse(rule.RedirectURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return fmt.Errorf("重定向地址必须是有效的 HTTP 或 HTTPS URL")
			}
			if u.User != nil {
				return fmt.Errorf("重定向地址禁止携带用户名或密码")
			}
			if rule.RedirectCode != 0 && rule.RedirectCode != http.StatusMovedPermanently && rule.RedirectCode != http.StatusFound && rule.RedirectCode != http.StatusTemporaryRedirect && rule.RedirectCode != http.StatusPermanentRedirect {
				return fmt.Errorf("重定向状态码必须是 301、302、307 或 308")
			}
		case "fileserver":
			if strings.TrimSpace(rule.RootDir) == "" {
				return fmt.Errorf("文件服务根目录不能为空")
			}
		default:
			return fmt.Errorf("不支持的子规则类型: %s", rule.Type)
		}
	}
	return nil
}

func validCookieDomain(value string) bool {
	value = strings.TrimPrefix(value, ".")
	return value == "" || (!strings.ContainsAny(value, " /\\\r\n\t:") && len(value) <= 253)
}

func normalizeSiteListen(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("不能为空")
	}
	if !strings.Contains(value, ":") {
		value = ":" + value
	}
	_, portValue, err := net.SplitHostPort(value)
	if err != nil {
		return "", err
	}
	port, err := strconv.Atoi(portValue)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("端口必须在 1 到 65535 之间")
	}
	return value, nil
}

func validateRuleLists(rule *config.SubRule) error {
	if rule.IPListMode != "" && rule.IPListMode != "off" && rule.IPListMode != "whitelist" && rule.IPListMode != "blacklist" {
		return fmt.Errorf("IP 名单模式无效")
	}
	if rule.UAListMode != "" && rule.UAListMode != "off" && rule.UAListMode != "whitelist" && rule.UAListMode != "blacklist" {
		return fmt.Errorf("User-Agent 名单模式无效")
	}
	if len(rule.IPList) > 1000 || len(rule.UAList) > 1000 {
		return fmt.Errorf("IP 或 User-Agent 名单最多 1000 条")
	}
	for _, raw := range rule.IPList {
		value := strings.TrimSpace(raw)
		if net.ParseIP(value) == nil {
			if _, _, err := net.ParseCIDR(value); err != nil {
				return fmt.Errorf("IP 或 CIDR 无效: %s", raw)
			}
		}
	}
	return nil
}
