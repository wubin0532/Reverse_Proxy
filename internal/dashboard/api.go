package dashboard

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ddns"
	"andey-proxy/internal/firewall"
	"andey-proxy/internal/forward"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/upgrade"
	"andey-proxy/internal/webproxy"
)

// RegisterRoutes exposes a single resilient dashboard snapshot. A failure in one
// runtime module is represented as an issue instead of failing the whole page.
func RegisterRoutes(r chi.Router, cfg *config.Config, ddnsWorker *ddns.Worker, web *webproxy.Service, fwd *forward.Service, fw *firewall.Manager, logs *logcenter.Center, update *upgrade.Manager, version string, adminHTTPS bool) {
	r.Get("/api/dashboard", func(w http.ResponseWriter, _ *http.Request) {
		cfg.RLock()
		providers := len(cfg.Providers)
		ddnsTasks := append([]config.DDNSTask(nil), cfg.DDNS...)
		certs := append([]config.CertConf(nil), cfg.Certs...)
		sites := append([]config.Site(nil), cfg.Sites...)
		forwards := append([]config.ForwardRule(nil), cfg.Forwards...)
		mustChange := cfg.Settings.MustChangePassword
		totpEnabled := cfg.Settings.TOTPEnabled
		cfg.RUnlock()

		stats := map[string]int{"providers": providers, "ddns": len(ddnsTasks), "certs": len(certs), "sites": len(sites), "forwards": len(forwards)}
		issues := make([]map[string]string, 0)
		for _, task := range ddnsTasks {
			if task.Enabled {
				stats["ddnsEnabled"]++
				if st := ddnsWorker.Status(task.ID); st != nil && !st.Success {
					issues = append(issues, map[string]string{"module": "DDNS", "id": task.ID, "message": st.Message, "path": "/ddns"})
				}
			}
		}
		for _, cert := range certs {
			if cert.LastError != "" {
				issues = append(issues, map[string]string{"module": "证书", "id": cert.ID, "message": cert.LastError, "path": "/certs"})
			}
			if expires, err := time.Parse(time.RFC3339, cert.NotAfter); err == nil && expires.After(time.Now()) {
				stats["certsOk"]++
			}
		}
		for _, site := range sites {
			status, errMsg := web.SiteStatus(site.ID)
			if status == "listening" {
				stats["sitesListening"]++
			} else if site.Enabled {
				issues = append(issues, map[string]string{"module": "Web", "id": site.ID, "message": errMsg, "path": "/web-service"})
			}
		}
		for _, rule := range forwards {
			if rule.Enabled {
				stats["forwardsEnabled"]++
				status, errMsg := fwd.RuleStatus(rule.ID)
				if status != "listening" {
					issues = append(issues, map[string]string{"module": "转发", "id": rule.ID, "message": errMsg, "path": "/forward"})
				}
			}
		}
		if !adminHTTPS {
			issues = append(issues, map[string]string{"module": "安全", "message": "管理后台正在使用明文 HTTP", "path": "/dashboard"})
		}
		if mustChange {
			issues = append(issues, map[string]string{"module": "安全", "message": "请立即修改一次性初始密码", "path": "/dashboard"})
		}
		recentErrors, _ := logs.Query(logcenter.Query{Level: "error", Limit: 8})
		recentUpdate, _ := logs.Query(logcenter.Query{Source: "update", Limit: 1})
		api.OK(w, map[string]interface{}{
			"version":            version,
			"adminHttps":         adminHTTPS,
			"mustChangePassword": mustChange,
			"totpEnabled":        totpEnabled,
			"stats":              stats,
			"issues":             issues,
			"firewall":           map[string]interface{}{"openwrt": fw.IsOpenWrt(), "rules": fw.Rules()},
			"lastUpdate":         update.Status(),
			"lastUpdateEntries":  recentUpdate,
			"recentErrors":       recentErrors,
		})
	})
}
