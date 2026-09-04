package webproxy

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

// apiHandler 站点管理 API。
type apiHandler struct {
	cfg *config.Config
	svc *Service
}

// RegisterRoutes 挂载 Web 服务相关路由（由主控在鉴权分组内调用）。
func RegisterRoutes(r chi.Router, cfg *config.Config, svc *Service) {
	h := &apiHandler{cfg: cfg, svc: svc}
	r.Get("/api/sites", h.list)
	r.Post("/api/sites", h.create)
	r.Put("/api/sites/{id}", h.update)
	r.Delete("/api/sites/{id}", h.delete)
	r.Post("/api/sites/{id}/toggle", h.toggle)
	r.Get("/api/sites/{id}/logs", h.logs)
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
	if site.Listen == "" {
		api.Fail(w, 400, "监听地址不能为空")
		return
	}
	site.ID = genID()
	ensureRuleIDs(&site)

	h.cfg.Lock()
	h.cfg.Sites = append(h.cfg.Sites, site)
	h.cfg.Unlock()
	if !h.saveAndReload(w) {
		return
	}
	api.OK(w, site)
}

func (h *apiHandler) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var site config.Site
	if err := api.DecodeBody(r, &site); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	if site.Listen == "" {
		api.Fail(w, 400, "监听地址不能为空")
		return
	}
	site.ID = id
	ensureRuleIDs(&site)

	h.cfg.Lock()
	found := false
	for i := range h.cfg.Sites {
		if h.cfg.Sites[i].ID == id {
			h.cfg.Sites[i] = site
			found = true
			break
		}
	}
	h.cfg.Unlock()
	if !found {
		api.Fail(w, 404, "站点不存在")
		return
	}
	if !h.saveAndReload(w) {
		return
	}
	api.OK(w, site)
}

func (h *apiHandler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.cfg.Lock()
	found := false
	for i := range h.cfg.Sites {
		if h.cfg.Sites[i].ID == id {
			h.cfg.Sites = append(h.cfg.Sites[:i], h.cfg.Sites[i+1:]...)
			found = true
			break
		}
	}
	h.cfg.Unlock()
	if !found {
		api.Fail(w, 404, "站点不存在")
		return
	}
	if !h.saveAndReload(w) {
		return
	}
	api.OK(w, nil)
}

func (h *apiHandler) toggle(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.cfg.Lock()
	found := false
	enabled := false
	for i := range h.cfg.Sites {
		if h.cfg.Sites[i].ID == id {
			h.cfg.Sites[i].Enabled = !h.cfg.Sites[i].Enabled
			enabled = h.cfg.Sites[i].Enabled
			found = true
			break
		}
	}
	h.cfg.Unlock()
	if !found {
		api.Fail(w, 404, "站点不存在")
		return
	}
	if !h.saveAndReload(w) {
		return
	}
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

// saveAndReload 保存配置并重载监听器；失败时写错误响应并返回 false。
func (h *apiHandler) saveAndReload(w http.ResponseWriter) bool {
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败: "+err.Error())
		return false
	}
	h.svc.Reload()
	return true
}

// ensureRuleIDs 给没有 ID 的子规则补 ID。
func ensureRuleIDs(site *config.Site) {
	for i := range site.Rules {
		if site.Rules[i].ID == "" {
			site.Rules[i].ID = genID()
		}
	}
}

// genID 生成 crypto/rand 短 hex ID（8 字符）。
func genID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
