package crawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"appmon/internal/cdp"
)

func TestRobots(t *testing.T) {
	r := ParseRobots("User-agent: *\nDisallow: /private\nAllow: /private/ok\nDisallow: /*.pdf$\nCrawl-delay: 2\nSitemap: https://x/s.xml\n\nUser-agent: appmon\nDisallow: /only-appmon\n", "appmon")
	if r.Allowed("/only-appmon/x") || !r.Allowed("/private") || r.Delay != 0 {
		t.Fatalf("özel grup seçilmeliydi: %+v", r)
	}
	r = ParseRobots("User-agent: *\nDisallow: /private\nAllow: /private/ok\nDisallow: /*.pdf$\nCrawl-delay: 2\nSitemap: https://x/s.xml\n", "appmon")
	cases := map[string]bool{"/": true, "/private": false, "/private/a": false, "/private/ok": true, "/a.pdf": false, "/a.pdf?x=1": true, "/x": true}
	for p, want := range cases {
		if r.Allowed(p) != want {
			t.Errorf("%s: beklenen %v", p, want)
		}
	}
	if r.Delay != 2 || len(r.Sitemaps) != 1 {
		t.Errorf("delay/sitemap: %+v", r)
	}
	if (&Robots{DenyAll: true}).Allowed("/") {
		t.Error("DenyAll her şeyi yasaklamalı")
	}
	if !ParseRobots("", "appmon").Allowed("/x") {
		t.Error("boş robots serbest olmalı")
	}
}

func TestWordlistsSafe(t *testing.T) {
	bad := []string{".env", ".git", "backup", "wp-config", "passwd", "id_rsa", ".htaccess", "phpinfo", "xmlrpc", ".sql", "dump"}
	for _, l := range Lists {
		w := Words(l.ID)
		for _, p := range w {
			if dangerRe.MatchString(p) {
				t.Errorf("%s: tehlikeli yol sözlükte: %s", l.ID, p)
			}
		}
		if len(w) < 50 {
			t.Fatalf("%s: liste çok kısa (%d)", l.ID, len(w))
		}
		for _, p := range w {
			for _, b := range bad {
				if strings.Contains(strings.ToLower(p), b) {
					t.Errorf("%s: izlenmemesi gereken yol: %s", l.ID, p)
				}
			}
		}
	}
	if len(Words("yok")) != 0 {
		t.Error("bilinmeyen sözlük boş dönmeli")
	}
}

func TestNormKey(t *testing.T) {
	base, _ := url.Parse("https://Ex.com:443/a/b")
	u := Norm("../c?x=1#frag", base)
	if u == nil || u.String() != "https://ex.com/c?x=1" {
		t.Fatalf("norm: %v", u)
	}
	if Norm("mailto:a@b", base) != nil || Norm("javascript:void(0)", base) != nil {
		t.Error("http dışı elenmeli")
	}
	a, b := Norm("https://ex.com/p?id=1&z=2", nil), Norm("https://ex.com/p?z=9&id=7", nil)
	if keyOf(a) != keyOf(b) {
		t.Error("yalnız değer farkı aynı anahtar olmalı")
	}
	if !sameSite("www.ex.com", "api.ex.com:8443") || sameSite("ex.com", "cdn.other.net") {
		t.Error("sameSite")
	}
	if !dangerRe.MatchString("/user/logout") || dangerRe.MatchString("/products") {
		t.Error("danger")
	}
}

// Sahte bir siteyi gerçek Chrome ile gezer (Chrome yoksa atlanır).
func TestRunE2E(t *testing.T) {
	if _, err := cdp.FindChrome(os.Getenv("APM_CHROME_PATH")); err != nil {
		t.Skip("Chrome yok")
	}
	hits := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User-agent: *\nDisallow: /gizli\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		if r.URL.Path == "/" {
			w.Write([]byte(`<title>Ana</title><a href="/a">a</a><a href="/gizli">g</a><a href="/logout">x</a><a href="https://dis.example/y">d</a>
<form action="/ara" method="post"><input name="q"><input type="password" name="p"></form>
<script>fetch('/api/data')</script>`))
			return
		}
		if r.URL.Path == "/a" {
			w.Write([]byte(`<title>A</title><a href="/b?id=1">b1</a><a href="/b?id=2">b2</a>`))
			return
		}
		if r.URL.Path == "/health" {
			w.Write([]byte("ok"))
			return
		}
		if r.URL.Path == "/api/data" {
			w.Write([]byte("{}"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/b") {
			w.Write([]byte("<title>B</title>"))
			return
		}
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := Run(ctx, Config{Start: srv.URL + "/", Spider: true, Wordlist: "kisa", MaxPages: 10, MaxDepth: 2, Delay: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.Robots != "uyuldu" {
		t.Errorf("robots: %s", res.Robots)
	}
	if hits["/gizli"] != 0 || hits["/logout"] != 0 {
		t.Errorf("yasak/tehlikeli yola gidildi: %v", hits)
	}
	if len(res.Pages) != 3 { // /, /a, /b?id=1 (id=2 aynı anahtar)
		t.Errorf("sayfa sayısı: %d %+v", len(res.Pages), res.Pages)
	}
	if len(res.Pages) > 0 && (len(res.Pages[0].Forms) != 1 || !res.Pages[0].Forms[0].Password) {
		t.Errorf("form: %+v", res.Pages[0].Forms)
	}
	found := false
	for _, e := range res.Endpoints {
		if strings.HasSuffix(e.URL, "/api/data") {
			found = true
		}
	}
	if !found {
		t.Errorf("API çağrısı bulunamadı: %+v", res.Endpoints)
	}
	ok := false
	for _, h := range res.Hits {
		if h.Path == "/health" && h.Status == 200 {
			ok = true
		}
	}
	if !ok {
		t.Errorf("sözlük /health bulmalı: %+v", res.Hits)
	}
}
