package dashboard

import (
	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ddns"
	"andey-proxy/internal/guard"
	"github.com/go-chi/chi/v5"
	"net/http"
	"time"
)

// Keep slow module status separate from resource sampling and its failure state.
func RegisterWorkspaceRoutes(r chi.Router, cfg *config.Config, worker *ddns.Worker, protection *guard.Engine) {
	r.Get("/api/dashboard/workspace", func(w http.ResponseWriter, _ *http.Request) {
		cfg.RLock()
		tasks := append([]config.DDNSTask(nil), cfg.DDNS...)
		certs := append([]config.CertConf(nil), cfg.Certs...)
		cfg.RUnlock()
		dns := make([]map[string]interface{}, 0, len(tasks))
		certViews := make([]map[string]interface{}, 0, len(certs))
		now := time.Now()
		for _, task := range tasks {
			dns = append(dns, map[string]interface{}{"id": task.ID, "name": task.Name, "enabled": task.Enabled, "status": worker.Status(task.ID)})
		}
		for _, cert := range certs {
			state := cfg.State().Cert(cert.ID)
			certViews = append(certViews, map[string]interface{}{"id": cert.ID, "name": cert.Name, "enabled": cert.Enabled, "source": cert.Source, "notAfter": state.NotAfter, "remainingDays": config.RemainingDays(state.NotAfter, now)})
		}
		api.OK(w, map[string]interface{}{"time": now, "ddns": dns, "certs": certViews, "protection": protection.Summary()})
	})
}
