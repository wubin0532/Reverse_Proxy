//go:build !linux

package metrics

// Runtime process metrics are available; host /proc metrics are unsupported.
func readHost() hostCounters { return hostCounters{} }
