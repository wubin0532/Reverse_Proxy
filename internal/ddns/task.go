package ddns

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
)

const defaultForceInterval = 6 * 60 * 60

// Legacy tasks stay in their original format on disk. Views expose virtual
// records with deterministic IDs; explicit record editing converts that task.
func effectiveRecords(task config.DDNSTask) []config.DDNSRecord {
	if len(task.Records) > 0 {
		return append([]config.DDNSRecord(nil), task.Records...)
	}
	typ := "A"
	if task.IPType == "ipv6" {
		typ = "AAAA"
	}
	records := make([]config.DDNSRecord, 0, len(task.Domains))
	for _, domain := range task.Domains {
		sum := sha256.Sum256([]byte(typ + "|" + domain))
		records = append(records, config.DDNSRecord{ID: "legacy-" + hex.EncodeToString(sum[:8]), Enabled: true, Domain: domain, Type: typ, TTL: task.TTL})
	}
	return records
}

func sourceTask(task config.DDNSTask, typ string) config.DDNSTask {
	out := task
	source := task.IPv4Source
	out.IPType = "ipv4"
	if typ == "AAAA" {
		source = task.IPv6Source
		out.IPType = "ipv6"
	}
	if source != nil {
		out.IPSource, out.Interface, out.APIURL = source.Source, source.Interface, source.APIURL
	} else if out.IPType != task.IPType || task.IPSource == "" {
		out.IPSource, out.Interface, out.APIURL = "interface", "auto", ""
	}
	return out
}

func taskRevision(task config.DDNSTask) string {
	data, _ := json.Marshal(task)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Credentials participate only in this private cache key, never in API output.
func targetFingerprint(task config.DDNSTask, provider config.DNSProviderConf) string {
	data, _ := json.Marshal(struct {
		Task     config.DDNSTask
		Provider config.DNSProviderConf
	}{task, provider})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func providerExistsLocked(cfg *config.Config, id string) bool {
	for _, provider := range cfg.Providers {
		if provider.ID == id {
			return true
		}
	}
	return false
}

func normalizeDomain(domain string) (string, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if strings.Contains(domain, "*") && (!strings.HasPrefix(domain, "*.") || strings.Contains(domain[2:], "*")) {
		return "", fmt.Errorf("通配符只能是完整前缀（如 *.example.com）")
	}
	if _, _, err := splitDomain(domain); err != nil {
		return "", err
	}
	return domain, nil
}

func validateSource(task config.DDNSTask, typ string) error {
	t := sourceTask(task, typ)
	s := config.IPSourceConfig{Source: t.IPSource, Interface: t.Interface, APIURL: t.APIURL}
	if typ == "A" && task.IPv4Source != nil {
		s = *task.IPv4Source
	}
	if typ == "AAAA" && task.IPv6Source != nil {
		s = *task.IPv6Source
	}
	return config.ValidateIPSource(s, typ == "AAAA")
}

func validateTaskShape(task *config.DDNSTask) (int, string) {
	task.Name = strings.TrimSpace(task.Name)
	if task.Name == "" {
		return 400, "任务名称不能为空"
	}
	if task.Interval <= 0 {
		task.Interval = 300
	}
	if task.Interval < 30 || task.Interval > 86400 {
		return 400, "检测间隔必须为 30 到 86400 秒"
	}
	if task.ForceInterval < 0 || task.ForceInterval > 604800 || (task.ForceInterval > 0 && task.ForceInterval < 30) {
		return 400, "强制核查间隔必须为 30 到 604800 秒，0 使用默认 6 小时"
	}
	if len(task.Records) > 0 {
		if len(task.Records) > 100 {
			return 400, "记录数量必须为 1 到 100 个"
		}
		seenIDs, seenTargets := map[string]bool{}, map[string]bool{}
		for i := range task.Records {
			record := &task.Records[i]
			if record.ID == "" {
				record.ID = ids.New()
			}
			if seenIDs[record.ID] {
				return 400, "记录 ID 重复"
			}
			seenIDs[record.ID] = true
			record.Name = strings.TrimSpace(record.Name)
			domain, err := normalizeDomain(record.Domain)
			if err != nil {
				return 400, err.Error()
			}
			record.Domain = domain
			if err := record.NormalizeStaticValue(); err != nil {
				return 400, err.Error()
			}
			target := domain + "|" + record.Type
			if seenTargets[target] {
				return 400, "同一域名和类型不能重复配置"
			}
			seenTargets[target] = true
			if record.TTL < 0 || record.TTL > 86400 {
				return 400, "TTL 必须为 0 到 86400 秒"
			}
			if record.Type == "A" || record.Type == "AAAA" {
				if err := validateSource(*task, record.Type); err != nil {
					return 400, err.Error()
				}
			}
		}
	} else {
		if len(task.Domains) == 0 || len(task.Domains) > 100 {
			return 400, "域名数量必须为 1 到 100 个"
		}
		seen := map[string]bool{}
		for i, raw := range task.Domains {
			domain, err := normalizeDomain(raw)
			if err != nil {
				return 400, err.Error()
			}
			if seen[domain] {
				return 400, "域名不能重复配置"
			}
			seen[domain] = true
			task.Domains[i] = domain
		}
		if task.IPType != "ipv4" && task.IPType != "ipv6" {
			return 400, "IP 类型必须是 ipv4 或 ipv6"
		}
		if task.TTL < 0 || task.TTL > 86400 {
			return 400, "TTL 必须为 0 到 86400 秒"
		}
		if task.IPSource != "interface" && task.IPSource != "api" {
			return 400, "IP 来源必须是 interface 或 api"
		}
		if task.IPSource == "api" {
			u, err := validateHTTPURL(task.APIURL, true)
			if err != nil {
				return 400, "IP 查询地址无效: " + err.Error()
			}
			task.APIURL = u.String()
		}
	}
	return 0, ""
}
