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

	base, err := url.Parse(strings.TrimRight(resourceURL, "/"))
	if err != nil {
		return err
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return errors.New("resourceURL must use http or https")
	}
	if base.Host == "" {
		return errors.New("resourceURL host is empty")
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

	for _, key := range []string{"Content-Type", "Content-Length", "Cache-Control", "ETag", "Last-Modified"} {
		if value := resp.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	return err
}
