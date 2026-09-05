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
		{"1.0.0-beta.1", "1.0.0", -1},
		{"1.0.0-beta.2", "1.0.0-beta.1", 1},
		{"1.0.0+build.2", "1.0.0+build.1", 0},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
