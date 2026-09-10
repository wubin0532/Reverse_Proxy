package logcenter

import (
	"encoding/json"
	"fmt"
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
		if !validLogType(q.Type) {
			api.Fail(w, 400, "日志类型无效")
			return
		}
		entries, next := c.Query(q)
		api.OK(w, map[string]interface{}{"entries": entries, "nextCursor": next})
	})
	r.Get("/api/logs/download", func(w http.ResponseWriter, req *http.Request) {
		q := queryFrom(req)
		if !validLogType(q.Type) {
			api.Fail(w, 400, "日志类型无效")
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		name := "andey-proxy-logs.ndjson"
		if q.Type != "" {
			name = fmt.Sprintf("andey-proxy-%s-logs.ndjson", q.Type)
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		_ = c.Export(w, q)
	})
	// SSE 日志流：事件格式 "event: log\ndata: <Entry JSON>\n\n"，每 25s 一行心跳注释。
	r.Get("/api/logs/stream", func(w http.ResponseWriter, req *http.Request) {
		q := queryFrom(req)
		if !validLogType(q.Type) {
			api.Fail(w, 400, "日志类型无效")
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			api.Fail(w, 500, "当前环境不支持流式输出")
			return
		}
		sub, ok := c.hub.subscribe(q)
		if !ok {
			api.Fail(w, http.StatusTooManyRequests, "日志流连接数已达上限")
			return
		}
		defer c.hub.unsubscribe(sub)
		// 主服务设有 WriteTimeout（120s），长连接须清除写期限，否则流会被中途切断。
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()
		heartbeat := time.NewTicker(streamHeartbeat)
		defer heartbeat.Stop()
		for {
			select {
			case e, ok := <-sub.ch:
				if !ok {
					return // Center 关闭（进程退出/重载）
				}
				data, err := json.Marshal(e)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "event: log\ndata: %s\n\n", data)
				flusher.Flush()
			case <-heartbeat.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			case <-req.Context().Done():
				return
			}
		}
	})
	r.Post("/api/logs/clear", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Password string `json:"password"`
			Type     string `json:"type"` // 可选：access / system，缺省清空全部
		}
		if api.DecodeBody(req, &body) != nil {
			api.Fail(w, 400, "请求格式错误")
			return
		}
		if !validLogType(body.Type) {
			api.Fail(w, 400, "日志类型无效")
			return
		}
		cfg.RLock()
		hash := cfg.Settings.AdminPassHash
		cfg.RUnlock()
		if !api.AdmitPasswordConfirm("logs", req.RemoteAddr) {
			api.Fail(w, http.StatusTooManyRequests, "密码错误次数过多，请稍后再试")
			return
		}
		release, ok := auth.AcquireVerifySlot(3 * time.Second)
		if !ok {
			api.Fail(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
			return
		}
		valid := auth.CheckPassword(hash, body.Password)
		release()
		if !valid {
			api.Fail(w, 403, "管理密码错误")
			return
		}
		api.ClearPasswordConfirmFailures("logs", req.RemoteAddr)
		var err error
		if body.Type != "" {
			err = c.ClearType(body.Type)
		} else {
			err = c.Clear()
		}
		if err != nil {
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
	return Query{Level: r.URL.Query().Get("level"), Source: r.URL.Query().Get("source"), Keyword: r.URL.Query().Get("q"), EntityID: r.URL.Query().Get("entityId"), Type: r.URL.Query().Get("type"), Limit: limit, Cursor: cursor, From: from, To: to}
}
