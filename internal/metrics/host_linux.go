//go:build linux

package metrics

import (
	"os"
	"strconv"
	"strings"
)

func readHost() hostCounters {
	var h hostCounters
	if d, e := os.ReadFile("/proc/stat"); e == nil {
		h.total, h.idle, h.cpuOK = cpuCounters(string(d))
	}
	if d, e := os.ReadFile("/proc/meminfo"); e == nil {
		h.memory, h.available, h.memoryOK = memoryCounters(string(d))
	}
	if d, e := os.ReadFile("/proc/net/route"); e == nil {
		h.iface = defaultInterface(string(d))
	}
	if d, e := os.ReadFile("/proc/net/dev"); e == nil {
		h.received, h.sent, h.networkOK = networkCounters(string(d), h.iface)
	}
	if d, e := os.ReadFile("/proc/self/statm"); e == nil {
		f := strings.Fields(string(d))
		if len(f) > 1 {
			if pages, e := strconv.ParseUint(f[1], 10, 64); e == nil {
				h.resident = pages * uint64(os.Getpagesize())
			}
		}
	}
	return h
}
