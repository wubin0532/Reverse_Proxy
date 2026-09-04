package ddns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"luckyx/internal/config"
)

func TestCloudflareUpsert(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	var created map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer cf-token" {
			t.Errorf("Authorization 头错误: %s", r.Header.Get("Authorization"))
		}
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/zones":
			if r.URL.Query().Get("name") != "example.com" {
				t.Errorf("zone 查询参数错误: %v", r.URL.Query())
			}
			w.Write([]byte(`{"success":true,"result":[{"id":"z1"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/zones/z1/dns_records":
			// 第一次返回空（触发创建），之后返回已有记录（触发更新）
			w.Write([]byte(`{"success":true,"result":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/zones/z1/dns_records":
			json.NewDecoder(r.Body).Decode(&created)
			w.Write([]byte(`{"success":true,"result":{"id":"r1"}}`))
		default:
			t.Errorf("未知请求: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newCloudflareProvider(config.DNSProviderConf{
		Type: "cloudflare", Key: "cf-token", Endpoint: srv.URL,
	})
	if _, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "1.2.3.4", 0); err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	if created["type"] != "A" || created["name"] != "home.example.com" ||
		created["content"] != "1.2.3.4" || created["ttl"] != float64(1) {
		t.Fatalf("创建请求体错误: %v", created)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"GET /zones",
		"GET /zones/z1/dns_records",
		"POST /zones/z1/dns_records",
	}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("调用序列错误: %v", calls)
	}
}

func TestCloudflareUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/zones":
			w.Write([]byte(`{"success":true,"result":[{"id":"z1"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/zones/z1/dns_records":
			w.Write([]byte(`{"success":true,"result":[{"id":"r1","name":"home.example.com","type":"A","content":"1.1.1.1"}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/zones/z1/dns_records/r1":
			w.Write([]byte(`{"success":true,"result":{"id":"r1"}}`))
		default:
			t.Errorf("未知请求: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	// Secret 非空时优先作为 token
	p := newCloudflareProvider(config.DNSProviderConf{
		Type: "cloudflare", Key: "k", Secret: "s", Endpoint: srv.URL,
	})
	msg, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "2.2.2.2", 120)
	if err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	if !strings.Contains(msg, "更新记录") {
		t.Fatalf("返回说明不符合预期: %s", msg)
	}
}

func TestCloudflareError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}]}`))
	}))
	defer srv.Close()
	p := newCloudflareProvider(config.DNSProviderConf{
		Type: "cloudflare", Key: "bad", Endpoint: srv.URL,
	})
	_, err := p.QueryRecord(context.Background(), "home.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "Invalid access token") {
		t.Fatalf("错误处理不符合预期: %v", err)
	}
}
