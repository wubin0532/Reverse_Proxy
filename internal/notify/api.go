package notify

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

var knownTypePrefixes = []string{"cert", "ddns", "site", "forward"}

type handler struct {
	cfg     *config.Config
	bus     *Bus
	manager *Manager
}

// RegisterRoutes 在已认证的 chi.Group 中挂载通知中心接口。
func RegisterRoutes(r chi.Router, cfg *config.Config, bus *Bus, manager *Manager) {
	h := &handler{cfg: cfg, bus: bus, manager: manager}
	r.Get("/api/notifications/events", h.listEvents)
	r.Get("/api/notifications/settings", h.getSettings)
	r.Put("/api/notifications/settings", h.putSettings)
	r.Post("/api/notifications/test/{channel}", h.testChannel)
	r.Delete("/api/notifications/channels/{channel}", h.deleteChannel)
}

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
	settings := h.cfg.Settings.Notifications
	settings.Types = append([]string{}, settings.Types...)
	h.cfg.RUnlock()
	api.OK(w, settingsResponse(settings))
}

type telegramRequest struct {
	Enabled         bool   `json:"enabled"`
	BotToken        string `json:"botToken"`
	ClearBotToken   bool   `json:"clearBotToken"`
	ChatID          string `json:"chatId"`
	MessageThreadID int64  `json:"messageThreadId"`
}

type settingsRequest struct {
	Types    []string        `json:"types"`
	Telegram telegramRequest `json:"telegram"`
}

func (h *handler) putSettings(w http.ResponseWriter, r *http.Request) {
	var body settingsRequest
	if err := api.DecodeBody(r, &body); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	for _, t := range body.Types {
		if !validType(t) {
			api.Fail(w, 400, "未知的事件类型: "+t)
			return
		}
	}
	body.Telegram.BotToken = strings.TrimSpace(body.Telegram.BotToken)
	body.Telegram.ChatID = strings.TrimSpace(body.Telegram.ChatID)
	var saved config.NotificationSettings
	var validationErr error
	if err := h.cfg.Update(func(c *config.Config) error {
		token := c.Settings.Notifications.Telegram.BotToken
		if body.Telegram.ClearBotToken {
			token = ""
		} else if body.Telegram.BotToken != "" {
			token = body.Telegram.BotToken
		}
		telegram := config.TelegramNotification{
			Enabled:         body.Telegram.Enabled,
			BotToken:        token,
			ChatID:          body.Telegram.ChatID,
			MessageThreadID: body.Telegram.MessageThreadID,
		}
		if err := ValidateTelegramConfig(telegram, true); err != nil {
			validationErr = err
			return err
		}
		saved = config.NotificationSettings{
			Types:    append([]string(nil), body.Types...),
			Telegram: telegram,
		}
		c.Settings.Notifications = saved
		return nil
	}); err != nil {
		if validationErr != nil {
			api.Fail(w, 400, validationErr.Error())
		} else {
			api.Fail(w, 500, "保存通知设置失败")
		}
		return
	}
	api.OK(w, settingsResponse(saved))
}

func settingsResponse(settings config.NotificationSettings) map[string]interface{} {
	tg := settings.Telegram
	return map[string]interface{}{
		"types": append([]string{}, settings.Types...),
		"telegram": map[string]interface{}{
			"enabled":            tg.Enabled,
			"configured":         tg.BotToken != "" && tg.ChatID != "",
			"botTokenConfigured": tg.BotToken != "",
			"chatId":             tg.ChatID,
			"messageThreadId":    tg.MessageThreadID,
		},
	}
}

func validType(t string) bool {
	for _, p := range knownTypePrefixes {
		if t == p || strings.HasPrefix(t, p+".") {
			return true
		}
	}
	return false
}

func (h *handler) testChannel(w http.ResponseWriter, r *http.Request) {
	if err := h.manager.Test(chi.URLParam(r, "channel")); err != nil {
		api.Fail(w, 502, "发送失败: "+err.Error())
		return
	}
	api.OK(w, map[string]string{"result": "测试消息已发送"})
}

func (h *handler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	channel := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "channel")))
	if channel != "telegram" {
		api.Fail(w, http.StatusNotFound, "通知渠道不存在")
		return
	}
	var saved config.NotificationSettings
	if err := h.cfg.Update(func(c *config.Config) error {
		c.Settings.Notifications.Telegram = config.TelegramNotification{}
		saved = c.Settings.Notifications
		saved.Types = append([]string(nil), saved.Types...)
		return nil
	}); err != nil {
		api.Fail(w, http.StatusInternalServerError, "删除通知渠道失败")
		return
	}
	log.Printf("[security] 已删除 Telegram 通知渠道配置")
	api.OK(w, settingsResponse(saved))
}
