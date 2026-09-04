package ddns

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"andey-proxy/internal/config"
)

func TestSplitDomain(t *testing.T) {
	cases := []struct {
		fqdn     string
		wantRR   string
		wantRoot string
	}{
		{"home.example.com", "home", "example.com"},
		{"example.com", "@", "example.com"},
		{"a.b.example.com", "a.b", "example.com"},
		{"home.example.com.cn", "home", "example.com.cn"},
		{"example.co.uk", "@", "example.co.uk"},
		{"www.example.co.uk", "www", "example.co.uk"},
		{"HOME.Example.COM.", "home", "example.com"},
	}
	for _, c := range cases {
		rr, root, err := splitDomain(c.fqdn)
		if err != nil {
			t.Fatalf("splitDomain(%s) 出错: %v", c.fqdn, err)
		}
		if rr != c.wantRR || root != c.wantRoot {
			t.Fatalf("splitDomain(%s) = (%s, %s), 期望 (%s, %s)", c.fqdn, rr, root, c.wantRR, c.wantRoot)
		}
	}
	if _, _, err := splitDomain("localhost"); err == nil {
		t.Fatal("非法域名应返回错误")
	}
}

func TestValidateIP(t *testing.T) {
	if got, err := validateIP(" 1.2.3.4\n", false); err != nil || got != "1.2.3.4" {
		t.Fatalf("validateIP ipv4 失败: got=%q err=%v", got, err)
	}
	if got, err := validateIP("2001:db8::1", true); err != nil || got != "2001:db8::1" {
		t.Fatalf("validateIP ipv6 失败: got=%q err=%v", got, err)
	}
	if _, err := validateIP("not-an-ip", false); err == nil {
		t.Fatal("非法 IP 应返回错误")
	}
	if _, err := validateIP("1.2.3.4", true); err == nil {
		t.Fatal("ipv6 任务收到 ipv4 应返回错误")
	}
	if _, err := validateIP("2001:db8::1", false); err == nil {
		t.Fatal("ipv4 任务收到 ipv6 应返回错误")
	}
}

func TestPickFromAddrs(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.1.2"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("fd00::1"), Mask: net.CIDRMask(64, 128)},
		&net.IPNet{IP: net.ParseIP("2001:db8::5"), Mask: net.CIDRMask(64, 128)},
	}
	// ipv4：跳过回环，取第一个全局单播
	ip, err := pickFromAddrs(addrs, false)
	if err != nil || ip != "192.168.1.2" {
		t.Fatalf("pickFromAddrs ipv4 = %q, %v", ip, err)
	}
	// ipv6：跳过链路本地与 ULA
	ip, err = pickFromAddrs(addrs, true)
	if err != nil || ip != "2001:db8::5" {
		t.Fatalf("pickFromAddrs ipv6 = %q, %v", ip, err)
	}
	// 只有回环时应报错
	if _, err := pickFromAddrs([]net.Addr{addrs[0]}, false); err == nil {
		t.Fatal("只有回环地址时应返回错误")
	}
}

func TestFetchIPFromAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(" 203.0.113.7\n"))
	}))
	defer srv.Close()
	ip, err := fetchIPFromAPI(context.Background(), srv.URL, false)
	if err != nil || ip != "203.0.113.7" {
		t.Fatalf("fetchIPFromAPI = %q, %v", ip, err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>error</html>"))
	}))
	defer bad.Close()
	if _, err := fetchIPFromAPI(context.Background(), bad.URL, false); err == nil {
		t.Fatal("返回非 IP 内容时应报错")
	}
}

func TestGetIP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("203.0.113.9"))
	}))
	defer srv.Close()
	ip, err := GetIP(context.Background(), config.DDNSTask{
		Name: "t1", IPSource: "api", APIURL: srv.URL, IPType: "ipv4",
	})
	if err != nil || ip != "203.0.113.9" {
		t.Fatalf("GetIP api = %q, %v", ip, err)
	}
	if _, err := GetIP(context.Background(), config.DDNSTask{Name: "t2", IPSource: "webhook"}); err == nil {
		t.Fatal("webhook 应返回未实现错误")
	}
	if _, err := GetIP(context.Background(), config.DDNSTask{Name: "t3", IPSource: "interface"}); err == nil {
		t.Fatal("未配置网卡名应报错")
	}
}
