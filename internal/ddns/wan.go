package ddns

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// resolveWANInterface 自动识别 WAN 口网卡名。
// 顺序：OpenWrt ubus（wan6/wan 的 l3_device）→ 系统默认路由网卡。
func resolveWANInterface(ipv6 bool) (string, error) {
	names := []string{"wan"}
	if ipv6 {
		names = []string{"wan6", "wan"}
	}
	for _, n := range names {
		if dev, err := ubusL3Device(n); err == nil && dev != "" {
			return dev, nil
		}
	}
	if dev, err := defaultRouteInterface(ipv6); err == nil && dev != "" {
		return dev, nil
	}
	return "", fmt.Errorf("无法自动识别 WAN 口网卡，请手动指定接口名")
}

// ubusL3Device 通过 OpenWrt ubus 查询接口的 L3 设备名（非 OpenWrt 系统返回错误）。
func ubusL3Device(name string) (string, error) {
	out, err := exec.Command("ubus", "call", "network.interface."+name, "status").Output()
	if err != nil {
		return "", err
	}
	var status struct {
		Up       bool   `json:"up"`
		L3Device string `json:"l3_device"`
		Device   string `json:"device"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return "", err
	}
	if !status.Up {
		return "", fmt.Errorf("接口 %s 未在线", name)
	}
	if status.L3Device != "" {
		return status.L3Device, nil
	}
	return status.Device, nil
}

// defaultRouteInterface 从内核路由表找默认路由所在网卡。
func defaultRouteInterface(ipv6 bool) (string, error) {
	if ipv6 {
		return parseIPv6DefaultRoute("/proc/net/ipv6_route")
	}
	return parseIPv4DefaultRoute("/proc/net/route")
}

// parseIPv4DefaultRoute 解析 /proc/net/route 格式，返回默认路由网卡（metric 最小优先）。
func parseIPv4DefaultRoute(path string) (string, error) {
	data, err := readFileLines(path)
	if err != nil {
		return "", err
	}
	bestDev, bestMetric := "", -1
	for i, line := range data {
		if i == 0 { // 表头
			continue
		}
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" { // 非默认路由
			continue
		}
		metric := 0
		fmt.Sscanf(f[6], "%d", &metric)
		if bestMetric == -1 || metric < bestMetric {
			bestDev, bestMetric = f[0], metric
		}
	}
	if bestDev == "" {
		return "", fmt.Errorf("路由表中无默认路由")
	}
	return bestDev, nil
}

// parseIPv6DefaultRoute 解析 /proc/net/ipv6_route 格式，返回默认路由网卡。
func parseIPv6DefaultRoute(path string) (string, error) {
	data, err := readFileLines(path)
	if err != nil {
		return "", err
	}
	for _, line := range data {
		f := strings.Fields(line)
		// 目标全零且前缀长度为 0 即默认路由；最后一列是网卡名
		if len(f) >= 10 && f[0] == "00000000000000000000000000000000" && f[1] == "00" {
			return f[len(f)-1], nil
		}
	}
	return "", fmt.Errorf("IPv6 路由表中无默认路由")
}

func readFileLines(path string) ([]string, error) {
	out, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s := strings.TrimRight(string(out), "\n")
	if s == "" {
		return nil, fmt.Errorf("%s 为空", path)
	}
	return strings.Split(s, "\n"), nil
}
