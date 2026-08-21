package main

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const version = "1.4.0"

const defaultEnvTemplate = `# ==========================================
# 上传配置文件
# ==========================================

# HTTP 监听地址，默认 :8080。
ADDR=:8080

# MD5 签名密钥【必填】。
# 上传签名：RESSIGN = md5(MD5_KEY + RESTIME)
# RESTIME = 上传请求时间（Unix 秒级时间戳）
# 资源访问签名：RESSIGN = md5(MD5_KEY + RESTIME)
# RESTIME = 访问凭证过期时间（Unix 秒级时间戳）
# 同一组 RESTIME + RESSIGN 在过期前可访问全部 /res/* 资源。
MD5_KEY=请修改为你的密钥

# 仅用于上传接口：RESTIME 与服务器当前时间允许的最大误差，单位秒。默认 300 秒。
# 资源访问不使用此配置；资源 RESTIME 本身就是过期时间。
TIME_EXPIRE=300

# 允许上传的文件扩展名，英文逗号分隔。
# 默认 * 表示允许全部扩展名。
# 示例：.jpg,.jpeg,.png,.gif,.pdf
UPLOAD_TYPES=*

# 单个上传文件大小上限，单位：字节。
# 默认 1048576 字节 = 1 MiB。
MAX_UPLOAD_SIZE=1048576

# 上传接口 IP 白名单，多个 IP 使用英文逗号分隔。
# 仅 /upload 验证；/res/* 静态资源不验证 IP 白名单。
# 留空表示上传接口不限制 IP。
# 示例：127.0.0.1,192.168.1.10,10.0.0.8
IP_WHITELIST=

# 是否信任反向代理传来的客户端 IP，仅影响上传接口白名单判断。
# 默认 false，仅在你控制的可信代理后开启。
TRUST_PROXY=false

# 资源目录无需配置。
# 启动目录 = os.Getwd()，真实资源根目录固定为：<启动目录>/res
# 对外 URL 始终使用 /res/...。`

type Config struct {
	Addr          string
	Root          string
	UploadTypes   map[string]bool
	AllowAllTypes bool
	MaxUploadSize int64
	MD5Key        string
	IPWhitelist   map[string]bool
	TimeExpire    int64
	TrustProxy    bool
}

type App struct {
	cfg Config
}

type Response struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// main 是程序入口，负责解析命令行参数并启动 HTTP 服务。
func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Printf("upload v%s\n", version)
			return
		case "config":
			printConfigHelp()
			return
		case "init":
			path := ".env"
			if len(args) > 1 && strings.TrimSpace(args[1]) != "" {
				path = args[1]
			}
			if err := writeDefaultConfig(path); err != nil {
				log.Fatalf("生成配置失败: %v", err)
			}
			fmt.Printf("已生成默认配置: %s\n", path)
			fmt.Println("请先修改 MD5_KEY，然后执行: upload start -config " + path)
			return
		case "start":
			args = args[1:]
		}
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
	mux.HandleFunc("/upload", app.handleUpload)
	mux.HandleFunc("/res/", app.handleResource)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Response{Code: 0, Message: "正常"})
	})

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           requestLog(cors(mux)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("upload v%s 正在监听 %s", version, cfg.Addr)
	log.Printf("资源根目录: %s", cfg.Root)
	log.Printf("最大上传大小: %d 字节", cfg.MaxUploadSize)
	if cfg.AllowAllTypes {
		log.Printf("允许上传类型: 全部")
	}
	log.Fatal(server.ListenAndServe())
}

// printConfigHelp 打印配置说明和示例命令。
func printConfigHelp() {
	fmt.Printf(`go-upload 配置参数说明

命令：
  upload start [-config .env]   启动服务
  upload config                打印配置说明和默认配置
  upload init [文件路径]        生成默认配置文件，默认生成 .env
  upload version               查看版本

上传 /upload：
  RESTIME   上传请求时间，Unix 秒级时间戳
  RESPATH   上传目标目录，可选，例如 users/avatar
  RESSIGN   md5(MD5_KEY + RESTIME)
  校验      IP 白名单 + TIME_EXPIRE 时间误差 + 签名

资源 /res/*：
  RESTIME   访问凭证的过期时间，Unix 秒级时间戳
  RESSIGN   md5(MD5_KEY + RESTIME)
  校验      过期时间 + 签名；不校验 IP 白名单
  特点      同一组 RESTIME + RESSIGN 在过期前可访问全部 /res/* 资源
  参数      支持 Header RESTIME/RESSIGN，也支持 ?time=...&sign=...
  过期      返回 HTTP 401，内容为 资源访问已过期

目录规则：启动目录 = os.Getwd()；资源根目录固定为 <启动目录>/res。
例如在 /www/upload 执行 upload，则真实资源目录为 /www/upload/res。
对外访问路径固定使用 /res/...，不会暴露服务器绝对路径。

默认配置：
%s`, defaultEnvTemplate)
}

// writeDefaultConfig 将默认环境配置写入指定路径，并避免覆盖已存在的文件。
func writeDefaultConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("文件 %s 已存在，为避免覆盖请先删除或指定其他路径", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(defaultEnvTemplate), 0600)
}

// buildConfig 根据环境变量构建应用配置，并校验关键参数。
func buildConfig(env map[string]string) (Config, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}
	root := filepath.Join(wd, "res")
	root, err = filepath.Abs(root)
	if err != nil {
		return Config{}, err
	}

	key := strings.TrimSpace(env["MD5_KEY"])
	if key == "" {
		return Config{}, errors.New("MD5_KEY 是必填项")
	}

	maxSize := int64(1024 * 1024)
	if v := strings.TrimSpace(env["MAX_UPLOAD_SIZE"]); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, errors.New("MAX_UPLOAD_SIZE 必须是大于 0 的整数，单位为字节")
		}
		maxSize = n
	}

	expire := int64(300)
	if v := strings.TrimSpace(env["TIME_EXPIRE"]); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, errors.New("TIME_EXPIRE 必须是大于 0 的整数，单位为秒")
		}
		expire = n
	}

	types, all := parseTypes(env["UPLOAD_TYPES"])
	ips := parseCSVSet(env["IP_WHITELIST"])

	addr := strings.TrimSpace(env["ADDR"])
	if addr == "" {
		addr = ":8080"
	}

	trustProxy := strings.EqualFold(strings.TrimSpace(env["TRUST_PROXY"]), "true") || strings.TrimSpace(env["TRUST_PROXY"]) == "1"

	return Config{
		Addr:          addr,
		Root:          root,
		UploadTypes:   types,
		AllowAllTypes: all,
		MaxUploadSize: maxSize,
		MD5Key:        key,
		IPWhitelist:   ips,
		TimeExpire:    expire,
		TrustProxy:    trustProxy,
	}, nil
}

// parseTypes 解析上传文件类型列表，支持逗号分隔和通配符。
func parseTypes(v string) (map[string]bool, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "*" {
		return map[string]bool{}, true
	}
	out := map[string]bool{}
	for _, item := range strings.Split(v, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if !strings.HasPrefix(item, ".") {
			item = "." + item
		}
		out[item] = true
	}
	return out, len(out) == 0
}

// parseCSVSet 解析逗号分隔的集合字符串，并返回可直接查询的字典。
func parseCSVSet(v string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.Split(v, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = true
		}
	}
	return out
}

// loadEnv 从 .env 文件中加载环境变量，并忽略空行和注释。
func loadEnv(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, nil
}

// handleUpload 处理上传接口，校验 IP、签名、目录和文件类型，然后保存文件。
func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, Response{Code: 405, Message: "方法不允许"})
		return
	}
	if !a.allowedIP(r) {
		writeJSON(w, http.StatusForbidden, Response{Code: 403, Message: "IP 不在白名单中"})
		return
	}

	resTime := strings.TrimSpace(r.Header.Get("RESTIME"))
	resSign := strings.TrimSpace(r.Header.Get("RESSIGN"))
	rawDir := strings.TrimSpace(r.Header.Get("RESPATH"))

	if !a.verifyUploadSign(resTime, resSign) {
		writeJSON(w, http.StatusUnauthorized, Response{Code: 401, Message: "签名无效或已过期"})
		return
	}

	signDir := ""
	if rawDir != "" {
		var err error
		signDir, _, err = safeTarget(a.cfg.Root, rawDir)
		if err != nil || signDir == "" {
			writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "RESPATH 目录无效"})
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadSize+1024*1024)
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytesErr):
			writeJSON(w, http.StatusRequestEntityTooLarge, Response{Code: 413, Message: "文件过大"})
		case errors.Is(err, http.ErrMissingFile):
			writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "表单字段 'file' 是必填项"})
		default:
			log.Printf("解析上传 multipart 表单失败: %v", err)
			writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "非法的 multipart 表单"})
		}
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(filepath.Base(header.Filename)))
	if !a.cfg.AllowAllTypes && !a.cfg.UploadTypes[ext] {
		writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "文件类型不允许"})
		return
	}

	targetDir := signDir
	if targetDir == "" {
		targetDir = resTime
	}
	cleanDir, absDir, err := safeTarget(a.cfg.Root, targetDir)
	if err != nil || cleanDir == "" {
		writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "目标目录无效"})
		return
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, Response{Code: 500, Message: "创建目标目录失败"})
		return
	}

	name, err := randomFileName(ext)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, Response{Code: 500, Message: "生成文件名失败"})
		return
	}
	cleanRel := filepath.ToSlash(filepath.Join(cleanDir, name))
	target := filepath.Join(absDir, name)

	n, err := saveLimited(target, file, a.cfg.MaxUploadSize)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			_ = os.Remove(target)
			writeJSON(w, http.StatusRequestEntityTooLarge, Response{Code: 413, Message: "文件过大"})
			return
		}
		_ = os.Remove(target)
		writeJSON(w, http.StatusInternalServerError, Response{Code: 500, Message: "保存文件失败"})
		return
	}

	publicPath := "/res/" + cleanRel
	writeJSON(w, http.StatusOK, Response{Code: 0, Message: "成功", Data: map[string]interface{}{
		"path": publicPath,
		"size": n,
		"url":  publicPath,
	}})
}

// randomFileName 生成随机文件名，并保留原始扩展名。
func randomFileName(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ext, nil
}

var errTooLarge = errors.New("文件过大")

// saveLimited 将读取流写入目标文件，并限制最大文件大小。
func saveLimited(target string, src multipart.File, max int64) (int64, error) {
	dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return 0, err
	}
	defer dst.Close()

	lr := &io.LimitedReader{R: src, N: max + 1}
	n, err := io.Copy(dst, lr)
	if err != nil {
		return n, err
	}
	if n > max {
		return n, errTooLarge
	}
	return n, nil
}

// handleResource 处理静态资源访问，校验签名和过期时间。
func (a *App) handleResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	relFromURL := strings.TrimPrefix(r.URL.Path, "/res/")
	cleanRel, target, err := safeTarget(a.cfg.Root, relFromURL)
	if err != nil || cleanRel == "" {
		http.NotFound(w, r)
		return
	}

	resTime := strings.TrimSpace(r.Header.Get("RESTIME"))
	resSign := strings.TrimSpace(r.Header.Get("RESSIGN"))
	if resTime == "" {
		resTime = strings.TrimSpace(r.URL.Query().Get("time"))
	}
	if resSign == "" {
		resSign = strings.TrimSpace(r.URL.Query().Get("sign"))
	}

	if err := a.verifyResourceSign(resTime, resSign); err != nil {
		if errors.Is(err, errResourceExpired) {
			http.Error(w, "资源访问已过期", http.StatusUnauthorized)
			return
		}
		http.Error(w, "资源签名无效", http.StatusUnauthorized)
		return
	}

	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, target)
}

// verifyUploadSign 校验上传请求中的时间戳和签名是否在允许范围内。
func (a *App) verifyUploadSign(ts, sign string) bool {
	if ts == "" || sign == "" {
		return false
	}
	unixTs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now().Unix()
	if unixTs < now-a.cfg.TimeExpire || unixTs > now+a.cfg.TimeExpire {
		return false
	}
	expected := md5Hex(a.cfg.MD5Key + ts)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(expected)), []byte(strings.ToLower(sign))) == 1
}

var errResourceExpired = errors.New("资源访问已过期")
var errInvalidResourceSignature = errors.New("资源签名无效")

// verifyResourceSign 校验资源访问凭证的过期时间和签名。
func (a *App) verifyResourceSign(expireTs, sign string) error {
	if expireTs == "" || sign == "" {
		return errInvalidResourceSignature
	}
	unixTs, err := strconv.ParseInt(expireTs, 10, 64)
	if err != nil {
		return errInvalidResourceSignature
	}
	if time.Now().Unix() > unixTs {
		return errResourceExpired
	}
	expected := md5Hex(a.cfg.MD5Key + expireTs)
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(expected)), []byte(strings.ToLower(sign))) != 1 {
		return errInvalidResourceSignature
	}
	return nil
}

// md5Hex 计算字符串的 MD5 十六进制值。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// safeTarget 清洗并校验输入路径，确保其落在资源根目录内。
func safeTarget(root, input string) (string, string, error) {
	input = strings.TrimSpace(strings.ReplaceAll(input, "\\", "/"))
	input = strings.TrimPrefix(input, "/")
	clean := filepath.ToSlash(filepath.Clean(input))
	if clean == "." {
		clean = ""
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(input) {
		return "", "", errors.New("RESPATH 必须位于资源根目录之内")
	}

	target := filepath.Join(root, filepath.FromSlash(clean))
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", errors.New("RESPATH 必须位于资源根目录之内")
	}
	return filepath.ToSlash(rel), absTarget, nil
}

// allowedIP 判断请求 IP 是否来自允许的白名单。
func (a *App) allowedIP(r *http.Request) bool {
	if len(a.cfg.IPWhitelist) == 0 {
		return true
	}
	ip := clientIP(r, a.cfg.TrustProxy)
	return a.cfg.IPWhitelist[ip]
}

// clientIP 提取客户端真实 IP，必要时支持反向代理头。
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			return xri
		}
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				xff = xff[:i]
			}
			return strings.TrimSpace(xff)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.Trim(r.RemoteAddr, "[]")
}

// writeJSON 统一写出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// cors 为接口启用 CORS，允许跨域访问以及预检请求。
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Expose-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requestLog 记录每次 HTTP 请求的状态和耗时。
func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s %s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}
