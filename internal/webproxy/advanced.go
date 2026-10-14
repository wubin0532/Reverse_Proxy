package webproxy

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"andey-proxy/internal/config"
)

func corsPreflight(r *http.Request, c *config.CORSConfig) bool {
	return c != nil && c.Enabled && r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != ""
}
func corsAllowed(c *config.CORSConfig, origin, method string, headers []string) bool {
	allowedOrigin, allowedMethod := false, false
	for _, o := range c.Origins {
		if o == origin || o == "*" {
			allowedOrigin = true
		}
	}
	for _, m := range c.Methods {
		if m == method {
			allowedMethod = true
		}
	}
	if !allowedOrigin || !allowedMethod {
		return false
	}
	for _, h := range headers {
		ok := false
		for _, a := range c.Headers {
			if strings.EqualFold(strings.TrimSpace(h), a) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
func (ss *siteServer) advancedMiddleware() middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rule := matchedRule(r)
			for k, v := range rule.ResponseHeaders {
				if v == "" {
					w.Header().Del(k)
				} else {
					w.Header().Set(k, v)
				}
			}
			c := rule.CORS
			origin := r.Header.Get("Origin")
			if c != nil && c.Enabled {
				w.Header().Add("Vary", "Origin")
				method := r.Method
				var headers []string
				if corsPreflight(r, c) {
					method = r.Header.Get("Access-Control-Request-Method")
					if v := r.Header.Get("Access-Control-Request-Headers"); v != "" {
						headers = strings.Split(v, ",")
					}
					w.Header().Add("Vary", "Access-Control-Request-Method")
					w.Header().Add("Vary", "Access-Control-Request-Headers")
				}
				if origin != "" && !corsAllowed(c, origin, method, headers) {
					http.Error(w, "403 Origin, method or headers not allowed", 403)
					return
				}
				if origin != "" {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					if c.Credentials {
						w.Header().Set("Access-Control-Allow-Credentials", "true")
					}
					if corsPreflight(r, c) {
						w.Header().Set("Access-Control-Allow-Methods", strings.Join(c.Methods, ", "))
						w.Header().Set("Access-Control-Allow-Headers", strings.Join(c.Headers, ", "))
						w.Header().Set("Access-Control-Max-Age", strconv.Itoa(c.MaxAge))
						w.WriteHeader(http.StatusNoContent)
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
func rewrittenURL(u *url.URL, rule config.SubRule) *url.URL {
	stripped := stripFrontendURL(u, normalizedFrontendPrefix(rule.FrontendPath))
	out := *stripped
	out.Path = strings.TrimSuffix(rule.PathRewritePrefix, "/") + stripped.Path
	out.RawPath = (&url.URL{Path: strings.TrimSuffix(rule.PathRewritePrefix, "/")}).EscapedPath() + stripped.EscapedPath()
	return &out
}
func proxyError(w http.ResponseWriter, status int, body string) {
	if body == "" {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
