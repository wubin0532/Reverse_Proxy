package tunnel

import (
	"encoding/json"
	"net/http"
	"time"

	"andey-proxy/internal/api"
	"andey-proxy/internal/auth"
)

func (h *handler) latestRuntime(w http.ResponseWriter, r *http.Request) {
	release, err := h.m.releases.latest(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		api.Fail(w, 502, err.Error())
		return
	}
	api.OK(w, release)
}

func (h *handler) installRuntime(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ReleaseID         string   `json:"releaseId"`
		Password          string   `json:"password"`
		RestartRunning    bool     `json:"restartRunning"`
		AffectedInstances []string `json:"affectedInstances"`
	}
	if api.DecodeBody(r, &input) != nil {
		api.Fail(w, 400, "请求格式错误")
		return
	}
	if len(input.Password) > 72 || !api.AdmitPasswordConfirm("runtime-install", r.RemoteAddr) {
		api.Fail(w, 429, "密码错误次数过多，请稍后再试")
		return
	}
	h.m.cfg.RLock()
	hash := h.m.cfg.Settings.AdminPassHash
	h.m.cfg.RUnlock()
	release, ok := auth.AcquireVerifySlot(3 * time.Second)
	if !ok {
		api.Fail(w, 429, "请求过于频繁，请稍后再试")
		return
	}
	valid := hash != "" && auth.CheckPassword(hash, input.Password)
	release()
	if !valid {
		api.Fail(w, 403, "管理密码错误")
		return
	}
	if !api.CurrentSession(r) {
		api.Fail(w, 401, "登录已失效，请重新登录")
		return
	}
	api.ClearPasswordConfirmFailures("runtime-install", r.RemoteAddr)
	op, err := h.m.installRuntime(input.ReleaseID, input.RestartRunning, input.AffectedInstances)
	if err != nil {
		api.Fail(w, 409, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(api.Response{Code: 0, Msg: "ok", Data: op})
}
