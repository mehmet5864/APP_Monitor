package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"appmon/internal/db"
	"appmon/internal/vault"
	"appmon/web"
)

// Çok kiracılı izolasyon testi. Boş bir PostgreSQL gerektirir:
//
//	APM_TEST_DSN="postgres://appmon:pw@localhost:5432/appmon_test?sslmode=disable" go test ./internal/app -run TestTenantIsolation
func TestTenantIsolation(t *testing.T) {
	dsn := os.Getenv("APM_TEST_DSN")
	if dsn == "" {
		t.Skip("APM_TEST_DSN yok")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, tb := range []string{"tenants"} { // temiz başla
		d.Exec(`TRUNCATE ` + tb + ` RESTART IDENTITY CASCADE`)
	}
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	v, err := vault.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 4}
	if _, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()

	call := func(key, method, path string, body any) (int, string) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("X-API-Key", key)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	idOf := func(body string) int64 {
		var o struct{ ID int64 }
		json.Unmarshal([]byte(body), &o)
		return o.ID
	}
	must := func(code int, want int, body, what string) string {
		t.Helper()
		if code != want {
			t.Fatalf("%s: %d (beklenen %d): %s", what, code, want, body)
		}
		return body
	}
	root := "rootkey-0123456789"

	// --- kiracıları aç ---
	for _, n := range []string{"A", "B"} {
		c, b := call(root, "POST", "/api/tenants", map[string]any{"name": "Kurum " + n, "slug": "kurum-" + strings.ToLower(n),
			"admin_username": "admin" + strings.ToLower(n), "admin_password": "Parola-" + n + "-12345", "asset_limit": 10, "min_interval_sec": 10})
		must(c, 201, b, "kiracı "+n)
	}
	if c, b := call(root, "POST", "/api/tenants", map[string]any{"name": "Kopya", "admin_username": "admina", "admin_password": "Parola-X-12345678"}); c != 409 {
		t.Fatalf("aynı kullanıcı adı başka kiracıda kabul edildi: %d %s", c, b)
	}
	if c, b := call(root, "POST", "/api/tenants", map[string]any{"name": "Kurum C", "admin_username": "adminc", "admin_password": "Parola-C-123456789"}); c != 201 {
		t.Fatalf("3. kiracı açılamadı: %d %s", c, b)
	}
	if c, _ := call(root, "POST", "/api/tenants", map[string]any{"name": "Kurum D", "admin_username": "admind", "admin_password": "Parola-D-123456789"}); c != 409 {
		t.Fatalf("kiracı üst sınırı uygulanmadı: %d", c)
	}
	login := func(u, p string) string {
		c, b := call("", "POST", "/api/login", map[string]string{"username": u, "password": p})
		must(c, 200, b, "giriş "+u)
		var o struct{ Key string }
		json.Unmarshal([]byte(b), &o)
		return o.Key
	}
	ka, kb := login("admina", "Parola-A-12345"), login("adminb", "Parola-B-12345")

	// --- A kiracısında kaynaklar ---
	ids := map[string]int64{}
	c, b := call(ka, "POST", "/api/probes", map[string]any{"asset_name": "ISO-A-asset", "type": "tcp", "target": "127.0.0.1:9", "interval_sec": 30, "timeout_ms": 1000})
	must(c, 201, b, "probe")
	ids["probe"] = idOf(b)
	var pr struct {
		AssetID int64 `json:"asset_id"`
	}
	json.Unmarshal([]byte(b), &pr)
	ids["asset"] = pr.AssetID
	c, b = call(ka, "POST", "/api/probes", map[string]any{"asset_name": "ISO-A-asset2", "type": "tcp", "target": "127.0.0.1:10", "interval_sec": 30, "timeout_ms": 1000})
	must(c, 201, b, "probe2")
	var pr2 struct {
		AssetID int64 `json:"asset_id"`
	}
	json.Unmarshal([]byte(b), &pr2)
	c, b = call(ka, "POST", "/api/scenarios", map[string]any{"asset_name": "ISO-A-asset", "name": "ISO-A-senaryo", "interval_sec": 60, "timeout_ms": 5000,
		"steps": []map[string]any{{"name": "s1", "method": "GET", "url": "https://example.com/", "expect_status": "200"}}})
	must(c, 201, b, "senaryo")
	ids["scenario"] = idOf(b)
	c, b = call(ka, "POST", "/api/alert-rules", map[string]any{"name": "ISO-A-kural", "metric": "probe.up", "comparator": "<", "threshold": 1, "window_sec": 60, "severity": "critical"})
	must(c, 201, b, "kural")
	ids["rule"] = idOf(b)
	c, b = call(ka, "POST", "/api/secrets", map[string]any{"name": "ISO-A-kasa", "kind": "token", "value": "gizli-deger-123"})
	must(c, 201, b, "kasa")
	ids["secret"] = idOf(b)
	now := time.Now()
	c, b = call(ka, "POST", "/api/maintenance", map[string]any{"name": "ISO-A-bakim", "starts_at": now, "ends_at": now.Add(time.Hour), "repeat": ""})
	if c == 201 {
		ids["maint"] = idOf(b)
	} else {
		t.Logf("bakım penceresi oluşturulamadı (%d %s); atlanıyor", c, b)
	}
	c, b = call(ka, "POST", "/api/keys", map[string]any{"label": "ISO-A-anahtar", "role": "viewer"})
	if c == 201 {
		ids["key"] = idOf(b)
	}
	c, b = call(ka, "POST", "/api/topology/edges", map[string]any{"from_id": ids["asset"], "to_id": pr2.AssetID})
	if c == 201 || c == 200 {
		ids["edge"] = idOf(b)
	}
	var uid int64
	d.QueryRow(`SELECT id FROM users WHERE username = 'admina'`).Scan(&uid)
	ids["user"] = uid
	var cj int64
	if err := d.QueryRow(`INSERT INTO crawl_jobs (tenant_id, url, status, requested_by) SELECT tenant_id, 'https://iso-a.example', 'pending', 'admina' FROM users WHERE username='admina' RETURNING id`).Scan(&cj); err != nil {
		t.Logf("crawl_jobs eklenemedi: %v", err)
	}
	ids["crawl"] = cj
	t.Logf("kimlikler: %v", ids)
	if ids["asset"] == 0 || ids["rule"] == 0 {
		t.Fatalf("hazırlık eksik: %v", ids)
	}

	// --- B, A'nın kimliklerine dokunamaz ---
	type try struct{ method, path, kind string }
	cases := []try{
		{"PUT", "/api/users/%d", "user"}, {"DELETE", "/api/users/%d", "user"},
		{"DELETE", "/api/assets/%d", "asset"}, {"PATCH", "/api/assets/%d", "asset"}, {"GET", "/api/assets/%d/metrics", "asset"}, {"GET", "/api/assets/%d/impact", "asset"},
		{"GET", "/api/assets/%d/disks", "asset"}, {"PUT", "/api/assets/%d/meta", "asset"}, {"PUT", "/api/assets/%d/sla", "asset"}, {"PUT", "/api/assets/%d/status-page", "asset"},
		{"PUT", "/api/probes/%d", "probe"}, {"DELETE", "/api/probes/%d", "probe"},
		{"PUT", "/api/scenarios/%d", "scenario"}, {"DELETE", "/api/scenarios/%d", "scenario"}, {"POST", "/api/scenarios/%d/run", "scenario"}, {"POST", "/api/scenarios/%d/retry-login", "scenario"},
		{"GET", "/api/scenarios/%d/last-change", "scenario"}, {"POST", "/api/scenarios/%d/ack-changes", "scenario"}, {"GET", "/api/scenarios/%d/ignores", "scenario"}, {"POST", "/api/scenarios/%d/ignores", "scenario"},
		{"GET", "/api/scenarios/%d/runs", "scenario"}, {"GET", "/api/scenarios/%d/runs/1/shots/0", "scenario"},
		{"PUT", "/api/secrets/%d", "secret"}, {"DELETE", "/api/secrets/%d", "secret"},
		{"PUT", "/api/alert-rules/%d", "rule"}, {"DELETE", "/api/alert-rules/%d", "rule"},
		{"POST", "/api/alerts/%d/ack", "alert"}, {"DELETE", "/api/alerts/%d/ack", "alert"},
		{"DELETE", "/api/maintenance/%d", "maint"}, {"DELETE", "/api/keys/%d", "key"},
		{"PUT", "/api/topology/edges/%d", "edge"}, {"DELETE", "/api/topology/edges/%d", "edge"},
		{"GET", "/api/crawl/%d", "crawl"}, {"POST", "/api/crawl/%d/approve", "crawl"}, {"POST", "/api/crawl/%d/reject", "crawl"}, {"DELETE", "/api/crawl/%d", "crawl"},
	}
	for _, tc := range cases {
		id := ids[tc.kind]
		if id == 0 {
			continue
		}
		code, body := call(kb, tc.method, fmt.Sprintf(tc.path, id), map[string]any{"kind": "host", "enabled": false, "role": "viewer", "target": "x", "name": "x", "value": "x", "note": "x", "password": "Hack-12345678"})
		if tb := strings.TrimSpace(body); code == 200 && tc.method == "GET" && (tb == "{}" || tb == "[]" || tb == "null") {
			continue // boş yanıt: A'nın verisi görünmüyor
		}
		if code < 400 {
			t.Errorf("SIZINTI: %s %s → %d %s", tc.method, fmt.Sprintf(tc.path, id), code, body)
		}
	}
	// A'nın verisi bozulmadı
	var n int
	d.QueryRow(`SELECT count(*) FROM assets WHERE id = $1`, ids["asset"]).Scan(&n)
	if n != 1 {
		t.Error("A'nın asset'i silinmiş")
	}
	d.QueryRow(`SELECT count(*) FROM users WHERE id = $1 AND role = 'admin' AND enabled`, ids["user"]).Scan(&n)
	if n != 1 {
		t.Error("A'nın yöneticisi değiştirilmiş")
	}

	// B listeleri A'nın işaretçisini içermez
	for _, p := range []string{"/api/assets", "/api/probes", "/api/scenarios", "/api/alerts", "/api/alert-rules", "/api/users", "/api/keys", "/api/audit", "/api/secrets", "/api/maintenance",
		"/api/topology", "/api/sla", "/api/export", "/api/traps", "/api/crawl", "/api/status-page", "/api/overview", "/api/dashboard", "/api/siem-targets", "/api/notification-channels", "/api/audit/facets"} {
		code, body := call(kb, "GET", p, nil)
		if code >= 500 {
			t.Errorf("%s: %d", p, code)
		}
		if strings.Contains(body, "ISO-A") || strings.Contains(body, "iso-a.example") || strings.Contains(body, "gizli-deger") {
			t.Errorf("SIZINTI: B, %s içinde A verisini görüyor", p)
		}
	}
	// B'nin kendi kullanıcı listesinde yalnızca B vardır
	_, body := call(kb, "GET", "/api/users", nil)
	if strings.Contains(body, "admina") || !strings.Contains(body, "adminb") {
		t.Errorf("kullanıcı listesi: %s", body)
	}

	// --- platform yetkisi ---
	for _, tc := range []try{{"GET", "/api/tenants", ""}, {"POST", "/api/tenants", ""}, {"PUT", "/api/license", ""}, {"GET", "/api/system/update", ""}, {"POST", "/api/system/update/apply", ""}, {"DELETE", "/api/tenants/1", ""}} {
		if code, _ := call(kb, tc.method, tc.path, map[string]any{}); code != 403 {
			t.Errorf("kiracı yöneticisi platform ucuna erişebiliyor: %s %s → %d", tc.method, tc.path, code)
		}
	}
	if code, _ := call(root, "GET", "/api/tenants", nil); code != 200 {
		t.Errorf("platform yöneticisi kiracıları listeleyemedi: %d", code)
	}

	// --- kotalar ---
	var ta int64
	d.QueryRow(`SELECT id FROM tenants WHERE slug = 'kurum-a'`).Scan(&ta)
	must2 := func(code int, body string) {
		t.Helper()
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
	}
	must2(call(root, "PUT", fmt.Sprintf("/api/tenants/%d", ta), map[string]any{"probe_limit": 2, "min_interval_sec": 60}))
	if code, _ := call(ka, "POST", "/api/probes", map[string]any{"asset_name": "ISO-A-asset", "type": "tcp", "target": "127.0.0.1:11", "interval_sec": 30, "timeout_ms": 1000}); code != 400 {
		t.Errorf("en kısa aralık kotası uygulanmadı: %d", code)
	}
	if code, _ := call(ka, "POST", "/api/probes", map[string]any{"asset_name": "ISO-A-asset", "type": "tcp", "target": "127.0.0.1:11", "interval_sec": 60, "timeout_ms": 1000}); code != 402 {
		t.Errorf("probe kotası uygulanmadı: %d", code)
	}
	if code, _ := call(ka, "POST", "/api/scenarios", map[string]any{"kind": "browser", "asset_name": "x", "name": "x", "interval_sec": 60, "browser_steps": []map[string]any{{"action": "goto", "url": "https://example.com"}}}); code != 403 && code != 400 {
		t.Errorf("tarayıcı senaryosu kotası: %d", code)
	}

	// --- herkese açık durum sayfası kiracıya özel ---
	must2(call(ka, "PUT", fmt.Sprintf("/api/assets/%d/status-page", ids["asset"]), map[string]any{"public": true, "name": "ISO-A-halka-acik"}))
	if _, body := call("", "GET", "/api/public/status?t=kurum-a", nil); !strings.Contains(body, "ISO-A-halka-acik") {
		t.Errorf("A'nın durum sayfası: %s", body)
	}
	if _, body := call("", "GET", "/api/public/status", nil); strings.Contains(body, "ISO-A") {
		t.Errorf("kök durum sayfasında A görünüyor: %s", body)
	}
	if code, _ := call("", "GET", "/api/public/status?t=yok", nil); code != 404 {
		t.Errorf("olmayan durum sayfası: %d", code)
	}

	// --- durdurma ---
	must2(call(root, "PUT", fmt.Sprintf("/api/tenants/%d", ta), map[string]any{"disabled": true}))
	if code, _ := call(ka, "GET", "/api/assets", nil); code != 401 {
		t.Errorf("durdurulan kiracının oturumu çalışıyor: %d", code)
	}
	if code, _ := call("", "POST", "/api/login", map[string]string{"username": "admina", "password": "Parola-A-12345"}); code != 403 {
		t.Errorf("durdurulan kiracı giriş yapabiliyor: %d", code)
	}
	if code, _ := call(root, "PUT", "/api/tenants/1", map[string]any{"disabled": true}); code != 400 {
		t.Errorf("kök kiracı durdurulabildi: %d", code)
	}
	// --- silme: veri kalıcı gider ---
	if code, _ := call(root, "DELETE", fmt.Sprintf("/api/tenants/%d", ta), map[string]any{"confirm": "yanlış", "api_key": root}); code != 400 {
		t.Errorf("onaysız silme: %d", code)
	}
	if code, b := call(root, "DELETE", fmt.Sprintf("/api/tenants/%d", ta), map[string]any{"confirm": "Kurum A", "api_key": root}); code != 204 {
		t.Fatalf("silme: %d %s", code, b)
	}
	d.QueryRow(`SELECT (SELECT count(*) FROM assets WHERE tenant_id = $1) + (SELECT count(*) FROM probes WHERE tenant_id = $1) + (SELECT count(*) FROM users WHERE tenant_id = $1) + (SELECT count(*) FROM secrets WHERE tenant_id = $1)`, ta).Scan(&n)
	if n != 0 {
		t.Errorf("silinen kiracıdan %d kayıt kaldı", n)
	}
}
