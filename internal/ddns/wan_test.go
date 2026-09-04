package ddns

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFixture(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "route")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseIPv4DefaultRoute(t *testing.T) {
	fixture := `Iface	Destination	Gateway	Flags	RefCnt	Use	Metric	Mask	MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	10	00000000	0	0	0
pppoe-wan	00000000	0101A8C0	0003	0	0	5	00000000	0	0	0
br-lan	0001A8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
`
	dev, err := parseIPv4DefaultRoute(writeFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if dev != "pppoe-wan" {
		t.Fatalf("应取 metric 最小的 pppoe-wan，得到 %s", dev)
	}
}

func TestParseIPv4DefaultRouteNone(t *testing.T) {
	fixture := `Iface	Destination	Gateway	Flags	RefCnt	Use	Metric	Mask	MTU	Window	IRTT
br-lan	0001A8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
`
	if _, err := parseIPv4DefaultRoute(writeFixture(t, fixture)); err == nil {
		t.Fatal("无默认路由应报错")
	}
}

func TestParseIPv6DefaultRoute(t *testing.T) {
	fixture := `00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000064 00000001 00000000 00000003 pppoe-wan
240e0340... 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001 eth0
`
	dev, err := parseIPv6DefaultRoute(writeFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if dev != "pppoe-wan" {
		t.Fatalf("应取 pppoe-wan，得到 %s", dev)
	}
}
