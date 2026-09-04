package upgrade

import (
	"strconv"
	"strings"
	"unicode"
)

// assetArch 将 Go 架构名映射为 release 资产命名中的架构后缀。
var assetArch = map[string]string{
	"amd64":  "x86_64",
	"arm64":  "arm64",
	"arm":    "armv7",
	"mipsle": "mipsle",
	"mips":   "mips",
}

// archSuffix 返回当前架构对应的资产后缀，不支持时 ok=false。
func archSuffix(goarch string) (suffix string, ok bool) {
	suffix, ok = assetArch[goarch]
	return
}

// isDev 判断版本号是否为开发版本（dev 或非数字开头），开发版本不参与升级比较。
func isDev(v string) bool {
	v = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(v)), "v")
	if v == "" {
		return true
	}
	return !unicode.IsDigit(rune(v[0]))
}

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

// hasUpdate 判断 latest 是否比 current 新；current 为 dev 时始终返回 false。
func hasUpdate(current, latest string) bool {
	if isDev(current) {
		return false
	}
	return compareVersions(latest, current) > 0
}

// normalizeTag 将用户输入的版本号规整为 release tag（补前导 v）。
func normalizeTag(v string) string {
	v = strings.TrimSpace(v)
	if v != "" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}
