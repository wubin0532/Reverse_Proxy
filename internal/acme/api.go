package acme

import (
	"context"
	"errors"
	"log"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"golang.org/x/net/idna"
)

type handler struct {
	cfg *config.Config
	m   *Manager
}

var (
	errCertNotFound = errors.New("证书不存在")
	errCertInUse    = errors.New("证书正在使用")
)

// RegisterRoutes 在已认证的 chi.Group 中挂载证书相关路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, m *Manager) {
	h := &handler{cfg: cfg, m: m}
	r.Get("/api/certs", h.listCerts)
	r.Post("/api/certs", h.createCert)
	r.Put("/api/certs/{id}", h.updateCert)
	r.Delete("/api/certs/{id}", h.deleteCert)
	r.Post("/api/certs/{id}/toggle", h.toggleCert)
	r.Post("/api/certs/{id}/obtain", h.obtainCert)
	r.Get("/api/certs/{id}/download", h.downloadCert)
}

// certView 列表视图：附带运行状态。
type certView struct {
	config.CertConf
	Status    string `json:"status"`    // pending / ok / expiring / expired / error
	Obtaining bool   `json:"obtaining"` // 是否正在申请中
}

// statusOf 根据配置计算证书状态。
func statusOf(c *config.CertConf, now time.Time) string {
	if c.NotAfter == "" {
		if c.LastError != "" {
			return "error"
		}
		return "pending"
	}
	notAfter, err := time.Parse(time.RFC3339, c.NotAfter)
	if err != nil {
		return "error"
	}
	if !notAfter.After(now) {
		return "expired"
	}
	if !notAfter.After(now.Add(time.Duration(renewDaysOf(c)) * 24 * time.Hour)) {
		return "expiring"
	}
	return "ok"
}

func (h *handler) listCerts(w http.ResponseWriter, r *http.Request) {
	h.cfg.RLock()
	certs := make([]config.CertConf, len(h.cfg.Certs))
	copy(certs, h.cfg.Certs)
	h.cfg.RUnlock()
	now := time.Now()
	views := make([]certView, 0, len(certs))
	for i := range certs {
		c := &certs[i]
		views = append(views, certView{
			CertConf:  *c,
			Status:    statusOf(c, now),
			Obtaining: h.m.Obtaining(c.ID),
		})
	}
	api.OK(w, views)
}

func (h *handler) validateCert(c *config.CertConf) (int, string) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return 400, "证书名称不能为空"
	}
	if len(c.Domains) == 0 || len(c.Domains) > 100 {
		return 400, "域名数量必须为 1 到 100 个"
	}
	for i, raw := range c.Domains {
		domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		wildcard := false
		if strings.HasPrefix(domain, "*.") {
			wildcard = true
			domain = strings.TrimPrefix(domain, "*.")
		}
		ascii, err := idna.Lookup.ToASCII(domain)
		if err != nil || ascii == "" || strings.Contains(ascii, "..") || strings.ContainsAny(ascii, " /\\*") {
			return 400, "域名格式不正确: " + raw
		}
		if wildcard {
			ascii = "*." + ascii
		}
		c.Domains[i] = ascii
	}
	if c.Email != "" {
		address, err := mail.ParseAddress(strings.TrimSpace(c.Email))
		if err != nil || address.Address != strings.TrimSpace(c.Email) {
			return 400, "联系邮箱格式不正确"
		}
		c.Email = address.Address
	}
	if c.RenewDays < 0 || c.RenewDays > 90 {
		return 400, "提前续签天数必须为 0 到 90"
	}
	if c.CADirURL != "" {
		u, err := url.Parse(strings.TrimSpace(c.CADirURL))
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return 400, "ACME CA 地址无效"
		}
		if u.Scheme == "http" {
			host := u.Hostname()
			ip := net.ParseIP(host)
			if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
				return 400, "非本机 ACME CA 必须使用 HTTPS"
			}
		}
		c.CADirURL = u.String()
	}
	h.cfg.RLock()
	found := false
	for _, p := range h.cfg.Providers {
		if p.ID == c.ProviderID {
			found = true
			break
		}
	}
	h.cfg.RUnlock()
	if !found {
		return 400, "引用的服务商凭据不存在"
	}
	return 0, ""
}

func (h *handler) createCert(w http.ResponseWriter, r *http.Request) {
	var c config.CertConf
	if err := api.DecodeBody(r, &c); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	c.ID = ""
	c.CertFile, c.KeyFile, c.NotAfter, c.LastError = "", "", "", ""
	if code, msg := h.validateCert(&c); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	c.ID = ids.New()
	if err := h.cfg.Update(func(cfg *config.Config) error {
		cfg.Certs = append(cfg.Certs, c)
		return nil
	}); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	log.Printf("[security] 新增证书配置，ID: %s", c.ID)
	api.OK(w, c)
}

func (h *handler) updateCert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var c config.CertConf
	if err := api.DecodeBody(r, &c); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	c.ID = id
	if code, msg := h.validateCert(&c); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	err := h.cfg.Update(func(cfg *config.Config) error {
		for i := range cfg.Certs {
			if cfg.Certs[i].ID != id {
				continue
			}
			old := cfg.Certs[i]
			// 保留原有证书文件与状态；域名变化时作废，等待重新申请
			c.CertFile, c.KeyFile = old.CertFile, old.KeyFile
			c.NotAfter, c.LastError = old.NotAfter, old.LastError
			if !sameDomains(old.Domains, c.Domains) {
				c.CertFile, c.KeyFile, c.NotAfter, c.LastError = "", "", "", ""
			}
			cfg.Certs[i] = c
			return nil
		}
		return errCertNotFound
	})
	if errors.Is(err, errCertNotFound) {
		api.Fail(w, 404, "证书不存在")
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.m.invalidate(id)
	log.Printf("[security] 修改证书配置，ID: %s", id)
	api.OK(w, c)
}

func sameDomains(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func (h *handler) deleteCert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var removed config.CertConf
	usedBy := ""
	err := h.cfg.Update(func(cfg *config.Config) error {
		for _, site := range cfg.Sites {
			if site.CertID == id {
				usedBy = site.Name
				return errCertInUse
			}
		}
		for i := range cfg.Certs {
			if cfg.Certs[i].ID == id {
				removed = cfg.Certs[i]
				cfg.Certs = append(cfg.Certs[:i], cfg.Certs[i+1:]...)
				return nil
			}
		}
		return errCertNotFound
	})
	if errors.Is(err, errCertInUse) {
		api.Fail(w, 400, "该证书正被站点「"+usedBy+"」使用，无法删除")
		return
	}
	if errors.Is(err, errCertNotFound) {
		api.Fail(w, 404, "证书不存在")
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.m.RemoveFiles(&removed)
	log.Printf("[security] 删除证书配置，ID: %s", id)
	api.OK(w, nil)
}

func (h *handler) toggleCert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var enabled bool
	err := h.cfg.Update(func(cfg *config.Config) error {
		for i := range cfg.Certs {
			if cfg.Certs[i].ID == id {
				cfg.Certs[i].Enabled = !cfg.Certs[i].Enabled
				enabled = cfg.Certs[i].Enabled
				return nil
			}
		}
		return errCertNotFound
	})
	if errors.Is(err, errCertNotFound) {
		api.Fail(w, 404, "证书不存在")
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	if !enabled {
		h.m.invalidate(id)
	}
	log.Printf("[security] 切换证书配置，ID: %s，启用: %t", id, enabled)
	api.OK(w, map[string]bool{"enabled": enabled})
}

// obtainCert 异步申请/重签，立即返回，状态轮询 GET /api/certs。
func (h *handler) obtainCert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.m.findCert(id); !ok {
		api.Fail(w, 404, "证书不存在")
		return
	}
	if h.m.Obtaining(id) {
		api.Fail(w, 400, "该证书正在申请中")
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(h.m.ctx, 10*time.Minute) // 派生自 Manager 生命周期，Stop 时取消
		defer cancel()
		h.m.Obtain(ctx, id) // 结果回写到 LastError / NotAfter，前端轮询即可
	}()
	api.OK(w, map[string]bool{"obtaining": true})
}

// downloadCert 下载证书或私钥文件。
func (h *handler) downloadCert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	c, ok := h.m.findCert(id)
	if !ok {
		api.Fail(w, 404, "证书不存在")
		return
	}
	part := r.URL.Query().Get("part")
	certFile, keyFile := h.m.certPath(&c)
	var fp, ext string
	switch part {
	case "cert":
		fp, ext = certFile, ".crt"
	case "key":
		fp, ext = keyFile, ".key"
	default:
		api.Fail(w, 400, "part 必须是 cert 或 key")
		return
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		api.Fail(w, 404, "证书文件不存在，请先申请")
		return
	}
	name := c.Name
	if name == "" {
		name = c.ID
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	name = filepath.Base(name)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ext}))
	w.Write(data)
}
