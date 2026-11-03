package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appmon/internal/cdp"
)

// authSite: çerez tabanlı oturum. Oturum yoksa giriş formu, varsa panel gösterir.
func authSite() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("sid"); err == nil && c.Value == "S1" {
			w.Write([]byte(`<!doctype html><title>Panel</title><h1>Panel</h1><p>Hoş geldin ali</p><a href="/orders">Siparişler</a>`))
			return
		}
		w.Write([]byte(`<!doctype html><title>Giriş</title><h1>Giriş</h1><form method="post" action="/login">
<input name="email" placeholder="E-posta"><input type="password" name="pw"><button type="submit">Oturum aç</button></form>`))
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("email") != "ali@x.test" || r.Form.Get("pw") != "sifre-123" {
			http.Redirect(w, r, "/?hata=1", 302)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "S1", Path: "/", Expires: time.Now().Add(time.Hour)})
		http.Redirect(w, r, "/", 302)
	})
	mux.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("sid"); err != nil || c.Value != "S1" {
			http.Error(w, "yetkisiz", 401)
			return
		}
		w.Write([]byte(`<h1>Siparişler</h1><p>3 sipariş</p>`))
	})
	return httptest.NewServer(mux)
}

func authSteps(base string) []Step {
	return []Step{
		{Name: "Aç", Action: "goto", URL: base + "/"},
		{Name: "Siparişler", Action: "click", Selector: "a", Text: "Siparişler", ExpectText: ""},
		{Name: "Kontrol", Action: "goto", URL: base + "/orders", ExpectText: "3 sipariş"},
	}
}

func TestFormLoginSessionReuseAndRelogin(t *testing.T) {
	needChrome(t)
	srv := authSite()
	defer srv.Close()
	steps := authSteps(srv.URL)
	a := &Auth{Enabled: true, Mode: "form", Username: "ali@x.test", Password: "sifre-123", LoggedInText: "Hoş geldin"}
	if err := ValidateAuth(a, steps); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	res, err := Replay(ctx, steps, nil, Options{Auth: a})
	if err != nil {
		t.Fatalf("giriş + akış başarılı olmalı: %v (%+v)", err, res.Steps)
	}
	if res.Session != "login" || len(res.Steps) != 4 || !strings.Contains(res.Steps[0].Name, "Giriş yapıldı") {
		t.Fatalf("giriş yapılmalıydı: %q %+v", res.Session, res.Steps)
	}
	var sid string
	for _, c := range res.Cookies {
		if c.Name == "sid" {
			sid = c.Value
		}
	}
	if sid != "S1" {
		t.Fatalf("oturum çerezi saklanmalı: %+v", res.Cookies)
	}

	// Saklanan oturum geçerli -> giriş atlanır
	res2, err := Replay(ctx, steps, nil, Options{Auth: a, Cookies: res.Cookies})
	if err != nil || res2.Session != "reused" || !strings.Contains(res2.Steps[0].Name, "atlandı") {
		t.Fatalf("oturum yeniden kullanılmalıydı: %v %q %+v", err, res2.Session, res2.Steps)
	}

	// Geçersiz/düşmüş oturum -> otomatik yeniden giriş
	stale := []Cookie{{Name: "sid", Value: "ESKI", Domain: res.Cookies[0].Domain, Path: "/"}}
	res3, err := Replay(ctx, steps, nil, Options{Auth: a, Cookies: stale})
	if err != nil || res3.Session != "login" {
		t.Fatalf("düşen oturumda yeniden giriş yapılmalı: %v %q", err, res3.Session)
	}

	// ForceLogin
	res4, err := Replay(ctx, steps, nil, Options{Auth: a, Cookies: res.Cookies, ForceLogin: true})
	if err != nil || res4.Session != "login" {
		t.Fatalf("ForceLogin giriş yapmalı: %v %q", err, res4.Session)
	}
}

func TestFormLoginWrongPasswordDoesNotLeak(t *testing.T) {
	needChrome(t)
	srv := authSite()
	defer srv.Close()
	a := &Auth{Enabled: true, Mode: "form", Username: "ali@x.test", Password: "YANLIS-parola"}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Replay(ctx, authSteps(srv.URL), nil, Options{Auth: a})
	if err == nil || !strings.Contains(err.Error(), "giriş başarısız") || strings.Contains(err.Error(), "YANLIS-parola") {
		t.Fatalf("giriş başarısız olmalı, parola sızmamalı: %v", err)
	}
	if len(res.Steps) != 1 || res.Steps[0].OK {
		t.Fatalf("yalnızca başarısız giriş adımı raporlanmalı: %+v", res.Steps)
	}
	if len(res.Cookies) != 0 {
		t.Error("başarısız girişte çerez saklanmamalı")
	}
}

func TestStepsModeLoginBlock(t *testing.T) {
	needChrome(t)
	srv := authSite()
	defer srv.Close()
	steps := []Step{
		{Name: "Aç", Action: "goto", URL: srv.URL + "/"},
		{Name: "E-posta", Action: "type", Selector: `input[name="email"]`, Value: "ali@x.test", Login: true},
		{Name: "Parola", Action: "type", Selector: `input[name="pw"]`, Value: "{{secret_1}}", Secret: true, Login: true},
		{Name: "Gönder", Action: "click", Selector: "button", Text: "Oturum aç", Login: true},
		{Name: "Kontrol", Action: "goto", URL: srv.URL + "/orders", ExpectText: "3 sipariş"},
	}
	vars := map[string]string{"secret_1": "sifre-123"}
	a := &Auth{Enabled: true, Mode: "steps"}
	if err := ValidateAuth(a, steps); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := Replay(ctx, steps, vars, Options{Auth: a})
	if err != nil || res.Session != "login" || len(res.Steps) != 3 { // oturum + Aç + Kontrol
		t.Fatalf("adım modu: %v %q %+v", err, res.Session, res.Steps)
	}
	res2, err := Replay(ctx, steps, vars, Options{Auth: a, Cookies: res.Cookies})
	if err != nil || res2.Session != "reused" {
		t.Fatalf("adım modunda oturum yeniden kullanılmalı: %v %q", err, res2.Session)
	}
}

func TestValidateAuth(t *testing.T) {
	st := []Step{{Name: "a", Action: "goto", URL: "http://x.test/"}, {Name: "b", Action: "click", Selector: "a", Login: true}}
	cases := []struct {
		name string
		a    *Auth
		want string
	}{
		{"mod", &Auth{Enabled: true, Mode: "x"}, "modu"},
		{"form bilgi yok", &Auth{Enabled: true, Mode: "form"}, "kullanıcı adı ve parola"},
		{"steps işaret yok", &Auth{Enabled: true, Mode: "steps"}, ""},
	}
	for _, c := range cases[:2] {
		if err := ValidateAuth(c.a, st); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if err := ValidateAuth(&Auth{Enabled: true, Mode: "steps"}, []Step{{Name: "a", Action: "goto", URL: "http://x.test/"}}); err == nil || !strings.Contains(err.Error(), "işaretleyin") {
		t.Errorf("giriş işareti zorunlu: %v", err)
	}
	if err := ValidateAuth(&Auth{Enabled: true, Mode: "form", Username: "u", Password: "p"}, []Step{{Name: "b", Action: "click", Selector: "a", Login: true}}); err == nil {
		t.Error("giriş dışı adım ve adres gerekli")
	}
	if err := ValidateAuth(nil, nil); err != nil {
		t.Error(err)
	}
}

func TestRecorderStartAuthLogsInUnrecorded(t *testing.T) {
	needChrome(t)
	srv := authSite()
	defer srv.Close()
	m := NewManager(cdp.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := m.StartAuth(ctx, 1, srv.URL+"/", &Auth{Username: "ali@x.test", Password: "yanlis"}); err == nil {
		t.Fatal("yanlış parola reddedilmeliydi")
	}
	s, err := m.StartAuth(ctx, 1, srv.URL+"/", &Auth{Username: "ali@x.test", Password: "sifre-123", LoggedInText: "Hoş geldin"})
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if len(st.Steps) != 1 || strings.Contains(strings.Join(st.Steps, "|"), "sifre") {
		t.Fatalf("yalnızca başlangıç adımı beklenirdi: %v", st.Steps)
	}
	m.Stop(s.ID, 1)
}
