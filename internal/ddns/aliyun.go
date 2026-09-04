package ddns

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"luckyx/internal/config"
)

const aliyunDefaultEndpoint = "https://alidns.aliyuncs.com"

// aliyunProvider 阿里云解析（alidns），RPC 风格 HMAC-SHA1 签名。
type aliyunProvider struct {
	key      string
	secret   string
	endpoint string
	client   *http.Client
}

func newAliyunProvider(conf config.DNSProviderConf) *aliyunProvider {
	ep := conf.Endpoint
	if ep == "" {
		ep = aliyunDefaultEndpoint
	}
	return &aliyunProvider{
		key:      conf.Key,
		secret:   conf.Secret,
		endpoint: strings.TrimSuffix(ep, "/"),
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// aliyunPercentEncode 阿里云 RFC3986 风格编码。
func aliyunPercentEncode(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}

// signedQuery 构造带签名的查询串。
func (p *aliyunProvider) signedQuery(action string, params map[string]string) string {
	nonce := make([]byte, 8)
	rand.Read(nonce)
	q := map[string]string{
		"Format":           "JSON",
		"Version":          "2015-01-09",
		"AccessKeyId":      p.key,
		"SignatureMethod":  "HMAC-SHA1",
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"SignatureVersion": "1.0",
		"SignatureNonce":   hex.EncodeToString(nonce),
		"Action":           action,
	}
	for k, v := range params {
		q[k] = v
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	for _, k := range keys {
		buf.WriteByte('&')
		buf.WriteString(aliyunPercentEncode(k))
		buf.WriteByte('=')
		buf.WriteString(aliyunPercentEncode(q[k]))
	}
	canonical := buf.String()[1:]
	stringToSign := "GET&%2F&" + aliyunPercentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(p.secret+"&"))
	mac.Write([]byte(stringToSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return canonical + "&Signature=" + aliyunPercentEncode(sig)
}

type aliyunRecord struct {
	RecordID string `json:"RecordId"`
	RR       string `json:"RR"`
	Type     string `json:"Type"`
	Value    string `json:"Value"`
}

// do 发起一次 alidns 调用，返回时已完成错误检查。
func (p *aliyunProvider) do(ctx context.Context, action string, params map[string]string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		p.endpoint+"/?"+p.signedQuery(action, params), nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var head struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("阿里云响应解析失败: %s", truncate(string(body), 200))
	}
	if head.Code != "" {
		return fmt.Errorf("阿里云错误 %s: %s", head.Code, head.Message)
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// findRecord 查询指定 RR/类型的记录，不存在返回 nil。
func (p *aliyunProvider) findRecord(ctx context.Context, root, rr, recordType string) (*aliyunRecord, error) {
	var out struct {
		DomainRecords struct {
			Record []aliyunRecord `json:"Record"`
		} `json:"DomainRecords"`
	}
	err := p.do(ctx, "DescribeDomainRecords", map[string]string{
		"DomainName":   root,
		"RRKeyWord":    rr,
		"TypeKeyWord":  recordType,
		"PageSize":     "100",
		"SearchMode":   "ADVANCED",
	}, &out)
	if err != nil {
		return nil, err
	}
	for i := range out.DomainRecords.Record {
		r := &out.DomainRecords.Record[i]
		if strings.EqualFold(r.RR, rr) && strings.EqualFold(r.Type, recordType) {
			return r, nil
		}
	}
	return nil, nil
}

func (p *aliyunProvider) QueryRecord(ctx context.Context, domain, recordType string) (string, error) {
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

func (p *aliyunProvider) UpsertRecord(ctx context.Context, domain, recordType, ip string, ttl int) (string, error) {
	rr, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	rec, err := p.findRecord(ctx, root, rr, recordType)
	if err != nil {
		return "", err
	}
	ttlParam := map[string]string{}
	if ttl > 0 {
		ttlParam["TTL"] = fmt.Sprintf("%d", ttl)
	}
	if rec == nil {
		params := map[string]string{
			"DomainName": root,
			"RR":         rr,
			"Type":       recordType,
			"Value":      ip,
		}
		for k, v := range ttlParam {
			params[k] = v
		}
		if err := p.do(ctx, "AddDomainRecord", params, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("新增记录 %s %s -> %s", domain, recordType, ip), nil
	}
	if rec.Value == ip {
		return fmt.Sprintf("记录 %s 已是最新（%s）", domain, ip), nil
	}
	params := map[string]string{
		"RecordId": rec.RecordID,
		"RR":       rr,
		"Type":     recordType,
		"Value":    ip,
	}
	for k, v := range ttlParam {
		params[k] = v
	}
	if err := p.do(ctx, "UpdateDomainRecord", params, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("更新记录 %s %s: %s -> %s", domain, recordType, rec.Value, ip), nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
