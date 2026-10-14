package metrics

import (
	"testing"
	"time"
)

func TestLinuxCountersAndDefaultRoute(t *testing.T) {
	total, idle, ok := cpuCounters("cpu 10 0 20 50 5 1 2 3 999 999\n")
	if !ok || total != 91 || idle != 55 {
		t.Fatalf("guest counters double counted: %d %d", total, idle)
	}
	memory, available, ok := memoryCounters("MemTotal: 100 kB\nMemAvailable: 40 kB\n")
	if !ok || memory != 102400 || available != 40960 {
		t.Fatal("memory units")
	}
	iface := defaultInterface("Iface Destination Gateway Flags RefCnt Use Metric\nbackup 00000000 01 0003 0 0 10\nwan 00000000 01 0003 0 0 1\n")
	if iface != "wan" {
		t.Fatal("must use lowest metric default route")
	}
	in, out, ok := networkCounters("wan: 100 1 0 0 0 0 0 0 200 1 0 0 0 0 0 0\n", iface)
	if !ok || in != 100 || out != 200 {
		t.Fatal("network counter parse")
	}
	if _, _, ok = cpuCounters("cpu malformed"); ok {
		t.Fatal("invalid sample accepted")
	}
}

func TestCounterDeltaResetAndUnsupported(t *testing.T) {
	s := &Sampler{}
	at := time.Unix(100, 0)
	s.sample(at, hostCounters{total: 100, idle: 50, cpuOK: true, memory: 100, available: 60, memoryOK: true, iface: "wan", received: 100, sent: 50, networkOK: true})
	if s.Snapshot().HostCPU != nil {
		t.Fatal("first sample must be unknown")
	}
	s.sample(at.Add(5*time.Second), hostCounters{total: 200, idle: 100, cpuOK: true, memory: 100, available: 60, memoryOK: true, iface: "wan", received: 150, sent: 60, networkOK: true})
	n := s.Snapshot()
	if points := s.History(false); len(points) != 2 || points[1].BytesInPerSecond == nil || *points[1].BytesInPerSecond != 10 {
		t.Fatal("network history missing measured rates")
	}
	if n.HostCPU == nil || *n.HostCPU != 50 || n.BytesInPerSecond == nil || *n.BytesInPerSecond != 10 {
		t.Fatalf("wrong differences: %+v", n)
	}
	s.sample(at.Add(10*time.Second), hostCounters{total: 10, idle: 5, cpuOK: true, iface: "wan", received: 1, networkOK: true})
	if s.Snapshot().HostCPU != nil || s.Snapshot().BytesInPerSecond != nil {
		t.Fatal("reset counters must not produce fabricated rates")
	}
	s.sample(at.Add(15*time.Second), hostCounters{})
	if s.Snapshot().HostSupported || s.Snapshot().MemoryPercent != nil {
		t.Fatal("unsupported host must not show 0 usage")
	}
}

func TestHistoryBoundedAndStopIdempotent(t *testing.T) {
	s := &Sampler{}
	for i := 0; i < 2000; i++ {
		s.sample(time.Unix(int64(i*60), 0), hostCounters{})
	}
	if len(s.realtime) != 720 || len(s.minutes) != 1440 {
		t.Fatal("history is not bounded")
	}
	if len(s.History(false)) != 61 {
		t.Fatal("one-hour query includes older samples")
	}
	s.sample(time.Unix(2000*60, 0).Add(48*time.Hour), hostCounters{})
	if len(s.History(true)) != 1 {
		t.Fatal("sleep gap returns old 24-hour samples")
	}
	live := NewSampler()
	live.Stop()
	live.Stop()
}
