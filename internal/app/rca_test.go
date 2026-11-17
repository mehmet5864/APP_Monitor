package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"appmon/internal/db"
	"appmon/internal/vault"
	"appmon/web"
)

func rcaSeries(at time.Time, f func(min int) float64) []RPt {
	var out []RPt
	for m := -60; m <= 3; m++ {
		out = append(out, RPt{at.Add(time.Duration(m) * time.Minute), f(m)})
	}
	return out
}

func TestDetectShift(t *testing.T) {
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	// 8 dk önce 1 ms -> 40 ms'ye sıçrayan RTT: değişim bulunmalı, başlangıç ~ -8 dk.
	pts := rcaSeries(at, func(m int) float64 {
		if m >= -8 {
			return 40
		}
		return 1 + float64(m%2)*0.1
	})
	sh, ok := DetectShift("host.conn_rtt_ms", pts, at)
	if !ok || sh.After < 39 || sh.Before > 2 {
		t.Fatalf("sıçrama bulunamadı: %+v %v", sh, ok)
	}
	if d := at.Sub(sh.Onset); d < 7*time.Minute || d > 9*time.Minute {
		t.Fatalf("başlangıç yanlış: %v", d)
	}
	if sc := RCAScore(sh, "latency", at, 1); sc < 8 {
		t.Fatalf("puan düşük: %v", sc)
	}
	// Alarmdan sonra başlayan değişim düşük puan almalı.
	late := rcaSeries(at, func(m int) float64 {
		if m >= 2 {
			return 90
		}
		return 10
	})
	if sh2, ok := DetectShift("host.cpu_pct", late, at); ok {
		if sc := RCAScore(sh2, "load", at, 0); sc > 4 {
			t.Fatalf("alarm sonrası değişim yüksek puanlı: %v", sc)
		}
	}
	// Sabit seri ve küçük dalgalanma değişim sayılmamalı.
	if _, ok := DetectShift("host.cpu_pct", rcaSeries(at, func(m int) float64 { return 30 + float64(m%3) }), at); ok {
		t.Fatal("gürültü değişim sayıldı")
	}
	// Yetersiz temel veri.
	if _, ok := DetectShift("x_ms", []RPt{{at, 5}}, at); ok {
		t.Fatal("tek noktadan değişim çıkmamalı")
	}
	// Bayrak (asset.down) çevrilmesi.
	down := rcaSeries(at, func(m int) float64 {
		if m >= -5 {
			return 1
		}
		return 0
	})
	sh3, ok := DetectShift("asset.down", down, at)
	if !ok || RCAScore(sh3, "down", at, 1) < 8 {
		t.Fatalf("down bayrağı yakalanmadı: %+v", sh3)
	}
	if !rcaSkip("db.xact_commit") || rcaSkip("db.slow_queries") {
		t.Fatal("rcaSkip yanlış")
	}
}

func TestAlertRCA(t *testing.T) {
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
	get := func(path string) (int, map[string]any) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
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
	mk := func(name string) int64 {
		var id int64
		if err := d.QueryRow(`INSERT INTO assets (tenant_id,name,kind,language,collection) VALUES ($1,$2,'service','none','agentless') RETURNING id`, tid, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	web1, db1, other := mk("web1"), mk("db1"), mk("diger")
	d.Exec(`INSERT INTO topology_edges (tenant_id, source_asset_id, target_asset_id) VALUES ($1,$2,$3)`, tid, web1, db1)
	at := time.Now().Add(-10 * time.Minute).Truncate(time.Minute)
	put := func(asset int64, name string, f func(m int) float64) {
		for m := -60; m <= 3; m++ {
			d.Exec(`INSERT INTO metrics (time, tenant_id, asset_id, name, value) VALUES ($1,$2,$3,$4,$5)`, at.Add(time.Duration(m)*time.Minute), tid, asset, name, f(m))
		}
	}
	put(db1, "probe.latency_ms", func(m int) float64 {
		if m >= -6 {
			return 900
		}
		return 20
	})
	put(web1, "probe.latency_ms", func(m int) float64 {
		if m >= -1 {
			return 2500
		}
		return 80
	})
	put(other, "host.cpu_pct", func(m int) float64 {
		if m >= -5 {
			return 95
		}
		return 10
	}) // topolojide ilgisiz asset: aday olmamalı
	var rule, alert int64
	d.QueryRow(`INSERT INTO alert_rules (tenant_id,name,metric,comparator,threshold,window_sec,severity) VALUES ($1,'yavas','probe.latency_ms','>',1000,60,'critical') RETURNING id`, tid).Scan(&rule)
	if err := d.QueryRow(`INSERT INTO alerts (tenant_id,rule_id,asset_id,state,value,opened_at) VALUES ($1,$2,$3,'open',2500,$4) RETURNING id`, tid, rule, web1, at).Scan(&alert); err != nil {
		t.Fatal(err)
	}
	c, m := get("/api/alerts/" + itoa(int(alert)) + "/rca")
	if c != 200 {
		t.Fatalf("rca: %d %v", c, m)
	}
	causes, _ := m["causes"].([]any)
	if len(causes) == 0 {
		t.Fatalf("neden yok: %v", m)
	}
	top := causes[0].(map[string]any)
	if top["asset"] != "db1" || top["metric"] != "probe.latency_ms" || top["depth"].(float64) != 1 {
		t.Fatalf("en olası neden db1 olmalı: %v", top)
	}
	for _, x := range causes {
		if x.(map[string]any)["asset"] == "diger" {
			t.Fatal("topolojide olmayan asset aday olmamalı")
		}
		if x.(map[string]any)["asset"] == "web1" && x.(map[string]any)["metric"] == "probe.latency_ms" {
			t.Fatal("alarmın kendi ölçümü neden sayılmamalı")
		}
	}
	if c, _ := get("/api/alerts/99999/rca"); c != 404 {
		t.Fatalf("olmayan alarm: %d", c)
	}
}
