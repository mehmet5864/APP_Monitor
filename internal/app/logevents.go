package app

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---- Log–trace ilişkilendirme (v3.10.0) ----
// Uygulamalar OTLP günlükleri (POST /v1/logs) veya basit JSON (POST /api/logs) gönderir. Yalnızca APM_LOG_EVENTS=1 iken ve
// en az APM_LOG_MIN_LEVEL seviyesindeki (varsayılan: uyarı) satırlar saklanır; mesaj kısaltılır ve maskelenir. Asset oluşturulmaz
// (lisansa etkisi yoktur). Satırlar trace_id ile trace ayrıntısına bağlanır.

const (
	logMsgMax     = 300
	logBatchMax   = 5000
	logDefaultRet = 3
	logDefaultRPS = 200
)

var logLevelNames = []string{"debug", "info", "warn", "error", "fatal"}

func logLevelOf(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "trace", "debug", "dbg":
		return 0
	case "info", "information", "notice":
		return 1
	case "warn", "warning":
		return 2
	case "error", "err", "critical", "crit":
		return 3
	case "fatal", "panic", "alert", "emerg", "emergency":
		return 4
	}
	return -1
}

// otlpSeverity OTLP severityNumber (1-24) değerini 0-4 seviyesine çevirir; bilinmiyorsa -1.
func otlpSeverity(n int) int {
	switch {
	case n >= 21:
		return 4
	case n >= 17:
		return 3
	case n >= 13:
		return 2
	case n >= 9:
		return 1
	case n >= 1:
		return 0
	}
	return -1
}

func (s *Server) logMinLevel() int {
	if l := logLevelOf(s.LogMin); l >= 0 {
		return l
	}
	return 2
}
func (s *Server) logRetention() int {
	if s.LogRetentionDays > 0 {
		return s.LogRetentionDays
	}
	return logDefaultRet
}

var (
	reEmail  = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	reBearer = regexp.MustCompile(`(?i)(bearer|basic|token|apikey|api_key|api-key|secret|password|passwd|pwd|authorization)([=: ]+)[^\s,;"']+`)
	reLong   = regexp.MustCompile(`\b[A-Za-z0-9_\-+/]{32,}={0,2}`)
	reDigits = regexp.MustCompile(`\b\d{9,}\b`)
	reTrace  = regexp.MustCompile(`(?i)(?:trace[_-]?id["']?\s*[=:]\s*["']?|traceparent["']?\s*[=:]\s*["']?00-)([0-9a-f]{32})`)
	reSpan   = regexp.MustCompile(`(?i)span[_-]?id["']?\s*[=:]\s*["']?([0-9a-f]{16})`)
)

// maskLog satırdan e-posta, parola/token benzeri değerler ve uzun belirteçleri çıkarır; en fazla logMsgMax karakter bırakır.
func maskLog(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	s = reBearer.ReplaceAllString(s, "$1$2{gizli}")
	s = reEmail.ReplaceAllString(s, "{eposta}")
	s = reLong.ReplaceAllStringFunc(s, func(m string) string {
		if isHexID(strings.ToLower(m), 32) { // trace kimliği korunur
			return m
		}
		return "{belirteç}"
	})
	s = reDigits.ReplaceAllString(s, "{sayı}")
	if r := []rune(s); len(r) > logMsgMax {
		s = string(r[:logMsgMax]) + "…"
	}
	return s
}

// extractTrace önce açık alanlardan, yoksa mesaj metninden trace/span kimliği çıkarır.
func extractTrace(trace, span string, attrs map[string]string, body string) (string, string) {
	trace, span = strings.ToLower(trace), strings.ToLower(span)
	for _, k := range []string{"trace_id", "traceId", "trace.id", "dd.trace_id", "otelTraceID"} {
		if !isHexID(trace, 32) && attrs[k] != "" {
			trace = strings.ToLower(attrs[k])
		}
	}
	for _, k := range []string{"span_id", "spanId", "span.id", "dd.span_id", "otelSpanID"} {
		if !isHexID(span, 16) && attrs[k] != "" {
			span = strings.ToLower(attrs[k])
		}
	}
	if !isHexID(trace, 32) {
		if m := reTrace.FindStringSubmatch(body); m != nil {
			trace = strings.ToLower(m[1])
		}
	}
	if !isHexID(span, 16) {
		if m := reSpan.FindStringSubmatch(body); m != nil {
			span = strings.ToLower(m[1])
		}
	}
	if !isHexID(trace, 32) {
		trace = ""
	}
	if !isHexID(span, 16) {
		span = ""
	}
	return trace, span
}

type logRec struct {
	Time            time.Time
	Service, Host   string
	Level           int
	Message         string
	TraceID, SpanID string
}

// logAllow kiracı başına saniyedeki satır sınırı (varsayılan 200; <0 sınırsız). Aşan satırlar atılır.
func (s *Server) logAllow(tenant int64, n int) int {
	lim := s.LogRate
	if lim < 0 {
		return n
	}
	if lim == 0 {
		lim = logDefaultRPS
	}
	s.logmu.Lock()
	defer s.logmu.Unlock()
	if s.lograte == nil {
		s.lograte = map[int64][2]int64{}
	}
	now := time.Now().Unix()
	e := s.lograte[tenant]
	if e[0] != now {
		e = [2]int64{now, 0}
	}
	ok := int64(lim) - e[1]
	if ok < 0 {
		ok = 0
	}
	if int64(n) < ok {
		ok = int64(n)
	}
	e[1] += ok
	s.lograte[tenant] = e
	return int(ok)
}

// storeLogs süzülmüş satırları yazar; saklanan ve reddedilen sayıyı döndürür.
func (s *Server) storeLogs(r *http.Request, tenant int64, recs []logRec) (stored, rejected int, err error) {
	min := s.logMinLevel()
	keep := recs[:0:0]
	for _, x := range recs {
		if x.Level >= min {
			keep = append(keep, x)
		}
	}
	if n := s.logAllow(tenant, len(keep)); n < len(keep) {
		rejected = len(keep) - n
		keep = keep[:n]
	}
	if len(keep) == 0 {
		return 0, rejected, nil
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		return 0, 0, err
	}
	for _, x := range keep {
		if _, err = tx.ExecContext(r.Context(),
			`INSERT INTO log_events (tenant_id, time, service, host, level, message, trace_id, span_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			tenant, x.Time, clip(x.Service, 200), clip(x.Host, 200), x.Level, x.Message, x.TraceID, x.SpanID); err != nil {
			tx.Rollback()
			return 0, 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return len(keep), rejected, nil
}

func logTime(ns uint64, now time.Time) time.Time {
	if ns > 0 {
		t := time.Unix(0, int64(ns))
		if t.After(now.Add(-7*24*time.Hour)) && t.Before(now.Add(5*time.Minute)) {
			return t
		}
	}
	return now
}

func (s *Server) readLogBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	var body io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			fail(w, 400, "geçersiz gzip gövdesi")
			return nil, false
		}
		defer zr.Close()
		body = zr
	}
	raw, err := io.ReadAll(io.LimitReader(body, 16<<20))
	if err != nil {
		fail(w, 400, "gövde okunamadı")
		return nil, false
	}
	return raw, true
}

type otlpLogReq struct {
	ResourceLogs []struct {
		Resource struct {
			Attributes []otlpAttr `json:"attributes"`
		} `json:"resource"`
		ScopeLogs []struct {
			LogRecords []struct {
				Time     json.RawMessage `json:"timeUnixNano"`
				Obs      json.RawMessage `json:"observedTimeUnixNano"`
				SevNum   json.RawMessage `json:"severityNumber"`
				SevText  string          `json:"severityText"`
				Body     otlpAny         `json:"body"`
				Attrs    []otlpAttr      `json:"attributes"`
				TraceID  string          `json:"traceId"`
				SpanID   string          `json:"spanId"`
				Reserved json.RawMessage `json:"-"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

func parseLogsJSON(b []byte) ([]pbLogRes, error) {
	var req otlpLogReq
	if err := json.Unmarshal(b, &req); err != nil {
		return nil, err
	}
	var out []pbLogRes
	for _, rl := range req.ResourceLogs {
		res := pbLogRes{Attrs: attrMap(rl.Resource.Attributes)}
		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				sev := 0
				if f, ok := rawNumber(lr.SevNum); ok {
					sev = int(f)
				}
				res.Logs = append(res.Logs, pbLog{TimeNs: nsOf(lr.Time), ObsNs: nsOf(lr.Obs), Severity: sev, SeverityText: lr.SevText,
					Body: lr.Body.text(), TraceID: lr.TraceID, SpanID: lr.SpanID, Attrs: attrMap(lr.Attrs)})
			}
		}
		out = append(out, res)
	}
	return out, nil
}

// POST /v1/logs - OTLP/HTTP günlük alımı (JSON/protobuf, isteğe bağlı gzip).
func (s *Server) otlpLogs(w http.ResponseWriter, r *http.Request, a Auth) {
	raw, ok := s.readLogBody(w, r)
	if !ok {
		return
	}
	proto := strings.Contains(r.Header.Get("Content-Type"), "protobuf")
	var res []pbLogRes
	var err error
	if proto {
		res, err = parseLogsPB(raw)
	} else {
		res, err = parseLogsJSON(raw)
	}
	if err != nil {
		fail(w, 400, "geçersiz OTLP günlük gövdesi: "+err.Error())
		return
	}
	total := 0
	var recs []logRec
	now := time.Now()
	for _, rs := range res {
		svc := strings.TrimSpace(rs.Attrs["service.name"])
		if svc == "" {
			svc = "unknown_service"
		}
		host := rs.Attrs["host.name"]
		for _, lg := range rs.Logs {
			total++
			if total > logBatchMax {
				fail(w, 400, "tek istekte en fazla 5000 günlük kaydı gönderilebilir")
				return
			}
			lvl := otlpSeverity(lg.Severity)
			if lvl < 0 {
				lvl = logLevelOf(lg.SeverityText)
			}
			if lvl < 0 {
				lvl = 1
			}
			tr, sp := extractTrace(lg.TraceID, lg.SpanID, lg.Attrs, lg.Body)
			t := lg.TimeNs
			if t == 0 {
				t = lg.ObsNs
			}
			recs = append(recs, logRec{Time: logTime(t, now), Service: svc, Host: host, Level: lvl, Message: maskLog(lg.Body), TraceID: tr, SpanID: sp})
		}
	}
	s.finishLogs(w, r, a, recs, total, proto)
}

func (s *Server) finishLogs(w http.ResponseWriter, r *http.Request, a Auth, recs []logRec, total int, proto bool) {
	if !s.LogOn {
		// OTLP istemcileri yeniden denemesin diye 200 döner, ama kısmi başarısızlığı bildirir.
		writeJSON(w, 200, map[string]any{"partialSuccess": map[string]any{"rejectedLogRecords": total, "errorMessage": "günlük saklama kapalı (sunucuda APM_LOG_EVENTS=1 verin)"}})
		return
	}
	stored, rejected, err := s.storeLogs(r, a.TenantID, recs)
	if err != nil {
		log.Printf("logs/store: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	if proto {
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(200)
		return
	}
	out := map[string]any{"stored": stored}
	if rejected > 0 {
		out["partialSuccess"] = map[string]any{"rejectedLogRecords": rejected, "errorMessage": "saniyedeki günlük sınırı aşıldı (APM_LOG_RATE)"}
	}
	writeJSON(w, 200, out)
}

// POST /api/logs - basit JSON: {"logs":[{"time":"RFC3339","service":"x","host":"h","level":"error","message":"...","trace_id":"..","span_id":".."}]}
func (s *Server) postLogs(w http.ResponseWriter, r *http.Request, a Auth) {
	raw, ok := s.readLogBody(w, r)
	if !ok {
		return
	}
	var in struct {
		Logs []struct {
			Time    string `json:"time"`
			Service string `json:"service"`
			Host    string `json:"host"`
			Level   string `json:"level"`
			Message string `json:"message"`
			TraceID string `json:"trace_id"`
			SpanID  string `json:"span_id"`
		} `json:"logs"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		fail(w, 400, "geçersiz JSON")
		return
	}
	if len(in.Logs) > logBatchMax {
		fail(w, 400, "tek istekte en fazla 5000 günlük kaydı gönderilebilir")
		return
	}
	now := time.Now()
	recs := make([]logRec, 0, len(in.Logs))
	for _, x := range in.Logs {
		lvl := logLevelOf(x.Level)
		if lvl < 0 {
			lvl = 1
		}
		t := now
		if pt, err := time.Parse(time.RFC3339Nano, x.Time); err == nil && pt.After(now.Add(-7*24*time.Hour)) && pt.Before(now.Add(5*time.Minute)) {
			t = pt
		}
		svc := strings.TrimSpace(x.Service)
		if svc == "" {
			svc = "unknown_service"
		}
		tr, sp := extractTrace(x.TraceID, x.SpanID, nil, x.Message)
		recs = append(recs, logRec{Time: t, Service: svc, Host: x.Host, Level: lvl, Message: maskLog(x.Message), TraceID: tr, SpanID: sp})
	}
	s.finishLogs(w, r, a, recs, len(recs), false)
}

type logOut struct {
	Time    time.Time `json:"time"`
	Service string    `json:"service"`
	Host    string    `json:"host,omitempty"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
	TraceID string    `json:"trace_id,omitempty"`
	SpanID  string    `json:"span_id,omitempty"`
}

func levelName(l int) string {
	if l >= 0 && l < len(logLevelNames) {
		return logLevelNames[l]
	}
	return "info"
}

// GET /api/logs?since=1h&service=&level=warn&trace_id=&q=&limit=200
func (s *Server) listLogs(w http.ResponseWriter, r *http.Request, a Auth) {
	q := r.URL.Query()
	win := traceWindow(r)
	lim := 200
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 1000 {
		lim = n
	}
	where, args := []string{"tenant_id = $1", "time > now() - make_interval(secs => $2)"}, []any{a.TenantID, win.Seconds()}
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, strings.Replace(cond, "?", "$"+strconv.Itoa(len(args)), 1))
	}
	if v := q.Get("service"); v != "" {
		add("service = ?", v)
	}
	if v := strings.ToLower(q.Get("trace_id")); v != "" {
		add("trace_id = ?", v)
	}
	if l := logLevelOf(q.Get("level")); l >= 0 {
		add("level >= ?", l)
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		add("message ILIKE ?", "%"+strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(v)+"%")
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT time, service, host, level, message, trace_id, span_id FROM log_events WHERE `+strings.Join(where, " AND ")+` ORDER BY time DESC LIMIT `+strconv.Itoa(lim), args...)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := []logOut{}
	for rows.Next() {
		var x logOut
		var lv int
		if rows.Scan(&x.Time, &x.Service, &x.Host, &lv, &x.Message, &x.TraceID, &x.SpanID) == nil {
			x.Level = levelName(lv)
			out = append(out, x)
		}
	}
	services := []string{}
	if sr, err := s.DB.QueryContext(r.Context(), `SELECT DISTINCT service FROM log_events WHERE tenant_id = $1 AND time > now() - interval '7 days' ORDER BY 1 LIMIT 200`, a.TenantID); err == nil {
		for sr.Next() {
			var sv string
			if sr.Scan(&sv) == nil {
				services = append(services, sv)
			}
		}
		sr.Close()
	}
	writeJSON(w, 200, map[string]any{"logs": out, "services": services, "enabled": s.LogOn, "min_level": levelName(s.logMinLevel()), "retention_days": s.logRetention()})
}

// GET /api/traces/{id}/logs - trace_id ile eşleşen (kesin) ve aynı servislerde zaman yakınlığıyla bulunan (yaklaşık) satırlar.
func (s *Server) traceLogs(w http.ResponseWriter, r *http.Request, a Auth) {
	id := strings.ToLower(r.PathValue("id"))
	if !isHexID(id, 32) {
		fail(w, 400, "geçersiz trace kimliği")
		return
	}
	scan := func(rows interface {
		Next() bool
		Scan(...any) error
		Close() error
	}) []logOut {
		defer rows.Close()
		out := []logOut{}
		for rows.Next() {
			var x logOut
			var lv int
			if rows.Scan(&x.Time, &x.Service, &x.Host, &lv, &x.Message, &x.TraceID, &x.SpanID) == nil {
				x.Level = levelName(lv)
				out = append(out, x)
			}
		}
		return out
	}
	exact := []logOut{}
	if rows, err := s.DB.QueryContext(r.Context(),
		`SELECT time, service, host, level, message, trace_id, span_id FROM log_events WHERE tenant_id = $1 AND trace_id = $2 ORDER BY time LIMIT 500`, a.TenantID, id); err == nil {
		exact = scan(rows)
	}
	near := []logOut{}
	if rows, err := s.DB.QueryContext(r.Context(),
		`WITH t AS (SELECT min(start_time) AS t0, max(start_time + make_interval(secs => dur_us / 1e6)) AS t1, array_agg(DISTINCT service) AS svc FROM spans WHERE tenant_id = $1 AND trace_id = $2)
		 SELECT l.time, l.service, l.host, l.level, l.message, l.trace_id, l.span_id FROM log_events l, t
		  WHERE l.tenant_id = $1 AND l.trace_id = '' AND t.t0 IS NOT NULL AND l.service = ANY(t.svc)
		    AND l.time BETWEEN t.t0 - interval '2 seconds' AND t.t1 + interval '2 seconds' ORDER BY l.time LIMIT 100`, a.TenantID, id); err == nil {
		near = scan(rows)
	}
	writeJSON(w, 200, map[string]any{"exact": exact, "near": near, "enabled": s.LogOn})
}
