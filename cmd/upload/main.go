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

const version = "1.8.3"

type App struct{ cfg Config }

type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printOverview(".env")
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
		printOverview(".env")
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


func printRouteList(configPath string) {
	uploadPath := "/upload"
	env, err := loadEnv(configPath)
	if err == nil {
		if p, e := resolveUploadPath(env); e == nil {
			uploadPath = p
		} else {
			fmt.Printf("配置错误：%v\n\n", e)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Printf("读取配置失败：%v\n\n", err)
	}

	fmt.Printf("go-upload v%s 路由列表\n\n", version)
	fmt.Printf("%-16s 上传图片\n", uploadPath)
	fmt.Printf("%-16s 资源路由\n", "/res/*")
	fmt.Printf("%-16s 状态检查\n", "/health")
}


func printOverview(configPath string) {
	printRouteList(configPath)
	fmt.Println()
	printCommandHelp()
	fmt.Println()
	printConfigHelp()
}

func printCommandHelp() {
	fmt.Printf("go-upload v%s 命令列表\n\n", version)
	fmt.Printf("%-28s %s\n", "upload", "显示路由、命令和配置说明")
	fmt.Printf("%-28s %s\n", "upload start [-config .env]", "启动服务")
	fmt.Printf("%-28s %s\n", "upload config", "显示配置说明")
	fmt.Printf("%-28s %s\n", "upload init [文件路径]", "生成默认配置文件")
	fmt.Printf("%-28s %s\n", "upload version", "查看版本")
}
