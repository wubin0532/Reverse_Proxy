package webproxy

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

// Admissions remain shared across all rules referring to a pool, including
// during configuration reload; replacing a handler does not reset in-flight use.
type poolAdmission struct {
	active  atomic.Int64
	mu      sync.Mutex
	nodes   map[string]*poolNodeSlot
	members int
	retired bool
	remove  func()
}
type poolNodeSlot struct {
	active     atomic.Int64
	references int
}

func (p *poolAdmission) node(url string) *atomic.Int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nodes == nil {
		p.nodes = map[string]*poolNodeSlot{}
	}
	n := p.nodes[url]
	if n == nil {
		n = &poolNodeSlot{}
		p.nodes[url] = n
	}
	n.references++
	return &n.active
}
func (p *poolAdmission) release(urls []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, url := range urls {
		if n := p.nodes[url]; n != nil {
			n.references--
			if n.references == 0 {
				delete(p.nodes, url)
			}
		}
	}
	p.members--
	if p.members == 0 {
		p.retired = true
		p.remove()
	}
}
func takeSlot(active *atomic.Int64, limit int) bool {
	if limit <= 0 {
		limit = 4096
	}
	for {
		old := active.Load()
		if old >= int64(limit) {
			return false
		}
		if active.CompareAndSwap(old, old+1) {
			return true
		}
	}
}
func (s *Service) poolAdmissionFor(id string) *poolAdmission {
	for {
		fresh := &poolAdmission{}
		fresh.remove = func() { s.poolAdmissions.CompareAndDelete(id, fresh) }
		v, _ := s.poolAdmissions.LoadOrStore(id, fresh)
		p := v.(*poolAdmission)
		p.mu.Lock()
		if !p.retired {
			p.members++
			p.mu.Unlock()
			return p
		}
		p.mu.Unlock()
	}
}

// Caller holds config read lock. The resolved copy is runtime-only; source
// configuration keeps the reference rather than duplicating pool nodes.
func (s *Service) runtimeSiteLocked(site config.Site) config.Site {
	out := cloneSite(site)
	for i := range out.Rules {
		r := &out.Rules[i]
		if r.BackendPoolID == "" {
			continue
		}
		for _, p := range s.cfg.BackendPools {
			if p.ID == r.BackendPoolID {
				v := p
				v.Nodes = append([]config.BackendNode(nil), p.Nodes...)
				r.ResolvedPool = &v
				r.Backends = nil
				for _, n := range v.Nodes {
					if n.Enabled {
						r.Backends = append(r.Backends, n.URL)
					}
				}
				break
			}
		}
	}
	return out
}

func (h *apiHandler) registerBackendPools(r chi.Router) {
	r.Get("/api/backend-pools", func(w http.ResponseWriter, _ *http.Request) {
		h.cfg.RLock()
		out := append([]config.BackendPool{}, h.cfg.BackendPools...)
		h.cfg.RUnlock()
		api.OK(w, out)
	})
	save := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, q *http.Request) {
			h.mutationMu.Lock()
			defer h.mutationMu.Unlock()
			var p config.BackendPool
			if err := api.DecodeBody(q, &p); err != nil {
				api.FailDecode(w, err, "请求格式错误")
				return
			}
			if err := p.Validate(); err != nil {
				api.Fail(w, 400, err.Error())
				return
			}
			if create {
				p.ID = ids.New()
				p.Revision = 1
			} else {
				p.ID = chi.URLParam(q, "id")
			}
			err := h.cfg.Update(func(c *config.Config) error {
				if create {
					c.BackendPools = append(c.BackendPools, p)
					return nil
				}
				for i, old := range c.BackendPools {
					if old.ID == p.ID {
						if p.Revision != old.Revision {
							return errRevisionConflict
						}
						p.Revision++
						c.BackendPools[i] = p
						return nil
					}
				}
				return errRuleNotFound
			})
			if err != nil {
				code := 500
				if errors.Is(err, errRevisionConflict) {
					code = 409
				}
				if errors.Is(err, errRuleNotFound) {
					code = 404
				}
				api.Fail(w, code, err.Error())
				return
			}
			if err := h.svc.Reload(); err != nil {
				api.Fail(w, 500, "后端池已保存，运行重载失败")
				return
			}
			api.OK(w, p)
		}
	}
	r.Post("/api/backend-pools", save(true))
	r.Put("/api/backend-pools/{id}", save(false))
	r.Delete("/api/backend-pools/{id}", func(w http.ResponseWriter, q *http.Request) {
		h.mutationMu.Lock()
		defer h.mutationMu.Unlock()
		id := chi.URLParam(q, "id")
		revision, err := headerRevision(q)
		if err != nil || revision == nil {
			api.Fail(w, 400, "须提供 If-Match 版本")
			return
		}
		referenced := errors.New("后端池仍被子规则引用")
		err = h.cfg.Update(func(c *config.Config) error {
			for _, s := range c.Sites {
				for _, r := range s.Rules {
					if r.BackendPoolID == id {
						return referenced
					}
				}
			}
			for i, p := range c.BackendPools {
				if p.ID == id {
					if *revision != p.Revision {
						return errRevisionConflict
					}
					c.BackendPools = append(c.BackendPools[:i], c.BackendPools[i+1:]...)
					return nil
				}
			}
			return errRuleNotFound
		})
		if err != nil {
			code := 500
			if errors.Is(err, referenced) || errors.Is(err, errRevisionConflict) {
				code = 409
			}
			if errors.Is(err, errRuleNotFound) {
				code = 404
			}
			api.Fail(w, code, err.Error())
			return
		}
		api.OK(w, nil)
	})
}
