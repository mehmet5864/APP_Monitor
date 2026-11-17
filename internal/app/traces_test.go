package app

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
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

// küçük protobuf kodlayıcı (test için)
func pbVar(v uint64) []byte { return binary.AppendUvarint(nil, v) }
func pbLen(num int, b []byte) []byte {
	out := pbVar(uint64(num<<3 | 2))
	out = append(out, pbVar(uint64(len(b)))...)
	return append(out, b...)
}
func pbVarF(num int, v uint64) []byte { return append(pbVar(uint64(num<<3)), pbVar(v)...) }
func pbFix(num int, v uint64) []byte {
	out := pbVar(uint64(num<<3 | 1))
	return binary.LittleEndian.AppendUint64(out, v)
}
func pbStrKV(k, v string) []byte {
	return append(pbLen(1, []byte(k)), pbLen(2, pbLen(1, []byte(v)))...)
}

func TestParseTracePB(t *testing.T) {
	tid := bytes.Repeat([]byte{0xab}, 16)
	sid := bytes.Repeat([]byte{0x01}, 8)
	var sp []byte
	sp = append(sp, pbLen(1, tid)...)
	sp = append(sp, pbLen(2, sid)...)
	sp = append(sp, pbLen(5, []byte("GET /x"))...)
	sp = append(sp, pbVarF(6, 2)...)
	sp = append(sp, pbFix(7, 1000)...)
	sp = append(sp, pbFix(8, 5000)...)
	sp = append(sp, pbLen(9, pbStrKV("http.method", "GET"))...)
	sp = append(sp, pbLen(15, append(pbLen(2, []byte("boom")), pbVarF(3, 2)...))...)
	res := pbLen(1, pbLen(1, pbStrKV("service.name", "svc-a")))
	res = append(res, pbLen(2, pbLen(2, sp))...)
	out, err := parseTracePB(pbLen(1, res))
	if err != nil || len(out) != 1 || len(out[0].Spans) != 1 {
		t.Fatalf("beklenmeyen: %v %+v", err, out)
	}
	g := out[0].Spans[0]
	if out[0].Attrs["service.name"] != "svc-a" || g.TraceID != "abababababababababababababababab" || g.SpanID != "0101010101010101" ||
		g.Name != "GET /x" || g.Kind != 2 || g.StartNs != 1000 || g.EndNs != 5000 || g.Status != 2 || g.StatusMsg != "boom" || g.Attrs["http.method"] != "GET" {
		t.Fatalf("alanlar yanlış: %+v", g)
	}
	if _, err := parseTracePB([]byte{0x0a, 0xff, 0x01}); err == nil {
		t.Fatal("bozuk gövde hata vermeli")
	}
}

func TestTracesIngestAndQuery(t *testing.T) {
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
	do := func(method, path string, body []byte, hdr map[string]string) (int, []byte) {
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	now := time.Now()
	ns := func(off time.Duration) string { return fmt.Sprint(now.Add(off).UnixNano()) }
	tid := "5b8aa5a2d2c872e8321cf37308d69df2"
	span := func(id, parent, name string, kind int, off, dur time.Duration, code int, attrs string) string {
		return fmt.Sprintf(`{"traceId":"%s","spanId":"%s","parentSpanId":"%s","name":"%s","kind":%d,"startTimeUnixNano":"%s","endTimeUnixNano":"%s","status":{"code":%d,"message":"x"},"attributes":[%s]}`,
			tid, id, parent, name, kind, ns(off), ns(off+dur), code, attrs)
	}
	rs := func(svc string, spans ...string) string {
		j := ""
		for i, s := range spans {
			if i > 0 {
				j += ","
			}
			j += s
		}
		return fmt.Sprintf(`{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"%s"}}]},"scopeSpans":[{"spans":[%s]}]}`, svc, j)
	}
	body := `{"resourceSpans":[` +
		rs("gateway", span("0000000000000001", "", "GET /orders", 2, -300*time.Millisecond, 200*time.Millisecond, 0, ""),
			span("0000000000000002", "0000000000000001", "call orders", 3, -290*time.Millisecond, 180*time.Millisecond, 0, "")) + `,` +
		rs("orders", span("0000000000000003", "0000000000000002", "list orders", 2, -280*time.Millisecond, 150*time.Millisecond, 2, ""),
			span("0000000000000004", "0000000000000003", "SELECT orders", 3, -270*time.Millisecond, 100*time.Millisecond, 0, `{"key":"db.system","value":{"stringValue":"postgresql"}}`)) +
		`]}`
	c, b := do("POST", "/v1/traces", []byte(body), map[string]string{"Content-Type": "application/json"})
	if c != 200 {
		t.Fatalf("alım: %d %s", c, b)
	}
	// aynı gövde tekrar gönderilince çoğalmaz (idempotent), gzip kabul edilir
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(body))
	zw.Close()
	if c, b = do("POST", "/v1/traces", gz.Bytes(), map[string]string{"Content-Type": "application/json", "Content-Encoding": "gzip"}); c != 200 {
		t.Fatalf("gzip alım: %d %s", c, b)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM spans`).Scan(&n)
	if n != 4 {
		t.Fatalf("4 span beklenir: %d", n)
	}
	// protobuf alımı
	{
		ptid := bytes.Repeat([]byte{0xcd}, 16)
		var sp []byte
		sp = append(sp, pbLen(1, ptid)...)
		sp = append(sp, pbLen(2, bytes.Repeat([]byte{0x09}, 8))...)
		sp = append(sp, pbLen(5, []byte("proto-op"))...)
		sp = append(sp, pbVarF(6, 2)...)
		sp = append(sp, pbFix(7, uint64(now.UnixNano()))...)
		sp = append(sp, pbFix(8, uint64(now.Add(50*time.Millisecond).UnixNano()))...)
		res := pbLen(1, pbLen(1, pbStrKV("service.name", "proto-svc")))
		res = append(res, pbLen(2, pbLen(2, sp))...)
		if c, b = do("POST", "/v1/traces", pbLen(1, res), map[string]string{"Content-Type": "application/x-protobuf"}); c != 200 {
			t.Fatalf("protobuf alım: %d %s", c, b)
		}
	}
	if c, _ = do("POST", "/v1/traces", []byte(`{bozuk`), map[string]string{"Content-Type": "application/json"}); c != 400 {
		t.Fatalf("bozuk gövde 400 olmalı: %d", c)
	}
	// liste
	c, b = do("GET", "/api/traces?since=1h", nil, nil)
	var lst struct {
		Traces []traceSummary `json:"traces"`
	}
	json.Unmarshal(b, &lst)
	if c != 200 || len(lst.Traces) != 2 {
		t.Fatalf("2 trace beklenir: %d %s", c, b)
	}
	var found *traceSummary
	for i := range lst.Traces {
		if lst.Traces[i].TraceID == tid {
			found = &lst.Traces[i]
		}
	}
	if found == nil || found.Spans != 4 || found.Errors != 1 || len(found.Services) != 2 || found.RootSvc != "gateway" || found.RootName != "GET /orders" {
		t.Fatalf("özet yanlış: %+v", found)
	}
	if found.DurUs < 190000 || found.DurUs > 210000 {
		t.Fatalf("süre ≈200ms olmalı: %d", found.DurUs)
	}
	for _, q := range []struct {
		qs   string
		want int
	}{{"status=error", 1}, {"service=orders", 1}, {"service=proto-svc", 1}, {"min_ms=150", 1}, {"q=select", 1}, {"q=yok-boyle", 0}, {"service=orders&status=error&min_ms=1", 1}} {
		_, b = do("GET", "/api/traces?"+q.qs, nil, nil)
		var l struct {
			Traces []traceSummary `json:"traces"`
		}
		json.Unmarshal(b, &l)
		if len(l.Traces) != q.want {
			t.Fatalf("%s: %d beklenirken %d: %s", q.qs, q.want, len(l.Traces), b)
		}
	}
	// ayrıntı
	c, b = do("GET", "/api/traces/"+tid, nil, nil)
	var det struct {
		Spans []spanOut `json:"spans"`
		DurUs int64     `json:"dur_us"`
	}
	json.Unmarshal(b, &det)
	if c != 200 || len(det.Spans) != 4 || det.Spans[0].OffsetUs != 0 || det.Spans[3].Attrs["db.system"] != "postgresql" {
		t.Fatalf("ayrıntı yanlış: %d %s", c, b)
	}
	if c, _ = do("GET", "/api/traces/"+"00000000000000000000000000000009", nil, nil); c != 404 {
		t.Fatalf("404 beklenir: %d", c)
	}
	if c, _ = do("GET", "/api/traces/zz", nil, nil); c != 400 {
		t.Fatalf("400 beklenir: %d", c)
	}
	// servis özeti
	_, b = do("GET", "/api/traces/services?since=1h", nil, nil)
	var sv struct {
		Services []struct {
			Service string  `json:"service"`
			Calls   int     `json:"calls"`
			Errors  int     `json:"errors"`
			P95Us   float64 `json:"p95_us"`
		} `json:"services"`
	}
	json.Unmarshal(b, &sv)
	got := map[string]int{}
	errs := map[string]int{}
	for _, x := range sv.Services {
		got[x.Service] = x.Calls
		errs[x.Service] = x.Errors
	}
	if got["gateway"] != 1 || got["orders"] != 1 || got["proto-svc"] != 1 || errs["orders"] != 1 {
		t.Fatalf("servis özeti yanlış: %s", b)
	}
	// operasyonlar
	_, b = do("GET", "/api/traces/operations?service=orders", nil, nil)
	if !bytes.Contains(b, []byte("SELECT orders")) {
		t.Fatalf("operasyonlar: %s", b)
	}
	// harita: gateway→orders (servisler arası) + orders→postgresql (dış)
	_, b = do("GET", "/api/traces/map?since=1h", nil, nil)
	var mp struct {
		Edges []struct {
			From, To string
			Calls    int
			Errors   int
			Ext      bool `json:"external"`
		}
	}
	json.Unmarshal(b, &mp)
	var svcEdge, extEdge bool
	for _, e := range mp.Edges {
		if e.From == "gateway" && e.To == "orders" && !e.Ext && e.Calls == 1 {
			svcEdge = true
		}
		if e.From == "orders" && e.To == "postgresql" && e.Ext {
			extEdge = true
		}
	}
	if !svcEdge || !extEdge {
		t.Fatalf("harita kenarları eksik: %s", b)
	}
	// servisler asset olarak kaydedildi
	d.QueryRow(`SELECT count(*) FROM assets WHERE name IN ('gateway','orders','proto-svc')`).Scan(&n)
	if n != 3 {
		t.Fatalf("3 asset beklenir: %d", n)
	}
	// saklama: eski span'lar temizlenir
	d.Exec(`UPDATE spans SET start_time = now() - interval '30 days' WHERE service = 'gateway'`)
	s.purgeSpans(t.Context())
	d.QueryRow(`SELECT count(*) FROM spans`).Scan(&n)
	if n != 3 {
		t.Fatalf("eski span'lar silinmeli: %d", n)
	}
}

func TestSelfTrace(t *testing.T) {
	if p, s, smp, ok := parseTraceparent("00-5b8aa5a2d2c872e8321cf37308d69df2-00f067aa0ba902b7-01"); !ok || p != "5b8aa5a2d2c872e8321cf37308d69df2" || s != "00f067aa0ba902b7" || !smp {
		t.Fatalf("traceparent: %v %v %v %v", p, s, smp, ok)
	}
	for _, bad := range []string{"", "00-xyz-00f067aa0ba902b7-01", "ff-5b8aa5a2d2c872e8321cf37308d69df2-00f067aa0ba902b7-01", "00-00000000000000000000000000000000-00f067aa0ba902b7-01"} {
		if _, _, _, ok := parseTraceparent(bad); ok {
			t.Fatalf("geçersiz kabul edildi: %q", bad)
		}
	}
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
	s := &Server{DB: d, Mode: "onprem", RetentionDays: 14, Static: web.Static(), Vault: v, MaxTenants: 4, SelfTrace: true, SelfTraceSample: 0}
	if _, err := s.Bootstrap(t.Context(), "rootkey-0123456789", 1000); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()
	get := func(path string, hdr map[string]string) int {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// örnekleme 0: sıradan istek saklanmaz
	get("/api/overview", nil)
	s.selfFlush(t.Context())
	var n int
	d.QueryRow(`SELECT count(*) FROM spans`).Scan(&n)
	if n != 0 {
		t.Fatalf("örnekleme 0 iken span olmamalı: %d", n)
	}
	// çağıran örnekledi (sampled=1): trace'e bağlanır
	tid := "5b8aa5a2d2c872e8321cf37308d69df2"
	get("/api/overview", map[string]string{"traceparent": "00-" + tid + "-00f067aa0ba902b7-01"})
	// trace uç noktaları ve alım izlenmez
	get("/api/traces?since=1h", map[string]string{"traceparent": "00-" + tid + "-00f067aa0ba902b8-01"})
	s.selfFlush(t.Context())
	var svc, name, parent, tr, attrs string
	var kind int
	if err := d.QueryRow(`SELECT service, name, parent_id, trace_id, kind, attrs::text FROM spans`).Scan(&svc, &name, &parent, &tr, &kind, &attrs); err != nil {
		t.Fatalf("1 span beklenir: %v", err)
	}
	if svc != "appmon-server" || name != "GET /api/overview" || parent != "00f067aa0ba902b7" || tr != tid || kind != 2 || !strings.Contains(attrs, `"200"`) {
		t.Fatalf("span yanlış: %s %s %s %s %d %s", svc, name, parent, tr, kind, attrs)
	}
	// servis asset sayılmaz
	d.QueryRow(`SELECT count(*) FROM assets WHERE name = 'appmon-server'`).Scan(&n)
	if n != 0 {
		t.Fatal("appmon-server asset olmamalı")
	}
	// örnekleme 1: parent'sız yeni trace kimliği üretilir; 4xx hata sayılmaz
	s.SelfTraceSample = 1
	if c := get("/api/sla?from=bozuk", nil); c < 400 {
		t.Fatalf("404 beklenir: %d", c)
	}
	s.selfFlush(t.Context())
	var st int
	if err := d.QueryRow(`SELECT parent_id, status FROM spans WHERE name = 'GET /api/sla' LIMIT 1`).Scan(&parent, &st); err != nil || parent != "" || st != 0 {
		t.Fatalf("kök span (4xx hata değil): %v %q %d", err, parent, st)
	}
	// API'den görünür
	req, _ := http.NewRequest("GET", ts.URL+"/api/traces/services?since=1h", nil)
	req.Header.Set("X-API-Key", "rootkey-0123456789")
	resp, _ := http.DefaultClient.Do(req)
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "appmon-server") {
		t.Fatalf("servis özetinde appmon-server olmalı: %s", b)
	}
}

func TestOtelScriptDownload(t *testing.T) {
	s := &Server{Static: web.Static()}
	ts := httptest.NewServer(s.Routes())
	defer ts.Close()
	for _, n := range []string{"otel-kurulum.sh", "otel-kurulum.ps1"} {
		resp, err := http.Get(ts.URL + "/kurulum/" + n)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || strings.Contains(string(b), "__APPMON_URL__") || !strings.Contains(string(b), ts.URL) {
			t.Fatalf("%s: %d, adres gömülmeli", n, resp.StatusCode)
		}
		if !strings.Contains(resp.Header.Get("Content-Disposition"), n) {
			t.Fatalf("%s: indirme başlığı yok", n)
		}
	}
	s.PublicURL = "https://appmon.ornek.local"
	resp, _ := http.Get(ts.URL + "/kurulum/otel-kurulum.sh")
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), `SERVER="${APPMON_URL:-https://appmon.ornek.local}"`) {
		t.Fatal("PublicURL gömülmeli")
	}
	if !strings.HasPrefix(string(b), "#!/usr/bin/env bash") {
		t.Fatal("betik bash ile başlamalı")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/kurulum/otel-kurulum.sh", nil)
	req.Host = "evil.com/x\"; rm -rf"
	s.PublicURL = ""
	resp, _ = http.DefaultClient.Do(req)
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(b), "rm -rf") {
		t.Fatal("geçersiz Host betiğe girmemeli")
	}
}

func TestConnMap(t *testing.T) {
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
	post := func(body map[string]any) int {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", ts.URL+"/api/ingest", bytes.NewReader(b))
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// db01 PostgreSQL dinliyor; web01'deki nginx -> app ve app -> db01:5432
	if c := post(map[string]any{"asset": map[string]any{"name": "db01", "kind": "host"}, "metrics": []any{},
		"apps":  map[string]any{"apps": []map[string]any{{"name": "postgres", "kind": "process", "ports": []int{5432}}}},
		"conns": map[string]any{"ips": []string{"10.0.0.5", "bozuk"}, "edges": []map[string]any{}}}); c >= 300 {
		t.Fatalf("db01: %d", c)
	}
	if c := post(map[string]any{"asset": map[string]any{"name": "web01", "kind": "host"}, "metrics": []any{},
		"conns": map[string]any{"ips": []string{"10.0.0.4"}, "edges": []map[string]any{
			{"src": "nginx", "dst": "gunicorn", "addr": "127.0.0.1", "port": 8000, "n": 4},
			{"src": "gunicorn", "dst": "", "addr": "10.0.0.5", "port": 5432, "n": 6},
			{"src": "gunicorn", "dst": "", "addr": "93.184.216.34", "port": 443, "n": 2},
			{"src": "bozuk", "dst": "", "addr": "not-an-ip", "port": 80, "n": 1},
			{"src": "x", "dst": "", "addr": "10.0.0.9", "port": 0, "n": 1},
		}}}); c >= 300 {
		t.Fatalf("web01: %d", c)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/traces/map?since=1h", nil)
	req.Header.Set("X-API-Key", "rootkey-0123456789")
	resp, _ := http.DefaultClient.Do(req)
	var mp struct {
		Conn []connEdge `json:"conn_edges"`
	}
	json.NewDecoder(resp.Body).Decode(&mp)
	resp.Body.Close()
	got := map[string]connEdge{}
	for _, e := range mp.Conn {
		got[e.From+">"+e.To] = e
	}
	if len(mp.Conn) != 3 {
		t.Fatalf("3 kenar beklenir (geçersizler atılır): %+v", mp.Conn)
	}
	if e := got["nginx@web01>gunicorn@web01"]; e.Conns != 4 || e.Ext {
		t.Fatalf("aynı makine kenarı: %+v", got)
	}
	if e := got["gunicorn@web01>postgres@db01"]; e.Conns != 6 || e.Ext {
		t.Fatalf("makineler arası kenar (IP -> makine, port -> süreç): %+v", got)
	}
	if e := got["gunicorn@web01>93.184.216.34:443 (https)"]; e.Conns != 2 || !e.Ext {
		t.Fatalf("dış adres kenarı: %+v", got)
	}
	// anlık görüntü değişir: yeni gönderimde eski kenarlar silinir
	post(map[string]any{"asset": map[string]any{"name": "web01", "kind": "host"}, "metrics": []any{},
		"conns": map[string]any{"ips": []string{"10.0.0.4"}, "edges": []map[string]any{{"src": "nginx", "dst": "gunicorn", "addr": "127.0.0.1", "port": 8000, "n": 1}}}})
	var n int
	d.QueryRow(`SELECT count(*) FROM net_edges`).Scan(&n)
	if n != 1 {
		t.Fatalf("anlık görüntü değiştirilmeli: %d", n)
	}
	// asset silinince kenarlar da silinir
	d.Exec(`DELETE FROM assets WHERE name = 'web01'`)
	d.QueryRow(`SELECT count(*) FROM net_edges`).Scan(&n)
	if n != 0 {
		t.Fatalf("asset silinince kenarlar silinmeli: %d", n)
	}
}

func TestIngestGzip(t *testing.T) {
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
	body := `{"asset":{"name":"gz-host","kind":"host"},"metrics":[{"name":"host.cpu_pct","value":5}]}`
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(body))
	zw.Close()
	do := func(r io.Reader) int {
		req, _ := http.NewRequest("POST", ts.URL+"/api/ingest", r)
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		req.Header.Set("Content-Encoding", "gzip")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := do(&buf); c != 202 {
		t.Fatalf("gzip ingest: %d", c)
	}
	if c := do(strings.NewReader("not gzip")); c != 400 {
		t.Fatalf("bozuk gzip 400 olmalı: %d", c)
	}
}

func TestAgentConfDownload(t *testing.T) {
	s := &Server{Static: web.Static(), PublicURL: "https://apm.test"}
	for _, n := range []string{"appmon-agent-linux.conf", "appmon-agent-windows.conf"} {
		w := httptest.NewRecorder()
		s.otelScript(n)(w, httptest.NewRequest("GET", "/kurulum/"+n, nil))
		b := w.Body.String()
		if w.Code != 200 || !strings.Contains(b, "APM_SERVER=https://apm.test") || !strings.Contains(b, "\nSEND_HOST_CPU=1\n") {
			t.Fatalf("%s: %d %q", n, w.Code, b[:min(len(b), 120)])
		}
	}
}

func TestAgentRemoteConfig(t *testing.T) {
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
	call := func(method, path string, body any) map[string]any {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		out["_code"] = float64(resp.StatusCode)
		return out
	}
	beat := func(labels map[string]string) map[string]any {
		return call("POST", "/api/ingest", map[string]any{"asset": map[string]any{"name": "rc-host", "kind": "host", "labels": labels}, "metrics": []any{}})
	}
	r := beat(map[string]string{"os": "linux", "cfg_send": "HOST_CPU,APPS"})
	if r["config"] != nil {
		t.Fatalf("ayar yokken config gelmemeli: %v", r)
	}
	id := int64(r["asset_id"].(float64))
	if c := call("PUT", "/api/agent/config", map[string]any{"asset_ids": []int64{id}, "send": []string{"BOGUS"}}); c["_code"] != float64(400) {
		t.Fatalf("geçersiz anahtar 400 olmalı: %v", c)
	}
	if c := call("PUT", "/api/agent/config", map[string]any{"asset_ids": []int64{id}, "send": []string{"SEND_HOST_CPU", "SEND_APPS"}}); c["updated"] != float64(1) {
		t.Fatalf("kaydedilemedi: %v", c)
	}
	r = beat(map[string]string{"os": "linux", "cfg_send": "HOST_CPU"})
	cf, _ := r["config"].(map[string]any)
	if cf == nil || cf["v"] != float64(1) || len(cf["send"].([]any)) != 2 {
		t.Fatalf("config teklifi yok: %v", r)
	}
	if r = beat(map[string]string{"os": "linux", "cfg_v": "1", "cfg_send": "HOST_CPU,APPS"}); r["config"] != nil {
		t.Fatalf("uygulanmış sürümde teklif tekrarlanmamalı: %v", r)
	}
	if r = beat(map[string]string{"os": "linux", "cfg_lock": "1"}); r["config"] != nil {
		t.Fatalf("kilitli ajana ayar gönderilmemeli: %v", r)
	}
	call("PUT", "/api/agent/config", map[string]any{"asset_ids": []int64{id}, "send": []string{"SEND_HOST_CPU"}})
	if r = beat(map[string]string{"cfg_v": "1"}); r["config"] == nil || r["config"].(map[string]any)["v"] != float64(2) {
		t.Fatalf("sürüm artmalı: %v", r)
	}
	l := call("GET", "/api/agent/config", nil)
	ag := l["agents"].([]any)
	if len(l["items"].([]any)) < 10 || len(ag) != 1 || ag[0].(map[string]any)["remote"] != true {
		t.Fatalf("liste hatalı: %v", l)
	}
	call("PUT", "/api/agent/config", map[string]any{"asset_ids": []int64{id}, "clear": true})
	if r = beat(map[string]string{"cfg_v": "2"}); r["config"] == nil || r["config"].(map[string]any)["v"] != float64(0) {
		t.Fatalf("kaldırma v=0 ile bildirilmeli: %v", r)
	}
	if r = beat(map[string]string{}); r["config"] != nil {
		t.Fatalf("etiketsiz istek: %v", r)
	}
}

func TestAgentPerfData(t *testing.T) {
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
	call := func(method, path string, body any) map[string]any {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		out["_code"] = float64(resp.StatusCode)
		return out
	}
	beat := func(body map[string]any) float64 { return call("POST", "/api/ingest", body)["_code"].(float64) }
	stmt := func(id string, calls int, total float64) map[string]any {
		return map[string]any{"id": id, "q": "SELECT * FROM t WHERE id = $1", "calls": calls, "total_ms": total, "mean_ms": total / float64(calls), "max_ms": 9.5, "rows": 3}
	}
	base := func(extra map[string]any) map[string]any {
		m := map[string]any{"asset": map[string]any{"name": "db-host", "kind": "host"}, "metrics": []any{}}
		for k, x := range extra {
			m[k] = x
		}
		return m
	}
	if c := beat(base(map[string]any{
		"dbstats": []any{map[string]any{"engine": "postgres", "db": "appdb", "available": true, "stmts": []any{stmt("111", 10, 50), stmt("222", 4, 4000)}},
			map[string]any{"engine": "postgres", "db": "other", "available": false, "note": "pg_stat_statements kurulu değil"}},
		"conns": map[string]any{"ips": []string{"10.9.0.5"}, "edges": []any{map[string]any{"src": "app", "addr": "10.9.0.7", "port": 5432, "n": 3}},
			"in": []any{map[string]any{"addr": "10.9.0.8", "port": 8080, "n": 2}}},
	})); c != 202 {
		t.Fatalf("ingest: %v", c)
	}
	beat(base(map[string]any{"dbstats": []any{map[string]any{"engine": "postgres", "db": "appdb", "available": true, "stmts": []any{stmt("111", 14, 70), stmt("222", 8, 8000)}}}}))
	q := call("GET", "/api/perf/queries", nil)
	qs, _ := q["queries"].([]any)
	srcs, _ := q["sources"].([]any)
	if len(qs) != 2 || len(srcs) != 2 {
		t.Fatalf("sorgular/kaynaklar: %v", q)
	}
	first := qs[0].(map[string]any)
	if first["asset"] != "db-host / appdb" || first["calls"] != float64(8) || first["recent_calls"] != float64(4) {
		t.Fatalf("ilk sorgu: %v", first)
	}
	cn := call("GET", "/api/perf/conns", nil)
	hs, _ := cn["hosts"].([]any)
	if len(hs) == 0 {
		t.Fatalf("bağlantı özeti boş: %v", cn)
	}
	h0 := hs[0].(map[string]any)
	if h0["out"] != float64(3) || h0["in"] != float64(2) {
		t.Fatalf("giden/gelen: %v", h0)
	}
	// trace verisinden uygulama
	body := `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"odeme"}}]},"scopeSpans":[{"spans":[{"traceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa01","spanId":"bbbbbbbbbbbbbb01","name":"POST /pay","kind":2,"startTimeUnixNano":"` + fmt.Sprint(time.Now().Add(-time.Minute).UnixNano()) + `","endTimeUnixNano":"` + fmt.Sprint(time.Now().Add(-time.Minute+80*time.Millisecond).UnixNano()) + `"}]}]}]}`
	req, _ := http.NewRequest("POST", ts.URL+"/v1/traces", strings.NewReader(body))
	req.Header.Set("X-API-Key", "rootkey-0123456789")
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 200 {
		t.Fatalf("trace: %v %v", err, resp)
	}
	ap := call("GET", "/api/perf/apps?window=1h", nil)
	found := false
	for _, x := range ap["apps"].([]any) {
		m := x.(map[string]any)
		if m["app"] == "odeme" && m["source"] == "trace" && m["reqs"] == float64(1) {
			found = true
		}
	}
	if !found {
		t.Fatalf("trace uygulaması listede yok: %v", ap)
	}
}

func TestAgentRemoteDB(t *testing.T) {
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
	call := func(method, path, host string, body any) (map[string]any, string) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, ts.URL+path, bytes.NewReader(b))
		req.Header.Set("X-API-Key", "rootkey-0123456789")
		if host != "" {
			req.Host = host
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var out map[string]any
		json.Unmarshal(raw, &out)
		out["_code"] = float64(resp.StatusCode)
		return out, string(raw)
	}
	beat := func(host string, labels map[string]string) (map[string]any, string) {
		return call("POST", "/api/ingest", host, map[string]any{"asset": map[string]any{"name": "db-agent", "kind": "host", "labels": labels}, "metrics": []any{}})
	}
	r, _ := beat("", map[string]string{"os": "linux"})
	id := int64(r["asset_id"].(float64))
	dbq := map[string]any{"host": "10.0.0.5", "port": 5432, "name": "appdb", "user": "izleme", "password": "S3cr@t:/?#", "sslmode": "require"}
	put := func(host string, send []string, db map[string]any, ids ...int64) map[string]any {
		if ids == nil {
			ids = []int64{id}
		}
		o, _ := call("PUT", "/api/agent/config", host, map[string]any{"asset_ids": ids, "send": send, "db": db})
		return o
	}
	// düz HTTP (loopback olmayan Host) ile parola reddedilir
	if o := put("apm.firma.local", []string{"SEND_DBSTATS"}, dbq); o["_code"] != float64(400) {
		t.Fatalf("HTTP üzerinden parola reddedilmeli: %v", o)
	}
	// birden çok ajana veritabanı bağlantısı verilemez
	if o := put("", []string{"SEND_DBSTATS"}, dbq, id, id+1); o["_code"] != float64(400) {
		t.Fatalf("çoklu ajan reddedilmeli: %v", o)
	}
	if o := put("", []string{"SEND_DBSTATS"}, map[string]any{"host": "bad host!", "name": "x", "user": "u", "password": "p"}); o["_code"] != float64(400) {
		t.Fatalf("geçersiz sunucu adı reddedilmeli: %v", o)
	}
	if o := put("", []string{"SEND_DBSTATS"}, dbq); o["updated"] != float64(1) {
		t.Fatalf("kaydedilemedi: %v", o)
	}
	// liste parolayı asla döndürmez
	l, raw := call("GET", "/api/agent/config", "", nil)
	if strings.Contains(raw, "S3cr@t") || !strings.Contains(raw, `"password_set":true`) {
		t.Fatalf("liste parolayı sızdırıyor veya password_set yok: %s", raw)
	}
	_ = l
	// düz HTTP'de teklif gönderilmez; güvenli istekte DSN iletilir
	if r, _ := beat("apm.firma.local", map[string]string{"os": "linux"}); r["config"] != nil {
		t.Fatalf("HTTP üzerinden parola içeren teklif verilmemeli: %v", r)
	}
	r, raw = beat("", map[string]string{"os": "linux"})
	cf, _ := r["config"].(map[string]any)
	dbm, _ := cf["db"].(map[string]any)
	got, _ := dbm["dsn"].(string)
	if got != "postgres://izleme:S3cr%40t%3A%2F%3F%23@10.0.0.5:5432/appdb?sslmode=require" {
		t.Fatalf("DSN hatalı: %q (%s)", got, raw)
	}
	// parola boş bırakılırsa mevcut parola korunur
	dbq["password"] = ""
	dbq["user"] = "izleme2"
	if o := put("", []string{"SEND_DBSTATS"}, dbq); o["updated"] != float64(1) {
		t.Fatalf("güncellenemedi: %v", o)
	}
	r, _ = beat("", map[string]string{"os": "linux", "cfg_v": "1"})
	cf, _ = r["config"].(map[string]any)
	got, _ = cf["db"].(map[string]any)["dsn"].(string)
	if !strings.HasPrefix(got, "postgres://izleme2:S3cr%40t%3A%2F%3F%23@") {
		t.Fatalf("parola korunmadı: %q", got)
	}
	// kaldırma
	if o := put("", []string{"SEND_DBSTATS"}, map[string]any{"remove": true}); o["updated"] != float64(1) {
		t.Fatalf("kaldırılamadı: %v", o)
	}
	r, _ = beat("", map[string]string{"os": "linux", "cfg_v": "2"})
	cf, _ = r["config"].(map[string]any)
	if cf == nil || cf["db"] != nil {
		t.Fatalf("db kaldırılmış olmalı: %v", r)
	}
	var n int
	d.QueryRow(`SELECT count(*) FROM audit_log WHERE detail LIKE '%S3cr%'`).Scan(&n)
	if n != 0 {
		t.Fatal("parola denetim kaydına yazılmış")
	}
}
