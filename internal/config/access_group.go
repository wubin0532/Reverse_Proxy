package config

import (
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"regexp"
	"strings"
)

type AccessGroup struct {
	ID               string   `json:"id"`
	Revision         uint64   `json:"revision"`
	Name             string   `json:"name"`
	Enabled          bool     `json:"enabled"`
	SessionMinutes   int      `json:"sessionMinutes"`
	CrossWebIPBypass bool     `json:"crossWebIpBypass"`
	UserIDs          []string `json:"userIds"`
}
type AccessUser struct {
	ID           string `json:"id"`
	Revision     uint64 `json:"revision"`
	Name         string `json:"name"`
	Username     string `json:"username"`
	Enabled      bool   `json:"enabled"`
	PasswordHash string `json:"passwordHash,omitempty"`
}

var accessID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)
var accessUsername = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,63}$`)

func (c *Config) normalizeAccessGroups() error {
	if len(c.AccessGroups) > 128 || len(c.AccessUsers) > 256 {
		return fmt.Errorf("安全组最多128个，本地用户最多256个")
	}
	users, names := map[string]bool{}, map[string]bool{}
	for _, u := range c.AccessUsers {
		if !accessID.MatchString(u.ID) || users[u.ID] || !accessUsername.MatchString(u.Username) || names[u.Username] || len(u.Name) > 128 {
			return fmt.Errorf("安全用户ID、账号重复或格式无效")
		}
		cost, err := bcrypt.Cost([]byte(u.PasswordHash))
		if err != nil || cost < 10 || cost > 12 {
			return fmt.Errorf("安全用户密码哈希无效")
		}
		users[u.ID] = true
		names[u.Username] = true
	}
	groups := map[string]bool{}
	for _, g := range c.AccessGroups {
		if !accessID.MatchString(g.ID) || groups[g.ID] || strings.TrimSpace(g.Name) == "" || len(g.Name) > 128 || g.SessionMinutes < 1 || g.SessionMinutes > 10080 || len(g.UserIDs) > 256 {
			return fmt.Errorf("安全组名称、ID或会话时长无效")
		}
		groups[g.ID] = true
		seen := map[string]bool{}
		for _, id := range g.UserIDs {
			if !users[id] || seen[id] {
				return fmt.Errorf("安全组用户引用无效或重复")
			}
			seen[id] = true
		}
	}
	for _, s := range c.Sites {
		for _, r := range s.Rules {
			if r.AccessGroupID != "" && !groups[r.AccessGroupID] {
				return fmt.Errorf("引用的安全组不存在")
			}
			if r.AccessGroupID != "" && r.BasicAuth {
				return fmt.Errorf("安全组认证与规则BasicAuth须选择其一")
			}
		}
	}
	for _, f := range c.Forwards {
		if f.AccessGroupID != "" && !groups[f.AccessGroupID] {
			return fmt.Errorf("转发引用的安全组不存在")
		}
	}
	return nil
}
