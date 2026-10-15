package guard

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"andey-proxy/internal/api"
	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

var errAccessConflict = errors.New("安全配置已变更，请重新加载；草稿已保留")
var errAccessMissing = errors.New("安全对象不存在")
var errAccessReferenced = errors.New("安全对象仍被引用")

func accessError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	code := 400
	if errors.Is(err, errAccessMissing) {
		code = 404
	}
	if errors.Is(err, errAccessConflict) || errors.Is(err, errAccessReferenced) {
		code = 409
	}
	api.Fail(w, code, err.Error())
	return true
}
func registerAccessRoutes(r chi.Router, cfg *config.Config, e *Engine) {
	r.Get("/api/protection/access/groups", func(w http.ResponseWriter, q *http.Request) {
		cfg.RLock()
		out := append([]config.AccessGroup{}, cfg.AccessGroups...)
		cfg.RUnlock()
		api.OK(w, out)
	})
	r.Get("/api/protection/access/users", func(w http.ResponseWriter, q *http.Request) {
		cfg.RLock()
		out := append([]config.AccessUser{}, cfg.AccessUsers...)
		cfg.RUnlock()
		for i := range out {
			out[i].PasswordHash = ""
		}
		api.OK(w, out)
	})
	groupSave := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, q *http.Request) {
			var g config.AccessGroup
			if err := api.DecodeBody(q, &g); err != nil {
				api.FailDecode(w, err, "请求格式错误")
				return
			}
			if create {
				g.ID = ids.New()
				g.Revision = 1
			} else {
				g.ID = chi.URLParam(q, "id")
			}
			err := cfg.Update(func(c *config.Config) error {
				if create {
					c.AccessGroups = append(c.AccessGroups, g)
					return nil
				}
				for i, old := range c.AccessGroups {
					if old.ID == g.ID {
						if g.Revision != old.Revision {
							return errAccessConflict
						}
						g.Revision++
						c.AccessGroups[i] = g
						return nil
					}
				}
				return errAccessMissing
			})
			if accessError(w, err) {
				return
			}
			api.OK(w, g)
		}
	}
	r.Post("/api/protection/access/groups", groupSave(true))
	r.Put("/api/protection/access/groups/{id}", groupSave(false))
	userSave := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, q *http.Request) {
			var body struct {
				config.AccessUser
				Password string `json:"password"`
			}
			if err := api.DecodeBody(q, &body); err != nil {
				api.FailDecode(w, err, "请求格式错误")
				return
			}
			u := body.AccessUser
			u.PasswordHash = ""
			if body.Password != "" {
				if len(body.Password) < 10 || len(body.Password) > 72 {
					api.Fail(w, 400, "用户密码须为10到72字节")
					return
				}
				hash, err := auth.HashPassword(body.Password)
				if accessError(w, err) {
					return
				}
				u.PasswordHash = hash
			}
			if create {
				u.ID = ids.New()
				u.Revision = 1
				if u.PasswordHash == "" {
					api.Fail(w, 400, "新用户须提供密码")
					return
				}
			} else {
				u.ID = chi.URLParam(q, "id")
			}
			err := cfg.Update(func(c *config.Config) error {
				if create {
					c.AccessUsers = append(c.AccessUsers, u)
					return nil
				}
				for i, old := range c.AccessUsers {
					if old.ID == u.ID {
						if u.Revision != old.Revision {
							return errAccessConflict
						}
						if u.PasswordHash == "" {
							u.PasswordHash = old.PasswordHash
						}
						u.Revision++
						c.AccessUsers[i] = u
						return nil
					}
				}
				return errAccessMissing
			})
			if accessError(w, err) {
				return
			}
			u.PasswordHash = ""
			api.OK(w, u)
		}
	}
	r.Post("/api/protection/access/users", userSave(true))
	r.Put("/api/protection/access/users/{id}", userSave(false))
	remove := func(group bool) http.HandlerFunc {
		return func(w http.ResponseWriter, q *http.Request) {
			id := chi.URLParam(q, "id")
			var revision uint64
			if q.Header.Get("If-Match") == "" {
				api.Fail(w, 400, "须提供If-Match版本")
				return
			}
			if n, err := strconv.ParseUint(strings.Trim(q.Header.Get("If-Match"), "\""), 10, 64); err != nil {
				api.Fail(w, 400, "版本无效")
				return
			} else {
				revision = n
			}
			err := cfg.Update(func(c *config.Config) error {
				if group {
					for _, site := range c.Sites {
						for _, rule := range site.Rules {
							if rule.AccessGroupID == id {
								return errAccessReferenced
							}
						}
					}
					for _, rule := range c.Forwards {
						if rule.AccessGroupID == id {
							return errAccessReferenced
						}
					}
					for i, g := range c.AccessGroups {
						if g.ID == id {
							if g.Revision != revision {
								return errAccessConflict
							}
							c.AccessGroups = append(c.AccessGroups[:i], c.AccessGroups[i+1:]...)
							return nil
						}
					}
				} else {
					for _, g := range c.AccessGroups {
						for _, uid := range g.UserIDs {
							if uid == id {
								return errAccessReferenced
							}
						}
					}
					for i, u := range c.AccessUsers {
						if u.ID == id {
							if u.Revision != revision {
								return errAccessConflict
							}
							c.AccessUsers = append(c.AccessUsers[:i], c.AccessUsers[i+1:]...)
							return nil
						}
					}
				}
				return errAccessMissing
			})
			if accessError(w, err) {
				return
			}
			api.OK(w, nil)
		}
	}
	r.Delete("/api/protection/access/groups/{id}", remove(true))
	r.Delete("/api/protection/access/users/{id}", remove(false))
	r.Get("/api/protection/access/sessions", func(w http.ResponseWriter, q *http.Request) { api.OK(w, e.AccessSessions()) })
	r.Delete("/api/protection/access/sessions/{id}", func(w http.ResponseWriter, q *http.Request) {
		e.RevokeAccessSession(chi.URLParam(q, "id"))
		api.OK(w, nil)
	})
	r.Post("/api/protection/access/grants", func(w http.ResponseWriter, q *http.Request) {
		var grant AccessSession
		if err := api.DecodeBody(q, &grant); err != nil {
			api.FailDecode(w, err, "请求格式错误")
			return
		}
		if accessError(w, e.AddAccessGrant(grant)) {
			return
		}
		api.OK(w, e.AccessSessions())
	})
}
