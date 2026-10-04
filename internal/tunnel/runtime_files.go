package tunnel

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

func runtimeDir(dir string) string         { return filepath.Join(dir, "runtime") }
func managedRuntimePath(dir string) string { return filepath.Join(runtimeDir(dir), "cloudflared") }

func prepareRuntimeDir(dir string) error {
	path := runtimeDir(dir)
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("无法创建程序目录")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("程序目录不安全")
	}
	return os.Chmod(path, 0o700)
}

func (c *releaseCatalog) download(ctx context.Context, release Release, path string, progress func(int64)) error {
	req, err := http.NewRequestWithContext(ctx, "GET", release.URL, nil)
	if err != nil || !officialDownloadURL(req.URL) {
		return fmt.Errorf("下载地址不受信任")
	}
	req.Header.Set("User-Agent", "andey-proxy")
	// The metadata request has a short timeout; streaming uses the job's deadline.
	client := *c.client
	client.Timeout = 0
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("连接官方下载失败，请重试")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("官方下载返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != release.Size {
		return fmt.Errorf("下载文件大小与官方记录不一致")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("无法创建下载文件")
	}
	hash := sha256.New()
	counter := &downloadCounter{fn: progress}
	n, copyErr := io.Copy(io.MultiWriter(file, hash, counter), io.LimitReader(resp.Body, maxRuntimeSize+1))
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("下载中断或写入失败")
	}
	if n != release.Size || n > maxRuntimeSize {
		return fmt.Errorf("下载文件不完整或超过 80 MiB")
	}
	if hex.EncodeToString(hash.Sum(nil)) != release.SHA256 {
		return fmt.Errorf("SHA-256 校验失败，未安装下载文件")
	}
	return nil
}

type downloadCounter struct {
	n  int64
	fn func(int64)
}

func (c *downloadCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	c.fn(c.n)
	return len(p), nil
}

func verifyRuntimeBinary(path, version string) error {
	file, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("下载文件不是有效的 Linux 程序")
	}
	defer file.Close()
	machines := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64, "arm": elf.EM_ARM}
	machine, ok := machines[runtime.GOARCH]
	if !ok || file.Machine != machine || file.Data != elf.ELFDATA2LSB {
		return fmt.Errorf("下载程序与当前系统架构不匹配")
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("无法设置程序权限")
	}
	rt := detectRuntimePath(path, "managed")
	if !rt.Compatible || rt.Version != version {
		return fmt.Errorf("下载程序版本检查失败")
	}
	return nil
}

type runtimeJournal struct {
	Schema     int  `json:"schema"`
	HadManaged bool `json:"hadManaged"`
	Committed  bool `json:"committed"`
}

func saveRuntimeJournal(dir string, j runtimeJournal) error {
	j.Schema = 1
	data, _ := json.Marshal(j)
	path := filepath.Join(runtimeDir(dir), "install-state.json")
	f, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err == nil {
		err = syncRuntimeDir(dir)
	}
	return err
}

func syncRuntimeDir(dir string) error {
	f, err := os.Open(runtimeDir(dir))
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func recoverRuntime(dir string) error {
	root := runtimeDir(dir)
	if _, err := os.Lstat(root); os.IsNotExist(err) {
		return nil
	}
	if err := prepareRuntimeDir(dir); err != nil {
		return err
	}
	path := filepath.Join(root, "install-state.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cleanupRuntimeTemps(root)
	}
	if err != nil {
		return fmt.Errorf("无法读取程序安装恢复记录")
	}
	var journal runtimeJournal
	if len(data) > 4096 || json.Unmarshal(data, &journal) != nil || journal.Schema != 1 {
		return fmt.Errorf("程序安装恢复记录损坏")
	}
	target := managedRuntimePath(dir)
	previous := target + ".previous"
	if !journal.Committed {
		if journal.HadManaged {
			if _, err = os.Lstat(previous); err == nil {
				err = os.Rename(previous, target)
			} else if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = os.Remove(target)
			if os.IsNotExist(err) {
				err = nil
			}
		}
		if err != nil {
			return fmt.Errorf("无法恢复安装前的程序")
		}
	}
	if err = os.Remove(previous); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("无法清理旧程序备份")
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	if err = cleanupRuntimeTemps(root); err != nil {
		return err
	}
	return syncRuntimeDir(dir)
}

func cleanupRuntimeTemps(root string) error {
	leftovers, err := filepath.Glob(filepath.Join(root, ".download-*"))
	if err != nil {
		return err
	}
	leftovers = append(leftovers, filepath.Join(root, "install-state.json.tmp"))
	for _, file := range leftovers {
		if info, e := os.Lstat(file); e == nil && info.Mode().IsRegular() {
			if err = os.Remove(file); err != nil {
				return fmt.Errorf("无法清理中断下载的临时文件")
			}
		}
	}
	return nil
}
