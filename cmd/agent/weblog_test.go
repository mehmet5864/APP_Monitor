package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAppmon(t *testing.T) {
	r, ok := parseAppmon("appmon h=Shop.Example.com:443 m=GET st=503 rt=0.250 uri=/siparis/123?x=1")
	if !ok || r.app != "Shop.Example.com" || r.method != "GET" || r.status != 503 || !r.timed || r.ms != 250 || r.path != "/siparis/123?x=1" {
		t.Fatalf("beklenmeyen: %+v ok=%v", r, ok)
	}
	r, _ = parseAppmon("appmon h=a m=POST st=200 rt_us=1500 uri=/x")
	if !r.timed || r.ms != 1.5 {
		t.Fatalf("rt_us milisaniyeye çevrilmeli: %+v", r)
	}
	for _, bad := range []string{"x h=a st=200", "appmon h=a m=GET st=999 uri=/", "appmon h=a m=GET uri=/"} {
		if _, ok := parseAppmon(bad); ok {
			t.Fatalf("geçersiz satır kabul edildi: %q", bad)
		}
	}
}

func TestParseCombined(t *testing.T) {
	base := `10.0.0.1 - bob [10/Oct/2026:13:55:36 +0300] "GET /a/b?q=1 HTTP/1.1" 404 512 "-" "curl/8"`
	r, ok := parseCombined(base)
	if !ok || r.method != "GET" || r.path != "/a/b?q=1" || r.status != 404 || r.timed {
		t.Fatalf("süresiz satır: %+v ok=%v", r, ok)
	}
	r, _ = parseCombined(base + ` rt=0.120`)
	if !r.timed || r.ms != 120 {
		t.Fatalf("rt= alınmalı: %+v", r)
	}
	r, _ = parseCombined(base + ` request_time=2.5 upstream=1`)
	if !r.timed || r.ms != 2500 {
		t.Fatalf("request_time= alınmalı: %+v", r)
	}
	r, _ = parseCombined(`1.1.1.1 - - [x] "GET / HTTP/1.0" 200 -`)
	if r.status != 200 || r.timed {
		t.Fatalf("common biçim: %+v", r)
	}
	if _, ok := parseCombined("garbage"); ok {
		t.Fatal("çöp satır reddedilmeli")
	}
}

func TestParseIIS(t *testing.T) {
	f := &iisFields{}
	f.parse("#Fields: date time s-sitename cs-method cs-uri-stem cs-uri-query sc-status time-taken")
	r, ok := f.parse("2026-11-14 10:00:00 W3SVC1 GET /urun/55 a=1 200 340")
	if !ok || r.app != "W3SVC1" || r.method != "GET" || r.path != "/urun/55" || r.status != 200 || !r.timed || r.ms != 340 {
		t.Fatalf("IIS satırı: %+v ok=%v", r, ok)
	}
	if _, ok := (&iisFields{}).parse("2026-11-14 10:00:00 W3SVC1 GET / - 200 1"); ok {
		t.Fatal("başlıksız IIS satırı çözülmemeli")
	}
}

func TestWebAggTakeRestore(t *testing.T) {
	a := newWebAgg()
	a.add("site", webRec{method: "GET", path: "/o/1", status: 200, ms: 30, timed: true})
	a.add("site", webRec{method: "GET", path: "/o/2", status: 500, ms: 700, timed: true})
	a.add("site", webRec{method: "GET", path: "/o/3", status: 404})
	list := a.take()
	if len(list) != 1 {
		t.Fatalf("yollar normalleşip tek uç nokta olmalı: %+v", list)
	}
	e := list[0]
	if e.Endpoint != "GET /o/{id}" || e.Reqs != 3 || e.E5 != 1 || e.E4 != 1 || e.Untimed != 1 || e.SumMS != 730 || e.MaxMS != 700 {
		t.Fatalf("özet: %+v", e)
	}
	var tot int64
	for _, c := range e.B {
		tot += c
	}
	if tot != 2 {
		t.Fatalf("yalnızca süreli istekler kovalara girmeli: %d", tot)
	}
	if len(a.take()) != 0 {
		t.Fatal("take sonrası boş olmalı")
	}
	a.restore(list)
	a.add("site", webRec{method: "GET", path: "/o/9", status: 200, ms: 10, timed: true})
	l2 := a.take()
	if len(l2) != 1 || l2[0].Reqs != 4 {
		t.Fatalf("geri koyma birleşmeli: %+v", l2)
	}
}

func TestWebAggCaps(t *testing.T) {
	a := newWebAgg()
	for i := 0; i < maxWebEndpoints+50; i++ {
		a.add("site", webRec{method: "GET", path: "/p" + strings.Repeat("x", i%7) + "/seg" + string(rune('a'+i%26)) + strings.Repeat("y", i/26), status: 200, ms: 1, timed: true})
	}
	if n := len(a.eps["site"]); n > maxWebEndpoints {
		t.Fatalf("uç nokta sınırı aşıldı: %d", n)
	}
	for i := 0; i < maxWebApps+5; i++ {
		a.add("app"+strings.Repeat("z", i), webRec{method: "GET", path: "/", status: 200})
	}
	if len(a.apps) > maxWebApps {
		t.Fatalf("uygulama sınırı aşıldı: %d", len(a.apps))
	}
}

func TestWebLogsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "access.log")
	os.WriteFile(p, []byte(`1.1.1.1 - - [x] "GET /eski HTTP/1.1" 200 1 "-" "-"`+"\n"), 0o644)
	w := newWebLogs("pc")
	w.refresh([]webCand{{server: "nginx", globs: []string{p}}})
	w.poll() // başlangıç: dosya sonu, eski satır sayılmaz
	if len(w.agg.take()) != 0 {
		t.Fatal("geçmiş satırlar sayılmamalı")
	}
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`1.1.1.1 - - [x] "GET /yeni/5 HTTP/1.1" 200 1 "-" "-" rt=0.050` + "\n" + `1.1.1.1 - - [x] "POST /yeni/6 HTTP/1.1" 500 1 "-" "-" rt=0.500` + "\n")
	f.Close()
	w.poll()
	l := w.agg.take()
	if len(l) != 2 || l[0].App != "pc-nginx" {
		t.Fatalf("iki uç nokta ve varsayılan uygulama adı beklenir: %+v", l)
	}
	if w.note != "1 günlük okunuyor" {
		t.Fatalf("durum: %q", w.note)
	}
	// Ajanın kendi günlüğü varsa varsayılan nginx günlüğü atlanır (çift sayım olmasın).
	own := filepath.Join(dir, "own.log")
	os.WriteFile(own, nil, 0o644)
	w.refresh([]webCand{{server: "appmon", globs: []string{own}}, {server: "nginx", globs: []string{p}}})
	if len(w.srcs) != 1 {
		t.Fatalf("yalnızca appmon günlüğü izlenmeli: %d", len(w.srcs))
	}
	w.poll() // ilk açılış: dosya sonu
	// Döndürme: dosya kısalırsa baştan okunur.
	os.WriteFile(own, []byte(strings.Repeat("eski-uzun-satir-", 8)+"\n"), 0o644)
	w.poll()
	os.WriteFile(own, []byte("appmon h=site m=GET st=200 rt=0.010 uri=/a\n"), 0o644)
	w.poll()
	if l := w.agg.take(); len(l) != 1 || l[0].App != "site" {
		t.Fatalf("kısalan dosya baştan okunmalı: %+v", l)
	}
	// Okunamayan dosya durumu
	w.srcs[own].tail.path = filepath.Join(dir, "yok.log")
	w.poll()
	if !strings.Contains(w.note, "okunamıyor") {
		t.Fatalf("okunamıyor notu beklenir: %q", w.note)
	}
}

func TestIISRefreshLoadsHeader(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "W3SVC2")
	os.MkdirAll(d, 0o755)
	p := filepath.Join(d, "u_ex261114.log")
	os.WriteFile(p, []byte("#Fields: date time s-sitename cs-method cs-uri-stem sc-status time-taken\n2026-11-14 10:00:00 W3SVC2 GET /eski 200 5\n"), 0o644)
	w := newWebLogs("win")
	w.refresh([]webCand{{server: "iis", perDir: true, globs: []string{filepath.Join(dir, "W3SVC*", "u_ex*.log")}}})
	w.poll()
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("2026-11-14 10:00:05 W3SVC2 GET /yeni 200 42\n")
	f.Close()
	w.poll()
	l := w.agg.take()
	if len(l) != 1 || l[0].App != "W3SVC2" || l[0].Endpoint != "GET /yeni" || l[0].SumMS != 42 {
		t.Fatalf("IIS: başlık baştan okunup yeni satır sayılmalı: %+v", l)
	}
}

func TestScanOverridesAndConf(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "site.conf")
	os.WriteFile(a, []byte("access_log /var/log/nginx/http-level.log main;\nserver {\n  # access_log /x;\n  access_log /var/log/nginx/site.log main;\n  access_log off;\n}\nserver { access_log /var/log/nginx/s2.log; }\n"), 0o644)
	got := scanOverrides("nginx", []string{a}, "")
	if len(got) != 2 || !strings.Contains(got[0], "site.conf:4") || !strings.Contains(got[1], "site.conf:7") {
		t.Fatalf("yalnızca sunucu düzeyindeki satırlar bulunmalı: %v", got)
	}
	m := filepath.Join(dir, "nginx.conf")
	os.WriteFile(m, []byte("http {\n  access_log /var/log/nginx/access.log main;\n  server {\n    location / { access_log /x/loc.log; }\n  }\n}\n"), 0o644)
	if got := scanOverrides("nginx", []string{m}, ""); len(got) != 1 || !strings.Contains(got[0], "nginx.conf:4") {
		t.Fatalf("nginx.conf: yalnızca location düzeyi: %v", got)
	}
	b := filepath.Join(dir, "vh.conf")
	os.WriteFile(b, []byte("CustomLog logs/main.log combined\n<VirtualHost *:80>\n  CustomLog logs/x.log combined\n  CustomLog /var/log/appmon/access.log appmon_fmt\n</VirtualHost>\n"), 0o644)
	if got := scanOverrides("apache", []string{b}, ""); len(got) != 1 || !strings.Contains(got[0], "vh.conf:3") {
		t.Fatalf("Apache: yalnızca VirtualHost içindeki kendi satır: %v", got)
	}
	if !strings.Contains(nginxConf(), "appmon_fmt") || !strings.Contains(apacheConf(), "rt_us=%D") {
		t.Fatal("ayar içeriği eksik")
	}
}
