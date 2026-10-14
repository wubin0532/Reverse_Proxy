package guard

import (
	"errors"
	"net/http"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

var errMissingPolicy = errors.New("防护策略不存在")
var errReferencedPolicy = errors.New("防护策略仍被Web站点、子规则或转发规则引用")

func referenced(c *config.Config, id string) bool {
	for _, s := range c.Sites {
		if s.PolicyID == id {
			return true
		}
		for _, r := range s.Rules {
			if r.PolicyID == id {
				return true
			}
		}
	}
	for _, r := range c.Forwards {
		if r.PolicyID == id {
			return true
		}
	}
	return false
}
func RegisterRoutes(r chi.Router, cfg *config.Config, e *Engine) {
	registerTrapRoutes(r, e)
	registerAccessRoutes(r, cfg, e)
	r.Get("/api/protection/policies", func(w http.ResponseWriter, _ *http.Request) {
		cfg.RLock()
		p := append([]config.ProtectionPolicy{}, cfg.ProtectionPolicies...)
		cfg.RUnlock()
		api.OK(w, p)
	})
	save := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			e.mutation.Lock()
			defer e.mutation.Unlock()
			var p config.ProtectionPolicy
			if api.DecodeBody(req, &p) != nil {
				api.Fail(w, 400, "请求格式错误")
				return
			}
			if err := ValidatePolicy(&p); err != nil {
				api.Fail(w, 400, err.Error())
				return
			}
			if p.Enabled && p.WAF != nil && p.WAF.Enabled {
				if _, err := prepareWAF(); err != nil {
					api.Fail(w, 400, "WAF初始化失败")
					return
				}
			}
			if create {
				p.ID = ids.New()
			} else {
				p.ID = chi.URLParam(req, "id")
			}
			err := cfg.Update(func(c *config.Config) error {
				if create {
					if len(c.ProtectionPolicies) >= 256 {
						return errors.New("防护策略最多256条")
					}
					c.ProtectionPolicies = append(c.ProtectionPolicies, p)
					return nil
				}
				for i := range c.ProtectionPolicies {
					if c.ProtectionPolicies[i].ID == p.ID {
						c.ProtectionPolicies[i] = p
						return nil
					}
				}
				return errMissingPolicy
			})
			if err != nil {
				status := 500
				if errors.Is(err, errMissingPolicy) {
					status = 404
				}
				api.Fail(w, status, err.Error())
				return
			}
			if err := e.Reload(); err != nil {
				api.Fail(w, 500, "策略已保存，重载失败")
				return
			}
			api.OK(w, p)
		}
	}
	r.Post("/api/protection/policies", save(true))
	r.Put("/api/protection/policies/{id}", save(false))
	r.Delete("/api/protection/policies/{id}", func(w http.ResponseWriter, req *http.Request) {
		e.mutation.Lock()
		defer e.mutation.Unlock()
		id := chi.URLParam(req, "id")
		err := cfg.Update(func(c *config.Config) error {
			if referenced(c, id) {
				return errReferencedPolicy
			}
			for i, p := range c.ProtectionPolicies {
				if p.ID == id {
					c.ProtectionPolicies = append(c.ProtectionPolicies[:i], c.ProtectionPolicies[i+1:]...)
					return nil
				}
			}
			return errMissingPolicy
		})
		if err != nil {
			status := 500
			if errors.Is(err, errMissingPolicy) {
				status = 404
			}
			if errors.Is(err, errReferencedPolicy) {
				status = 409
			}
			api.Fail(w, status, err.Error())
			return
		}
		if e.Reload() != nil {
			api.Fail(w, 500, "重载失败")
			return
		}
		api.OK(w, nil)
	})
	r.Get("/api/protection/bans", func(w http.ResponseWriter, _ *http.Request) { api.OK(w, e.Bans()) })
	r.Post("/api/protection/bans", func(w http.ResponseWriter, req *http.Request) {
		var b Ban
		if api.DecodeBody(req, &b) != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		if err := e.AddBan(b); err != nil {
			api.Fail(w, 400, err.Error())
			return
		}
		api.OK(w, e.Bans())
	})
	r.Delete("/api/protection/bans/{ip}", func(w http.ResponseWriter, req *http.Request) { e.DeleteBan(chi.URLParam(req, "ip")); api.OK(w, nil) })
	r.Get("/api/protection/events", func(w http.ResponseWriter, _ *http.Request) { api.OK(w, e.Events()) })
	r.Get("/api/protection/summary", func(w http.ResponseWriter, _ *http.Request) { api.OK(w, e.Summary()) })
}
