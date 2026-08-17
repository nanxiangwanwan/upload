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

func TestSafeTarget(t *testing.T) {
	root := t.TempDir()
	rel, target, err := safeTarget(root, "images/a.png")
	if err != nil {
		t.Fatal(err)
	}
	if rel != "images/a.png" {
		t.Fatalf("unexpected rel: %q", rel)
	}
	want := filepath.Join(root, "images", "a.png")
	if target != want {
		t.Fatalf("target=%q want=%q", target, want)
	}
	if _, _, err := safeTarget(root, "../escape.txt"); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}

func TestParseTypesDefaultsToAll(t *testing.T) {
	_, all := parseTypes("")
	if !all {
		t.Fatal("empty UPLOAD_TYPES should allow all")
	}
	types, all := parseTypes("jpg,.png")
	if all || !types[".jpg"] || !types[".png"] {
		t.Fatalf("unexpected parsed types: %#v all=%v", types, all)
	}
}

func TestUploadRejectsNonWhitelistedIP(t *testing.T) {
	app := &App{cfg: Config{IPWhitelist: map[string]bool{"127.0.0.1": true}}}
	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.RemoteAddr = "10.0.0.9:54321"
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusForbidden, rr.Body.String())
	}
}

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
		t.Fatalf("decode response: %v body=%s", err, rr.Body.String())
	}
	if out.Code != 0 || out.Data.Path == "" {
		t.Fatalf("unexpected response: %s", rr.Body.String())
	}
	return out.Data.Path
}

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
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	p := decodeUploadPath(t, rr)
	if !regexp.MustCompile(`^/res/users/avatar/[0-9a-f]{32}\.jpg$`).MatchString(p) {
		t.Fatalf("unexpected random path: %q", p)
	}
	rel := strings.TrimPrefix(p, "/res/")
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || string(b) != "hello" {
		t.Fatalf("saved file mismatch err=%v content=%q", err, string(b))
	}
}

func TestUploadWithoutResPathUsesRestimeDirectory(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	sign := md5Hex(app.cfg.MD5Key + ts)
	req := newUploadRequest(t, "document.pdf", []byte("pdf"), ts, sign, "")
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	p := decodeUploadPath(t, rr)
	want := regexp.MustCompile(`^/res/` + regexp.QuoteMeta(ts) + `/[0-9a-f]{32}\.pdf$`)
	if !want.MatchString(p) {
		t.Fatalf("path=%q does not use RESTIME directory %q", p, ts)
	}
}

func TestUploadRejectsWrongSignatureForDirectory(t *testing.T) {
	root := t.TempDir()
	app := testApp(root)
	ts := formatUnixNow()
	req := newUploadRequest(t, "a.png", []byte("x"), ts, "bad", "users/avatar")
	rr := httptest.NewRecorder()
	app.handleUpload(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

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
		// Resource access must not enforce the upload IP whitelist.
		req.RemoteAddr = "203.0.113.9:12345"
		rr := httptest.NewRecorder()
		app.handleResource(rr, req)
		if rr.Code != http.StatusOK || rr.Body.String() != rel {
			t.Fatalf("path=%s status=%d body=%q", rel, rr.Code, rr.Body.String())
		}
	}
}

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
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "resource access expired") {
		t.Fatalf("unexpected body=%q", rr.Body.String())
	}
}

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
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
}

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
		t.Fatalf("status=%d want=%d", rr.Code, http.StatusNoContent)
	}
	if called {
		t.Fatal("preflight request should not reach the application handler")
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin=%q want=*", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Headers"); got != "*" {
		t.Fatalf("Access-Control-Allow-Headers=%q want=*", got)
	}
}

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
	if cfg.Root != want {
		t.Fatalf("root=%q want=%q", cfg.Root, want)
	}
}

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
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	p := decodeUploadPath(t, rr)
	if !regexp.MustCompile(`^/res/users/avatar/[0-9a-f]{32}\.jpg$`).MatchString(p) {
		t.Fatalf("unexpected normalized storage path: %q", p)
	}
}

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
		t.Fatalf("status=%d want=%d body=%s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

func formatUnixNow() string {
	return strconvFormatInt(time.Now().Unix())
}

func strconvFormatInt(v int64) string {
	// Kept tiny so tests stay focused on HTTP behavior.
	return fmt.Sprintf("%d", v)
}
