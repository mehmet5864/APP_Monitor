package app

import (
	"appmon/internal/version"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var validKinds = []string{"host", "service", "database", "url", "website", "webservice", "network", "cache", "queue"}

func validKind(k string) bool { return oneOf(k, validKinds...) }

// PATCH /api/assets/{id} - asset türünü değiştirir (topolojide şekli belirler).
func (s *Server) patchAsset(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Kind string `json:"kind"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !validKind(req.Kind) {
		fail(w, 400, "kind geçersiz")
		return
	}
	var name string
	err := s.DB.QueryRowContext(r.Context(), `UPDATE assets SET kind = $1 WHERE id = $2 AND tenant_id = $3 RETURNING name`, req.Kind, id, a.TenantID).Scan(&name)
	if err != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "asset.kind", name+" → "+req.Kind)
	writeJSON(w, 200, map[string]any{"id": id, "kind": req.Kind})
}

// GET /api/dashboard - panodaki grafikler için tek çağrıda özet veri.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request, a Auth) {
	ctx, t := r.Context(), a.TenantID
	out := map[string]any{}

	// 24 saatlik erişilebilirlik (saatlik ortalama probe.up ve scenario.up, %).
	avail := make([]any, 24)
	now := time.Now().Truncate(time.Hour)
	if rows, err := s.DB.QueryContext(ctx,
		`SELECT date_trunc('hour', time) AS h, avg(value) * 100 FROM metrics
		 WHERE tenant_id = $1 AND name IN ('probe.up','scenario.up') AND time > now() - interval '24 hours' GROUP BY 1`, t); err == nil {
		for rows.Next() {
			var h time.Time
			var v float64
			if rows.Scan(&h, &v) == nil {
				if i := 23 - int(now.Sub(h.Truncate(time.Hour)).Hours()); i >= 0 && i < 24 {
					avail[i] = v
				}
			}
		}
		rows.Close()
	}
	out["availability"] = avail
	var avail24 *float64
	s.DB.QueryRowContext(ctx, `SELECT avg(value) * 100 FROM metrics WHERE tenant_id = $1 AND name IN ('probe.up','scenario.up') AND time > now() - interval '24 hours'`, t).Scan(&avail24)
	out["availability_24h"] = avail24
	var latency *float64
	s.DB.QueryRowContext(ctx, `SELECT avg(value) FROM metrics WHERE tenant_id = $1 AND name = 'probe.latency_ms' AND time > now() - interval '1 hour'`, t).Scan(&latency)
	out["avg_latency_ms"] = latency

	// Alarm zaman çizelgesi: son 24 saatte saat başına açılan alarmlar (kritik / diğer).
	crit, other := make([]int, 24), make([]int, 24)
	if rows, err := s.DB.QueryContext(ctx,
		`SELECT date_trunc('hour', al.opened_at), (r.severity = 'critical'), count(*) FROM alerts al JOIN alert_rules r ON r.id = al.rule_id
		 WHERE al.tenant_id = $1 AND al.opened_at > now() - interval '24 hours' GROUP BY 1, 2`, t); err == nil {
		for rows.Next() {
			var h time.Time
			var c bool
			var n int
			if rows.Scan(&h, &c, &n) == nil {
				if i := 23 - int(now.Sub(h.Truncate(time.Hour)).Hours()); i >= 0 && i < 24 {
					if c {
						crit[i] += n
					} else {
						other[i] += n
					}
				}
			}
		}
		rows.Close()
	}
	out["alerts_critical"], out["alerts_other"] = crit, other
	out["alerts_by_severity"] = countBy(ctx, s.DB,
		`SELECT r.severity, count(*) FROM alerts al JOIN alert_rules r ON r.id = al.rule_id WHERE al.tenant_id = $1 AND al.state = 'open' GROUP BY 1`, t)
	out["by_kind"] = countBy(ctx, s.DB, `SELECT kind, count(*) FROM assets WHERE tenant_id = $1 GROUP BY kind`, t)

	// Senaryo sağlık şeritleri: her senaryonun son 30 çalıştırması.
	scs := []map[string]any{}
	if rows, err := s.DB.QueryContext(ctx,
		`SELECT sc.id, sc.name, a.name, sc.kind, sc.interval_sec, sc.last_changed_at, sc.last_run, sc.last_ok, sc.last_error,
		        (SELECT coalesce(json_agg(json_build_object('ok', x.ok, 'ms', round(x.duration_ms::numeric, 0)) ORDER BY x.id), '[]'::json)
		         FROM (SELECT id, ok, duration_ms FROM scenario_runs WHERE scenario_id = sc.id ORDER BY id DESC LIMIT 30) x)
		 FROM scenarios sc JOIN assets a ON a.id = sc.asset_id WHERE sc.tenant_id = $1 ORDER BY a.name, sc.name LIMIT 12`, t); err == nil {
		for rows.Next() {
			var id int64
			var name, asset, kind string
			var interval int
			var changed, lastRun *time.Time
			var lastOK *bool
			var lastErr string
			var runs []byte
			if rows.Scan(&id, &name, &asset, &kind, &interval, &changed, &lastRun, &lastOK, &lastErr, &runs) == nil {
				it := map[string]any{"id": id, "name": name, "asset": asset, "kind": kind, "interval_sec": interval, "runs": json.RawMessage(runs), "last_error": lastErr}
				if changed != nil {
					it["last_changed_at"] = *changed
				}
				if lastRun != nil {
					it["last_run"] = *lastRun
				}
				if lastOK != nil {
					it["last_ok"] = *lastOK
				}
				if changed != nil {
					if ch := s.lastChange(ctx, id); ch != nil {
						it["change"] = ch
					}
				}
				// Küçük resim: görüntüsü olan en son çalıştırmanın son adımı (başarısız çalıştırmada son sağlam görüntü).
				if kind == "browser" {
					var runID int64
					var step int
					var at time.Time
					if s.DB.QueryRowContext(ctx,
						`SELECT sh.run_id, max(sh.step_idx), max(r.at) FROM scenario_shots sh JOIN scenario_runs r ON r.id = sh.run_id
						 WHERE r.scenario_id = $1 GROUP BY sh.run_id ORDER BY sh.run_id DESC LIMIT 1`, id).Scan(&runID, &step, &at) == nil {
						it["shot"] = map[string]any{"run_id": runID, "step": step, "at": at}
					}
				}
				scs = append(scs, it)
			}
		}
		rows.Close()
	}
	out["scenarios"] = scs

	// En yavaş probe hedefleri (son 1 saat ortalaması).
	slow := []map[string]any{}
	if rows, err := s.DB.QueryContext(ctx,
		`SELECT a.name, a.kind, avg(m.value), max(m.value) FROM metrics m JOIN assets a ON a.id = m.asset_id
		 WHERE m.tenant_id = $1 AND m.name = 'probe.latency_ms' AND m.time > now() - interval '1 hour'
		 GROUP BY a.name, a.kind ORDER BY 3 DESC LIMIT 6`, t); err == nil {
		for rows.Next() {
			var name, kind string
			var avg, mx float64
			if rows.Scan(&name, &kind, &avg, &mx) == nil {
				slow = append(slow, map[string]any{"asset": name, "kind": kind, "avg_ms": avg, "max_ms": mx})
			}
		}
		rows.Close()
	}
	out["slowest"] = slow
	writeJSON(w, 200, out)
}

// GET /api/settings - salt okunur sistem bilgisi.
func (s *Server) settingsInfo(w http.ResponseWriter, r *http.Request, a Auth) {
	info := map[string]any{
		"version": version.Version, "mode": s.Mode, "retention_days": s.RetentionDays, "block_private": s.BlockPrivate,
		"browser_enabled": s.Browser != nil, "user": a.Label, "role": a.Role,
	}
	writeJSON(w, 200, info)
}

// topologyStats topoloji kutularında gösterilen asset başına özet istatistikler.
func (s *Server) topologyStats(w http.ResponseWriter, r *http.Request, a Auth) {
	ctx := r.Context()
	out := map[string]map[string]any{}
	get := func(id int64) map[string]any {
		k := strconv.FormatInt(id, 10)
		if out[k] == nil {
			out[k] = map[string]any{}
		}
		return out[k]
	}
	q := func(query string, f func(id int64, name string, v float64, ts time.Time)) {
		rows, err := s.DB.QueryContext(ctx, query, a.TenantID)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var name string
			var v float64
			var ts time.Time
			if rows.Scan(&id, &name, &v, &ts) == nil {
				f(id, name, v, ts)
			}
		}
	}
	q(`SELECT asset_id, 'avail', avg(value) * 100, max(time) FROM metrics
	   WHERE tenant_id = $1 AND name IN ('probe.up','scenario.up') AND time > now() - interval '24 hours' GROUP BY asset_id`,
		func(id int64, _ string, v float64, _ time.Time) { get(id)["avail_24h"] = v })
	q(`SELECT asset_id, 'lat', avg(value), max(time) FROM metrics
	   WHERE tenant_id = $1 AND name IN ('probe.latency_ms','scenario.duration_ms') AND time > now() - interval '1 hour' GROUP BY asset_id`,
		func(id int64, _ string, v float64, _ time.Time) { get(id)["latency_ms"] = v })
	q(`SELECT DISTINCT ON (asset_id, name) asset_id, name, value, time FROM metrics
	   WHERE tenant_id = $1 AND time > now() - interval '10 minutes' AND name IN ('host.cpu_pct','host.mem_pct','host.disk_pct')
	   ORDER BY asset_id, name, time DESC`,
		func(id int64, name string, v float64, _ time.Time) {
			get(id)[strings.TrimSuffix(strings.TrimPrefix(name, "host."), "_pct")] = v
		})
	q(`SELECT asset_id, 'last', 0, max(time) FROM metrics WHERE tenant_id = $1 AND time > now() - interval '24 hours' GROUP BY asset_id`,
		func(id int64, _ string, _ float64, ts time.Time) { get(id)["last_seen"] = ts })
	writeJSON(w, 200, out)
}

// lastChange bir senaryonun en son sayfa değişikliğini (hangi adımda ne eklendi/kayboldu) özetler.
func (s *Server) lastChange(ctx context.Context, scenarioID int64) map[string]any {
	rows, err := s.DB.QueryContext(ctx, `SELECT at, steps FROM scenario_runs WHERE scenario_id = $1 ORDER BY id DESC LIMIT 30`, scenarioID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	for rows.Next() {
		var at time.Time
		var raw []byte
		if rows.Scan(&at, &raw) != nil {
			continue
		}
		var steps []struct {
			Name    string   `json:"name"`
			Added   []string `json:"added"`
			Removed []string `json:"removed"`
		}
		if json.Unmarshal(raw, &steps) != nil {
			continue
		}
		var lines []string
		for _, st := range steps {
			for _, a := range st.Added {
				lines = append(lines, "+ "+a)
			}
			for _, r := range st.Removed {
				lines = append(lines, "− "+r)
			}
		}
		if len(lines) > 0 {
			total := len(lines)
			if len(lines) > 6 {
				lines = lines[:6]
			}
			return map[string]any{"at": at, "items": lines, "total": total}
		}
	}
	return nil
}

// ackScenarioChanges "sayfa değişti" işaretini ve ilgili açık alarmı temizler. Pencere ortalamasının alarmı yeniden
// açmaması için son bir saatin scenario.changes ölçümleri de silinir.
func (s *Server) ackScenarioChanges(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var assetID int64
	var name string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT asset_id, name FROM scenarios WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&assetID, &name); err != nil {
		fail(w, 404, "senaryo bulunamadı")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	for _, q := range []string{
		`UPDATE scenarios SET last_changed_at = NULL WHERE id = $1`,
	} {
		if _, err := tx.ExecContext(r.Context(), q, id); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND name = 'scenario.changes' AND time > now() - interval '1 hour'`, a.TenantID, assetID); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE alerts SET state = 'resolved', resolved_at = now()
		 WHERE tenant_id = $1 AND asset_id = $2 AND state = 'open' AND rule_id IN (SELECT id FROM alert_rules WHERE tenant_id = $1 AND metric = 'scenario.changes')`,
		a.TenantID, assetID); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "scenario.ack_changes", name)
	w.WriteHeader(http.StatusNoContent)
}
