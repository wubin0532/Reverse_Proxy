package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// apiBase GitHub API 地址，测试中可替换为 httptest 服务。
	defaultAPIBase = "https://api.github.com"
	repoPath       = "/repos/wubin0532/Reverse_Proxy"

	// maxRunSize .run 包下载上限 100MB。
	maxRunSize = 100 << 20

	// initdPath OpenWrt procd 服务脚本，存在则用它重启。
	initdPath = "/etc/init.d/andey-proxy"
)

// State 升级状态机状态。
type State string

const (
	StateIdle        State = "idle"        // 空闲
	StateDownloading State = "downloading" // 下载安装包中
	StateInstalling  State = "installing"  // 解包、校验、替换二进制中
	StateRestarting  State = "restarting"  // 触发服务重启中
	StateDone        State = "done"        // 完成（含需手动重启的情况）
	StateFailed      State = "failed"      // 失败，error 字段有原因
)

// busy 报告状态是否处于升级进行中（用于防并发）。
func (s State) busy() bool {
	return s == StateDownloading || s == StateInstalling || s == StateRestarting
}

// Status 升级状态快照。
type Status struct {
	State   State  `json:"state"`
	Version string `json:"version,omitempty"` // 目标版本
	Error   string `json:"error,omitempty"`   // 失败原因
	Note    string `json:"note,omitempty"`    // 附加提示（如需手动重启）
}

// ghRelease GitHub release API 返回结构（仅取需要的字段）。
type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// CheckResult 版本检查结果。
type CheckResult struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	HasUpdate  bool   `json:"hasUpdate"`
	ReleaseURL string `json:"releaseUrl"`
}

// Manager 在线升级管理器，持有状态机并执行下载、安装、重启。
type Manager struct {
	version string // 当前版本（main 注入）
	started time.Time

	mu      sync.Mutex
	state   State
	target  string
	errMsg  string
	note    string

	// 以下字段可在测试中替换
	client  *http.Client // GitHub API / 元数据请求客户端（15s 超时）
	dlClient *http.Client // 大文件下载客户端（5 分钟超时）
	apiBase string
}

// NewManager 创建升级管理器，version 为当前版本号。
func NewManager(version string) *Manager {
	return &Manager{
		version:  version,
		started:  time.Now(),
		state:    StateIdle,
		client:   &http.Client{Timeout: 15 * time.Second},
		dlClient: &http.Client{Timeout: 5 * time.Minute},
		apiBase:  defaultAPIBase,
	}
}

// Version 返回当前版本号。
func (m *Manager) Version() string { return m.version }

// Uptime 返回进程启动至今的秒数。
func (m *Manager) Uptime() int64 { return int64(time.Since(m.started).Seconds()) }

// Status 返回当前升级状态快照。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{State: m.state, Version: m.target, Error: m.errMsg, Note: m.note}
}

// setState 推进状态机。
func (m *Manager) setState(s State) {
	m.mu.Lock()
	m.state = s
	m.mu.Unlock()
}

// fail 将状态机置为 failed 并记录原因。
func (m *Manager) fail(err error) {
	m.mu.Lock()
	m.state = StateFailed
	m.errMsg = err.Error()
	m.mu.Unlock()
}

// Check 查询 GitHub 最新 release 并与当前版本比较。
func (m *Manager) Check(ctx context.Context) (*CheckResult, error) {
	rel, err := m.fetchRelease(ctx, "")
	if err != nil {
		return nil, err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	return &CheckResult{
		Current:    m.version,
		Latest:     latest,
		HasUpdate:  hasUpdate(m.version, rel.TagName),
		ReleaseURL: rel.HTMLURL,
	}, nil
}

// Start 尝试启动一次升级，已有升级进行中返回 false；成功则后台异步执行。
func (m *Manager) Start(version string) bool {
	m.mu.Lock()
	if m.state.busy() {
		m.mu.Unlock()
		return false
	}
	m.state = StateDownloading
	m.target = version
	m.errMsg = ""
	m.note = ""
	m.mu.Unlock()

	go m.run(version)
	return true
}

// fetchRelease 拉取 release 信息；tag 为空取 latest，否则按 tag 查询。
func (m *Manager) fetchRelease(ctx context.Context, tag string) (*ghRelease, error) {
	url := m.apiBase + repoPath + "/releases/latest"
	if tag != "" {
		url = m.apiBase + repoPath + "/releases/tags/" + normalizeTag(tag)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "andey-proxy/"+m.version)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("无法连接 GitHub，请检查网络或稍后重试: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("未找到版本 %s 的发布", tag)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API 返回错误（HTTP %d），请稍后重试", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("解析 GitHub 响应失败: %w", err)
	}
	return &rel, nil
}

// run 升级主流程：下载 → 校验 → 解包 → 替换 → 重启。
func (m *Manager) run(version string) {
	ctx := context.Background()

	// 解析目标 release（空则 latest）
	rel, err := m.fetchRelease(ctx, version)
	if err != nil {
		m.fail(err)
		return
	}
	ver := strings.TrimPrefix(rel.TagName, "v")
	m.mu.Lock()
	m.target = ver
	m.mu.Unlock()

	// 匹配当前架构的 .run 资产
	suffix, ok := archSuffix(runtime.GOARCH)
	if !ok {
		m.fail(fmt.Errorf("当前架构 %s 不支持在线升级", runtime.GOARCH))
		return
	}
	assetName := fmt.Sprintf("andey-proxy_%s_linux_%s.run", ver, suffix)
	var runURL, sumURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case assetName:
			runURL = a.BrowserDownloadURL
		case "checksums.txt":
			sumURL = a.BrowserDownloadURL
		}
	}
	if runURL == "" {
		m.fail(fmt.Errorf("版本 %s 中未找到安装包 %s", ver, assetName))
		return
	}
	if sumURL == "" {
		m.fail(errors.New("release 中未找到 checksums.txt，无法校验安装包"))
		return
	}

	// 下载校验和并取目标包对应行
	expect, err := m.fetchChecksum(ctx, sumURL, assetName)
	if err != nil {
		m.fail(err)
		return
	}

	// 下载 .run 到 /tmp，同时计算 SHA256
	runPath, err := m.download(ctx, runURL, expect)
	if err != nil {
		m.fail(err)
		return
	}
	defer os.Remove(runPath)

	// 解包并安装
	m.setState(StateInstalling)
	dir, err := extractPayload(runPath)
	if err != nil {
		m.fail(err)
		return
	}
	defer os.RemoveAll(dir)

	if err := install(filepath.Join(dir, binaryName)); err != nil {
		m.fail(err)
		return
	}

	// 重启：优先 procd 服务脚本
	if _, err := os.Stat(initdPath); err == nil {
		m.setState(StateRestarting)
		if err := exec.Command(initdPath, "restart").Start(); err != nil {
			m.mu.Lock()
			m.state = StateDone
			m.note = "新版本已安装，但自动重启失败，请手动重启服务"
			m.mu.Unlock()
		}
		// 重启命令发出后 procd 会拉起新进程，本进程随即退出
		return
	}
	m.mu.Lock()
	m.state = StateDone
	m.note = "升级完成，请手动重启进程以运行新版本"
	m.mu.Unlock()
}

// fetchChecksum 下载 checksums.txt 并解析出指定文件的 SHA256。
func (m *Manager) fetchChecksum(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载校验文件失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载校验文件失败（HTTP %d）", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取校验文件失败: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("校验文件中未找到 %s 的 SHA256", name)
}

// download 流式下载到 /tmp 临时文件，限制 100MB 并校验 SHA256，成功后返回文件路径。
func (m *Manager) download(ctx context.Context, url, expectSum string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := m.dlClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("下载安装包失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载安装包失败（HTTP %d）", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "andey-proxy-*.run")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxRunSize+1))
	if err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("下载安装包失败: %w", err)
	}
	if n > maxRunSize {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", errors.New("安装包超过 100MB 上限，已中止")
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != expectSum {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("安装包 SHA256 校验失败（期望 %s，实际 %s）", expectSum, sum)
	}
	return tmp.Name(), nil
}

// install 备份当前二进制为 <路径>.bak 后用新二进制覆盖；失败时回滚。
func install(newBin string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前程序路径失败: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	backup := exe + ".bak"

	// 备份当前二进制
	if err := copyFile(exe, backup, 0o755); err != nil {
		return fmt.Errorf("备份当前程序失败: %w", err)
	}

	// 覆盖安装（rename 失败说明跨文件系统，退化为复制）
	if err := os.Rename(newBin, exe); err != nil {
		if err := copyFile(newBin, exe, 0o755); err != nil {
			// 回滚备份
			if rbErr := os.Rename(backup, exe); rbErr != nil {
				return fmt.Errorf("写入新程序失败: %w；且回滚失败: %v", err, rbErr)
			}
			return fmt.Errorf("写入新程序失败: %w（已回滚到旧版本）", err)
		}
	}
	if err := os.Chmod(exe, 0o755); err != nil {
		return fmt.Errorf("设置执行权限失败: %w", err)
	}
	return nil
}

// copyFile 复制文件内容并设置权限。
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
