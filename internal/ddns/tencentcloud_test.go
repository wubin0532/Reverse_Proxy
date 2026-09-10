package ddns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"andey-proxy/internal/config"
)

func TestTencentUpsert(t *testing.T) {
	var mu sync.Mutex
	var actions []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("X-TC-Action")
		if r.Header.Get("X-TC-Version") != "2021-03-23" {
			t.Errorf("X-TC-Version 错误: %s", r.Header.Get("X-TC-Version"))
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "TC3-HMAC-SHA256 Credential=tc-secret-id/") || !strings.Contains(auth, "/dnspod/tc3_request") {
			t.Errorf("Authorization 格式错误: %s", auth)
		}
		if r.Header.Get("X-TC-Timestamp") == "" {
			t.Error("缺少 X-TC-Timestamp")
		}
		body, _ := io.ReadAll(r.Body)
		var params map[string]interface{}
		if err := json.Unmarshal(body, &params); err != nil {
			t.Errorf("请求体不是 JSON: %v", err)
		}
		mu.Lock()
		actions = append(actions, action)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch action {
		case "DescribeRecordList":
			if params["Domain"] != "example.com" || params["Subdomain"] != "home" {
				t.Errorf("DescribeRecordList 参数错误: %v", params)
			}
			w.Write([]byte(`{"Response":{"RecordList":[],"TotalCount":0,"RequestId":"x"}}`))
		case "CreateRecord":
			if params["Value"] != "1.2.3.4" || params["RecordLine"] != "默认" {
				t.Errorf("CreateRecord 参数错误: %v", params)
			}
			w.Write([]byte(`{"Response":{"RecordId":55,"RequestId":"x"}}`))
		default:
			t.Errorf("未知 Action: %s", action)
		}
	}))
	defer srv.Close()

	p, err := newTencentProvider(config.DNSProviderConf{
		Type: "tencentcloud", Key: "tc-secret-id", Secret: "tc-secret-key", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpsertRecord(context.Background(), "home.example.com", "A", "1.2.3.4", 600); err != nil {
		t.Fatalf("UpsertRecord 失败: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"DescribeRecordList", "CreateRecord"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("调用序列错误: %v", actions)
	}
}

func TestTencentModify(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("X-TC-Action") {
		case "DescribeRecordList":
			w.Write([]byte(`{"Response":{"RecordList":[{"RecordId":55,"Name":"home","Type":"A","Value":"1.1.1.1"}],"RequestId":"x"}}`))
		case "ModifyRecord":
			body, _ := io.ReadAll(r.Body)
			var params map[string]interface{}
			json.Unmarshal(body, &params)
			if params["RecordId"] != float64(55) {
				t.Errorf("RecordId 错误: %v", params["RecordId"])
			}
			w.Write([]byte(`{"Response":{"RequestId":"x"}}`))
		default:
			t.Errorf("未知 Action: %s", r.Header.Get("X-TC-Action"))
		}
	}))
	defer srv.Close()

	p, err := newTencentProvider(config.DNSProviderConf{
		Type: "tencentcloud", Key: "k", Secret: "s", Endpoint: srv.URL,
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

func TestTencentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"Response":{"Error":{"Code":"InvalidParameter","Message":"域名不存在 tc-secret-id"},"RequestId":"x"}}`))
	}))
	defer srv.Close()
	p, err := newTencentProvider(config.DNSProviderConf{
		Type: "tencentcloud", Key: "tc-secret-id", Secret: "x", Endpoint: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.QueryRecord(context.Background(), "home.example.com", "A")
	if err == nil || !strings.Contains(err.Error(), "InvalidParameter") {
		t.Fatalf("错误处理不符合预期: %v", err)
	}
	if strings.Contains(err.Error(), "tc-secret-id") {
		t.Fatalf("错误信息泄露凭据: %v", err)
	}
}
