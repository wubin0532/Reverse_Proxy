package firewall

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
)

// RegisterRoutes 在已认证的 chi 分组上注册防火墙状态查询路由。
func RegisterRoutes(r chi.Router, m *Manager) {
	r.Get("/api/firewall/status", func(w http.ResponseWriter, _ *http.Request) {
		api.OK(w, map[string]interface{}{
			"openwrt": m.IsOpenWrt(),
			"rules":   m.Rules(),
		})
	})
}
