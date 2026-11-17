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

func TestParseSLAFilter(t *testing.T) {
	get := func(q string) (slaFilter, error) {
		return parseSLAFilter(httptest.NewRequest("GET", "/api/sla?"+q, nil))
	}
	f, err := get("from=2026-11-01&to=2026-11-30")
	if err != nil || !f.legacy() || f.hasWindow || f.To.Day() != 1 || f.To.Month() != 12 {
		t.Fatalf("tam gün aralığı eski yolu kullanmalı: %+v %v", f, err)
	}
	f, err = get("from=2026-11-01&to=2026-11-30&hours=8-17&days=1,2,3,4,5")
	if err != nil || f.legacy() || !f.hasWindow || f.HourFrom != 8 || f.HourTo != 17 || len(f.Days) != 5 {
		t.Fatalf("mesai süzgeci: %+v %v", f, err)
	}
	cond, args := f.where(7)
	if len(args) != 4 || cond == "" {
		t.Fatalf("koşul: %s %v", cond, args)
	}
	f, err = get("from=2026-11-01&to=2026-11-30&hours=7-24")
	if err != nil || !f.hasWindow || f.HourTo != 24 {
		t.Fatalf("7-24: %+v %v", f, err)
	}
	f, err = get("from=2026-11-01&to=2026-11-30&hours=22-6")
	if err != nil {
		t.Fatalf("gece penceresi: %v", err)
	}
	if c, _ := f.where(1); c == "" || !contains(c, "OR h.hod < 6") {
		t.Fatalf("gece penceresi koşulu: %s", c)
	}
	f, err = get("from=2026-11-01&to=2026-11-30&days=0,1,2,3,4,5,6")
	if err != nil || f.hasWindow || f.Days != nil {
		t.Fatalf("7 gün süzgeç sayılmamalı: %+v %v", f, err)
	}
	f, err = get("from=2026-11-14T08:00&to=2026-11-14T20:00&group=hour")
	if err != nil || f.DateOnly || f.legacy() || f.Group != "hour" {
		t.Fatalf("saatlik aralık: %+v %v", f, err)
	}
	for _, bad := range []string{"hours=17-17", "hours=a-b", "hours=8-25", "days=7", "group=year", "from=x", "from=2026-11-30&to=2026-11-01", "from=2025-01-01&to=2026-11-30"} {
		if _, err := get(bad); err == nil {
			t.Errorf("%s geçersiz sayılmalıydı", bad)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestSLAHourlyFiltersAndReset(t *testing.T) {
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
	ctx := t.Context()
	var aid int64
	if err := d.QueryRowContext(ctx, `INSERT INTO assets (tenant_id, name, kind, language, collection) VALUES ($1,'sla-app','service','none','agentless') RETURNING id`, tid).Scan(&aid); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, time.Local)
	for tm := day; tm.Before(day.AddDate(0, 0, 1)); tm = tm.Add(5 * time.Minute) {
		v := 0.0
		if !tm.Before(day.Add(9*time.Hour)) && tm.Before(day.Add(9*time.Hour+30*time.Minute)) { // 09:00-09:30 kesinti
			v = 1
		}
		if _, err := d.Exec(`INSERT INTO metrics (time, tenant_id, asset_id, name, value) VALUES ($1,$2,$3,'asset.down',$4)`, tm, tid, aid, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.rollupSLA(ctx, tid, day, day); err != nil {
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
	ds := day.Format("2006-01-02")
	avail := func(q string) (float64, int, map[string]any) {
		code, m := call("GET", "/api/sla?from="+ds+"&to="+ds+q, nil)
		if code != 200 {
			t.Fatalf("%s: %d %v", q, code, m)
		}
		rows, _ := m["rows"].([]any)
		if len(rows) == 0 {
			return -1, 0, m
		}
		r := rows[0].(map[string]any)
		return r["availability_pct"].(float64), len(rows), m
	}
	all, _, m := avail("")
	if all >= 99.9 || all < 95 || m["legacy"] != true {
		t.Fatalf("tüm gün: %v %v", all, m["legacy"])
	}
	if a, _, m := avail("&hours=0-24&days=0,1,2,3,4,5,6"); a != all || m["legacy"] != true {
		t.Fatalf("süzgeçsiz eşdeğer olmalı: %v %v", a, all)
	}
	if a, _, m := avail("&hours=10-17"); a != 100 || m["legacy"] == true {
		t.Fatalf("10-17 kesinti dışı: %v", a)
	}
	if a, _, _ := avail("&hours=9-10"); a > 60 {
		t.Fatalf("9-10 kesinti içi: %v", a)
	}
	if a, _, _ := avail("&hours=22-6"); a != 100 {
		t.Fatalf("gece penceresi: %v", a)
	}
	wd := int(day.Weekday())
	other := (wd + 3) % 7
	if _, n, _ := avail(fmt.Sprintf("&days=%d", other)); n != 0 {
		t.Fatalf("başka gün süzgeci veri vermemeli: %d", n)
	}
	if _, n, _ := avail(fmt.Sprintf("&days=%d", wd)); n != 1 {
		t.Fatalf("aynı gün süzgeci veri vermeli: %d", n)
	}
	_, _, gm := avail("&group=hour")
	series, _ := gm["series"].([]any)
	if len(series) < 23 {
		t.Fatalf("saatlik seri: %d", len(series))
	}
	bad := 0
	for _, p := range series {
		if p.(map[string]any)["availability_pct"].(float64) < 100 {
			bad++
		}
	}
	if bad != 1 {
		t.Fatalf("yalnız 09:00 saati kesintili olmalı: %d", bad)
	}
	_, _, gd := avail("&group=day")
	if ds, _ := gd["series"].([]any); len(ds) != 1 {
		t.Fatalf("günlük seri: %d", len(ds))
	}
	_, _, gw := avail("&group=week")
	if ws, _ := gw["series"].([]any); len(ws) != 1 {
		t.Fatalf("haftalık seri: %d", len(ws))
	}
	// CSV süzgeçli
	req, _ := http.NewRequest("GET", ts.URL+"/api/sla.csv?from="+ds+"&to="+ds+"&hours=8-17&days=1,2,3,4,5,6,0", nil)
	req.Header.Set("X-API-Key", "rootkey-0123456789")
	resp, _ := http.DefaultClient.Do(req)
	cb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(cb), "08:00-17:00") || !strings.Contains(string(cb), "sla-app") {
		t.Fatalf("csv: %s", cb)
	}
	// SIFIRLAMA: parola/anahtar olmadan reddedilir, doğru anahtarla yapılır
	if code, _ := call("POST", "/api/sla/reset", map[string]any{"asset_ids": []int64{aid}}); code != 403 {
		t.Fatalf("anahtarsız sıfırlama reddedilmeli: %d", code)
	}
	if code, _ := call("POST", "/api/sla/reset", map[string]any{"asset_ids": []int64{aid}, "api_key": "yanlis"}); code != 403 {
		t.Fatalf("yanlış anahtar reddedilmeli: %d", code)
	}
	if code, _ := call("POST", "/api/sla/reset", map[string]any{}); code != 400 {
		t.Fatalf("boş istek 400 olmalı: %d", code)
	}
	if code, m := call("POST", "/api/sla/reset", map[string]any{"asset_ids": []int64{aid}, "api_key": "rootkey-0123456789"}); code != 200 || m["reset"].(float64) != 1 {
		t.Fatalf("sıfırlama: %d %v", code, m)
	}
	if a, n, _ := avail(""); n != 0 || a != -1 {
		t.Fatalf("sıfırlamadan sonra veri kalmamalı: %v %d", a, n)
	}
	// yeniden özetleme eski ölçümleri geri getirmemeli
	if err := s.rollupSLA(ctx, tid, day, day); err != nil {
		t.Fatal(err)
	}
	if _, n, _ := avail(""); n != 0 {
		t.Fatalf("yeniden özetleme sıfırlanan veriyi geri getirdi: %d", n)
	}
	if _, n, _ := avail("&hours=9-10"); n != 0 {
		t.Fatalf("saatlik veri geri geldi: %d", n)
	}
	// başka asset etkilenmemeli; yanlış kiracı asset'i bulunamaz
	if code, _ := call("POST", "/api/sla/reset", map[string]any{"asset_ids": []int64{999999}, "api_key": "rootkey-0123456789"}); code != 404 {
		t.Fatalf("olmayan asset 404 olmalı: %d", code)
	}
	var cnt int
	d.QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'sla_reset'`).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("denetim kaydı: %d", cnt)
	}
}
