package ddns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"andey-proxy/internal/config"
)

// hwCheckAuth 校验 SDK-HMAC-SHA256 签名头的基本结构。
func hwCheckAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Header.Get("X-Sdk-Date") == "" {
		t.Error("缺少 X-Sdk-Date")
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "SDK-HMAC-SHA256 Access=hw-ak,") || !strings.Contains(auth, "Signature=") {
		t.Errorf("Authorization 格式错误: %s", auth)
	}
}

func TestHuaweiUpsert(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hwCheckAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/zones":
			if r.URL.Query().Get("name") != "example.com." {
				t.Errorf("zone 查询参数错误: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"zones":[{"id":"zone-1","name":"example.com."}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2.1/zones/zone-1/recordsets":
			if r.URL.Query().Get("name") != "home.example.com." || r.URL.Query().Get("type") != "A" {
				t.Errorf("recordset 查询参数错误: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"recordsets":[],"metadata":{"total_count":0}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2.1/zones/zone-1/recordsets":
			body, _ := io.ReadAll(r.Body)
			var params map[string]interface{}
			if err := json.Unmarshal(body, &params); err != nil {
				t.Errorf("请求体不是 JSON: %v", err)
			}
			if params["name"] != "home.example.com." || params["type"] != "A" || params["ttl"] != float64(600) {
				t.Errorf("create 参数错误: %v", params)
			}
			recs, _ := params["records"].([]interface{})
			if len(recs) != 1 || recs[0] != "1.2.3.4" {
				t.Errorf("records 参数错误: %v", params["records"])
			}
			w.Write([]byte(`{"id":"rs-1"}`))
		default:
			t.Errorf("未知请求: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	p, err := newHuaweiProvider(config.DNSProviderConf{
		Type: "huaweicloud", Key: "hw-ak", Secret: "hw-sk", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "1.2.3.4", 600)
	if err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	if !strings.Contains(msg, "新增记录") {
		t.Fatalf("返回说明不符合预期: %s", msg)
	}
}

func TestHuaweiModify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hwCheckAuth(t, r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/zones":
			w.Write([]byte(`{"zones":[{"id":"zone-1","name":"example.com."}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2.1/zones/zone-1/recordsets":
			w.Write([]byte(`{"recordsets":[{"id":"rs-1","name":"home.example.com.","type":"A","records":["1.1.1.1"]}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v2.1/zones/zone-1/recordsets/rs-1":
			w.Write([]byte(`{"id":"rs-1"}`))
		default:
			t.Errorf("未知请求: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	p, err := newHuaweiProvider(config.DNSProviderConf{
		Type: "huaweicloud", Key: "hw-ak", Secret: "hw-sk", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "2.2.2.2", 0)
	if err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	if !strings.Contains(msg, "更新记录") {
		t.Fatalf("返回说明不符合预期: %s", msg)
	}
}

func TestHuaweiZoneNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"zones":[]}`))
	}))
	defer srv.Close()
	p, err := newHuaweiProvider(config.DNSProviderConf{
		Type: "huaweicloud", Key: "hw-ak", Secret: "hw-sk", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.QueryRecord(context.Background(), "home.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "托管区域") {
		t.Fatalf("应报区域不存在: %v", err)
	}
}

func TestHuaweiAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"code":"DNS.0301","message":"token invalid, ak=hw-ak"}`))
	}))
	defer srv.Close()
	p, err := newHuaweiProvider(config.DNSProviderConf{
		Type: "huaweicloud", Key: "hw-ak", Secret: "hw-sk", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.QueryRecord(context.Background(), "home.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "DNS.0301") {
		t.Fatalf("错误处理不符合预期: %v", err)
	}
	if strings.Contains(err.Error(), "hw-ak") {
		t.Fatalf("错误信息泄露凭据: %v", err)
	}
}
