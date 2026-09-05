package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	status := code
	if status < 400 || status > 599 {
		status = http.StatusBadRequest
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
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra interface{}
	if err := d.Decode(&extra); err == nil {
		return fmt.Errorf("请求体包含多个 JSON 值")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}
