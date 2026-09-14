package main

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testApp(root string) *App {
	return &App{cfg: Config{Root: root, UploadPath: "/upload", AllowAllTypes: true, MaxUploadSize: 1024 * 1024, MD5Key: "test-key", IPWhitelist: map[string]bool{"127.0.0.1": true}, TimeExpire: 300}}
}

func newUploadRequest(t *testing.T, name string, content []byte, ts, sign, path string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("RESTIME", ts)
	req.Header.Set("RESSIGN", sign)
	if path != "" {
		req.Header.Set("RESPATH", path)
	}
	req.RemoteAddr = "127.0.0.1:12345"
	return req
}

func nowString() string { return fmt.Sprintf("%d", time.Now().Unix()) }

func TestUploadTypeSizeOverride(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	app.cfg.AllowAllTypes = false
	app.cfg.UploadTypes = map[string]bool{".jpg": true}
	app.cfg.MaxUploadSize = 1
	ts := nowString()
	rt := ".png"
	rs := "10"
	sign := md5Hex(app.cfg.MD5Key + ts + rt + rs)
	req := newUploadRequest(t, "a.png", []byte("hello"), ts, sign, "images")
	req.Header.Set("RESTYPE", rt)
	req.Header.Set("RESSIZE", rs)
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestRequireTypeSize(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	app.cfg.RequireTypeSize = true
	ts := nowString()
	sign := md5Hex(app.cfg.MD5Key + ts + ".png")
	req := newUploadRequest(t, "a.png", []byte("x"), ts, sign, "images")
	req.Header.Set("RESTYPE", ".png")
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestConfigUploadPath(t *testing.T) {
	old, _ := os.Getwd()
	defer os.Chdir(old)
	_ = os.Chdir(t.TempDir())
	cfg, err := buildConfig(map[string]string{"MD5_KEY": "k", "UPLOAD_PATH": "files/upload", "REQUIRE_TYPE_SIZE": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UploadPath != "/files/upload" || !cfg.RequireTypeSize {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestResourcePathSignature(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	rel := "images/a.jpg"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	_ = os.MkdirAll(filepath.Dir(abs), 0755)
	_ = os.WriteFile(abs, []byte("image"), 0644)
	expire := fmt.Sprintf("%d", time.Now().Add(time.Hour).Unix())
	data := "u1"
	publicPath := "/res/" + rel
	sign := md5Hex(app.cfg.MD5Key + expire + data + publicPath)
	req := httptest.NewRequest(http.MethodGet, publicPath+"?time="+expire+"&data="+data+"&path=1&sign="+sign, nil)
	rr := httptest.NewRecorder()
	app.handleResource(rr, req)
	if rr.Code != http.StatusOK || rr.Body.String() != "image" {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

func TestSafeTarget(t *testing.T) {
	root := t.TempDir()
	if _, _, err := safeTarget(root, "../x"); err == nil {
		t.Fatal("expected error")
	}
}
