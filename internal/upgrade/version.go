package upgrade

import (
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

// parseVersion 去掉前导 v 后按点拆分，各段取前导数字，非数字段按 0 处理。
func parseVersion(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		end := 0
		for end < len(p) && p[end] >= '0' && p[end] <= '9' {
			end++
		}
		n, _ := strconv.Atoi(p[:end])
		nums[i] = n
	}
	return nums
}

// compareVersions 比较两个版本号，a<b 返回 -1，相等返回 0，a>b 返回 1。
func compareVersions(a, b string) int {
	semA, semB := normalizeSemver(a), normalizeSemver(b)
	if semver.IsValid(semA) && semver.IsValid(semB) {
		return semver.Compare(semA, semB)
	}
	pa, pb := parseVersion(a), parseVersion(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}
