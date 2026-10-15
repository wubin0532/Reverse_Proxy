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

// DecodeBody 解析 JSON 请求体（普通 API 上限 1 MiB）。
func DecodeBody(r *http.Request, v interface{}) error {
	return DecodeBodyLimit(r, v, 1<<20)
}

// DecodeBodyLimit 与 DecodeBody 相同，但使用调用方指定的请求体上限。
// 备份导入等天然大于普通 API 的请求需要放宽，同时仍受硬上限保护。
func DecodeBodyLimit(r *http.Request, v interface{}, limit int64) error {
	r.Body = http.MaxBytesReader(nil, r.Body, limit)
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
	if !CurrentSession(r) {
		return ErrSessionRevoked
	}
	return nil
}

// ErrSessionRevoked 表示请求体读取期间会话已被撤销（改密、启用/关闭 TOTP、
// 导入备份等都会撤销全部会话）。调用方必须据此返回 401 而不是 400，
// 否则前端会按"参数错误"处理，不引导重新登录，用户重试也不会成功。
var ErrSessionRevoked = errors.New("登录已失效，请重新登录")

// FailDecode 统一处理请求体解析失败：会话失效返回 401，其余按 400 处理，
// 并保留调用方原有的提示文案。
func FailDecode(w http.ResponseWriter, err error, badRequestMsg string) {
	if errors.Is(err, ErrSessionRevoked) {
		Fail(w, http.StatusUnauthorized, ErrSessionRevoked.Error())
		return
	}
	Fail(w, http.StatusBadRequest, badRequestMsg)
}

// FailDecodeAs 与 FailDecode 相同，但把非会话错误映射为调用方指定的状态码与
// 文案（例如二次确认接口为避免泄露信息而统一返回 403）。
func FailDecodeAs(w http.ResponseWriter, err error, status int, msg string) {
	if errors.Is(err, ErrSessionRevoked) {
		Fail(w, http.StatusUnauthorized, ErrSessionRevoked.Error())
		return
	}
	Fail(w, status, msg)
}
