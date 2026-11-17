package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"appmon/internal/db"
	"appmon/internal/vault"
	"appmon/web"
)

func TestAgentRemoteUpdate(t *testing.T) {
	dsn := os.Getenv("APM_TEST_DSN")
	if dsn == "" {
		t.Skip("APM_TEST_DSN yok")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	d.Exec(`TRUNCATE tenants RESTART IDENTITY CASCADE`)
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := []byte("agent-binary-v2")
	os.WriteFile(filepath.Join(dir, "appmon-agent-linux-amd64"), bin, 0o755)
	sum := sha256.Sum256(bin)
	newSHA := hex.EncodeToString(sum[:])
	v, _ := vault.New(bytes.Repeat([]byte{7}, 32))
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 4, AgentDir: dir}
	if _, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()
	call := func(method, path string, body any) (int, map[string]any) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		return resp.StatusCode, m
	}
	hb := func(name, sha string) map[string]any {
		labels := map[string]string{"os": "linux", "arch": "amd64", "agent_version": "v1"}
		if sha != "" {
			labels["agent_sha"] = sha
		}
		_, m := call("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": name, "labels": labels}, "metrics": []any{}})
		return m
	}
	// Eski ajan (agent_sha yok): teklif yok, listede desteklenmiyor.
	if m := hb("old", ""); m["update"] != nil {
		t.Fatalf("eski ajana teklif verilmemeli: %v", m)
	}
	// Yeni ajan, farklı özet; istek yokken teklif yok.
	if m := hb("web", "aaaa"); m["update"] != nil {
		t.Fatalf("istek yokken teklif olmamalı: %v", m)
	}
	_, list := call("GET", "/api/agent/updates", nil)
	rows := list["agents"].([]any)
	if len(rows) != 2 {
		t.Fatalf("2 ajan beklenir: %v", list)
	}
	var webID float64
	for _, r := range rows {
		m := r.(map[string]any)
		if m["name"] == "web" {
			webID = m["asset_id"].(float64)
			if m["available"] != true || m["supported"] != true {
				t.Fatalf("web için güncelleme mevcut olmalı: %v", m)
			}
		} else if m["supported"] != false {
			t.Fatalf("eski ajan desteklenmiyor görünmeli: %v", m)
		}
	}
	// Yönetici isteği: teklif gelir.
	if c, m := call("POST", "/api/agent/update", map[string]any{"asset_ids": []float64{webID}}); c != 200 || m["updated"].(float64) != 1 {
		t.Fatalf("istek: %d %v", c, m)
	}
	m := hb("web", "aaaa")
	up, _ := m["update"].(map[string]any)
	if up == nil || up["sha256"] != newSHA || up["name"] != "appmon-agent-linux-amd64" {
		t.Fatalf("teklif beklenir: %v", m)
	}
	// Ajan güncellendi (özet eşit): teklif yok ve istek temizlenir.
	if m := hb("web", newSHA); m["update"] != nil {
		t.Fatalf("güncel ajana teklif olmamalı: %v", m)
	}
	var req bool
	d.QueryRow(`SELECT agent_update_req FROM assets WHERE name = 'web'`).Scan(&req)
	if req {
		t.Fatal("istek tamamlanınca temizlenmeli")
	}
	// Otomatik güncelleme: istek olmadan teklif.
	if c, _ := call("PUT", "/api/agent/auto-update", map[string]any{"enabled": true}); c != 200 {
		t.Fatal("auto-update ayarlanamadı")
	}
	if m := hb("web", "bbbb"); m["update"] == nil {
		t.Fatalf("otomatik modda teklif beklenir: %v", m)
	}
	call("PUT", "/api/agent/auto-update", map[string]any{"enabled": false})
	if m := hb("web", "bbbb"); m["update"] != nil {
		t.Fatalf("otomatik kapalıyken teklif olmamalı: %v", m)
	}
	// "Tümü" yalnızca destekleyen ajanları etkiler; iptal eder.
	if _, m := call("POST", "/api/agent/update", map[string]any{"all": true}); m["updated"].(float64) != 1 {
		t.Fatalf("all yalnızca destekleyen ajanı seçmeli: %v", m)
	}
	if _, m := call("POST", "/api/agent/update", map[string]any{"all": true, "cancel": true}); m["updated"].(float64) != 1 {
		t.Fatalf("iptal: %v", m)
	}
	// Dosya yokken istek reddedilir (AgentDir boş sunucu).
	s.AgentDir = ""
	if c, _ := call("POST", "/api/agent/update", map[string]any{"all": true}); c != 400 {
		t.Fatalf("ajan dosyası yokken 400 beklenir: %d", c)
	}
}
