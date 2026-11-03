package scenario

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// mockApp: giriş (çerez + token), token korumalı liste ve ayrıntı uçları.
func mockApp() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var b struct{ User, Pass string }
		json.NewDecoder(r.Body).Decode(&b)
		if b.User != "ali" || b.Pass != "gizli" {
			http.Error(w, "yetkisiz", 401)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "S1", Path: "/"})
		w.Header().Set("X-Request-Id", "req-42")
		w.Write([]byte(`{"data":{"token":"T123"}}`))
	})
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie("sid")
			if err != nil || c.Value != "S1" || r.Header.Get("Authorization") != "Bearer T123" {
				http.Error(w, "yetkisiz", 401)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /items", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[{"id":7,"name":"kalem"}]}`))
	}))
	mux.HandleFunc("GET /items/7", auth(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>ürün: kalem</html>`))
	}))
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) { time.Sleep(150 * time.Millisecond) })
	return httptest.NewServer(mux)
}

func loginFlow(base string) []Step {
	return []Step{
		{Name: "Giriş", Method: "POST", URL: base + "/login", Body: `{"user":"{{user}}","pass":"{{pass}}"}`,
			Headers: map[string]string{"Content-Type": "application/json"}, ExpectStatus: "200",
			Extract: []Extract{{Var: "token", From: "json", Path: "data.token"}, {Var: "rid", From: "header", Path: "X-Request-Id"}}},
		{Name: "Liste", Method: "GET", URL: base + "/items", Headers: map[string]string{"Authorization": "Bearer {{token}}"},
			Extract: []Extract{{Var: "item_id", From: "json", Path: "items.0.id"}}},
		{Name: "Ayrıntı", Method: "GET", URL: base + "/items/{{item_id}}", Headers: map[string]string{"Authorization": "Bearer {{token}}"},
			ExpectText: "kalem", Extract: []Extract{{Var: "urun", From: "regex", Path: `ürün: (\w+)`}}},
	}
}

func opts() Options {
	return Options{Timeout: 2 * time.Second, Vars: map[string]string{"user": "ali", "pass": "gizli"}}
}

func TestLoginFlowWithCookiesAndExtraction(t *testing.T) {
	srv := mockApp()
	defer srv.Close()
	steps := loginFlow(srv.URL)
	Normalize(steps)
	if err := Validate(steps, opts().Vars); err != nil {
		t.Fatalf("geçerli senaryo reddedildi: %v", err)
	}
	results, _, err := Run(context.Background(), steps, opts())
	if err != nil {
		t.Fatalf("senaryo başarılı olmalı: %v (sonuçlar: %+v)", err, results)
	}
	if len(results) != 3 {
		t.Fatalf("3 adım sonucu bekleniyordu, %d geldi", len(results))
	}
	for _, r := range results {
		if !r.OK || r.Status != 200 {
			t.Errorf("adım başarısız: %+v", r)
		}
	}
}

func TestWrongPasswordStopsAtFirstStep(t *testing.T) {
	srv := mockApp()
	defer srv.Close()
	steps := loginFlow(srv.URL)
	Normalize(steps)
	o := opts()
	o.Vars["pass"] = "yanlis"
	results, _, err := Run(context.Background(), steps, o)
	if err == nil || !strings.Contains(err.Error(), "adım 1 (Giriş)") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("1. adımda 401 hatası bekleniyordu: %v", err)
	}
	if len(results) != 1 || results[0].OK {
		t.Fatalf("yalnızca başarısız ilk adım raporlanmalı: %+v", results)
	}
}

func TestErrorsNeverLeakTokenOrBody(t *testing.T) {
	srv := mockApp()
	defer srv.Close()
	steps := loginFlow(srv.URL)
	steps[2].ExpectText = "olmayan metin"
	Normalize(steps)
	results, _, err := Run(context.Background(), steps, opts())
	if err == nil {
		t.Fatal("eksik metin hata vermeli")
	}
	all := err.Error()
	for _, r := range results {
		all += r.Error
	}
	for _, secret := range []string{"T123", "gizli", "kalem"} {
		if strings.Contains(all, secret) {
			t.Fatalf("hata çıktısı %q içeriyor: %s", secret, all)
		}
	}
}

func TestMaxMS(t *testing.T) {
	srv := mockApp()
	defer srv.Close()
	steps := []Step{{Name: "Yavaş", Method: "GET", URL: srv.URL + "/slow", MaxMS: 50}}
	Normalize(steps)
	_, _, err := Run(context.Background(), steps, opts())
	if err == nil || !strings.Contains(err.Error(), "sınırı aşıldı") {
		t.Fatalf("süre sınırı aşımı hata vermeli: %v", err)
	}
}

func TestExtractFailureMessages(t *testing.T) {
	srv := mockApp()
	defer srv.Close()
	steps := loginFlow(srv.URL)
	steps[0].Extract[0].Path = "data.yok"
	Normalize(steps)
	_, _, err := Run(context.Background(), steps, opts())
	if err == nil || !strings.Contains(err.Error(), "JSON yolu bulunamadı") {
		t.Fatalf("eksik JSON yolu hata vermeli: %v", err)
	}
}

func TestValidate(t *testing.T) {
	base := func() []Step {
		return []Step{{Name: "a", Method: "GET", URL: "http://x.test/"}}
	}
	cases := []struct {
		name   string
		mutate func(s []Step) []Step
		vars   map[string]string
		want   string
	}{
		{"adımsız", func(s []Step) []Step { return nil }, nil, "adım"},
		{"tanımsız değişken", func(s []Step) []Step { s[0].URL = "http://x.test/{{yok}}"; return s }, nil, "tanımlı değil"},
		{"sonraki adımda çıkarılan değişken önceki adımda kullanılamaz", func(s []Step) []Step {
			s[0].URL = "http://x.test/{{t}}"
			return append(s, Step{Name: "b", Method: "GET", URL: "http://x.test/", Extract: []Extract{{Var: "t", From: "json", Path: "a"}}})
		}, nil, "tanımlı değil"},
		{"geçersiz yöntem", func(s []Step) []Step { s[0].Method = "TRACE"; return s }, nil, "yöntem"},
		{"geçersiz şema", func(s []Step) []Step { s[0].URL = "ftp://x.test"; return s }, nil, "http://"},
		{"geçersiz regex", func(s []Step) []Step {
			s[0].Extract = []Extract{{Var: "a", From: "regex", Path: "("}}
			return s
		}, nil, "düzenli ifade"},
		{"geçersiz durum", func(s []Step) []Step { s[0].ExpectStatus = "abc"; return s }, nil, "durum kodu"},
		{"HEAD ve metin", func(s []Step) []Step { s[0].Method = "HEAD"; s[0].ExpectText = "x"; return s }, nil, "HEAD"},
		{"geçersiz değişken adı", func(s []Step) []Step { return s }, map[string]string{"1x": "a"}, "değişken adı"},
	}
	for _, c := range cases {
		steps := c.mutate(base())
		Normalize(steps)
		err := Validate(steps, c.vars)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: beklenen %q, gelen %v", c.name, c.want, err)
		}
	}
	ok := base()
	Normalize(ok)
	if err := Validate(ok, nil); err != nil {
		t.Errorf("geçerli senaryo reddedildi: %v", err)
	}
}

func TestSaaSBlocksPrivateTargets(t *testing.T) {
	srv := mockApp()
	defer srv.Close()
	steps := []Step{{Name: "a", Method: "GET", URL: srv.URL + "/items"}}
	Normalize(steps)
	o := opts()
	o.BlockPrivate = true
	_, _, err := Run(context.Background(), steps, o)
	if err == nil || !strings.Contains(err.Error(), "SaaS") {
		t.Fatalf("SaaS modunda loopback engellenmeli: %v", err)
	}
}
