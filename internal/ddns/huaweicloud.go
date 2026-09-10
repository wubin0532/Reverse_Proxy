package ddns

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"andey-proxy/internal/config"
)

const hwDefaultEndpoint = "https://dns.myhuaweicloud.com"

// huaweiProvider 华为云解析（DNS 全局服务），SDK-HMAC-SHA256 签名。
type huaweiProvider struct {
	ak       string
	sk       string
	endpoint string
	host     string
	client   *http.Client
}

func newHuaweiProvider(conf config.DNSProviderConf) (*huaweiProvider, error) {
	ep := conf.Endpoint
	if ep == "" {
		ep = hwDefaultEndpoint
	}
	ep = strings.TrimSuffix(ep, "/")
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("华为云端点无效: %s", ep)
	}
	return &huaweiProvider{
		ak:       conf.Key,
		sk:       conf.Secret,
		endpoint: ep,
		host:     u.Host,
		client:   providerHTTPClient(),
	}, nil
}

// do 发起一次华为云 DNS 调用（JSON + SDK-HMAC-SHA256 签名），返回时已检查错误。
func (p *huaweiProvider) do(ctx context.Context, method, path string, query url.Values, payload []byte, out interface{}) error {
	sdkDate := time.Now().UTC().Format("20060102T150405Z")

	// 规范查询串：键值分别编码后按字典序拼接
	var pairs []string
	for k, vs := range query {
		for _, v := range vs {
			pairs = append(pairs, hwEscape(k)+"="+hwEscape(v))
		}
	}
	sort.Strings(pairs)
	canonicalQuery := strings.Join(pairs, "&")

	signedHeaders := "host;x-sdk-date"
	canonicalHeaders := "host:" + p.host + "\nx-sdk-date:" + sdkDate + "\n"
	if payload != nil {
		signedHeaders = "content-type;host;x-sdk-date"
		canonicalHeaders = "content-type:application/json\n" + canonicalHeaders
	}
	canonicalRequest := method + "\n" + path + "\n" + canonicalQuery + "\n" +
		canonicalHeaders + "\n" + signedHeaders + "\n" + hwSHA256Hex(payload)
	stringToSign := "SDK-HMAC-SHA256\n" + sdkDate + "\n" + hwSHA256Hex([]byte(canonicalRequest))
	mac := hmac.New(sha256.New, []byte(p.sk))
	mac.Write([]byte(stringToSign))
	signature := hex.EncodeToString(mac.Sum(nil))
	authorization := "SDK-HMAC-SHA256 Access=" + p.ak +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature

	reqURL := p.endpoint + path
	if canonicalQuery != "" {
		reqURL += "?" + canonicalQuery
	}
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Host", p.host)
	req.Header.Set("X-Sdk-Date", sdkDate)
	req.Header.Set("Authorization", authorization)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return safeRequestError("华为云", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var head struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &head)
		if head.Message == "" {
			head.Message = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("华为云错误 %s: %s", head.Code, redactProviderMessage(head.Message, p.ak, p.sk))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// hwEscape 华为云签名规范编码：QueryEscape 基础上 %20、*、~ 规则同 RFC3986。
func hwEscape(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}

func hwSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// findZoneID 查询主域对应的托管区域 ID。
func (p *huaweiProvider) findZoneID(ctx context.Context, root string) (string, error) {
	var out struct {
		Zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"zones"`
	}
	err := p.do(ctx, http.MethodGet, "/v2/zones", url.Values{
		"name":      {root + "."},
		"zone_type": {"public"},
	}, nil, &out)
	if err != nil {
		return "", err
	}
	for _, z := range out.Zones {
		if strings.EqualFold(strings.TrimSuffix(z.Name, "."), root) {
			return z.ID, nil
		}
	}
	return "", fmt.Errorf("华为云找不到域名 %s 的托管区域", root)
}

type hwRecordset struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Records []string `json:"records"`
}

// findRecordset 查询指定 FQDN/类型的记录集，不存在返回 nil。
func (p *huaweiProvider) findRecordset(ctx context.Context, zoneID, fqdn, recordType string) (*hwRecordset, error) {
	var out struct {
		Recordsets []hwRecordset `json:"recordsets"`
	}
	err := p.do(ctx, http.MethodGet, "/v2.1/zones/"+zoneID+"/recordsets", url.Values{
		"name": {fqdn + "."},
		"type": {recordType},
	}, nil, &out)
	if err != nil {
		return nil, err
	}
	for i := range out.Recordsets {
		r := &out.Recordsets[i]
		if strings.EqualFold(strings.TrimSuffix(r.Name, "."), fqdn) && strings.EqualFold(r.Type, recordType) {
			return r, nil
		}
	}
	return nil, nil
}

func (p *huaweiProvider) QueryRecord(ctx context.Context, domain, recordType string) (string, error) {
	_, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	zoneID, err := p.findZoneID(ctx, root)
	if err != nil {
		return "", err
	}
	rs, err := p.findRecordset(ctx, zoneID, domain, recordType)
	if err != nil {
		return "", err
	}
	if rs == nil || len(rs.Records) == 0 {
		return "", nil
	}
	return rs.Records[0], nil
}

func (p *huaweiProvider) UpsertRecord(ctx context.Context, domain, recordType, ip string, ttl int) (string, error) {
	_, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	zoneID, err := p.findZoneID(ctx, root)
	if err != nil {
		return "", err
	}
	rs, err := p.findRecordset(ctx, zoneID, domain, recordType)
	if err != nil {
		return "", err
	}
	if ttl <= 0 {
		ttl = 300
	}
	body, err := json.Marshal(map[string]interface{}{
		"name":    domain + ".",
		"type":    recordType,
		"ttl":     ttl,
		"records": []string{ip},
	})
	if err != nil {
		return "", err
	}
	if rs == nil {
		if err := p.do(ctx, http.MethodPost, "/v2.1/zones/"+zoneID+"/recordsets", nil, body, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("新增记录 %s %s -> %s", domain, recordType, ip), nil
	}
	if len(rs.Records) == 1 && rs.Records[0] == ip {
		return fmt.Sprintf("记录 %s 已是最新（%s）", domain, ip), nil
	}
	if err := p.do(ctx, http.MethodPut, "/v2.1/zones/"+zoneID+"/recordsets/"+rs.ID, nil, body, nil); err != nil {
		return "", err
	}
	old := strings.Join(rs.Records, ",")
	return fmt.Sprintf("更新记录 %s %s: %s -> %s", domain, recordType, old, ip), nil
}
