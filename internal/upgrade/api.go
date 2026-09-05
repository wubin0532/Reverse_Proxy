package upgrade

import (
	"net/http"
	"runtime"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func RegisterRoutes(r chi.Router, m *Manager, cfg *config.Config) {
	started := time.Now()
	r.Get("/api/system/info", func(w http.ResponseWriter, _ *http.Request) {
		api.OK(w, map[string]interface{}{"version": m.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "uptime": int64(time.Since(started).Seconds())})
	})
	r.Post("/api/system/update/inspect", func(w http.ResponseWriter, req *http.Request) {
		res, err := m.InspectUpload(w, req)
		if err != nil {
			api.Fail(w, 400, err.Error())
			return
		}
		api.OK(w, res)
	})
	r.Post("/api/system/update/{id}/install", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Password       string `json:"password"`
			AllowDowngrade bool   `json:"allowDowngrade"`
		}
		if api.DecodeBody(req, &body) != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		cfg.RLock()
		hash := cfg.Settings.AdminPassHash
		cfg.RUnlock()
		if api.PasswordConfirmLimited("upgrade", req.RemoteAddr) {
			api.Fail(w, http.StatusTooManyRequests, "密码错误次数过多，请稍后再试")
			return
		}
		if !auth.CheckPassword(hash, body.Password) {
			api.RecordPasswordConfirmFailure("upgrade", req.RemoteAddr)
			api.Fail(w, 403, "管理密码错误")
			return
		}
		api.ClearPasswordConfirmFailures("upgrade", req.RemoteAddr)
		if err := m.Install(chi.URLParam(req, "id"), body.AllowDowngrade); err != nil {
			api.Fail(w, 400, err.Error())
			return
		}
		api.OK(w, nil)
	})
	r.Delete("/api/system/update/{id}", func(w http.ResponseWriter, req *http.Request) {
		if !m.Cancel(chi.URLParam(req, "id")) {
			api.Fail(w, 404, "更新包不存在")
			return
		}
		api.OK(w, nil)
	})
	r.Get("/api/system/update/status", func(w http.ResponseWriter, _ *http.Request) { api.OK(w, m.Status()) })
}
