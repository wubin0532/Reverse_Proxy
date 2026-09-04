package ddns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"luckyx/internal/config"
)

func TestDnspodUpsert(t *testing.T) {
	var mu sync.Mutex
	var actions []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("表单解析失败: %v", err)
		}
		if r.Form.Get("login_token") != "1001,tokensecret" {
			t.Errorf("login_token 错误: %s", r.Form.Get("login_token"))
		}
		if r.Form.Get("format") != "json" {
			t.Errorf("format 参数错误: %s", r.Form.Get("format"))
		}
		action := strings.TrimPrefix(r.URL.Path, "/")
		mu.Lock()
		actions = append(actions, action)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "Record.List":
			if r.Form.Get("domain") != "example.com" || r.Form.Get("sub_domain") != "home" {
				t.Errorf("Record.List 参数错误: %v", r.Form)
			}
			w.Write([]byte(`{"status":{"code":"1","message":"ok"},"records":[]}`))
		case "Record.Create":
			if r.Form.Get("value") != "1.2.3.4" || r.Form.Get("record_line") == "" {
				t.Errorf("Record.Create 参数错误: %v", r.Form)
			}
			w.Write([]byte(`{"status":{"code":"1","message":"ok"},"record":{"id":"55"}}`))
		default:
			t.Errorf("未知 Action: %s", action)
		}
	}))
	defer srv.Close()

	p := newDnspodProvider(config.DNSProviderConf{
		Type: "dnspod", Key: "1001", Secret: "tokensecret", Endpoint: srv.URL,
	})
	if _, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "1.2.3.4", 600); err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"Record.List", "Record.Create"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("调用序列错误: %v", actions)
	}
}

func TestDnspodModify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/") {
		case "Record.List":
			w.Write([]byte(`{"status":{"code":"1","message":"ok"},"records":[{"id":"55","name":"home","type":"A","value":"1.1.1.1"}]}`))
		case "Record.Modify":
			if r.Form.Get("record_id") != "55" {
				t.Errorf("record_id 错误: %s", r.Form.Get("record_id"))
			}
			w.Write([]byte(`{"status":{"code":"1","message":"ok"}}`))
		default:
			t.Errorf("未知请求: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := newDnspodProvider(config.DNSProviderConf{
		Type: "dnspod", Key: "1001", Secret: "tokensecret", Endpoint: srv.URL,
	})
	msg, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "2.2.2.2", 0)
	if err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	if !strings.Contains(msg, "更新记录") {
		t.Fatalf("返回说明不符合预期: %s", msg)
	}
}

func TestDnspodError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":{"code":"6","message":"记录负载无效"}}`))
	}))
	defer srv.Close()
	p := newDnspodProvider(config.DNSProviderConf{
		Type: "dnspod", Key: "1", Secret: "x", Endpoint: srv.URL,
	})
	_, err := p.QueryRecord(context.Background(), "home.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "记录负载无效") {
		t.Fatalf("错误处理不符合预期: %v", err)
	}
}
