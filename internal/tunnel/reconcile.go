package tunnel

import (
	"net/http"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

type routeConflict struct {
	Hostname       string `json:"hostname"`
	Service        string `json:"service"`
	DesiredService string `json:"desiredService"`
	Removing       bool   `json:"removing"`
	Recoverable    bool   `json:"recoverable"`
}

func routeConflicts(inst config.TunnelInstance, remote cloudConfiguration, dir string) []routeConflict {
	entries := make(map[string][]remoteRouteView)
	for _, row := range remoteRouteViews(remote) {
		if row.Hostname != "" && row.Path == "" {
			entries[row.Hostname] = append(entries[row.Hostname], row)
		}
	}
	desired := make(map[string]config.TunnelRoute)
	for _, route := range inst.Routes {
		desired[route.ID] = route
	}
	seen := make(map[string]bool)
	owned := ownedRouteBindings(inst)
	out := []routeConflict{}
	for _, route := range append(append([]config.TunnelRoute{}, inst.Routes...), inst.RetiredRoutes...) {
		for _, host := range []string{route.AppliedHostname, route.PendingHostname} {
			rows := entries[host]
			if host == "" || seen[host] || len(rows) == 0 {
				continue
			}
			seen[host] = true
			row := rows[0]
			binding := owned[host]
			if len(rows) == 1 && ((host == binding.AppliedHostname && row.rawService == binding.AppliedService) || (host == binding.PendingHostname && row.rawService == binding.PendingService)) {
				continue
			}
			item := routeConflict{Hostname: host, Service: row.Service, Removing: true, Recoverable: len(rows) == 1 && (row.Editable || (binding.Target == "site" && row.rawService == routeService(dir, binding)))}
			if target, ok := desired[binding.ID]; ok {
				item.Removing = false
				item.DesiredService = routeService(dir, target)
			}
			out = append(out, item)
		}
	}
	return out
}

// Acknowledge only explicitly selected bindings from the displayed cloud snapshot.
// This changes local ownership metadata; publishing remains a separate operation.
func (h *handler) reconcileRoutes(r *http.Request) (any, error) {
	var input struct {
		Digest    string   `json:"digest"`
		Hostnames []string `json:"hostnames"`
	}
	if err := decode(r, &input); err != nil {
		return nil, err
	}
	if len(input.Hostnames) == 0 || len(input.Hostnames) > 100 {
		return nil, invalid("请选择需要确认的云端路由")
	}
	inst, err := h.m.instance(chi.URLParam(r, "id"))
	if err != nil {
		return nil, requestError{404, err.Error()}
	}
	c, err := h.m.cloud(inst)
	if err != nil {
		return nil, invalid(err.Error())
	}
	if err = h.m.checkConnectors(r.Context(), c, inst); err != nil {
		return nil, conflict(err.Error())
	}
	remote, err := c.configuration(r.Context(), inst.TunnelID)
	if err != nil {
		return nil, invalid(err.Error())
	}
	if input.Digest == "" || configDigest(remote) != input.Digest {
		return nil, conflict("云端配置已变化，请刷新后重新确认")
	}
	changes := make(map[string]routeConflict)
	for _, change := range routeConflicts(inst, remote, h.m.cfg.Dir()) {
		changes[change.Hostname] = change
	}
	selected := make(map[string]string)
	for _, hostname := range input.Hostnames {
		change, ok := changes[hostname]
		if !ok || !change.Recoverable {
			return nil, invalid("所选路由不可重新接管，请检查云端配置")
		}
		selected[hostname] = change.Service
	}
	acknowledge := func(routes []config.TunnelRoute) {
		for i := range routes {
			if service, ok := selected[routes[i].AppliedHostname]; ok {
				routes[i].AppliedService = service
			}
			if service, ok := selected[routes[i].PendingHostname]; ok {
				routes[i].PendingService = service
			}
		}
	}
	acknowledge(inst.Routes)
	acknowledge(inst.RetiredRoutes)
	return nil, h.m.saveInstance(inst)
}
