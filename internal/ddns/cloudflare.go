package ddns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"andey-proxy/internal/config"
)

const cloudflareDefaultEndpoint = "https://api.cloudflare.com/client/v4"

// cloudflareProvider Cloudflare API v4，Bearer Token 认证。
type cloudflareProvider struct {
	token    string
	endpoint string
	client   *http.Client
}

func newCloudflareProvider(conf config.DNSProviderConf) *cloudflareProvider {
	token := conf.Secret
	if token == "" {
		token = conf.Key
	}
	ep := conf.Endpoint
	if ep == "" {
		ep = cloudflareDefaultEndpoint
	}
	return &cloudflareProvider{
		token:    token,
		endpoint: strings.TrimSuffix(ep, "/"),
		client:   providerHTTPClient(),
	}
}

type cfRecord struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

// do 发起一次 Cloudflare API 调用。
func (p *cloudflareProvider) do(ctx context.Context, method, path string, query url.Values, body interface{}, out interface{}) error {
	u := p.endpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return safeRequestError("Cloudflare", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var head struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return errors.New("cloudflare 响应解析失败")
	}
	if !head.Success {
		msg := "未知错误"
		if len(head.Errors) > 0 {
			msg = head.Errors[0].Message
		}
		return fmt.Errorf("cloudflare 错误: %s", redactProviderMessage(msg, p.token))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// zoneID 按主域名查 zone_id。
func (p *cloudflareProvider) zoneID(ctx context.Context, root string) (string, error) {
	var out struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	q := url.Values{"name": {root}}
	if err := p.do(ctx, http.MethodGet, "/zones", q, nil, &out); err != nil {
		return "", err
	}
	if len(out.Result) == 0 {
		return "", fmt.Errorf("cloudflare 未找到域 %s 的 zone", root)
	}
	return out.Result[0].ID, nil
}

// findRecord 查询指定域名/类型的记录。
func (p *cloudflareProvider) findRecord(ctx context.Context, zoneID, fqdn, recordType string) (*cfRecord, error) {
	var out struct {
		Result []cfRecord `json:"result"`
	}
	q := url.Values{"type": {recordType}, "name": {fqdn}, "per_page": {"100"}}
	if err := p.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records", q, nil, &out); err != nil {
		return nil, err
	}
	if (recordType == "TXT" || recordType == "CNAME") && len(out.Result) >= 100 {
		return nil, fmt.Errorf("远程查询结果超过安全核查范围，请先在服务商控制台整理记录")
	}
	var match *cfRecord
	for i := range out.Result {
		r := &out.Result[i]
		if strings.EqualFold(r.Name, fqdn) && strings.EqualFold(r.Type, recordType) {
			if match != nil && (recordType == "TXT" || recordType == "CNAME") {
				return nil, fmt.Errorf("同名同类型存在多条远程记录，请先在服务商控制台明确要管理的记录")
			}
			if match == nil {
				match = r
			}
		}
	}
	return match, nil
}

func (p *cloudflareProvider) QueryRecord(ctx context.Context, domain, recordType string) (string, error) {
	_, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	zid, err := p.zoneID(ctx, root)
	if err != nil {
		return "", err
	}
	rec, err := p.findRecord(ctx, zid, strings.ToLower(domain), recordType)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", nil
	}
	return rec.Content, nil
}

func (p *cloudflareProvider) UpsertRecord(ctx context.Context, domain, recordType, ip string, ttl int) (string, error) {
	_, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	zid, err := p.zoneID(ctx, root)
	if err != nil {
		return "", err
	}
	fqdn := strings.ToLower(strings.TrimSuffix(domain, "."))
	rec, err := p.findRecord(ctx, zid, fqdn, recordType)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 1 // Cloudflare 1 表示自动
	}
	body := map[string]interface{}{
		"type":    recordType,
		"name":    fqdn,
		"content": ip,
		"ttl":     ttl,
	}
	if rec == nil {
		if err := p.do(ctx, http.MethodPost, "/zones/"+zid+"/dns_records", nil, body, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("新增记录 %s %s -> %s", domain, recordType, ip), nil
	}
	if recordValuesEqual(recordType, rec.Content, ip) && rec.TTL == ttl {
		return fmt.Sprintf("记录 %s 已是最新（%s）", domain, ip), nil
	}
	// Preserve the remote proxy mode when reconciling only address/TTL.
	if recordType != "TXT" {
		body["proxied"] = rec.Proxied
	}
	if err := p.do(ctx, http.MethodPatch, "/zones/"+zid+"/dns_records/"+rec.ID, nil, body, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("更新记录 %s %s: %s -> %s", domain, recordType, rec.Content, ip), nil
}
