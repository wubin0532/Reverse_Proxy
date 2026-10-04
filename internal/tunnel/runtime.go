package tunnel

import (
	"andey-proxy/internal/config"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

type Runtime struct {
	Source               string                  `json:"source"`
	GOOS                 string                  `json:"goos"`
	GOARCH               string                  `json:"goarch"`
	DownloadSupported    bool                    `json:"downloadSupported"`
	Operation            *config.TunnelOperation `json:"operation,omitempty"`
	Path                 string                  `json:"path"`
	Version              string                  `json:"version"`
	Compatible           bool                    `json:"compatible"`
	VerifiedArchitecture bool                    `json:"verifiedArchitecture"`
	Message              string                  `json:"message,omitempty"`
}

var versionPattern = regexp.MustCompile(`\b(\d{4})\.(\d+)\.(\d+)\b`)

type versionOutput struct {
	buf      bytes.Buffer
	oversize bool
}

func (v *versionOutput) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > 4096-v.buf.Len() {
		v.oversize = true
		p = p[:4096-v.buf.Len()]
	}
	_, _ = v.buf.Write(p)
	return n, nil
}

func DetectRuntime() Runtime {
	path, err := exec.LookPath("cloudflared")
	if err != nil {
		return detectRuntimePath("", "missing")
	}
	return detectRuntimePath(path, "system")
}

func detectManagedRuntime(dir string) Runtime {
	if info, err := os.Lstat(runtimeDir(dir)); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		r := detectRuntimePath("", "managed")
		r.Message = "受管理程序目录不安全"
		return r
	}
	path := managedRuntimePath(dir)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return DetectRuntime()
	}
	if err != nil || !info.Mode().IsRegular() {
		r := detectRuntimePath("", "managed")
		r.Message = "受管理程序不可用，请重新安装"
		return r
	}
	return detectRuntimePath(path, "managed")
}

func detectRuntimePath(path, source string) Runtime {
	r := Runtime{Source: source, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, DownloadSupported: runtimeAsset(runtime.GOOS, runtime.GOARCH) != "", VerifiedArchitecture: runtime.GOOS == "linux" && (runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" || runtime.GOARCH == "arm")}
	if path == "" {
		r.Message = "请安装 cloudflared 2025.4.0 或更新版本"
		return r
	}
	r.Path = path
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	out := &versionOutput{}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil || out.oversize {
		r.Message = "无法检测 cloudflared 版本"
		return r
	}
	match := versionPattern.FindStringSubmatch(out.buf.String())
	if match == nil {
		r.Message = "无法识别 cloudflared 版本"
		return r
	}
	r.Version = match[0]
	year, _ := strconv.Atoi(match[1])
	month, _ := strconv.Atoi(match[2])
	r.Compatible = year > 2025 || year == 2025 && month >= 4
	if !r.Compatible {
		r.Message = "cloudflared 版本过低，需要 2025.4.0 或更新版本"
	} else if !r.VerifiedArchitecture {
		r.Message = fmt.Sprintf("%s/%s 未经过 Tunnel 运行验证", runtime.GOOS, runtime.GOARCH)
	}
	return r
}
