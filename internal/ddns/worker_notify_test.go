package ddns

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"andey-proxy/internal/config"
)

// TestRunTaskNotifyFailureTransition 验证“上次成功、本次失败”才推送失败通知。
func TestRunTaskNotifyFailureTransition(t *testing.T) {
	// IP 查询接口返回固定 IPv4
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1.2.3.4"))
	}))
	defer ts.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// ProviderID 指向不存在的凭据，runTask 必然在更新阶段失败
	task := config.DDNSTask{
		ID: "t1", Name: "测试任务", Enabled: true,
		Domains: []string{"home.example.com"},
		IPType:  "ipv4", IPSource: "api", APIURL: ts.URL,
		ProviderID: "missing",
	}
	w := NewWorker(cfg)
	var calls []string
	w.Notify = func(event, title, content string) {
		calls = append(calls, event+"|"+title+"|"+content)
	}

	// 首次失败（无历史状态）不通知。首轮已缓存 IP，后续用 force 强制走到更新阶段。
	if err := w.runTask(context.Background(), task, false); err == nil {
		t.Fatal("凭据缺失应执行失败")
	}
	if len(calls) != 0 {
		t.Fatalf("首次失败不应通知，实际 %d 次", len(calls))
	}

	// 第二次失败（上次失败）仍不通知
	w.runTask(context.Background(), task, true)
	if len(calls) != 0 {
		t.Fatalf("连续失败不应重复通知，实际 %d 次", len(calls))
	}

	// 上次成功后失败 → 通知一次
	w.setStatus(task.ID, "1.2.3.4", "", true, "ok")
	w.runTask(context.Background(), task, true)
	if len(calls) != 1 {
		t.Fatalf("状态转失败应通知一次，实际 %d 次", len(calls))
	}
	parts := strings.SplitN(calls[0], "|", 3)
	if parts[0] != "ddns" {
		t.Errorf("事件类型错误: %s", parts[0])
	}
	if !strings.Contains(parts[1], "DDNS 更新失败") {
		t.Errorf("标题错误: %s", parts[1])
	}
	if !strings.Contains(parts[2], "测试任务") || !strings.Contains(parts[2], "home.example.com") {
		t.Errorf("内容缺少任务名或域名: %s", parts[2])
	}
}

// TestRunTaskNoNotifyOnUnchangedIP IP 未变化跳过更新时不发通知。
func TestRunTaskNoNotifyOnUnchangedIP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("1.2.3.4"))
	}))
	defer ts.Close()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task := config.DDNSTask{
		ID: "t2", Name: "测试任务", Enabled: true,
		Domains: []string{"home.example.com"},
		IPType:  "ipv4", IPSource: "api", APIURL: ts.URL,
		ProviderID: "missing",
	}
	w := NewWorker(cfg)
	calls := 0
	w.Notify = func(event, title, content string) { calls++ }

	// 预置相同 IP 缓存与成功状态，本轮应“IP 未变化，跳过更新”
	w.lastIP["t2|ipv4"] = "1.2.3.4"
	w.setStatus(task.ID, "1.2.3.4", "", true, "ok")
	if err := w.runTask(context.Background(), task, false); err != nil {
		t.Fatalf("跳过更新不应报错: %v", err)
	}
	if calls != 0 {
		t.Fatalf("IP 未变化不应通知，实际 %d 次", calls)
	}
}
