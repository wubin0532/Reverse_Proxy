package acme

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
)

type MappingResult struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	UpdatedAt time.Time `json:"updatedAt"`
	Error     string    `json:"error,omitempty"`
}

var mappingGeneration = regexp.MustCompile(`^v-[a-f0-9]{32}$`)
var safeExportID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// Export within a rooted tree. Consumers opening two separate files must first
// resolve current once and use that version directory. A single bundle.pem open
// is also available; two independent current/... opens can straddle an update.
func exportPair(root *os.Root, base string, certPEM, keyPEM []byte) error {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return err
	}
	generation := "v-" + ids.New()
	dir := filepath.Join(base, "versions", generation)
	if err := root.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			root.RemoveAll(dir)
		}
	}()
	bundle := append(append(append([]byte(nil), certPEM...), '\n'), keyPEM...)
	for name, data := range map[string][]byte{"fullchain.pem": certPEM, "privkey.pem": keyPEM, "bundle.pem": bundle} {
		f, err := root.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
	}
	old, _ := root.Readlink(filepath.Join(base, "current"))
	temporary := filepath.Join(base, ".current-"+ids.New())
	defer root.Remove(temporary)
	if err := root.Symlink(filepath.Join("versions", generation), temporary); err != nil {
		return err
	}
	if err := root.Rename(temporary, filepath.Join(base, "current")); err != nil {
		return err
	}
	published = true
	if d, err := root.Open(base); err == nil {
		err = d.Sync()
		d.Close()
		if err != nil {
			return err
		}
	}
	// Bound retained material to current and previous generations. Only remove
	// program-generated names, never arbitrary files supplied in this directory.
	d, err := root.Open(filepath.Join(base, "versions"))
	if err != nil {
		return err
	}
	entries, err := d.ReadDir(-1)
	d.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if mappingGeneration.MatchString(name) && name != generation && filepath.Join("versions", name) != old {
			if err := root.RemoveAll(filepath.Join(base, "versions", name)); err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Manager) SyncMappings(ctx context.Context, id string) ([]MappingResult, error) {
	m.mappingMu.Lock()
	defer m.mappingMu.Unlock()
	m.cfg.RLock()
	cert, ok := m.findCertLocked(id)
	if !ok {
		m.cfg.RUnlock()
		return nil, fmt.Errorf("证书不存在")
	}
	if len(cert.Mappings) == 0 {
		m.cfg.RUnlock()
		return []MappingResult{}, nil
	}
	if !safeExportID.MatchString(id) {
		m.cfg.RUnlock()
		return nil, fmt.Errorf("证书ID不可作为导出目录")
	}
	if err := cert.ValidateAdvanced(); err != nil {
		m.cfg.RUnlock()
		return nil, err
	}
	m.pairMu.RLock()
	certFile, keyFile := m.certPath(&cert)
	certPEM, err := os.ReadFile(certFile)
	var keyPEM []byte
	if err == nil {
		keyPEM, err = os.ReadFile(keyFile)
	}
	m.pairMu.RUnlock()
	if err != nil {
		m.cfg.RUnlock()
		return nil, fmt.Errorf("请先导入或申请有效证书")
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil || !coversDomains(&pair, cert.Domains) {
		m.cfg.RUnlock()
		return nil, fmt.Errorf("证书对或域名覆盖无效")
	}
	root, err := os.OpenRoot(m.cfg.Dir())
	if err != nil {
		m.cfg.RUnlock()
		return nil, err
	}
	defer root.Close()
	results := make([]MappingResult, 0, len(cert.Mappings))
	var firstErr error
	for _, mapping := range cert.Mappings {
		result := MappingResult{Name: mapping.Name, Path: filepath.Join(m.cfg.Dir(), "exports", id, mapping.Name, "current"), UpdatedAt: time.Now().UTC()}
		err = ctx.Err()
		if err == nil {
			err = exportPair(root, filepath.Join("exports", id, mapping.Name), certPEM, keyPEM)
		}
		if err != nil {
			result.Error = "映射文件写入失败"
			if firstErr == nil {
				firstErr = err
			}
		}
		results = append(results, result)
	}
	m.cfg.RUnlock()
	// Fixed adapters only. Configuration is not held during external reloads.
	for i, mapping := range cert.Mappings {
		if mapping.ReloadService == "" || results[i].Error != "" {
			continue
		}
		deadline, cancel := context.WithTimeout(ctx, 10*time.Second)
		cmd := exec.CommandContext(deadline, "/etc/init.d/"+mapping.ReloadService, "reload")
		err := cmd.Run()
		cancel()
		if err != nil {
			results[i].Error = "文件已映射，服务重载失败"
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	m.mappingResults.Store(id, results)
	if firstErr != nil {
		return results, fmt.Errorf("部分映射未完成")
	}
	return results, nil
}
func (m *Manager) findCertLocked(id string) (config.CertConf, bool) {
	for _, cert := range m.cfg.Certs {
		if cert.ID == id {
			cert.Domains = append([]string(nil), cert.Domains...)
			cert.Mappings = append([]config.CertificateMapping(nil), cert.Mappings...)
			return cert, true
		}
	}
	return config.CertConf{}, false
}
func (m *Manager) MappingResults(id string) []MappingResult {
	if value, ok := m.mappingResults.Load(id); ok {
		return append([]MappingResult(nil), value.([]MappingResult)...)
	}
	return []MappingResult{}
}

func (m *Manager) autoSyncMappings(id string) {
	ctx, cancel := context.WithTimeout(m.ctx, 60*time.Second)
	defer cancel()
	if _, err := m.SyncMappings(ctx, id); err != nil {
		log.Printf("[acme] 证书 %s 映射未完成，详情见映射状态", id)
	}
}
