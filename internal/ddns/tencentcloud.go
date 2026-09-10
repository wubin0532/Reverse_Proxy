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
	"strconv"
	"strings"
	"time"

	"andey-proxy/internal/config"
)

const tcDefaultEndpoint = "https://dnspod.tencentcloudapi.com"

// tencentProvider 腾讯云 DNSPod（dnspod.tencentcloudapi.com），TC3-HMAC-SHA256 签名。
type tencentProvider struct {
	secretID  string
	secretKey string
	endpoint  string
	host      string
	client    *http.Client
}

func newTencentProvider(conf config.DNSProviderConf) (*tencentProvider, error) {
	ep := conf.Endpoint
	if ep == "" {
		ep = tcDefaultEndpoint
	}
	ep = strings.TrimSuffix(ep, "/")
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("腾讯云端点无效: %s", ep)
	}
	return &tencentProvider{
		secretID:  conf.Key,
		secretKey: conf.Secret,
		endpoint:  ep,
		host:      u.Host,
		client:    providerHTTPClient(),
	}, nil
}

func tcHmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func tcSHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// do 发起一次腾讯云 API 调用（JSON POST + TC3 签名），返回时已检查业务错误。
func (p *tencentProvider) do(ctx context.Context, action string, params interface{}, out interface{}) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	timestamp := strconv.FormatInt(now.Unix(), 10)
	date := now.Format("2006-01-02")

	contentType := "application/json; charset=utf-8"
	canonicalHeaders := "content-type:" + contentType + "\nhost:" + p.host + "\n"
	signedHeaders := "content-type;host"
	canonicalRequest := "POST\n/\n\n" + canonicalHeaders + "\n" + signedHeaders + "\n" + tcSHA256Hex(payload)
	credentialScope := date + "/dnspod/tc3_request"
	stringToSign := "TC3-HMAC-SHA256\n" + timestamp + "\n" + credentialScope + "\n" + tcSHA256Hex([]byte(canonicalRequest))
	secretSigning := tcHmacSHA256(tcHmacSHA256(tcHmacSHA256([]byte("TC3"+p.secretKey), date), "dnspod"), "tc3_request")
	signature := hex.EncodeToString(tcHmacSHA256(secretSigning, stringToSign))
	authorization := "TC3-HMAC-SHA256 Credential=" + p.secretID + "/" + credentialScope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", p.host)
	req.Header.Set("Authorization", authorization)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", "2021-03-23")
	req.Header.Set("X-TC-Timestamp", timestamp)
	resp, err := p.client.Do(req)
	if err != nil {
		return safeRequestError("腾讯云", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var head struct {
		Response struct {
			Error *struct {
				Code    string `json:"Code"`
				Message string `json:"Message"`
			} `json:"Error"`
		} `json:"Response"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return fmt.Errorf("腾讯云响应解析失败")
	}
	if head.Response.Error != nil {
		return fmt.Errorf("腾讯云错误 %s: %s", head.Response.Error.Code,
			redactProviderMessage(head.Response.Error.Message, p.secretID, p.secretKey))
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

type tcRecord struct {
	RecordID int64  `json:"RecordId"`
	Name     string `json:"Name"`
	Type     string `json:"Type"`
	Value    string `json:"Value"`
}

// findRecord 查询指定子域/类型的记录，不存在返回 nil。
func (p *tencentProvider) findRecord(ctx context.Context, root, rr, recordType string) (*tcRecord, error) {
	var out struct {
		Response struct {
			RecordList []tcRecord `json:"RecordList"`
		} `json:"Response"`
	}
	err := p.do(ctx, "DescribeRecordList", map[string]interface{}{
		"Domain":     root,
		"Subdomain":  rr,
		"RecordType": recordType,
		"Limit":      100,
	}, &out)
	if err != nil {
		return nil, err
	}
	for i := range out.Response.RecordList {
		r := &out.Response.RecordList[i]
		if strings.EqualFold(r.Name, rr) && strings.EqualFold(r.Type, recordType) {
			return r, nil
		}
	}
	return nil, nil
}

func (p *tencentProvider) QueryRecord(ctx context.Context, domain, recordType string) (string, error) {
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

func (p *tencentProvider) UpsertRecord(ctx context.Context, domain, recordType, ip string, ttl int) (string, error) {
	rr, root, err := splitDomain(domain)
	if err != nil {
		return "", err
	}
	rec, err := p.findRecord(ctx, root, rr, recordType)
	if err != nil {
		return "", err
	}
	if rec == nil {
		params := map[string]interface{}{
			"Domain":     root,
			"SubDomain":  rr,
			"RecordType": recordType,
			"RecordLine": "默认",
			"Value":      ip,
		}
		if ttl > 0 {
			params["TTL"] = ttl
		}
		if err := p.do(ctx, "CreateRecord", params, nil); err != nil {
			return "", err
		}
		return fmt.Sprintf("新增记录 %s %s -> %s", domain, recordType, ip), nil
	}
	if rec.Value == ip {
		return fmt.Sprintf("记录 %s 已是最新（%s）", domain, ip), nil
	}
	params := map[string]interface{}{
		"Domain":     root,
		"RecordId":   rec.RecordID,
		"SubDomain":  rr,
		"RecordType": recordType,
		"RecordLine": "默认",
		"Value":      ip,
	}
	if ttl > 0 {
		params["TTL"] = ttl
	}
	if err := p.do(ctx, "ModifyRecord", params, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("更新记录 %s %s: %s -> %s", domain, recordType, rec.Value, ip), nil
}
