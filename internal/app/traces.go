package app

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// ---- OTLP/HTTP trace alımı (POST /v1/traces; JSON ve protobuf, isteğe bağlı gzip) ----

type otlpAny struct {
	String *string         `json:"stringValue"`
	Int    json.RawMessage `json:"intValue"`
	Bool   *bool           `json:"boolValue"`
	Double *float64        `json:"doubleValue"`
}

func (v otlpAny) text() string {
	switch {
	case v.String != nil:
		return *v.String
	case len(v.Int) > 0:
		return strings.Trim(string(v.Int), `"`)
	case v.Bool != nil:
		return strconv.FormatBool(*v.Bool)
	case v.Double != nil:
		return strconv.FormatFloat(*v.Double, 'g', -1, 64)
	}
	return ""
}

type otlpAttr struct {
	Key   string  `json:"key"`
	Value otlpAny `json:"value"`
}

type otlpTraceReq struct {
	ResourceSpans []struct {
		Resource struct {
			Attributes []otlpAttr `json:"attributes"`
		} `json:"resource"`
		ScopeSpans []struct {
			Spans []struct {
				TraceID    string          `json:"traceId"`
				SpanID     string          `json:"spanId"`
				ParentID   string          `json:"parentSpanId"`
				Name       string          `json:"name"`
				Kind       json.RawMessage `json:"kind"`
				Start      json.RawMessage `json:"startTimeUnixNano"`
				End        json.RawMessage `json:"endTimeUnixNano"`
				Attributes []otlpAttr      `json:"attributes"`
				Status     struct {
					Code    json.RawMessage `json:"code"`
					Message string          `json:"message"`
				} `json:"status"`
			} `json:"spans"`
		} `json:"scopeSpans"`
	} `json:"resourceSpans"`
}

func enumInt(raw json.RawMessage, names map[string]int) int {
	s := strings.Trim(string(raw), `"`)
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return names[s]
}

var spanKindNames = map[string]int{"SPAN_KIND_INTERNAL": 1, "SPAN_KIND_SERVER": 2, "SPAN_KIND_CLIENT": 3, "SPAN_KIND_PRODUCER": 4, "SPAN_KIND_CONSUMER": 5}
var statusNames = map[string]int{"STATUS_CODE_OK": 1, "STATUS_CODE_ERROR": 2}

func nsOf(raw json.RawMessage) uint64 {
	n, err := strconv.ParseUint(strings.Trim(string(raw), `"`), 10, 64)
	if err != nil {
		f, ok := rawNumber(raw)
		if !ok || f < 0 {
			return 0
		}
		return uint64(f)
	}
	return n
}

func attrMap(in []otlpAttr) map[string]string {
	m := map[string]string{}
	for _, kv := range in {
		if len(m) >= 40 {
			break
		}
		m[kv.Key] = kv.Value.text()
	}
	return m
}

func parseTraceJSON(b []byte) ([]pbResource, error) {
	var req otlpTraceReq
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, err
	}
	var out []pbResource
	for _, rs := range req.ResourceSpans {
		res := pbResource{Attrs: attrMap(rs.Resource.Attributes)}
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				res.Spans = append(res.Spans, pbSpan{
					TraceID: strings.ToLower(sp.TraceID), SpanID: strings.ToLower(sp.SpanID), ParentID: strings.ToLower(sp.ParentID),
					Name: sp.Name, Kind: enumInt(sp.Kind, spanKindNames),
					StartNs: nsOf(sp.Start), EndNs: nsOf(sp.End),
					Status: enumInt(sp.Status.Code, statusNames), StatusMsg: sp.Status.Message,
					Attrs: attrMap(sp.Attributes),
				})
			}
		}
		out = append(out, res)
	}
	return out, nil
}

func isHexID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return false
	}
	return strings.Trim(s, "0") != ""
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Server) otlpTraces(w http.ResponseWriter, r *http.Request, a Auth) {
	var body io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			fail(w, 400, "geçersiz gzip gövdesi")
			return
		}
		defer zr.Close()
		body = zr
	}
	raw, err := io.ReadAll(io.LimitReader(body, 32<<20))
	if err != nil {
		fail(w, 400, "gövde okunamadı")
		return
	}
	proto := strings.Contains(r.Header.Get("Content-Type"), "protobuf")
	var resources []pbResource
	if proto {
		resources, err = parseTracePB(raw)
	} else {
		resources, err = parseTraceJSON(raw)
	}
	if err != nil {
		fail(w, 400, "geçersiz OTLP trace gövdesi: "+err.Error())
		return
	}
	total := 0
	for _, rs := range resources {
		total += len(rs.Spans)
	}
	if total > 10000 {
		fail(w, 400, "tek istekte en fazla 10000 span gönderilebilir")
		return
	}
	now := time.Now()
	stored := 0
	for _, rs := range resources {
		name := strings.TrimSpace(rs.Attrs["service.name"])
		if name == "" {
			name = "unknown_service"
		}
		name = clip(name, 200)
		lang := normalizeLanguage(rs.Attrs["telemetry.sdk.language"])
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
			log.Printf("otlp/traces/ensureAsset: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
		_, err = tx.ExecContext(r.Context(),
			`UPDATE assets SET status = 'up', last_seen = now(), language = CASE WHEN $2 <> 'none' THEN $2 ELSE language END WHERE id = $1`, id, lang)
		for _, sp := range rs.Spans {
			if err != nil {
				break
			}
			if !isHexID(sp.TraceID, 32) || !isHexID(sp.SpanID, 16) {
				continue
			}
			parent := sp.ParentID
			if !isHexID(parent, 16) {
				parent = ""
			}
			start := now
			if sp.StartNs > 0 {
				t := time.Unix(0, int64(sp.StartNs))
				if t.After(now.Add(-7*24*time.Hour)) && t.Before(now.Add(5*time.Minute)) {
					start = t
				}
			}
			var durUs int64
			if sp.EndNs > sp.StartNs && sp.StartNs > 0 {
				durUs = int64(sp.EndNs-sp.StartNs) / 1000
				if durUs > int64(7*24*time.Hour/time.Microsecond) {
					durUs = int64(7 * 24 * time.Hour / time.Microsecond)
				}
			}
			status := sp.Status
			if status < 0 || status > 2 {
				status = 0
			}
			kind := sp.Kind
			if kind < 0 || kind > 5 {
				kind = 0
			}
			attrs := map[string]string{}
			for k, v := range sp.Attrs {
				attrs[clip(k, 120)] = clip(v, 300)
			}
			ab, _ := json.Marshal(attrs)
			_, err = tx.ExecContext(r.Context(),
				`INSERT INTO spans (tenant_id, trace_id, span_id, parent_id, service, name, kind, start_time, dur_us, status, status_msg, attrs)
				 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT DO NOTHING`,
				a.TenantID, sp.TraceID, sp.SpanID, parent, name, clip(sp.Name, 300), kind, start, durUs, status, clip(sp.StatusMsg, 300), string(ab))
			stored++
		}
		if err == nil {
			err = tx.Commit()
		} else {
			tx.Rollback()
		}
		if err != nil {
			log.Printf("otlp/traces/store: %v", err)
			fail(w, 500, "veritabanı hatası")
			return
		}
		if created {
			s.audit(r.Context(), a.TenantID, a.Label, "asset.auto_register", name+" (otlp trace)")
		}
	}
	if proto {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
		return
	}
	writeJSON(w, 200, map[string]any{})
}

// ---- Sorgu API'leri ----

func traceWindow(r *http.Request) time.Duration {
	switch r.URL.Query().Get("since") {
	case "15m":
		return 15 * time.Minute
	case "6h":
		return 6 * time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	}
	return time.Hour
}

type traceSummary struct {
	TraceID  string    `json:"trace_id"`
	Start    time.Time `json:"start"`
	DurUs    int64     `json:"dur_us"`
	Spans    int       `json:"spans"`
	Errors   int       `json:"errors"`
	Services []string  `json:"services"`
	RootName string    `json:"root_name"`
	RootSvc  string    `json:"root_service"`
}

func (s *Server) listTraces(w http.ResponseWriter, r *http.Request, a Auth) {
	q := r.URL.Query()
	since := time.Now().Add(-traceWindow(r))
	args := []any{a.TenantID, since}
	inner := ""
	if svc := strings.TrimSpace(q.Get("service")); svc != "" {
		args = append(args, svc)
		inner += ` AND trace_id IN (SELECT trace_id FROM spans WHERE tenant_id = $1 AND start_time >= $2 AND service = $` + strconv.Itoa(len(args)) + `)`
	}
	if t := strings.TrimSpace(q.Get("q")); t != "" {
		args = append(args, strings.ToLower(clip(t, 100)))
		n := strconv.Itoa(len(args))
		inner += ` AND trace_id IN (SELECT trace_id FROM spans WHERE tenant_id = $1 AND start_time >= $2 AND (trace_id LIKE $` + n + ` || '%' OR lower(name) LIKE '%' || $` + n + ` || '%'))`
	}
	having := ""
	if q.Get("status") == "error" {
		having += ` AND count(*) FILTER (WHERE status = 2) > 0`
	}
	if ms, err := strconv.Atoi(q.Get("min_ms")); err == nil && ms > 0 {
		args = append(args, int64(ms)*1000)
		having += ` AND (extract(epoch FROM max(start_time + dur_us * interval '1 microsecond') - min(start_time)) * 1000000) >= $` + strconv.Itoa(len(args))
	}
	order := `min(start_time) DESC`
	if q.Get("sort") == "slow" {
		order = `max(start_time + dur_us * interval '1 microsecond') - min(start_time) DESC`
	}
	query := `SELECT trace_id, min(start_time),
	            (extract(epoch FROM max(start_time + dur_us * interval '1 microsecond') - min(start_time)) * 1000000)::bigint,
	            count(*), count(*) FILTER (WHERE status = 2), array_agg(DISTINCT service)
	          FROM spans WHERE tenant_id = $1 AND start_time >= $2` + inner + `
	          GROUP BY trace_id HAVING true` + having + ` ORDER BY ` + order + ` LIMIT 100`
	rows, err := s.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		log.Printf("traces/list: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := []traceSummary{}
	ids := []string{}
	for rows.Next() {
		var t traceSummary
		var svcs pq.StringArray
		if err := rows.Scan(&t.TraceID, &t.Start, &t.DurUs, &t.Spans, &t.Errors, &svcs); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		t.Services = []string(svcs)
		sort.Strings(t.Services)
		out = append(out, t)
		ids = append(ids, t.TraceID)
	}
	if len(ids) > 0 {
		rr, err := s.DB.QueryContext(r.Context(),
			`SELECT DISTINCT ON (trace_id) trace_id, service, name FROM spans
			  WHERE tenant_id = $1 AND trace_id = ANY($2) ORDER BY trace_id, (parent_id = '') DESC, start_time`, a.TenantID, pq.Array(ids))
		if err == nil {
			defer rr.Close()
			root := map[string][2]string{}
			for rr.Next() {
				var id, svc, nm string
				if rr.Scan(&id, &svc, &nm) == nil {
					root[id] = [2]string{svc, nm}
				}
			}
			for i := range out {
				out[i].RootSvc, out[i].RootName = root[out[i].TraceID][0], root[out[i].TraceID][1]
			}
		}
	}
	writeJSON(w, 200, map[string]any{"traces": out, "since": q.Get("since")})
}

type spanOut struct {
	SpanID   string            `json:"span_id"`
	ParentID string            `json:"parent_id"`
	Service  string            `json:"service"`
	Name     string            `json:"name"`
	Kind     int               `json:"kind"`
	OffsetUs int64             `json:"offset_us"`
	DurUs    int64             `json:"dur_us"`
	Status   int               `json:"status"`
	Message  string            `json:"status_msg"`
	Attrs    map[string]string `json:"attrs"`
}

func (s *Server) getTrace(w http.ResponseWriter, r *http.Request, a Auth) {
	id := strings.ToLower(r.PathValue("id"))
	if !isHexID(id, 32) {
		fail(w, 400, "geçersiz trace kimliği")
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT span_id, parent_id, service, name, kind, start_time, dur_us, status, status_msg, attrs
		   FROM spans WHERE tenant_id = $1 AND trace_id = $2 ORDER BY start_time, span_id LIMIT 5000`, a.TenantID, id)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	spans := []spanOut{}
	var t0 time.Time
	var end int64
	for rows.Next() {
		var sp spanOut
		var st time.Time
		var ab []byte
		if err := rows.Scan(&sp.SpanID, &sp.ParentID, &sp.Service, &sp.Name, &sp.Kind, &st, &sp.DurUs, &sp.Status, &sp.Message, &ab); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		if t0.IsZero() {
			t0 = st
		}
		sp.OffsetUs = st.Sub(t0).Microseconds()
		if e := sp.OffsetUs + sp.DurUs; e > end {
			end = e
		}
		json.Unmarshal(ab, &sp.Attrs)
		spans = append(spans, sp)
	}
	if len(spans) == 0 {
		fail(w, 404, "trace bulunamadı")
		return
	}
	writeJSON(w, 200, map[string]any{"trace_id": id, "start": t0, "dur_us": end, "spans": spans})
}

// entryExpr: servisin dışarıdan aldığı çağrıyı temsil eden span'lar (sunucu/tüketici ya da kök).
const entryExpr = `(kind IN (2,5) OR parent_id = '')`

func (s *Server) traceServices(w http.ResponseWriter, r *http.Request, a Auth) {
	win := traceWindow(r)
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT service, count(*), count(*) FILTER (WHERE status = 2),
		        percentile_cont(0.5) WITHIN GROUP (ORDER BY dur_us),
		        percentile_cont(0.95) WITHIN GROUP (ORDER BY dur_us),
		        percentile_cont(0.99) WITHIN GROUP (ORDER BY dur_us),
		        max(start_time)
		   FROM spans WHERE tenant_id = $1 AND start_time >= $2 AND `+entryExpr+`
		  GROUP BY service ORDER BY count(*) DESC LIMIT 200`, a.TenantID, time.Now().Add(-win))
	if err != nil {
		log.Printf("traces/services: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	type svc struct {
		Service string    `json:"service"`
		Calls   int       `json:"calls"`
		Errors  int       `json:"errors"`
		RatePM  float64   `json:"rate_per_min"`
		P50Us   float64   `json:"p50_us"`
		P95Us   float64   `json:"p95_us"`
		P99Us   float64   `json:"p99_us"`
		Last    time.Time `json:"last"`
	}
	out := []svc{}
	for rows.Next() {
		var v svc
		if err := rows.Scan(&v.Service, &v.Calls, &v.Errors, &v.P50Us, &v.P95Us, &v.P99Us, &v.Last); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		v.RatePM = float64(v.Calls) / win.Minutes()
		out = append(out, v)
	}
	writeJSON(w, 200, map[string]any{"services": out})
}

func (s *Server) traceOperations(w http.ResponseWriter, r *http.Request, a Auth) {
	svc := strings.TrimSpace(r.URL.Query().Get("service"))
	if svc == "" {
		fail(w, 400, "service parametresi gerekli")
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT name, count(*), count(*) FILTER (WHERE status = 2),
		        percentile_cont(0.5) WITHIN GROUP (ORDER BY dur_us),
		        percentile_cont(0.95) WITHIN GROUP (ORDER BY dur_us)
		   FROM spans WHERE tenant_id = $1 AND service = $2 AND start_time >= $3
		  GROUP BY name ORDER BY count(*) DESC LIMIT 50`, a.TenantID, svc, time.Now().Add(-traceWindow(r)))
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	type op struct {
		Name   string  `json:"name"`
		Calls  int     `json:"calls"`
		Errors int     `json:"errors"`
		P50Us  float64 `json:"p50_us"`
		P95Us  float64 `json:"p95_us"`
	}
	out := []op{}
	for rows.Next() {
		var o op
		if err := rows.Scan(&o.Name, &o.Calls, &o.Errors, &o.P50Us, &o.P95Us); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		out = append(out, o)
	}
	writeJSON(w, 200, map[string]any{"service": svc, "operations": out})
}

// traceMap servisler arası çağrı grafiğini döndürür: iki servis arasında ebeveyn-çocuk span geçişi
// bir bağımlılıktır; çocuğu olmayan istemci span'ları (veritabanı, dış servis) "dış" düğümlerdir.
func (s *Server) traceMap(w http.ResponseWriter, r *http.Request, a Auth) {
	since := time.Now().Add(-traceWindow(r))
	type edge struct {
		From   string  `json:"from"`
		To     string  `json:"to"`
		Calls  int     `json:"calls"`
		Errors int     `json:"errors"`
		P95Us  float64 `json:"p95_us"`
		Ext    bool    `json:"external"`
	}
	edges := []edge{}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT p.service, c.service, count(*), count(*) FILTER (WHERE c.status = 2),
		        percentile_cont(0.95) WITHIN GROUP (ORDER BY c.dur_us)
		   FROM spans c JOIN spans p ON p.tenant_id = c.tenant_id AND p.trace_id = c.trace_id AND p.span_id = c.parent_id
		  WHERE c.tenant_id = $1 AND c.start_time >= $2 AND c.service <> p.service
		  GROUP BY p.service, c.service ORDER BY count(*) DESC LIMIT 300`, a.TenantID, since)
	if err != nil {
		log.Printf("traces/map: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	for rows.Next() {
		var e edge
		if rows.Scan(&e.From, &e.To, &e.Calls, &e.Errors, &e.P95Us) == nil {
			edges = append(edges, e)
		}
	}
	rows.Close()
	rows2, err := s.DB.QueryContext(r.Context(),
		`SELECT c.service,
		        COALESCE(NULLIF(c.attrs->>'peer.service',''), NULLIF(c.attrs->>'db.system',''), NULLIF(c.attrs->>'messaging.system',''),
		                 NULLIF(c.attrs->>'server.address',''), NULLIF(c.attrs->>'net.peer.name',''), NULLIF(c.attrs->>'http.host','')) AS peer,
		        count(*), count(*) FILTER (WHERE c.status = 2), percentile_cont(0.95) WITHIN GROUP (ORDER BY c.dur_us)
		   FROM spans c
		  WHERE c.tenant_id = $1 AND c.start_time >= $2 AND c.kind = 3
		    AND NOT EXISTS (SELECT 1 FROM spans k WHERE k.tenant_id = c.tenant_id AND k.trace_id = c.trace_id AND k.parent_id = c.span_id)
		  GROUP BY 1, 2 HAVING COALESCE(NULLIF(c.attrs->>'peer.service',''), NULLIF(c.attrs->>'db.system',''), NULLIF(c.attrs->>'messaging.system',''),
		                 NULLIF(c.attrs->>'server.address',''), NULLIF(c.attrs->>'net.peer.name',''), NULLIF(c.attrs->>'http.host','')) IS NOT NULL
		  ORDER BY count(*) DESC LIMIT 100`, a.TenantID, since)
	if err == nil {
		for rows2.Next() {
			var e edge
			if rows2.Scan(&e.From, &e.To, &e.Calls, &e.Errors, &e.P95Us) == nil {
				e.Ext = true
				edges = append(edges, e)
			}
		}
		rows2.Close()
	}
	conns, err := s.connMap(r.Context(), a.TenantID)
	if err != nil {
		log.Printf("traces/map/conns: %v", err)
		conns = []connEdge{}
	}
	writeJSON(w, 200, map[string]any{"edges": edges, "conn_edges": conns})
}

func (s *Server) purgeSpans(ctx context.Context) {
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM net_edges WHERE updated_at < now() - interval '2 days'`); err != nil {
		log.Printf("eski bağlantı temizliği: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM spans m USING licenses l WHERE l.tenant_id = m.tenant_id AND m.start_time < now() - make_interval(days => LEAST(COALESCE(NULLIF(l.retention_days, 0), $1), 14))`, s.RetentionDays); err != nil {
		log.Printf("eski span temizliği: %v", err)
	}
}
