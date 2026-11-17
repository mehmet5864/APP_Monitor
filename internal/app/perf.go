package app

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"appmon/internal/probe"

	"github.com/lib/pq"
)

// Ajansız APM verileri: yavaş sorgular (pg_stat_statements), HTTP gecikme kırılımı ve SSH ile okunan bağlantılar.

const stmtEvery = 5 * time.Minute // sorgu özeti en sık bu aralıkla alınır

// prepareProbeOutputs probe çalışmadan önce, ek tablo verisi toplanacaksa çıkış işaretçilerini hazırlar.
func (s *Server) prepareProbeOutputs(ctx context.Context, p dueProbe, spec *probe.Spec) {
	switch {
	case p.typ == "pgstats":
		var at time.Time
		err := s.DB.QueryRowContext(ctx, `SELECT updated_at FROM db_stmt_state WHERE probe_id = $1`, p.id).Scan(&at)
		if err != nil || time.Since(at) >= stmtEvery {
			spec.StmtOut = &probe.StmtSnapshot{}
		}
	case p.typ == "host" && p.opts.Method == "ssh":
		spec.ConnOut = &probe.ConnSnapshot{}
	case p.typ == "http":
		spec.SurfOut = &probe.HTTPSurface{}
	}
}

// afterProbe probe bittikten sonra HTTP kırılımını, yavaş sorguları ve bağlantıları saklar; ek ölçümleri extra'ya ekler.
func (s *Server) afterProbe(ctx context.Context, p dueProbe, spec probe.Spec, res *probe.Result) {
	if res.Timing != nil {
		if b, err := json.Marshal(res.Timing); err == nil {
			s.DB.ExecContext(ctx, `UPDATE probes SET last_timing = $2 WHERE id = $1`, p.id, string(b))
		}
	}
	if spec.StmtOut != nil && res.Up {
		s.saveStatements(ctx, p, spec.StmtOut)
	}
	if spec.ConnOut != nil && res.Up {
		total, rttSum, rttN := 0, 0.0, 0
		for _, c := range spec.ConnOut.Conns {
			total += c.N
			if c.RTT >= 0 {
				rttSum += c.RTT * float64(c.N)
				rttN += c.N
			}
		}
		if res.Extra == nil {
			res.Extra = map[string]float64{}
		}
		if !spec.ConnOut.SkipConns {
			res.Extra["host.conns"] = float64(total)
			if rttN > 0 {
				res.Extra["host.conn_rtt_ms"] = rttSum / float64(rttN)
			}
		}
		s.saveConns(ctx, p, spec.ConnOut)
		s.surfaceFromConns(ctx, p.tenant, p.asset, spec.ConnOut)
	}
	if spec.SurfOut != nil && res.Up {
		s.surfaceFromHTTP(ctx, p.tenant, p.asset, spec.SurfOut)
	}
}

func (s *Server) saveStatements(ctx context.Context, p dueProbe, snap *probe.StmtSnapshot) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	ts := time.Now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO db_stmt_state (probe_id, tenant_id, available, note, updated_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (probe_id) DO UPDATE SET available = EXCLUDED.available, note = EXCLUDED.note, updated_at = EXCLUDED.updated_at`,
		p.id, p.tenant, snap.Available, snap.Note, ts); err != nil {
		return
	}
	if snap.Available {
		for _, st := range snap.Stmts {
			if _, err := tx.ExecContext(ctx, `INSERT INTO db_statements (probe_id, tenant_id, queryid, query, calls, total_ms, mean_ms, max_ms, rows_total, updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
				ON CONFLICT (probe_id, queryid) DO UPDATE SET
				  prev_calls = db_statements.calls, prev_total = db_statements.total_ms, prev_at = db_statements.updated_at,
				  query = EXCLUDED.query, calls = EXCLUDED.calls, total_ms = EXCLUDED.total_ms, mean_ms = EXCLUDED.mean_ms,
				  max_ms = EXCLUDED.max_ms, rows_total = EXCLUDED.rows_total, updated_at = EXCLUDED.updated_at`,
				p.id, p.tenant, st.QueryID, st.Query, st.Calls, st.TotalMS, st.MeanMS, st.MaxMS, st.Rows, ts); err != nil {
				return
			}
		}
	}
	// Bu turda görünmeyen eski satırları kaldır (snapshot, tablo her turda tazelenir).
	if _, err := tx.ExecContext(ctx, `DELETE FROM db_statements WHERE probe_id = $1 AND updated_at < $2`, p.id, ts); err != nil {
		return
	}
	tx.Commit()
}

func (s *Server) saveConns(ctx context.Context, p dueProbe, snap *probe.ConnSnapshot) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if !snap.SkipConns {
		if _, err := tx.ExecContext(ctx, `DELETE FROM host_conns WHERE probe_id = $1`, p.id); err != nil {
			return
		}
	}
	for _, c := range snap.Conns {
		if snap.SkipConns {
			break
		}
		var rtt any
		if c.RTT >= 0 {
			rtt = c.RTT
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO host_conns (probe_id, tenant_id, asset_id, direction, peer_ip, port, conns, rtt_ms) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
			ON CONFLICT DO NOTHING`, p.id, p.tenant, p.asset, c.Dir, c.Peer, c.Port, c.N, rtt); err != nil {
			return
		}
	}
	if snap.SkipAddrs {
		tx.Commit()
		return
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM host_addrs WHERE tenant_id = $1 AND asset_id = $2`, p.tenant, p.asset); err != nil {
		return
	}
	for _, a := range snap.Addrs {
		ip := net.ParseIP(a)
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO host_addrs (tenant_id, asset_id, ip) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, p.tenant, p.asset, ip.String()); err != nil {
			return
		}
	}
	tx.Commit()
}

// ---- Okuma API'leri ----

// GET /api/perf/queries - pg_stat_statements'ten toplanan yavaş sorgular.
func (s *Server) perfQueries(w http.ResponseWriter, r *http.Request, a Auth) {
	srcs := []map[string]any{}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT st.probe_id, a.id, a.name, st.available, st.note, st.updated_at
		   FROM db_stmt_state st JOIN probes p ON p.id = st.probe_id JOIN assets a ON a.id = p.asset_id
		  WHERE st.tenant_id = $1
		UNION ALL
		SELECT 0, a.id, a.name || ' / ' || st.db, st.available, st.note, st.updated_at
		   FROM agent_db_state st JOIN assets a ON a.id = st.asset_id
		  WHERE st.tenant_id = $1 AND st.updated_at > now() - interval '1 hour'
		ORDER BY 3`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	for rows.Next() {
		var pid, aid int64
		var name, note string
		var avail bool
		var at time.Time
		if rows.Scan(&pid, &aid, &name, &avail, &note, &at) == nil {
			srcs = append(srcs, map[string]any{"probe_id": pid, "asset_id": aid, "asset": name, "available": avail, "note": note, "updated_at": at})
		}
	}
	rows.Close()
	qrows, err := s.DB.QueryContext(r.Context(),
		`SELECT d.probe_id, a.name, d.queryid, d.query, d.calls, d.total_ms, d.mean_ms, d.max_ms, d.rows_total, d.prev_calls, d.prev_total, d.prev_at, d.updated_at
		   FROM db_statements d JOIN probes p ON p.id = d.probe_id JOIN assets a ON a.id = p.asset_id
		  WHERE d.tenant_id = $1
		UNION ALL
		SELECT 0, a.name || ' / ' || d.db, d.queryid, d.query, d.calls, d.total_ms, d.mean_ms, d.max_ms, d.rows_total, d.prev_calls, d.prev_total, d.prev_at, d.updated_at
		   FROM agent_db_statements d JOIN assets a ON a.id = d.asset_id
		  WHERE d.tenant_id = $1 AND d.updated_at > now() - interval '1 hour'
		ORDER BY 6 DESC LIMIT 300`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer qrows.Close()
	list := []map[string]any{}
	for qrows.Next() {
		var pid, calls, rowsN, pcalls int64
		var asset, qid, query string
		var total, mean, mx, ptotal float64
		var prevAt *time.Time
		var upd time.Time
		if qrows.Scan(&pid, &asset, &qid, &query, &calls, &total, &mean, &mx, &rowsN, &pcalls, &ptotal, &prevAt, &upd) != nil {
			continue
		}
		it := map[string]any{"probe_id": pid, "asset": asset, "queryid": qid, "query": query, "calls": calls, "total_ms": total,
			"mean_ms": mean, "max_ms": mx, "rows": rowsN, "slow": mean >= probe.SlowQueryMS && calls >= 2, "updated_at": upd}
		if prevAt != nil && calls >= pcalls && total >= ptotal {
			if dc := calls - pcalls; dc > 0 {
				it["recent_calls"] = dc
				it["recent_mean_ms"] = (total - ptotal) / float64(dc)
			} else {
				it["recent_calls"] = 0
			}
		}
		list = append(list, it)
	}
	writeJSON(w, 200, map[string]any{"sources": srcs, "queries": list, "slow_ms": probe.SlowQueryMS})
}

func stripQuery(t string) string {
	if u, err := url.Parse(t); err == nil && u.Host != "" {
		u.RawQuery, u.Fragment, u.User = "", "", nil
		return u.String()
	}
	return t
}

// GET /api/perf/timing - HTTP probe'larının son gecikme kırılımı.
func (s *Server) perfTiming(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT p.id, a.id, a.name, p.target, p.last_timing, p.last_ok, p.last_run
		   FROM probes p JOIN assets a ON a.id = p.asset_id
		  WHERE p.tenant_id = $1 AND p.type = 'http' AND p.enabled AND p.last_timing <> '{}'::jsonb ORDER BY a.name, p.id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var pid, aid int64
		var name, target, raw string
		var ok bool
		var lr *time.Time
		if rows.Scan(&pid, &aid, &name, &target, &raw, &ok, &lr) != nil {
			continue
		}
		list = append(list, map[string]any{"probe_id": pid, "asset_id": aid, "asset": name, "target": stripQuery(target), "timing": json.RawMessage(raw), "ok": ok, "last_run": lr})
	}
	writeJSON(w, 200, map[string]any{"probes": list})
}

// hostPart bir probe hedefinden (URL, host:port, düz adres) ana bilgisayar kısmını çıkarır.
func hostPart(t string) string {
	t = strings.TrimSpace(t)
	if strings.Contains(t, "://") {
		if u, err := url.Parse(t); err == nil {
			return u.Hostname()
		}
	}
	if h, _, err := net.SplitHostPort(t); err == nil {
		return h
	}
	return strings.Trim(t, "[]")
}

// GET /api/perf/conns - SSH ile okunan bağlantılardan çıkan bağımlılık önerileri ve bilinmeyen hedefler.
func (s *Server) perfConns(w http.ResponseWriter, r *http.Request, a Auth) {
	ctx := r.Context()
	// IP -> asset (yalnızca benzersiz eşleşmeler kullanılır)
	owners := map[string]map[int64]bool{}
	add := func(ip string, id int64) {
		if net.ParseIP(ip) == nil {
			return
		}
		if owners[ip] == nil {
			owners[ip] = map[int64]bool{}
		}
		owners[ip][id] = true
	}
	names := map[int64]string{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM assets WHERE tenant_id = $1`, a.TenantID); err == nil {
		for rows.Next() {
			var id int64
			var n string
			if rows.Scan(&id, &n) == nil {
				names[id] = n
			}
		}
		rows.Close()
	}
	if rows, err := s.DB.QueryContext(ctx, `SELECT asset_id, ip FROM host_addrs WHERE tenant_id = $1`, a.TenantID); err == nil {
		for rows.Next() {
			var id int64
			var ip string
			if rows.Scan(&id, &ip) == nil {
				add(ip, id)
			}
		}
		rows.Close()
	}
	if rows, err := s.DB.QueryContext(ctx, `SELECT asset_id, target FROM probes WHERE tenant_id = $1 AND type <> 'http'`, a.TenantID); err == nil {
		for rows.Next() {
			var id int64
			var t string
			if rows.Scan(&id, &t) == nil {
				add(hostPart(t), id)
			}
		}
		rows.Close()
	}
	resolve := func(ip string) (int64, bool) {
		if m := owners[ip]; len(m) == 1 {
			for id := range m {
				return id, true
			}
		}
		return 0, false
	}
	existing := map[[2]int64]bool{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT source_asset_id, target_asset_id FROM topology_edges WHERE tenant_id = $1 AND kind = 'depends_on'`, a.TenantID); err == nil {
		for rows.Next() {
			var x, y int64
			if rows.Scan(&x, &y) == nil {
				existing[[2]int64{x, y}] = true
			}
		}
		rows.Close()
	}
	type link struct {
		Source, Target int64
		Port           int
		Conns          int
		RTT            float64
		HasRTT         bool
	}
	links := map[[3]int64]*link{}
	type unk struct {
		Asset string
		Peer  string
		Port  int
		Conns int
		RTT   *float64
	}
	var unknown []unk
	hosts := map[int64]map[string]any{}
	rows, err := s.DB.QueryContext(ctx, `SELECT asset_id, direction, peer_ip, port, conns, rtt_ms, updated_at FROM host_conns WHERE tenant_id = $1
		 UNION ALL SELECT asset_id, direction, peer_ip, port, conns, NULL::double precision, updated_at FROM agent_host_conns WHERE tenant_id = $1 AND updated_at > now() - interval '30 minutes'`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	for rows.Next() {
		var aid int64
		var dir, peer string
		var port, n int
		var rtt *float64
		var upd time.Time
		if rows.Scan(&aid, &dir, &peer, &port, &n, &rtt, &upd) != nil {
			continue
		}
		h := hosts[aid]
		if h == nil {
			h = map[string]any{"asset_id": aid, "asset": names[aid], "out": 0, "in": 0, "updated_at": upd}
			hosts[aid] = h
		}
		h[dir] = h[dir].(int) + n
		other, ok := resolve(peer)
		if !ok || other == aid {
			if dir == "out" && !ok {
				unknown = append(unknown, unk{names[aid], peer, port, n, rtt})
			}
			continue
		}
		src, tgt := aid, other
		if dir == "in" {
			src, tgt = other, aid
		}
		k := [3]int64{src, tgt, int64(port)}
		l := links[k]
		if l == nil {
			l = &link{Source: src, Target: tgt, Port: port}
			links[k] = l
		}
		if n > l.Conns {
			l.Conns = n
		}
		if rtt != nil && (dir == "out" || !l.HasRTT) {
			l.RTT, l.HasRTT = *rtt, true
		}
	}
	linkList := []map[string]any{}
	for _, l := range links {
		it := map[string]any{"source_id": l.Source, "source": names[l.Source], "target_id": l.Target, "target": names[l.Target],
			"port": l.Port, "conns": l.Conns, "edge_exists": existing[[2]int64{l.Source, l.Target}]}
		if l.HasRTT {
			it["rtt_ms"] = l.RTT
		}
		linkList = append(linkList, it)
	}
	sort.Slice(linkList, func(i, j int) bool {
		if linkList[i]["conns"].(int) != linkList[j]["conns"].(int) {
			return linkList[i]["conns"].(int) > linkList[j]["conns"].(int)
		}
		return linkList[i]["source"].(string)+linkList[i]["target"].(string) < linkList[j]["source"].(string)+linkList[j]["target"].(string)
	})
	sort.Slice(unknown, func(i, j int) bool { return unknown[i].Conns > unknown[j].Conns })
	if len(unknown) > 100 {
		unknown = unknown[:100]
	}
	unkList := []map[string]any{}
	for _, u := range unknown {
		it := map[string]any{"asset": u.Asset, "peer": u.Peer, "port": u.Port, "conns": u.Conns}
		if u.RTT != nil {
			it["rtt_ms"] = *u.RTT
		}
		unkList = append(unkList, it)
	}
	hostList := []map[string]any{}
	for _, h := range hosts {
		hostList = append(hostList, h)
	}
	sort.Slice(hostList, func(i, j int) bool { return hostList[i]["asset"].(string) < hostList[j]["asset"].(string) })
	writeJSON(w, 200, map[string]any{"hosts": hostList, "links": linkList, "unknown": unkList})
}

func atoiDefault(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}

// netProbeTypes Performans → DNS / Ağ servisleri / Yol sekmelerinde listelenen probe türleridir.
var netProbeTypes = []string{"dns", "tls", "tcp", "icmp", "udp", "ntp", "trace"}

type netStats struct {
	N      int      `json:"n"`
	AvgMS  *float64 `json:"avg_ms"`
	P95MS  *float64 `json:"p95_ms"`
	MaxMS  *float64 `json:"max_ms"`
	UpPct  *float64 `json:"up_pct"`
	PrevMS *float64 `json:"prev_avg_ms"` // önceki eşit uzunluktaki dönemin ortalaması (eğilim için)
}

// GET /api/perf/network?window=1h|24h|7d - DNS, TLS, TCP, ICMP, UDP, NTP ve yol izleme probe'larının son durumu ve dönem istatistikleri.
// Ölçümler asset düzeyinde saklanır; bir asset'te birden çok probe varsa istatistikler ortaktır ("shared").
func (s *Server) perfNetwork(w http.ResponseWriter, r *http.Request, a Auth) {
	win := map[string]string{"1h": "1 hour", "24h": "24 hours", "7d": "7 days"}[r.URL.Query().Get("window")]
	if win == "" {
		win = "24 hours"
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT p.id, p.asset_id, a.name, p.type, p.target, p.options, p.last_ok, p.last_run, p.last_error, p.last_detail
		   FROM probes p JOIN assets a ON a.id = p.asset_id
		  WHERE p.tenant_id = $1 AND p.enabled AND p.type = ANY($2) ORDER BY a.name, p.id`, a.TenantID, pq.Array(netProbeTypes))
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	type item struct {
		m  map[string]any
		id int64
	}
	var items []item
	assetIDs := []int64{}
	perAsset := map[int64]int{}
	for rows.Next() {
		var pid, aid int64
		var name, typ, target string
		var optsRaw, detRaw []byte
		var lastOK *bool
		var lastRun *time.Time
		var lastErr string
		if rows.Scan(&pid, &aid, &name, &typ, &target, &optsRaw, &lastOK, &lastRun, &lastErr, &detRaw) != nil {
			continue
		}
		var po probe.Options
		json.Unmarshal(optsRaw, &po)
		var det []map[string]string
		json.Unmarshal(detRaw, &det)
		if det == nil {
			det = []map[string]string{}
		}
		m := map[string]any{"probe_id": pid, "asset_id": aid, "asset": name, "type": typ, "target": stripQuery(target), "last_ok": lastOK, "last_run": lastRun, "last_error": lastErr, "detail": det}
		switch typ {
		case "dns":
			m["server"], m["record"] = po.Server, strings.ToUpper(po.Record)
			if po.Record == "" {
				m["record"] = "A"
			}
		case "trace":
			m["dscp"] = po.DSCP
		}
		if perAsset[aid] == 0 {
			assetIDs = append(assetIDs, aid)
		}
		perAsset[aid]++
		items = append(items, item{m, aid})
	}
	rows.Close()

	stats := map[int64]netStats{}
	if len(assetIDs) > 0 {
		q := `SELECT asset_id,
		        count(*) FILTER (WHERE name = 'probe.up'),
		        avg(value) FILTER (WHERE name = 'probe.latency_ms'),
		        percentile_cont(0.95) WITHIN GROUP (ORDER BY value) FILTER (WHERE name = 'probe.latency_ms'),
		        max(value) FILTER (WHERE name = 'probe.latency_ms'),
		        avg(value) FILTER (WHERE name = 'probe.up')
		   FROM metrics WHERE tenant_id = $1 AND asset_id = ANY($2) AND name IN ('probe.up','probe.latency_ms') AND time > now() - $3::interval GROUP BY asset_id`
		if mr, err := s.DB.QueryContext(r.Context(), q, a.TenantID, pq.Array(assetIDs), win); err == nil {
			for mr.Next() {
				var id int64
				var st netStats
				var upAvg *float64
				if mr.Scan(&id, &st.N, &st.AvgMS, &st.P95MS, &st.MaxMS, &upAvg) == nil {
					if upAvg != nil {
						v := *upAvg * 100
						st.UpPct = &v
					}
					stats[id] = st
				}
			}
			mr.Close()
		}
		pq2 := `SELECT asset_id, avg(value) FROM metrics WHERE tenant_id = $1 AND asset_id = ANY($2) AND name = 'probe.latency_ms'
		         AND time <= now() - $3::interval AND time > now() - ($3::interval * 2) GROUP BY asset_id`
		if mr, err := s.DB.QueryContext(r.Context(), pq2, a.TenantID, pq.Array(assetIDs), win); err == nil {
			for mr.Next() {
				var id int64
				var v float64
				if mr.Scan(&id, &v) == nil {
					st := stats[id]
					st.PrevMS = &v
					stats[id] = st
				}
			}
			mr.Close()
		}
	}
	last := map[int64]map[string]float64{}
	if len(assetIDs) > 0 {
		lq := `SELECT asset_id, name, value FROM asset_latest
		        WHERE tenant_id = $1 AND asset_id = ANY($2) AND time > now() - interval '1 day'
		          AND name IN ('probe.cert_days_left','probe.ntp_offset_ms','net.path.hops','net.path.rtt_ms','net.path.loss_pct','net.path.changed','net.path.dscp_changed_ttl')`
		if mr, err := s.DB.QueryContext(r.Context(), lq, a.TenantID, pq.Array(assetIDs)); err == nil {
			for mr.Next() {
				var id int64
				var n string
				var v float64
				if mr.Scan(&id, &n, &v) == nil {
					if last[id] == nil {
						last[id] = map[string]float64{}
					}
					last[id][n] = v
				}
			}
			mr.Close()
		}
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		it.m["stats"] = stats[it.id]
		it.m["shared"] = perAsset[it.id] > 1
		it.m["last"] = last[it.id]
		out = append(out, it.m)
	}
	writeJSON(w, 200, map[string]any{"probes": out, "window": r.URL.Query().Get("window")})
}
