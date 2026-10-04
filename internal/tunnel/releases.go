package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"andey-proxy/internal/ids"
)

const maxRuntimeSize int64 = 80 << 20
const officialReleaseAPI = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"

var releaseVersion = regexp.MustCompile(`^20[0-9]{2}\.[0-9]{1,2}\.[0-9]+$`)
var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Release struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Asset     string `json:"asset"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	CheckedAt string `json:"checkedAt"`
	URL       string `json:"-"`
}

type releaseCatalog struct {
	mu                 sync.Mutex
	client             *http.Client
	endpoint           string
	cached             Release
	checked, attempted time.Time
	lastError          error
}

func newReleaseCatalog() *releaseCatalog {
	return &releaseCatalog{endpoint: officialReleaseAPI, client: &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 4 || !officialDownloadURL(r.URL) {
				return fmt.Errorf("下载重定向来源不受信任")
			}
			return nil
		},
	}}
}

func runtimeAsset(goos, arch string) string {
	if goos != "linux" {
		return ""
	}
	switch arch {
	case "amd64", "arm64", "arm":
		return "cloudflared-linux-" + arch
	}
	return ""
}

func officialDownloadURL(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}

func (c *releaseCatalog) latest(ctx context.Context, force bool) (Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	asset := runtimeAsset(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		return Release{}, fmt.Errorf("此系统架构不支持官方 cloudflared 自动下载")
	}
	if !force && c.cached.ID != "" && time.Since(c.checked) < time.Hour {
		return c.cached, nil
	}
	if time.Since(c.attempted) < 10*time.Second {
		if c.lastError != nil {
			return c.cached, c.lastError
		}
		return c.cached, nil
	}
	c.attempted = time.Now()
	release, err := c.fetch(ctx, asset)
	c.lastError = err
	if err != nil {
		return c.cached, err
	}
	if release.Version == c.cached.Version && release.SHA256 == c.cached.SHA256 {
		release.ID = c.cached.ID
	}
	c.cached = release
	c.checked = time.Now()
	return release, nil
}

func (c *releaseCatalog) fetch(ctx context.Context, asset string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("版本查询请求无效")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "andey-proxy")
	resp, err := c.client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("无法连接官方发布源，请稍后重试或手动安装")
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return Release{}, fmt.Errorf("官方发布源访问受限或限流，请稍后重试")
	}
	if resp.StatusCode != 200 {
		return Release{}, fmt.Errorf("官方发布源返回 HTTP %d", resp.StatusCode)
	}
	var meta struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&meta) != nil {
		return Release{}, fmt.Errorf("官方版本信息格式无效")
	}
	version := strings.TrimPrefix(meta.Tag, "v")
	if !releaseVersion.MatchString(version) || meta.Draft || meta.Prerelease {
		return Release{}, fmt.Errorf("未找到有效的官方稳定版本")
	}
	for _, a := range meta.Assets {
		if a.Name != asset {
			continue
		}
		digest := strings.TrimPrefix(a.Digest, "sha256:")
		if a.Size <= 0 || a.Size > maxRuntimeSize || !sha256Pattern.MatchString(digest) {
			return Release{}, fmt.Errorf("官方文件大小或 SHA-256 校验信息无效")
		}
		return Release{ID: ids.New(), Version: version, Asset: asset, Size: a.Size, SHA256: digest, CheckedAt: time.Now().UTC().Format(time.RFC3339), URL: "https://github.com/cloudflare/cloudflared/releases/download/" + meta.Tag + "/" + asset}, nil
	}
	return Release{}, fmt.Errorf("官方稳定版本没有匹配当前系统的二进制文件")
}

func (c *releaseCatalog) selected(id string) (Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id == "" || c.cached.ID != id || time.Since(c.checked) > time.Hour {
		return Release{}, fmt.Errorf("版本信息已过期，请重新检查版本")
	}
	return c.cached, nil
}
