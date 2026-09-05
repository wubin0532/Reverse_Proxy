package logcenter

import (
	"net/http"
	"strconv"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

func RegisterRoutes(r chi.Router, c *Center, cfg *config.Config) {
	r.Get("/api/logs", func(w http.ResponseWriter, req *http.Request) {
		q := queryFrom(req)
		entries, next := c.Query(q)
		api.OK(w, map[string]interface{}{"entries": entries, "nextCursor": next})
	})
	r.Get("/api/logs/download", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("Content-Disposition", `attachment; filename="andey-proxy-logs.ndjson"`)
		_ = c.Export(w, queryFrom(req))
	})
	r.Post("/api/logs/clear", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Password string `json:"password"`
		}
		if api.DecodeBody(req, &body) != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		cfg.RLock()
		hash := cfg.Settings.AdminPassHash
		cfg.RUnlock()
		if api.PasswordConfirmLimited("logs", req.RemoteAddr) {
			api.Fail(w, http.StatusTooManyRequests, "密码错误次数过多，请稍后再试")
			return
		}
		if !auth.CheckPassword(hash, body.Password) {
			api.RecordPasswordConfirmFailure("logs", req.RemoteAddr)
			api.Fail(w, 403, "管理密码错误")
			return
		}
		api.ClearPasswordConfirmFailures("logs", req.RemoteAddr)
		if err := c.Clear(); err != nil {
			api.Fail(w, 500, "清空日志失败")
			return
		}
		c.Add(Entry{Level: "warn", Source: "security", Message: "管理员已清空日志"})
		api.OK(w, nil)
	})
}

func queryFrom(r *http.Request) Query {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	cursor, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
	from, _ := time.Parse(time.RFC3339, r.URL.Query().Get("from"))
	to, _ := time.Parse(time.RFC3339, r.URL.Query().Get("to"))
	return Query{Level: r.URL.Query().Get("level"), Source: r.URL.Query().Get("source"), Keyword: r.URL.Query().Get("q"), EntityID: r.URL.Query().Get("entityId"), Limit: limit, Cursor: cursor, From: from, To: to}
}
