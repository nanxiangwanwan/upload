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
# upload 配置文件 / Configuration file
# ==========================================

# HTTP 监听地址，默认 :8080。
# HTTP listen address. Default: :8080.
ADDR=:8080

# MD5 签名密钥【必填】。
# MD5 signing secret [required].
#
# 上传签名 / Upload signature:
#   RESSIGN = md5(MD5_KEY + RESTIME)
#   RESTIME = 上传请求时间（Unix 秒级时间戳）
#   RESTIME = upload request time (Unix timestamp in seconds)
#
# 资源访问签名 / Resource access signature:
#   RESSIGN = md5(MD5_KEY + RESTIME)
#   RESTIME = 访问凭证过期时间（Unix 秒级时间戳）
#   RESTIME = access-token expiration time (Unix timestamp in seconds)
#   同一组 RESTIME + RESSIGN 在过期前可访问全部 /res/* 资源。
#   The same RESTIME + RESSIGN can access all /res/* resources until expiration.
MD5_KEY=请修改为你的密钥

# 仅用于上传接口：RESTIME 与服务器当前时间允许的最大误差，单位秒。默认 300 秒。
# Upload only: maximum allowed clock skew for RESTIME, in seconds. Default: 300.
# 资源访问不使用此配置；资源 RESTIME 本身就是过期时间。
# Resource access does not use this setting; its RESTIME is the expiration time itself.
TIME_EXPIRE=300

# 允许上传的文件扩展名，英文逗号分隔。
# Allowed upload extensions, comma-separated.
# 默认 * 表示允许全部扩展名。/ Default * allows all extensions.
# 示例 / Example: .jpg,.jpeg,.png,.gif,.pdf
UPLOAD_TYPES=*

# 单个上传文件大小上限，单位：字节。
# Maximum size of one uploaded file, in bytes.
# 默认 1048576 字节 = 1 MiB。/ Default: 1048576 bytes = 1 MiB.
MAX_UPLOAD_SIZE=1048576

# 上传接口 IP 白名单，多个 IP 使用英文逗号分隔。
# Upload-endpoint IP whitelist, comma-separated.
# 仅 /upload 验证；/res/* 静态资源不验证 IP 白名单。
# Applied only to /upload; /res/* does not enforce the IP whitelist.
# 留空表示上传接口不限制 IP。/ Empty means no IP restriction for uploads.
# 示例 / Example: 127.0.0.1,192.168.1.10,10.0.0.8
IP_WHITELIST=

# 是否信任反向代理传来的客户端 IP，仅影响上传接口白名单判断。
# Whether to trust reverse-proxy client-IP headers; affects upload whitelist checks only.
# 默认 false，仅在你控制的可信代理后开启。
# Default: false. Enable only behind a trusted proxy you control.
TRUST_PROXY=false

# 资源目录无需配置。
# Resource directory is not configurable.
# 启动目录 = os.Getwd()，真实资源根目录固定为：<启动目录>/res
# Startup directory = os.Getwd(); actual resource root is always: <startup-directory>/res
# 对外 URL 始终使用 /res/...。
# Public URLs always use /res/....`

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
	cfgPath := fs.String("config", ".env", "path to .env config file")
	showVersion := fs.Bool("version", false, "show version")
	_ = fs.Parse(args)

	if *showVersion {
		fmt.Printf("upload v%s\n", version)
		return
	}

	env, err := loadEnv(*cfgPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Fatalf("load config: %v", err)
	}

	cfg, err := buildConfig(env)
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	if err := os.MkdirAll(cfg.Root, 0755); err != nil {
		log.Fatalf("create resource root: %v", err)
	}

	app := &App{cfg: cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", app.handleUpload)
	mux.HandleFunc("/res/", app.handleResource)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Response{Code: 0, Message: "ok"})
	})

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           requestLog(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Printf("upload v%s listening on %s", version, cfg.Addr)
	log.Printf("resource root: %s", cfg.Root)
	log.Printf("max upload size: %d bytes", cfg.MaxUploadSize)
	if cfg.AllowAllTypes {
		log.Printf("upload types: all")
	}
	log.Fatal(server.ListenAndServe())
}

func printConfigHelp() {
	fmt.Printf(`go-upload 配置参数说明

命令：
  upload start [-config .env]   启动服务
  upload config                打印中文配置说明和默认配置
  upload init [文件路径]        生成带中英文注释的默认配置，默认生成 .env
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
  过期      返回 HTTP 401，内容 resource access expired

目录规则：启动目录 = os.Getwd()；资源根目录固定为 <启动目录>/res。
例如在 /www/upload 执行 upload，则真实资源目录为 /www/upload/res。
对外访问路径固定使用 /res/...，不会暴露服务器绝对路径。

默认配置：
%s`, defaultEnvTemplate)
}

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

func buildConfig(env map[string]string) (Config, error) {
	// Resource storage is always tied to the directory where the service is started,
	// not to the location of the installed executable.
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
		return Config{}, errors.New("MD5_KEY is required")
	}

	maxSize := int64(1024 * 1024)
	if v := strings.TrimSpace(env["MAX_UPLOAD_SIZE"]); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, errors.New("MAX_UPLOAD_SIZE must be a positive integer in bytes")
		}
		maxSize = n
	}

	expire := int64(300)
	if v := strings.TrimSpace(env["TIME_EXPIRE"]); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return Config{}, errors.New("TIME_EXPIRE must be a positive integer in seconds")
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

func (a *App) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, Response{Code: 405, Message: "method not allowed"})
		return
	}
	// IP whitelist is checked before authentication/body parsing to reject unauthorized clients early.
	if !a.allowedIP(r) {
		writeJSON(w, http.StatusForbidden, Response{Code: 403, Message: "ip not allowed"})
		return
	}

	resTime := strings.TrimSpace(r.Header.Get("RESTIME"))
	resSign := strings.TrimSpace(r.Header.Get("RESSIGN"))
	rawDir := strings.TrimSpace(r.Header.Get("RESPATH"))

	// Upload and resource access use the same signature input: md5(MD5_KEY + RESTIME).
	// RESPATH only selects the storage directory and never participates in signing.
	if !a.verifyUploadSign(resTime, resSign) {
		writeJSON(w, http.StatusUnauthorized, Response{Code: 401, Message: "invalid or expired signature"})
		return
	}

	// Only after authentication do we normalize/validate the directory for safe storage.
	signDir := ""
	if rawDir != "" {
		var err error
		signDir, _, err = safeTarget(a.cfg.Root, rawDir)
		if err != nil || signDir == "" {
			writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "invalid RESPATH directory"})
			return
		}
	}

	r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxUploadSize+1024*1024)
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "multipart field 'file' is required"})
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(filepath.Base(header.Filename)))
	if !a.cfg.AllowAllTypes && !a.cfg.UploadTypes[ext] {
		writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "file type not allowed"})
		return
	}

	targetDir := signDir
	if targetDir == "" {
		// RESTIME has already been validated as a Unix timestamp in seconds.
		targetDir = resTime
	}
	cleanDir, absDir, err := safeTarget(a.cfg.Root, targetDir)
	if err != nil || cleanDir == "" {
		writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "invalid target directory"})
		return
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, Response{Code: 500, Message: "create target directory failed"})
		return
	}

	name, err := randomFileName(ext)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, Response{Code: 500, Message: "generate file name failed"})
		return
	}
	cleanRel := filepath.ToSlash(filepath.Join(cleanDir, name))
	target := filepath.Join(absDir, name)

	n, err := saveLimited(target, file, a.cfg.MaxUploadSize)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			_ = os.Remove(target)
			writeJSON(w, http.StatusRequestEntityTooLarge, Response{Code: 413, Message: "file too large"})
			return
		}
		_ = os.Remove(target)
		writeJSON(w, http.StatusInternalServerError, Response{Code: 500, Message: "save file failed"})
		return
	}

	publicPath := "/res/" + cleanRel
	writeJSON(w, http.StatusOK, Response{Code: 0, Message: "success", Data: map[string]interface{}{
		"path": publicPath,
		"size": n,
		"url":  publicPath,
	}})
}

func randomFileName(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ext, nil
}

var errTooLarge = errors.New("file too large")

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

func (a *App) handleResource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	// Resource access is intentionally independent from upload authentication.
	// RESTIME is the expiration timestamp; one valid signature can access all /res/* paths until it expires.
	if err := a.verifyResourceSign(resTime, resSign); err != nil {
		if errors.Is(err, errResourceExpired) {
			http.Error(w, "resource access expired", http.StatusUnauthorized)
			return
		}
		http.Error(w, "invalid resource signature", http.StatusUnauthorized)
		return
	}

	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, target)
}

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

var errResourceExpired = errors.New("resource access expired")
var errInvalidResourceSignature = errors.New("invalid resource signature")

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

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func safeTarget(root, input string) (string, string, error) {
	input = strings.TrimSpace(strings.ReplaceAll(input, "\\", "/"))
	input = strings.TrimPrefix(input, "/")
	clean := filepath.ToSlash(filepath.Clean(input))
	if clean == "." {
		clean = ""
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || filepath.IsAbs(input) {
		return "", "", errors.New("RESPATH must be inside resource root")
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
		return "", "", errors.New("RESPATH must be inside resource root")
	}
	return filepath.ToSlash(rel), absTarget, nil
}

func (a *App) allowedIP(r *http.Request) bool {
	if len(a.cfg.IPWhitelist) == 0 {
		return true
	}
	ip := clientIP(r, a.cfg.TrustProxy)
	return a.cfg.IPWhitelist[ip]
}

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

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s %s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}
