package ddns

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"andey-proxy/internal/config"
)

const dnspodDefaultEndpoint = "https://dnsapi.cn"

// dnspodProvider DNSPod 国内版，login_token=ID,Token 认证。
type dnspodProvider struct {
	tokenID  string
	token    string
	endpoint string
	client   *http.Client
}

func newDnspodProvider(conf config.DNSProviderConf) *dnspodProvider {
	ep := conf.Endpoint
	if ep == "" {
		ep = dnspodDefaultEndpoint
	}
	return &dnspodProvider{
		tokenID:  conf.Key,
		token:    conf.Secret,
		endpoint: strings.TrimSuffix(ep, "/"),
		client:   providerHTTPClient(),
	}
}

type dnspodRecord struct {
	ID    json.Number `json:"id"`
	Name  string      `json:"name"`
	Type  string      `json:"type"`
	Value string      `json:"value"`
}

// do 发起一次 DNSPod 调用（form 编码 POST），action 如 Record.List。
func (p *dnspodProvider) do(ctx context.Context, action string, form url.Values, out interface{}) error {
	if form == nil {
		form = url.Values{}
	}
	form.Set("login_token", p.tokenID+","+p.token)
	form.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.endpoint+"/"+action, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := p.client.Do(req)
	if err != nil {
		return safeRequestError("DNSPod", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var head struct {
		Status struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"status"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("DNSPod 响应解析失败")
	}
	if head.Status.Code != "1" {
		return fmt.Errorf("DNSPod 错误 %s: %s", head.Status.Code, redactProviderMessage(head.Status.Message, p.tokenID, p.token))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// findRecord 查询指定子域/类型的记录。
func (p *dnspodProvider) findRecord(ctx context.Context, root, rr, recordType string) (*dnspodRecord, error) {
	var out struct {
		Records []dnspodRecord `json:"records"`
	}
	err := p.do(ctx, "Record.List", url.Values{
		"domain":      {root},
		"sub_domain":  {rr},
		"record_type": {recordType},
	}, &out)
	if err != nil {
		return nil, err
	}
	for i := range out.Records {
		r := &out.Records[i]
		if strings.EqualFold(r.Name, rr) && strings.EqualFold(r.Type, recordType) {
			return r, nil
		}
	}
	return nil, nil
}

func (p *dnspodProvider) QueryRecord(ctx context.Context, domain, recordType string) (string, error) {
	rr, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	rec, err := p.findRecord(ctx, root, rr, recordType)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", nil
	}
	return rec.Value, nil
}

func (p *dnspodProvider) UpsertRecord(ctx context.Context, domain, recordType, ip string, ttl int) (string, error) {
	rr, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	rec, err := p.findRecord(ctx, root, rr, recordType)
	if err != nil {
		return "", err
	}
	if rec == nil {
		form := url.Values{
			"domain":      {root},
			"sub_domain":  {rr},
			"record_type": {recordType},
			"record_line": {"默认"},
			"value":       {ip},
		}
		if ttl > 0 {
			form.Set("ttl", fmt.Sprintf("%d", ttl))
		}
		if err := p.do(ctx, "Record.Create", form, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("新增记录 %s %s -> %s", domain, recordType, ip), nil
	}
	if rec.Value == ip {
		return fmt.Sprintf("记录 %s 已是最新（%s）", domain, ip), nil
	}
	form := url.Values{
		"domain":      {root},
		"record_id":   {rec.ID.String()},
		"sub_domain":  {rr},
		"record_type": {recordType},
		"record_line": {"默认"},
		"value":       {ip},
	}
	if ttl > 0 {
		form.Set("ttl", fmt.Sprintf("%d", ttl))
	}
	if err := p.do(ctx, "Record.Modify", form, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("更新记录 %s %s: %s -> %s", domain, recordType, rec.Value, ip), nil
}
