package ddns

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"andey-proxy/internal/config"
	"github.com/miekg/dns"
)

func stunReply(request []byte, ip net.IP) []byte {
	ip = ip.To4()
	b := make([]byte, 32)
	copy(b, request[:20])
	binary.BigEndian.PutUint16(b, 0x0101)
	binary.BigEndian.PutUint16(b[2:], 12)
	binary.BigEndian.PutUint16(b[20:], 0x0020)
	binary.BigEndian.PutUint16(b[22:], 8)
	b[25] = 1
	binary.BigEndian.PutUint16(b[26:], 1234^0x2112)
	for i := 0; i < 4; i++ {
		b[28+i] = ip[i] ^ b[4+i]
	}
	return b
}
func TestSTUNBindingAndResponseValidation(t *testing.T) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		b := make([]byte, 1024)
		for {
			n, peer, e := conn.ReadFrom(b)
			if e != nil {
				return
			}
			if n == 20 {
				conn.WriteTo(stunReply(b[:n], net.ParseIP("203.0.113.17")), peer)
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ip, _, err := resolveSource(ctx, config.IPSourceConfig{Source: "stun", APIURL: "stun://" + conn.LocalAddr().String()}, false)
	if err != nil || ip != "203.0.113.17" {
		t.Fatal(ip, err)
	}
	req := make([]byte, 20)
	binary.BigEndian.PutUint32(req[4:], 0x2112a442)
	reply := stunReply(req, net.ParseIP("203.0.113.17"))
	for _, change := range []func([]byte){func(b []byte) { b[8] = 1 }, func(b []byte) { b[4] = 0 }, func(b []byte) { b[2] = 1 }, func(b []byte) { b[25] = 2 }} {
		bad := append([]byte(nil), reply...)
		change(bad)
		if _, err := decodeSTUN(bad, req[8:], false); err == nil {
			t.Fatal("invalid STUN response accepted")
		}
	}
	if _, err := decodeSTUN(reply[:24], req[8:], false); err == nil {
		t.Fatal("truncated STUN accepted")
	}
}
func TestDNSAndOrderedSourceFallback(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	server := &dns.Server{PacketConn: pc, NotifyStartedFunc: func() { close(started) }, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, q *dns.Msg) {
		res := new(dns.Msg)
		res.SetReply(q)
		name := q.Question[0].Name
		switch name {
		case "source.example.":
			res.Answer = []dns.RR{&dns.TXT{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 60}, Txt: []string{"203.0.", "113.18"}}}
		case "alias.example.":
			res.Answer = []dns.RR{&dns.CNAME{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypeCNAME, Class: dns.ClassINET}, Target: "source.example."}}
		default:
			res.Answer = []dns.RR{&dns.A{Hdr: dns.RR_Header{Name: "unrelated.example.", Rrtype: dns.TypeA, Class: dns.ClassINET}, A: net.ParseIP("203.0.113.19")}}
		}
		w.WriteMsg(res)
	})}
	go server.ActivateAndServe()
	<-started
	defer server.Shutdown()
	source := config.IPSourceConfig{Source: "interface", Interface: "not-a-real-network", Fallbacks: []config.IPSourceConfig{{Source: "dns", APIURL: "dns://" + pc.LocalAddr().String() + "/alias.example/TXT"}}}
	ip, _, err := resolveSource(context.Background(), source, false)
	if err != nil || ip != "203.0.113.18" {
		t.Fatal(ip, err)
	}
	if _, _, err := resolveSource(context.Background(), config.IPSourceConfig{Source: "dns", APIURL: "dns://" + pc.LocalAddr().String() + "/other.example/A"}, false); err == nil {
		t.Fatal("unrelated DNS answer accepted")
	}
	httpSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("203.0.113.20")) }))
	defer httpSource.Close()
	source.Fallbacks = append(source.Fallbacks[:0], config.IPSourceConfig{Source: "api", APIURL: httpSource.URL})
	if ip, _, err := resolveSource(context.Background(), source, false); err != nil || ip != "203.0.113.20" {
		t.Fatal(ip, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := resolveSource(cancelled, source, false); err != context.Canceled {
		t.Fatal(err)
	}
	if config.ValidateIPSource(config.IPSourceConfig{Source: "dns", APIURL: "dns://localhost/example.com/AAAA"}, false) == nil {
		t.Fatal("wrong-family DNS type accepted")
	}
	if config.ValidateIPSource(config.IPSourceConfig{Source: "stun", APIURL: "stun://user:secret@localhost"}, false) == nil {
		t.Fatal("STUN credentials accepted")
	}
}
