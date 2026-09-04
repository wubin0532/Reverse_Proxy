package ddns

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"andey-proxy/internal/config"
)

// verifyAliyunSignature 用同样的算法在服务端重算签名并比对。
func verifyAliyunSignature(t *testing.T, q url.Values, secret string) {
	t.Helper()
	got := q.Get("Signature")
	if got == "" {
		t.Fatal("缺少 Signature 参数")
	}
	if q.Get("AccessKeyId") == "" || q.Get("SignatureMethod") != "HMAC-SHA1" {
		t.Fatal("缺少公共签名参数")
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		if k == "Signature" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	for _, k := range keys {
		buf.WriteByte('&')
		buf.WriteString(aliyunPercentEncode(k))
		buf.WriteByte('=')
		buf.WriteString(aliyunPercentEncode(q.Get(k)))
	}
	stringToSign := "GET&%2F&" + aliyunPercentEncode(buf.String()[1:])
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	want := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("签名校验失败: got %s want %s", got, want)
	}
}

func TestAliyunPercentEncode(t *testing.T) {
	if got := aliyunPercentEncode("a b*c~"); got != "a%20b%2Ac~" {
		t.Fatalf("percentEncode 结果错误: %s", got)
	}
}

// newAliyunMock 返回模拟 alidns 的服务器与收到的 Action 列表。
func newAliyunMock(t *testing.T, secret string, records string) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		verifyAliyunSignature(t, q, secret)
		action := q.Get("Action")
		mu.Lock()
		actions = append(actions, action)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "DescribeDomainRecords":
			if q.Get("DomainName") != "example.com" || q.Get("RRKeyWord") != "home" {
				t.Errorf("DescribeDomainRecords 参数错误: %v", q)
			}
			fmt.Fprintf(w, `{"TotalCount":1,"DomainRecords":{"Record":[%s]}}`, records)
		case "AddDomainRecord", "UpdateDomainRecord":
			w.Write([]byte(`{"RecordId":"999"}`))
		default:
			t.Errorf("未知 Action: %s", action)
		}
	}))
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), actions...)
	}
}

func TestAliyunUpsertUpdate(t *testing.T) {
	secret := "testsecret"
	srv, actions := newAliyunMock(t, secret, `{"RecordId":"123","RR":"home","Type":"A","Value":"1.1.1.1"}`)
	defer srv.Close()
	p := newAliyunProvider(config.DNSProviderConf{
		Type: "aliyun", Key: "testkey", Secret: secret, Endpoint: srv.URL,
	})
	msg, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "2.2.2.2", 600)
	if err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	if !strings.Contains(msg, "更新记录") {
		t.Fatalf("返回说明不符合预期: %s", msg)
	}
	got := actions()
	want := []string{"DescribeDomainRecords", "UpdateDomainRecord"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("调用序列错误: %v", got)
	}
}

func TestAliyunUpsertAdd(t *testing.T) {
	secret := "testsecret"
	srv, actions := newAliyunMock(t, secret, "")
	defer srv.Close()
	p := newAliyunProvider(config.DNSProviderConf{
		Type: "aliyun", Key: "testkey", Secret: secret, Endpoint: srv.URL,
	})
	if _, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "2.2.2.2", 0); err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	got := actions()
	want := []string{"DescribeDomainRecords", "AddDomainRecord"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("调用序列错误: %v", got)
	}
}

func TestAliyunError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Code":"InvalidAccessKeyId.NotFound","Message":"bad key"}`))
	}))
	defer srv.Close()
	p := newAliyunProvider(config.DNSProviderConf{
		Type: "aliyun", Key: "bad", Secret: "bad", Endpoint: srv.URL,
	})
	_, err := p.QueryRecord(context.Background(), "home.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "InvalidAccessKeyId.NotFound") {
		t.Fatalf("错误处理不符合预期: %v", err)
	}
}
