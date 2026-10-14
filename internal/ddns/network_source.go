package ddns

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"andey-proxy/internal/config"
	"github.com/miekg/dns"
)

func resolveSource(ctx context.Context, s config.IPSourceConfig, v6 bool) (string, string, error) {
	if err := config.ValidateIPSource(s, v6); err != nil {
		return "", "", err
	}
	sources := append([]config.IPSourceConfig{s}, s.Fallbacks...)
	var failures []string
	for i, source := range sources {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		op, cancel := context.WithTimeout(ctx, 5*time.Second)
		var ip, iface string
		var err error
		switch source.Source {
		case "stun":
			ip, err = fetchSTUN(op, source.APIURL, v6)
		case "dns":
			ip, err = fetchDNS(op, source.APIURL, v6)
		default:
			typ := "ipv4"
			if v6 {
				typ = "ipv6"
			}
			ip, iface, err = getSingleIPDetail(op, config.DDNSTask{IPType: typ, IPSource: source.Source, Interface: source.Interface, APIURL: source.APIURL})
		}
		cancel()
		if err == nil {
			return ip, iface, nil
		}
		failures = append(failures, fmt.Sprintf("来源%d (%s): %s", i+1, source.Source, err))
	}
	return "", "", errors.New(strings.Join(failures, "；"))
}
func sourceServer(u *url.URL, port string) string {
	if u.Port() != "" {
		port = u.Port()
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// A connected UDP socket restricts replies to the selected peer. Transaction
// IDs, cookie, exact message length and address family are checked before use.
func fetchSTUN(ctx context.Context, raw string, v6 bool) (string, error) {
	u, _ := url.Parse(raw)
	network := "udp4"
	if v6 {
		network = "udp6"
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, network, sourceServer(u, "3478"))
	if err != nil {
		return "", fmt.Errorf("STUN连接失败")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request, 1)
	binary.BigEndian.PutUint32(request[4:], 0x2112a442)
	if _, err = rand.Read(request[8:]); err != nil {
		return "", err
	}
	deadline, _ := ctx.Deadline()
	rto := 500 * time.Millisecond
	buf := make([]byte, 2048)
	for attempt := 0; attempt < 4; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		conn.SetWriteDeadline(deadline)
		if _, err = conn.Write(request); err != nil {
			return "", fmt.Errorf("STUN发送失败")
		}
		until := time.Now().Add(rto)
		if until.After(deadline) {
			until = deadline
		}
		conn.SetReadDeadline(until)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				break
			}
			if ip, err := decodeSTUN(buf[:n], request[8:], v6); err == nil {
				return ip, nil
			}
		}
		rto *= 2
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("STUN查询超时或未返回匹配地址")
}
func decodeSTUN(b, tid []byte, v6 bool) (string, error) {
	if len(b) < 20 || binary.BigEndian.Uint16(b) != 0x0101 || binary.BigEndian.Uint32(b[4:]) != 0x2112a442 || !bytes.Equal(b[8:20], tid) || int(binary.BigEndian.Uint16(b[2:])) != len(b)-20 || (len(b)-20)%4 != 0 {
		return "", errors.New("STUN响应头无效")
	}
	for offset := 20; offset < len(b); {
		if offset+4 > len(b) {
			return "", errors.New("STUN属性截断")
		}
		typ := binary.BigEndian.Uint16(b[offset:])
		size := int(binary.BigEndian.Uint16(b[offset+2:]))
		offset += 4
		if offset+size > len(b) {
			return "", errors.New("STUN属性长度无效")
		}
		attr := b[offset : offset+size]
		offset += (size + 3) &^ 3
		if typ != 0x0020 && typ != 0x0001 {
			continue
		}
		length := 4
		family := byte(1)
		if v6 {
			length = 16
			family = 2
		}
		if len(attr) != 4+length || attr[0] != 0 || attr[1] != family {
			continue
		}
		ip := append(net.IP(nil), attr[4:]...)
		if typ == 0x0020 {
			mask := append([]byte{0x21, 0x12, 0xa4, 0x42}, tid...)
			for i := range ip {
				ip[i] ^= mask[i]
			}
		}
		return validateIP(ip.String(), v6)
	}
	return "", errors.New("STUN响应缺少地址")
}
func fetchDNS(ctx context.Context, raw string, v6 bool) (string, error) {
	u, _ := url.Parse(raw)
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	name := dns.Fqdn(parts[0])
	typ := dns.StringToType[strings.ToUpper(parts[1])]
	network := "udp4"
	tcp := "tcp4"
	if v6 {
		network = "udp6"
		tcp = "tcp6"
	}
	server := sourceServer(u, "53")
	seen := map[string]bool{}
	for hop := 0; hop < 8; hop++ {
		if seen[strings.ToLower(name)] {
			return "", errors.New("DNS CNAME循环")
		}
		seen[strings.ToLower(name)] = true
		q := new(dns.Msg)
		q.SetQuestion(name, typ)
		answer, _, err := (&dns.Client{Net: network, Timeout: 5 * time.Second}).ExchangeContext(ctx, q, server)
		if err == nil && answer.Truncated {
			answer, _, err = (&dns.Client{Net: tcp, Timeout: 5 * time.Second}).ExchangeContext(ctx, q, server)
		}
		if err != nil {
			return "", errors.New("DNS查询失败")
		}
		if answer.Rcode != dns.RcodeSuccess {
			return "", fmt.Errorf("DNS返回状态%d", answer.Rcode)
		}
		// Accept only the requested name or a CNAME linked from that name.
		current := name
		for link := 0; link < 8; link++ {
			next := ""
			for _, rr := range answer.Answer {
				if !strings.EqualFold(rr.Header().Name, current) {
					continue
				}
				var candidate string
				switch v := rr.(type) {
				case *dns.A:
					if typ == dns.TypeA {
						candidate = v.A.String()
					}
				case *dns.AAAA:
					if typ == dns.TypeAAAA {
						candidate = v.AAAA.String()
					}
				case *dns.TXT:
					if typ == dns.TypeTXT {
						candidate = strings.Join(v.Txt, "")
					}
				case *dns.CNAME:
					next = v.Target
				}
				if candidate != "" {
					if ip, e := validateIP(candidate, v6); e == nil {
						return ip, nil
					}
				}
			}
			if next == "" {
				break
			}
			current = next
		}
		if strings.EqualFold(current, name) {
			return "", errors.New("DNS未返回匹配的IP地址")
		}
		name = current
	}
	return "", errors.New("DNS CNAME链超过8层")
}
