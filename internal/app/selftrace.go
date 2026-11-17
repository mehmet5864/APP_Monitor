package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	mrand "math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"appmon/internal/version"
)

// Kendi kendini izleme (v3.1.0): APP_MON, kimliği doğrulanmış API isteklerini sunucu span'ı olarak
// kendi trace deposuna yazar (servis adı "appmon-server"). Böylece yavaş veya hatalı API çağrıları
// Dağıtık izleme sayfasında, gelen istekte W3C `traceparent` başlığı varsa çağıran servisin trace'ine bağlı görünür.
//
// Span'lar bellekte toplanıp toplu yazılır; kuyruk doluysa span atılır (istek yavaşlamaz).
// Hatalar (5xx), yavaş istekler (>=1 sn) ve çağıranın örneklediği (sampled) istekler her zaman,
// diğerleri APM_SELF_TRACE_SAMPLE oranında saklanır.

const selfService = "appmon-server"

type selfSpan struct {
	tenant               int64
	traceID, spanID, par string
	name                 string
	start                time.Time
	durUs                int64
	status               int
	msg                  string
	attrs                map[string]string
}

type selfState struct {
	once sync.Once
	ch   chan selfSpan
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) {
	if w.code == 0 {
		w.code = c
	}
	w.ResponseWriter.WriteHeader(c)
}
func (w *statusWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.ResponseWriter.Write(b)
}
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// parseTraceparent "00-<32 hex>-<16 hex>-<2 hex>" biçimini ayrıştırır.
func parseTraceparent(v string) (trace, span string, sampled, ok bool) {
	p := strings.Split(strings.TrimSpace(v), "-")
	if len(p) < 4 || len(p[0]) != 2 || p[0] == "ff" || !isHexID(p[1], 32) || !isHexID(p[2], 16) || len(p[3]) != 2 {
		return "", "", false, false
	}
	fl, err := strconv.ParseUint(p[3], 16, 8)
	if err != nil {
		return "", "", false, false
	}
	return strings.ToLower(p[1]), strings.ToLower(p[2]), fl&1 == 1, true
}

// selfSkip gürültü üreten veya kendine bakan uç noktaları izlemeden çıkarır.
func selfSkip(path string) bool {
	return path == "/v1/traces" || path == "/v1/logs" || path == "/api/logs" || path == "/v1/metrics" || path == "/api/ingest" || strings.HasPrefix(path, "/api/traces")
}

// selfSpanStart isteği izlemeye başlar; dönen işlev istek bitince çağrılmalıdır (nil = izleme yok).
func (s *Server) selfSpanStart(w *http.ResponseWriter, r *http.Request, a Auth) func() {
	if !s.SelfTrace || a.TenantID == 0 || selfSkip(r.URL.Path) {
		return nil
	}
	sw := &statusWriter{ResponseWriter: *w}
	*w = sw
	start := time.Now()
	trace, parent, upSampled, hasUp := parseTraceparent(r.Header.Get("traceparent"))
	if !hasUp {
		trace, parent = randHex(16), ""
	}
	id := randHex(8)
	return func() {
		code := sw.code
		if code == 0 {
			code = 200
		}
		dur := time.Since(start)
		keep := code >= 500 || dur >= time.Second || (hasUp && upSampled) || mrand.Float64() < s.SelfTraceSample
		if !keep {
			return
		}
		route := r.Pattern
		if route == "" {
			route = r.Method + " " + r.URL.Path
		}
		sp := selfSpan{tenant: a.TenantID, traceID: trace, spanID: id, par: parent, name: route, start: start, durUs: dur.Microseconds(),
			attrs: map[string]string{
				"http.request.method":       r.Method,
				"url.path":                  clip(r.URL.Path, 200),
				"http.response.status_code": strconv.Itoa(code),
				"enduser.role":              a.Role,
				"service.version":           version.Version,
			}}
		if code >= 500 {
			sp.status, sp.msg = 2, "HTTP "+strconv.Itoa(code)
		}
		st := s.selfInit()
		select {
		case st.ch <- sp:
		default: // kuyruk dolu: isteği yavaşlatma, span'ı at
		}
	}
}

func (s *Server) selfInit() *selfState {
	s.self.once.Do(func() {
		s.self.ch = make(chan selfSpan, 4096)
		go func() {
			t := time.NewTicker(2 * time.Second)
			for range t.C {
				s.selfFlush(context.Background())
			}
		}()
	})
	return &s.self
}

// selfFlush kuyruktaki span'ları tek seferde veritabanına yazar.
func (s *Server) selfFlush(ctx context.Context) {
	st := s.selfInit()
	var batch []selfSpan
	for len(batch) < 500 {
		select {
		case sp := <-st.ch:
			batch = append(batch, sp)
			continue
		default:
		}
		break
	}
	if len(batch) == 0 {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("self-trace: %v", err)
		return
	}
	for _, sp := range batch {
		ab, _ := json.Marshal(sp.attrs)
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO spans (tenant_id, trace_id, span_id, parent_id, service, name, kind, start_time, dur_us, status, status_msg, attrs)
			 SELECT $1,$2,$3,$4,$5,$6,2,$7,$8,$9,$10,$11 WHERE EXISTS (SELECT 1 FROM tenants WHERE id = $1) ON CONFLICT DO NOTHING`,
			sp.tenant, sp.traceID, sp.spanID, sp.par, selfService, sp.name, sp.start, sp.durUs, sp.status, sp.msg, string(ab)); err != nil {
			break
		}
	}
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	if err != nil {
		log.Printf("self-trace yazılamadı: %v", err)
	}
}
