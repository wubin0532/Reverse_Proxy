package notify

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

type handler struct {
	cfg *config.Config
	n   *Notifier
}

// RegisterRoutes 在已认证的 chi.Group 中挂载 Webhook 通知相关路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, n *Notifier) {
	h := &handler{cfg: cfg, n: n}
	r.Get("/api/settings/webhook", h.getConf)
	r.Put("/api/settings/webhook", h.putConf)
	r.Post("/api/settings/webhook/test", h.testSend)
}

func (h *handler) getConf(w http.ResponseWriter, r *http.Request) {
	h.cfg.RLock()
	wc := h.cfg.Settings.Webhook
	h.cfg.RUnlock()
	api.OK(w, wc)
}

// validType 校验通知类型合法。
func validType(t string) bool {
	switch t {
	case "serverchan", "bark", "telegram", "custom":
		return true
	}
	return false
}

func (h *handler) putConf(w http.ResponseWriter, r *http.Request) {
	var wc config.WebhookConf
	if err := api.DecodeBody(r, &wc); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	if wc.Enabled && !validType(wc.Type) {
		api.Fail(w, 400, "不支持的通知类型: "+wc.Type)
		return
	}
	if wc.Enabled && wc.Type == "custom" && wc.URL == "" {
		api.Fail(w, 400, "自定义 Webhook 地址不能为空")
		return
	}
	h.cfg.Lock()
	h.cfg.Settings.Webhook = wc
	h.cfg.Unlock()
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	api.OK(w, wc)
}

// testSend 同步发送一条测试消息，返回成功或失败原因。
func (h *handler) testSend(w http.ResponseWriter, r *http.Request) {
	h.cfg.RLock()
	wc := h.cfg.Settings.Webhook
	h.cfg.RUnlock()
	if err := h.n.send(wc, "andey-Proxy 测试消息", "这是一条来自 andey-Proxy 的 Webhook 测试消息。"); err != nil {
		api.Fail(w, 500, "发送失败: "+err.Error())
		return
	}
	api.OK(w, nil)
}
