package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestSafeTarget 验证路径清洗逻辑会保留合法路径，同时拒绝目录穿越。
func TestSafeTarget(t *testing.T) {
	root := t.TempDir()
	rel, target, err := safeTarget(root, "images/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if rel != "images/a.png" {
		t.Fatalf("实际 rel: %q", rel)
	}
	want := filepath.Join(root, "images", "a.png")
	if target != want {
		t.Fatalf("target=%q 期望=%q", target, want)
	}
	if _, _, err := safeTarget(root, "../escape.txt"); err == nil {
		t.Fatal("越界路径应被拒绝")
	}
}

// TestParseTypesDefaultsToAll 验证空配置时默认允许所有扩展名。
func TestParseTypesDefaultsToAll(t *testing.T) {
	_, all := parseTypes("")
	if !all {
		t.Fatal("空 UPLOAD_TYPES 应允许全部类型")
	}
	types, all := parseTypes("jpg,.png")
	if all || !types[".jpg"] || !types[".png"] {
		t.Fatalf("解析结果异常: %#v all=%v", types, all)
	}
}

// TestUploadRejectsNonWhitelistedIP 验证不在白名单中的 IP 会被拒绝。
func TestUploadRejectsNonWhitelistedIP(t *testing.T) {
	app := &App{cfg: Config{IPWhitelist: map[string]bool{"127.0.0.1": true}}}
	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.RemoteAddr = "10.0.0.9:54321"
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("状态=%d 期望=%d 响应=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
}

// newUploadRequest 构造带文件和签名的上传请求，用于测试上传接口行为。
func newUploadRequest(t *testing.T, fileName string, content []byte, ts, sign, resPath string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("RESTIME", ts)
	req.Header.Set("RESSIGN", sign)
	if resPath != "" {
		req.Header.Set("RESPATH", resPath)
	}
	req.RemoteAddr = "127.0.0.1:12345"
	return req
}

// testApp 创建测试用 App 实例，提供固定配置用于 HTTP 行为测试。
func testApp(root string) *App {
	return &App{cfg: Config{
		Root:          root,
		AllowAllTypes: true,
		MaxUploadSize: 1024 * 1024,
		MD5Key:        "test-key",
		IPWhitelist:   map[string]bool{"127.0.0.1": true},
		TimeExpire:    300,
	}}
}

// decodeUploadPath 从上传响应中解析返回的资源访问路径。
func decodeUploadPath(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Code int `json:"code"`
		Data struct {
			Path string `json:"path"`
			URL  string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("解码响应失败: %v body=%s", err, rr.Body.String())
	}
	if out.Code != 0 || out.Data.Path == "" {
		t.Fatalf("响应异常: %s", rr.Body.String())
	}
	return out.Data.Path
}

// TestUploadUsesResPathAsDirectoryAndRandomName 验证 RESPATH 会作为存储目录，并生成随机文件名。
func TestUploadUsesResPathAsDirectoryAndRandomName(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	dir := "users/avatar"
	sign := md5Hex(app.cfg.MD5Key + ts)
	req := newUploadRequest(t, "photo.JPG", []byte("hello"), ts, sign, dir)
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("状态=%d 响应=%s", rr.Code, rr.Body.String())
	}
	p := decodeUploadPath(t, rr)
	if !regexp.MustCompile(`^/res/users/avatar/[0-9a-f]{32}\.jpg$`).MatchString(p) {
		t.Fatalf("随机路径异常: %q", p)
	}
	rel := strings.TrimPrefix(p, "/res/")
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || string(b) != "hello" {
		t.Fatalf("保存结果异常 err=%v content=%q", err, string(b))
	}
}

// TestUploadReportsRequestBodyTooLarge 验证请求体过大时返回 413 并提示文件过大。
func TestUploadReportsRequestBodyTooLarge(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	app.cfg.MaxUploadSize = 16
	ts := formatUnixNow()
	sign := md5Hex(app.cfg.MD5Key + ts)
	content := bytes.Repeat([]byte("x"), 2*1024*1024)
	req := newUploadRequest(t, "large.png", content, ts, sign, "gameico")
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态=%d 期望=%d 响应=%s", rr.Code, http.StatusRequestEntityTooLarge, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "文件过大") {
		t.Fatalf("响应体异常: %s", rr.Body.String())
	}
}

// TestUploadWithoutResPathUsesRestimeDirectory 验证未指定 RESPATH 时使用 RESTIME 目录。
func TestUploadWithoutResPathUsesRestimeDirectory(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	sign := md5Hex(app.cfg.MD5Key + ts)
	req := newUploadRequest(t, "document.pdf", []byte("pdf"), ts, sign, "")
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("状态=%d 响应=%s", rr.Code, rr.Body.String())
	}
	p := decodeUploadPath(t, rr)
	want := regexp.MustCompile(`^/res/` + regexp.QuoteMeta(ts) + `/[0-9a-f]{32}\.pdf$`)
	if !want.MatchString(p) {
		t.Fatalf("路径=%q 未使用 RESTIME 目录 %q", p, ts)
	}
}

// TestUploadRejectsWrongSignatureForDirectory 验证错误签名会被拒绝。
func TestUploadRejectsWrongSignatureForDirectory(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	req := newUploadRequest(t, "a.png", []byte("x"), ts, "bad", "users/avatar")
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("状态=%d 响应=%s", rr.Code, rr.Body.String())
	}
}

// TestUploadSignatureIncludesResData 验证非空 RESDATA 必须参与上传签名。
func TestUploadSignatureIncludesResData(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	data := "user-1001"
	sign := md5Hex(app.cfg.MD5Key + ts + data)
	req := newUploadRequest(t, "a.png", []byte("x"), ts, sign, "images")
	req.Header.Set("RESDATA", data)
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("状态=%d 响应=%s", rr.Code, rr.Body.String())
	}

	badReq := newUploadRequest(t, "b.png", []byte("x"), ts, md5Hex(app.cfg.MD5Key+ts), "images")
	badReq.Header.Set("RESDATA", data)
	badRR := httptest.NewRecorder()
	app.handleUpload(badRR, badReq)
	if badRR.Code != http.StatusUnauthorized {
		t.Fatalf("未包含 RESDATA 的签名状态=%d 期望=%d", badRR.Code, http.StatusUnauthorized)
	}
}

// TestResourceUsesSharedExpirationSignature 验证资源访问能使用共享过期签名访问多个文件。
func TestResourceUsesSharedExpirationSignature(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	paths := []string{
		"users/avatar/0123456789abcdef0123456789abcdef.jpg",
		"banner/abcdef0123456789abcdef0123456789.png",
	}
	for _, rel := range paths {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(rel), 0644); err != nil {
			t.Fatal(err)
		}
	}

	expireTs := strconvFormatInt(time.Now().Add(time.Hour).Unix())
	sign := md5Hex(app.cfg.MD5Key + expireTs)
	for _, rel := range paths {
		req := httptest.NewRequest(http.MethodGet, "/res/"+rel+"?time="+expireTs+"&sign="+sign, nil)
		req.RemoteAddr = "203.0.113.9:12345"
		rr := httptest.NewRecorder()
		app.handleResource(rr, req)
		if rr.Code != http.StatusOK || rr.Body.String() != rel {
			t.Fatalf("路径=%s 状态=%d 响应=%q", rel, rr.Code, rr.Body.String())
		}
	}
}

// TestResourceExpiredReturnsExpiredMessage 验证过期资源返回中文过期提示。
func TestResourceExpiredReturnsExpiredMessage(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	rel := "users/avatar/a.jpg"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}
	expireTs := strconvFormatInt(time.Now().Add(-time.Second).Unix())
	sign := md5Hex(app.cfg.MD5Key + expireTs)
	req := httptest.NewRequest(http.MethodGet, "/res/"+rel+"?time="+expireTs+"&sign="+sign, nil)
	rr := httptest.NewRecorder()
	app.handleResource(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("状态=%d 响应=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "资源访问已过期") {
		t.Fatalf("响应体异常=%q", rr.Body.String())
	}
}

// TestResourceSignatureIncludesData 验证非空 data 必须参与资源访问签名。
func TestResourceSignatureIncludesData(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	rel := "images/a.jpg"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}
	expireTs := strconvFormatInt(time.Now().Add(time.Hour).Unix())
	data := "user-1001"
	sign := md5Hex(app.cfg.MD5Key + expireTs + data)
	req := httptest.NewRequest(http.MethodGet, "/res/"+rel+"?time="+expireTs+"&data="+data+"&sign="+sign, nil)
	rr := httptest.NewRecorder()
	app.handleResource(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "image" {
		t.Fatalf("状态=%d 响应=%q", rr.Code, rr.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodGet, "/res/"+rel+"?time="+expireTs+"&data="+data+"&sign="+md5Hex(app.cfg.MD5Key+expireTs), nil)
	badRR := httptest.NewRecorder()
	app.handleResource(badRR, badReq)
	if badRR.Code != http.StatusUnauthorized {
		t.Fatalf("未包含 data 的签名状态=%d 期望=%d", badRR.Code, http.StatusUnauthorized)
	}
}

// TestResourceRejectsPathBasedSignature 验证携带路径信息的签名会被拒绝。
func TestResourceRejectsPathBasedSignature(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	rel := "users/avatar/a.jpg"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("image"), 0644); err != nil {
		t.Fatal(err)
	}
	expireTs := strconvFormatInt(time.Now().Add(time.Hour).Unix())
	oldSign := md5Hex(app.cfg.MD5Key + expireTs + "/res/" + rel)
	req := httptest.NewRequest(http.MethodGet, "/res/"+rel+"?time="+expireTs+"&sign="+oldSign, nil)
	rr := httptest.NewRecorder()
	app.handleResource(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("状态=%d 响应=%q", rr.Code, rr.Body.String())
	}
}

// TestCORSAllowsAllOriginsAndPreflight 验证 CORS 预检请求返回正确的跨域头信息。
func TestCORSAllowsAllOriginsAndPreflight(t *testing.T) {
	called := false
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	req := httptest.NewRequest(http.MethodOptions, "/upload", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "RESTIME, RESSIGN, RESPATH, Content-Type")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("状态=%d 期望=%d", rr.Code, http.StatusNoContent)
	}
	if called {
		t.Fatal("预检请求不应到达应用处理器")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin=%q 期望=*", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "*" {
		t.Fatalf("Access-Control-Allow-Headers=%q 期望=*", got)
	}
}

// TestBuildConfigUsesWorkingDirectoryRes 验证默认资源目录位于当前工作目录下的 res 文件夹中。
func TestBuildConfigUsesWorkingDirectoryRes(t *testing.T) {
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	wd := t.TempDir()
	if err := os.Chdir(wd); err != nil {
		t.Fatal(err)
	}
	cfg, err := buildConfig(map[string]string{"MD5_KEY": "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wd, "res")
	gotReal, gotErr := filepath.EvalSymlinks(filepath.Dir(cfg.Root))
	wantReal, wantErr := filepath.EvalSymlinks(filepath.Dir(want))
	if gotErr == nil && wantErr == nil {
		gotReal = filepath.Join(gotReal, filepath.Base(cfg.Root))
		wantReal = filepath.Join(wantReal, filepath.Base(want))
	} else {
		gotReal = cfg.Root
		wantReal = want
	}
	if gotReal != wantReal {
		t.Fatalf("root=%q 期望=%q", cfg.Root, want)
	}
}

// TestUploadSignatureDoesNotIncludeResPath 验证签名不包含 RESPATH 路径信息。
func TestUploadSignatureDoesNotIncludeResPath(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	rawDir := "users//avatar/./"
	sign := md5Hex(app.cfg.MD5Key + ts)
	req := newUploadRequest(t, "photo.jpg", []byte("hello"), ts, sign, rawDir)
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("状态=%d 响应=%s", rr.Code, rr.Body.String())
	}
	p := decodeUploadPath(t, rr)
	if !regexp.MustCompile(`^/res/users/avatar/[0-9a-f]{32}\.jpg$`).MatchString(p) {
		t.Fatalf("标准化后的存储路径异常: %q", p)
	}
}

// TestUploadRejectsLegacyPathBasedSignature 验证旧版基于路径的签名会被拒绝。
func TestUploadRejectsLegacyPathBasedSignature(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	dir := "users/avatar"
	sign := md5Hex(app.cfg.MD5Key + ts + dir)
	req := newUploadRequest(t, "photo.jpg", []byte("hello"), ts, sign, dir)
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("状态=%d 期望=%d 响应=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

// formatUnixNow 返回当前 Unix 时间戳字符串，供测试构造签名。
func formatUnixNow() string {
	return strconvFormatInt(time.Now().Unix())
}

// strconvFormatInt 将 int64 转成字符串，便于测试中生成时间戳。
func strconvFormatInt(v int64) string {
	return fmt.Sprintf("%d", v)
}
