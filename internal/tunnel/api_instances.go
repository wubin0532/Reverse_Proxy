package tunnel

import (
	"fmt"
	"net/http"
	"strings"

	"andey-proxy/internal/config"
	"andey-proxy/internal/ids"
	"github.com/go-chi/chi/v5"
)

func (h *handler) saveInstance(r *http.Request) (any, error) {
	var input struct {
		Name       string `json:"name"`
		Source     string `json:"source"`
		AccountRef string `json:"accountRef"`
		TunnelID   string `json:"tunnelId"`
		Token      string `json:"token"`
		Protocol   string `json:"protocol"`
		IPVersion  string `json:"ipVersion"`
	}
	if err := decode(r, &input); err != nil {
		return nil, err
	}
	inst := config.TunnelInstance{ID: chi.URLParam(r, "id"), Name: input.Name, Source: input.Source, AccountRef: input.AccountRef, TunnelID: strings.TrimSpace(input.TunnelID), Token: strings.TrimSpace(input.Token), Protocol: input.Protocol, IPVersion: input.IPVersion}
	creating := inst.ID == ""
	if creating {
		inst.ID = ids.New()
		if inst.Source == "managed" {
			inst.TunnelID = ""
			inst.Token = ""
		}
	} else {
		previous, err := h.m.instance(inst.ID)
		if err != nil {
			return nil, requestError{404, err.Error()}
		}
		if inst.Source != previous.Source {
			return nil, invalid("不能改变实例来源")
		}
		if inst.Token == "" {
			inst.Token = previous.Token
		}
		if previous.Source == "managed" {
			if inst.AccountRef != previous.AccountRef || inst.TunnelID != previous.TunnelID {
				return nil, invalid("不能改变已管理隧道的账户或 Tunnel ID")
			}
		} else if (len(previous.Routes) > 0 || len(previous.RetiredRoutes) > 0) && (inst.AccountRef != previous.AccountRef || inst.TunnelID != previous.TunnelID) {
			return nil, conflict("已有路由，不能改变隧道身份")
		}
		inst.Enabled = previous.Enabled
		inst.Routes = previous.Routes
		inst.RetiredRoutes = previous.RetiredRoutes
	}
	if err := validateInstance(&inst); err != nil {
		return nil, invalid(err.Error())
	}
	if err := h.m.validateCredentials(&inst); err != nil {
		return nil, invalid(err.Error())
	}
	if inst.AccountRef != "" {
		if _, err := h.m.account(inst.AccountRef); err != nil {
			return nil, invalid(err.Error())
		}
	}
	if inst.Source == "managed" && inst.AccountRef == "" {
		return nil, invalid("新建隧道需要账户 API 凭据")
	}
	if inst.Source == "imported" && inst.Token == "" {
		return nil, invalid("请填写 Tunnel Token")
	}
	for _, other := range h.m.instances() {
		if other.ID != inst.ID && ((inst.TunnelID != "" && inst.TunnelID == other.TunnelID) || (inst.Token != "" && inst.Token == other.Token)) {
			return nil, conflict("该隧道已在本机接入")
		}
	}
	err := h.m.cfg.Update(func(c *config.Config) error {
		if creating {
			c.Tunnels = append(c.Tunnels, inst)
			return nil
		}
		for i := range c.Tunnels {
			if c.Tunnels[i].ID == inst.ID {
				c.Tunnels[i] = inst
				return nil
			}
		}
		return fmt.Errorf("实例不存在")
	})
	if err != nil {
		return nil, err
	}
	h.m.Reload()
	return h.view(inst), nil
}

func (h *handler) control(r *http.Request, action string) (any, error) {
	inst, err := h.m.instance(chi.URLParam(r, "id"))
	if err != nil {
		return nil, requestError{404, err.Error()}
	}
	if action != "stop" {
		if inst.Token == "" {
			return nil, invalid("请先完成隧道创建或填写 Token")
		}
		if err := h.m.validateCredentials(&inst); err != nil {
			return nil, invalid(err.Error())
		}
		rt := h.m.runtime()
		if !rt.Compatible {
			return nil, invalid(rt.Message)
		}
	}
	if action == "restart" {
		inst.Enabled = false
		if err = h.m.saveInstance(inst); err != nil {
			return nil, err
		}
		h.m.Reload()
	}
	inst.Enabled = action != "stop"
	if err = h.m.saveInstance(inst); err != nil {
		return nil, err
	}
	h.m.Reload()
	return h.view(inst), nil
}

func (h *handler) deleteInstance(r *http.Request) (any, error) {
	inst, err := h.m.instance(chi.URLParam(r, "id"))
	if err != nil {
		return nil, requestError{404, err.Error()}
	}
	if inst.Source == "managed" && inst.TunnelID != "" {
		return nil, conflict("请使用删除云端隧道操作，避免遗留云端资源")
	}
	if err = h.m.removeInstance(inst.ID); err != nil {
		return nil, err
	}
	h.m.Reload()
	return nil, nil
}
