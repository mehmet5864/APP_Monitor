package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type point struct {
	Name  string
	Value float64
	At    time.Time
}

func insertMetrics(ctx context.Context, tx *sql.Tx, tenant, asset int64, pts []point) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO metrics (time, tenant_id, asset_id, name, value) VALUES ($1,$2,$3,$4,$5)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range pts {
		if _, err := stmt.ExecContext(ctx, p.At, tenant, asset, p.Name, p.Value); err != nil {
			return err
		}
	}
	return nil
}

func validMetric(name string, v float64) bool {
	return name != "" && len(name) <= 128 && !math.IsNaN(v) && !math.IsInf(v, 0)
}

// POST /api/ingest - hafif agent'ın gönderdiği heartbeat + metrikler.
func (s *Server) ingest(w http.ResponseWriter, r *http.Request, a Auth) {
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
	}
	if !decode(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Asset.Name)
	if name == "" || len(name) > 200 {
		fail(w, 400, "asset.name zorunlu ve en fazla 200 karakter olmalı")
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
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if created {
		s.audit(r.Context(), a.TenantID, a.Label, "asset.auto_register", name)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"asset_id": id, "accepted": len(pts)})
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
