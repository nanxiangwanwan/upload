package zupload

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUploadProxy(t *testing.T) {
	var gotRESTIME, gotRESSIGN, gotRESPATH, gotRESTYPE, gotRESSIZE string
	var gotBody []byte

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRESTIME = r.Header.Get("RESTIME")
		gotRESSIGN = r.Header.Get("RESSIGN")
		gotRESPATH = r.Header.Get("RESPATH")
		gotRESTYPE = r.Header.Get("RESTYPE")
		gotRESSIZE = r.Header.Get("RESSIZE")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("hello"))
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/proxy-upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()

	const key = "secret"
	const resType = ".jpg,.png"
	const resSize = "5242880"
	err = UploadProxy(rr, req, upstream.URL, "users/avatar", resType, resSize, key)
	if err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusCreated {
		t.Fatalf("status=%d", rr.Code)
	}
	if gotRESPATH != "users/avatar" || gotRESTYPE != resType || gotRESSIZE != resSize {
		t.Fatalf("headers path=%q type=%q size=%q", gotRESPATH, gotRESTYPE, gotRESSIZE)
	}
	sum := md5.Sum([]byte(key + gotRESTIME + resType + resSize))
	wantSign := hex.EncodeToString(sum[:])
	if gotRESSIGN != wantSign {
		t.Fatalf("sign=%q want=%q", gotRESSIGN, wantSign)
	}
	if len(gotBody) == 0 {
		t.Fatal("multipart body was not forwarded")
	}
}
