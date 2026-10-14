package config

import (
	"fmt"
	"net"
	"strings"
	"unicode/utf8"
)

// NormalizeStaticValue is shared by API mutations, configuration imports and execution.
// TXT is literal text: do not strip spaces, quotes or change its case.
func (r *DDNSRecord) NormalizeStaticValue() error {
	switch r.Type {
	case "A", "AAAA":
		r.Value = ""
	case "CNAME":
		r.Value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(r.Value), "."))
		if net.ParseIP(r.Value) != nil || !validDNSName(r.Value) || !strings.Contains(r.Value, ".") || strings.EqualFold(r.Value, strings.TrimSuffix(r.Domain, ".")) {
			return fmt.Errorf("CNAME目标必须是其它完整域名，不能填写IP、URL或自身域名")
		}
	case "TXT":
		if r.Value == "" || len(r.Value) > 2048 || !utf8.ValidString(r.Value) || strings.ContainsAny(r.Value, "\x00\r\n") {
			return fmt.Errorf("TXT值不能为空，最多2048字节且不能包含换行或NUL")
		}
	default:
		return fmt.Errorf("记录类型必须是 A、AAAA、CNAME 或 TXT")
	}
	return nil
}

func validDNSName(name string) bool {
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if ch != '-' && !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') {
				return false
			}
		}
	}
	return true
}
