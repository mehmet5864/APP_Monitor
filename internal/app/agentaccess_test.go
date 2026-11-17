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

func TestAgentAccessIngest(t *testing.T) {
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
	v, _ := vault.New(bytes.Repeat([]byte{7}, 32))
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 4}
	if _, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()
	call := func(method, path string, body any) (int, []byte) {
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
		return resp.StatusCode, b
	}
	b := [12]int64{}
	b[2], b[4] = 6, 2 // 8 süreli istek
	acc := []map[string]any{
		{"app": "shop.example.com", "endpoint": "GET /siparis/123", "reqs": 10, "e4": 1, "e5": 1, "untimed": 2, "sum_ms": 400.0, "max_ms": 120.0, "b": b},
		{"app": "bozuk app", "endpoint": "GET /x", "reqs": 1, "b": [12]int64{}},                                         // geçersiz ad
		{"app": "x", "endpoint": "GET /y", "reqs": 5, "untimed": 9, "b": [12]int64{}},                                   // untimed > reqs
		{"app": "x", "endpoint": "GET /y", "reqs": 5, "untimed": 0, "b": [12]int64{9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}, // kova toplamı > süreli
		{"app": "iis-only", "endpoint": "GET /a", "reqs": 4, "untimed": 4, "b": [12]int64{}},                            // tamamen süresiz
	}
	if c, body := call("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": "web-1", "kind": "host"}, "metrics": []any{}, "access": acc}); c != 202 {
		t.Fatalf("ingest: %d %s", c, body)
	}
	s.flushSyslog(t.Context())
	c, body := call("GET", "/api/perf/apps?window=1h", nil)
	if c != 200 {
		t.Fatalf("perf apps: %d %s", c, body)
	}
	var out struct {
		Apps []map[string]any `json:"apps"`
	}
	json.Unmarshal(body, &out)
	by := map[string]map[string]any{}
	for _, a := range out.Apps {
		by[a["app"].(string)] = a
	}
	if len(by) != 2 || by["shop.example.com"] == nil || by["iis-only"] == nil {
		t.Fatalf("yalnızca geçerli iki uygulama beklenir: %v", by)
	}
	sh := by["shop.example.com"]
	if sh["reqs"].(float64) != 10 || sh["untimed"].(float64) != 2 || sh["err5xx"].(float64) != 1 {
		t.Fatalf("sayılar: %v", sh)
	}
	if avg := sh["avg_ms"].(float64); avg != 50 { // 400 ms / 8 süreli istek
		t.Fatalf("ortalama yalnızca süreli isteklerle hesaplanmalı: %v", avg)
	}
	if sh["p95_ms"] == nil {
		t.Fatalf("süreli istek varken p95 olmalı: %v", sh)
	}
	io := by["iis-only"]
	if io["reqs"].(float64) != 4 || io["avg_ms"] != nil || io["p95_ms"] != nil {
		t.Fatalf("süresiz uygulamada gecikme alanı olmamalı: %v", io)
	}
	// Uç nokta yolu sunucuda da normalleştirilir.
	var id float64 = sh["asset_id"].(float64)
	_, eb := call("GET", "/api/perf/endpoints?asset="+itoa(int(id))+"&window=1h", nil)
	var ep struct {
		Endpoints []map[string]any `json:"endpoints"`
	}
	json.Unmarshal(eb, &ep)
	if len(ep.Endpoints) != 1 || ep.Endpoints[0]["endpoint"] != "GET /siparis/{id}" {
		t.Fatalf("uç nokta: %v", ep.Endpoints)
	}
}
