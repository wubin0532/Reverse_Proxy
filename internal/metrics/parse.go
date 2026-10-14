package metrics

import (
	"strconv"
	"strings"
)

func cpuCounters(data string) (total, idle uint64, ok bool) {
	line := strings.SplitN(data, "\n", 2)[0]
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return
	}
	for i := 1; i < len(f) && i <= 8; i++ {
		v, e := strconv.ParseUint(f[i], 10, 64)
		if e != nil {
			return 0, 0, false
		}
		total += v
		if i == 4 || i == 5 {
			idle += v
		}
	}
	return total, idle, true
}

func memoryCounters(data string) (total, available uint64, ok bool) {
	fields := map[string]uint64{}
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, e := strconv.ParseUint(f[1], 10, 64)
		if e == nil {
			fields[strings.TrimSuffix(f[0], ":")] = v * 1024
		}
	}
	total = fields["MemTotal"]
	available = fields["MemAvailable"]
	if _, exists := fields["MemAvailable"]; !exists {
		available = fields["MemFree"] + fields["Buffers"] + fields["Cached"]
	}
	return total, available, total > 0 && available <= total
}

func defaultInterface(data string) string {
	best := ""
	var metric uint64 = ^uint64(0)
	for _, l := range strings.Split(data, "\n") {
		f := strings.Fields(l)
		if len(f) > 6 && f[1] == "00000000" {
			flags, e := strconv.ParseUint(f[3], 16, 64)
			m, me := strconv.ParseUint(f[6], 10, 64)
			if e == nil && me == nil && flags&1 != 0 && m < metric {
				best, metric = f[0], m
			}
		}
	}
	return best
}
func networkCounters(data, iface string) (received, sent uint64, ok bool) {
	if iface == "" {
		return
	}
	for _, l := range strings.Split(data, "\n") {
		parts := strings.SplitN(l, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != iface {
			continue
		}
		f := strings.Fields(parts[1])
		if len(f) < 9 {
			return
		}
		var a, b error
		received, a = strconv.ParseUint(f[0], 10, 64)
		sent, b = strconv.ParseUint(f[8], 10, 64)
		return received, sent, a == nil && b == nil
	}
	return
}
