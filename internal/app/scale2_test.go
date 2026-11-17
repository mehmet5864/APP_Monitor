package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func scaleServer(t *testing.T) (*Server, *httptest.Server, func(method, path string, body any) (int, []byte, http.Header)) {
	t.Helper()
	dsn := os.Getenv("APM_TEST_DSN")
	if dsn == "" {
		t.Skip("APM_TEST_DSN yok")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
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
	t.Cleanup(ts.Close)
	do := func(method, path string, body any) (int, []byte, http.Header) {
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b, resp.Header
	}
	return s, ts, do
}

func TestBulkProbesAndImport(t *testing.T) {
	s, _, do := scaleServer(t)
	for i := 0; i < 3; i++ {
		if c, b, _ := do("POST", "/api/probes", map[string]any{"asset_name": fmt.Sprintf("toplu-%d", i), "type": "tcp", "target": "127.0.0.1:9", "interval_sec": 30}); c != 201 {
			t.Fatalf("probe %d: %d %s", i, c, b)
		}
	}
	var ids []int64
	rows, _ := s.DB.Query(`SELECT id FROM probes ORDER BY id`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if c, b, _ := do("POST", "/api/probes/bulk", map[string]any{"ids": ids[:2], "action": "disable"}); c != 200 {
		t.Fatalf("disable: %d %s", c, b)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM probes WHERE NOT enabled`).Scan(&n)
	if n != 2 {
		t.Errorf("2 probe pasif olmalıydı: %d", n)
	}
	if c, b, _ := do("POST", "/api/probes/bulk", map[string]any{"ids": ids, "action": "set", "interval_sec": 77, "timeout_ms": 1234}); c != 200 {
		t.Fatalf("set: %d %s", c, b)
	}
	s.DB.QueryRow(`SELECT count(*) FROM probes WHERE interval_sec = 77 AND timeout_ms = 1234`).Scan(&n)
	if n != 3 {
		t.Errorf("3 probe güncellenmeliydi: %d", n)
	}
	if c, _, _ := do("POST", "/api/probes/bulk", map[string]any{"ids": ids, "action": "set", "interval_sec": 1}); c != 400 {
		t.Errorf("çok kısa aralık kabul edildi: %d", c)
	}
	if c, _, _ := do("POST", "/api/probes/bulk", map[string]any{"ids": ids, "action": "boom"}); c != 400 {
		t.Errorf("geçersiz işlem kabul edildi: %d", c)
	}
	if c, _, _ := do("POST", "/api/probes/bulk", map[string]any{"ids": ids[:1], "action": "delete"}); c != 200 {
		t.Fatal("delete")
	}
	s.DB.QueryRow(`SELECT count(*) FROM probes`).Scan(&n)
	if n != 2 {
		t.Errorf("silme sonrası 2 probe kalmalı: %d", n)
	}

	// CSV/JSON içe aktarma: biri geçersiz üç satır
	imp := map[string]any{"rows": []map[string]any{
		{"asset": "ice-1", "type": "tcp", "target": "127.0.0.1:22", "interval_sec": 60},
		{"asset": "ice-2", "type": "http", "target": "http://127.0.0.1:1/health"},
		{"asset": "ice-3", "type": "foo", "target": "x"},
	}}
	imp["dry_run"] = true
	c, b, _ := do("POST", "/api/probes/import", imp)
	var out struct{ Created, Failed int }
	json.Unmarshal(b, &out)
	if c != 200 || out.Created != 2 || out.Failed != 1 {
		t.Fatalf("dry-run: %d %s", c, b)
	}
	s.DB.QueryRow(`SELECT count(*) FROM probes`).Scan(&n)
	if n != 2 {
		t.Error("dry-run kayıt oluşturdu")
	}
	imp["dry_run"] = false
	c, b, _ = do("POST", "/api/probes/import", imp)
	json.Unmarshal(b, &out)
	if c != 200 || out.Created != 2 || out.Failed != 1 {
		t.Fatalf("import: %d %s", c, b)
	}
	s.DB.QueryRow(`SELECT count(*) FROM probes`).Scan(&n)
	if n != 4 {
		t.Errorf("içe aktarma sonrası 4 probe olmalı: %d", n)
	}
}

func TestIngestRateLimit(t *testing.T) {
	_, _, do := scaleServer(t)
	got429 := false
	for i := 0; i < 30; i++ {
		c, _, h := do("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": "hizli", "kind": "host"}, "metrics": []map[string]any{{"name": "cpu", "value": 1}}})
		if c == 429 {
			got429 = true
			if h.Get("Retry-After") == "" {
				t.Error("429 yanıtında Retry-After yok")
			}
			break
		}
	}
	if !got429 {
		t.Error("30 hızlı istekte oran sınırı devreye girmedi")
	}
	// başka asset etkilenmez
	if c, _, _ := do("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": "yavas", "kind": "host"}, "metrics": []map[string]any{{"name": "cpu", "value": 1}}}); c >= 300 {
		t.Errorf("diğer asset engellendi: %d", c)
	}
}

func TestRolloutWaves(t *testing.T) {
	s, _, do := scaleServer(t)
	dir := t.TempDir()
	bin := []byte("yeni-ajan-ikilisi")
	os.WriteFile(filepath.Join(dir, "appmon-agent-linux-amd64"), bin, 0o755)
	s.AgentDir = dir
	sum := sha256.Sum256(bin)
	newSHA := hex.EncodeToString(sum[:])
	s.IngestRate = -1
	beat := func(name, sha, errMsg string) {
		l := map[string]string{"os": "linux", "arch": "amd64", "agent_sha": sha, "agent_version": "1.4.0"}
		if errMsg != "" {
			l["update_error"] = errMsg
		}
		if c, b, _ := do("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": name, "kind": "host", "labels": l}, "metrics": []map[string]any{{"name": "agent.up", "value": 1}}}); c >= 300 {
			t.Fatalf("ingest %s: %d %s", name, c, b)
		}
	}
	names := []string{"r1", "r2", "r3", "r4", "r5"}
	for _, n := range names {
		beat(n, "eski", "")
	}
	c, b, _ := do("POST", "/api/agent/rollout", map[string]any{"all": true, "batch": 2})
	var ro rolloutRow
	json.Unmarshal(b, &ro)
	if c != 201 || ro.Total != 5 || ro.InWave != 2 || ro.Queued != 3 {
		t.Fatalf("başlangıç: %d %s", c, b)
	}
	if c, _, _ := do("POST", "/api/agent/rollout", map[string]any{"all": true}); c != 409 && c != 400 {
		t.Errorf("ikinci kademeli güncelleme engellenmedi: %d", c)
	}
	count := func(q string) (n int) { s.DB.QueryRow(q).Scan(&n); return }
	if n := count(`SELECT count(*) FROM assets WHERE agent_update_req`); n != 2 {
		t.Fatalf("ilk dalgada 2 ajan istenmeli: %d", n)
	}
	// dalga henüz bitmedi: ilerlemez
	s.rolloutJob(t.Context())
	if n := count(`SELECT count(*) FROM assets WHERE agent_update_req`); n != 2 {
		t.Errorf("dalga bitmeden ilerledi: %d", n)
	}
	// ilk dalgadakiler güncellendi
	var wave1 []string
	rows, _ := s.DB.Query(`SELECT name FROM assets WHERE agent_update_req ORDER BY name`)
	for rows.Next() {
		var n string
		rows.Scan(&n)
		wave1 = append(wave1, n)
	}
	rows.Close()
	for _, n := range wave1 {
		beat(n, newSHA, "")
	}
	s.rolloutJob(t.Context())
	var d, f, q, w int
	s.DB.QueryRow(`SELECT done, failed, cardinality(queue), cardinality(wave) FROM agent_rollouts`).Scan(&d, &f, &q, &w)
	if d != 2 || f != 0 || q == 3 || w != 2 {
		t.Fatalf("2. dalga: done=%d failed=%d queue=%d wave=%d", d, f, q, w)
	}
	// 2. dalgada bir ajan hata bildirir -> duraklar
	var wave2 []string
	rows, _ = s.DB.Query(`SELECT name FROM assets WHERE agent_update_req AND labels->>'agent_sha' = 'eski' ORDER BY name`)
	for rows.Next() {
		var n string
		rows.Scan(&n)
		wave2 = append(wave2, n)
	}
	rows.Close()
	if len(wave2) != 2 {
		t.Fatalf("2. dalga ajanları: %v", wave2)
	}
	beat(wave2[0], "eski", "indirme hatası")
	beat(wave2[1], newSHA, "")
	s.rolloutJob(t.Context())
	var state string
	s.DB.QueryRow(`SELECT state, failed FROM agent_rollouts`).Scan(&state, &f)
	if state != "paused" || f != 1 {
		t.Fatalf("hata sonrası duraklamalı: %s failed=%d", state, f)
	}
	// devam: kalan 1 ajan için son dalga, sonra tamamlanır
	if c, b, _ := do("POST", "/api/agent/rollout/resume", nil); c != 200 {
		t.Fatalf("resume: %d %s", c, b)
	}
	var last string
	s.DB.QueryRow(`SELECT name FROM assets WHERE agent_update_req AND labels->>'agent_sha' = 'eski' AND name NOT IN ($1)`, wave2[0]).Scan(&last)
	if last == "" {
		t.Fatal("son dalga başlamadı")
	}
	beat(last, newSHA, "")
	s.rolloutJob(t.Context())
	s.DB.QueryRow(`SELECT state FROM agent_rollouts`).Scan(&state)
	if state != "done" {
		t.Errorf("tamamlanmalıydı: %s", state)
	}
	c, b, _ = do("GET", "/api/agent/rollout", nil)
	json.Unmarshal(b, &ro)
	if c != 200 || ro.State != "done" || ro.Done != 4 || ro.Failed != 1 {
		t.Errorf("sonuç: %d %s", c, b)
	}
	// iptal akışı
	beat("r6", "eski", "")
	beat("r7", "eski", "")
	if c, b, _ := do("POST", "/api/agent/rollout", map[string]any{"asset_ids": []int64{}, "all": true, "batch": 1}); c != 201 {
		t.Fatalf("2. rollout: %d %s", c, b)
	}
	if c, _, _ := do("POST", "/api/agent/rollout/cancel", nil); c != 200 {
		t.Fatal("cancel")
	}
	if n := count(`SELECT count(*) FROM assets WHERE agent_update_req`); n != 0 {
		t.Errorf("iptalden sonra istekler geri alınmalı: %d", n)
	}
}

func TestBulkScenarios(t *testing.T) {
	s, _, do := scaleServer(t)
	for i := 0; i < 2; i++ {
		c, b, _ := do("POST", "/api/scenarios", map[string]any{"asset_name": fmt.Sprintf("sc-%d", i), "name": fmt.Sprintf("akış-%d", i), "interval_sec": 120, "timeout_ms": 5000,
			"steps": []map[string]any{{"name": "ana", "method": "GET", "url": "http://127.0.0.1:1/"}}})
		if c != 201 {
			t.Fatalf("senaryo %d: %d %s", i, c, b)
		}
	}
	var ids []int64
	rows, _ := s.DB.Query(`SELECT id FROM scenarios ORDER BY id`)
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if c, b, _ := do("POST", "/api/scenarios/bulk", map[string]any{"ids": ids, "action": "set", "interval_sec": 90}); c != 200 {
		t.Fatalf("set: %d %s", c, b)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM scenarios WHERE interval_sec = 90`).Scan(&n)
	if n != 2 {
		t.Errorf("aralık güncellenmedi: %d", n)
	}
	if c, _, _ := do("POST", "/api/scenarios/bulk", map[string]any{"ids": ids, "action": "set", "interval_sec": 10}); c != 400 {
		t.Errorf("30 sn altı kabul edildi: %d", c)
	}
	do("POST", "/api/scenarios/bulk", map[string]any{"ids": ids[:1], "action": "disable"})
	s.DB.QueryRow(`SELECT count(*) FROM scenarios WHERE NOT enabled`).Scan(&n)
	if n != 1 {
		t.Errorf("pasife alma: %d", n)
	}
	do("POST", "/api/scenarios/bulk", map[string]any{"ids": ids, "action": "delete"})
	s.DB.QueryRow(`SELECT count(*) FROM scenarios`).Scan(&n)
	if n != 0 {
		t.Errorf("silme: %d", n)
	}
}
