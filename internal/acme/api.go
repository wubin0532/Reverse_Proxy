package acme

import (
	"errors"
	"log"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/url"
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
	errCertNotFound        = errors.New("证书不存在")
	errCertInUse           = errors.New("证书正在使用")
	errCertProviderMissing = errors.New("引用的服务商凭据不存在")
	errCertSourceChange    = errors.New("证书来源不可通过普通编辑改变，请使用导入替换")
)

// RegisterRoutes 在已认证的 chi.Group 中挂载证书相关路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, m *Manager) {
	h := &handler{cfg: cfg, m: m}
	r.Get("/api/certs", h.listCerts)
	r.Post("/api/certs", h.createCert)
	r.Post("/api/certs/import", h.importCert)
	r.Put("/api/certs/{id}", h.updateCert)
	r.Delete("/api/certs/{id}", h.deleteCert)
	r.Post("/api/certs/{id}/toggle", h.toggleCert)
	r.Post("/api/certs/{id}/obtain", h.obtainCert)
	r.Get("/api/certs/{id}/download", h.downloadCert)
	r.Put("/api/certs/{id}/mappings", h.updateMappings)
	r.Get("/api/certs/{id}/mappings", func(w http.ResponseWriter, q *http.Request) { api.OK(w, m.MappingResults(chi.URLParam(q, "id"))) })
	r.Post("/api/certs/{id}/mappings/sync", func(w http.ResponseWriter, q *http.Request) {
		results, err := m.SyncMappings(q.Context(), chi.URLParam(q, "id"))
		if err != nil {
			api.Fail(w, 500, err.Error())
			return
		}
		api.OK(w, results)
	})
}

// certView 证书视图：用户配置 + 运行状态（state.json）合成，保持 API 响应结构不变。
type certView struct {
	config.CertConf
	CertFile        string          `json:"certFile"`
	KeyFile         string          `json:"keyFile"`
	NotAfter        string          `json:"notAfter"`
	LastError       string          `json:"lastError,omitempty"`
	Status          string          `json:"status"` // pending / ok / expiring / expired / error
	MappingRevision string          `json:"mappingRevision"`
	MappingPaths    []string        `json:"mappingPaths"`
	MappingResults  []MappingResult `json:"mappingResults"`
	EABConfigured   bool            `json:"eabConfigured"`
	RemainingDays   *int            `json:"remainingDays"`
	UsingSites      []usingSite     `json:"usingSites"`
	Obtaining       bool            `json:"obtaining"` // 是否正在申请中
}

type usingSite struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// viewOf 由配置与运行状态合成证书视图。
func (h *handler) viewOf(c config.CertConf, now time.Time) certView {
	st := h.cfg.State().Cert(c.ID)
	eabConfigured := c.EABHMAC != ""
	c.EABHMAC = ""
	c.Source = certSource(c)
	remaining := config.RemainingDays(st.NotAfter, now)
	sites := []usingSite{}
	h.cfg.RLock()
	for _, site := range h.cfg.Sites {
		if site.CertID == c.ID {
			sites = append(sites, usingSite{ID: site.ID, Name: site.Name})
		}
	}
	h.cfg.RUnlock()
	return certView{
		RemainingDays: remaining, UsingSites: sites, EABConfigured: eabConfigured, MappingRevision: mappingRevision(c.Mappings), MappingPaths: mappingPaths(h.cfg.Dir(), c), MappingResults: h.m.MappingResults(c.ID),
		CertConf:  c,
		CertFile:  st.CertFile,
		KeyFile:   st.KeyFile,
		NotAfter:  st.NotAfter,
		LastError: st.LastError,
		Status:    statusOf(&c, st, now),
		Obtaining: h.m.Obtaining(c.ID),
	}
}

// statusOf 根据配置与运行状态计算证书状态。
func statusOf(c *config.CertConf, st config.CertState, now time.Time) string {
	if st.NotAfter == "" {
		if st.LastError != "" {
			return "error"
		}
		return "pending"
	}
	notAfter, err := time.Parse(time.RFC3339, st.NotAfter)
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
		views = append(views, h.viewOf(certs[i], now))
	}
	api.OK(w, views)
}

func (h *handler) validateCert(c *config.CertConf) (int, string) {
	if err := c.ValidateAdvanced(); err != nil {
		return 400, err.Error()
	}
	if c.Source != "" && c.Source != "acme" && c.Source != "uploaded" {
		return 400, "证书来源无效"
	}
	if c.Source != "uploaded" {
		if _, err := certificateKeyType(c.KeyType); err != nil {
			return 400, err.Error()
		}
	}
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
	if c.Source == "uploaded" {
		return 0, ""
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

// Recheck references inside the configuration commit; an earlier form validation
// cannot protect against a simultaneous provider deletion.
func validateCertProviderLocked(c *config.Config, cert config.CertConf) error {
	if certSource(cert) == "uploaded" {
		return nil
	}
	for _, p := range c.Providers {
		if p.ID == cert.ProviderID {
			return nil
		}
	}
	return errCertProviderMissing
}

func (h *handler) createCert(w http.ResponseWriter, r *http.Request) {
	var c config.CertConf
	if err := api.DecodeBody(r, &c); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	c.ID = ""
	if c.Source == "uploaded" {
		api.Fail(w, 400, "导入证书请使用导入接口")
		return
	}
	if code, msg := h.validateCert(&c); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	c.ID = ids.New()
	if err := h.cfg.Update(func(cfg *config.Config) error {
		if err := validateCertProviderLocked(cfg, c); err != nil {
			return err
		}
		cfg.Certs = append(cfg.Certs, c)
		return nil
	}); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.m.Reload()
	log.Printf("[security] 新增证书配置，ID: %s", c.ID)
	api.OK(w, h.viewOf(c, time.Now()))
}

func (h *handler) updateCert(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var c config.CertConf
	if err := api.DecodeBody(r, &c); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	c.ID = id
	keepHMAC := c.EABEnabled && c.EABHMAC == ""
	if keepHMAC {
		h.cfg.RLock()
		for _, old := range h.cfg.Certs {
			if old.ID == id && old.EABKeyID == c.EABKeyID {
				c.EABHMAC = old.EABHMAC
				break
			}
		}
		h.cfg.RUnlock()
	}
	if code, msg := h.validateCert(&c); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	domainsChanged := false
	err := h.cfg.Update(func(cfg *config.Config) error {
		if err := validateCertProviderLocked(cfg, c); err != nil {
			return err
		}
		for i := range cfg.Certs {
			if cfg.Certs[i].ID != id {
				continue
			}
			if keepHMAC {
				if c.EABKeyID != cfg.Certs[i].EABKeyID {
					return errors.New("EAB Key ID变更时须提供新密钥")
				}
				c.EABHMAC = cfg.Certs[i].EABHMAC
			}
			if !c.EABEnabled {
				c.EABHMAC = ""
				c.EABKeyID = ""
			}
			if certSource(cfg.Certs[i]) != certSource(c) {
				return errCertSourceChange
			}
			if c.Source == "uploaded" {
				pair, err := h.m.loadCached(&c)
				if err != nil || !coversDomains(pair, c.Domains) {
					return errCertCoverage
				}
				c.KeyType = cfg.Certs[i].KeyType
			}
			// 运行状态按证书 ID 存于状态库，配置更新天然保留；域名变化时作废，等待重新申请
			domainsChanged = !sameDomains(cfg.Certs[i].Domains, c.Domains)
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
		code := 500
		message := "保存配置失败"
		if errors.Is(err, errCertCoverage) || errors.Is(err, errCertSourceChange) || errors.Is(err, errCertProviderMissing) {
			code = 400
			message = err.Error()
		}
		api.Fail(w, code, message)
		return
	}
	if domainsChanged && c.Source != "uploaded" {
		h.cfg.RLock()
		for _, current := range h.cfg.Certs {
			if current.ID == id && certSource(current) == certSource(c) && sameDomains(current.Domains, c.Domains) && current.KeyType == c.KeyType {
				h.cfg.State().Update(func(s *config.State) { delete(s.Certs, id) })
				break
			}
		}
		h.cfg.RUnlock()
	}
	h.m.invalidate(id)
	h.m.Reload()
	log.Printf("[security] 修改证书配置，ID: %s", id)
	api.OK(w, h.viewOf(c, time.Now()))
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
	h.cfg.State().Update(func(s *config.State) { delete(s.Certs, id) })
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
	h.m.Reload()
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
	if err := h.m.ObtainAsync(id); err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
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
	var ext string
	switch part {
	case "cert":
		ext = ".crt"
	case "key":
		ext = ".key"
	default:
		api.Fail(w, 400, "part 必须是 cert 或 key")
		return
	}
	data, err := h.m.ReadPEM(&c, part == "key")
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
