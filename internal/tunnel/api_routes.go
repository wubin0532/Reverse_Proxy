package tunnel

import (
	"encoding/json"
	"net/http"
	"net/url"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

type remoteRouteView struct {
	rawService    string
	Hostname      string `json:"hostname"`
	Service       string `json:"service"`
	Path          string `json:"path"`
	Editable      bool   `json:"editable"`
	SkipTLSVerify bool   `json:"skipTlsVerify"`
}

func remoteRouteViews(remote cloudConfiguration) []remoteRouteView {
	var ingress []map[string]json.RawMessage
	_ = json.Unmarshal(remote.Config["ingress"], &ingress)
	out := []remoteRouteView{}
	for _, entry := range ingress {
		var row remoteRouteView
		_ = json.Unmarshal(entry["hostname"], &row.Hostname)
		_ = json.Unmarshal(entry["service"], &row.Service)
		row.rawService = row.Service
		_ = json.Unmarshal(entry["path"], &row.Path)
		var origin struct {
			SkipTLSVerify bool `json:"noTLSVerify"`
		}
		_ = json.Unmarshal(entry["originRequest"], &origin)
		row.SkipTLSVerify = origin.SkipTLSVerify
		u, err := url.Parse(row.Service)
		row.Editable = err == nil && u != nil && (u.Scheme == "http" || u.Scheme == "https") && row.Hostname != "" && row.Path == "" && u.User == nil
		if u != nil && u.User != nil {
			u.User = nil
			row.Service = u.String()
			row.Editable = false
		}
		out = append(out, row)
	}
	return out
}

func (h *handler) getRoutes(w http.ResponseWriter, r *http.Request) {
	inst, err := h.m.instance(chi.URLParam(r, "id"))
	if err != nil {
		api.Fail(w, 404, err.Error())
		return
	}
	services := make(map[string]string)
	for _, route := range inst.Routes {
		services[route.ID] = routeService(h.m.cfg.Dir(), route)
	}
	result := map[string]any{"routes": h.view(inst).Routes, "remote": []remoteRouteView{}, "conflicts": []routeConflict{}, "digest": "", "readOnly": true, "cloudError": "", "services": services}
	if inst.AccountRef != "" && inst.TunnelID != "" {
		c, err := h.m.cloud(inst)
		if err == nil {
			var remote cloudConfiguration
			remote, err = c.configuration(r.Context(), inst.TunnelID)
			if err == nil {
				result["digest"] = configDigest(remote)
				result["remote"] = remoteRouteViews(remote)
				result["conflicts"] = routeConflicts(inst, remote, h.m.cfg.Dir())
				err = h.m.checkConnectors(r.Context(), c, inst)
				result["readOnly"] = err != nil
			}
		}
		if err != nil {
			result["cloudError"] = err.Error()
		}
	}
	api.OK(w, result)
}

type routeInput struct {
	ID            string `json:"id"`
	Hostname      string `json:"hostname"`
	ZoneID        string `json:"zoneId"`
	Target        string `json:"target"`
	SiteID        string `json:"siteId"`
	URL           string `json:"url"`
	SkipTLSVerify bool   `json:"skipTlsVerify"`
	AdoptHostname string `json:"adoptHostname,omitempty"`
}

func (h *handler) saveRoutes(r *http.Request) (any, error) {
	inst, err := h.m.instance(chi.URLParam(r, "id"))
	if err != nil {
		return nil, requestError{404, err.Error()}
	}
	var input struct {
		Routes []routeInput `json:"routes"`
		Digest string       `json:"digest"`
	}
	if err = decode(r, &input); err != nil {
		return nil, err
	}
	if len(input.Routes) > 100 {
		return nil, invalid("每个实例最多配置 100 条路由")
	}
	previous := make(map[string]config.TunnelRoute)
	for _, route := range inst.Routes {
		previous[route.ID] = route
	}
	desired := []config.TunnelRoute{}
	seenID := map[string]bool{}
	seenHost := map[string]bool{}
	var remote cloudConfiguration
	loaded := false
	for _, item := range input.Routes {
		old, exists := previous[item.ID]
		if item.ID != "" && !exists {
			return nil, invalid("路由 ID 不存在")
		}
		route := old
		route.Hostname = item.Hostname
		route.ZoneID = item.ZoneID
		route.Target = item.Target
		route.SiteID = item.SiteID
		route.URL = item.URL
		route.SkipTLSVerify = item.SkipTLSVerify
		if !exists {
			route.ID = ids.New()
		}
		if err = h.m.validateRoute(&route); err != nil {
			return nil, invalid(err.Error())
		}
		if seenID[route.ID] || seenHost[route.Hostname] {
			return nil, invalid("路由 ID 或域名重复")
		}
		seenID[route.ID] = true
		seenHost[route.Hostname] = true
		for _, other := range h.m.instances() {
			if other.ID != inst.ID {
				for _, otherRoute := range other.Routes {
					if otherRoute.Hostname == route.Hostname {
						return nil, conflict("该域名已被另一个本机隧道使用")
					}
				}
			}
		}
		changed := exists && (old.Hostname != route.Hostname || old.ZoneID != route.ZoneID || routeService(h.m.cfg.Dir(), old) != routeService(h.m.cfg.Dir(), route) || old.SkipTLSVerify != route.SkipTLSVerify)
		if changed && (old.AppliedHostname != "" || old.PendingHostname != "") {
			inst.RetiredRoutes = append(inst.RetiredRoutes, old)
		}
		if exists && (old.Hostname != route.Hostname || old.ZoneID != route.ZoneID) {
			route.DNSRecordID = ""
			route.DNSOwned = false
		}
		if item.AdoptHostname != "" {
			if exists || inst.AccountRef == "" || inst.TunnelID == "" {
				return nil, invalid("仅能将选中的云端路由导入为新路由")
			}
			if !loaded {
				c, cloudErr := h.m.cloud(inst)
				if cloudErr != nil {
					return nil, invalid(cloudErr.Error())
				}
				if cloudErr = h.m.checkConnectors(r.Context(), c, inst); cloudErr != nil {
					return nil, conflict(cloudErr.Error())
				}
				remote, cloudErr = c.configuration(r.Context(), inst.TunnelID)
				if cloudErr != nil {
					return nil, invalid(cloudErr.Error())
				}
				if input.Digest == "" || configDigest(remote) != input.Digest {
					return nil, conflict("云端配置已变化，请刷新后重新导入")
				}
				loaded = true
			}
			matched := false
			for _, row := range remoteRouteViews(remote) {
				if row.Hostname == item.AdoptHostname && row.Editable {
					if matched {
						return nil, invalid("该域名有多条云端路由，不支持导入")
					}
					matched = true
					route.AppliedHostname = row.Hostname
					route.AppliedService = row.Service
					route.AppliedSkipTLSVerify = row.SkipTLSVerify
				}
			}
			if !matched {
				return nil, invalid("未找到可导入的云端 HTTP/HTTPS 路由")
			}
			for _, owned := range inst.Routes {
				if owned.AppliedHostname == route.AppliedHostname {
					return nil, conflict("该云端路由已被管理")
				}
			}
		}
		desired = append(desired, route)
	}
	for _, old := range inst.Routes {
		if !seenID[old.ID] && (old.AppliedHostname != "" || old.PendingHostname != "") {
			inst.RetiredRoutes = append(inst.RetiredRoutes, old)
		}
	}
	inst.Routes = desired
	if err = h.m.saveInstance(inst); err != nil {
		return nil, err
	}
	h.m.Reload()
	return h.view(inst).Routes, nil
}
