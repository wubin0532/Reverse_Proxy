package upgrade

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
)

// RegisterRoutes 在已认证的 chi.Group 中挂载系统信息与在线升级路由。
// version 由 main 注入（构建时 -X main.version）。
func RegisterRoutes(r chi.Router, version string) {
	NewManager(version).routes(r)
}

// routes 挂载 Manager 的 HTTP 路由，供 RegisterRoutes 与测试使用。
func (m *Manager) routes(r chi.Router) {
	r.Get("/api/system/info", m.handleInfo)
	r.Get("/api/system/upgrade/check", m.handleCheck)
	r.Post("/api/system/upgrade", m.handleUpgrade)
	r.Get("/api/system/upgrade/status", m.handleStatus)
}

// handleInfo 返回系统信息：版本、平台、运行时长。
func (m *Manager) handleInfo(w http.ResponseWriter, r *http.Request) {
	api.OK(w, map[string]interface{}{
		"version": m.version,
		"goos":    runtime.GOOS,
		"goarch":  runtime.GOARCH,
		"uptime":  m.Uptime(),
	})
}

// handleCheck 查询 GitHub 最新版本并比较。
func (m *Manager) handleCheck(w http.ResponseWriter, r *http.Request) {
	res, err := m.Check(r.Context())
	if err != nil {
		api.Fail(w, 500, err.Error())
		return
	}
	api.OK(w, res)
}

// handleUpgrade 触发异步升级，立即返回；已有升级进行中返回 409。
func (m *Manager) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version string `json:"version"`
	}
	// body 可省略，空体按默认 latest 处理
	if err := api.DecodeBody(r, &body); err != nil && !errors.Is(err, io.EOF) {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	if !m.Start(body.Version) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(api.Response{Code: 409, Msg: "已有升级任务进行中"})
		return
	}
	api.OK(w, map[string]interface{}{"upgrading": true})
}

// handleStatus 返回升级状态机当前状态。
func (m *Manager) handleStatus(w http.ResponseWriter, r *http.Request) {
	api.OK(w, m.Status())
}
