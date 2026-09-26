package zupload

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func Proxy(w http.ResponseWriter, r *http.Request, resourceURL, resourcePath, md5Key string) error {
	if w == nil || r == nil {
		return errors.New("invalid http request")
	}
	if resourceURL == "" || resourcePath == "" || md5Key == "" {
		return errors.New("resourceURL, path and md5Key are required")
	}

	base, err := parseHTTPURL(resourceURL)
	if err != nil {
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
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	copyResponseHeaders(w, resp)
	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	return err
}

// UploadProxy proxies the current multipart upload request to a signed upload endpoint.
// uploadURL must be the complete remote upload URL, for example http://127.0.0.1:8080/upload.
// resourcePath is forwarded as the RESPATH header.
func UploadProxy(w http.ResponseWriter, r *http.Request, uploadURL, resourcePath, resType, resSize, md5Key string) error {
	if w == nil || r == nil {
		return errors.New("invalid http request")
	}
	if uploadURL == "" || md5Key == "" {
		return errors.New("uploadURL and md5Key are required")
	}
	if r.Method != http.MethodPost {
		return errors.New("upload proxy only supports POST")
	}

	base, err := parseHTTPURL(uploadURL)
	if err != nil {
		return err
	}

	ts := fmt.Sprintf("%d", time.Now().Unix())
	sum := md5.Sum([]byte(md5Key + ts + resType + resSize))
	sign := hex.EncodeToString(sum[:])

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, base.String(), r.Body)
	if err != nil {
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
		return err
	}
	defer resp.Body.Close()

	copyResponseHeaders(w, resp)
	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	return err
}

func parseHTTPURL(rawURL string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, errors.New("URL must use http or https")
	}
	if base.Host == "" {
		return nil, errors.New("URL host is empty")
	}
	return base, nil
}

func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for _, key := range []string{"Content-Type", "Content-Length", "Cache-Control", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
}
