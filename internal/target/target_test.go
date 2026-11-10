package target

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	s := New()
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func get(t *testing.T, url string, hdr map[string]string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

func TestItems(t *testing.T) {
	_, ts := newTestServer(t)
	code, body := get(t, ts.URL+"/api/items", nil)
	var list []item
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list) != 10 {
		t.Fatalf("items: %d %s", code, body)
	}
	if code, _ := get(t, ts.URL+"/api/items/3", nil); code != 200 {
		t.Fatalf("item 3: %d", code)
	}
	if code, _ := get(t, ts.URL+"/api/items/999", nil); code != 404 {
		t.Fatalf("item 999: %d", code)
	}
}

func TestFlakyIsDeterministic(t *testing.T) {
	s, ts := newTestServer(t)
	for round := 0; round < 2; round++ {
		var fails []int
		for i := 1; i <= 30; i++ {
			if code, _ := get(t, ts.URL+"/api/flaky", nil); code == 500 {
				fails = append(fails, i)
			}
		}
		if len(fails) != 3 || fails[0] != 10 || fails[1] != 20 || fails[2] != 30 {
			t.Fatalf("round %d: 500 at %v", round, fails)
		}
		s.Reset()
	}
}

func TestHeavyQueues(t *testing.T) {
	_, ts := newTestServer(t)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			get(t, ts.URL+"/api/heavy", nil)
		}()
	}
	wg.Wait()
	// 5件が1件ずつ200msかかるので，全体で約1秒になる
	if d := time.Since(start); d < 950*time.Millisecond || d > 2*time.Second {
		t.Fatalf("5 concurrent heavy took %v", d)
	}
}

func TestLoginAndMe(t *testing.T) {
	_, ts := newTestServer(t)
	post := func(ct, body string) (int, []byte) {
		res, err := http.Post(ts.URL+"/api/login", ct, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	if code, _ := post("application/x-www-form-urlencoded", "user=drill"); code != 415 {
		t.Fatalf("form body: %d", code)
	}
	if code, _ := post("application/json", `{"user":"drill","password":"x"}`); code != 401 {
		t.Fatalf("bad password: %d", code)
	}
	code, body := post("application/json", `{"user":"drill","password":"k6-practice"}`)
	var tok struct{ Token string }
	if code != 200 || json.Unmarshal(body, &tok) != nil || tok.Token == "" {
		t.Fatalf("login: %d %s", code, body)
	}
	if code, _ := get(t, ts.URL+"/api/me", nil); code != 401 {
		t.Fatalf("me without token: %d", code)
	}
	if code, _ := get(t, ts.URL+"/api/me", map[string]string{"Authorization": "Bearer " + tok.Token}); code != 200 {
		t.Fatalf("me with token: %d", code)
	}
}

func TestUpload(t *testing.T) {
	_, ts := newTestServer(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "sample.png")
	fw.Write([]byte("12345"))
	mw.Close()
	send := func(user string) (int, []byte) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/files", bytes.NewReader(buf.Bytes()))
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if user != "" {
			req.Header.Set("X-User-ID", user)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	if code, _ := send(""); code != 401 {
		t.Fatalf("without X-User-ID: %d", code)
	}
	code, body := send("u1")
	if code != 201 || !strings.Contains(string(body), `"size":5`) {
		t.Fatalf("upload: %d %s", code, body)
	}
}
