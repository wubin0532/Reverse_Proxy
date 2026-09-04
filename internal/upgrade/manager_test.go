package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/api"
)

func TestStateMachineTransitions(t *testing.T) {
	m := NewManager("0.1.0")

	if got := m.Status().State; got != StateIdle {
		t.Fatalf("初始状态 = %q, want idle", got)
	}

	// idle → downloading（Start 成功后立即返回）
	if !m.Start("0.2.0") {
		t.Fatal("空闲时 Start 应返回 true")
	}
	st := m.Status()
	if st.State != StateDownloading || st.Version != "0.2.0" {
		t.Fatalf("Start 后状态 = %+v, want downloading/0.2.0", st)
	}

	// 进行中再次 Start 应被拒绝
	if m.Start("0.3.0") {
		t.Fatal("升级进行中 Start 应返回 false（防并发）")
	}

	// downloading → installing → done
	m.setState(StateInstalling)
	if got := m.Status().State; got != StateInstalling {
		t.Fatalf("setState 后状态 = %q, want installing", got)
	}
	m.mu.Lock()
	m.state = StateDone
	m.note = "升级完成，请手动重启进程以运行新版本"
	m.mu.Unlock()
	st = m.Status()
	if st.State != StateDone || st.Note == "" {
		t.Fatalf("完成后状态 = %+v, want done 且带提示", st)
	}

	// 完成后允许再次升级
	m.setState(StateIdle)
	m.mu.Lock()
	m.state = StateDownloading
	m.mu.Unlock()
	m.fail(errors.New("下载安装包失败"))
	st = m.Status()
	if st.State != StateFailed || !strings.Contains(st.Error, "下载安装包失败") {
		t.Fatalf("失败后状态 = %+v, want failed 且带错误信息", st)
	}
}

func TestCheckAgainstFakeGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != repoPath+"/releases/latest" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tag_name": "v0.2.0",
			"html_url": "https://github.com/wubin0532/Reverse_Proxy/releases/tag/v0.2.0",
			"assets":   []interface{}{},
		})
	}))
	defer srv.Close()

	m := NewManager("0.1.0")
	m.apiBase = srv.URL
	m.client = srv.Client()

	res, err := m.Check(context.Background())
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if res.Current != "0.1.0" || res.Latest != "0.2.0" || !res.HasUpdate {
		t.Errorf("Check 结果 = %+v, want current=0.1.0 latest=0.2.0 hasUpdate=true", res)
	}
	if res.ReleaseURL == "" {
		t.Error("releaseUrl 不应为空")
	}

	// dev 版本不应提示升级
	mDev := NewManager("dev")
	mDev.apiBase = srv.URL
	mDev.client = srv.Client()
	res, err = mDev.Check(context.Background())
	if err != nil {
		t.Fatalf("Check 失败: %v", err)
	}
	if res.HasUpdate {
		t.Error("dev 版本 hasUpdate 应为 false")
	}
}

func TestCheckGitHubUnreachable(t *testing.T) {
	m := NewManager("0.1.0")
	m.apiBase = "http://127.0.0.1:1" // 不可达地址
	if _, err := m.Check(context.Background()); err == nil {
		t.Fatal("GitHub 不可达时应返回错误")
	} else if !strings.Contains(err.Error(), "无法连接 GitHub") {
		t.Errorf("错误信息不友好: %v", err)
	}
}

// newTestRouter 构造挂了升级路由的测试路由。
func newTestRouter(m *Manager) chi.Router {
	r := chi.NewRouter()
	m.routes(r)
	return r
}

func TestAPIInfoAndStatus(t *testing.T) {
	m := NewManager("0.1.0")
	r := newTestRouter(m)

	req := httptest.NewRequest(http.MethodGet, "/api/system/info", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var resp api.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	data := resp.Data.(map[string]interface{})
	if data["version"] != "0.1.0" || data["goos"] == "" || data["goarch"] == "" {
		t.Errorf("info 响应异常: %v", data)
	}
	if _, ok := data["uptime"]; !ok {
		t.Error("info 响应缺少 uptime")
	}

	m.setState(StateDownloading)
	req = httptest.NewRequest(http.MethodGet, "/api/system/upgrade/status", nil)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Data.(map[string]interface{})["state"] != "downloading" {
		t.Errorf("status 响应 = %v, want downloading", resp.Data)
	}
}

func TestAPIUpgradeConflict(t *testing.T) {
	m := NewManager("0.1.0")
	// 置为进行中，POST 应返回 409
	m.setState(StateDownloading)
	r := newTestRouter(m)

	req := httptest.NewRequest(http.MethodPost, "/api/system/upgrade", strings.NewReader(`{"version":"0.2.0"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Errorf("升级中重复 POST 状态码 = %d, want 409", rec.Code)
	}
}
