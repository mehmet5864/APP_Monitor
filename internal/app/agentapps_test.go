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

	"appmon/internal/db"
	"appmon/internal/vault"
	"appmon/web"
)

func TestAgentAppsSanitize(t *testing.T) {
	in := AgentApps{Apps: []AgentApp{
		{Name: "  nginx\x00 ", Kind: "weird", CPU: 12.5, MemMB: 80, Ports: []int{80, 0, 70000, 443}},
		{Name: "", Kind: "process"},
		{Name: strings.Repeat("a", 300), Kind: "service", State: "running", CPU: -5, MemMB: 1e12, Count: -3},
	}, Logs: []AgentLog{{App: "auto_nginx", Path: "/var/log/x\n", Sample: strings.Repeat("é", 500)}, {App: ""}}}
	a, l := in.sanitize()
	var apps []AgentApp
	var logs []AgentLog
	json.Unmarshal([]byte(a), &apps)
	json.Unmarshal([]byte(l), &logs)
	if len(apps) != 2 || apps[0].Name != "nginx" || apps[0].Kind != "process" || len(apps[0].Ports) != 2 {
		t.Fatalf("beklenmeyen uygulamalar: %+v", apps)
	}
	if len([]rune(apps[1].Name)) != 80 || apps[1].CPU != 0 || apps[1].MemMB != 0 || apps[1].Count != 0 {
		t.Fatalf("sınırlar uygulanmalı: %+v", apps[1])
	}
	if len(logs) != 1 || strings.ContainsAny(logs[0].Path, "\n") || len([]rune(logs[0].Sample)) != 260 {
		t.Fatalf("beklenmeyen günlükler: %+v", logs)
	}
	// çok fazla kayıt kesilir
	big := AgentApps{}
	for i := 0; i < 500; i++ {
		big.Apps = append(big.Apps, AgentApp{Name: "p" + itoa(i)})
	}
	a, _ = big.sanitize()
	json.Unmarshal([]byte(a), &apps)
	if len(apps) != maxAgentApps {
		t.Fatalf("en çok %d uygulama: %d", maxAgentApps, len(apps))
	}
}

func TestIngestAppsAndRead(t *testing.T) {
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
	body := map[string]any{
		"asset": map[string]any{"name": "web-1", "kind": "host"},
		"metrics": []map[string]any{{"name": "log.auto_nginx.count", "value": 3}, {"name": "log.auto_nginx.warn", "value": 5},
			{"name": "log.auto_nginx.count", "value": 2}, {"name": "log.auto_nginx.lines", "value": 99}, {"name": "host.cpu_pct", "value": 10}},
		"apps": map[string]any{
			"apps": []map[string]any{{"name": "sshd", "kind": "service", "state": "running"}, {"name": "nginx", "kind": "process", "count": 3, "cpu_pct": 4.2, "mem_mb": 120.5, "ports": []int{80, 443}}},
			"logs": []map[string]any{{"app": "auto_nginx", "path": "/var/log/nginx/error.log", "readable": true, "sample": "[error] connect() failed"}},
		},
	}
	if c, b := call("POST", "/api/ingest", body); c != 202 {
		t.Fatalf("ingest: %d %s", c, b)
	}
	var id int64
	d.QueryRow(`SELECT id FROM assets WHERE name = 'web-1'`).Scan(&id)
	c, b := call("GET", "/api/assets/"+itoa(int(id))+"/apps", nil)
	if c != 200 {
		t.Fatalf("apps: %d %s", c, b)
	}
	var out struct {
		Apps []AgentApp       `json:"apps"`
		Logs []map[string]any `json:"logs"`
	}
	json.Unmarshal(b, &out)
	if len(out.Apps) != 2 || out.Apps[0].Name != "nginx" || out.Apps[1].Kind != "service" {
		t.Fatalf("süreçler önce, servisler sonra gelmeli: %+v", out.Apps)
	}
	if len(out.Logs) != 1 || out.Logs[0]["app"] != "nginx" || out.Logs[0]["errors_1h"].(float64) != 5 || out.Logs[0]["warns_1h"].(float64) != 5 {
		t.Fatalf("günlük özeti (hata=3+2, uyarı=5) beklenir: %+v", out.Logs)
	}
	// Apps göndermeyen heartbeat listeyi silmemeli; yeni liste eskisini değiştirir.
	call("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": "web-1"}, "metrics": []map[string]any{{"name": "host.cpu_pct", "value": 11}}})
	_, b = call("GET", "/api/assets/"+itoa(int(id))+"/apps", nil)
	json.Unmarshal(b, &out)
	if len(out.Apps) != 2 {
		t.Fatalf("heartbeat uygulama listesini silmemeli: %+v", out.Apps)
	}
	body["apps"] = map[string]any{"apps": []map[string]any{{"name": "redis-server", "kind": "process"}}, "logs": []any{}}
	call("POST", "/api/ingest", body)
	_, b = call("GET", "/api/assets/"+itoa(int(id))+"/apps", nil)
	out.Apps = nil
	json.Unmarshal(b, &out)
	if len(out.Apps) != 1 || out.Apps[0].Name != "redis-server" {
		t.Fatalf("yeni liste eskisini değiştirmeli: %+v", out.Apps)
	}
	// Başka asset için boş yanıt
	if c, _ := call("GET", "/api/assets/999999/apps", nil); c != 404 {
		t.Fatalf("olmayan asset 404 olmalı: %d", c)
	}
}
