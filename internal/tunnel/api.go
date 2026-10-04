package tunnel

import (
	"encoding/json"
	"errors"
	"net/http"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

type handler struct{ m *Manager }
type requestError struct {
	status  int
	message string
}

func (e requestError) Error() string { return e.message }
func invalid(message string) error   { return requestError{400, message} }
func conflict(message string) error  { return requestError{409, message} }

func reply(w http.ResponseWriter, data any, err error) {
	if err == nil {
		api.OK(w, data)
		return
	}
	var failure requestError
	if errors.As(err, &failure) {
		api.Fail(w, failure.status, failure.message)
	} else {
		api.Fail(w, 500, "保存配置失败")
	}
}
func decode(r *http.Request, out any) error {
	if api.DecodeBody(r, out) != nil {
		return invalid("请求格式错误")
	}
	return nil
}
func (h *handler) locked(w http.ResponseWriter, r *http.Request, fn func(*http.Request) (any, error)) {
	if !h.m.mutation.TryLock() {
		api.Fail(w, 409, "已有隧道操作正在进行")
		return
	}
	defer h.m.mutation.Unlock()
	data, err := fn(r)
	reply(w, data, err)
}

func RegisterRoutes(r chi.Router, m *Manager) {
	h := &handler{m: m}
	r.Get("/api/tunnels/runtime", func(w http.ResponseWriter, r *http.Request) { api.OK(w, m.runtimeView()) })
	r.Get("/api/tunnels/runtime/release", h.latestRuntime)
	r.Post("/api/tunnels/runtime/install", h.installRuntime)
	r.Get("/api/tunnels/accounts", h.listAccounts)
	r.Post("/api/tunnels/accounts", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.saveAccount) })
	r.Put("/api/tunnels/accounts/{id}", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.saveAccount) })
	r.Delete("/api/tunnels/accounts/{id}", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.deleteAccount) })
	r.Post("/api/tunnels/accounts/{id}/test", h.testAccount)
	r.Get("/api/tunnels/accounts/{id}/zones", h.zones)
	r.Get("/api/tunnels/instances", h.listInstances)
	r.Post("/api/tunnels/instances", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.saveInstance) })
	r.Put("/api/tunnels/instances/{id}", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.saveInstance) })
	r.Delete("/api/tunnels/instances/{id}", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.deleteInstance) })
	for _, action := range []string{"start", "stop", "restart"} {
		r.Post("/api/tunnels/instances/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			h.locked(w, r, func(r *http.Request) (any, error) { return h.control(r, action) })
		})
	}
	r.Post("/api/tunnels/instances/{id}/create", h.cloudOperation("create"))
	r.Post("/api/tunnels/instances/{id}/sync", h.cloudOperation("sync"))
	r.Post("/api/tunnels/instances/{id}/delete-cloud", h.cloudOperation("delete"))
	r.Get("/api/tunnels/instances/{id}/routes", h.getRoutes)
	r.Put("/api/tunnels/instances/{id}/routes", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.saveRoutes) })
	r.Post("/api/tunnels/instances/{id}/routes/reconcile", func(w http.ResponseWriter, r *http.Request) { h.locked(w, r, h.reconcileRoutes) })
	r.Get("/api/tunnels/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		op, ok := m.cfg.State().TunnelOperation(chi.URLParam(r, "id"))
		if !ok {
			api.Fail(w, 404, "操作不存在")
			return
		}
		api.OK(w, op)
	})
}

func (h *handler) cloudOperation(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Digest string `json:"digest"`
		}
		if r.ContentLength != 0 {
			if err := decode(r, &input); err != nil {
				reply(w, nil, err)
				return
			}
		}
		op, err := h.m.submit(chi.URLParam(r, "id"), kind, input.Digest)
		if err != nil {
			api.Fail(w, 409, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.Response{Code: 0, Msg: "ok", Data: op})
	}
}

type instanceView struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Source          string               `json:"source"`
	AccountRef      string               `json:"accountRef"`
	TunnelID        string               `json:"tunnelId"`
	Enabled         bool                 `json:"enabled"`
	Protocol        string               `json:"protocol"`
	IPVersion       string               `json:"ipVersion"`
	TokenConfigured bool                 `json:"tokenConfigured"`
	Routes          []config.TunnelRoute `json:"routes"`
	Status          Status               `json:"status"`
}

func (h *handler) view(inst config.TunnelInstance) instanceView {
	routes := inst.Routes
	if routes == nil {
		routes = []config.TunnelRoute{}
	}
	return instanceView{inst.ID, inst.Name, inst.Source, inst.AccountRef, inst.TunnelID, inst.Enabled, inst.Protocol, inst.IPVersion, inst.Token != "", routes, h.m.Status(inst.ID)}
}
func (h *handler) listInstances(w http.ResponseWriter, r *http.Request) {
	views := []instanceView{}
	for _, inst := range h.m.instances() {
		views = append(views, h.view(inst))
	}
	api.OK(w, views)
}
