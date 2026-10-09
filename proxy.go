package zupload

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const proxyErrorLogLimit = 8 * 1024

func Proxy(w http.ResponseWriter, r *http.Request, resourceURL, resourcePath, md5Key string) error {
	if w == nil || r == nil {
		err := errors.New("HTTP 请求参数不能为空")
		log.Printf("图片代理失败：%v", err)
		return err
	}
	if resourceURL == "" || resourcePath == "" || md5Key == "" {
		err := errors.New("资源地址、资源路径和 MD5 密钥不能为空")
		log.Printf("图片代理失败：%v", err)
		return err
	}

	base, err := parseHTTPURL(resourceURL)
	if err != nil {
		err = fmt.Errorf("资源地址无效：%w", err)
		log.Printf("图片代理失败：%v", err)
		return err
	}

	if !strings.HasPrefix(resourcePath, "/") {
		resourcePath = "/" + resourcePath
	}

	ts := fmt.Sprintf("%d", time.Now().Unix())
	sum := md5.Sum([]byte(md5Key + ts + resourcePath))
	sign := hex.EncodeToString(sum[:])

	base.Path = strings.TrimRight(base.Path, "/") + resourcePath
	q := base.Query()
	q.Set("time", ts)
	q.Set("sign", sign)
	q.Set("path", "1")
	base.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base.String(), nil)
	if err != nil {
		err = fmt.Errorf("创建图片请求失败：%w", err)
		log.Printf("图片代理失败：%v", err)
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		err = fmt.Errorf("请求图片服务器失败：%w", err)
		log.Printf("图片代理失败：%v", err)
		return err
	}
	defer resp.Body.Close()

	return copyProxyResponse(w, resp, "图片代理")
}

// UploadProxy 将当前 multipart 上传请求转发到签名上传接口。
// uploadURL 必须是完整上传地址，例如 http://127.0.0.1:8080/upload。
// resourcePath 会作为 RESPATH Header 转发。
func UploadProxy(w http.ResponseWriter, r *http.Request, uploadURL, resourcePath, resType, resSize, md5Key string) error {
	if w == nil || r == nil {
		err := errors.New("HTTP 请求参数不能为空")
		log.Printf("上传代理失败：%v", err)
		return err
	}
	if uploadURL == "" || md5Key == "" {
		err := errors.New("上传地址和 MD5 密钥不能为空")
		log.Printf("上传代理失败：%v", err)
		return err
	}
	if r.Method != http.MethodPost {
		err := errors.New("上传代理只支持 POST 请求")
		log.Printf("上传代理失败：%v", err)
		return err
	}

	base, err := parseHTTPURL(uploadURL)
	if err != nil {
		err = fmt.Errorf("上传地址无效：%w", err)
		log.Printf("上传代理失败：%v", err)
		return err
	}

	ts := fmt.Sprintf("%d", time.Now().Unix())
	sum := md5.Sum([]byte(md5Key + ts + resType + resSize))
	sign := hex.EncodeToString(sum[:])

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base.String(), r.Body)
	if err != nil {
		err = fmt.Errorf("创建上传请求失败：%w", err)
		log.Printf("上传代理失败：%v", err)
		return err
	}

	if contentType := r.Header.Get("Content-Type"); contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if r.ContentLength >= 0 {
		req.ContentLength = r.ContentLength
	}

	req.Header.Set("RESTIME", ts)
	req.Header.Set("RESSIGN", sign)
	if resourcePath != "" {
		req.Header.Set("RESPATH", resourcePath)
	}
	if resType != "" {
		req.Header.Set("RESTYPE", resType)
	}
	if resSize != "" {
		req.Header.Set("RESSIZE", resSize)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		err = fmt.Errorf("请求上传服务器失败：%w", err)
		log.Printf("上传代理失败：%v", err)
		return err
	}
	defer resp.Body.Close()

	return copyProxyResponse(w, resp, "上传代理")
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("URL 解析失败：%w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, errors.New("URL 必须使用 http 或 https")
	}
	if base.Host == "" {
		return nil, errors.New("URL 主机地址不能为空")
	}
	return base, nil
}

func copyProxyResponse(w http.ResponseWriter, resp *http.Response, scene string) error {
	copyResponseHeaders(w, resp)
	w.WriteHeader(resp.StatusCode)

	if resp.StatusCode >= http.StatusBadRequest {
		capture := &limitedLogBuffer{limit: proxyErrorLogLimit}
		_, err := io.Copy(w, io.TeeReader(resp.Body, capture))
		message := strings.TrimSpace(capture.String())
		if message == "" {
			message = "上游未返回错误内容"
		}
		if capture.truncated {
			message += "（日志内容已截断）"
		}
		log.Printf("%s失败：上游状态=%s，响应=%s", scene, resp.Status, message)
		if err != nil {
			log.Printf("%s失败：转发错误响应时发生异常：%v", scene, err)
			return fmt.Errorf("转发上游错误响应失败：%w", err)
		}
		return nil
	}

	_, err := io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("%s失败：转发响应内容时发生异常：%v", scene, err)
		return fmt.Errorf("转发响应内容失败：%w", err)
	}
	return nil
}

func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for _, key := range []string{"Content-Type", "Content-Length", "Cache-Control", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
}

type limitedLogBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedLogBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.truncated = true
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *limitedLogBuffer) String() string {
	return b.buf.String()
}
