package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"appmon/internal/probe"
)

// RunJobs arka plan işlerini başlatır; ctx iptal edilince durur.
func (s *Server) RunJobs(ctx context.Context) {
	s.startedAt = time.Now()
	go every(ctx, 5*time.Second, func(c context.Context) { s.runDueProbesN(c, false) })
	go every(ctx, 5*time.Second, func(c context.Context) { s.runDueScenariosN(c, false) })
	go every(ctx, 10*time.Minute, s.slaJob)
	go every(ctx, 2*time.Minute, s.anomalyJob)
	go every(ctx, 15*time.Second, s.markOffline)
	go every(ctx, 15*time.Second, s.evaluateAlerts)
	go every(ctx, 15*time.Second, s.rolloutJob)
	go every(ctx, time.Minute, s.surfaceMetrics)
	go every(ctx, time.Hour, s.maintainPartitions)
	go every(ctx, time.Hour, s.purgeOld)
}

func every(ctx context.Context, d time.Duration, f func(context.Context)) {
	t := time.NewTicker(d)
	defer t.Stop()
	f(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f(ctx)
		}
	}
}

type dueProbe struct {
	id, tenant, asset int64
	typ, target       string
	timeoutMS         int
	http              probe.HTTPOptions
	opts              probe.Options
}

// runDueProbes zamanı gelen probe'ları çalıştırır. last_run, SKIP LOCKED ile aynı sorguda
// işaretlendiği için birden fazla sunucu örneği aynı probe'u iki kez çalıştırmaz.
func (s *Server) runDueProbes(ctx context.Context) { s.runDueProbesN(ctx, true) }

// runDueProbesN: wait=false iken zamanlayıcı turu beklemez; yuva sayısı (APM_PROBE_CONCURRENCY) kadar probe aynı anda
// çalışır, yavaş probe'lar sonraki turları geciktirmez. Yalnızca boş yuva kadar probe seçilir.
func (s *Server) runDueProbesN(ctx context.Context, wait bool) {
	sem := s.sems().probe
	free := cap(sem) - len(sem)
	if free <= 0 {
		return
	}
	rows, err := s.DB.QueryContext(ctx,
		`UPDATE probes SET last_run = now() WHERE id IN (
		     SELECT id FROM probes
		     WHERE enabled AND tenant_id NOT IN (SELECT id FROM tenants WHERE disabled OR paused) AND (last_run IS NULL OR last_run + make_interval(secs => interval_sec) <= now())
		     ORDER BY last_run NULLS FIRST LIMIT $1 FOR UPDATE SKIP LOCKED)
		 RETURNING id, tenant_id, asset_id, type, target, timeout_ms,
		           http_method, headers, body, expect_status, expect_text, verify_tls, follow_redirects, options`, free)
	if err != nil {
		log.Printf("probe sorgusu: %v", err)
		return
	}
	var due []dueProbe
	for rows.Next() {
		var p dueProbe
		var headers, optsRaw []byte
		var verify, follow bool
		if rows.Scan(&p.id, &p.tenant, &p.asset, &p.typ, &p.target, &p.timeoutMS,
			&p.http.Method, &headers, &p.http.Body, &p.http.ExpectStatus, &p.http.ExpectText, &verify, &follow, &optsRaw) != nil {
			continue
		}
		json.Unmarshal(headers, &p.http.Headers)
		json.Unmarshal(optsRaw, &p.opts)
		p.http.SkipTLSVerify, p.http.NoFollowRedirects = !verify, !follow
		due = append(due, p)
	}
	rows.Close()

	var wg sync.WaitGroup
	for _, p := range due {
		wg.Add(1)
		sem <- struct{}{}
		go func(p dueProbe) {
			defer wg.Done()
			defer func() { <-sem }()
			spec := probe.Spec{Type: p.typ, Target: p.target,
				Timeout: time.Duration(p.timeoutMS) * time.Millisecond, HTTP: p.http, Opts: p.opts}
			if p.typ == "host" && p.opts.Method == "ssh" {
				if s.BlockPrivate {
					s.recordProbe(ctx, p, false, 0, "SaaS modunda SSH ile ölçüm kapalıdır", nil)
					return
				}
				if q, qe := s.quotasOf(ctx, p.tenant); qe == nil && !q.AllowSSH {
					s.recordProbe(ctx, p, false, 0, "SSH ile ölçüm bu kiracı için kapalı", nil)
					return
				}
				val, kind, err := s.secretValue(ctx, p.tenant, p.opts.SecretID)
				if err != nil {
					s.recordProbe(ctx, p, false, 0, "SSH kimlik bilgisi kasadan alınamadı: "+err.Error(), nil)
					return
				}
				if kind == "password" {
					spec.Opts.SSHPass = []byte(strings.TrimRight(string(val), "\n"))
				} else {
					spec.Opts.SSHKey = val
				}
				spec.OnHostKey = s.learnHostKey(p)
			}
			s.prepareProbeOutputs(ctx, p, &spec)
			res := probe.RunX(ctx, spec, s.BlockPrivate)
			s.afterProbe(ctx, p, spec, &res)
			if res.Err != nil {
				log.Printf("probe #%d (%s) başarısız: %v", p.id, p.typ, res.Err)
			}
			s.recordProbe(ctx, p, res.Up, res.Latency, probeError(p.target, res.Err), res.Extra)
			if len(res.Detail) > 0 || res.Up {
				// ayrıntı yoksa ve probe başarısızsa önceki başarılı ölçümün ayrıntısı korunur
				if b, e := json.Marshal(res.Detail); e == nil {
					s.DB.ExecContext(ctx, `UPDATE probes SET last_detail = $2::jsonb WHERE id = $1`, p.id, string(b))
				}
			}
		}(p)
	}
	if wait {
		wg.Wait()
	}
}

// probeError hatayı kullanıcıya gösterilecek kısa bir metne çevirir; localhost hedeflerinde Docker ipucu ekler.
func probeError(target string, err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	if hasLocalhost(target) && isConnFailure(msg) {
		msg += dockerHint
	}
	return msg
}

const dockerHint = " | İpucu: APP_MON Docker içinde çalışıyorsa localhost konteynerin kendisidir; ana makinedeki uygulama için host.docker.internal kullanın."

// isConnFailure hatanın bağlantı kurulamamasından (HTTP durum/doğrulama hatası değil) kaynaklandığını söyler.
func isConnFailure(msg string) bool {
	m := strings.ToLower(msg)
	for _, k := range []string{"connection refused", "dial ", "no such host", "i/o timeout", "deadline exceeded", "connection reset", "eof", "unreachable", "err_connection", "err_name_not_resolved", "err_timed_out"} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

func hasLocalhost(s string) bool {
	return strings.Contains(s, "localhost") || strings.Contains(s, "127.0.0.1") || strings.Contains(s, "[::1]")
}

// assetHealth asset'in probe ve senaryolarının son sonuçlarına bakarak durumu belirler:
// hepsi başarılı = up, hepsi başarısız = down, karışık (sayfa açılıyor ama bazı alt bileşenlere gidilemiyor) = degraded.
func assetHealth(ctx context.Context, tx *sql.Tx, asset int64) (status string, err error) {
	var ok, fail int
	err = tx.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM probes WHERE asset_id = $1 AND enabled AND last_ok) + (SELECT count(*) FROM scenarios WHERE asset_id = $1 AND enabled AND last_ok),
		(SELECT count(*) FROM probes WHERE asset_id = $1 AND enabled AND NOT last_ok) + (SELECT count(*) FROM scenarios WHERE asset_id = $1 AND enabled AND NOT last_ok)`,
		asset).Scan(&ok, &fail)
	switch {
	case err != nil:
		return "", err
	case fail == 0:
		return "up", nil
	case ok == 0:
		return "down", nil
	}
	return "degraded", nil
}

// healthPoints durumu alarm kurallarının kullanacağı ölçümlere çevirir (asset.down, asset.degraded).
func healthPoints(status string, now time.Time) []point {
	d, g := 0.0, 0.0
	if status == "down" {
		d = 1
	} else if status == "degraded" {
		g = 1
	}
	return []point{{"asset.down", d, now}, {"asset.degraded", g, now}}
}

func (s *Server) recordProbe(ctx context.Context, p dueProbe, up bool, latency time.Duration, errMsg string, extra map[string]float64) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	now := time.Now()
	upVal := 0.0
	if up {
		upVal = 1
	}
	pts := []point{{"probe.up", upVal, now}}
	if up {
		pts = append(pts, point{"probe.latency_ms", float64(latency.Microseconds()) / 1000, now})
	}
	for k, v := range extra {
		if strings.HasPrefix(k, "host.") || strings.HasPrefix(k, "net.") || strings.HasPrefix(k, "snmp.") || strings.HasPrefix(k, "db.") {
			pts = append(pts, point{k, v, now}) // ana makine ölçümleri agent'ın adlarıyla aynıdır; mevcut kurallar çalışır
		} else {
			pts = append(pts, point{"probe." + k, v, now})
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE probes SET last_error = $2, last_ok = $3, fail_streak = CASE WHEN $3::boolean THEN 0 ELSE fail_streak + 1 END WHERE id = $1`, p.id, errMsg, up); err != nil {
		return
	}
	status, err := assetHealth(ctx, tx, p.asset)
	if err != nil {
		return
	}
	pts = append(pts, healthPoints(status, now)...)
	if err := insertMetrics(ctx, tx, p.tenant, p.asset, pts); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE assets SET status = $2, last_seen = now() WHERE id = $1`, p.asset, status); err != nil {
		return
	}
	tx.Commit()
}

// offlineAgentSec ajanın çevrimdışı sayılacağı sessizlik süresidir (APM_OFFLINE_AGENT_SEC, varsayılan 90 sn).
func (s *Server) offlineAgentSec() int { return clampInt(s.OfflineAgentSec, 90, 30, 86400) }

// warmupLeft sunucu (yeniden) başladıktan sonra çevrimdışı işaretlemenin askıda kaldığı kalan süreyi verir (D2):
// kesintide tamponlayan ajanların yeniden bağlanmasına zaman tanınır; toplu "çevrimdışı" ve bildirim fırtınası olmaz.
func (s *Server) warmupLeft() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	w := time.Duration(s.OfflineWarmupSec) * time.Second
	if s.OfflineWarmupSec == 0 {
		w = 2 * time.Minute
	}
	if s.OfflineWarmupSec < 0 {
		w = 0
	}
	if left := w - time.Since(s.startedAt); left > 0 {
		return left
	}
	return 0
}

// markOffline veri göndermeyen asset'leri çevrimdışı işaretler.
func (s *Server) markOffline(ctx context.Context) {
	if s.warmupLeft() > 0 {
		return
	}
	agentSec := s.offlineAgentSec()
	agentlessSec := 180
	if 2*agentSec > agentlessSec {
		agentlessSec = 2 * agentSec
	}
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE assets a SET status = 'offline'
		 WHERE status <> 'offline' AND last_seen IS NOT NULL AND (
		     (collection = 'agent' AND last_seen < now() - make_interval(secs => $1))
		  OR (collection = 'agentless' AND NOT EXISTS (SELECT 1 FROM probes p WHERE p.asset_id = a.id)
		      AND NOT EXISTS (SELECT 1 FROM scenarios sc WHERE sc.asset_id = a.id)
		      AND last_seen < now() - make_interval(secs => $2)))`, agentSec, agentlessSec); err != nil {
		log.Printf("çevrimdışı işaretleme: %v", err)
	}
}

// maintainPartitions metrics bölümlerini (bugünden 7 gün ilerisine kadar) hazırlar.
func (s *Server) maintainPartitions(ctx context.Context) {
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT appmon_metrics_ensure(7)`).Scan(&n); err != nil {
		log.Printf("metrics bölümleri: %v", err)
		return
	}
	if n > 0 {
		log.Printf("metrics: %d yeni günlük bölüm oluşturuldu", n)
	}
}

// dropOldPartitions saklama süresinin (kiracılar arasında en uzunu) dışında kalan metrics bölümlerini DROP eder.
func (s *Server) dropOldPartitions(ctx context.Context) {
	days := s.RetentionDays
	var lic int
	if s.DB.QueryRowContext(ctx, `SELECT COALESCE(max(retention_days), 0) FROM licenses`).Scan(&lic) == nil && lic > days {
		days = lic
	}
	var n int
	if err := s.DB.QueryRowContext(ctx, `SELECT appmon_metrics_drop_before(now() - make_interval(days => $1))`, days+1).Scan(&n); err != nil {
		log.Printf("eski metrics bölümleri: %v", err)
		return
	}
	if n > 0 {
		log.Printf("metrics: saklama süresi dolan %d bölüm silindi", n)
	}
}

// evaluateAlerts her kural için pencere ortalamasını eşikle karşılaştırır; ihlalde alarm açar, düzelince kapatır.
func (s *Server) evaluateAlerts(ctx context.Context) {
	rules, err := s.DB.QueryContext(ctx,
		`SELECT id, tenant_id, metric, comparator, threshold, window_sec, name, severity, asset_ids, group_name, tag FROM alert_rules WHERE enabled AND tenant_id NOT IN (SELECT id FROM tenants WHERE disabled OR paused)`)
	if err != nil {
		log.Printf("kural sorgusu: %v", err)
		return
	}
	type rule struct {
		id, tenant  int64
		metric, cmp string
		thr         float64
		win         int
		name, sev   string
		assets      []int64
		group, tag  string
	}
	var list []rule
	for rules.Next() {
		var r rule
		if rules.Scan(&r.id, &r.tenant, &r.metric, &r.cmp, &r.thr, &r.win, &r.name, &r.sev, pq.Array(&r.assets), &r.group, &r.tag) == nil {
			list = append(list, r)
		}
	}
	rules.Close()

	for _, r := range list {
		if !s.ruleDue(r.id, r.win) {
			continue // pencere kısaysa sık, uzunsa seyrek değerlendirilir (15–60 sn)
		}
		rows, err := s.DB.QueryContext(ctx,
			`SELECT asset_id, avg(value) FROM metrics
			 WHERE tenant_id = $1 AND name = $2 AND time > now() - make_interval(secs => $3)
			   AND (cardinality($4::bigint[]) = 0 OR asset_id = ANY($4::bigint[]))
			   AND ($5 = '' OR asset_id IN (SELECT id FROM assets WHERE tenant_id = $1 AND grp = $5))
			   AND ($6 = '' OR asset_id IN (SELECT id FROM assets WHERE tenant_id = $1 AND $6 = ANY(tags)))
			 GROUP BY asset_id`,
			r.tenant, r.metric, r.win, pq.Array(r.assets), r.group, r.tag)
		if err != nil {
			continue
		}
		breached, seen := []int64{}, []int64{}
		for rows.Next() {
			var asset int64
			var avg float64
			if rows.Scan(&asset, &avg) != nil {
				continue
			}
			seen = append(seen, asset)
			if (r.cmp == ">" && avg > r.thr) || (r.cmp == "<" && avg < r.thr) {
				breached = append(breached, asset)
				var alertID int64
				var inserted bool
				maint := s.inMaintenance(ctx, r.tenant, asset)
				if err := s.DB.QueryRowContext(ctx,
					`INSERT INTO alerts (tenant_id, rule_id, asset_id, state, value, silenced) VALUES ($1,$2,$3,'open',$4,$5)
					 ON CONFLICT (rule_id, asset_id) WHERE state = 'open' DO UPDATE SET value = EXCLUDED.value
					 RETURNING id, (xmax = 0)`, r.tenant, r.id, asset, avg, maint).Scan(&alertID, &inserted); err == nil && inserted {
					s.siemAlert(r.tenant, "open", asset, r.name, r.sev, avg, r.thr, r.cmp, maint)
					if !maint {
						s.dispatchAlert(r.tenant, "open", asset, r.name, r.sev, avg, r.thr, r.cmp)
					}
				}
			}
		}
		rows.Close()
		// Açık alarm şu durumlarda kapanır: pencerede veri var ve eşik artık aşılmıyor (düzeldi), ya da metrik uzun süredir
		// hiç gelmiyor. Seyrek çalışan senaryolarda pencerede geçici olarak veri olmaması alarmı kapatmaz.
		stale := 3 * r.win
		if stale < 600 {
			stale = 600
		}
		if rrows, err := s.DB.QueryContext(ctx,
			`UPDATE alerts SET state = 'resolved', resolved_at = now()
			 WHERE rule_id = $1 AND state = 'open' AND NOT (asset_id = ANY($2::bigint[]))
			   AND (asset_id = ANY($3::bigint[])
			        OR NOT EXISTS (SELECT 1 FROM metrics m WHERE m.asset_id = alerts.asset_id AND m.name = $4
			                       AND m.time > now() - make_interval(secs => $5)))
			 RETURNING asset_id, value, silenced`,
			r.id, pq.Array(breached), pq.Array(seen), r.metric, stale); err == nil {
			type done struct {
				asset int64
				val   float64
				sil   bool
			}
			var ds []done
			for rrows.Next() {
				var d done
				if rrows.Scan(&d.asset, &d.val, &d.sil) == nil {
					ds = append(ds, d)
				}
			}
			rrows.Close()
			for _, d := range ds {
				s.siemAlert(r.tenant, "resolved", d.asset, r.name, r.sev, d.val, r.thr, r.cmp, d.sil)
				if !d.sil {
					s.dispatchAlert(r.tenant, "resolved", d.asset, r.name, r.sev, d.val, r.thr, r.cmp)
				}
			}
		}
	}
}

func (s *Server) purgeOld(ctx context.Context) {
	s.purgeSpans(ctx)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM metrics m USING licenses l WHERE l.tenant_id = m.tenant_id AND m.time < now() - make_interval(days => COALESCE(NULLIF(l.retention_days, 0), $1))`, s.RetentionDays); err != nil {
		log.Printf("eski metrik temizliği: %v", err)
	}
	s.dropOldPartitions(ctx)
	s.purgeFlows(ctx)
	s.DB.ExecContext(ctx, `DELETE FROM log_events WHERE time < now() - make_interval(days => $1)`, s.logRetention())
	s.DB.ExecContext(ctx, `DELETE FROM asset_latest WHERE time < now() - interval '2 days'`)
	s.DB.ExecContext(ctx, `DELETE FROM snmp_traps m USING licenses l WHERE l.tenant_id = m.tenant_id AND m.at < now() - make_interval(days => COALESCE(NULLIF(l.retention_days, 0), $1))`, s.RetentionDays)
	s.DB.ExecContext(ctx, `DELETE FROM surface_events WHERE reviewed AND reviewed_at < now() - interval '180 days'`)
	s.DB.ExecContext(ctx, `DELETE FROM http_endpoint_stats m USING licenses l WHERE l.tenant_id = m.tenant_id AND m.bucket < now() - make_interval(days => COALESCE(NULLIF(l.retention_days, 0), $1))`, s.RetentionDays)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM scenario_runs m USING licenses l WHERE l.tenant_id = m.tenant_id AND m.at < now() - make_interval(days => COALESCE(NULLIF(l.retention_days, 0), $1))`, s.RetentionDays); err != nil {
		log.Printf("eski senaryo geçmişi temizliği: %v", err)
	}
	// Ekran görüntüleri yalnızca her senaryonun son 10 çalıştırması için tutulur.
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM scenario_shots WHERE run_id IN (
		     SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY scenario_id ORDER BY id DESC) AS rn FROM scenario_runs) t WHERE rn > 10)`); err != nil {
		log.Printf("ekran görüntüsü temizliği: %v", err)
	}
	// Her senaryo için en fazla 500 çalıştırma kaydı tutulur.
	if _, err := s.DB.ExecContext(ctx,
		`DELETE FROM scenario_runs WHERE id IN (
		     SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY scenario_id ORDER BY id DESC) AS rn FROM scenario_runs) t WHERE rn > 500)`); err != nil {
		log.Printf("senaryo geçmişi sınırı: %v", err)
	}
}

// runDueScenarios zamanı gelen senaryoları çalıştırır (aynı SKIP LOCKED yaklaşımı).
func (s *Server) runDueScenarios(ctx context.Context) { s.runDueScenariosN(ctx, true) }

// runDueScenariosN: tarayıcı senaryoları ayrı kapasiteyle (APM_BROWSER_CONCURRENCY) çalışır; böylece uzun süren
// tarayıcı oynatmaları HTTP senaryolarını geciktirmez. wait=false iken tur beklenmez.
func (s *Server) runDueScenariosN(ctx context.Context, wait bool) {
	var wg sync.WaitGroup
	for _, browserKind := range []bool{false, true} {
		var sem chan struct{}
		if browserKind {
			sem = browserSem
		} else {
			sem = s.sems().scen
		}
		free := cap(sem) - len(sem)
		if free <= 0 {
			continue
		}
		for _, sc := range s.dueScenarios(ctx, browserKind, free) {
			wg.Add(1)
			go func(sc scenarioRow, sem chan struct{}, isBrowser bool) {
				defer wg.Done()
				if !isBrowser { // tarayıcı yuvasını executeBrowser kendisi alır
					sem <- struct{}{}
					defer func() { <-sem }()
				}
				if _, ok, _, errMsg := s.executeScenario(ctx, sc, 0); !ok {
					log.Printf("senaryo #%d (%s) başarısız: %s", sc.id, sc.name, errMsg)
				}
			}(sc, sem, browserKind)
		}
	}
	if wait {
		wg.Wait()
	}
}

func (s *Server) dueScenarios(ctx context.Context, browserKind bool, limit int) []scenarioRow {
	rows, err := s.DB.QueryContext(ctx,
		`UPDATE scenarios SET last_run = now() WHERE id IN (
		     SELECT id FROM scenarios
		     WHERE enabled AND (kind = 'browser') = $2 AND tenant_id NOT IN (SELECT id FROM tenants WHERE disabled OR paused) AND (last_run IS NULL OR last_run + make_interval(secs => interval_sec) <= now())
		     ORDER BY last_run NULLS FIRST LIMIT $1 FOR UPDATE SKIP LOCKED)
		 RETURNING id, tenant_id, asset_id, name, timeout_ms, verify_tls, vars, steps, kind`, limit, browserKind)
	if err != nil {
		log.Printf("senaryo sorgusu: %v", err)
		return nil
	}
	defer rows.Close()
	var due []scenarioRow
	for rows.Next() {
		var sc scenarioRow
		var vars, steps []byte
		if rows.Scan(&sc.id, &sc.tenant, &sc.asset, &sc.name, &sc.timeoutMS, &sc.verifyTLS, &vars, &steps, &sc.kind) != nil {
			continue
		}
		if json.Unmarshal(vars, &sc.vars) != nil || sc.decodeSteps(steps) != nil {
			log.Printf("senaryo #%d okunamadı", sc.id)
			continue
		}
		if sc.kind == "browser" {
			if q, qe := s.quotasOf(ctx, sc.tenant); qe == nil && !q.AllowBrowser {
				continue // kiracıda tarayıcı senaryoları kapalı
			}
		}
		due = append(due, sc)
	}
	return due
}
