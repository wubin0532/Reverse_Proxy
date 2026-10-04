package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/logcenter"
)

type cloudClient struct {
	account  config.TunnelAccount
	endpoint string
	http     *http.Client
}
type cloudTunnel struct {
	ID           string `json:"id"`
	Token        string `json:"token"`
	ConfigSource string `json:"config_src"`
}
type zone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type dnsRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	Comment string `json:"comment,omitempty"`
}
type cloudConfiguration struct {
	Config map[string]json.RawMessage `json:"config"`
}
type connectorConnection struct {
	Pending bool `json:"is_pending_reconnect"`
}

type cloudConnection struct {
	ID          string                `json:"id"`
	Connections []connectorConnection `json:"connections"`
	Conns       []connectorConnection `json:"conns"`
}

func newCloudClient(account config.TunnelAccount) *cloudClient {
	return &cloudClient{account: account, endpoint: "https://api.cloudflare.com/client/v4", http: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *cloudClient) base() string { return "/accounts/" + c.account.AccountID + "/cfd_tunnel" }

func (c *cloudClient) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, reader)
	if err != nil {
		return fmt.Errorf("Cloudflare 请求地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+c.account.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare 请求失败或超时，请检查网络并重新查询状态")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(data) > 2<<20 {
		return fmt.Errorf("Cloudflare 响应过大或读取失败")
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return fmt.Errorf("Cloudflare 响应格式无效 (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !envelope.Success {
		message := "Cloudflare 操作失败"
		if len(envelope.Errors) > 0 {
			message = logcenter.Redact(strings.ReplaceAll(envelope.Errors[0].Message, c.account.Token, "[REDACTED]"))
			if len(message) > 512 {
				message = message[:512]
			}
		}
		return fmt.Errorf("%s (HTTP %d)", message, resp.StatusCode)
	}
	if out != nil && len(envelope.Result) > 0 && string(envelope.Result) != "null" {
		if json.Unmarshal(envelope.Result, out) != nil {
			return fmt.Errorf("Cloudflare 结果格式无效")
		}
	}
	return nil
}

func (c *cloudClient) zones(ctx context.Context) ([]zone, error) {
	all := []zone{}
	for page := 1; page <= 100; page++ {
		var result []zone
		path := fmt.Sprintf("/zones?account.id=%s&per_page=50&page=%d", c.account.AccountID, page)
		if err := c.do(ctx, "GET", path, nil, &result); err != nil {
			return nil, err
		}
		all = append(all, result...)
		if len(result) < 50 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("可见域名区域过多，请缩小 API Token 的权限范围")
}

func (c *cloudClient) configuration(ctx context.Context, id string) (cloudConfiguration, error) {
	var result cloudConfiguration
	err := c.do(ctx, "GET", c.base()+"/"+id+"/configurations", nil, &result)
	if result.Config == nil {
		result.Config = make(map[string]json.RawMessage)
	}
	return result, err
}

func (c *cloudClient) records(ctx context.Context, zid, hostname string) ([]dnsRecord, error) {
	var result []dnsRecord
	err := c.do(ctx, "GET", "/zones/"+zid+"/dns_records?per_page=100&name="+url.QueryEscape(hostname), nil, &result)
	return result, err
}

func (c *cloudClient) token(ctx context.Context, id string) (string, error) {
	var token string
	err := c.do(ctx, "GET", c.base()+"/"+id+"/token", nil, &token)
	if err == nil && token == "" {
		err = fmt.Errorf("Cloudflare 未返回隧道 Token")
	}
	return token, err
}

func (m *Manager) cloud(inst config.TunnelInstance) (*cloudClient, error) {
	if err := m.validateCredentials(&inst); err != nil {
		return nil, err
	}
	if inst.TunnelID != "" && !uuidPattern.MatchString(inst.TunnelID) {
		return nil, fmt.Errorf("Tunnel ID 格式无效")
	}
	a, err := m.account(inst.AccountRef)
	if err != nil {
		return nil, err
	}
	if !cloudIDPattern.MatchString(a.AccountID) || a.Token == "" || strings.ContainsAny(a.Token, "\r\n\x00") {
		return nil, fmt.Errorf("账户 API 凭据无效")
	}
	return m.clientFactory(a), nil
}

func (m *Manager) checkConnectors(ctx context.Context, c *cloudClient, inst config.TunnelInstance) error {
	var remote cloudTunnel
	if err := c.do(ctx, "GET", c.base()+"/"+inst.TunnelID, nil, &remote); err != nil {
		return err
	}
	if remote.ConfigSource != "cloudflare" {
		return fmt.Errorf("仅支持远程管理的 Cloudflare 隧道")
	}
	var connections []cloudConnection
	if err := c.do(ctx, "GET", c.base()+"/"+inst.TunnelID+"/connections", nil, &connections); err != nil {
		return err
	}
	own := ""
	m.mu.Lock()
	w := m.workers[inst.ID]
	m.mu.Unlock()
	if w != nil {
		w.mu.Lock()
		own = w.connectorID
		w.mu.Unlock()
	}
	for _, connector := range connections {
		active := false
		for _, conn := range append(connector.Conns, connector.Connections...) {
			if !conn.Pending {
				active = true
			}
		}
		if active && connector.ID != own {
			return fmt.Errorf("已有隧道存在其他活跃连接器，首版不支持修改共享路由，请使用独立隧道")
		}
	}
	return nil
}
