package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"appmon/internal/db"
	"appmon/internal/vault"
	"appmon/web"
)

func TestMonitoringPause(t *testing.T) {
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
	v, _ := vault.New(bytes.Repeat([]byte{5}, 32))
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 3}
	if _, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()
	do := func(key, method, path string, body any) (int, []byte) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	root := "rootkey-0123456789"
	if c, b := do(root, "POST", "/api/probes", map[string]any{"asset_name": "duraklat-test", "type": "tcp", "target": "127.0.0.1:9", "interval_sec": 5, "timeout_ms": 500}); c != 201 {
		t.Fatalf("probe: %d %s", c, b)
	}
	// görüntüleyici duraklatamaz
	_, kb := do(root, "POST", "/api/keys", map[string]any{"label": "izleyici", "role": "viewer"})
	var kr struct{ Key string }
	json.Unmarshal(kb, &kr)
	if kr.Key != "" {
		if c, _ := do(kr.Key, "PUT", "/api/monitoring", map[string]any{"paused": true}); c != 403 {
			t.Errorf("görüntüleyici duraklatabildi: %d", c)
		}
	}
	if c, _ := do(root, "PUT", "/api/monitoring", map[string]any{"paused": true}); c != 403 {
		t.Errorf("parolasız duraklatma kabul edildi: %d", c)
	}
	if c, _ := do(root, "PUT", "/api/monitoring", map[string]any{"paused": true, "api_key": "yanlis-anahtar"}); c != 403 {
		t.Errorf("yanlış doğrulama kabul edildi: %d", c)
	}
	if c, b := do(root, "PUT", "/api/monitoring", map[string]any{"paused": true, "api_key": root}); c != 200 {
		t.Fatalf("duraklat: %d %s", c, b)
	}
	d.Exec(`UPDATE probes SET last_run = NULL`)
	s.runDueProbes(t.Context())
	var n int
	d.QueryRow(`SELECT count(*) FROM probes WHERE last_run IS NOT NULL`).Scan(&n)
	if n != 0 {
		t.Error("duraklatılmışken probe çalıştı")
	}
	var ov struct {
		Monitoring struct{ Paused bool } `json:"monitoring"`
	}
	_, b := do(root, "GET", "/api/overview", nil)
	json.Unmarshal(b, &ov)
	if !ov.Monitoring.Paused {
		t.Errorf("overview duraklatmayı göstermiyor: %s", b)
	}
	if c, _ := do(root, "POST", "/api/crawl", map[string]any{"url": "https://example.com"}); c != 409 {
		t.Errorf("duraklatılmışken Keşfet isteği alındı: %d", c)
	}
	if c, _ := do(root, "PUT", "/api/monitoring", map[string]any{"paused": false, "api_key": root}); c != 200 {
		t.Fatal("başlat")
	}
	d.Exec(`UPDATE probes SET last_run = NULL`)
	s.runDueProbes(t.Context())
	d.QueryRow(`SELECT count(*) FROM probes WHERE last_run IS NOT NULL`).Scan(&n)
	if n != 1 {
		t.Error("başlatıldıktan sonra probe çalışmadı")
	}
}
