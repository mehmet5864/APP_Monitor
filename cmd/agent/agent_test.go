package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadConfAndApply(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.conf")
	os.WriteFile(p, []byte("# yorum\nAPM_SERVER=https://apm.local\nAPM_KEY=\"k1\"\nAPM_INTERVAL=30\nAPM_WATCH=nginx, sshd\nAPM_INSECURE=true\n"), 0o600)
	m, err := readConf(p)
	if err != nil {
		t.Fatal(err)
	}
	s := settings{Interval: 15 * time.Second}
	s.apply(m)
	if s.Server != "https://apm.local" || s.Key != "k1" || s.Interval != 30*time.Second || !s.Insecure || len(splitList(s.Watch)) != 2 {
		t.Fatalf("beklenmeyen ayarlar: %+v", s)
	}
}

func TestSendAndBufferFormat(t *testing.T) {
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "k" {
			w.WriteHeader(401)
			return
		}
		var v map[string]any
		json.NewDecoder(r.Body).Decode(&v)
		got.Store(v)
	}))
	defer srv.Close()
	now := time.Now()
	cfg := settings{Server: srv.URL, Key: "k", Name: "x"}
	if err := send(newClient(cfg), srv.URL, cfg, []metric{{Name: "host.cpu_pct", Value: 1, Time: &now}}); err != nil {
		t.Fatal(err)
	}
	v := got.Load().(map[string]any)
	ms := v["metrics"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["time"] == nil {
		t.Fatalf("zaman damgası gitmedi: %v", v)
	}
	cfg.Key = "yanlis"
	if err := send(newClient(cfg), srv.URL, cfg, nil); err == nil {
		t.Fatal("401 hata döndürmeli")
	}
}

func TestWatchMetrics(t *testing.T) {
	ms := watchMetrics([]string{"yok-boyle-bir-surec-x"})
	if len(ms) != 2 || ms[0].Value != 0 {
		t.Fatalf("beklenmeyen: %+v", ms)
	}
}

func TestLogWatch(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/app.log"
	os.WriteFile(path, []byte("ERROR eski satir\n"), 0o644) // geçmiş sayılmamalı
	ws, err := parseLogSpecs("hata::" + path + `::ERROR|FATAL ;; x::` + path + "::zzz")
	if err != nil || len(ws) != 2 {
		t.Fatal(err, ws)
	}
	for _, w := range ws {
		if m, l, ok := w.poll(); m != 0 || l != 0 || !ok {
			t.Fatalf("başlangıç: %d %d %v", m, l, ok)
		}
	}
	w := ws[0]
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("tamam\nERROR bir\nFATAL iki\nyarim ERR")
	if m, l, _ := w.poll(); m != 2 || l != 3 {
		t.Fatalf("1. tur: %d %d", m, l)
	}
	f.WriteString("OR uc\n") // yarım satır tamamlandı -> eşleşir
	if m, l, _ := w.poll(); m != 1 || l != 1 {
		t.Fatalf("2. tur: %d %d", m, l)
	}
	f.Close()
	// döndürme: dosya yeniden oluşur
	os.Rename(path, path+".1")
	os.WriteFile(path, []byte("ERROR yeni dosya\nok\n"), 0o644)
	if m, l, ok := w.poll(); m != 1 || l != 2 || !ok {
		t.Fatalf("döndürme: %d %d %v", m, l, ok)
	}
	// kısaltma
	os.WriteFile(path, []byte("FATAL\n"), 0o644)
	if m, l, _ := w.poll(); m != 1 || l != 1 {
		t.Fatalf("kısaltma: %d %d", m, l)
	}
	os.Remove(path)
	if _, _, ok := w.poll(); ok {
		t.Fatal("silinen dosya okunabilir sayılmamalı")
	}
	for _, bad := range []string{"a::b", "a b::/x::y", "a::/x::(", "::/x::y", "a::/x::y;;a::/x::z"} {
		if _, err := parseLogSpecs(bad); err == nil {
			t.Errorf("geçersiz kabul edildi: %q", bad)
		}
	}
}
