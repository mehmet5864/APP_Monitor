package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"appmon/internal/db"
	"appmon/internal/vault"
	"appmon/web"
)

func TestEvalEveryAndRuleDue(t *testing.T) {
	if evalEvery(30) != 15*time.Second || evalEvery(120) != 30*time.Second || evalEvery(3600) != 60*time.Second {
		t.Errorf("evalEvery: %v %v %v", evalEvery(30), evalEvery(120), evalEvery(3600))
	}
	s := &Server{}
	if !s.ruleDue(7, 120) {
		t.Fatal("ilk değerlendirme atlandı")
	}
	if s.ruleDue(7, 120) {
		t.Error("30 sn dolmadan aynı kural yeniden değerlendirildi")
	}
	if !s.ruleDue(8, 120) {
		t.Error("başka kural etkilendi")
	}
}

func TestClampAndSems(t *testing.T) {
	if clampInt(0, 50, 1, 100) != 50 || clampInt(500, 50, 1, 100) != 100 || clampInt(-3, 5, 1, 9) != 5 {
		t.Error("clampInt")
	}
	s := &Server{ProbeConc: 3, ScenarioConc: 2, IngestConc: 1}
	if cap(s.sems().probe) != 3 || cap(s.sems().scen) != 2 || cap(s.sems().ingest) != 1 {
		t.Error("yuva sayıları")
	}
	SetBrowserConcurrency(4)
	if cap(browserSem) != 4 {
		t.Error("tarayıcı yuvası")
	}
	SetBrowserConcurrency(2)
}

func TestIngestBusyAndNotifyGroup(t *testing.T) {
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
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 3, IngestConc: 1, NotifyGroup: 300 * time.Millisecond}
	if _, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()

	// 1) Tek yuva dolu: ingest 503 + Retry-After döner; yuva boşalınca normal çalışır.
	rel, ok := s.acquireIngest(context.Background())
	if !ok {
		t.Fatal("yuva alınamadı")
	}
	body := `{"asset":{"name":"yuk-1","kind":"host"},"metrics":[{"name":"cpu","value":1}]}`
	post := func() *http.Response {
		req, _ := http.NewRequest("POST", ts.URL+"/api/ingest", strings.NewReader(body))
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post()
	resp.Body.Close()
	if resp.StatusCode != 503 || resp.Header.Get("Retry-After") == "" {
		t.Errorf("yoğunken beklenen 503+Retry-After, gelen %d %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	rel()
	resp = post()
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Errorf("yuva boşalınca ingest başarısız: %d %s", resp.StatusCode, b)
	}

	// 2) Bildirim gruplama: aynı kuraldan 5 alarm tek mesaj; tek alarm eski biçimde.
	var mu sync.Mutex
	var got []map[string]any
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		got = append(got, m)
		mu.Unlock()
	}))
	defer hook.Close()
	cfg, _ := json.Marshal(map[string]any{"url": hook.URL})
	if _, err := d.Exec(`INSERT INTO notification_channels (tenant_id, name, kind, config, min_severity) VALUES (1,'t','webhook',$1,'info')`, string(cfg)); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a1", "a2", "a3", "a4", "a5"} {
		s.queueNotify(1, "open", "CPU yüksek", "critical", n, 91, 80, ">")
	}
	s.queueNotify(1, "open", "Tek kural", "warning", "solo", 5, 3, ">")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("2 bildirim bekleniyordu, %d geldi: %v", len(got), got)
	}
	var grouped, single map[string]any
	for _, m := range got {
		if strings.Contains(m["subject"].(string), "5 varlıkta") {
			grouped = m
		} else {
			single = m
		}
	}
	if grouped == nil || !strings.Contains(grouped["text"].(string), "a1, a2, a3, a4, a5") {
		t.Errorf("gruplu bildirim hatalı: %v", grouped)
	}
	if single == nil || single["subject"] != "Tek kural — solo" {
		t.Errorf("tekil bildirim hatalı: %v", single)
	}

	// 3) Kapasite uç noktası
	req, _ := http.NewRequest("GET", ts.URL+"/api/scenarios/capacity", nil)
	req.Header.Set("X-API-Key", "rootkey-0123456789")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "browser_concurrency") {
		t.Errorf("kapasite: %d %s", resp.StatusCode, b)
	}
}
