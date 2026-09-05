package notify

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

// knownTypePrefixes 允许订阅的事件类型前缀（按模块归类）。
var knownTypePrefixes = []string{"cert", "ddns", "site", "forward"}

type handler struct {
	cfg     *config.Config
	bus     *Bus
	webhook *Webhook
}

// RegisterRoutes 在已认证的 chi.Group 中挂载通知相关路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, bus *Bus, webhook *Webhook) {
	h := &handler{cfg: cfg, bus: bus, webhook: webhook}
	r.Get("/api/notify/events", h.listEvents)
	r.Get("/api/notify/settings", h.getSettings)
	r.Put("/api/notify/settings", h.putSettings)
	r.Post("/api/notify/test", h.testWebhook)
}

// listEvents 返回最近事件（新的在前），limit 默认 20、上限 100。
func (h *handler) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if s := r.URL.Query().Get("limit"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			limit = v
		}
	}
	api.OK(w, h.bus.Recent(limit))
}

func (h *handler) getSettings(w http.ResponseWriter, _ *http.Request) {
	h.cfg.RLock()
	hookURL := h.cfg.Settings.NotifyWebhookURL
	types := append([]string(nil), h.cfg.Settings.NotifyTypes...)
	h.cfg.RUnlock()
	// Webhook URL 本身不是凭据（userinfo 已被禁止），可明文返回。
	api.OK(w, map[string]interface{}{"notifyWebhookURL": hookURL, "notifyTypes": types})
}

func (h *handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NotifyWebhookURL string   `json:"notifyWebhookURL"`
		NotifyTypes      []string `json:"notifyTypes"`
	}
	if err := api.DecodeBody(r, &body); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	body.NotifyWebhookURL = strings.TrimSpace(body.NotifyWebhookURL)
	if err := ValidateWebhookURL(body.NotifyWebhookURL); err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
	for _, t := range body.NotifyTypes {
		if !validType(t) {
			api.Fail(w, 400, "未知的事件类型: "+t)
			return
		}
	}
	if err := h.cfg.Update(func(c *config.Config) error {
		c.Settings.NotifyWebhookURL = body.NotifyWebhookURL
		c.Settings.NotifyTypes = body.NotifyTypes
		return nil
	}); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	api.OK(w, map[string]interface{}{"notifyWebhookURL": body.NotifyWebhookURL, "notifyTypes": body.NotifyTypes})
}

// validType 事件类型须是已知模块前缀或已知完整类型。
func validType(t string) bool {
	for _, p := range knownTypePrefixes {
		if t == p || strings.HasPrefix(t, p+".") {
			return true
		}
	}
	return false
}

// testWebhook 同步发送测试事件，返回发送结果。
func (h *handler) testWebhook(w http.ResponseWriter, _ *http.Request) {
	if err := h.webhook.Test(); err != nil {
		api.Fail(w, 502, "发送失败: "+err.Error())
		return
	}
	api.OK(w, map[string]string{"result": "测试事件已发送"})
}
