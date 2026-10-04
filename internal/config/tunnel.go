package config

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

// Tunnel credentials are write-only through the management API.
type TunnelAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"accountId"`
	Token     string `json:"token"`
}

type TunnelInstance struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Source        string        `json:"source"` // managed / imported
	AccountRef    string        `json:"accountRef"`
	TunnelID      string        `json:"tunnelId"`
	Token         string        `json:"token"`
	Enabled       bool          `json:"enabled"`
	Protocol      string        `json:"protocol"`
	IPVersion     string        `json:"ipVersion"`
	Routes        []TunnelRoute `json:"routes"`
	RetiredRoutes []TunnelRoute `json:"retiredRoutes,omitempty"`
}

type TunnelRoute struct {
	ID            string `json:"id"`
	Hostname      string `json:"hostname"`
	ZoneID        string `json:"zoneId"`
	Target        string `json:"target"` // site / url
	SiteID        string `json:"siteId,omitempty"`
	URL           string `json:"url,omitempty"`
	SkipTLSVerify bool   `json:"skipTlsVerify,omitempty"`
	// Applied fields belong to the synchronizer, never to API input.
	AppliedHostname      string `json:"appliedHostname,omitempty"`
	AppliedService       string `json:"appliedService,omitempty"`
	AppliedSkipTLSVerify bool   `json:"appliedSkipTlsVerify,omitempty"`
	PendingHostname      string `json:"pendingHostname,omitempty"`
	PendingService       string `json:"pendingService,omitempty"`
	DNSRecordID          string `json:"dnsRecordId,omitempty"`
	DNSOwned             bool   `json:"dnsOwned,omitempty"`
}

type TunnelOperation struct {
	Version         string `json:"version,omitempty"`
	DownloadedBytes int64  `json:"downloadedBytes,omitempty"`
	TotalBytes      int64  `json:"totalBytes,omitempty"`
	ID              string `json:"id"`
	InstanceID      string `json:"instanceId"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	Stage           string `json:"stage"`
	Error           string `json:"error,omitempty"`
	RemoteID        string `json:"remoteId,omitempty"`
	StartedAt       string `json:"startedAt"`
}

// TunnelReferencedSite must be called with the configuration lock held.
func (c *Config) TunnelReferencedSite(id string) bool {
	return len(c.TunnelSiteBindings(false)[id]) > 0
}

func (st *StateStore) TunnelOperation(id string) (TunnelOperation, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	op, ok := st.state.TunnelOperations[id]
	return op, ok
}

func (st *StateStore) LatestTunnelOperation(id string) *TunnelOperation {
	st.mu.Lock()
	defer st.mu.Unlock()
	var latest *TunnelOperation
	for _, op := range st.state.TunnelOperations {
		if op.InstanceID == id && (latest == nil || op.StartedAt > latest.StartedAt) {
			copy := op
			latest = &copy
		}
	}
	return latest
}

// TunnelSocketPath never contains user-supplied path segments.
func TunnelSocketPath(dir, siteID string) string {
	root := sha256.Sum256([]byte(dir))
	site := sha256.Sum256([]byte(siteID))
	return filepath.Join("/tmp", fmt.Sprintf("ap-tunnel-%x", root[:8]), fmt.Sprintf("%x.sock", site[:10]))
}

// Caller holds Config's read/write lock. Only known local socket paths are
// recognized; draft, applied, pending and retired entries share one lifecycle.
func (c *Config) TunnelSiteBindings(enabledOnly bool) map[string]map[string]bool {
	return c.tunnelSiteBindings(c.Tunnels, enabledOnly)
}

func (c *Config) TunnelInstanceSiteBindings(inst TunnelInstance) map[string]map[string]bool {
	return c.tunnelSiteBindings([]TunnelInstance{inst}, false)
}

func (c *Config) tunnelSiteBindings(instances []TunnelInstance, enabledOnly bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool)
	add := func(siteID, host string) {
		if host == "" {
			return
		}
		if out[siteID] == nil {
			out[siteID] = make(map[string]bool)
		}
		out[siteID][strings.ToLower(host)] = true
	}
	addService := func(host, service string) {
		if host == "" || !strings.HasPrefix(service, "unix:") {
			return
		}
		for _, site := range c.Sites {
			if service == "unix:"+TunnelSocketPath(c.Dir(), site.ID) {
				add(site.ID, host)
			}
		}
	}
	for _, inst := range instances {
		if enabledOnly && !inst.Enabled {
			continue
		}
		for _, r := range inst.Routes {
			if r.Target == "site" {
				add(r.SiteID, r.Hostname)
			}
		}
		for _, r := range append(append([]TunnelRoute{}, inst.Routes...), inst.RetiredRoutes...) {
			addService(r.AppliedHostname, r.AppliedService)
			addService(r.PendingHostname, r.PendingService)
		}
	}
	return out
}
