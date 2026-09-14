package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const version = "1.8.0"

type App struct{ cfg Config }

type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printConfigHelp()
		return
	}
	switch args[0] {
	case "version":
		fmt.Printf("upload v%s\n", version)
		return
	case "config":
		printConfigHelp()
		return
	case "init":
		p := ".env"
		if len(args) > 1 && strings.TrimSpace(args[1]) != "" {
			p = args[1]
		}
		if err := writeDefaultConfig(p); err != nil {
			log.Fatalf("生成配置失败: %v", err)
		}
		fmt.Printf("已生成默认配置: %s\n", p)
		fmt.Println("请先修改 MD5_KEY，然后执行: upload start -config " + p)
		return
	case "start":
		args = args[1:]
	default:
		printConfigHelp()
		return
	}

	fs := flag.NewFlagSet("upload", flag.ExitOnError)
	cfgPath := fs.String("config", ".env", "配置文件路径")
	showVersion := fs.Bool("version", false, "显示版本号")
	_ = fs.Parse(args)
	if *showVersion {
		fmt.Printf("upload v%s\n", version)
		return
	}

	env, err := loadEnv(*cfgPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("加载配置失败: %v", err)
	}
	cfg, err := buildConfig(env)
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}
	if err := os.MkdirAll(cfg.Root, 0755); err != nil {
		log.Fatalf("创建资源目录失败: %v", err)
	}

	app := &App{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc(cfg.UploadPath, app.handleUpload)
	mux.HandleFunc("/res/", app.handleResource)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Response{Code: 0, Message: "正常"})
	})

	server := &http.Server{
		Addr: cfg.Addr, Handler: requestLog(cors(mux)),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	log.Printf("upload v%s 正在监听 %s", version, cfg.Addr)
	log.Printf("上传接口: %s", cfg.UploadPath)
	log.Printf("资源根目录: %s", cfg.Root)
	log.Printf("默认最大上传大小: %d 字节", cfg.MaxUploadSize)
	log.Fatal(server.ListenAndServe())
}
