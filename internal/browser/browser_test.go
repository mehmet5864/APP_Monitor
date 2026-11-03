package browser

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appmon/internal/cdp"
)

func needChrome(t *testing.T) {
	if _, err := cdp.FindChrome(""); err != nil {
		t.Skip("Chromium yok:", err)
	}
}

var version = "v1"

func site() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<!doctype html><title>Giriş</title><h1>Hoş geldiniz</h1>
<form id="f" method="post" action="/login"><input name="user" placeholder="Kullanıcı"><input name="pass" type="password" placeholder="Parola">
<button type="submit">Giriş yap</button></form><a href="/about">Hakkında</a>`))
	})
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("user") != "ali" || r.Form.Get("pass") != "gizli123" {
			http.Error(w, "yetkisiz", 401)
			return
		}
		extra := ""
		if version == "v2" {
			extra = `<button>Yeni düğme</button>`
		}
		w.Write([]byte(`<!doctype html><title>Panel</title><h1>Panel</h1><p>Merhaba ali</p><a href="/orders">Siparişler</a>` + extra +
			`<script>fetch('/api/data').then(r=>r.text()).then(t=>{document.body.append(' veri:'+t)})</script>`))
	})
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Write([]byte("tamam"))
	})
	mux.HandleFunc("/about", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`<h1>Hakkında</h1>`)) })
	return httptest.NewServer(mux)
}

func flow(base string) []Step {
	return []Step{
		{Name: "Aç", Action: "goto", URL: base + "/"},
		{Name: "Kullanıcı", Action: "type", Selector: `input[name="user"]`, Value: "ali"},
		{Name: "Parola", Action: "type", Selector: `input[name="pass"]`, Value: "{{secret_1}}", Secret: true},
		{Name: "Gönder", Action: "click", Selector: `button`, Text: "Giriş yap", ExpectText: ""},
		{Name: "Panel", Action: "press", Key: "Tab", ExpectText: "veri:tamam"},
	}
}

func TestReplayLoginFlowAndFingerprint(t *testing.T) {
	needChrome(t)
	srv := site()
	defer srv.Close()
	steps := flow(srv.URL)
	vars := map[string]string{"secret_1": "gizli123"}
	if err := Validate(steps, vars); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := Replay(ctx, steps, vars, Options{Shots: true})
	if err != nil {
		t.Fatalf("oynatma başarılı olmalı: %v (%+v)", err, res.Steps)
	}
	if len(res.Steps) != 5 || res.Steps[4].Page == "" || len(res.Steps[4].Shot) < 500 {
		t.Fatalf("beklenmeyen sonuç: %+v", res.Steps)
	}
	if res.Steps[3].MS < 100 {
		t.Errorf("tıklama sonrası XHR beklenmeliydi, ms=%v", res.Steps[3].MS)
	}
	found := false
	for _, it := range res.Steps[4].Items {
		if it == "h:h1:Panel" {
			found = true
		}
	}
	if !found {
		t.Errorf("parmak izinde başlık yok: %v", res.Steps[4].Items)
	}
	if _, ok := res.Hosts[strings.TrimPrefix(srv.URL, "http://")]; !ok {
		t.Errorf("ağ bağımlılığı çıkarılmadı: %v", res.Hosts)
	}
	// Sayfa değişir -> fark görünür
	prev := res.Steps[4].Items
	version = "v2"
	defer func() { version = "v1" }()
	res2, err := Replay(ctx, steps, vars, Options{})
	if err != nil {
		t.Fatal(err)
	}
	added, removed := Diff(prev, res2.Steps[4].Items)
	if len(added) != 1 || added[0] != "btn:Yeni düğme" || len(removed) != 0 {
		t.Errorf("fark beklenen gibi değil: +%v -%v", added, removed)
	}
}

func TestReplayFailuresDontLeakSecrets(t *testing.T) {
	needChrome(t)
	srv := site()
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// yanlış parola -> 401
	_, err := Replay(ctx, flow(srv.URL), map[string]string{"secret_1": "yanlis-PAROLA"}, Options{})
	if err == nil || !strings.Contains(err.Error(), "401") || strings.Contains(err.Error(), "yanlis-PAROLA") {
		t.Fatalf("401 bekleniyordu, sır sızmamalı: %v", err)
	}
	// öğe yok
	steps := []Step{{Name: "Aç", Action: "goto", URL: srv.URL + "/"}, {Name: "Yok", Action: "click", Selector: "#yok"}}
	_, err = Replay(ctx, steps, nil, Options{StepTimeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "öğe bulunamadı") {
		t.Fatalf("öğe bulunamadı bekleniyordu: %v", err)
	}
	// bağlantı reddi
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	closedAddr := l.Addr().String()
	l.Close()
	_, err = Replay(ctx, []Step{{Name: "Aç", Action: "goto", URL: "http://" + closedAddr + "/"}}, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "ERR_CONNECTION_REFUSED") {
		t.Fatalf("bağlantı hatası bekleniyordu: %v", err)
	}
	// süre sınırı
	steps = []Step{{Name: "Aç", Action: "goto", URL: srv.URL + "/api/data", MaxMS: 20}}
	_, err = Replay(ctx, steps, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "sınırı aşıldı") {
		t.Fatalf("süre sınırı bekleniyordu: %v", err)
	}
}

func TestRecorderCapturesFlow(t *testing.T) {
	needChrome(t)
	srv := site()
	defer srv.Close()
	m := NewManager(cdp.Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s, err := m.Start(ctx, 1, srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(ctx, 1, ""); err == nil {
		t.Error("aynı kiracı için ikinci oturum reddedilmeli")
	}
	frame, seq, _ := s.Frame(ctx, 0, 5*time.Second)
	if len(frame) < 500 || seq == 0 {
		t.Fatalf("ekran karesi gelmedi (%d bayt)", len(frame))
	}
	locate := func(js string) (float64, float64) {
		var pt Point
		if err := s.page.Eval(ctx, js, &pt); err != nil {
			t.Fatal(err)
		}
		return pt.X, pt.Y
	}
	center := func(sel string) (float64, float64) {
		return locate(`(() => { const r = document.querySelector('` + sel + `').getBoundingClientRect(); return {X: r.left + r.width/2, Y: r.top + r.height/2}; })()`)
	}
	x, y := center(`input[name="user"]`)
	must := func(in Input) {
		if err := s.Input(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	must(Input{Type: "click", X: x, Y: y})
	must(Input{Type: "text", Text: "al"})
	must(Input{Type: "text", Text: "ix"})
	must(Input{Type: "key", Key: "Backspace"})
	must(Input{Type: "text", Text: "i"})
	must(Input{Type: "key", Key: "Backspace"})
	x, y = center(`input[name="pass"]`)
	must(Input{Type: "click", X: x, Y: y})
	must(Input{Type: "text", Text: "gizli123"})
	must(Input{Type: "key", Key: "Enter"})
	time.Sleep(300 * time.Millisecond)
	st := s.State()
	if !strings.Contains(st.URL, "/login") {
		t.Fatalf("giriş sonrası /login bekleniyordu: %s", st.URL)
	}
	steps, secrets, err := m.Stop(s.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	var vals []string
	for _, x := range steps {
		vals = append(vals, x.Action+":"+x.Value)
	}
	t.Logf("adımlar: %v secrets=%d", vals, len(secrets))
	if steps[0].Action != "goto" || secrets["secret_1"] != "gizli123" {
		t.Fatalf("gizli değer ayrı tutulmalı: %+v %v", steps, secrets)
	}
	for _, x := range steps {
		if strings.Contains(x.Value, "gizli123") || strings.Contains(x.Name, "gizli123") {
			t.Fatalf("gizli değer adımda sızdı: %+v", x)
		}
	}
	var typed *Step
	for i := range steps {
		if steps[i].Action == "type" && !steps[i].Secret {
			typed = &steps[i]
		}
	}
	if typed == nil || typed.Value != "ali" {
		t.Fatalf("yazılan değer birleştirilmeli (ali): %+v", steps)
	}
	// kaydedilen akış aynen oynatılabilmeli
	if err := Validate(steps, secrets); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(ctx, steps, secrets, Options{}); err != nil {
		t.Fatalf("kaydedilen akış oynatılamadı: %v", err)
	}
}
