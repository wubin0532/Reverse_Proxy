package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
)

const TokenCookie = "andey-proxy_token"

// Server 管理后台 API 服务。
type Server struct {
	cfg      *config.Config
	tokens   *auth.TokenStore
	mounters []func(chi.Router)
}

func NewServer(cfg *config.Config) *Server {
	return &Server{cfg: cfg, tokens: auth.NewTokenStore()}
}

// Mount 注册模块路由（会在已认证分组内调用 fn）。
// 由各模块包（import api）提供 RegisterRoutes，main 负责装配，避免 import cycle。
func (s *Server) Mount(fn func(chi.Router)) {
	s.mounters = append(s.mounters, fn)
}

// Router 构建 API 路由。
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	r.Post("/api/login", s.handleLogin)
	r.Post("/api/logout", s.handleLogout)

	r.Group(func(r chi.Router) {
		r.Use(s.requireAuth)
		r.Get("/api/me", s.handleMe)
		r.Post("/api/settings/password", s.handleChangePassword)
		for _, m := range s.mounters {
			m(r)
		}
	})

	return r
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(TokenCookie)
		if err != nil || !s.tokens.Valid(c.Value) {
			Fail(w, 401, "未登录或登录已过期")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := DecodeBody(r, &body); err != nil {
		Fail(w, 400, "请求格式错误")
		return
	}
	s.cfg.RLock()
	user := s.cfg.Settings.AdminUser
	hash := s.cfg.Settings.AdminPassHash
	s.cfg.RUnlock()

	// 首次使用：未设置密码哈希时默认账号 666/666
	if hash == "" {
		if body.Username != "666" || body.Password != "666" {
			Fail(w, 403, "账号或密码错误")
			return
		}
	} else if body.Username != user || !auth.CheckPassword(hash, body.Password) {
		Fail(w, 403, "账号或密码错误")
		return
	}

	tok := s.tokens.Issue()
	http.SetCookie(w, &http.Cookie{
		Name:     TokenCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	OK(w, map[string]interface{}{"needChangePassword": hash == ""})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(TokenCookie); err == nil {
		s.tokens.Revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: TokenCookie, Value: "", Path: "/", MaxAge: -1})
	OK(w, nil)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	s.cfg.RLock()
	defer s.cfg.RUnlock()
	OK(w, map[string]interface{}{
		"username":           s.cfg.Settings.AdminUser,
		"needChangePassword": s.cfg.Settings.AdminPassHash == "",
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := DecodeBody(r, &body); err != nil || body.NewPassword == "" {
		Fail(w, 400, "请求格式错误")
		return
	}
	s.cfg.Lock()
	hash := s.cfg.Settings.AdminPassHash
	user := s.cfg.Settings.AdminUser
	s.cfg.Unlock()
	if hash != "" && !auth.CheckPassword(hash, body.OldPassword) {
		Fail(w, 403, "原密码错误")
		return
	}
	if body.Username == "" {
		body.Username = user
	}
	newHash, err := auth.HashPassword(body.NewPassword)
	if err != nil {
		Fail(w, 500, "密码加密失败")
		return
	}
	s.cfg.Lock()
	s.cfg.Settings.AdminUser = body.Username
	s.cfg.Settings.AdminPassHash = newHash
	s.cfg.Unlock()
	if err := s.cfg.Save(); err != nil {
		Fail(w, 500, "保存配置失败")
		return
	}
	OK(w, nil)
}
