package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const defaultEnvTemplate = `# ==========================================
# 上传配置文件
# ==========================================
ADDR=:8080

# 上传接口路径，默认 /upload。
UPLOAD_PATH=/upload

# true 时 RESTYPE 和 RESSIZE 必须同时携带。
REQUIRE_TYPE_SIZE=false

# MD5 签名密钥【必填】。
# 上传签名：RESSIGN = md5(MD5_KEY + RESTIME + RESTYPE + RESSIZE)
# RESTYPE/RESSIZE 为空时按空字符串参与拼接。
MD5_KEY=请修改为你的密钥

TIME_EXPIRE=300

# 默认允许类型；RESTYPE 非空时由 RESTYPE 覆盖。
UPLOAD_TYPES=*

# 默认最大字节数；RESSIZE 非空时由 RESSIZE 覆盖。
MAX_UPLOAD_SIZE=1048576

IP_WHITELIST=
TRUST_PROXY=false
`

type Config struct {
	Addr            string
	Root            string
	UploadPath      string
	RequireTypeSize bool
	UploadTypes     map[string]bool
	AllowAllTypes   bool
	MaxUploadSize   int64
	MD5Key          string
	IPWhitelist     map[string]bool
	TimeExpire      int64
	TrustProxy      bool
}

func printConfigHelp() {
	fmt.Printf(`go-upload v%s 配置参数说明

上传接口路径由 UPLOAD_PATH 配置，默认 /upload。
Header:
  RESTIME  Unix 秒级请求时间
  RESSIGN  md5(MD5_KEY + RESTIME + RESTYPE + RESSIZE)
  RESPATH  可选上传目录
  RESTYPE  可选，本次允许的扩展名列表；覆盖 UPLOAD_TYPES
  RESSIZE  可选，本次最大字节数；覆盖 MAX_UPLOAD_SIZE

REQUIRE_TYPE_SIZE=true 时，RESTYPE 和 RESSIZE 必须同时携带。

资源 /res/* 规则保持不变。

默认配置：
%s`, version, defaultEnvTemplate)
}

func writeDefaultConfig(p string) error {
	if _, err := os.Stat(p); err == nil {
		return fmt.Errorf("文件 %s 已存在，为避免覆盖请先删除或指定其他路径", p)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if dir := filepath.Dir(p); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	return os.WriteFile(p, []byte(defaultEnvTemplate), 0600)
}

func buildConfig(env map[string]string) (Config, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Config{}, err
	}
	root, err := filepath.Abs(filepath.Join(wd, "res"))
	if err != nil {
		return Config{}, err
	}
	key := strings.TrimSpace(env["MD5_KEY"])
	if key == "" {
		return Config{}, errors.New("MD5_KEY 是必填项")
	}

	maxSize := int64(1024 * 1024)
	if v := strings.TrimSpace(env["MAX_UPLOAD_SIZE"]); v != "" {
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n <= 0 {
			return Config{}, errors.New("MAX_UPLOAD_SIZE 必须是大于 0 的整数，单位为字节")
		}
		maxSize = n
	}
	expire := int64(300)
	if v := strings.TrimSpace(env["TIME_EXPIRE"]); v != "" {
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n <= 0 {
			return Config{}, errors.New("TIME_EXPIRE 必须是大于 0 的整数，单位为秒")
		}
		expire = n
	}
	uploadPath := strings.TrimSpace(env["UPLOAD_PATH"])
	if uploadPath == "" {
		uploadPath = "/upload"
	}
	if !strings.HasPrefix(uploadPath, "/") {
		uploadPath = "/" + uploadPath
	}
	uploadPath = filepath.ToSlash(filepath.Clean(uploadPath))
	if uploadPath == "/" || uploadPath == "/health" || uploadPath == "/res" || strings.HasPrefix(uploadPath, "/res/") {
		return Config{}, errors.New("UPLOAD_PATH 不能使用 /、/health、/res 或 /res/*")
	}

	types, all := parseTypes(env["UPLOAD_TYPES"])
	addr := strings.TrimSpace(env["ADDR"])
	if addr == "" {
		addr = ":8080"
	}
	return Config{
		Addr: addr, Root: root, UploadPath: uploadPath,
		RequireTypeSize: parseBool(env["REQUIRE_TYPE_SIZE"]),
		UploadTypes: types, AllowAllTypes: all, MaxUploadSize: maxSize,
		MD5Key: key, IPWhitelist: parseCSVSet(env["IP_WHITELIST"]),
		TimeExpire: expire, TrustProxy: parseBool(env["TRUST_PROXY"]),
	}, nil
}

func parseBool(v string) bool {
	v = strings.TrimSpace(v)
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes") || strings.EqualFold(v, "on")
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

func loadEnv(p string) (map[string]string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
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
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, nil
}
