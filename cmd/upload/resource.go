package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var errResourceExpired = errors.New("资源访问已过期")
var errInvalidResourceSignature = errors.New("资源签名无效")

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
	resData := strings.TrimSpace(r.URL.Query().Get("data"))
	signPath := ""
	if _, exists := r.URL.Query()["path"]; exists {
		signPath = "/res/" + cleanRel
	}
	if err := a.verifyResourceSign(resTime, resSign, resData, signPath); err != nil {
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

func (a *App) verifyResourceSign(expireTs, sign, data, resourcePath string) error {
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
	expected := md5Hex(a.cfg.MD5Key + expireTs + data + resourcePath)
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(expected)), []byte(strings.ToLower(sign))) != 1 {
		return errInvalidResourceSignature
	}
	return nil
}

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

func (a *App) allowedIP(r *http.Request) bool {
	if len(a.cfg.IPWhitelist) == 0 {
		return true
	}
	return a.cfg.IPWhitelist[clientIP(r, a.cfg.TrustProxy)]
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

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s %s", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}
