package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func detailMap(r Result) map[string]string {
	m := map[string]string{}
	for _, d := range r.Detail {
		m[d.K] = d.V
	}
	return m
}

func TestDetailsAddLimitsAndSkipsEmpty(t *testing.T) {
	var d *Details
	d.Add("x", "y") // nil güvenli
	d = &Details{}
	d.Add("boş", "  ")
	d.Add("uzun", strings.Repeat("a", 900))
	for i := 0; i < 100; i++ {
		d.Add("k", i)
	}
	if len(d.L) != 60 || len(d.L[0].V) > 410 {
		t.Fatalf("len=%d first=%d", len(d.L), len(d.L[0].V))
	}
}

func TestDetailHTTPAndTCP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "nginx/1.25")
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Write([]byte("merhaba"))
	}))
	defer srv.Close()
	r := RunX(context.Background(), Spec{Type: "http", Target: srv.URL, Timeout: 3 * time.Second}, false)
	if !r.Up {
		t.Fatal(r.Err)
	}
	m := detailMap(r)
	if m["HTTP durum kodu"] != "200 OK" || m["Sunucu başlığı"] != "nginx/1.25" || m["İçerik türü"] != "text/plain" || !strings.Contains(m["Güvenlik başlıkları"], "x-frame-options") || !strings.HasPrefix(m["Protokol"], "HTTP/") {
		t.Fatalf("http detay: %v", m)
	}
	// başarısız durum kodunda da ayrıntı gelir
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "x", 503) }))
	defer bad.Close()
	rb := RunX(context.Background(), Spec{Type: "http", Target: bad.URL, Timeout: 3 * time.Second}, false)
	if rb.Up || detailMap(rb)["HTTP durum kodu"] != "503 Service Unavailable" {
		t.Fatalf("503: %v %v", rb.Up, detailMap(rb))
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	rt := RunX(context.Background(), Spec{Type: "tcp", Target: ln.Addr().String(), Timeout: 3 * time.Second}, false)
	if !rt.Up || detailMap(rt)["Bağlanılan adres"] != ln.Addr().String() {
		t.Fatalf("tcp: %v %v", rt.Err, detailMap(rt))
	}
}

func TestDetailTLSCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	r := RunX(context.Background(), Spec{Type: "tls", Target: addr, Timeout: 3 * time.Second, HTTP: HTTPOptions{SkipTLSVerify: true}}, false)
	if !r.Up {
		t.Fatal(r.Err)
	}
	m := detailMap(r)
	for _, k := range []string{"Geçerlilik başlangıcı", "Geçerlilik bitişi", "İmza algoritması", "Zincir uzunluğu", "TLS sürümü", "Şifre takımı"} {
		if m[k] == "" {
			t.Errorf("%q yok: %v", k, m)
		}
	}
}
