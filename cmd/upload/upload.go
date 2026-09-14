package main

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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
	resType := strings.TrimSpace(r.Header.Get("RESTYPE"))
	resSize := strings.TrimSpace(r.Header.Get("RESSIZE"))
	rawDir := strings.TrimSpace(r.Header.Get("RESPATH"))

	if a.cfg.RequireTypeSize && (resType == "" || resSize == "") {
		writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "RESTYPE 和 RESSIZE 必须同时携带"})
		return
	}
	if !a.verifyUploadSign(resTime, resSign, resType, resSize) {
		writeJSON(w, http.StatusUnauthorized, Response{Code: 401, Message: "签名无效或已过期"})
		return
	}

	maxSize := a.cfg.MaxUploadSize
	if resSize != "" {
		n, err := strconv.ParseInt(resSize, 10, 64)
		if err != nil || n <= 0 {
			writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "RESSIZE 必须是大于 0 的整数，单位为字节"})
			return
		}
		maxSize = n
	}
	uploadTypes, allowAll := a.cfg.UploadTypes, a.cfg.AllowAllTypes
	if resType != "" {
		uploadTypes, allowAll = parseTypes(resType)
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

	r.Body = http.MaxBytesReader(w, r.Body, maxSize+1024*1024)
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytesErr):
			writeJSON(w, http.StatusRequestEntityTooLarge, Response{Code: 413, Message: "文件过大"})
		case errors.Is(err, http.ErrMissingFile):
			writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "表单字段 'file' 是必填项"})
		default:
			writeJSON(w, http.StatusBadRequest, Response{Code: 400, Message: "非法的 multipart 表单"})
		}
		return
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(filepath.Base(header.Filename)))
	if !allowAll && !uploadTypes[ext] {
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
	n, err := saveLimited(target, file, maxSize)
	if err != nil {
		_ = os.Remove(target)
		if errors.Is(err, errTooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, Response{Code: 413, Message: "文件过大"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, Response{Code: 500, Message: "保存文件失败"})
		return
	}
	publicPath := "/res/" + cleanRel
	writeJSON(w, http.StatusOK, Response{Code: 0, Message: "成功", Data: map[string]interface{}{"path": publicPath, "size": n, "url": publicPath}})
}

func (a *App) verifyUploadSign(ts, sign, resType, resSize string) bool {
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
	expected := md5Hex(a.cfg.MD5Key + ts + resType + resSize)
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(expected)), []byte(strings.ToLower(sign))) == 1
}

func randomFileName(ext string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ext, nil
}

var errTooLarge = errors.New("文件过大")

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

func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
