package ddns

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
)

type handler struct {
	cfg *config.Config
	w   *Worker
}

// RegisterRoutes 在已认证的 chi.Group 中挂载 DDNS 相关路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, w *Worker) {
	h := &handler{cfg: cfg, w: w}
	r.Get("/api/ddns/tasks", h.listTasks)
	r.Post("/api/ddns/tasks", h.createTask)
	r.Put("/api/ddns/tasks/{id}", h.updateTask)
	r.Delete("/api/ddns/tasks/{id}", h.deleteTask)
	r.Post("/api/ddns/tasks/{id}/toggle", h.toggleTask)
	r.Post("/api/ddns/tasks/{id}/run", h.runTask)

	r.Get("/api/providers", h.listProviders)
	r.Post("/api/providers", h.createProvider)
	r.Put("/api/providers/{id}", h.updateProvider)
	r.Delete("/api/providers/{id}", h.deleteProvider)
	r.Post("/api/providers/test", h.testProvider)
}

func newID() string {
	buf := make([]byte, 4)
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

type taskView struct {
	config.DDNSTask
	Status *TaskStatus `json:"status,omitempty"`
}

func (h *handler) listTasks(w http.ResponseWriter, r *http.Request) {
	h.cfg.RLock()
	tasks := make([]config.DDNSTask, len(h.cfg.DDNS))
	copy(tasks, h.cfg.DDNS)
	h.cfg.RUnlock()
	views := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, taskView{DDNSTask: t, Status: h.w.Status(t.ID)})
	}
	api.OK(w, views)
}

func (h *handler) validateTask(t *config.DDNSTask) (int, string) {
	if t.Name == "" {
		return 400, "任务名称不能为空"
	}
	if len(t.Domains) == 0 {
		return 400, "域名列表不能为空"
	}
	if t.IPType != "ipv4" && t.IPType != "ipv6" {
		return 400, "IP 类型必须是 ipv4 或 ipv6"
	}
	switch t.IPSource {
	case "interface":
		if t.Interface == "" {
			return 400, "网卡名不能为空"
		}
	case "api":
		if t.APIURL == "" {
			return 400, "IP 查询地址不能为空"
		}
	case "webhook":
	default:
		return 400, "IP 来源必须是 interface / api / webhook"
	}
	h.cfg.RLock()
	found := false
	for _, p := range h.cfg.Providers {
		if p.ID == t.ProviderID {
			found = true
			break
		}
	}
	h.cfg.RUnlock()
	if !found {
		return 400, "引用的服务商凭据不存在"
	}
	return 0, ""
}

func (h *handler) createTask(w http.ResponseWriter, r *http.Request) {
	var t config.DDNSTask
	if err := api.DecodeBody(r, &t); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	t.ID = ""
	if code, msg := h.validateTask(&t); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	t.ID = newID()
	h.cfg.Lock()
	h.cfg.DDNS = append(h.cfg.DDNS, t)
	h.cfg.Unlock()
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	api.OK(w, t)
}

func (h *handler) updateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var t config.DDNSTask
	if err := api.DecodeBody(r, &t); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	t.ID = id
	if code, msg := h.validateTask(&t); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	h.cfg.Lock()
	idx := -1
	for i := range h.cfg.DDNS {
		if h.cfg.DDNS[i].ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		h.cfg.DDNS[idx] = t
	}
	h.cfg.Unlock()
	if idx < 0 {
		api.Fail(w, 404, "任务不存在")
		return
	}
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	api.OK(w, t)
}

func (h *handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.cfg.Lock()
	idx := -1
	for i := range h.cfg.DDNS {
		if h.cfg.DDNS[i].ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		h.cfg.DDNS = append(h.cfg.DDNS[:idx], h.cfg.DDNS[idx+1:]...)
	}
	h.cfg.Unlock()
	if idx < 0 {
		api.Fail(w, 404, "任务不存在")
		return
	}
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	api.OK(w, nil)
}

func (h *handler) toggleTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.cfg.Lock()
	idx := -1
	for i := range h.cfg.DDNS {
		if h.cfg.DDNS[i].ID == id {
			idx = i
			break
		}
	}
	var enabled bool
	if idx >= 0 {
		h.cfg.DDNS[idx].Enabled = !h.cfg.DDNS[idx].Enabled
		enabled = h.cfg.DDNS[idx].Enabled
	}
	h.cfg.Unlock()
	if idx < 0 {
		api.Fail(w, 404, "任务不存在")
		return
	}
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	api.OK(w, map[string]bool{"enabled": enabled})
}

func (h *handler) runTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.w.RunNow(id); err != nil {
		api.Fail(w, 500, err.Error())
		return
	}
	api.OK(w, h.w.Status(id))
}

func (h *handler) listProviders(w http.ResponseWriter, r *http.Request) {
	h.cfg.RLock()
	providers := make([]config.DNSProviderConf, len(h.cfg.Providers))
	copy(providers, h.cfg.Providers)
	h.cfg.RUnlock()
	api.OK(w, providers)
}

func (h *handler) validateProvider(p *config.DNSProviderConf) (int, string) {
	switch p.Type {
	case "aliyun", "cloudflare", "dnspod":
	default:
		return 400, "服务商类型必须是 aliyun / cloudflare / dnspod"
	}
	if p.Key == "" {
		return 400, "Key 不能为空"
	}
	if p.Type != "cloudflare" && p.Secret == "" {
		return 400, "Secret 不能为空"
	}
	return 0, ""
}

func (h *handler) createProvider(w http.ResponseWriter, r *http.Request) {
	var p config.DNSProviderConf
	if err := api.DecodeBody(r, &p); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	p.ID = ""
	if code, msg := h.validateProvider(&p); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	p.ID = p.Type + "-" + newID()
	h.cfg.Lock()
	h.cfg.Providers = append(h.cfg.Providers, p)
	h.cfg.Unlock()
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	api.OK(w, p)
}

func (h *handler) updateProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var p config.DNSProviderConf
	if err := api.DecodeBody(r, &p); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	p.ID = id
	if code, msg := h.validateProvider(&p); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	h.cfg.Lock()
	idx := -1
	for i := range h.cfg.Providers {
		if h.cfg.Providers[i].ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		h.cfg.Providers[idx] = p
	}
	h.cfg.Unlock()
	if idx < 0 {
		api.Fail(w, 404, "服务商凭据不存在")
		return
	}
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	api.OK(w, p)
}

func (h *handler) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h.cfg.RLock()
	used := ""
	for _, t := range h.cfg.DDNS {
		if t.ProviderID == id {
			used = t.Name
			break
		}
	}
	h.cfg.RUnlock()
	if used != "" {
		api.Fail(w, 400, "该凭据正被任务「"+used+"」使用，无法删除")
		return
	}
	h.cfg.Lock()
	idx := -1
	for i := range h.cfg.Providers {
		if h.cfg.Providers[i].ID == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		h.cfg.Providers = append(h.cfg.Providers[:idx], h.cfg.Providers[idx+1:]...)
	}
	h.cfg.Unlock()
	if idx < 0 {
		api.Fail(w, 404, "服务商凭据不存在")
		return
	}
	if err := h.cfg.Save(); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	api.OK(w, nil)
}

// testProvider 只读查询一条记录，验证凭据可用，不做任何修改。
func (h *handler) testProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type       string `json:"type"`
		Key        string `json:"key"`
		Secret     string `json:"secret"`
		Endpoint   string `json:"endpoint"`
		Domain     string `json:"domain"`
		RecordType string `json:"recordType"`
	}
	if err := api.DecodeBody(r, &body); err != nil || body.Domain == "" {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	if body.RecordType == "" {
		body.RecordType = "A"
	}
	provider, err := NewProvider(config.DNSProviderConf{
		Type:     body.Type,
		Key:      body.Key,
		Secret:   body.Secret,
		Endpoint: body.Endpoint,
	})
	if err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	value, err := provider.QueryRecord(ctx, body.Domain, body.RecordType)
	if err != nil {
		api.Fail(w, 500, "凭据测试失败: "+err.Error())
		return
	}
	msg := "凭据有效，记录 " + body.Domain + " 当前不存在"
	if value != "" {
		msg = "凭据有效，记录 " + body.Domain + " 当前值: " + value
	}
	api.OK(w, map[string]string{"message": msg, "value": value})
}
