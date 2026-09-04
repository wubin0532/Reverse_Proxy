package upgrade

import "testing"

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "0.2.0", -1},
		{"0.2.0", "v0.1.0", 1},
		{"v0.1.0", "0.1.0", 0},
		{"1.0.0", "0.9.9", 1},
		{"0.1.0", "0.1.0", 0},
		{"0.1", "0.1.0", 0},
		{"0.1.1", "0.1", 1},
		{"v10.0.0", "v9.9.9", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestHasUpdate(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.1.0", "v0.2.0", true},
		{"v0.1.0", "0.1.0", false},
		{"0.2.0", "v0.1.0", false},
		{"dev", "v0.2.0", false},   // dev 版本不提示升级
		{"dev", "v99.0.0", false},  // dev 无论多新都不提示
		{"", "v0.2.0", false},      // 空版本视为 dev
	}
	for _, c := range cases {
		if got := hasUpdate(c.current, c.latest); got != c.want {
			t.Errorf("hasUpdate(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestIsDev(t *testing.T) {
	for _, v := range []string{"dev", "Dev", "", "dev-abc"} {
		if !isDev(v) {
			t.Errorf("isDev(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"0.1.0", "v1.2.3"} {
		if isDev(v) {
			t.Errorf("isDev(%q) = true, want false", v)
		}
	}
}

func TestArchSuffix(t *testing.T) {
	cases := map[string]string{
		"amd64":  "x86_64",
		"arm64":  "arm64",
		"arm":    "armv7",
		"mipsle": "mipsle",
		"mips":   "mips",
	}
	for goarch, want := range cases {
		got, ok := archSuffix(goarch)
		if !ok || got != want {
			t.Errorf("archSuffix(%q) = %q, %v; want %q, true", goarch, got, ok, want)
		}
	}
	for _, goarch := range []string{"386", "riscv64", "ppc64"} {
		if _, ok := archSuffix(goarch); ok {
			t.Errorf("archSuffix(%q) ok = true, want false（不支持的架构）", goarch)
		}
	}
}

func TestNormalizeTag(t *testing.T) {
	if got := normalizeTag("0.2.0"); got != "v0.2.0" {
		t.Errorf("normalizeTag(0.2.0) = %q, want v0.2.0", got)
	}
	if got := normalizeTag("v0.2.0"); got != "v0.2.0" {
		t.Errorf("normalizeTag(v0.2.0) = %q, want v0.2.0", got)
	}
}
