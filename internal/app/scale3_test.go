package app

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPartitionsAndLatest(t *testing.T) {
	s, _, do := scaleServer(t)
	s.IngestRate = -1
	var kind string
	s.DB.QueryRow(`SELECT relkind::text FROM pg_class WHERE oid = 'metrics'::regclass`).Scan(&kind)
	if kind != "p" {
		t.Fatalf("metrics bölümlü olmalı, relkind=%s", kind)
	}
	var parts int
	s.DB.QueryRow(`SELECT count(*) FROM pg_inherits WHERE inhparent = 'metrics'::regclass`).Scan(&parts)
	if parts < 8 {
		t.Errorf("günlük bölümler eksik: %d", parts)
	}
	// aynı metrik için iki ölçüm: asset_latest en yenisini tutar, eski zamanlı ölçüm üzerine yazmaz
	now := time.Now()
	send := func(v float64, at time.Time) {
		if c, b, _ := do("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": "son-1", "kind": "host"},
			"metrics": []map[string]any{{"name": "host.cpu_pct", "value": v, "time": at}, {"name": "host.mem_pct", "value": 5, "time": at}}}); c >= 300 {
			t.Fatalf("ingest: %d %s", c, b)
		}
	}
	send(10, now.Add(-30*time.Second))
	send(20, now)
	send(99, now.Add(-2*time.Minute)) // geç gelen eski ölçüm
	var v float64
	if err := s.DB.QueryRow(`SELECT value FROM asset_latest l JOIN assets a ON a.id = l.asset_id WHERE a.name = 'son-1' AND l.name = 'host.cpu_pct'`).Scan(&v); err != nil || v != 20 {
		t.Errorf("asset_latest son değer 20 olmalı: %v %v", v, err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM metrics WHERE name = 'host.cpu_pct'`).Scan(&n)
	if n != 3 {
		t.Errorf("3 ham ölçüm olmalı: %d", n)
	}
	// /api/assets son değerleri asset_latest'ten okur
	_, b, _ := do("GET", "/api/assets", nil)
	var as []struct {
		Name   string
		Latest map[string]float64
	}
	json.Unmarshal(b, &as)
	found := false
	for _, a := range as {
		if a.Name == "son-1" {
			found = true
			if a.Latest["host.cpu_pct"] != 20 {
				t.Errorf("assets latest: %v", a.Latest)
			}
		}
	}
	if !found {
		t.Error("asset listede yok")
	}
	// bölüm bakımı: mevcut bölümleri silmez, eskilerini siler
	s.RetentionDays = 14
	s.dropOldPartitions(t.Context())
	s.DB.QueryRow(`SELECT count(*) FROM metrics WHERE name = 'host.cpu_pct'`).Scan(&n)
	if n != 3 {
		t.Errorf("bölüm temizliği güncel veriyi sildi: %d", n)
	}
	s.DB.QueryRow(`SELECT appmon_metrics_ensure(7)`).Scan(&n)
	if n != 0 {
		t.Errorf("ensure ikinci çağrıda bölüm oluşturmamalı: %d", n)
	}
	var dropped int
	s.DB.QueryRow(`SELECT appmon_metrics_drop_before(now() + interval '30 days')`).Scan(&dropped)
	if dropped < 8 {
		t.Errorf("uzak tarih öncesi tüm bölümler silinmeliydi: %d", dropped)
	}
	s.DB.QueryRow(`SELECT appmon_metrics_ensure(7)`).Scan(&n)
	if n < 8 {
		t.Errorf("bölümler yeniden oluşturulmalı: %d", n)
	}
}

func TestOfflineWarmupAndSummary(t *testing.T) {
	s, _, do := scaleServer(t)
	s.IngestRate = -1
	if c, b, _ := do("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": "sessiz", "kind": "host", "labels": map[string]string{"os": "linux", "arch": "amd64", "agent_version": "1.5.0", "agent_sha": "x", "cfg_send": "A"}},
		"metrics": []map[string]any{{"name": "agent.up", "value": 1}}}); c >= 300 {
		t.Fatalf("ingest: %d %s", c, b)
	}
	s.DB.Exec(`UPDATE assets SET last_seen = now() - interval '10 minutes', status = 'up' WHERE name = 'sessiz'`)
	status := func() (st string) { s.DB.QueryRow(`SELECT status FROM assets WHERE name = 'sessiz'`).Scan(&st); return }
	// ısınma sürerken çevrimdışı işaretlenmez
	s.startedAt = time.Now()
	s.OfflineWarmupSec = 120
	s.markOffline(t.Context())
	if st := status(); st != "up" {
		t.Errorf("ısınma sırasında durum değişti: %s", st)
	}
	if s.warmupLeft() <= 0 {
		t.Error("warmupLeft pozitif olmalı")
	}
	// ısınma bitti: eşik aşıldığı için çevrimdışı olur
	s.startedAt = time.Now().Add(-5 * time.Minute)
	s.markOffline(t.Context())
	if st := status(); st != "offline" {
		t.Errorf("çevrimdışı olmalıydı: %s", st)
	}
	// eşik büyütülünce (1 saat) 10 dakikadır sessiz ajan çevrimdışı sayılmaz
	s.DB.Exec(`UPDATE assets SET status = 'up' WHERE name = 'sessiz'`)
	s.OfflineAgentSec = 3600
	s.markOffline(t.Context())
	if st := status(); st != "up" {
		t.Errorf("eşik 1 saatken çevrimdışı oldu: %s", st)
	}
	c, b, _ := do("GET", "/api/agent/summary", nil)
	var sum struct {
		Total    int
		Versions []struct {
			Version string
			Count   int
		}
		Update map[string]int
		Config map[string]int
		After  int `json:"offline_after_sec"`
	}
	json.Unmarshal(b, &sum)
	if c != 200 || sum.Total != 1 || len(sum.Versions) != 1 || sum.Versions[0].Version != "1.5.0" || sum.Config["local"] != 1 || sum.After != 3600 {
		t.Errorf("özet: %d %s", c, b)
	}
}
