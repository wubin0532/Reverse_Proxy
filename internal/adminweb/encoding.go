package adminweb

import (
	"strconv"
	"strings"
)

func negotiateEncoding(header string, br, gzip, ranged bool) (string, bool) {
	weights := map[string]float64{}
	for _, field := range strings.Split(header, ",") {
		parts := strings.Split(field, ";")
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		if name == "" {
			continue
		}
		quality := 1.0
		for _, param := range parts[1:] {
			key, value, found := strings.Cut(strings.TrimSpace(param), "=")
			if found && strings.EqualFold(key, "q") {
				q, err := strconv.ParseFloat(value, 64)
				if err != nil || q < 0 || q > 1 {
					quality = 0
				} else {
					quality = q
				}
			}
		}
		weights[name] = quality
	}
	quality := func(name string) float64 {
		if q, ok := weights[name]; ok {
			return q
		}
		if q, ok := weights["*"]; ok {
			if name != "identity" || q == 0 {
				return q
			}
		}
		if name == "identity" {
			return 1
		}
		return 0
	}
	// Byte ranges use the original representation whenever it is acceptable.
	if ranged && quality("identity") > 0 {
		return "identity", true
	}
	chosen, best := "", 0.0
	for _, candidate := range []struct {
		name      string
		available bool
	}{{"br", br}, {"gzip", gzip}, {"identity", true}} {
		if q := quality(candidate.name); candidate.available && q > best {
			chosen, best = candidate.name, q
		}
	}
	return chosen, best > 0
}
