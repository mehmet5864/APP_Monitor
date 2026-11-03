package probe

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func run(typ, target string, block bool, o HTTPOptions) (bool, error) {
	up, _, err := Run(context.Background(), Spec{Type: typ, Target: target, Timeout: 2 * time.Second, HTTP: o}, block)
	return up, err
}

func TestHTTPProbeUpAndDown(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer bad.Close()

	if up, err := run("http", ok.URL, false, HTTPOptions{}); !up || err != nil {
		t.Fatalf("200 yanıtı up olmalı: up=%v err=%v", up, err)
	}
	if up, _ := run("http", bad.URL, false, HTTPOptions{}); up {
		t.Fatal("500 yanıtı down olmalı")
	}
}

func TestHTTPOptions(t *testing.T) {
	var gotMethod, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotMethod, gotAuth, gotBody = r.Method, r.Header.Get("Authorization"), string(b)
		w.WriteHeader(201)
		w.Write([]byte(`{"status":"ready"}`))
	}))
	defer srv.Close()

	o := HTTPOptions{Method: "POST", Headers: map[string]string{"Authorization": "Bearer abc"}, Body: `{"ping":1}`,
		ExpectStatus: "201", ExpectText: `"ready"`}
	if up, err := run("http", srv.URL, false, o); !up || err != nil {
		t.Fatalf("seçenekli probe başarılı olmalı: %v", err)
	}
	if gotMethod != "POST" || gotAuth != "Bearer abc" || gotBody != `{"ping":1}` {
		t.Fatalf("istek yanlış gitti: %s %q %q", gotMethod, gotAuth, gotBody)
	}

	o.ExpectStatus = "200"
	if up, err := run("http", srv.URL, false, o); up || err == nil || !strings.Contains(err.Error(), "201") {
		t.Fatalf("beklenmeyen durum kodu hata vermeli: up=%v err=%v", up, err)
	}
	o.ExpectStatus, o.ExpectText = "201", "bulunmayan metin"
	if up, err := run("http", srv.URL, false, o); up || err == nil || !strings.Contains(err.Error(), "metin") {
		t.Fatalf("eksik metin hata vermeli: up=%v err=%v", up, err)
	}
}

func TestHTTPNoFollowRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer srv.Close()
	if up, err := run("http", srv.URL, false, HTTPOptions{ExpectStatus: "302", NoFollowRedirects: true}); !up || err != nil {
		t.Fatalf("yönlendirme izlenmeden 302 beklenmeli: %v", err)
	}
	if up, err := run("http", srv.URL, false, HTTPOptions{ExpectStatus: "200"}); !up || err != nil {
		t.Fatalf("yönlendirme izlenip 200 beklenmeli: %v", err)
	}
}

func TestErrorDoesNotLeakURLQuery(t *testing.T) {
	_, err := run("http", "http://127.0.0.1:1/?token=GIZLI", false, HTTPOptions{})
	if err == nil || strings.Contains(err.Error(), "GIZLI") {
		t.Fatalf("hata mesajı adresi sızdırmamalı: %v", err)
	}
}

func TestStatusSpec(t *testing.T) {
	cases := []struct {
		spec string
		code int
		want bool
	}{
		{"", 200, true}, {"", 399, true}, {"", 404, false},
		{"200", 200, true}, {"200", 201, false},
		{"200-299", 204, true}, {"200,204,301-302", 302, true}, {"200,204,301-302", 303, false},
	}
	for _, c := range cases {
		if got := StatusMatches(c.spec, c.code); got != c.want {
			t.Errorf("%q %d: got %v, want %v", c.spec, c.code, got, c.want)
		}
	}
	for _, bad := range []string{"abc", "99", "200-", "600", "300-200", "200,x"} {
		if ValidateStatusSpec(bad) == nil {
			t.Errorf("%q geçersiz sayılmalıydı", bad)
		}
	}
}

func TestValidateHeaders(t *testing.T) {
	if ValidateHeaders(map[string]string{"X-Test": "ok"}) != nil {
		t.Fatal("geçerli başlık reddedildi")
	}
	if ValidateHeaders(map[string]string{"Bad Name": "x"}) == nil {
		t.Fatal("boşluklu başlık adı reddedilmeliydi")
	}
	if ValidateHeaders(map[string]string{"X-Test": "a\r\nInjected: 1"}) == nil {
		t.Fatal("satır sonu içeren değer reddedilmeliydi")
	}
}

func TestSaaSModeBlocksPrivateTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close() // 127.0.0.1 üzerinde dinler

	up, err := run("http", srv.URL, true, HTTPOptions{})
	if up || err == nil || !strings.Contains(err.Error(), "SaaS") {
		t.Fatalf("SaaS modunda loopback engellenmeli: up=%v err=%v", up, err)
	}
	if up, err := run("tcp", "10.0.0.5:5432", true, HTTPOptions{}); up || err == nil {
		t.Fatalf("özel ağ adresi engellenmeli: up=%v err=%v", up, err)
	}
}

func TestTCPProbeDown(t *testing.T) {
	if up, _ := run("tcp", "127.0.0.1:1", false, HTTPOptions{}); up {
		t.Fatal("kapalı port down olmalı")
	}
}
