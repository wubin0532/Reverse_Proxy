package ddns

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
)

type handler struct {
	cfg *config.Config
	w   *Worker
}

var (
	errTaskNotFound     = errors.New("任务不存在")
	errProviderNotFound = errors.New("服务商凭据不存在")
	errProviderInUse    = errors.New("服务商凭据正在使用")
)

// RegisterRoutes 在已认证的 chi.Group 中挂载 DDNS 相关路由。
func RegisterRoutes(r chi.Router, cfg *config.Config, w *Worker) {
	h := &handler{cfg: cfg, w: w}
	r.Get("/api/ddns/tasks", h.listTasks)
	r.Post("/api/ddns/tasks", h.createTask)
	r.Put("/api/ddns/tasks/{id}", h.updateTask)
	r.Delete("/api/ddns/tasks/{id}", h.deleteTask)
	r.Post("/api/ddns/tasks/{id}/toggle", h.toggleTask)
	r.Post("/api/ddns/tasks/{id}/run", h.runTask)
	r.Get("/api/ddns/tasks/{id}/history", h.taskHistory)
	r.Post("/api/ddns/tasks/{id}/records", h.createRecord)
	r.Put("/api/ddns/tasks/{id}/records/{recordId}", h.updateRecord)
	r.Delete("/api/ddns/tasks/{id}/records/{recordId}", h.deleteRecord)
	r.Post("/api/ddns/tasks/{id}/records/{recordId}/toggle", h.toggleRecord)

	r.Get("/api/providers", h.listProviders)
	r.Post("/api/providers", h.createProvider)
	r.Put("/api/providers/{id}", h.updateProvider)
	r.Delete("/api/providers/{id}", h.deleteProvider)
	r.Post("/api/providers/test", h.testProvider)

	r.Get("/api/ddns/interfaces", h.listInterfaces)
	r.Post("/api/ddns/preview-ip", h.previewIP)
}

type taskView struct {
	config.DDNSTask
	Status           *TaskStatus         `json:"status,omitempty"`
	Revision         string              `json:"revision"`
	EffectiveRecords []config.DDNSRecord `json:"effectiveRecords"`
}

func (h *handler) listTasks(w http.ResponseWriter, r *http.Request) {
	h.cfg.RLock()
	tasks := make([]config.DDNSTask, len(h.cfg.DDNS))
	copy(tasks, h.cfg.DDNS)
	h.cfg.RUnlock()
	views := make([]taskView, 0, len(tasks))
	for _, t := range tasks {
		views = append(views, h.taskView(t))
	}
	api.OK(w, views)
}

func (h *handler) validateTask(t *config.DDNSTask) (int, string) {
	if code, msg := validateTaskShape(t); code != 0 {
		return code, msg
	}
	h.cfg.RLock()
	found := providerExistsLocked(h.cfg, t.ProviderID)
	h.cfg.RUnlock()
	if !found {
		return 400, "引用的服务商凭据不存在"
	}
	return 0, ""
}

func (h *handler) taskView(task config.DDNSTask) taskView {
	return taskView{DDNSTask: task, Status: h.w.Status(task.ID), Revision: taskRevision(task), EffectiveRecords: effectiveRecords(task)}
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
	t.ID = ids.New()
	if err := h.cfg.Update(func(c *config.Config) error {
		if !providerExistsLocked(c, t.ProviderID) {
			return errProviderNotFound
		}
		c.DDNS = append(c.DDNS, t)
		return nil
	}); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	log.Printf("[security] 新增 DDNS 任务，ID: %s", t.ID)
	api.OK(w, h.taskView(t))
}

func (h *handler) updateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		config.DDNSTask
		Revision string `json:"revision"`
	}
	if err := api.DecodeBody(r, &body); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	task := body.DDNSTask
	task.ID = id
	err := h.cfg.Update(func(cfg *config.Config) error {
		for i, current := range cfg.DDNS {
			if current.ID != id {
				continue
			}
			if body.Revision != "" && body.Revision != taskRevision(current) {
				return errTaskConflict
			}
			if (len(task.Records) > 0 || len(current.Records) > 0) && body.Revision == "" {
				return errTaskConflict
			}
			if task.Records != nil && len(task.Records) == 0 && len(current.Records) > 0 {
				return fmt.Errorf("validation:400:任务至少保留一条记录，可停用最后一条记录")
			}
			// Legacy-only clients may still edit legacy tasks; record tasks require a revision.
			if task.Records == nil && len(current.Records) > 0 {
				task.Records = append([]config.DDNSRecord(nil), current.Records...)
				task.IPv4Source, task.IPv6Source, task.ForceInterval = current.IPv4Source, current.IPv6Source, current.ForceInterval
			}
			if code, msg := validateTaskShape(&task); code != 0 {
				return fmt.Errorf("validation:%d:%s", code, msg)
			}
			if !providerExistsLocked(cfg, task.ProviderID) {
				return errProviderNotFound
			}
			cfg.DDNS[i] = task
			return nil
		}
		return errTaskNotFound
	})
	if h.taskMutationError(w, err) {
		return
	}
	h.w.Reload()
	log.Printf("[security] 修改 DDNS 任务，ID: %s", id)
	api.OK(w, h.taskView(task))
}

func taskActionRevision(r *http.Request) (string, error) {
	var body struct {
		Revision string `json:"revision"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if err := api.DecodeBody(r, &body); err != nil {
			return "", err
		}
	}
	return body.Revision, nil
}
func checkTaskActionRevision(task config.DDNSTask, revision string) error {
	if revision != "" && revision != taskRevision(task) || len(task.Records) > 0 && revision == "" {
		return errTaskConflict
	}
	return nil
}
func (h *handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	revision, err := taskActionRevision(r)
	if err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	err = h.cfg.Update(func(c *config.Config) error {
		for i, task := range c.DDNS {
			if task.ID == id {
				if err := checkTaskActionRevision(task, revision); err != nil {
					return err
				}
				c.DDNS = append(c.DDNS[:i], c.DDNS[i+1:]...)
				return nil
			}
		}
		return errTaskNotFound
	})
	if h.taskMutationError(w, err) {
		return
	}
	h.w.Reload()
	log.Printf("[security] 删除 DDNS 任务，ID: %s", id)
	api.OK(w, nil)
}
func (h *handler) toggleTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	revision, err := taskActionRevision(r)
	if err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	var enabled bool
	err = h.cfg.Update(func(c *config.Config) error {
		for i, task := range c.DDNS {
			if task.ID == id {
				if err := checkTaskActionRevision(task, revision); err != nil {
					return err
				}
				c.DDNS[i].Enabled = !task.Enabled
				enabled = c.DDNS[i].Enabled
				return nil
			}
		}
		return errTaskNotFound
	})
	if h.taskMutationError(w, err) {
		return
	}
	h.w.Reload()
	log.Printf("[security] 切换 DDNS 任务，ID: %s，启用: %t", id, enabled)
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
	type providerView struct {
		ID               string `json:"id"`
		Type             string `json:"type"`
		Remark           string `json:"remark"`
		Endpoint         string `json:"endpoint,omitempty"`
		KeyConfigured    bool   `json:"keyConfigured"`
		SecretConfigured bool   `json:"secretConfigured"`
	}
	views := make([]providerView, 0, len(providers))
	for _, p := range providers {
		views = append(views, providerView{ID: p.ID, Type: p.Type, Remark: p.Remark, Endpoint: p.Endpoint, KeyConfigured: p.Key != "", SecretConfigured: p.Secret != ""})
	}
	api.OK(w, views)
}

func (h *handler) validateProvider(p *config.DNSProviderConf) (int, string) {
	p.Type = strings.ToLower(strings.TrimSpace(p.Type))
	p.Remark = strings.TrimSpace(p.Remark)
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
	if p.Endpoint != "" {
		u, err := validateHTTPURL(p.Endpoint, false)
		if err != nil {
			return 400, "自定义端点无效: " + err.Error()
		}
		p.Endpoint = strings.TrimSuffix(u.String(), "/")
	}
	return 0, ""
}

// endpointChanged 判断请求指定的端点是否与已存配置不同；请求未指定端点视为未变更。
// 已存与请求均为空同样视为未变更。
func endpointChanged(requested, saved string) bool {
	return requested != "" && requested != saved
}

func validateHTTPURL(raw string, allowSafeQuery bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("必须是有效的 HTTP 或 HTTPS URL")
	}
	if u.User != nil {
		return nil, fmt.Errorf("URL 不能包含用户名或密码")
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("非本机地址必须使用 HTTPS")
		}
	}
	if u.Fragment != "" {
		return nil, fmt.Errorf("URL 不能包含片段")
	}
	if !allowSafeQuery && u.RawQuery != "" {
		return nil, fmt.Errorf("服务商端点不能包含查询参数")
	}
	if allowSafeQuery {
		for key := range u.Query() {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "key") || strings.Contains(lower, "auth") {
				return nil, fmt.Errorf("查询参数不能携带凭据")
			}
		}
	}
	return u, nil
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
	p.ID = p.Type + "-" + ids.New()
	if err := h.cfg.Update(func(c *config.Config) error {
		c.Providers = append(c.Providers, p)
		return nil
	}); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	log.Printf("[security] 新增 DNS 服务商凭据，ID: %s，类型: %s", p.ID, p.Type)
	api.OK(w, map[string]interface{}{"id": p.ID, "type": p.Type, "remark": p.Remark, "keyConfigured": true, "secretConfigured": p.Secret != ""})
}

func (h *handler) updateProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Type          string `json:"type"`
		Remark        string `json:"remark"`
		Key           string `json:"key"`
		Secret        string `json:"secret"`
		Endpoint      string `json:"endpoint"`
		ClearSecret   bool   `json:"clearSecret"`
		ClearEndpoint bool   `json:"clearEndpoint"`
	}
	if err := api.DecodeBody(r, &body); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	p := config.DNSProviderConf{ID: id, Type: body.Type, Remark: body.Remark, Key: body.Key, Secret: body.Secret, Endpoint: body.Endpoint}
	p.ID = id
	err := h.cfg.Update(func(c *config.Config) error {
		idx := -1
		for i := range c.Providers {
			if c.Providers[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return errProviderNotFound
		}
		// 端点变更后禁止回填已保存的凭据，防止凭据被发往未授权的端点。
		if endpointChanged(p.Endpoint, c.Providers[idx].Endpoint) {
			if p.Key == "" {
				return fmt.Errorf("validation:%d:%s", 400, "更换端点后需重新输入凭据，不能使用已保存的凭据")
			}
		} else {
			if p.Key == "" {
				p.Key = c.Providers[idx].Key
			}
			if p.Secret == "" && !body.ClearSecret {
				p.Secret = c.Providers[idx].Secret
			}
		}
		if p.Endpoint == "" && !body.ClearEndpoint {
			p.Endpoint = c.Providers[idx].Endpoint
		}
		if code, msg := h.validateProvider(&p); code != 0 {
			return fmt.Errorf("validation:%d:%s", code, msg)
		}
		c.Providers[idx] = p
		return nil
	})
	if errors.Is(err, errProviderNotFound) {
		api.Fail(w, 404, "服务商凭据不存在")
		return
	}
	if err != nil && strings.HasPrefix(err.Error(), "validation:") {
		parts := strings.SplitN(err.Error(), ":", 3)
		code, _ := strconv.Atoi(parts[1])
		api.Fail(w, code, parts[2])
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	log.Printf("[security] 修改 DNS 服务商凭据，ID: %s", id)
	api.OK(w, map[string]interface{}{"id": p.ID, "type": p.Type, "remark": p.Remark, "keyConfigured": p.Key != "", "secretConfigured": p.Secret != ""})
}

func (h *handler) deleteProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	used := ""
	err := h.cfg.Update(func(c *config.Config) error {
		for _, t := range c.DDNS {
			if t.ProviderID == id {
				used = t.Name
				return errProviderInUse
			}
		}
		for _, cert := range c.Certs {
			if cert.ProviderID == id {
				used = cert.Name
				return errProviderInUse
			}
		}
		for i := range c.Providers {
			if c.Providers[i].ID == id {
				c.Providers = append(c.Providers[:i], c.Providers[i+1:]...)
				return nil
			}
		}
		return errProviderNotFound
	})
	if errors.Is(err, errProviderInUse) {
		api.Fail(w, 400, "该凭据正被任务或证书「"+used+"」使用，无法删除")
		return
	}
	if errors.Is(err, errProviderNotFound) {
		api.Fail(w, 404, "服务商凭据不存在")
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	log.Printf("[security] 删除 DNS 服务商凭据，ID: %s", id)
	api.OK(w, nil)
}

// listInterfaces 返回系统网卡列表（供前端下拉选择）。
func (h *handler) listInterfaces(w http.ResponseWriter, r *http.Request) {
	ifaces, err := net.Interfaces()
	if err != nil {
		api.Fail(w, 500, err.Error())
		return
	}
	names := make([]string, 0, len(ifaces))
	for _, i := range ifaces {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		names = append(names, i.Name)
	}
	// 附带当前自动识别的 WAN 口结果
	wan4, _ := resolveWANInterface(false)
	api.OK(w, map[string]interface{}{"interfaces": names, "wan": wan4})
}

// previewIP 预览 IP 来源取到的地址，返回实际网卡名与 IP。
func (h *handler) previewIP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IPType    string                  `json:"ipType"`
		IPSource  string                  `json:"ipSource"`
		Interface string                  `json:"interface"`
		APIURL    string                  `json:"apiUrl"`
		Fallbacks []config.IPSourceConfig `json:"fallbacks"`
	}
	if api.DecodeBody(r, &body) != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	if body.IPType == "" {
		body.IPType = "ipv4"
	}
	if body.IPType != "ipv4" && body.IPType != "ipv6" {
		api.Fail(w, 400, "IP类型无效")
		return
	}
	source := config.IPSourceConfig{Source: body.IPSource, Interface: body.Interface, APIURL: body.APIURL, Fallbacks: body.Fallbacks}
	if err := config.ValidateIPSource(source, body.IPType == "ipv6"); err != nil {
		api.Fail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ip, iface, err := resolveSource(ctx, source, body.IPType == "ipv6")
	if err != nil {
		api.Fail(w, 500, err.Error())
		return
	}
	api.OK(w, map[string]string{"ip": ip, "interface": iface})
}

// testProvider 只读查询一条记录，验证凭据可用，不做任何修改。
func (h *handler) testProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID         string `json:"id"`
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
	if body.RecordType != "A" && body.RecordType != "AAAA" {
		api.Fail(w, 400, "记录类型必须是 A 或 AAAA")
		return
	}
	providerConf := config.DNSProviderConf{
		Type:     body.Type,
		Key:      body.Key,
		Secret:   body.Secret,
		Endpoint: body.Endpoint,
	}
	if body.ID != "" {
		found := false
		h.cfg.RLock()
		for _, saved := range h.cfg.Providers {
			if saved.ID != body.ID {
				continue
			}
			found = true
			if providerConf.Type != "" && !strings.EqualFold(providerConf.Type, saved.Type) {
				h.cfg.RUnlock()
				api.Fail(w, 400, "服务商类型与已保存凭据不一致")
				return
			}
			providerConf.Type = saved.Type
			// 端点变更后禁止回填已保存的凭据，防止凭据被发往未授权的端点。
			if endpointChanged(providerConf.Endpoint, saved.Endpoint) {
				if providerConf.Key == "" {
					h.cfg.RUnlock()
					api.Fail(w, 400, "更换端点后需重新输入凭据，不能使用已保存的凭据")
					return
				}
			} else {
				if providerConf.Key == "" {
					providerConf.Key = saved.Key
				}
				if providerConf.Secret == "" {
					providerConf.Secret = saved.Secret
				}
			}
			if providerConf.Endpoint == "" {
				providerConf.Endpoint = saved.Endpoint
			}
			break
		}
		h.cfg.RUnlock()
		if !found {
			api.Fail(w, 404, "服务商凭据不存在")
			return
		}
	}
	if code, msg := h.validateProvider(&providerConf); code != 0 {
		api.Fail(w, code, msg)
		return
	}
	provider, err := NewProvider(providerConf)
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
