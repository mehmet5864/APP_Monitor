package app

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
)

// GET /api/assets/{id}/hostinfo: SSH/SNMP ile toplanan sunucu verilerinin son değerleri (son 30 dk),
// makinenin IP adresleri, dinleyen portlar ve en yoğun bağlantılar. Disk ayrıntısı /disks ile gelir.
func (s *Server) assetHostInfo(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var name string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT name FROM assets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&name); err != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	vals := map[string]float64{}
	var at sql.NullTime
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT DISTINCT ON (name) name, value, time FROM metrics
		 WHERE tenant_id = $1 AND asset_id = $2 AND time > now() - interval '30 minutes'
		   AND name IN ('host.cpu_pct','host.mem_pct','host.mem_used_gb','host.mem_total_gb','host.swap_pct','host.swap_used_gb','host.swap_total_gb',
		                'host.uptime_sec','host.load1','host.load5','host.load15','host.conns','host.conn_rtt_ms','host.disk_pct')
		 ORDER BY name, time DESC`, a.TenantID, id)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	for rows.Next() {
		var n string
		var v float64
		var t sql.NullTime
		if rows.Scan(&n, &v, &t) == nil {
			vals[n] = v
			if t.Time.After(at.Time) {
				at = t
			}
		}
	}
	rows.Close()

	addrs := []string{}
	if ar, err := s.DB.QueryContext(r.Context(), `SELECT ip FROM host_addrs WHERE tenant_id = $1 AND asset_id = $2 ORDER BY ip`, a.TenantID, id); err == nil {
		for ar.Next() {
			var ip string
			if ar.Scan(&ip) == nil {
				addrs = append(addrs, ip)
			}
		}
		ar.Close()
	}
	ports := []int{}
	if pr, err := s.DB.QueryContext(r.Context(), `SELECT item FROM surface_items WHERE tenant_id = $1 AND asset_id = $2 AND kind = 'port'`, a.TenantID, id); err == nil {
		for pr.Next() {
			var it string
			if pr.Scan(&it) == nil {
				if n, e := strconv.Atoi(it); e == nil {
					ports = append(ports, n)
				}
			}
		}
		pr.Close()
		sort.Ints(ports)
	}
	type conn struct {
		Dir   string   `json:"dir"`
		Peer  string   `json:"peer"`
		Port  int      `json:"port"`
		Conns int      `json:"conns"`
		RTT   *float64 `json:"rtt_ms,omitempty"`
	}
	conns := []conn{}
	if cr, err := s.DB.QueryContext(r.Context(),
		`SELECT direction, peer_ip, port, conns, rtt_ms FROM host_conns WHERE tenant_id = $1 AND asset_id = $2 ORDER BY conns DESC, peer_ip LIMIT 20`, a.TenantID, id); err == nil {
		for cr.Next() {
			var c conn
			var rtt sql.NullFloat64
			if cr.Scan(&c.Dir, &c.Peer, &c.Port, &c.Conns, &rtt) == nil {
				if rtt.Valid {
					v := rtt.Float64
					c.RTT = &v
				}
				conns = append(conns, c)
			}
		}
		cr.Close()
	}
	out := map[string]any{"name": name, "metrics": vals, "addrs": addrs, "ports": ports, "conns": conns}
	if at.Valid {
		out["time"] = at.Time
	}
	writeJSON(w, 200, out)
}

// GET /api/probes/{id}/detail: bir probe'un son ölçüm ayrıntıları, durumu ve asset'in son 24 saatlik
// erişilebilirlik/gecikme özeti. Not: ölçümler asset bazlıdır; aynı asset'in birden çok probe'u varsa özet hepsini kapsar.
func (s *Server) probeDetail(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var assetID int64
	var asset, typ, target, lastErr string
	var interval, timeout, streak int
	var enabled bool
	var lastRun sql.NullTime
	var lastOK sql.NullBool
	var detRaw []byte
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT p.asset_id, a.name, p.type, p.target, p.interval_sec, p.timeout_ms, p.enabled, p.last_run, p.last_ok, p.fail_streak, p.last_error, p.last_detail
		   FROM probes p JOIN assets a ON a.id = p.asset_id WHERE p.id = $1 AND p.tenant_id = $2`, id, a.TenantID).
		Scan(&assetID, &asset, &typ, &target, &interval, &timeout, &enabled, &lastRun, &lastOK, &streak, &lastErr, &detRaw)
	if err != nil {
		fail(w, 404, "probe bulunamadı")
		return
	}
	var detail []map[string]string
	json.Unmarshal(detRaw, &detail)
	if detail == nil {
		detail = []map[string]string{}
	}
	out := map[string]any{"id": id, "asset": asset, "type": typ, "target": maskTarget(typ, target), "interval_sec": interval,
		"timeout_ms": timeout, "enabled": enabled, "fail_streak": streak, "last_error": lastErr, "detail": detail}
	if lastRun.Valid {
		out["last_run"] = lastRun.Time
	}
	if lastOK.Valid {
		out["last_ok"] = lastOK.Bool
	}
	var checks sql.NullInt64
	var upPct, avg, p95, mx sql.NullFloat64
	s.DB.QueryRowContext(r.Context(),
		`SELECT (SELECT count(*) FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND name = 'probe.up' AND time > now() - interval '24 hours'),
		        (SELECT 100 * avg(value) FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND name = 'probe.up' AND time > now() - interval '24 hours'),
		        (SELECT avg(value) FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND name = 'probe.latency_ms' AND time > now() - interval '24 hours'),
		        (SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY value) FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND name = 'probe.latency_ms' AND time > now() - interval '24 hours'),
		        (SELECT max(value) FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND name = 'probe.latency_ms' AND time > now() - interval '24 hours')`,
		a.TenantID, assetID).Scan(&checks, &upPct, &avg, &p95, &mx)
	st := map[string]any{"checks": checks.Int64}
	if upPct.Valid {
		st["up_pct"] = upPct.Float64
	}
	if avg.Valid {
		st["avg_ms"], st["p95_ms"], st["max_ms"] = avg.Float64, p95.Float64, mx.Float64
	}
	out["stats24h"] = st
	// son ölçümün ek metrikleri (probe.* ve host/net/db/snmp)
	extra := map[string]float64{}
	if rows, e := s.DB.QueryContext(r.Context(),
		`SELECT DISTINCT ON (name) name, value FROM metrics
		  WHERE tenant_id = $1 AND asset_id = $2 AND time > now() - interval '30 minutes'
		    AND (name LIKE 'probe.%' OR name LIKE 'net.%' OR name LIKE 'db.%' OR name LIKE 'snmp.%')
		    AND name NOT IN ('probe.up', 'probe.latency_ms')
		  ORDER BY name, time DESC LIMIT 80`, a.TenantID, assetID); e == nil {
		for rows.Next() {
			var n string
			var v float64
			if rows.Scan(&n, &v) == nil {
				extra[n] = v
			}
		}
		rows.Close()
	}
	out["metrics"] = extra
	writeJSON(w, 200, out)
}
