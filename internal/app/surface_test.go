package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"appmon/internal/db"
	"appmon/internal/probe"
	"appmon/internal/vault"
	"appmon/web"
)

func TestSurfaceSeverity(t *testing.T) {
	cases := []struct{ kind, item, change, want string }{
		{"port", "3389", "added", "critical"},
		{"port", "8080", "added", "warning"},
		{"port", "8080", "removed", "info"},
		{"endpoint", "GET /admin/login", "added", "critical"},
		{"endpoint", "GET /api/urun/{id}", "added", "warning"},
		{"tls", "TLS 1.0", "added", "critical"},
		{"tls", "TLS 1.2", "added", "warning"},
		{"header", "strict-transport-security", "removed", "warning"},
		{"banner", "nginx/1.25", "added", "info"},
	}
	for _, c := range cases {
		if got := surfaceSeverity(c.kind, c.item, c.change); got != c.want {
			t.Errorf("%s %s %s: %s != %s", c.kind, c.item, c.change, got, c.want)
		}
	}
}

func TestBurstDetected(t *testing.T) {
	calm := []int64{1, 0, 2, 1, 0, 1, 2, 1, 0, 1, 1, 2}
	if !BurstDetected(300, 400, calm) {
		t.Fatal("belirgin 4xx patlaması yakalanmalı")
	}
	if BurstDetected(30, 40, calm) {
		t.Fatal("eşik altı patlama sayılmamalı")
	}
	if BurstDetected(300, 2000, calm) {
		t.Fatal("4xx oranı düşükse (normal trafik) patlama sayılmamalı")
	}
	busy := []int64{100, 120, 90, 110, 100, 130, 95, 105, 100, 115, 100, 100}
	if BurstDetected(300, 400, busy) {
		t.Fatal("zaten yüksek 4xx tabanında patlama sayılmamalı")
	}
}

func TestParseConnsListen(t *testing.T) {
	listen := []string{"State  Recv-Q Send-Q Local Address:Port Peer Address:Port",
		"LISTEN 0 128 0.0.0.0:22 0.0.0.0:*", "LISTEN 0 128 127.0.0.1:5432 0.0.0.0:*", "LISTEN 0 128 [::]:443 [::]:*", "LISTEN 0 5 10.0.0.5:8080 0.0.0.0:*"}
	snap := probe.ParseConns(nil, listen, nil)
	if !snap.ListenOK || len(snap.Listen) != 3 || snap.Listen[0] != 22 || snap.Listen[1] != 443 || snap.Listen[2] != 8080 {
		t.Fatalf("dinleyen portlar: %+v", snap)
	}
	if s2 := probe.ParseConns(nil, nil, nil); s2.ListenOK {
		t.Fatal("ss çıktısı yoksa ListenOK false olmalı")
	}
}

func TestSurfaceDrift(t *testing.T) {
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
	tid, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000)
	if err != nil {
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
	var asset int64
	d.QueryRow(`INSERT INTO assets (tenant_id,name,kind,language,collection) VALUES ($1,'web1','service','none','agentless') RETURNING id`, tid).Scan(&asset)
	ctx := t.Context()
	ports := func(p ...int) *probe.ConnSnapshot { return &probe.ConnSnapshot{Listen: p, ListenOK: true} }

	// İlk gözlem referans olur: olay yok.
	s.surfaceFromConns(ctx, tid, asset, ports(22, 443))
	_, m := call("GET", "/api/surface/events", nil)
	if len(m["events"].([]any)) != 0 {
		t.Fatalf("ilk gözlemde olay olmamalı: %v", m)
	}
	// Yeni riskli port + yeni normal port.
	s.surfaceFromConns(ctx, tid, asset, ports(22, 443, 3389, 8080))
	_, m = call("GET", "/api/surface/events", nil)
	evs := m["events"].([]any)
	if len(evs) != 2 || m["critical"].(float64) != 1 {
		t.Fatalf("2 olay (1 kritik) beklenirdi: %v", m)
	}
	first := evs[0].(map[string]any)
	if first["item"] != "3389" || first["severity"] != "critical" || first["kind"] != "port" {
		t.Fatalf("kritik olay önce gelmeli: %v", first)
	}
	// Aynı durum tekrar gözlenince yinelenen olay oluşmaz.
	s.surfaceFromConns(ctx, tid, asset, ports(22, 443, 3389, 8080))
	_, m = call("GET", "/api/surface/events", nil)
	if len(m["events"].([]any)) != 2 {
		t.Fatalf("yinelenen olay: %v", m)
	}
	// Kapanan port 3 gözlemden sonra "kaldırıldı" olur.
	for i := 0; i < 3; i++ {
		s.surfaceFromConns(ctx, tid, asset, ports(22, 443, 3389))
	}
	_, m = call("GET", "/api/surface/events?state=all", nil)
	found := false
	for _, e := range m["events"].([]any) {
		x := e.(map[string]any)
		if x["item"] == "8080" && x["change"] == "removed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("kaldırılan port olayı yok: %v", m)
	}
	// ss çıktısı yoksa (ListenOK=false) hiçbir şey kaldırılmaz.
	s.surfaceFromConns(ctx, tid, asset, &probe.ConnSnapshot{})
	// surface.drift ölçümü: 3389 (critical) + 8080 added (warning) açık => 2 (kaldırıldı olayı info)
	s.surfaceMetrics(ctx)
	var drift float64
	d.QueryRow(`SELECT value FROM metrics WHERE asset_id = $1 AND name = 'surface.drift' ORDER BY time DESC LIMIT 1`, asset).Scan(&drift)
	if drift != 2 {
		t.Fatalf("surface.drift = %v, 2 beklenirdi", drift)
	}
	// Gözden geçirme.
	id := int(first["id"].(float64))
	if c, _ := call("POST", "/api/surface/events/"+itoa(id)+"/review", map[string]string{"note": "kabul"}); c != 204 {
		t.Fatalf("review: %d", c)
	}
	if c, _ := call("POST", "/api/surface/events/"+itoa(id)+"/review", nil); c != 404 {
		t.Fatalf("ikinci review 404 olmalı: %d", c)
	}
	// TLS / başlık / banner.
	s.surfaceFromHTTP(ctx, tid, asset, &probe.HTTPSurface{TLS: "TLS 1.3", Headers: []string{"strict-transport-security", "x-frame-options"}, Banner: "nginx"})
	for i := 0; i < 2; i++ {
		s.surfaceFromHTTP(ctx, tid, asset, &probe.HTTPSurface{TLS: "TLS 1.0", Headers: []string{"x-frame-options"}, Banner: "nginx"})
	}
	_, m = call("GET", "/api/surface/events", nil)
	var tlsCrit, hdrRemoved bool
	for _, e := range m["events"].([]any) {
		x := e.(map[string]any)
		if x["kind"] == "tls" && x["item"] == "TLS 1.0" && x["severity"] == "critical" {
			tlsCrit = true
		}
		if x["kind"] == "header" && x["item"] == "strict-transport-security" && x["change"] == "removed" {
			hdrRemoved = true
		}
	}
	if !tlsCrit || !hdrRemoved {
		t.Fatalf("TLS düşürme/başlık kaybı yakalanmadı: %v", m)
	}
	// Uç noktalar: öğrenme süresi içinde olay yok; süre dolunca yeni uç nokta olay üretir.
	s.surfaceFromAccess(ctx, tid, asset, []string{"GET /", "GET /api/urun/{id}"})
	s.surfaceFromAccess(ctx, tid, asset, []string{"GET /", "GET /admin/panel"})
	_, m = call("GET", "/api/surface/events", nil)
	for _, e := range m["events"].([]any) {
		if e.(map[string]any)["kind"] == "endpoint" {
			t.Fatalf("öğrenme süresinde uç nokta olayı olmamalı: %v", e)
		}
	}
	d.Exec(`UPDATE surface_state SET started_at = now() - interval '2 days' WHERE asset_id = $1 AND kind = 'endpoint'`, asset)
	s.surfaceFromAccess(ctx, tid, asset, []string{"GET /", "GET /phpmyadmin/index"})
	_, m = call("GET", "/api/surface/events", nil)
	epFound := false
	for _, e := range m["events"].([]any) {
		x := e.(map[string]any)
		if x["kind"] == "endpoint" && strings.Contains(x["item"].(string), "phpmyadmin") && x["severity"] == "critical" {
			epFound = true
		}
	}
	if !epFound {
		t.Fatalf("yeni riskli uç nokta yakalanmadı: %v", m)
	}
	// Referans sıfırlama.
	if c, _ := call("POST", "/api/surface/reset", map[string]any{"asset_id": asset}); c != 204 {
		t.Fatalf("reset: %d", c)
	}
	_, m = call("GET", "/api/surface/events", nil)
	if len(m["events"].([]any)) != 0 {
		t.Fatalf("sıfırlamadan sonra açık olay kalmamalı: %v", m)
	}
	s.surfaceFromConns(ctx, tid, asset, ports(22, 9999)) // yeniden öğrenme: olay yok
	_, m = call("GET", "/api/surface/events", nil)
	if len(m["events"].([]any)) != 0 {
		t.Fatalf("yeniden öğrenmede olay olmamalı: %v", m)
	}
	// Kiracı yalıtımı: ikinci kiracı bu olayları göremez.
	if c, m2 := call("POST", "/api/tenants", map[string]any{"name": "Kurum B", "slug": "k-b", "admin_key": "tenantb-0123456789"}); c/100 != 2 {
		t.Logf("kiracı oluşturulamadı (%d %v), yalıtım testi atlandı", c, m2)
	} else {
		req, _ := http.NewRequest("GET", ts.URL+"/api/surface/events?state=all", nil)
		req.Header.Set("X-API-Key", "tenantb-0123456789")
		resp, _ := http.DefaultClient.Do(req)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		var mb map[string]any
		json.Unmarshal(b, &mb)
		if evs, ok := mb["events"].([]any); ok && len(evs) != 0 {
			t.Fatalf("kiracı yalıtımı bozuk: %v", mb)
		}
	}
	_ = time.Now
}
