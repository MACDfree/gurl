package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestSend(t *testing.T) {
	resetHistory(t)
	resetEnvCol(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Test", "hello")
			q := r.URL.Query().Get("b")
			body, _ := io.ReadAll(r.Body)
			io.WriteString(w, `{"query":"`+q+`","body":"`+string(body)+`","method":"`+r.Method+`","auth":"`+r.Header.Get("Authorization")+`"}`)
		case "/gz":
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Type", "application/json")
			zw := gzip.NewWriter(w)
			zw.Write([]byte(`{"gz":"ok"}`))
			zw.Close()
		case "/gunzip":
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				io.WriteString(w, `{"error":"`+err.Error()+`"}`)
				return
			}
			plain, _ := io.ReadAll(zr)
			io.WriteString(w, `{"encoding":"`+r.Header.Get("Content-Encoding")+`","plain":"`+string(plain)+`"}`)
		case "/parse":
			ct := r.Header.Get("Content-Type")
			name := r.FormValue("name")
			fileContent := ""
			if strings.HasPrefix(ct, "multipart/") {
				f, _, err := r.FormFile("upload")
				if err == nil {
					b, _ := io.ReadAll(f)
					f.Close()
					fileContent = string(b)
				}
			}
			io.WriteString(w, `{"ct":"`+ct+`","name":"`+name+`","file":"`+fileContent+`"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	svc := &HTTPService{}

	resp, err := svc.Send(&HTTPRequest{
		Method: "post",
		URL:    srv.URL + "/echo?keep=1",
		Params: []KV{{Key: "b", Value: "2", Enabled: true}, {Key: "c", Value: "3", Enabled: false}},
		Headers: []KV{
			{Key: "Authorization", Value: "Bearer x", Enabled: true},
			{Key: "X-Skip", Value: "1", Enabled: false},
		},
		Body: `{"a":1}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d", resp.Status)
	}
	if !strings.Contains(resp.Body, `"method":"POST"`) ||
		!strings.Contains(resp.Body, `"query":"2"`) ||
		!strings.Contains(resp.Body, `"body":"{"a":1}"`) ||
		!strings.Contains(resp.Body, `"auth":"Bearer x"`) {
		t.Fatalf("unexpected echo body: %s", resp.Body)
	}
	if resp.DurationMS < 0 || !resp.IsText {
		t.Fatalf("meta wrong: %+v", resp)
	}
	// 未截断时 Size 就是正文长度：旧的 `Size > 0` 带着「截断后才取 len」的 bug 也能过。
	if resp.Size != int64(len(resp.Body)) {
		t.Fatalf("Size = %d, len(Body) = %d", resp.Size, len(resp.Body))
	}
	var found bool
	for _, h := range resp.Headers {
		if h.Key == "X-Test" && h.Value == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatal("response header X-Test missing")
	}

	gzResp, err := svc.Send(&HTTPRequest{
		URL:     srv.URL + "/gz",
		Headers: []KV{{Key: "Accept-Encoding", Value: "gzip", Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gzResp.Body != `{"gz":"ok"}` || !gzResp.IsText {
		t.Fatalf("gzip body wrong: %q isText=%v", gzResp.Body, gzResp.IsText)
	}

	gzSent, err := svc.Send(&HTTPRequest{
		Method:   "POST",
		URL:      srv.URL + "/gunzip",
		Body:     "hello-gzip",
		BodyGzip: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gzSent.Body != `{"encoding":"gzip","plain":"hello-gzip"}` {
		t.Fatalf("request gzip wrong: %s", gzSent.Body)
	}

	formResp, err := svc.Send(&HTTPRequest{
		Method:   "POST",
		URL:      srv.URL + "/parse",
		BodyType: "form",
		Form:     []KV{{Key: "name", Value: "tom", Enabled: true}, {Key: "off", Value: "x", Enabled: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if formResp.Body != `{"ct":"application/x-www-form-urlencoded","name":"tom","file":""}` {
		t.Fatalf("form wrong: %s", formResp.Body)
	}

	tmp, err := os.CreateTemp(t.TempDir(), "up-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	tmp.WriteString("filedata")
	tmp.Close()

	mpResp, err := svc.Send(&HTTPRequest{
		Method:   "POST",
		URL:      srv.URL + "/parse",
		BodyType: "multipart",
		Form:     []KV{{Key: "name", Value: "jerry", Enabled: true}},
		Files:    []FileKV{{Key: "upload", Path: tmp.Name()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(mpResp.Body, `{"ct":"multipart/form-data; boundary=`) ||
		!strings.Contains(mpResp.Body, `"name":"jerry"`) ||
		!strings.Contains(mpResp.Body, `"file":"filedata"`) {
		t.Fatalf("multipart wrong: %s", mpResp.Body)
	}

	if _, err := svc.Send(&HTTPRequest{URL: ""}); err == nil {
		t.Fatal("empty URL should error")
	}
	if _, err := svc.Send(&HTTPRequest{URL: srv.URL + "/missing"}); err != nil {
		t.Fatalf("404 should not be a Go error: %v", err)
	}
}
