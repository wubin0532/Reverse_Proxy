package forward

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/go-chi/chi/v5"

	"luckyx/internal/api"
	"luckyx/internal/config"
)

// RegisterRoutes 在已认证的 chi 分组上注册端口转发 CRUD 路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, svc *Service) {
	r.Get("/api/forwards", func(w http.ResponseWriter, _ *http.Request) {
		cfg.RLock()
		defer cfg.RUnlock()
		api.OK(w, cfg.Forwards)
	})

	r.Post("/api/forwards", func(w http.ResponseWriter, req *http.Request) {
		var rule config.ForwardRule
		if err := api.DecodeBody(req, &rule); err != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		rule.ID = newID()
		cfg.Lock()
		cfg.Forwards = append(cfg.Forwards, rule)
		cfg.Unlock()
		saveAndReload(w, cfg, svc)
	})

	r.Put("/api/forwards/{id}", func(w http.ResponseWriter, req *http.Request) {
		var body config.ForwardRule
		if err := api.DecodeBody(req, &body); err != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		id := chi.URLParam(req, "id")
		cfg.Lock()
		found := false
		for i := range cfg.Forwards {
			if cfg.Forwards[i].ID == id {
				body.ID = id
				cfg.Forwards[i] = body
				found = true
				break
			}
		}
		cfg.Unlock()
		if !found {
			api.Fail(w, 404, "规则不存在")
			return
		}
		saveAndReload(w, cfg, svc)
	})

	r.Delete("/api/forwards/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		cfg.Lock()
		found := false
		for i := range cfg.Forwards {
			if cfg.Forwards[i].ID == id {
				cfg.Forwards = append(cfg.Forwards[:i], cfg.Forwards[i+1:]...)
				found = true
				break
			}
		}
		cfg.Unlock()
		if !found {
			api.Fail(w, 404, "规则不存在")
			return
		}
		saveAndReload(w, cfg, svc)
	})

	r.Post("/api/forwards/{id}/toggle", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		cfg.Lock()
		found := false
		for i := range cfg.Forwards {
			if cfg.Forwards[i].ID == id {
				cfg.Forwards[i].Enabled = !cfg.Forwards[i].Enabled
				found = true
				break
			}
		}
		cfg.Unlock()
		if !found {
			api.Fail(w, 404, "规则不存在")
			return
		}
		saveAndReload(w, cfg, svc)
	})

	r.Get("/api/forwards/{id}/logs", func(w http.ResponseWriter, req *http.Request) {
		api.OK(w, svc.Logs(chi.URLParam(req, "id")))
	})
}

func saveAndReload(w http.ResponseWriter, cfg *config.Config, svc *Service) {
	if err := cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	svc.Reload()
	api.OK(w, nil)
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}
