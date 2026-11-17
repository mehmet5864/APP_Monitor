package app

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
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

func TestLicenseSignVerify(t *testing.T) {
	_, k, _ := ed25519.GenerateKey(nil)
	p := licPayload{ID: "abc", Tenant: "x", AssetLimit: 5, IssuedAt: time.Now()}
	raw, err := signLicense(k, p)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := verifyLicense(k, raw); err != nil || got.AssetLimit != 5 {
		t.Fatalf("doğrulama: %v %+v", err, got)
	}
	bad := bytes.Replace(raw, []byte(`"payload": "`), []byte(`"payload": "AA`), 1)
	if _, err := verifyLicense(k, bad); err == nil {
		t.Fatal("bozuk dosya kabul edildi")
	}
	_, k2, _ := ed25519.GenerateKey(nil)
	if _, err := verifyLicense(k2, raw); err == nil {
		t.Fatal("başka anahtarla imzalı dosya kabul edildi")
	}
}

func TestLicenseFileFlow(t *testing.T) {
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
	d.Exec(`DELETE FROM platform_secrets`)
	if err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	v, _ := vault.New(bytes.Repeat([]byte{9}, 32))
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 5}
	if _, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()
	do := func(key, method, path string, body []byte) (int, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
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
	j := func(v any) []byte { b, _ := json.Marshal(v); return b }
	root := "rootkey-0123456789"
	var ids [2]int64
	keys := [2]string{}
	for i, n := range []string{"alfa", "beta"} {
		c, b := do(root, "POST", "/api/tenants", j(map[string]any{"name": "Kurum " + n, "slug": n, "admin_username": "adm" + n, "admin_password": "Parola-" + n + "-123456"}))
		if c != 201 {
			t.Fatalf("kiracı: %d %s", c, b)
		}
		var o struct{ ID int64 }
		json.Unmarshal(b, &o)
		ids[i] = o.ID
		_, lb := do("", "POST", "/api/login", j(map[string]string{"username": "adm" + n, "password": "Parola-" + n + "-123456"}))
		var lo struct{ Key string }
		json.Unmarshal(lb, &lo)
		keys[i] = lo.Key
	}
	idPath := func(i int) string { return "/api/tenants/" + strings.TrimSpace(string(j(ids[i]))) + "/license-file" }
	// kiracı yöneticisi lisans üretemez
	if c, _ := do(keys[0], "POST", idPath(0), j(map[string]any{})); c != 403 {
		t.Errorf("kiracı lisans üretebildi: %d", c)
	}
	c, lic := do(root, "POST", idPath(0), j(map[string]any{"customer": "Alfa AŞ", "asset_limit": 7, "user_limit": 3, "min_interval_sec": 30, "allow_crawl": true}))
	if c != 200 {
		t.Fatalf("lisans üretimi: %d %s", c, lic)
	}
	// yanlış kiracıya yüklenemez
	if c, b := do(keys[1], "POST", "/api/license/file", lic); c != 400 {
		t.Errorf("başka kiracıya yüklendi: %d %s", c, b)
	}
	// değiştirilmiş dosya
	if c, _ := do(keys[0], "POST", "/api/license/file", bytes.Replace(lic, []byte(`"payload": "`), []byte(`"payload": "AA`), 1)); c != 400 {
		t.Errorf("bozuk dosya kabul edildi: %d", c)
	}
	if c, b := do(keys[0], "POST", "/api/license/file", lic); c != 200 {
		t.Fatalf("yükleme: %d %s", c, b)
	}
	var q struct {
		AssetLimit int `json:"asset_limit"`
		Quotas     Quotas
		Signed     map[string]any
	}
	_, b := do(keys[0], "GET", "/api/license", nil)
	json.Unmarshal(b, &q)
	if q.AssetLimit != 7 || q.Quotas.UserLimit != 3 || q.Quotas.MinInterval != 30 || !q.Quotas.AllowCrawl || q.Quotas.AllowSSH || q.Signed["customer"] != "Alfa AŞ" {
		t.Errorf("lisans uygulanmadı: %s", b)
	}
	// elle değiştirilemez
	if c, _ := do(root, "PUT", "/api/tenants/"+strings.TrimSpace(string(j(ids[0]))), j(map[string]any{"asset_limit": 999})); c != 409 {
		t.Errorf("lisanslı kiracının kotası elle değişti: %d", c)
	}
	if c, _ := do(root, "PUT", "/api/tenants/"+strings.TrimSpace(string(j(ids[0]))), j(map[string]any{"name": "Alfa Yeni"})); c != 200 {
		t.Errorf("ad değişikliği reddedildi: %d", c)
	}
	// aynı (eski) dosya tekrar yüklenebilir; daha eski tarihli olan değil
	c, old := do(root, "POST", idPath(0), j(map[string]any{"asset_limit": 9}))
	if c != 200 {
		t.Fatal("ikinci lisans")
	}
	if c, _ := do(keys[0], "POST", "/api/license/file", old); c != 200 {
		t.Errorf("yeni lisans yüklenemedi: %d", c)
	}
	// süresi dolmuş dosya
	pk, _ := s.signingKey(t.Context())
	past := time.Now().Add(-time.Hour)
	raw, _ := signLicense(pk, licPayload{ID: "zz", Tenant: "alfa", IssuedAt: time.Now().Add(time.Minute), ValidUntil: &past, AssetLimit: 1})
	if c, _ := do(keys[0], "POST", "/api/license/file", raw); c != 400 {
		t.Errorf("süresi dolmuş lisans kabul edildi: %d", c)
	}
	// B etkilenmedi
	_, b = do(keys[1], "GET", "/api/license", nil)
	if strings.Contains(string(b), "Alfa") || strings.Contains(string(b), `"signed"`) {
		t.Errorf("B lisansı etkilendi: %s", b)
	}
}
