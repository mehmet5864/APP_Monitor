package app

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

type point struct {
	Name  string
	Value float64
	At    time.Time
}

// insertMetrics ölçümleri tek ifadede (unnest) toplu yazar ve asset_latest tablosundaki son değerleri günceller.
// Satır satır INSERT yerine istek başına birkaç gidiş-dönüş yapılır (500 ajan için ingest yükünü ciddi azaltır).
func insertMetrics(ctx context.Context, tx *sql.Tx, tenant, asset int64, pts []point) error {
	latest := map[string]point{}
	for start := 0; start < len(pts); start += 1000 {
		end := start + 1000
		if end > len(pts) {
			end = len(pts)
		}
		chunk := pts[start:end]
		ts := make([]float64, len(chunk))
		names := make([]string, len(chunk))
		vals := make([]float64, len(chunk))
		for i, p := range chunk {
			ts[i] = float64(p.At.UnixNano()) / 1e9
			names[i], vals[i] = p.Name, p.Value
			if cur, ok := latest[p.Name]; !ok || !p.At.Before(cur.At) {
				latest[p.Name] = p
			}
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO metrics (time, tenant_id, asset_id, name, value)
			 SELECT to_timestamp(u.t), $1, $2, u.n, u.v FROM unnest($3::float8[], $4::text[], $5::float8[]) AS u(t, n, v)`,
			tenant, asset, pq.Array(ts), pq.Array(names), pq.Array(vals)); err != nil {
			return err
		}
	}
	if len(latest) == 0 {
		return nil
	}
	ts := make([]float64, 0, len(latest))
	names := make([]string, 0, len(latest))
	vals := make([]float64, 0, len(latest))
	for n, p := range latest {
		ts, names, vals = append(ts, float64(p.At.UnixNano())/1e9), append(names, n), append(vals, p.Value)
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO asset_latest (asset_id, name, tenant_id, value, time)
		 SELECT $1, u.n, $2, u.v, to_timestamp(u.t) FROM unnest($3::float8[], $4::text[], $5::float8[]) AS u(t, n, v)
		 ON CONFLICT (asset_id, name) DO UPDATE SET value = EXCLUDED.value, time = EXCLUDED.time, tenant_id = EXCLUDED.tenant_id
		 WHERE asset_latest.time <= EXCLUDED.time`,
		asset, tenant, pq.Array(ts), pq.Array(names), pq.Array(vals))
	return err
}

func validMetric(name string, v float64) bool {
	return name != "" && len(name) <= 128 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// POST /api/ingest - agent'ın gönderdiği heartbeat + metrikler.
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, a Auth) {
	release, ok := s.acquireIngest(r.Context())
	if !ok {
		// sunucu yoğun: ajan rastgele gecikmeyle yeniden dener (yeniden başlatma sonrası ani yükü yayar)
		w.Header().Set("Retry-After", strconv.Itoa(5+rand.Intn(20)))
		fail(w, http.StatusServiceUnavailable, "sunucu yoğun, daha sonra yeniden deneyin")
		return
	}
	defer release()
	var req struct {
		Asset struct {
			Name     string            `json:"name"`
			Kind     string            `json:"kind"`
			Language string            `json:"language"`
			Labels   map[string]string `json:"labels"`
		} `json:"asset"`
		Metrics []struct {
			Name  string     `json:"name"`
			Value float64    `json:"value"`
			Time  *time.Time `json:"time"`
		} `json:"metrics"`
		Apps      *AgentApps      `json:"apps"`
		Access    []AgentAccess   `json:"access"`
		Conns     *AgentConns     `json:"conns"`
		DBs       []AgentDB       `json:"dbstats"`
		Inventory *AgentInventory `json:"inventory"`
	}
	if r.Header.Get("Content-Encoding") == "gzip" { // ajan 1.2.0+ gövdeyi sıkıştırır
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			fail(w, 400, "geçersiz gzip gövdesi")
			return
		}
		defer zr.Close()
		r.Body = http.MaxBytesReader(w, zr, 16<<20) // açılmış boyut sınırı (gzip bombasına karşı)
	}
	if !decode(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Asset.Name)
	if name == "" || len(name) > 200 {
		fail(w, 400, "asset.name zorunlu ve en fazla 200 karakter olmalı")
		return
	}
	if ok, wait := s.ingestAllow(a.TenantID, name); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		fail(w, http.StatusTooManyRequests, "bu asset için çok sık gönderim yapılıyor; yavaşlayın")
		return
	}
	kind := req.Asset.Kind
	if kind == "" {
		kind = "host"
	}
	if kind != "host" && kind != "service" && kind != "database" && kind != "url" {
		fail(w, 400, "asset.kind host, service, database veya url olmalı")
		return
	}
	if len(req.Metrics) > 500 {
		fail(w, 400, "tek istekte en fazla 500 metrik gönderilebilir")
		return
	}
	now := time.Now()
	pts := make([]point, 0, len(req.Metrics))
	for _, m := range req.Metrics {
		if !validMetric(m.Name, m.Value) {
			continue
		}
		at := now
		if m.Time != nil && m.Time.After(now.Add(-24*time.Hour)) && m.Time.Before(now.Add(5*time.Minute)) {
			at = *m.Time
		}
		pts = append(pts, point{m.Name, m.Value, at})
	}

	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	lang := normalizeLanguage(req.Asset.Language)
	id, created, err := s.ensureAsset(r.Context(), tx, a.TenantID, name, kind, lang, "agent")
	if err != nil {
		if code, ok := licenseStatus(err); ok {
			fail(w, code, err.Error())
			return
		}
		log.Printf("ingest/ensureAsset: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	labels, _ := json.Marshal(req.Asset.Labels)
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE assets SET status = 'up', last_seen = now(),
		        language = CASE WHEN $2 <> 'none' THEN $2 ELSE language END,
		        labels = CASE WHEN $3::jsonb <> 'null'::jsonb THEN $3::jsonb ELSE labels END
		 WHERE id = $1`, id, lang, string(labels)); err != nil {
		log.Printf("ingest/update: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	if err := insertMetrics(r.Context(), tx, a.TenantID, id, pts); err != nil {
		log.Printf("ingest/metrics: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	if req.Apps != nil {
		apps, logs := req.Apps.sanitize()
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO asset_apps (asset_id, tenant_id, apps, logs, updated_at) VALUES ($1, $2, $3::jsonb, $4::jsonb, now())
			 ON CONFLICT (asset_id) DO UPDATE SET apps = EXCLUDED.apps, logs = EXCLUDED.logs, updated_at = now()`,
			id, a.TenantID, apps, logs); err != nil {
			log.Printf("ingest/apps: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	if req.Conns != nil {
		if err := storeConns(r.Context(), tx, a.TenantID, id, req.Conns); err != nil {
			log.Printf("ingest/conns: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	if req.Inventory != nil {
		req.Inventory.sanitize()
		if err := storeInventory(r.Context(), tx, a.TenantID, id, req.Inventory); err != nil {
			log.Printf("ingest/inventory: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
		v := s.inventoryView(req.Inventory.Listeners, req.Inventory.Deps)
		if err := insertMetrics(r.Context(), tx, a.TenantID, id, inventoryMetrics(v, time.Now())); err != nil {
			log.Printf("ingest/inventory metrics: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	if len(req.DBs) > 0 {
		if err := storeDBStats(r.Context(), tx, a.TenantID, id, req.DBs); err != nil {
			log.Printf("ingest/dbstats: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if created {
		s.audit(r.Context(), a.TenantID, a.Label, "asset.auto_register", name)
	}
	if req.Inventory != nil {
		s.surfaceFromInventory(r.Context(), a.TenantID, id, req.Inventory)
	}
	if len(req.Access) > 0 {
		s.mergeAgentAccess(a.TenantID, req.Access)
	}
	resp := map[string]any{"asset_id": id, "accepted": len(pts)}
	if req.Asset.Labels["agent_sha"] != "" {
		var requested, auto bool
		if err := s.DB.QueryRowContext(r.Context(),
			`SELECT a.agent_update_req, t.agent_auto_update FROM assets a JOIN tenants t ON t.id = a.tenant_id WHERE a.id = $1`, id).Scan(&requested, &auto); err == nil {
			if up := s.updateOffer(req.Asset.Labels, requested, auto); up != nil {
				resp["update"] = up
			} else if requested {
				// Ajan sunucudaki dosyayla aynı sürümde (veya dosya yok): istek tamamlandı.
				if b, ok := s.agentBinary(req.Asset.Labels["os"], req.Asset.Labels["arch"]); ok && b.SHA256 == req.Asset.Labels["agent_sha"] {
					_, _ = s.DB.ExecContext(r.Context(), `UPDATE assets SET agent_update_req = false WHERE id = $1`, id)
				}
			}
		}
	}
	if len(req.Asset.Labels) > 0 { // yalnızca etiket bildiren (ajan) istekleri
		if c := s.confOffer(r, id, req.Asset.Labels); c != nil {
			resp["config"] = c
		}
	}
	writeJSON(w, http.StatusAccepted, resp)
}

// OTLP/HTTP JSON gövdesi (yalnızca gauge ve sum metrikleri).
type otlpKV struct {
	Key   string `json:"key"`
	Value struct {
		String *string `json:"stringValue"`
	} `json:"value"`
}
type otlpPoint struct {
	AsDouble *float64        `json:"asDouble"`
	AsInt    json.RawMessage `json:"asInt"`
	Time     json.RawMessage `json:"timeUnixNano"`
}
type otlpData struct {
	DataPoints []otlpPoint `json:"dataPoints"`
}
type otlpRequest struct {
	ResourceMetrics []struct {
		Resource struct {
			Attributes []otlpKV `json:"attributes"`
		} `json:"resource"`
		ScopeMetrics []struct {
			Metrics []struct {
				Name  string    `json:"name"`
				Gauge *otlpData `json:"gauge"`
				Sum   *otlpData `json:"sum"`
			} `json:"metrics"`
		} `json:"scopeMetrics"`
	} `json:"resourceMetrics"`
}

func rawNumber(raw json.RawMessage) (float64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.Trim(string(raw), `"`), 64)
	return f, err == nil
}

// POST /v1/metrics - agent kurmadan OpenTelemetry SDK/Collector ile metrik alımı (OTLP/HTTP, JSON).
func (s *Server) otlpMetrics(w http.ResponseWriter, r *http.Request, a Auth) {
	if strings.Contains(r.Header.Get("Content-Type"), "protobuf") {
		fail(w, http.StatusUnsupportedMediaType,
			"yalnızca OTLP/HTTP JSON desteklenir; OTEL_EXPORTER_OTLP_PROTOCOL=http/json kullanın veya bir OpenTelemetry Collector (otlphttp, encoding: json) ekleyin")
		return
	}
	var req otlpRequest
	if !decode(w, r, &req) {
		return
	}
	now := time.Now()
	for _, rm := range req.ResourceMetrics {
		attrs := map[string]string{}
		for _, kv := range rm.Resource.Attributes {
			if kv.Value.String != nil {
				attrs[kv.Key] = *kv.Value.String
			}
		}
		name := strings.TrimSpace(attrs["service.name"])
		if name == "" {
			name = "unknown_service"
		}
		if len(name) > 200 {
			name = name[:200]
		}
		var pts []point
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				var data *otlpData
				if m.Gauge != nil {
					data = m.Gauge
				} else if m.Sum != nil {
					data = m.Sum
				}
				if data == nil {
					continue
				}
				for _, dp := range data.DataPoints {
					var v float64
					if dp.AsDouble != nil {
						v = *dp.AsDouble
					} else if f, ok := rawNumber(dp.AsInt); ok {
						v = f
					} else {
						continue
					}
					at := now
					if ns, ok := rawNumber(dp.Time); ok && ns > 0 {
						t := time.Unix(0, int64(ns))
						if t.After(now.Add(-24*time.Hour)) && t.Before(now.Add(5*time.Minute)) {
							at = t
						}
					}
					if validMetric(m.Name, v) {
						pts = append(pts, point{m.Name, v, at})
					}
				}
			}
		}
		if len(pts) > 5000 {
			fail(w, 400, "tek istekte en fazla 5000 veri noktası gönderilebilir")
			return
		}
		lang := normalizeLanguage(attrs["telemetry.sdk.language"])

		tx, err := s.DB.BeginTx(r.Context(), nil)
		if err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		id, created, err := s.ensureAsset(r.Context(), tx, a.TenantID, name, "service", lang, "agentless")
		if err != nil {
			tx.Rollback()
			if code, ok := licenseStatus(err); ok {
				fail(w, code, err.Error())
				return
			}
			log.Printf("otlp/ensureAsset: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
		_, err = tx.ExecContext(r.Context(),
			`UPDATE assets SET status = 'up', last_seen = now(), language = CASE WHEN $2 <> 'none' THEN $2 ELSE language END WHERE id = $1`, id, lang)
		if err == nil {
			err = insertMetrics(r.Context(), tx, a.TenantID, id, pts)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			log.Printf("otlp/store: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
		if created {
			s.audit(r.Context(), a.TenantID, a.Label, "asset.auto_register", name+" (otlp)")
		}
	}
	writeJSON(w, 200, map[string]any{}) // OTLP: boş partialSuccess
}
