package guard

import "testing"

func TestCompiledIPMatcher(t *testing.T) {
	list := []string{"192.0.2.0/24", "2001:db8::/32", " 127.0.0.1 ", "invalid"}
	for _, mode := range []string{"whitelist", "blacklist", "off"} {
		m := CompileIP(mode, list)
		for _, tc := range []struct {
			ip    string
			match bool
		}{{"192.0.2.1", true}, {"::ffff:192.0.2.1", true}, {"2001:db8::1", true}, {"127.0.0.1", true}, {"198.51.100.1", false}, {"bad-ip", false}} {
			want := tc.match
			if mode == "blacklist" {
				want = !want
			}
			if mode == "off" {
				want = true
			}
			if m.Allow(tc.ip) != want {
				t.Errorf("%s %s: want %v", mode, tc.ip, want)
			}
		}
	}
	if CompileIP("whitelist", nil).Allow("127.0.0.1") {
		t.Fatal("empty whitelist must deny")
	}
}
