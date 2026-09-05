package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"andey-proxy/internal/acme"
	"andey-proxy/internal/adminweb"
	"andey-proxy/internal/api"
	"andey-proxy/internal/auth"
	"andey-proxy/internal/config"
	"andey-proxy/internal/configlock"
	"andey-proxy/internal/dashboard"
	"andey-proxy/internal/ddns"
	"andey-proxy/internal/firewall"
	"andey-proxy/internal/forward"
	"andey-proxy/internal/logcenter"
	"andey-proxy/internal/notify"
	"andey-proxy/internal/upgrade"
	"andey-proxy/internal/webproxy"
)

var version = "0.2.2"

func main() {
	confDir := flag.String("cd", "", "配置文件夹路径（默认 ./andey-proxy-conf）")
	port := flag.Int("p", 16601, "后台管理端口")
	listen := flag.String("listen", "", "后台管理绑定地址（默认全部网卡）")
	showVersion := flag.Bool("v", false, "显示版本号")
	allowHTTP := flag.Bool("admin-http", false, "允许管理后台使用明文 HTTP（不推荐）")
	resetTOTP := flag.Bool("reset-totp", false, "从设备本机关闭 Google Authenticator 后退出（须先停止服务）")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	dir := *confDir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			log.Fatalf("获取工作目录失败: %v", err)
		}
		dir = filepath.Join(wd, "andey-proxy-conf")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		log.Fatalf("配置目录路径无效: %v", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		log.Fatalf("创建配置目录失败: %v", err)
	}
	if *resetTOTP {
		if _, err := os.Stat(filepath.Join(abs, "config.json")); err != nil {
			log.Fatalf("重置双重验证失败，配置文件不存在: %v", err)
		}
	}
	instanceLock, err := configlock.Acquire(abs)
	if err != nil {
		log.Fatalf("锁定配置目录失败: %v", err)
	}
	defer instanceLock.Close()

	cfg, err := config.Load(abs)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if *resetTOTP {
		if err := cfg.Update(func(c *config.Config) error {
			c.Settings.TOTPEnabled = false
			c.Settings.TOTPSecret = ""
			c.Settings.TOTPRecoveryHashes = nil
			return nil
		}); err != nil {
			log.Fatalf("重置双重验证失败: %v", err)
		}
		if center, err := logcenter.New(abs); err == nil {
			center.Add(logcenter.Entry{Time: time.Now(), Level: "warn", Source: "security", Message: "通过设备本机命令关闭了 Google Authenticator"})
			_ = center.Close()
		}
		fmt.Fprintln(os.Stderr, "Google Authenticator 已关闭；请重新启动 andey-proxy")
		return
	}
	if cfg.Settings.AdminPassHash == "" {
		initialPassword, err := auth.RandomPassword()
		if err != nil {
			log.Fatalf("生成初始密码失败: %v", err)
		}
		hash, err := auth.HashPassword(initialPassword)
		if err != nil {
			log.Fatalf("加密初始密码失败: %v", err)
		}
		if err := cfg.Update(func(c *config.Config) error {
			if c.Settings.AdminUser == "" || c.Settings.AdminUser == "666" {
				c.Settings.AdminUser = "admin"
			}
			c.Settings.AdminPassHash = hash
			c.Settings.MustChangePassword = true
			return nil
		}); err != nil {
			log.Fatalf("保存初始密码失败: %v", err)
		}
		// 一次性密码只写 stderr，不进入持久化日志中心。
		fmt.Fprintf(os.Stderr, "首次启动管理员: %s，一次性密码: %s（仅显示本次）\n", cfg.Settings.AdminUser, initialPassword)
	}
	logs, err := logcenter.New(abs)
	if err != nil {
		log.Fatalf("初始化日志中心失败: %v", err)
	}
	defer logs.Close()
	logcenter.SetDefault(logs)
	// Center 会先统一脱敏，再写 stderr 和磁盘，避免标准日志旁路泄露凭据。
	log.SetOutput(logs)

	// 模块服务
	acmeMgr := acme.NewManager(cfg)
	webSvc := webproxy.NewService(cfg, acmeMgr.GetCertificate)
	fwdSvc := forward.NewService(cfg)
	ddnsWorker := ddns.NewWorker(cfg)
	updateMgr := upgrade.NewManager(version, cfg.Dir())

	// 事件总线 + Webhook 推送：各模块通过 notify.Publish 上报事件
	notifyBus := notify.NewBus()
	notify.SetDefault(notifyBus)
	notifyWebhook := notify.NewWebhook(cfg)
	notifyBus.Subscribe(notifyWebhook.Handle)

	// 防火墙自动放行（OpenWrt）
	fwMgr := firewall.NewManager()
	webSvc.FW = fwMgr
	fwdSvc.FW = fwMgr

	acmeMgr.Start()
	webSvc.Start()
	fwdSvc.Start()
	ddnsWorker.Start()
	updateMgr.MarkStarted()

	apiSrv := api.NewServer(cfg, !*allowHTTP)
	// 备份导入后的热重载：webproxy/forward 显式 Reload，DDNS worker 重排任务。
	// acme 没有 Reload，由周期扫描与证书 mtime 缓存自动感知。
	apiSrv.SetConfigRestore(version, func() {
		if err := webSvc.Reload(); err != nil {
			log.Printf("导入配置后重载 Web 服务失败: %v", err)
		}
		if err := fwdSvc.Reload(); err != nil {
			log.Printf("导入配置后重载端口转发失败: %v", err)
		}
		ddnsWorker.Reload()
	})
	apiSrv.Mount(func(r chi.Router) { ddns.RegisterRoutes(r, cfg, ddnsWorker) })
	apiSrv.Mount(func(r chi.Router) { forward.RegisterRoutes(r, cfg, fwdSvc) })
	apiSrv.Mount(func(r chi.Router) { webproxy.RegisterRoutes(r, cfg, webSvc) })
	apiSrv.Mount(func(r chi.Router) { acme.RegisterRoutes(r, cfg, acmeMgr) })
	apiSrv.Mount(func(r chi.Router) { firewall.RegisterRoutes(r, fwMgr) })
	apiSrv.Mount(func(r chi.Router) { upgrade.RegisterRoutes(r, updateMgr, cfg) })
	apiSrv.Mount(func(r chi.Router) { logcenter.RegisterRoutes(r, logs, cfg) })
	apiSrv.Mount(func(r chi.Router) { notify.RegisterRoutes(r, cfg, notifyBus, notifyWebhook) })
	apiSrv.Mount(func(r chi.Router) {
		dashboard.RegisterRoutes(r, cfg, ddnsWorker, webSvc, fwdSvc, fwMgr, logs, updateMgr, version, !*allowHTTP)
	})
	mux := http.NewServeMux()
	mux.Handle("/api/", apiSrv.Router())
	mux.Handle("/", adminweb.Handler())

	addr := fmt.Sprintf(":%d", *port)
	if *listen != "" {
		addr = net.JoinHostPort(*listen, strconv.Itoa(*port))
	}
	// 不设 ReadTimeout：它会覆盖整个请求体读取，慢链路上传 100MiB 更新包必然超时；
	// 请求体大小由路由级 MaxBytesReader 控制（普通 API 1MiB，更新包上传 100MiB），
	// ReadHeaderTimeout 保留以防慢速 header 攻击。
	httpSrv := &http.Server{Addr: addr, Handler: managementHeaders(mux, !*allowHTTP), ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20}
	if !*allowHTTP {
		httpSrv.TLSConfig = webSvc.AdminTLSConfig()
	}

	go func() {
		log.Printf("andey-proxy %s 启动，配置目录: %s", version, abs)
		scheme := "https"
		if *allowHTTP {
			scheme = "http"
			log.Printf("警告：管理后台正在使用不安全的 HTTP")
		}
		if *listen == "" || *listen == "0.0.0.0" || *listen == "::" {
			log.Printf("后台管理地址: %s://<设备IP>%s", scheme, addr)
			if !fwMgr.IsOpenWrt() {
				log.Printf("警告：管理后台监听全部网卡，请确认防火墙规则或考虑使用 -listen 绑定内网地址")
			}
		} else {
			log.Printf("后台管理地址: %s://%s", scheme, addr)
		}
		var serveErr error
		if *allowHTTP {
			serveErr = httpSrv.ListenAndServe()
		} else {
			serveErr = httpSrv.ListenAndServeTLS("", "")
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			log.Fatalf("后台服务启动失败: %v", serveErr)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
	ddnsWorker.Stop()
	fwdSvc.Stop()
	webSvc.Stop()
	acmeMgr.Stop()
	log.Println("已退出")
}

func managementHeaders(next http.Handler, secure bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if secure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}
