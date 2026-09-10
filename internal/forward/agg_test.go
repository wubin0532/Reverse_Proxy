package forward

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func TestRingLogEscapesNewlines(t *testing.T) {
	rl := NewRingLog(10)
	rl.Add("first\nsecond\r\nthird")
	entries := rl.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d", len(entries))
	}
	if strings.Contains(entries[0], "\n") || strings.Contains(entries[0], "\r") {
		t.Fatalf("环形日志条目含换行: %q", entries[0])
	}
	if !strings.Contains(entries[0], `first\nsecond\nthird`) {
		t.Fatalf("换行未被转义: %q", entries[0])
	}
}

func TestConnAggregationSummarizes(t *testing.T) {
	svc := NewService(&config.Config{})
	defer svc.Stop()
	rule := config.ForwardRule{ID: "r1", Name: "r1", Targets: []string{"127.0.0.1:80"}}

	for i := 0; i < 5; i++ {
		svc.noteConn(rule, "10.0.0.1", "127.0.0.1:80", false)
	}
	svc.noteConn(rule, "10.0.0.2", "127.0.0.1:80", false)
	svc.noteBytes("r1", 2048, 4096)
	svc.noteReject(rule, "10.0.0.9")
	svc.noteReject(rule, "10.0.0.9")
	svc.noteDialFail(rule, "127.0.0.1:80", fmt.Errorf("refused"))
	svc.noteDialFail(rule, "127.0.0.1:80", fmt.Errorf("refused"))
	svc.flushConnSummaries()

	entries := svc.Logs("r1")
	var newPeer, reject, dialFail, summaries int
	var summary string
	for _, e := range entries {
		switch {
		case strings.Contains(e, "新对端"):
			newPeer++
		case strings.Contains(e, "拒绝来自"):
			reject++
		case strings.Contains(e, "连接目标") && strings.Contains(e, "失败"):
			dialFail++
		case strings.Contains(e, "最近一分钟"):
			summaries++
			summary = e
		}
	}
	if newPeer != 2 {
		t.Fatalf("新对端条目 = %d，期望 2（每个独立对端一条）", newPeer)
	}
	if reject != 1 || dialFail != 1 {
		t.Fatalf("拒绝/失败即时条目 = %d/%d，期望各 1（每窗口第一条）", reject, dialFail)
	}
	if summaries != 1 {
		t.Fatalf("汇总条目 = %d: %v", summaries, entries)
	}
	for _, want := range []string{"6 个 TCP 连接", "独立对端 3", "上行 2.0 KiB", "下行 4.0 KiB", "拒绝 2", "目标连接失败 2"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("汇总缺少 %q: %s", want, summary)
		}
	}
}

func TestConnSummaryFlushesOnWindowExpiry(t *testing.T) {
	old := connSummaryInterval
	connSummaryInterval = 40 * time.Millisecond
	defer func() { connSummaryInterval = old }()

	svc := NewService(&config.Config{})
	defer svc.Stop()
	rule := config.ForwardRule{ID: "r2", Name: "r2", Targets: []string{"127.0.0.1:80"}}
	svc.noteConn(rule, "10.0.0.1", "127.0.0.1:80", false)
	time.Sleep(60 * time.Millisecond)
	svc.noteConn(rule, "10.0.0.1", "127.0.0.1:80", false)

	var summaries int
	for _, e := range svc.Logs("r2") {
		if strings.Contains(e, "最近一分钟") {
			summaries++
		}
	}
	if summaries != 1 {
		t.Fatalf("窗口到期未自动汇总: %v", svc.Logs("r2"))
	}
}

// 集成：多条 TCP 连接只产生少量聚合日志，不再逐连接刷屏。
func TestTCPForwardAggregatesConnLogs(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			c, err := backend.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddr := ln.Addr().String()
	ln.Close()

	cfg := &config.Config{
		Forwards: []config.ForwardRule{{
			ID: "t-agg", Enabled: true, Proto: "tcp",
			Listen:  listenAddr,
			Targets: []string{backend.Addr().String()},
		}},
	}
	svc := NewService(cfg)
	svc.Start()
	defer svc.Stop()
	time.Sleep(100 * time.Millisecond)

	for i := 0; i < 3; i++ {
		conn, err := net.DialTimeout("tcp", listenAddr, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte("ping")); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	waitSlots(t, svc.tcpSlots, 0)
	svc.flushConnSummaries()

	entries := svc.Logs("t-agg")
	var established, newPeer, summaries int
	for _, e := range entries {
		switch {
		case strings.Contains(e, "已建立"):
			established++
		case strings.Contains(e, "新对端"):
			newPeer++
		case strings.Contains(e, "最近一分钟"):
			summaries++
		}
	}
	if established != 0 {
		t.Fatalf("仍存在逐连接日志: %v", entries)
	}
	if newPeer != 1 {
		t.Fatalf("新对端条目 = %d（同一对端应只记一条）: %v", newPeer, entries)
	}
	if summaries != 1 || !strings.Contains(entries[len(entries)-1], "3 个 TCP 连接") {
		t.Fatalf("汇总条目异常: %v", entries)
	}
}

func TestKnownPeersCapStopsNewPeerSpam(t *testing.T) {
	svc := NewService(&config.Config{})
	defer svc.Stop()
	rule := config.ForwardRule{ID: "r3", Name: "r3", Targets: []string{"127.0.0.1:80"}}
	a := svc.aggFor(rule.ID)
	a.known = make(map[string]struct{}, maxKnownPeersPerRule)
	for i := 0; i < maxKnownPeersPerRule; i++ {
		a.known[fmt.Sprintf("10.1.%d.%d", i/256, i%256)] = struct{}{}
	}
	// 表满后新对端不再即时记日志，只计数
	svc.noteConn(rule, "10.2.0.1", "127.0.0.1:80", false)
	var newPeer, notice int
	for _, e := range svc.Logs("r3") {
		if strings.Contains(e, "已连接 ->") {
			newPeer++
		}
		if strings.Contains(e, "已达上限") {
			notice++
		}
	}
	if newPeer != 0 || notice != 1 {
		t.Fatalf("newPeer=%d notice=%d", newPeer, notice)
	}
}
