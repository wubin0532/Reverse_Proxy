package config

import (
	"fmt"
	"golang.org/x/net/http/httpguts"
	"net/url"
	"path/filepath"
	"strings"
)

type CORSConfig struct {
	Enabled     bool     `json:"enabled"`
	Origins     []string `json:"origins"`
	Methods     []string `json:"methods"`
	Headers     []string `json:"headers"`
	Credentials bool     `json:"credentials"`
	MaxAge      int      `json:"maxAge"`
}

func (r *SubRule) ValidateAdvanced() error {
	if r.RateLimitRPS < 0 || r.RateLimitRPS > 100000 || r.RateLimitBurst < 0 || r.RateLimitBurst > 200000 || r.MaxRequestBodyMiB < 0 || r.MaxRequestBodyMiB > 10240 {
		return fmt.Errorf("请求频率、突发或请求体容量超出范围")
	}
	if r.Type == "webdav" || r.Type == "cgi" {
		if !filepath.IsAbs(r.RootDir) || filepath.Clean(r.RootDir) == string(filepath.Separator) {
			return fmt.Errorf("WebDAV/CGI须指定独立的绝对根目录")
		}
		if (r.Type == "cgi" || r.WebDAVWritable) && !r.BasicAuth && r.AccessGroupID == "" {
			return fmt.Errorf("CGI和可写WebDAV必须配置认证")
		}
	}
	if r.Type == "cgi" {
		if r.CGIEntry == "" || !filepath.IsLocal(r.CGIEntry) || strings.ContainsAny(r.CGIEntry, "\\\x00\r\n") || len(r.CGIEntry) > 512 {
			return fmt.Errorf("CGI入口须为根目录内的相对文件名")
		}
		if r.CGITimeoutSeconds < 0 || r.CGITimeoutSeconds > 60 {
			return fmt.Errorf("CGI执行超时须为0到60秒，0使用15秒")
		}
	}
	if len(r.ProxyErrorBody) > 65536 || len(r.ResponseHeaders) > 64 {
		return fmt.Errorf("代理错误正文或响应头超过容量")
	}
	for k, v := range r.ResponseHeaders {
		if !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(v) || len(v) > 8192 {
			return fmt.Errorf("响应头无效")
		}
		switch strings.ToLower(k) {
		case "connection", "transfer-encoding", "content-length", "upgrade", "trailer", "set-cookie", "proxy-authenticate", "proxy-authorization", "keep-alive", "te":
			return fmt.Errorf("响应头不可覆盖协议控制字段或Cookie")
		}
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			return fmt.Errorf("跨域响应头须使用CORS配置")
		}
	}
	if p := r.PathRewritePrefix; p != "" {
		if len(p) > 2048 || !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\?#\r\n") || strings.Contains(p, "//") {
			return fmt.Errorf("路径重写前缀无效")
		}
		for _, seg := range strings.Split(p, "/") {
			if seg == "." || seg == ".." {
				return fmt.Errorf("路径重写不可包含相对路径")
			}
		}
	}
	c := r.CORS
	if c == nil {
		return nil
	}
	if c.MaxAge < 0 || c.MaxAge > 86400 || len(c.Origins) > 64 || len(c.Methods) > 16 || len(c.Headers) > 64 {
		return fmt.Errorf("CORS参数超出范围")
	}
	if c.Enabled && (len(c.Origins) == 0 || len(c.Methods) == 0) {
		return fmt.Errorf("CORS须有允许来源和方法")
	}
	for _, o := range c.Origins {
		if o == "*" {
			if c.Credentials {
				return fmt.Errorf("带凭据的CORS不能使用通配来源")
			}
			continue
		}
		u, err := url.Parse(o)
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("CORS来源须为协议和主机，不含路径")
		}
	}
	for _, m := range c.Methods {
		if !httpguts.ValidHeaderFieldName(m) || strings.ToUpper(m) != m {
			return fmt.Errorf("CORS方法须为大写HTTP方法")
		}
	}
	for _, h := range c.Headers {
		if !httpguts.ValidHeaderFieldName(h) {
			return fmt.Errorf("CORS允许头名称无效")
		}
	}
	return nil
}

// A business WebDAV writer must not be able to replace a CGI executable.
func validateServiceRoots(sites []Site) error {
	var writable, scripts []string
	for _, site := range sites {
		for _, rule := range site.Rules {
			if rule.Type == "webdav" && rule.WebDAVWritable {
				writable = append(writable, filepath.Clean(rule.RootDir))
			}
			if rule.Type == "cgi" {
				scripts = append(scripts, filepath.Clean(rule.RootDir))
			}
		}
	}
	for _, root := range writable {
		for _, script := range scripts {
			rel, err := filepath.Rel(root, script)
			if err == nil && (rel == "." || filepath.IsLocal(rel)) {
				return fmt.Errorf("CGI目录不可置于可写WebDAV目录中")
			}
		}
	}
	return nil
}
