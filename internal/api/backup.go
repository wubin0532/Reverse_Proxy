package api

import (
	"fmt"
	"log"
	"net/http"
	"time"
	"unicode/utf8"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/backup"
)

// handleBackupExport 导出加密备份：密码二次确认后，把解密后的配置明文
// 用备份口令重新加密为自描述文件，以附件形式下发。明文绝不写日志、不落盘。
func (s *Server) handleBackupExport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password       string `json:"password"`
		BackupPassword string `json:"backupPassword"`
	}
	if err := DecodeBody(r, &body); err != nil {
		Fail(w, 400, "请求格式错误")
		return
	}
	if utf8.RuneCountInString(body.BackupPassword) < 8 || len(body.BackupPassword) > 256 {
		Fail(w, 400, "备份口令至少 8 个字符")
		return
	}
	if !s.backupMu.TryLock() {
		Fail(w, http.StatusConflict, "已有备份导入或导出正在处理")
		return
	}
	defer s.backupMu.Unlock()
	if !s.confirmAdminPassword(w, r, body.Password) {
		return
	}
	plain, err := s.cfg.PlainJSON()
	if err != nil {
		Fail(w, 500, "读取配置失败")
		return
	}
	data, err := backup.Encrypt(plain, body.BackupPassword, s.version, time.Now())
	if err != nil {
		Fail(w, 500, "生成备份失败")
		return
	}
	log.Printf("[security] 配置备份已导出，客户端: %s", directIP(r.RemoteAddr))
	name := "andey-proxy-backup-" + time.Now().Format("20060102") + ".json"
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleBackupImport 从备份恢复配置：验管理密码 → 解备份 → 校验结构 →
// 原子落盘（先备份当前配置为 config.json.bak）→ 热重载各服务 → 撤销全部会话。
func (s *Server) handleBackupImport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Password       string `json:"password"`
		BackupPassword string `json:"backupPassword"`
		Backup         string `json:"backup"`
	}
	if err := DecodeBody(r, &body); err != nil {
		Fail(w, 400, "请求格式错误")
		return
	}
	if len(body.BackupPassword) > 256 {
		Fail(w, 400, "备份口令不能超过 256 字节")
		return
	}
	if body.Backup == "" {
		Fail(w, 400, "备份内容不能为空")
		return
	}
	if !s.backupMu.TryLock() {
		Fail(w, http.StatusConflict, "已有备份导入或导出正在处理")
		return
	}
	defer s.backupMu.Unlock()
	if !s.confirmAdminPassword(w, r, body.Password) {
		return
	}
	plain, err := backup.Decrypt([]byte(body.Backup), body.BackupPassword)
	if err != nil {
		Fail(w, 400, err.Error())
		return
	}
	if err := s.cfg.Restore(plain); err != nil {
		Fail(w, 400, "导入失败: "+err.Error())
		return
	}
	// 热重载各服务；失败只记日志，配置本身已生效，重启后也会加载。
	if s.restoreHook != nil {
		s.restoreHook()
	}
	log.Printf("[security] 已从备份导入配置，全部会话已撤销，客户端: %s", directIP(r.RemoteAddr))
	s.revokeAllSessions(w)
	OK(w, map[string]bool{"loginRequired": true})
}

// confirmAdminPassword 已认证接口的管理密码二次确认（带限速）。
func (s *Server) confirmAdminPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	if PasswordConfirmLimited("backup", r.RemoteAddr) {
		Fail(w, http.StatusTooManyRequests, "密码错误次数过多，请稍后再试")
		return false
	}
	s.cfg.RLock()
	hash := s.cfg.Settings.AdminPassHash
	s.cfg.RUnlock()
	if hash == "" || !auth.CheckPassword(hash, password) {
		RecordPasswordConfirmFailure("backup", r.RemoteAddr)
		Fail(w, 403, "管理密码错误")
		return false
	}
	ClearPasswordConfirmFailures("backup", r.RemoteAddr)
	return true
}
