package app

import (
	"appmon/internal/weblog"
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Erişim günlüğü (access log) alıcısı: web sunucusu/ters vekil syslog ile satır gönderir, APP_MON uç nokta başına
// istek sayısı, hata oranı ve gecikme yüzdeliklerini özetler. Satır biçimi anahtar=değer'dir (bkz. kılavuz):
//   appmon_key=<API anahtarı> app=<uygulama adı> m=GET uri=/yol?x=1 st=200 rt=0.123
// rt saniye (nginx $request_time), rt_ms milisaniye, rt_us mikrosaniye (Apache %D) olabilir.

var bucketBounds = weblog.BucketBounds // ms; 12. kova = sonsuz

const (
	nBuckets           = 12
	flushEvery         = 30 * time.Second
	maxEndpointsPerApp = 300
	maxAppsPerTenant   = 50
	statBucket         = 5 * time.Minute
)

// AccessRec ayrıştırılmış tek bir erişim günlüğü satırıdır.
type AccessRec struct {
	Key, App, Method, Path string
	Status                 int
	MS                     float64
}

var (
	appNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$`)
	methodRe  = regexp.MustCompile(`^[A-Z]{3,8}$`)
)

// splitKV "a=1 b=\"iki kelime\" c=3" biçimini çözer; tırnaklı değerlerde boşluk olabilir.
func splitKV(s string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		j := i
		for j < len(s) && s[j] != '=' && s[j] != ' ' && s[j] != '\t' {
			j++
		}
		if j >= len(s) || s[j] != '=' {
			// anahtarsız sözcük: atla
			for j < len(s) && s[j] != ' ' && s[j] != '\t' {
				j++
			}
			i = j
			continue
		}
		k := s[i:j]
		j++
		var v string
		if j < len(s) && s[j] == '"' {
			j++
			e := j
			for e < len(s) && s[e] != '"' {
				e++
			}
			v = s[j:e]
			j = e + 1
		} else {
			e := j
			for e < len(s) && s[e] != ' ' && s[e] != '\t' {
				e++
			}
			v = s[j:e]
			j = e
		}
		if _, dup := out[k]; !dup && len(out) < 40 {
			out[k] = v
		}
		i = j
	}
	return out
}

// ParseAccessLine syslog satırından (RFC 3164/5424 başlığı olabilir) erişim kaydını çıkarır.
func ParseAccessLine(line string) (AccessRec, bool) {
	var rec AccessRec
	idx := strings.Index(line, "appmon_key=")
	if idx < 0 || len(line) > 4096 {
		return rec, false
	}
	kv := splitKV(line[idx:])
	rec.Key = kv["appmon_key"]
	rec.App = kv["app"]
	if rec.Key == "" || len(rec.Key) > 100 || !appNameRe.MatchString(rec.App) {
		return rec, false
	}
	st, err := strconv.Atoi(kv["st"])
	if err != nil || st < 100 || st > 599 {
		return rec, false
	}
	rec.Status = st
	var ms float64
	switch {
	case kv["rt"] != "":
		f, e := strconv.ParseFloat(kv["rt"], 64)
		if e != nil {
			return rec, false
		}
		ms = f * 1000
	case kv["rt_ms"] != "":
		f, e := strconv.ParseFloat(kv["rt_ms"], 64)
		if e != nil {
			return rec, false
		}
		ms = f
	case kv["rt_us"] != "":
		f, e := strconv.ParseFloat(kv["rt_us"], 64)
		if e != nil {
			return rec, false
		}
		ms = f / 1000
	default:
		return rec, false
	}
	if ms < 0 || ms > 3_600_000 {
		return rec, false
	}
	rec.MS = ms
	rec.Method = strings.ToUpper(kv["m"])
	if !methodRe.MatchString(rec.Method) {
		rec.Method = "OTHER"
	}
	rec.Path = NormalizePath(kv["uri"])
	return rec, true
}

// NormalizePath, weblog paketindeki ortak uygulamadır (ajan da aynı kuralı kullanır).
func NormalizePath(uri string) string { return weblog.NormalizePath(uri) }

func bucketIndex(ms float64) int {
	for i, b := range bucketBounds {
		if ms <= b {
			return i
		}
	}
	return nBuckets - 1
}

// Percentile kovalardan (histogram) yüzdelik tahmini yapar; kova içinde doğrusal ara değer kullanır.
// maxMS son (sonsuz) kovanın üst sınırıdır.
func Percentile(b []int64, q, maxMS float64) float64 {
	var total int64
	for _, c := range b {
		total += c
	}
	if total == 0 {
		return 0
	}
	target := q * float64(total)
	var cum float64
	for i, c := range b {
		if c == 0 {
			continue
		}
		if cum+float64(c) >= target {
			lo := 0.0
			if i > 0 {
				lo = bucketBounds[i-1]
			}
			hi := maxMS
			if i < len(bucketBounds) {
				hi = bucketBounds[i]
			}
			if hi < lo {
				hi = lo
			}
			if maxMS > 0 && hi > maxMS {
				hi = maxMS // gözlenen en büyük değerin ötesine çıkma
			}
			if hi < lo {
				lo = hi
			}
			frac := (target - cum) / float64(c)
			return lo + (hi-lo)*frac
		}
		cum += float64(c)
	}
	return maxMS
}

type epAgg struct {
	reqs, e4, e5 int64
	untimed      int64 // süresi bilinmeyen istekler (reqs içinde sayılır; gecikme hesabına girmez)
	sum, max     float64
	b            [nBuckets]int64
}

type aggKey struct {
	tenant   int64
	app      string
	bucket   time.Time
	endpoint string
}

type keyInfo struct {
	tenant int64
	ok     bool
	exp    time.Time
}

type tenantRate struct {
	sec int64
	n   int
}

// SyslogStats alıcının sayaçlarıdır (arayüzdeki durum için).
type SyslogStats struct {
	Accepted, Unauthorized, Invalid, Dropped atomic.Int64
	LastAt                                   atomic.Int64 // unix
}

type syslogState struct {
	mu        sync.Mutex
	agg       map[aggKey]*epAgg
	endpoints map[string]map[string]bool // "tenant|app" -> görülen uç noktalar (günlük)
	apps      map[int64]map[string]bool  // tenant -> uygulama adları (bellekteki sınır)
	day       string
	keys      map[string]keyInfo
	rate      map[int64]*tenantRate
	rateLimit int
	allow     []*net.IPNet
	assetIDs  map[string]int64 // "tenant|app" -> asset id
	warned    map[string]bool
}

func newSyslogState() *syslogState {
	return &syslogState{agg: map[aggKey]*epAgg{}, endpoints: map[string]map[string]bool{}, apps: map[int64]map[string]bool{},
		keys: map[string]keyInfo{}, rate: map[int64]*tenantRate{}, rateLimit: 2000, assetIDs: map[string]int64{}, warned: map[string]bool{}}
}

// InitAccess erişim özeti durumunu ve 30 sn'lik yazma döngüsünü bir kez başlatır (syslog alıcısı ve ajan akışı ortak kullanır).
func (s *Server) InitAccess(ctx context.Context) *syslogState {
	s.accessOnce.Do(func() {
		if s.sys == nil {
			s.sys = newSyslogState()
		}
		go s.syslogFlushLoop(ctx)
	})
	return s.sys
}

// SyslogOn alıcı açık mı; SyslogAddr dinlenen adres.
func (s *Server) syslogStats() *SyslogStats {
	s.syslogOnce.Do(func() { s.syslogStat = &SyslogStats{} })
	return s.syslogStat
}

// StartSyslog UDP ve TCP üzerinde erişim günlüğü alıcısını başlatır. allow doluysa yalnızca bu ağlardaki
// kaynaklar kabul edilir. rate, kiracı başına saniyedeki en çok satırdır (0 = 2000).
func (s *Server) StartSyslog(ctx context.Context, addr string, allow []*net.IPNet, rate int) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", pc.LocalAddr().String())
	if err != nil {
		pc.Close()
		return err
	}
	st := s.InitAccess(ctx)
	st.mu.Lock()
	st.allow = allow
	if rate > 0 {
		st.rateLimit = rate
	}
	st.mu.Unlock()
	s.SyslogAddr = pc.LocalAddr().String()
	s.SyslogOn = true
	s.syslogStats()
	go func() {
		<-ctx.Done()
		pc.Close()
		ln.Close()
	}()
	go func() {
		buf := make([]byte, 8192)
		for {
			n, src, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			ip := addrIP(src)
			for _, l := range strings.Split(string(buf[:n]), "\n") {
				if strings.TrimSpace(l) != "" {
					s.handleSyslogLine(ctx, ip, l)
				}
			}
		}
	}()
	go func() {
		var open atomic.Int32
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			if open.Add(1) > 200 {
				open.Add(-1)
				c.Close()
				continue
			}
			go func(c net.Conn) {
				defer open.Add(-1)
				defer c.Close()
				ip := addrIP(c.RemoteAddr())
				sc := bufio.NewScanner(c)
				sc.Buffer(make([]byte, 0, 8192), 64<<10)
				for {
					c.SetReadDeadline(time.Now().Add(5 * time.Minute))
					if !sc.Scan() {
						return
					}
					s.handleSyslogLine(ctx, ip, sc.Text())
				}
			}(c)
		}
	}()
	log.Printf("erişim günlüğü (syslog) alıcısı dinliyor: %s (UDP+TCP)", s.SyslogAddr)
	return nil
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.UDPAddr:
		return v.IP
	case *net.TCPAddr:
		return v.IP
	}
	return nil
}

// tenantOfKey anahtarı kiracıya çevirir (60 sn önbellekli); yalnızca veri gönderebilen roller kabul edilir.
func (s *Server) tenantOfKey(ctx context.Context, key string) (int64, bool) {
	st := s.sys
	h := HashKey(key)
	st.mu.Lock()
	ki, ok := st.keys[h]
	st.mu.Unlock()
	if ok && time.Now().Before(ki.exp) {
		return ki.tenant, ki.ok
	}
	var tenant int64
	var role string
	err := s.DB.QueryRowContext(ctx,
		`SELECT k.tenant_id, k.role FROM api_keys k JOIN tenants t ON t.id = k.tenant_id
		  WHERE k.key_hash = $1 AND (k.expires_at IS NULL OR k.expires_at > now()) AND NOT t.disabled`, h).Scan(&tenant, &role)
	good := err == nil && allowed("ingest", role)
	ttl := 60 * time.Second
	if !good {
		ttl = 30 * time.Second
	}
	st.mu.Lock()
	if len(st.keys) > 2000 {
		st.keys = map[string]keyInfo{}
	}
	st.keys[h] = keyInfo{tenant, good, time.Now().Add(ttl)}
	st.mu.Unlock()
	return tenant, good
}

func (s *Server) handleSyslogLine(ctx context.Context, src net.IP, line string) {
	st, stat := s.sys, s.syslogStats()
	if len(st.allow) > 0 {
		ok := false
		for _, n := range st.allow {
			if src != nil && n.Contains(src) {
				ok = true
				break
			}
		}
		if !ok {
			stat.Unauthorized.Add(1)
			return
		}
	}
	rec, ok := ParseAccessLine(line)
	if !ok {
		stat.Invalid.Add(1)
		return
	}
	tenant, good := s.tenantOfKey(ctx, rec.Key)
	if !good {
		stat.Unauthorized.Add(1)
		return
	}
	now := time.Now()
	st.mu.Lock()
	defer st.mu.Unlock()
	rt := st.rate[tenant]
	if rt == nil {
		rt = &tenantRate{}
		st.rate[tenant] = rt
	}
	if sec := now.Unix(); rt.sec != sec {
		rt.sec, rt.n = sec, 0
	}
	if rt.n++; rt.n > st.rateLimit {
		stat.Dropped.Add(1)
		return
	}
	if d := now.UTC().Format("2006-01-02"); d != st.day {
		st.day, st.endpoints = d, map[string]map[string]bool{}
	}
	if st.apps[tenant] == nil {
		st.apps[tenant] = map[string]bool{}
	}
	if !st.apps[tenant][rec.App] {
		if len(st.apps[tenant]) >= maxAppsPerTenant {
			stat.Dropped.Add(1)
			return
		}
		st.apps[tenant][rec.App] = true
	}
	ek := strconv.FormatInt(tenant, 10) + "|" + rec.App
	set := st.endpoints[ek]
	if set == nil {
		set = map[string]bool{}
		st.endpoints[ek] = set
	}
	ep := rec.Method + " " + rec.Path
	if !set[ep] {
		if len(set) >= maxEndpointsPerApp {
			ep = "OTHER /*"
		} else {
			set[ep] = true
		}
	}
	k := aggKey{tenant, rec.App, now.Truncate(statBucket), ep}
	a := st.agg[k]
	if a == nil {
		if len(st.agg) > 200000 {
			stat.Dropped.Add(1)
			return
		}
		a = &epAgg{}
		st.agg[k] = a
	}
	a.reqs++
	switch {
	case rec.Status >= 500:
		a.e5++
	case rec.Status >= 400:
		a.e4++
	}
	a.sum += rec.MS
	if rec.MS > a.max {
		a.max = rec.MS
	}
	a.b[bucketIndex(rec.MS)]++
	stat.Accepted.Add(1)
	stat.LastAt.Store(now.Unix())
}

func (s *Server) syslogFlushLoop(ctx context.Context) {
	t := time.NewTicker(flushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.flushSyslog(context.Background())
			return
		case <-t.C:
			s.flushSyslog(ctx)
		}
	}
}

// flushSyslog bellekteki özetleri tabloya yazar ve her uygulama için app.* ölçümlerini üretir.
func (s *Server) flushSyslog(ctx context.Context) {
	st := s.sys
	if st == nil {
		return
	}
	st.mu.Lock()
	batch := st.agg
	st.agg = map[aggKey]*epAgg{}
	st.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	type appKey struct {
		tenant int64
		app    string
	}
	totals := map[appKey]*epAgg{}
	for k, a := range batch {
		ak := appKey{k.tenant, k.app}
		t := totals[ak]
		if t == nil {
			t = &epAgg{}
			totals[ak] = t
		}
		t.reqs += a.reqs
		t.e4 += a.e4
		t.e5 += a.e5
		t.untimed += a.untimed
		t.sum += a.sum
		if a.max > t.max {
			t.max = a.max
		}
		for i := range a.b {
			t.b[i] += a.b[i]
		}
	}
	ids := map[appKey]int64{}
	for ak := range totals {
		id, err := s.syslogAsset(ctx, ak.tenant, ak.app)
		if err != nil {
			if _, lic := licenseStatus(err); lic {
				st.mu.Lock()
				wk := fmt.Sprintf("%d|%s|%s", ak.tenant, ak.app, time.Now().UTC().Format("2006-01-02"))
				first := !st.warned[wk]
				st.warned[wk] = true
				st.mu.Unlock()
				if first {
					log.Printf("erişim günlüğü: %q uygulaması lisans sınırı nedeniyle eklenemedi", ak.app)
				}
			} else {
				log.Printf("erişim günlüğü asset: %v", err)
			}
			continue
		}
		ids[ak] = id
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	for k, a := range batch {
		id, ok := ids[appKey{k.tenant, k.app}]
		if !ok {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO http_endpoint_stats (tenant_id, asset_id, bucket, endpoint, reqs, err4xx, err5xx, sum_ms, max_ms,
			b0,b1,b2,b3,b4,b5,b6,b7,b8,b9,b10,b11, untimed) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)
			ON CONFLICT (tenant_id, asset_id, bucket, endpoint) DO UPDATE SET reqs = http_endpoint_stats.reqs + EXCLUDED.reqs, untimed = http_endpoint_stats.untimed + EXCLUDED.untimed,
			  err4xx = http_endpoint_stats.err4xx + EXCLUDED.err4xx, err5xx = http_endpoint_stats.err5xx + EXCLUDED.err5xx,
			  sum_ms = http_endpoint_stats.sum_ms + EXCLUDED.sum_ms, max_ms = GREATEST(http_endpoint_stats.max_ms, EXCLUDED.max_ms),
			  b0 = http_endpoint_stats.b0 + EXCLUDED.b0, b1 = http_endpoint_stats.b1 + EXCLUDED.b1, b2 = http_endpoint_stats.b2 + EXCLUDED.b2,
			  b3 = http_endpoint_stats.b3 + EXCLUDED.b3, b4 = http_endpoint_stats.b4 + EXCLUDED.b4, b5 = http_endpoint_stats.b5 + EXCLUDED.b5,
			  b6 = http_endpoint_stats.b6 + EXCLUDED.b6, b7 = http_endpoint_stats.b7 + EXCLUDED.b7, b8 = http_endpoint_stats.b8 + EXCLUDED.b8,
			  b9 = http_endpoint_stats.b9 + EXCLUDED.b9, b10 = http_endpoint_stats.b10 + EXCLUDED.b10, b11 = http_endpoint_stats.b11 + EXCLUDED.b11`,
			k.tenant, id, k.bucket, k.endpoint, a.reqs, a.e4, a.e5, a.sum, a.max,
			a.b[0], a.b[1], a.b[2], a.b[3], a.b[4], a.b[5], a.b[6], a.b[7], a.b[8], a.b[9], a.b[10], a.b[11], a.untimed); err != nil {
			log.Printf("erişim günlüğü özeti yazılamadı: %v", err)
			return
		}
	}
	eps := map[appKey][]string{}
	for k := range batch {
		ak := appKey{k.tenant, k.app}
		eps[ak] = append(eps[ak], k.endpoint)
	}
	now := time.Now()
	defer func() {
		for ak, list := range eps {
			if id, ok := ids[ak]; ok {
				s.surfaceFromAccess(ctx, ak.tenant, id, list)
			}
		}
	}()
	for ak, t := range totals {
		id, ok := ids[ak]
		if !ok || t.reqs == 0 {
			continue
		}
		pts := []point{
			{"app.req_per_min", float64(t.reqs) * 60 / flushEvery.Seconds(), now},
			{"app.err_pct", 100 * float64(t.e5) / float64(t.reqs), now},
		}
		if timed := t.reqs - t.untimed; timed > 0 { // süresiz istekler gecikme ölçümlerine girmez
			pts = append(pts, point{"app.avg_ms", t.sum / float64(timed), now}, point{"app.p50_ms", Percentile(t.b[:], 0.50, t.max), now},
				point{"app.p95_ms", Percentile(t.b[:], 0.95, t.max), now}, point{"app.p99_ms", Percentile(t.b[:], 0.99, t.max), now})
		}
		if err := insertMetrics(ctx, tx, ak.tenant, id, pts); err != nil {
			return
		}
		tx.ExecContext(ctx, `UPDATE assets SET status = 'up', last_seen = now() WHERE id = $1`, id)
	}
	tx.Commit()
}

// syslogAsset uygulama adı için asset'i bulur veya (lisans izin veriyorsa) oluşturur.
func (s *Server) syslogAsset(ctx context.Context, tenant int64, app string) (int64, error) {
	st := s.sys
	ck := strconv.FormatInt(tenant, 10) + "|" + app
	st.mu.Lock()
	id, ok := st.assetIDs[ck]
	st.mu.Unlock()
	if ok {
		return id, nil
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	id, created, err := s.ensureAsset(ctx, tx, tenant, app, "service", "none", "logs")
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	if created {
		s.audit(ctx, tenant, "syslog", "asset.auto_register", app)
	}
	st.mu.Lock()
	if len(st.assetIDs) > 5000 {
		st.assetIDs = map[string]int64{}
	}
	st.assetIDs[ck] = id
	st.mu.Unlock()
	return id, nil
}

// ---- Okuma API'leri ----

var windows = map[string]string{"1h": "1 hour", "6h": "6 hours", "24h": "24 hours"}

func windowOf(r *http.Request) (string, string) {
	w := r.URL.Query().Get("window")
	if iv, ok := windows[w]; ok {
		return w, iv
	}
	return "1h", windows["1h"]
}

const bucketCols = `sum(b0), sum(b1), sum(b2), sum(b3), sum(b4), sum(b5), sum(b6), sum(b7), sum(b8), sum(b9), sum(b10), sum(b11)`

func statsRow(reqs, untimed, e4, e5 int64, sum, mx float64, b []int64) map[string]any {
	m := map[string]any{"reqs": reqs, "err4xx": e4, "err5xx": e5, "max_ms": mx, "untimed": untimed}
	if reqs > 0 {
		m["err_pct"] = 100 * float64(e5) / float64(reqs)
	}
	m["total_ms"] = sum
	if timed := reqs - untimed; timed > 0 { // istek süresi olmayan satırlar gecikmeyi bozmasın
		m["avg_ms"] = sum / float64(timed)
		m["p50_ms"] = Percentile(b, 0.50, mx)
		m["p95_ms"] = Percentile(b, 0.95, mx)
		m["p99_ms"] = Percentile(b, 0.99, mx)
	}
	return m
}

// GET /api/perf/apps?window=1h|6h|24h - erişim günlüğünden gelen uygulamaların özeti.
func (s *Server) perfApps(w http.ResponseWriter, r *http.Request, a Auth) {
	win, iv := windowOf(r)
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT a.id, a.name, sum(reqs), sum(err4xx), sum(err5xx), sum(sum_ms), max(max_ms), sum(untimed), `+bucketCols+`
		   FROM http_endpoint_stats s JOIN assets a ON a.id = s.asset_id
		  WHERE s.tenant_id = $1 AND s.bucket >= now() - $2::interval GROUP BY a.id, a.name ORDER BY a.name`, a.TenantID, iv)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id, reqs, e4, e5, un int64
		var name string
		var sum, mx float64
		b := make([]int64, nBuckets)
		args := []any{&id, &name, &reqs, &e4, &e5, &sum, &mx, &un}
		for i := range b {
			args = append(args, &b[i])
		}
		if rows.Scan(args...) != nil {
			continue
		}
		m := statsRow(reqs, un, e4, e5, sum, mx, b)
		m["asset_id"], m["app"] = id, name
		m["req_per_min"] = float64(reqs) / secondsOf(win) * 60
		list = append(list, m)
	}
	// Dağıtık izleme (OTLP/ajan) verisinden gelen uygulamalar: giriş span'leri (sunucu/tüketici veya kök) istek sayılır.
	seen := map[string]bool{}
	for _, m := range list {
		seen[m["app"].(string)] = true
	}
	if trs, err := s.DB.QueryContext(r.Context(),
		`SELECT service, count(*), count(*) FILTER (WHERE status = 2), avg(dur_us)/1000.0,
		        percentile_cont(0.5) WITHIN GROUP (ORDER BY dur_us)/1000.0, percentile_cont(0.95) WITHIN GROUP (ORDER BY dur_us)/1000.0,
		        percentile_cont(0.99) WITHIN GROUP (ORDER BY dur_us)/1000.0, max(dur_us)/1000.0
		   FROM spans WHERE tenant_id = $1 AND start_time >= now() - $2::interval AND (kind IN (2,5) OR parent_id = '')
		  GROUP BY service ORDER BY service`, a.TenantID, iv); err == nil {
		for trs.Next() {
			var svc string
			var reqs, errs int64
			var avg, p50, p95, p99, mx float64
			if trs.Scan(&svc, &reqs, &errs, &avg, &p50, &p95, &p99, &mx) != nil || seen[svc] {
				continue
			}
			list = append(list, map[string]any{"asset_id": 0, "app": svc, "source": "trace", "reqs": reqs, "err5xx": errs, "err4xx": 0, "untimed": 0,
				"err_pct": 100 * float64(errs) / float64(reqs), "avg_ms": avg, "p50_ms": p50, "p95_ms": p95, "p99_ms": p99, "max_ms": mx,
				"req_per_min": float64(reqs) / secondsOf(win) * 60})
		}
		trs.Close()
	}
	stat := s.syslogStats()
	writeJSON(w, 200, map[string]any{"window": win, "apps": list, "receiver": map[string]any{
		"enabled": s.SyslogOn, "addr": s.SyslogAddr, "accepted": stat.Accepted.Load(), "unauthorized": stat.Unauthorized.Load(),
		"invalid": stat.Invalid.Load(), "dropped": stat.Dropped.Load(), "last_at": stat.LastAt.Load()}})
}

func secondsOf(win string) float64 {
	switch win {
	case "6h":
		return 6 * 3600
	case "24h":
		return 24 * 3600
	}
	return 3600
}

// GET /api/perf/endpoints?asset=ID&window= - bir uygulamanın uç nokta bazında özeti ve zaman serisi.
func (s *Server) perfEndpoints(w http.ResponseWriter, r *http.Request, a Auth) {
	win, iv := windowOf(r)
	asset, err := strconv.ParseInt(r.URL.Query().Get("asset"), 10, 64)
	if err != nil || asset <= 0 {
		fail(w, 400, "asset parametresi zorunlu")
		return
	}
	var name string
	if s.DB.QueryRowContext(r.Context(), `SELECT name FROM assets WHERE id = $1 AND tenant_id = $2`, asset, a.TenantID).Scan(&name) != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT endpoint, sum(reqs), sum(err4xx), sum(err5xx), sum(sum_ms), max(max_ms), sum(untimed), `+bucketCols+`
		   FROM http_endpoint_stats WHERE tenant_id = $1 AND asset_id = $2 AND bucket >= now() - $3::interval
		  GROUP BY endpoint ORDER BY sum(sum_ms) DESC LIMIT 300`, a.TenantID, asset, iv)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	eps := []map[string]any{}
	for rows.Next() {
		var ep string
		var reqs, e4, e5, un int64
		var sum, mx float64
		b := make([]int64, nBuckets)
		args := []any{&ep, &reqs, &e4, &e5, &sum, &mx, &un}
		for i := range b {
			args = append(args, &b[i])
		}
		if rows.Scan(args...) != nil {
			continue
		}
		m := statsRow(reqs, un, e4, e5, sum, mx, b)
		m["endpoint"] = ep
		eps = append(eps, m)
	}
	rows.Close()
	srows, err := s.DB.QueryContext(r.Context(),
		`SELECT bucket, sum(reqs), sum(err5xx), sum(sum_ms), max(max_ms), sum(untimed), `+bucketCols+`
		   FROM http_endpoint_stats WHERE tenant_id = $1 AND asset_id = $2 AND bucket >= now() - $3::interval
		  GROUP BY bucket ORDER BY bucket`, a.TenantID, asset, iv)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer srows.Close()
	series := []map[string]any{}
	for srows.Next() {
		var at time.Time
		var reqs, e5, un int64
		var sum, mx float64
		b := make([]int64, nBuckets)
		args := []any{&at, &reqs, &e5, &sum, &mx, &un}
		for i := range b {
			args = append(args, &b[i])
		}
		if srows.Scan(args...) != nil {
			continue
		}
		pt := map[string]any{"t": at, "reqs": reqs, "err5xx": e5}
		if timed := reqs - un; timed > 0 {
			pt["avg_ms"], pt["p95_ms"] = sum/float64(timed), Percentile(b, 0.95, mx)
		}
		series = append(series, pt)
	}
	sort.SliceStable(eps, func(i, j int) bool { return eps[i]["total_ms"].(float64) > eps[j]["total_ms"].(float64) })
	writeJSON(w, 200, map[string]any{"window": win, "app": name, "asset_id": asset, "endpoints": eps, "series": series})
}

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

var _ = sql.ErrNoRows
