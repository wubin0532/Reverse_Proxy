package tunnel

import (
	"fmt"
	"net/http"
	"strings"

	"andey-proxy/internal/api"
	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

type accountView struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	AccountID       string `json:"accountId"`
	TokenConfigured bool   `json:"tokenConfigured"`
}

func accountOutput(a config.TunnelAccount) accountView {
	return accountView{a.ID, a.Name, a.AccountID, a.Token != ""}
}

func (h *handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	h.m.cfg.RLock()
	out := []accountView{}
	for _, a := range h.m.cfg.TunnelAccounts {
		out = append(out, accountOutput(a))
	}
	h.m.cfg.RUnlock()
	api.OK(w, out)
}

func (h *handler) saveAccount(r *http.Request) (any, error) {
	var input struct {
		Name      string `json:"name"`
		AccountID string `json:"accountId"`
		Token     string `json:"token"`
	}
	if err := decode(r, &input); err != nil {
		return nil, err
	}
	a := config.TunnelAccount{ID: chi.URLParam(r, "id"), Name: strings.TrimSpace(input.Name), AccountID: strings.TrimSpace(input.AccountID), Token: strings.TrimSpace(input.Token)}
	if a.Name == "" || len(a.Name) > 128 || !cloudIDPattern.MatchString(a.AccountID) {
		return nil, invalid("名称或 Account ID 无效")
	}
	creating := a.ID == ""
	if creating {
		a.ID = ids.New()
	} else {
		prev, err := h.m.account(a.ID)
		if err != nil {
			return nil, requestError{404, err.Error()}
		}
		if a.Token == "" {
			a.Token = prev.Token
		}
		if a.AccountID != prev.AccountID {
			for _, inst := range h.m.instances() {
				if inst.AccountRef == a.ID {
					return nil, conflict("账户正在被隧道引用，不能改变 Account ID")
				}
			}
		}
	}
	if a.Token == "" || len(a.Token) > 4096 || strings.ContainsAny(a.Token, "\r\n\x00") {
		return nil, invalid("API Token 不能为空或包含换行")
	}
	err := h.m.cfg.Update(func(c *config.Config) error {
		if creating {
			c.TunnelAccounts = append(c.TunnelAccounts, a)
			return nil
		}
		for i := range c.TunnelAccounts {
			if c.TunnelAccounts[i].ID == a.ID {
				c.TunnelAccounts[i] = a
				return nil
			}
		}
		return fmt.Errorf("账户不存在")
	})
	return accountOutput(a), err
}

func (h *handler) deleteAccount(r *http.Request) (any, error) {
	id := chi.URLParam(r, "id")
	for _, inst := range h.m.instances() {
		if inst.AccountRef == id {
			return nil, conflict("账户凭据正在被隧道引用")
		}
	}
	err := h.m.cfg.Update(func(c *config.Config) error {
		for i := range c.TunnelAccounts {
			if c.TunnelAccounts[i].ID == id {
				c.TunnelAccounts = append(c.TunnelAccounts[:i], c.TunnelAccounts[i+1:]...)
				return nil
			}
		}
		return requestError{404, "账户不存在"}
	})
	return nil, err
}

func (h *handler) zones(w http.ResponseWriter, r *http.Request) {
	a, err := h.m.account(chi.URLParam(r, "id"))
	if err != nil {
		api.Fail(w, 404, err.Error())
		return
	}
	zones, err := h.m.clientFactory(a).zones(r.Context())
	if err != nil {
		api.Fail(w, 502, err.Error())
		return
	}
	api.OK(w, zones)
}
func (h *handler) testAccount(w http.ResponseWriter, r *http.Request) {
	a, err := h.m.account(chi.URLParam(r, "id"))
	if err != nil {
		api.Fail(w, 404, err.Error())
		return
	}
	c := h.m.clientFactory(a)
	var tunnels []cloudTunnel
	if err = c.do(r.Context(), "GET", c.base()+"?per_page=1&is_deleted=false", nil, &tunnels); err == nil {
		_, err = c.zones(r.Context())
	}
	if err != nil {
		api.Fail(w, 502, err.Error())
		return
	}
	api.OK(w, map[string]string{"message": "账户与域名读取权限验证成功；写权限将在执行操作时验证"})
}
