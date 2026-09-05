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
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return 400, "任务名称不能为空"
	}
	if len(t.Domains) == 0 || len(t.Domains) > 100 {
		return 400, "域名数量必须为 1 到 100 个"
	}
	for i, domain := range t.Domains {
		domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
		if strings.Contains(domain, "*") {
			return 400, "DDNS 域名不能使用通配符: " + domain
		}
		if _, _, err := splitDomain(domain); err != nil {
			return 400, err.Error()
		}
		t.Domains[i] = domain
	}
	if t.IPType != "ipv4" && t.IPType != "ipv6" {
		return 400, "IP 类型必须是 ipv4 或 ipv6"
	}
	if t.Interval <= 0 {
		t.Interval = 300
	}
	if t.Interval < 30 || t.Interval > 86400 {
		return 400, "检测间隔必须为 30 到 86400 秒"
	}
	if t.TTL < 0 || t.TTL > 86400 {
		return 400, "TTL 必须为 0 到 86400 秒"
	}
	switch t.IPSource {
	case "interface":
		// 空或 auto 表示自动识别 WAN 口，均合法
	case "api":
		u, err := validateHTTPURL(t.APIURL, true)
		if err != nil {
			return 400, "IP 查询地址无效: " + err.Error()
		}
		t.APIURL = u.String()
	default:
		return 400, "IP 来源必须是 interface 或 api"
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
	t.ID = ids.New()
	if err := h.cfg.Update(func(c *config.Config) error {
		c.DDNS = append(c.DDNS, t)
		return nil
	}); err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	log.Printf("[security] 新增 DDNS 任务，ID: %s", t.ID)
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
	err := h.cfg.Update(func(c *config.Config) error {
		for i := range c.DDNS {
			if c.DDNS[i].ID == id {
				c.DDNS[i] = t
				return nil
			}
		}
		return errTaskNotFound
	})
	if errors.Is(err, errTaskNotFound) {
		api.Fail(w, 404, "任务不存在")
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	log.Printf("[security] 修改 DDNS 任务，ID: %s", id)
	api.OK(w, t)
}

func (h *handler) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := h.cfg.Update(func(c *config.Config) error {
		for i := range c.DDNS {
			if c.DDNS[i].ID == id {
				c.DDNS = append(c.DDNS[:i], c.DDNS[i+1:]...)
				return nil
			}
		}
		return errTaskNotFound
	})
	if errors.Is(err, errTaskNotFound) {
		api.Fail(w, 404, "任务不存在")
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
		return
	}
	h.w.Reload()
	log.Printf("[security] 删除 DDNS 任务，ID: %s", id)
	api.OK(w, nil)
}

func (h *handler) toggleTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var enabled bool
	err := h.cfg.Update(func(c *config.Config) error {
		for i := range c.DDNS {
			if c.DDNS[i].ID == id {
				c.DDNS[i].Enabled = !c.DDNS[i].Enabled
				enabled = c.DDNS[i].Enabled
				return nil
			}
		}
		return errTaskNotFound
	})
	if errors.Is(err, errTaskNotFound) {
		api.Fail(w, 404, "任务不存在")
		return
	}
	if err != nil {
		api.Fail(w, 500, "保存配置失败")
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
		if p.Key == "" {
			p.Key = c.Providers[idx].Key
		}
		if p.Secret == "" && !body.ClearSecret {
			p.Secret = c.Providers[idx].Secret
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
		IPType    string `json:"ipType"`
		IPSource  string `json:"ipSource"`
		Interface string `json:"interface"`
		APIURL    string `json:"apiUrl"`
	}
	if err := api.DecodeBody(r, &body); err != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	if body.IPType == "" {
		body.IPType = "ipv4"
	}
	if body.IPType != "ipv4" && body.IPType != "ipv6" {
		api.Fail(w, 400, "IP 类型必须是 ipv4 或 ipv6")
		return
	}
	if body.IPSource == "api" {
		u, err := validateHTTPURL(body.APIURL, true)
		if err != nil {
			api.Fail(w, 400, "IP 查询地址无效: "+err.Error())
			return
		}
		body.APIURL = u.String()
	} else if body.IPSource != "interface" {
		api.Fail(w, 400, "IP 来源必须是 interface 或 api")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	ip, iface, err := GetIPDetail(ctx, config.DDNSTask{
		IPType:    body.IPType,
		IPSource:  body.IPSource,
		Interface: body.Interface,
		APIURL:    body.APIURL,
	})
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
			if providerConf.Key == "" {
				providerConf.Key = saved.Key
			}
			if providerConf.Secret == "" {
				providerConf.Secret = saved.Secret
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
