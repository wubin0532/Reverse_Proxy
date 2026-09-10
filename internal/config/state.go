package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// stateWriteDelay 运行状态落盘去抖窗口：窗口内的多次更新合并为一次写盘。
const stateWriteDelay = 5 * time.Second

// CertState 一张证书的运行状态（申请结果）。从加密配置迁出：
// 运行事件只写 state.json，不再触发整份加密配置重写。
type CertState struct {
	CertFile  string `json:"certFile,omitempty"` // 相对配置目录
	KeyFile   string `json:"keyFile,omitempty"`
	NotAfter  string `json:"notAfter,omitempty"` // 最近证书到期时间 RFC3339
	LastError string `json:"lastError,omitempty"`
}

// State 全部运行期状态的磁盘快照。只放运行事件产生的状态，用户意图留在加密配置里。
type State struct {
	TOTPLastCounter int64                `json:"totpLastCounter,omitempty"` // 最近一次验证成功的 TOTP 计数器，防重放
	Certs           map[string]CertState `json:"certs,omitempty"`           // key: CertConf.ID
}

// StateStore 运行状态存储：单个 state.json（0600，tmp+rename+fsync 原子写）。
// Update 去抖合并落盘；关键低频事件（登录成功、证书申请结束）可紧接 Flush 立即落盘。
type StateStore struct {
	mu         sync.Mutex
	path       string
	state      State
	dirty      bool
	gen        uint64 // 每次 Update 递增，Flush 用它识别写盘期间的新变更
	timer      *time.Timer
	closed     bool
	writeDelay time.Duration
}

// LoadState 加载目录下的运行状态，文件缺失或损坏时从空状态开始（状态可由运行事件重建）。
func LoadState(dir string) *StateStore {
	st := &StateStore{
		path:       filepath.Join(dir, "state.json"),
		writeDelay: stateWriteDelay,
	}
	st.state.Certs = make(map[string]CertState)
	if data, err := os.ReadFile(st.path); err == nil {
		var s State
		if json.Unmarshal(data, &s) == nil {
			st.state = s
		}
	}
	if st.state.Certs == nil {
		st.state.Certs = make(map[string]CertState)
	}
	return st
}

// Cert 取一张证书的运行状态快照，无记录返回零值。
func (st *StateStore) Cert(certID string) CertState {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.state.Certs[certID]
}

// TOTPCounter 取最近一次验证成功的 TOTP 计数器。
func (st *StateStore) TOTPCounter() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.state.TOTPLastCounter
}

// Update 修改内存状态并标记待落盘；写盘在去抖窗口后合并执行（Flush/Close 会立即落盘）。
func (st *StateStore) Update(fn func(*State)) {
	st.mu.Lock()
	defer st.mu.Unlock()
	fn(&st.state)
	if st.state.Certs == nil {
		st.state.Certs = make(map[string]CertState)
	}
	st.dirty = true
	st.gen++
	if st.timer == nil && !st.closed {
		st.timer = time.AfterFunc(st.writeDelay, func() {
			st.mu.Lock()
			st.timer = nil
			st.mu.Unlock()
			_ = st.Flush()
		})
	}
}

// Flush 有待落盘变更时立即原子写盘；无变更直接返回。
func (st *StateStore) Flush() error {
	st.mu.Lock()
	if !st.dirty {
		st.mu.Unlock()
		return nil
	}
	gen := st.gen
	data, err := json.Marshal(st.state)
	st.mu.Unlock()
	if err != nil {
		return err
	}
	if err := writeStateFile(st.path, data); err != nil {
		return err
	}
	st.mu.Lock()
	// 写盘期间可能有新 Update：代际未变才清除待落盘标记，否则留给已排期的去抖写。
	if st.gen == gen {
		st.dirty = false
	}
	st.mu.Unlock()
	return nil
}

// Close 停止去抖定时器并落盘全部待写变更（优雅退出时调用）。
func (st *StateStore) Close() error {
	st.mu.Lock()
	st.closed = true
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.mu.Unlock()
	return st.Flush()
}

// seed 仅填充状态库中尚无值的条目：state.json 的内容比旧加密配置里的残留更新，
// 迁移/导入备份时旧值不得覆盖已有状态。有填充时立即落盘。
func (st *StateStore) seed(certs map[string]CertState, totpCounter int64) error {
	st.mu.Lock()
	changed := false
	for id, cs := range certs {
		if existing, ok := st.state.Certs[id]; !ok || existing == (CertState{}) {
			st.state.Certs[id] = cs
			changed = true
		}
	}
	if st.state.TOTPLastCounter == 0 && totpCounter > 0 {
		st.state.TOTPLastCounter = totpCounter
		changed = true
	}
	if changed {
		st.dirty = true
		st.gen++
	}
	st.mu.Unlock()
	return st.Flush()
}

// legacyRuntime 旧版配置/备份中携带的运行状态字段（新结构体已移除，仅靠影子解析读取）。
type legacyRuntime struct {
	Settings struct {
		TOTPLastCounter int64 `json:"totpLastCounter"`
	} `json:"settings"`
	Certs []struct {
		ID        string `json:"id"`
		CertFile  string `json:"certFile"`
		KeyFile   string `json:"keyFile"`
		NotAfter  string `json:"notAfter"`
		LastError string `json:"lastError"`
	} `json:"certs"`
}

// seedRuntimeState 把旧配置明文里的运行状态迁入状态库。影子解析失败不视为错误：
// 主配置已成功解析，运行状态可由后续事件重建。
func (st *StateStore) seedRuntimeState(plain []byte) error {
	var legacy legacyRuntime
	if json.Unmarshal(plain, &legacy) != nil {
		return nil
	}
	certs := make(map[string]CertState)
	for _, c := range legacy.Certs {
		cs := CertState{CertFile: c.CertFile, KeyFile: c.KeyFile, NotAfter: c.NotAfter, LastError: c.LastError}
		if c.ID != "" && cs != (CertState{}) {
			certs[c.ID] = cs
		}
	}
	if len(certs) == 0 && legacy.Settings.TOTPLastCounter == 0 {
		return nil
	}
	return st.seed(certs, legacy.Settings.TOTPLastCounter)
}

// writeStateFile tmp+rename+fsync 原子写，与配置落盘同一套耐久性语义。
func writeStateFile(path string, data []byte) error {
	tmp, err := os.OpenFile(path+".tmp", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path + ".tmp")
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		_ = os.Remove(path + ".tmp")
		return err
	}
	if dir, openErr := os.Open(filepath.Dir(path)); openErr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
