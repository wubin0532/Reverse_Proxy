package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"luckyx/internal/acme"
	"luckyx/internal/adminweb"
	"luckyx/internal/api"
	"luckyx/internal/config"
	"luckyx/internal/ddns"
	"luckyx/internal/forward"
	"luckyx/internal/webproxy"
)

var version = "dev"

func main() {
	confDir := flag.String("cd", "", "配置文件夹路径（默认 ./luckyxconf）")
	port := flag.Int("p", 16601, "后台管理端口")
	showVersion := flag.Bool("v", false, "显示版本号")
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
		dir = filepath.Join(wd, "luckyxconf")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		log.Fatalf("配置目录路径无效: %v", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		log.Fatalf("创建配置目录失败: %v", err)
	}

	cfg, err := config.Load(abs)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 模块服务
	acmeMgr := acme.NewManager(cfg)
	webSvc := webproxy.NewService(cfg, acmeMgr.GetCertificate)
	fwdSvc := forward.NewService(cfg)
	ddnsWorker := ddns.NewWorker(cfg)

	acmeMgr.Start()
	webSvc.Start()
	fwdSvc.Start()
	ddnsWorker.Start()

	apiSrv := api.NewServer(cfg)
	apiSrv.Mount(func(r chi.Router) { ddns.RegisterRoutes(r, cfg, ddnsWorker) })
	apiSrv.Mount(func(r chi.Router) { forward.RegisterRoutes(r, cfg, fwdSvc) })
	apiSrv.Mount(func(r chi.Router) { webproxy.RegisterRoutes(r, cfg, webSvc) })
	apiSrv.Mount(func(r chi.Router) { acme.RegisterRoutes(r, cfg, acmeMgr) })
	mux := http.NewServeMux()
	mux.Handle("/api/", apiSrv.Router())
	mux.Handle("/", adminweb.Handler())

	addr := fmt.Sprintf(":%d", *port)
	httpSrv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		log.Printf("luckyx %s 启动，配置目录: %s", version, abs)
		log.Printf("后台管理地址: http://<设备IP>%s （默认账号 666 / 密码 666）", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("后台服务启动失败: %v", err)
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
