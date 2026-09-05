package forward

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"andey-proxy/internal/config"
)

func waitSlots(t *testing.T, slots chan struct{}, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for len(slots) != want {
		if time.Now().After(deadline) {
			t.Fatalf("slots=%d want %d", len(slots), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTCPBudgetRejectsAndRecovers(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			c, e := backend.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(&config.Config{})
	svc.tcpSlots = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.serveTCP(ctx, config.ForwardRule{Targets: []string{backend.Addr().String()}}, ln)
	}()
	defer func() { cancel(); <-done; svc.wg.Wait() }()
	connect := func() net.Conn {
		t.Helper()
		c, e := net.Dial("tcp", ln.Addr().String())
		if e != nil {
			t.Fatal(e)
		}
		c.SetDeadline(time.Now().Add(2 * time.Second))
		return c
	}
	echo := func(c net.Conn) {
		t.Helper()
		if _, e := c.Write([]byte("x")); e != nil {
			t.Fatal(e)
		}
		b := make([]byte, 1)
		if _, e := io.ReadFull(c, b); e != nil {
			t.Fatal(e)
		}
		if b[0] != 'x' {
			t.Fatal("bad echo")
		}
	}
	first := connect()
	defer first.Close()
	echo(first)
	extra := connect()
	defer extra.Close()
	extra.Write([]byte("x"))
	b := make([]byte, 1)
	if _, err := extra.Read(b); err == nil {
		t.Fatal("over-budget TCP request reached backend")
	} else if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("over-budget connection was left open")
	}
	first.Close()
	waitSlots(t, svc.tcpSlots, 0)
	again := connect()
	defer again.Close()
	echo(again)
}

func TestUDPBudgetPreservesExistingSessionAndReleasesOnStop(t *testing.T) {
	backend, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		buf := make([]byte, 128)
		for {
			n, a, e := backend.ReadFromUDP(buf)
			if e != nil {
				return
			}
			backend.WriteToUDP(buf[:n], a)
		}
	}()
	ln, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(&config.Config{})
	svc.udpSlots = make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.serveUDP(ctx, config.ForwardRule{Targets: []string{backend.LocalAddr().String()}}, ln)
	}()
	defer func() {
		cancel()
		<-done
		svc.wg.Wait()
		if len(svc.udpSlots) != 0 {
			t.Error("UDP budget leaked")
		}
	}()
	connect := func() *net.UDPConn {
		t.Helper()
		c, e := net.DialUDP("udp", nil, ln.LocalAddr().(*net.UDPAddr))
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	first := connect()
	defer first.Close()
	echo := func(c *net.UDPConn) {
		t.Helper()
		c.SetDeadline(time.Now().Add(time.Second))
		c.Write([]byte("x"))
		b := make([]byte, 1)
		if _, e := c.Read(b); e != nil {
			t.Fatal(e)
		}
	}
	echo(first)
	extra := connect()
	defer extra.Close()
	extra.SetDeadline(time.Now().Add(150 * time.Millisecond))
	extra.Write([]byte("x"))
	b := make([]byte, 1)
	if _, e := extra.Read(b); e == nil {
		t.Fatal("new UDP session exceeded budget")
	}
	echo(first)
	if len(svc.udpSlots) != 1 {
		t.Fatal("unexpected session count")
	}
}
