package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendDefaultsAllOn(t *testing.T) {
	var s settings
	s.applySend(map[string]string{"APM_SERVER": "x"})
	if s.Send != nil || !s.sends("SEND_HOST_CPU") || s.NoApps || s.NoOTLP {
		t.Fatal("SEND_* yoksa her şey açık olmalı")
	}
}

func TestSendCommentedOff(t *testing.T) {
	// # ile kapatılan satırlar readConf'ta görünmez
	conf := "SEND_HOST_CPU=1\nSEND_APPS=1\n#SEND_CONNS=1\nSEND_HOST_MEM=0\n"
	m := map[string]string{}
	for _, ln := range strings.Split(conf, "\n") {
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		i := strings.IndexByte(ln, '=')
		m[ln[:i]] = ln[i+1:]
	}
	var s settings
	s.applySend(m)
	if !s.sends("SEND_HOST_CPU") || s.sends("SEND_HOST_MEM") || s.sends("SEND_HOST_DISK") {
		t.Fatalf("beklenmeyen: %v", s.Send)
	}
	if s.NoApps || !s.NoConns || !s.NoWeb || !s.NoOTLP || !s.NoUpdate {
		t.Fatalf("türetilen bayraklar yanlış: %+v", s)
	}
	ms := s.filterHost([]metric{{Name: "host.cpu_pct"}, {Name: "host.mem_pct"}, {Name: "host.disk_pct"}, {Name: "host.disk_pct:/var"}, {Name: "agent.up"}, {Name: "proc.x.up"}})
	if len(ms) != 3 || ms[0].Name != "host.cpu_pct" || ms[1].Name != "agent.up" {
		t.Fatalf("süzme hatalı: %v", ms)
	}
}

func TestConfExtrasRoundTrip(t *testing.T) {
	s := settings{Send: map[string]bool{"SEND_HOST_CPU": true}}
	txt := confExtras(s)
	if !strings.Contains(txt, "\nSEND_HOST_CPU=1\n") || !strings.Contains(txt, "\n#SEND_HOST_MEM=1\n") {
		t.Fatal("şablon beklenen satırları içermiyor")
	}
	all := confExtras(settings{})
	for _, it := range dataItems {
		if !strings.Contains(all, "\n"+it.Key+"=1\n") {
			t.Fatalf("%s varsayılanda açık olmalı", it.Key)
		}
	}
}

func TestPostGzipAndFallback(t *testing.T) {
	var enc string
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc = r.Header.Get("Content-Encoding")
		var rd io.Reader = r.Body
		if enc == "gzip" {
			zr, _ := gzip.NewReader(r.Body)
			rd = zr
		} else if gzipRejected {
			// eski sunucu davranışı simülasyonu aşağıda
		}
		json.NewDecoder(rd).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	raw, _ := json.Marshal(map[string]any{"x": strings.Repeat("a", 2000)})
	gzipOK = true
	resp, err := post(srv.Client(), srv.URL, "k", raw)
	if err != nil || resp.StatusCode != 200 || enc != "gzip" || got["x"] == nil {
		t.Fatalf("gzip gönderimi: %v %v enc=%q", err, resp, enc)
	}
	// 400 veren (gzip bilmeyen) sunucu: düz JSON'a dönülür
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "" {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(200)
	}))
	defer old.Close()
	gzipOK = true
	resp, err = post(old.Client(), old.URL, "k", raw)
	if err != nil || resp.StatusCode != 200 || gzipOK {
		t.Fatalf("geri dönüş çalışmadı: %v %v ok=%v", err, resp, gzipOK)
	}
	gzipOK = true
}

var gzipRejected = false

func TestRemoteConfig(t *testing.T) {
	c := decodeConf([]byte(`{"asset_id":1,"config":{"v":3,"send":["SEND_HOST_CPU","SEND_APPS","BOGUS"]}}`))
	if c == nil || c.V != 3 {
		t.Fatal("config çözülemedi")
	}
	var s settings
	s.applyRemote(c)
	if !s.sends("SEND_HOST_CPU") || !s.sends("SEND_APPS") || s.sends("SEND_HOST_MEM") || s.sends("BOGUS") {
		t.Fatalf("seçim yanlış: %v", s.Send)
	}
	if s.NoApps || !s.NoWeb || !s.NoConns || !s.NoOTLP {
		t.Fatalf("bayraklar yanlış: %+v", s)
	}
	if got := s.effective(); got != "HOST_CPU,APPS" {
		t.Fatalf("effective: %q", got)
	}
	if decodeConf([]byte(`{"asset_id":1}`)) != nil {
		t.Fatal("config yokken nil olmalı")
	}
	labels := map[string]string{}
	remoteVer = 3
	cfgLabels(s, labels)
	remoteVer = 0
	if labels["cfg_v"] != "3" || labels["cfg_send"] != "HOST_CPU,APPS" {
		t.Fatalf("etiketler: %v", labels)
	}
	var l settings
	l.apply(map[string]string{"APM_REMOTE_CONFIG": "off"})
	if !l.NoRemote {
		t.Fatal("APM_REMOTE_CONFIG=off okunmadı")
	}
}

func TestInboundAndDSN(t *testing.T) {
	socks := []sock{
		{lip: "10.0.0.5", rip: "10.0.0.9", lport: 5432, rport: 40000, pid: 1},
		{lip: "10.0.0.5", rip: "10.0.0.9", lport: 5432, rport: 40001, pid: 1},
		{lip: "10.0.0.5", rip: "10.0.0.7", lport: 5432, rport: 40002, pid: 1},
		{lip: "127.0.0.1", rip: "127.0.0.1", lport: 5432, rport: 40003, pid: 1},
		{lip: "10.0.0.5", rip: "10.0.0.20", lport: 50000, rport: 443, pid: 2},
	}
	in := buildInbound(socks, map[int][]int{1: {5432}})
	if len(in) != 2 || in[0].Addr != "10.0.0.9" || in[0].N != 2 || in[0].Port != 5432 {
		t.Fatalf("gelen bağlantılar: %+v", in)
	}
	if got := splitDSNs(" a ;; b;;;; c "); len(got) != 3 || got[1] != "b" {
		t.Fatalf("splitDSNs: %v", got)
	}
	if w := withTimeout("postgres://u:p@h/db?sslmode=disable"); !strings.Contains(w, "connect_timeout=5") {
		t.Fatalf("withTimeout: %s", w)
	}
	e := sanitizeErr(errString("dial postgres://u:SIR@h/db failed SIR"), "postgres://u:SIR@h/db")
	if strings.Contains(e, "SIR") {
		t.Fatalf("parola sızdı: %s", e)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestRemoteDB(t *testing.T) {
	c := decodeConf([]byte(`{"config":{"v":2,"send":["SEND_DBSTATS"],"db":{"dsn":"postgres://u:p@h:5432/d?sslmode=require"}}}`))
	if c == nil || c.DB == nil {
		t.Fatal("db çözülemedi")
	}
	s := settings{PGDSN: "yerel"}
	s.applyRemote(c)
	if s.PGDSN != "postgres://u:p@h:5432/d?sslmode=require" || !s.PGRemote {
		t.Fatalf("uzaktan DSN uygulanmadı: %+v", s)
	}
	l := map[string]string{}
	cfgLabels(s, l)
	if l["cfg_db"] != "remote" {
		t.Fatal("cfg_db etiketi yok")
	}
	s2 := settings{PGDSN: "yerel"}
	s2.applyRemote(decodeConf([]byte(`{"config":{"v":3,"send":[]}}`)))
	if s2.PGDSN != "yerel" || s2.PGRemote {
		t.Fatal("db yokken yerel DSN korunmalı")
	}
}
