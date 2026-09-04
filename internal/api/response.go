package api

import (
	"encoding/json"
	"net/http"
)

// 统一响应格式：{"code":0,"msg":"ok","data":...}

type Response struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

func OK(w http.ResponseWriter, data interface{}) {
	writeJSON(w, http.StatusOK, Response{Code: 0, Msg: "ok", Data: data})
}

func Fail(w http.ResponseWriter, code int, msg string) {
	status := http.StatusOK
	if code == 401 {
		status = http.StatusUnauthorized
	}
	writeJSON(w, status, Response{Code: code, Msg: msg})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// DecodeBody 解析 JSON 请求体。
func DecodeBody(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}
