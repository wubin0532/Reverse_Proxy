package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"andey-proxy/internal/config"
)

// Match cloudflared's token format without logging any decoded credentials.
type tokenClaims struct {
	AccountID string `json:"a"`
	TunnelID  string `json:"t"`
	Secret    []byte `json:"s"`
}

func (m *Manager) validateCredentials(inst *config.TunnelInstance) error {
	if inst.Token == "" {
		return nil // A managed instance may be waiting for cloud creation.
	}
	if len(inst.Token) > 16384 {
		return fmt.Errorf("Tunnel Token 格式无效")
	}
	data, err := base64.StdEncoding.DecodeString(inst.Token)
	var claims tokenClaims
	if err != nil || json.Unmarshal(data, &claims) != nil || !cloudIDPattern.MatchString(claims.AccountID) || !uuidPattern.MatchString(claims.TunnelID) || len(claims.Secret) == 0 {
		return fmt.Errorf("Tunnel Token 格式无效")
	}
	if inst.TunnelID != "" && !strings.EqualFold(inst.TunnelID, claims.TunnelID) {
		return fmt.Errorf("Tunnel Token 与 Tunnel ID 不一致")
	}
	if inst.AccountRef != "" {
		account, err := m.account(inst.AccountRef)
		if err != nil {
			return err
		}
		if !strings.EqualFold(account.AccountID, claims.AccountID) {
			return fmt.Errorf("Tunnel Token 与管理账户的 Account ID 不一致")
		}
	}
	inst.TunnelID = strings.ToLower(claims.TunnelID)
	return nil
}
