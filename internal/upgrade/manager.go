package upgrade

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"debug/elf"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const maxRunSize = 100 << 20
const payloadMarker = "__PAYLOAD_BELOW__"
const binaryName = "andey-proxy"
const releasePublicKey = "umIQ8sxGGf3XRLWs24h36YB9XZ6pFtIcCvKuKLh91Pw="

var versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)

type State string

const (
	StateIdle       State = "idle"
	StateInspecting State = "inspecting"
	StateInspected  State = "inspected"
	StateInstalling State = "installing"
	StateRestarting State = "restarting"
	StateDone       State = "done"
	StateFailed     State = "failed"
)

type Status struct {
	State      State       `json:"state"`
	Version    string      `json:"version,omitempty"`
	Error      string      `json:"error,omitempty"`
	Note       string      `json:"note,omitempty"`
	Inspection *Inspection `json:"inspection,omitempty"`
}
type Manifest struct {
	Version string `json:"version"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}
type Inspection struct {
	UploadID string `json:"uploadId"`
	Manifest
	Compatible bool `json:"compatible"`
	Downgrade  bool `json:"downgrade"`
	Signed     bool `json:"signed"`
}
type stagedPackage struct {
	path, binaryPath string
	inspection       Inspection
	expires          time.Time
}
type Manager struct {
	version, dir string
	statusPath   string
	mu           sync.Mutex
	state        State
	errMsg, note string
	staged       *stagedPackage
}

func NewManager(version, dir string) *Manager {
	cleanupStaleUploads(dir)
	m := &Manager{version: version, dir: dir, statusPath: filepath.Join(dir, "update-status.json"), state: StateIdle}
	if data, err := os.ReadFile(m.statusPath); err == nil {
		var previous Status
		if json.Unmarshal(data, &previous) == nil {
			m.state, m.errMsg, m.note = previous.State, previous.Error, previous.Note
			if previous.Version != "" && previous.State == StateRestarting && compareVersions(version, previous.Version) != 0 {
				m.state = StateFailed
				m.errMsg = fmt.Sprintf("服务已重新启动，但运行版本 %s 与待安装版本 %s 不一致", version, previous.Version)
			}
		} else {
			m.state = StateFailed
			m.errMsg = "更新状态文件损坏"
		}
	}
	return m
}
func (m *Manager) Version() string { return m.version }
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	ver := m.version
	var inspection *Inspection
	if m.staged != nil {
		ver = m.staged.inspection.Version
		copy := m.staged.inspection
		inspection = &copy
	}
	return Status{State: m.state, Version: ver, Error: m.errMsg, Note: m.note, Inspection: inspection}
}

// MarkStarted 在新进程完成核心模块启动后确认上一次更新成功。
func (m *Manager) MarkStarted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateRestarting {
		return
	}
	m.state = StateDone
	m.errMsg = ""
	m.note = "新版本已启动"
	_ = m.persistStatusLocked(m.version)
	log.Printf("[update] 更新 %s 已完成并成功启动", m.version)
}

func (m *Manager) InspectUpload(w http.ResponseWriter, r *http.Request) (*Inspection, error) {
	m.mu.Lock()
	if m.staged != nil || m.state == StateInspecting || m.state == StateInstalling || m.state == StateRestarting {
		m.mu.Unlock()
		return nil, errors.New("已有更新包正在处理")
	}
	m.state = StateInspecting
	m.mu.Unlock()
	completed := false
	defer func() {
		if completed {
			return
		}
		m.mu.Lock()
		if m.state == StateInspecting {
			m.state = StateIdle
		}
		m.mu.Unlock()
	}()
	r.Body = http.MaxBytesReader(w, r.Body, maxRunSize+(1<<20))
	// 用 MultipartReader 流式读取，直接落到配置目录；ParseMultipartForm 会把
	// 大文件暂存到 os.TempDir()，OpenWrt 上 /tmp 是 tmpfs（占内存），有 OOM 风险。
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("更新包超过 100 MiB 或格式错误")
	}
	var part *multipart.Part
	for {
		p, e := mr.NextPart()
		if e == io.EOF {
			return nil, errors.New("请选择 .run 更新包")
		}
		if e != nil {
			return nil, fmt.Errorf("更新包超过 100 MiB 或格式错误")
		}
		if p.FormName() == "package" {
			part = p
			break
		}
		p.Close()
	}
	defer part.Close()
	if !strings.HasSuffix(strings.ToLower(part.FileName()), ".run") {
		return nil, errors.New("只支持 .run 更新包")
	}
	tmp, err := os.CreateTemp(m.dir, "update-*.run")
	if err != nil {
		return nil, err
	}
	_ = tmp.Chmod(0o600)
	n, copyErr := io.Copy(tmp, io.LimitReader(part, maxRunSize+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || n > maxRunSize {
		_ = os.Remove(tmp.Name())
		return nil, errors.New("保存更新包失败或文件超过 100 MiB")
	}
	manifest, binPath, err := inspectSignedRun(tmp.Name(), m.dir)
	if err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		_ = os.Remove(tmp.Name())
		_ = os.Remove(binPath)
		return nil, errors.New("生成上传标识失败")
	}
	id := hex.EncodeToString(idBytes)
	ins := Inspection{UploadID: id, Manifest: *manifest, Compatible: manifest.GOOS == runtime.GOOS && manifest.GOARCH == runtime.GOARCH, Signed: true, Downgrade: compareVersions(manifest.Version, m.version) < 0}
	if !ins.Compatible {
		_ = os.Remove(tmp.Name())
		_ = os.Remove(binPath)
		return nil, fmt.Errorf("更新包架构 %s/%s 与当前 %s/%s 不兼容", manifest.GOOS, manifest.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	m.mu.Lock()
	m.staged = &stagedPackage{path: tmp.Name(), binaryPath: binPath, inspection: ins, expires: time.Now().Add(10 * time.Minute)}
	m.state = StateInspected
	m.errMsg = ""
	m.note = ""
	m.mu.Unlock()
	completed = true
	time.AfterFunc(10*time.Minute, func() { m.expire(id) })
	log.Printf("[update] 更新包已通过签名与架构检查，版本: %s", ins.Version)
	return &ins, nil
}
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.staged == nil || m.staged.inspection.UploadID != id {
		return false
	}
	m.removeStagedLocked()
	m.state = StateIdle
	log.Printf("[update] 已取消并删除临时更新包")
	return true
}
func (m *Manager) Install(id string, allowDowngrade bool) error {
	m.mu.Lock()
	if m.staged == nil || m.staged.inspection.UploadID != id {
		m.mu.Unlock()
		return errors.New("更新包不存在或已过期")
	}
	if time.Now().After(m.staged.expires) {
		m.removeStagedLocked()
		m.mu.Unlock()
		return errors.New("更新包已过期")
	}
	if m.staged.inspection.Downgrade && !allowDowngrade {
		m.mu.Unlock()
		return errors.New("默认禁止降级，请明确允许降级")
	}
	// 锁内取出 staged 并置 nil 转移所有权，过期定时器此后不会再误删安装中的文件。
	staged := m.staged
	m.staged = nil
	ver := staged.inspection.Version
	m.state = StateInstalling
	m.mu.Unlock()
	if err := installAtomic(staged.binaryPath); err != nil {
		_ = os.Remove(staged.path)
		_ = os.Remove(staged.binaryPath)
		m.fail(err)
		return err
	}
	m.mu.Lock()
	m.state = StateRestarting
	m.note = "新版本已安装，服务即将重启"
	_ = os.Remove(staged.path)
	_ = os.Remove(staged.binaryPath)
	if err := m.persistStatusLocked(ver); err != nil {
		m.mu.Unlock()
		_ = restoreBackup()
		m.fail(fmt.Errorf("保存更新状态失败: %w", err))
		return err
	}
	m.mu.Unlock()
	log.Printf("[update] 更新 %s 已安装，已安排服务重启", ver)
	go func() {
		time.Sleep(time.Second)
		if err := startRestart(); err != nil {
			if rollbackErr := restoreBackup(); rollbackErr != nil {
				err = fmt.Errorf("重启失败: %v；恢复备份也失败: %w", err, rollbackErr)
			}
			m.fail(err)
		}
	}()
	return nil
}
func (m *Manager) fail(err error) {
	m.mu.Lock()
	m.state = StateFailed
	m.errMsg = err.Error()
	_ = m.persistStatusLocked(m.version)
	m.mu.Unlock()
	log.Printf("[update] 更新失败: %v", err)
}

func (m *Manager) persistStatusLocked(version string) error {
	status := Status{State: m.state, Version: version, Error: m.errMsg, Note: m.note}
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	tmp, err := os.OpenFile(m.statusPath+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Chmod(0o600)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(m.statusPath + ".tmp")
		return err
	}
	if err = os.Rename(m.statusPath+".tmp", m.statusPath); err != nil {
		_ = os.Remove(m.statusPath + ".tmp")
		return err
	}
	return nil
}

func cleanupStaleUploads(dir string) {
	for _, pattern := range []string{"update-*.run", "update-bin-*"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, path := range matches {
			if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
				_ = os.Remove(path)
			}
		}
	}
}
func (m *Manager) expire(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.staged != nil && m.staged.inspection.UploadID == id && time.Now().After(m.staged.expires) {
		m.removeStagedLocked()
		m.state = StateIdle
	}
}
func (m *Manager) removeStagedLocked() {
	if m.staged != nil {
		_ = os.Remove(m.staged.path)
		_ = os.Remove(m.staged.binaryPath)
		m.staged = nil
	}
}

func inspectSignedRun(path, dir string) (*Manifest, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	headerBytes := 0
	for {
		line, e := br.ReadSlice('\n')
		headerBytes += len(line)
		if e == bufio.ErrBufferFull || headerBytes > 64<<10 {
			return nil, "", errors.New("更新包脚本头超过 64 KiB")
		}
		if trimLine(string(line)) == payloadMarker {
			break
		}
		if e != nil {
			return nil, "", errors.New("更新包缺少 payload 标记")
		}
	}
	gz, err := gzip.NewReader(br)
	if err != nil {
		return nil, "", errors.New("更新包 payload 无效")
	}
	defer gz.Close()
	tr := tar.NewReader(io.LimitReader(gz, maxRunSize+(2<<20)))
	var manifestBytes, sig []byte
	var binPath string
	var binSize int64
	var binDigest string
	cleanup := func() {
		if binPath != "" {
			_ = os.Remove(binPath)
		}
	}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			cleanup()
			return nil, "", e
		}
		name := filepath.Base(h.Name)
		if h.Typeflag != tar.TypeReg {
			continue
		}
		switch name {
		case "manifest.json":
			if len(manifestBytes) != 0 {
				cleanup()
				return nil, "", errors.New("更新包包含重复清单")
			}
			manifestBytes, e = io.ReadAll(io.LimitReader(tr, 1<<20))
		case "manifest.sig":
			if len(sig) != 0 {
				cleanup()
				return nil, "", errors.New("更新包包含重复签名")
			}
			sig, e = io.ReadAll(io.LimitReader(tr, 4096))
		case binaryName:
			if binPath != "" {
				cleanup()
				return nil, "", errors.New("更新包包含重复二进制")
			}
			tmp, createErr := os.CreateTemp(dir, "update-bin-*")
			if createErr != nil {
				return nil, "", createErr
			}
			binPath = tmp.Name()
			_ = tmp.Chmod(0o700)
			hash := sha256.New()
			binSize, e = io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(tr, maxRunSize+1))
			if e == nil && binSize > maxRunSize {
				e = errors.New("更新包内二进制超过 100 MiB")
			}
			if e == nil {
				e = tmp.Sync()
			}
			if closeErr := tmp.Close(); e == nil {
				e = closeErr
			}
			binDigest = hex.EncodeToString(hash.Sum(nil))
		}
		if e != nil {
			cleanup()
			return nil, "", e
		}
	}
	if len(manifestBytes) == 0 || len(sig) == 0 || binPath == "" || binSize == 0 {
		cleanup()
		return nil, "", errors.New("更新包缺少二进制、清单或签名")
	}
	pub, err := base64.StdEncoding.DecodeString(releasePublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		cleanup()
		return nil, "", errors.New("内置更新公钥无效")
	}
	sigRaw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), manifestBytes, sigRaw) {
		cleanup()
		return nil, "", errors.New("更新包签名验证失败")
	}
	var manifest Manifest
	if json.Unmarshal(manifestBytes, &manifest) != nil || !versionPattern.MatchString(manifest.Version) || manifest.GOOS != "linux" {
		cleanup()
		return nil, "", errors.New("更新清单的版本或平台无效")
	}
	if binSize != manifest.Size || binDigest != strings.ToLower(manifest.SHA256) {
		cleanup()
		return nil, "", errors.New("更新包大小或 SHA256 校验失败")
	}
	ef, err := elf.Open(binPath)
	if err != nil {
		cleanup()
		return nil, "", errors.New("更新包不是有效 ELF")
	}
	machine, elfType, byteOrder := ef.Machine, ef.Type, ef.Data
	_ = ef.Close()
	if (elfType != elf.ET_EXEC && elfType != elf.ET_DYN) || !elfMatches(machine, byteOrder, manifest.GOARCH) {
		cleanup()
		return nil, "", errors.New("ELF 类型或架构与清单不一致")
	}
	return &manifest, binPath, nil
}
func elfMatches(m elf.Machine, data elf.Data, arch string) bool {
	switch arch {
	case "amd64":
		return m == elf.EM_X86_64 && data == elf.ELFDATA2LSB
	case "arm64":
		return m == elf.EM_AARCH64 && data == elf.ELFDATA2LSB
	case "arm":
		return m == elf.EM_ARM && data == elf.ELFDATA2LSB
	case "mips":
		return m == elf.EM_MIPS && data == elf.ELFDATA2MSB
	case "mipsle":
		return m == elf.EM_MIPS && data == elf.ELFDATA2LSB
	}
	return false
}
func installAtomic(newBin string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if r, e := filepath.EvalSymlinks(exe); e == nil {
		exe = r
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".andey-proxy-new-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	src, err := os.Open(newBin)
	if err != nil {
		tmp.Close()
		return err
	}
	_, err = io.Copy(tmp, src)
	src.Close()
	if err == nil {
		err = tmp.Sync()
	}
	if err == nil {
		err = tmp.Chmod(0o755)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	backup := exe + ".bak"
	_ = os.Remove(backup)
	if err = os.Rename(exe, backup); err != nil {
		return err
	}
	if err = os.Rename(tmpName, exe); err != nil {
		_ = os.Rename(backup, exe)
		return err
	}
	if dirHandle, openErr := os.Open(dir); openErr == nil {
		err = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	if err != nil {
		failed := exe + ".failed-update"
		_ = os.Rename(exe, failed)
		_ = os.Rename(backup, exe)
		_ = os.Remove(failed)
		return err
	}
	return nil
}

func executablePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(exe); resolveErr == nil {
		exe = resolved
	}
	return exe, nil
}

func startRestart() error {
	var cmd *exec.Cmd
	if _, err := os.Stat("/etc/init.d/andey-proxy"); err == nil {
		cmd = exec.Command("/etc/init.d/andey-proxy", "restart")
	} else if systemctl, err := exec.LookPath("systemctl"); err == nil {
		cmd = exec.Command(systemctl, "restart", "andey-proxy")
	} else {
		return errors.New("未找到可用的服务重启方式")
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(output))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}

func restoreBackup() error {
	exe, err := executablePath()
	if err != nil {
		return err
	}
	backup := exe + ".bak"
	failed := exe + ".failed-update"
	if err := os.Rename(exe, failed); err != nil {
		return err
	}
	if err := os.Rename(backup, exe); err != nil {
		_ = os.Rename(failed, exe)
		return err
	}
	_ = os.Remove(failed)
	return nil
}

func trimLine(s string) string {
	return strings.TrimRight(s, "\r\n")
}
