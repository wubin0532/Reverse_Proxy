package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 会话在请求体读取期间被撤销时，DecodeBody 必须返回哨兵错误，调用方才能回 401。
func TestDecodeBodyReportsRevokedSession(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/sites", strings.NewReader(`{"name":"x"}`))
	r = r.WithContext(context.WithValue(r.Context(), admissionKey{}, admissionCheck(func() bool { return false })))
	var dst struct {
		Name string `json:"name"`
	}
	if err := DecodeBody(r, &dst); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("期望 ErrSessionRevoked, got %v", err)
	}
}

// 会话仍有效时不得误报撤销。
func TestDecodeBodyAcceptsLiveSession(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/sites", strings.NewReader(`{"name":"x"}`))
	r = r.WithContext(context.WithValue(r.Context(), admissionKey{}, admissionCheck(func() bool { return true })))
	var dst struct {
		Name string `json:"name"`
	}
	if err := DecodeBody(r, &dst); err != nil {
		t.Fatalf("有效会话不应报错: %v", err)
	}
	if dst.Name != "x" {
		t.Fatalf("请求体未解析: %+v", dst)
	}
}

// FailDecode 把会话撤销映射为 401，其余解析错误保持 400 与调用方文案。
func TestFailDecodeStatusMapping(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		want    int
		wantMsg string
		assert  func(t *testing.T, w *httptest.ResponseRecorder)
	}{
		{
			name: "会话撤销", err: ErrSessionRevoked, want: http.StatusUnauthorized,
			wantMsg: ErrSessionRevoked.Error(),
			assert: func(t *testing.T, w *httptest.ResponseRecorder) {
				if cc := w.Header().Get("Content-Type"); !strings.HasPrefix(cc, "application/json") {
					t.Fatalf("会话撤销响应应为 JSON, got %q", cc)
				}
			},
		},
		{name: "报文错误", err: errors.New("bad json"), want: http.StatusBadRequest, wantMsg: "请求格式错误"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			FailDecode(w, tc.err, "请求格式错误")
			if w.Code != tc.want {
				t.Fatalf("status=%d want %d", w.Code, tc.want)
			}
			if !strings.Contains(w.Body.String(), tc.wantMsg) {
				t.Fatalf("响应缺少 %q: %s", tc.wantMsg, w.Body.String())
			}
			if tc.assert != nil {
				tc.assert(t, w)
			}
		})
	}
}

// FailDecodeAs 保留调用方状态码（例如二次确认统一 403），但会话撤销仍为 401。
func TestFailDecodeAsKeepsCallerStatus(t *testing.T) {
	w := httptest.NewRecorder()
	FailDecodeAs(w, errors.New("bad json"), http.StatusForbidden, "当前密码错误")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "当前密码错误") {
		t.Fatalf("期望 403/当前密码错误, got %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	FailDecodeAs(w, ErrSessionRevoked, http.StatusForbidden, "当前密码错误")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("会话撤销必须回 401, got %d", w.Code)
	}
}
