package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"appmon/internal/db"
	"appmon/internal/probe"
	"appmon/internal/vault"
	"appmon/web"
)

func TestParseAccessLine(t *testing.T) {
	line := `<134>Nov 11 12:00:00 web1 nginx: appmon_key=apm_abc app=shop-web m=GET uri=/siparis/123?x=1&token=gizli st=200 rt=0.250`
	r, ok := ParseAccessLine(line)
	if !ok || r.Key != "apm_abc" || r.App != "shop-web" || r.Method != "GET" || r.Status != 200 || r.Path != "/siparis/{id}" || math.Abs(r.MS-250) > 0.001 {
		t.Fatalf("ayrıştırma yanlış: %+v ok=%v", r, ok)
	}
	if strings.Contains(r.Path, "gizli") || strings.Contains(r.Path, "token") {
		t.Fatalf("sorgu dizesi atılmalı: %q", r.Path)
	}
	r, ok = ParseAccessLine(`<14>1 2026-11-11T12:00:00Z h apache - - - appmon_key=k app=api m=post uri="/a/b c" st=503 rt_us=1500000`)
	if !ok || r.Method != "POST" || r.Status != 503 || r.MS != 1500 || r.Path != "/a/b c" {
		t.Fatalf("rt_us/tırnaklı değer: %+v ok=%v", r, ok)
	}
	r, ok = ParseAccessLine(`appmon_key=k app=x m=GET uri=/ st=200 rt_ms=12.5`)
	if !ok || r.MS != 12.5 {
		t.Fatalf("rt_ms: %+v", r)
	}
	for _, bad := range []string{
		`nginx: GET /x 200`,                                    // anahtar yok
		`appmon_key=k m=GET uri=/ st=200 rt=1`,                 // app yok
		`appmon_key=k app=x m=GET uri=/ st=999 rt=1`,           // durum geçersiz
		`appmon_key=k app=x m=GET uri=/ st=200`,                // süre yok
		`appmon_key=k app=x m=GET uri=/ st=200 rt=-4`,          // negatif süre
		`appmon_key=k app=bad/name m=GET uri=/ st=200 rt=1`,    // uygulama adı geçersiz
		`appmon_key=k app=x m=GET uri=/ st=200 rt=99999999999`, // aşırı süre
	} {
		if _, ok := ParseAccessLine(bad); ok {
			t.Errorf("geçersiz satır kabul edildi: %q", bad)
		}
	}
}

func TestNormalizePath(t *testing.T) {
	for in, want := range map[string]string{
		"/":                "/",
		"":                 "/",
		"/api/v1/users/42": "/api/v1/users/{id}",
		"/api/users/550e8400-e29b-41d4-a716-446655440000/x": "/api/users/{uuid}/x",
		"/dl/0123456789abcdef0123":                          "/dl/{token}",
		"/u/ali@ornek.com/profil":                           "/u/{email}/profil",
		"/s?q=gizli#x":                                      "/s",
		"/rapor/2026-11-11":                                 "/rapor/{id}",
		"/a/b/c/d/e/f/g/h/i/j":                              "/a/b/c/d/e/f/g/h",
		"siparis/9":                                         "/siparis/{id}",
	} {
		if got := NormalizePath(in); got != want {
			t.Errorf("NormalizePath(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

func TestPercentile(t *testing.T) {
	b := make([]int64, nBuckets)
	// 100 istek: 90 tanesi 10-25 ms kovasında, 10 tanesi 500-1000 ms kovasında
	b[2], b[7] = 90, 10
	if p50 := Percentile(b, 0.5, 900); p50 < 10 || p50 > 25 {
		t.Fatalf("p50 = %v", p50)
	}
	if p99 := Percentile(b, 0.99, 900); p99 < 500 || p99 > 900 {
		t.Fatalf("p99 = %v", p99)
	}
	if Percentile(make([]int64, nBuckets), 0.95, 0) != 0 {
		t.Fatalf("boş histogram 0 vermeli")
	}
	// Sonsuz kova: üst sınır gözlenen en büyük değerdir
	b2 := make([]int64, nBuckets)
	b2[11] = 4
	if p := Percentile(b2, 0.95, 20000); p < 10000 || p > 20000 {
		t.Fatalf("sonsuz kova p95 = %v", p)
	}
}

// Uçtan uca: syslog (UDP) -> özet -> API; kiracı izolasyonu; yavaş sorgu ve bağlantı kayıtları.
func TestAPMAgentless(t *testing.T) {
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
	call := func(key, method, path string, body any) (int, string) {
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
		return resp.StatusCode, string(b)
	}
	root := "rootkey-0123456789"
	for _, n := range []string{"A", "B"} {
		if c, b := call(root, "POST", "/api/tenants", map[string]any{"name": "Kurum " + n, "slug": "k-" + strings.ToLower(n),
			"admin_username": "admin" + strings.ToLower(n), "admin_password": "Parola-" + n + "-12345", "asset_limit": 20, "min_interval_sec": 10}); c != 201 {
			t.Fatalf("kiracı %s: %d %s", n, c, b)
		}
	}
	login := func(u, p string) string {
		_, b := call("", "POST", "/api/login", map[string]string{"username": u, "password": p})
		var o struct{ Key string }
		json.Unmarshal([]byte(b), &o)
		return o.Key
	}
	ka, kb := login("admina", "Parola-A-12345"), login("adminb", "Parola-B-12345")
	agentKey := func(admin, label string) string {
		c, b := call(admin, "POST", "/api/keys", map[string]any{"label": label, "role": "agent"})
		if c != 201 {
			t.Fatalf("agent anahtarı: %d %s", c, b)
		}
		var o struct{ Key string }
		json.Unmarshal([]byte(b), &o)
		return o.Key
	}
	agA, agB := agentKey(ka, "syslog-a"), agentKey(kb, "syslog-b")
	viewerA := func() string {
		_, b := call(ka, "POST", "/api/keys", map[string]any{"label": "viewer-a", "role": "viewer"})
		var o struct{ Key string }
		json.Unmarshal([]byte(b), &o)
		return o.Key
	}()

	// --- syslog alıcısı ---
	if err := s.StartSyslog(t.Context(), "127.0.0.1:0", nil, 5000); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("udp", s.SyslogAddr)
	if err != nil {
		t.Fatal(err)
	}
	send := func(key, app, uri string, st int, rt string) {
		conn.Write([]byte("<134>Nov 11 12:00:00 web1 nginx: appmon_key=" + key + " app=" + app + " m=GET uri=" + uri + " st=" + itoa(st) + " rt=" + rt))
	}
	for i := 0; i < 40; i++ {
		send(agA, "shop", "/siparis/"+itoa(100+i), 200, "0.020")
	}
	for i := 0; i < 8; i++ {
		send(agA, "shop", "/odeme", 200, "0.900")
	}
	send(agA, "shop", "/odeme", 500, "1.500")
	send(agB, "shop", "/b-sadece", 200, "0.010") // aynı uygulama adı, başka kiracı
	send("apm_gecersiz", "shop", "/x", 200, "0.010")
	conn.Write([]byte("çöp satır"))
	wantAccepted := int64(40 + 8 + 1 + 1)
	deadline := time.Now().Add(3 * time.Second)
	for s.syslogStats().Accepted.Load() < wantAccepted && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	st := s.syslogStats()
	if st.Accepted.Load() != wantAccepted || st.Unauthorized.Load() != 1 || st.Invalid.Load() != 1 {
		t.Fatalf("sayaçlar: kabul=%d yetkisiz=%d geçersiz=%d", st.Accepted.Load(), st.Unauthorized.Load(), st.Invalid.Load())
	}
	s.flushSyslog(t.Context())

	var apps struct {
		Apps []struct {
			AssetID int64   `json:"asset_id"`
			App     string  `json:"app"`
			Reqs    int64   `json:"reqs"`
			Err5xx  int64   `json:"err5xx"`
			P95     float64 `json:"p95_ms"`
		}
		Receiver struct{ Enabled bool }
	}
	_, body := call(viewerA, "GET", "/api/perf/apps?window=1h", nil)
	json.Unmarshal([]byte(body), &apps)
	if len(apps.Apps) != 1 || apps.Apps[0].App != "shop" || apps.Apps[0].Reqs != 49 || apps.Apps[0].Err5xx != 1 || !apps.Receiver.Enabled {
		t.Fatalf("A özeti yanlış: %s", body)
	}
	if apps.Apps[0].P95 < 100 {
		t.Fatalf("p95 beklenenden düşük: %v", apps.Apps[0].P95)
	}
	_, body = call(kb, "GET", "/api/perf/apps", nil)
	if !strings.Contains(body, `"reqs":1`) || strings.Contains(body, `"reqs":49`) {
		t.Fatalf("B yalnızca kendi verisini görmeli: %s", body)
	}
	var eps struct {
		Endpoints []struct {
			Endpoint string `json:"endpoint"`
			Reqs     int64  `json:"reqs"`
		}
		Series []map[string]any
	}
	_, body = call(ka, "GET", "/api/perf/endpoints?asset="+itoa(int(apps.Apps[0].AssetID)), nil)
	json.Unmarshal([]byte(body), &eps)
	got := map[string]int64{}
	for _, e := range eps.Endpoints {
		got[e.Endpoint] = e.Reqs
	}
	if got["GET /siparis/{id}"] != 40 || got["GET /odeme"] != 9 || len(eps.Series) == 0 {
		t.Fatalf("uç noktalar: %v seri=%d", got, len(eps.Series))
	}
	if c, _ := call(kb, "GET", "/api/perf/endpoints?asset="+itoa(int(apps.Apps[0].AssetID)), nil); c != 404 {
		t.Fatalf("başka kiracının asset'i görülebildi: %d", c)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM metrics m JOIN assets a ON a.id = m.asset_id WHERE a.name = 'shop' AND m.name IN ('app.p95_ms','app.err_pct','app.req_per_min')`).Scan(&n)
	if n < 6 {
		t.Fatalf("app.* ölçümleri yazılmadı: %d", n)
	}

	// --- yavaş sorgular ---
	_, body = call(ka, "POST", "/api/probes", map[string]any{"asset_name": "db-a", "type": "pgstats", "target": "postgres://x:y@127.0.0.1:1/db", "interval_sec": 3600, "timeout_ms": 1000})
	var pp struct {
		ID      int64 `json:"id"`
		AssetID int64 `json:"asset_id"`
	}
	json.Unmarshal([]byte(body), &pp)
	if pp.ID == 0 {
		t.Fatalf("pgstats probe'u oluşmadı: %s", body)
	}
	dp := dueProbe{id: pp.ID, tenant: tenantOf(t, d, "k-a"), asset: pp.AssetID, typ: "pgstats"}
	snap := &probe.StmtSnapshot{Available: true, Stmts: []probe.Stmt{{QueryID: "1", Query: "SELECT * FROM t WHERE id = $1", Calls: 10, TotalMS: 6000, MeanMS: 600, MaxMS: 900, Rows: 10}}}
	s.saveStatements(t.Context(), dp, snap)
	snap.Stmts[0].Calls, snap.Stmts[0].TotalMS, snap.Stmts[0].MeanMS = 20, 6200, 310 // son turda 10 çağrı, ortalama 20 ms
	s.saveStatements(t.Context(), dp, snap)
	_, body = call(ka, "GET", "/api/perf/queries", nil)
	var q struct {
		Queries []map[string]any
	}
	json.Unmarshal([]byte(body), &q)
	if len(q.Queries) != 1 || q.Queries[0]["recent_mean_ms"] == nil || math.Abs(q.Queries[0]["recent_mean_ms"].(float64)-20) > 0.01 || q.Queries[0]["slow"] != false {
		t.Fatalf("sorgu listesi: %s", body)
	}
	if _, body = call(kb, "GET", "/api/perf/queries", nil); strings.Contains(body, "SELECT") {
		t.Fatalf("B, A'nın sorgularını gördü: %s", body)
	}
	snap2 := &probe.StmtSnapshot{Note: "pg_stat_statements uzantısı bu veritabanında kurulu değil"}
	s.saveStatements(t.Context(), dp, snap2)
	_, body = call(ka, "GET", "/api/perf/queries", nil)
	if strings.Contains(body, "SELECT * FROM") || !strings.Contains(body, "kurulu değil") {
		t.Fatalf("uzantı kaybolunca eski sorgular temizlenmeli ve neden görünmeli: %s", body)
	}

	// --- bağlantılar ve bağımlılık önerisi ---
	mk := func(name, tgt string) dueProbe {
		_, b := call(ka, "POST", "/api/probes", map[string]any{"asset_name": name, "type": "tcp", "target": tgt, "interval_sec": 3600, "timeout_ms": 1000})
		var o struct {
			ID      int64 `json:"id"`
			AssetID int64 `json:"asset_id"`
		}
		json.Unmarshal([]byte(b), &o)
		return dueProbe{id: o.ID, tenant: dp.tenant, asset: o.AssetID, typ: "host"}
	}
	web, dbh := mk("web-host", "127.0.0.1:81"), mk("db-host", "127.0.0.1:82")
	s.saveConns(t.Context(), web, &probe.ConnSnapshot{Addrs: []string{"10.9.0.1", "127.0.0.1"}, Conns: []probe.Conn{
		{Dir: "out", Peer: "10.9.0.2", Port: 5432, N: 3, RTT: 0.4}, {Dir: "out", Peer: "10.9.0.99", Port: 6379, N: 2, RTT: -1}}})
	s.saveConns(t.Context(), dbh, &probe.ConnSnapshot{Addrs: []string{"10.9.0.2"}, Conns: []probe.Conn{{Dir: "in", Peer: "10.9.0.1", Port: 5432, N: 3, RTT: -1}}})
	var cn struct {
		Links   []map[string]any
		Unknown []map[string]any
		Hosts   []map[string]any
	}
	_, body = call(ka, "GET", "/api/perf/conns", nil)
	json.Unmarshal([]byte(body), &cn)
	if len(cn.Links) != 1 || cn.Links[0]["source"] != "web-host" || cn.Links[0]["target"] != "db-host" || cn.Links[0]["port"].(float64) != 5432 ||
		cn.Links[0]["edge_exists"] != false || cn.Links[0]["rtt_ms"].(float64) != 0.4 {
		t.Fatalf("bağımlılık önerisi: %s", body)
	}
	if len(cn.Unknown) != 1 || cn.Unknown[0]["peer"] != "10.9.0.99" || len(cn.Hosts) != 2 {
		t.Fatalf("bilinmeyen hedefler/makineler: %s", body)
	}
	if c, b := call(ka, "POST", "/api/topology/edges", map[string]any{"source": cn.Links[0]["source_id"], "target": cn.Links[0]["target_id"]}); c != 201 {
		t.Fatalf("kenar eklenemedi: %d %s", c, b)
	}
	_, body = call(ka, "GET", "/api/perf/conns", nil)
	if !strings.Contains(body, `"edge_exists":true`) {
		t.Fatalf("kenar eklendikten sonra edge_exists true olmalı: %s", body)
	}
	if _, body = call(kb, "GET", "/api/perf/conns", nil); strings.Contains(body, "web-host") || strings.Contains(body, "10.9.0.") {
		t.Fatalf("B, A'nın bağlantılarını gördü: %s", body)
	}
}

func tenantOf(t *testing.T, d *sql.DB, slug string) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRow(`SELECT id FROM tenants WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func itoa(n int) string { return strconv.Itoa(n) }
