package logcenter

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/go-chi/chi/v5"
)

// 回归：含内嵌换行/回车的消息必须保持一行一个 JSON 对象，
// 写盘、重载、导出全程不破坏 NDJSON 结构。
func TestMultiLineMessageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	c.Add(Entry{Level: "error", Source: "webproxy", Message: "stack: first\nsecond\r\nthird"})
	c.Add(Entry{Level: "info", Source: "system", Message: "after"})
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"andey-proxy.log", "andey-proxy-access.log"} {
		data, err := os.ReadFile(filepath.Join(dir, "logs", name))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		for i, line := range lines {
			var e Entry
			if err := json.Unmarshal([]byte(line), &e); err != nil {
				t.Fatalf("%s 第 %d 行不是合法 JSON: %v", name, i, err)
			}
		}
	}
	c2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	entries, _ := c2.Query(Query{Limit: 10})
	if len(entries) != 2 {
		t.Fatalf("重载后条目数 = %d", len(entries))
	}
	found := false
	for _, e := range entries {
		if strings.Contains(e.Message, "first\nsecond\r\nthird") {
			found = true
		}
	}
	if !found {
		t.Fatalf("多行消息未完整保留: %+v", entries)
	}
	var buf bytes.Buffer
	if err := c2.Export(&buf, Query{}); err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("导出第 %d 行不是合法 JSON: %v", i, err)
		}
	}
}

// 回归：超长行（超过旧 Scanner 64KiB 上限）或损坏行只跳过自身，
// 不能导致同文件后续条目全部丢失。
func TestLoadSurvivesLongAndCorruptLines(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	big, _ := json.Marshal(Entry{Level: "info", Source: "system", Message: strings.Repeat("x", 200<<10)})
	after, _ := json.Marshal(Entry{Level: "info", Source: "system", Message: "after-long-line"})
	var data []byte
	data = append(data, append(big, '\n')...)
	data = append(data, []byte("corrupted-line\n")...)
	data = append(data, append(after, '\n')...)
	if err := os.WriteFile(filepath.Join(dir, "logs", "andey-proxy.log"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	entries, _ := c.Query(Query{Limit: 10})
	if len(entries) != 2 {
		t.Fatalf("加载条目数 = %d（超长行/损坏行之后的条目丢失）", len(entries))
	}
	if entries[0].Message != "after-long-line" {
		t.Fatalf("最新条目 = %q", entries[0].Message)
	}
}

func TestAccessAndSystemStoresAreSeparate(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for i := 0; i < 5; i++ {
		c.Add(Entry{Level: "info", Source: "webproxy", Message: fmt.Sprintf("1.2.3.4 /req%d 200 1ms", i)})
	}
	c.Add(Entry{Level: "error", Source: "system", Message: "磁盘几乎写满"})
	c.Add(Entry{Level: "warn", Source: "security", Message: "登录失败"})

	access, _ := c.Query(Query{Type: TypeAccess, Limit: 100})
	if len(access) != 5 {
		t.Fatalf("access 查询 = %d 条", len(access))
	}
	system, _ := c.Query(Query{Type: TypeSystem, Limit: 100})
	if len(system) != 2 {
		t.Fatalf("system 查询 = %d 条", len(system))
	}
	all, _ := c.Query(Query{Limit: 100})
	if len(all) != 7 {
		t.Fatalf("缺省合并查询 = %d 条", len(all))
	}
	// 合并视图按时间倒序
	for i := 1; i < len(all); i++ {
		if all[i-1].Time.Before(all[i].Time) {
			t.Fatal("合并查询未按时间倒序")
		}
	}
	// 导出同样支持类型过滤
	var buf bytes.Buffer
	if err := c.Export(&buf, Query{Type: TypeAccess}); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(strings.TrimSpace(buf.String()), "\n") + 1; n != 5 {
		t.Fatalf("access 导出行数 = %d", n)
	}
	if !validLogType("") || !validLogType(TypeAccess) || !validLogType(TypeSystem) || validLogType("bogus") {
		t.Fatal("validLogType 判定错误")
	}
}

// 访问库写满轮转时，系统库文件必须原样保留。
func TestAccessRotationKeepsSystemStore(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	marker, _ := json.Marshal(Entry{Level: "error", Source: "system", Message: "关键错误标记"})
	marker = append(marker, '\n')
	c.writeBatch(c.system, marker)
	chunk := bytes.Repeat([]byte("y"), 64<<10)
	for i := 0; i < 100; i++ {
		c.writeBatch(c.access, chunk)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// 访问库总量有界，系统库未被轮转
	var accessTotal int64
	entries, _ := os.ReadDir(filepath.Join(dir, "logs"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "andey-proxy-access.log") {
			st, _ := e.Info()
			accessTotal += st.Size()
		}
	}
	if accessTotal > 5*(1<<20) {
		t.Fatalf("访问库占用 %d 字节，超过 5 MiB", accessTotal)
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs", "andey-proxy.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "关键错误标记") {
		t.Fatal("访问库轮转冲掉了系统库内容")
	}
}

func TestClearTypeOnlyClearsOneStore(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Add(Entry{Level: "info", Source: "webproxy", Message: "access-entry"})
	c.Add(Entry{Level: "error", Source: "system", Message: "system-entry"})
	if err := c.ClearType(TypeAccess); err != nil {
		t.Fatal(err)
	}
	access, _ := c.Query(Query{Type: TypeAccess, Limit: 10})
	system, _ := c.Query(Query{Type: TypeSystem, Limit: 10})
	if len(access) != 0 || len(system) != 1 {
		t.Fatalf("access=%d system=%d", len(access), len(system))
	}
	if err := c.ClearType("bogus"); err == nil {
		t.Fatal("非法类型应报错")
	}
}

func streamTestServer(t *testing.T, c *Center) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	RegisterRoutes(r, c, &config.Config{})
	return r
}

func TestStreamEndpoint(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := httptest.NewServer(streamTestServer(t, c))
	defer srv.Close()

	// 带类型过滤的流：只应收到 access 条目
	req, _ := http.NewRequest("GET", srv.URL+"/api/logs/stream?type=access&level=info", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	reader := bufio.NewReader(resp.Body)
	if line, _ := reader.ReadString('\n'); line != ": connected\n" {
		t.Fatalf("首行 = %q", line)
	}
	c.Add(Entry{Level: "info", Source: "webproxy", Message: "access-hit\nmultiline"})
	c.Add(Entry{Level: "error", Source: "system", Message: "should-not-appear"})
	deadline := time.Now().Add(3 * time.Second)
	var event string
	for time.Now().Before(deadline) {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			event = line
			break
		}
	}
	if event == "" {
		t.Fatal("未收到日志事件")
	}
	var e Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(event, "data: "))), &e); err != nil {
		t.Fatalf("事件数据不是合法 JSON: %v", err)
	}
	if e.Message != "access-hit\nmultiline" || e.Source != "webproxy" {
		t.Fatalf("事件内容 = %+v", e)
	}
}

func TestStreamClientCap(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := httptest.NewServer(streamTestServer(t, c))
	defer srv.Close()

	var bodies []interface{ Close() error }
	for i := 0; i < maxStreamClients; i++ {
		resp, err := http.Get(srv.URL + "/api/logs/stream")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("第 %d 个流 status = %d", i, resp.StatusCode)
		}
		bodies = append(bodies, resp.Body)
	}
	defer func() {
		for _, b := range bodies {
			b.Close()
		}
	}()
	resp, err := http.Get(srv.URL + "/api/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("超限连接 status = %d", resp.StatusCode)
	}
	// 关闭一个后应能再连
	bodies[0].Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(srv.URL + "/api/logs/stream")
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode == 200 {
			resp.Body.Close()
			return
		}
		resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("释放一个连接后仍无法新建流")
}

func TestStreamQueryRejectsInvalidType(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	srv := httptest.NewServer(streamTestServer(t, c))
	defer srv.Close()
	for _, path := range []string{"/api/logs?type=bogus", "/api/logs/download?type=bogus", "/api/logs/stream?type=bogus"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("%s status = %d", path, resp.StatusCode)
		}
	}
}

// 慢消费者丢最旧策略：不消费的情况下灌入超过缓冲的事件，
// 恢复消费后应收到最新的事件而不是卡住。
func TestStreamHubDropsOldestForSlowClient(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sub, ok := c.hub.subscribe(Query{})
	if !ok {
		t.Fatal("订阅失败")
	}
	for i := 0; i < streamQueueSize+50; i++ {
		c.hub.publish(Entry{Level: "info", Source: "system", Message: fmt.Sprintf("msg-%d", i)})
	}
	last := -1
	for i := 0; i < streamQueueSize; i++ {
		e := <-sub.ch
		var n int
		fmt.Sscanf(e.Message, "msg-%d", &n)
		last = n
	}
	if last != streamQueueSize+49 {
		t.Fatalf("缓冲末尾事件 = msg-%d，期望最新事件", last)
	}
}
