// Package app APP_MON sunucusunun HTTP API'sini, kimlik doğrulamasını ve arka plan işlerini içerir.
package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"appmon/internal/browser"
	"appmon/internal/cdp"
)

type Server struct {
	DB            *sql.DB
	Mode          string // "saas" veya "onprem"
	BlockPrivate  bool   // probe'ların özel ağlara erişimini engeller (SaaS'ta açık)
	RetentionDays int
	Static        fs.FS

	Browser        *browser.Manager // gömülü tarayıcı kaydı (nil ise özellik kapalı)
	BrowserOptions cdp.Options
	drafts         draftStore
}

type Auth struct {
	TenantID int64
	Role     string
	Label    string
}

type handler func(http.ResponseWriter, *http.Request, Auth)

func HashKey(k string) string {
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:])
}

func NewKey() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return "apm_" + hex.EncodeToString(b)
}

func (s *Server) authenticate(r *http.Request) (Auth, bool) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			key = strings.TrimSpace(h[7:])
		}
	}
	if key == "" {
		return Auth{}, false
	}
	var a Auth
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT tenant_id, role, label FROM api_keys WHERE key_hash = $1`, HashKey(key)).Scan(&a.TenantID, &a.Role, &a.Label)
	return a, err == nil
}

// guard kimlik doğrular. role "admin" ise yalnızca yönetici anahtarları, "ingest" ise yönetici ve agent anahtarları geçer.
func (s *Server) guard(role string, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "geçersiz veya eksik API anahtarı")
			return
		}
		if role == "admin" && a.Role != "admin" {
			fail(w, http.StatusForbidden, "bu işlem için yönetici yetkisi gerekir")
			return
		}
		maxBody := int64(1 << 20)
		if role == "ingest" {
			maxBody = 8 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		h(w, r, a)
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.DB.PingContext(r.Context()); err != nil {
			fail(w, http.StatusServiceUnavailable, "veritabanı yanıt vermiyor")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok", "mode": s.Mode})
	})

	// Toplama uçları (agent anahtarı yeterli)
	mux.HandleFunc("POST /api/ingest", s.guard("ingest", s.ingest))
	mux.HandleFunc("POST /v1/metrics", s.guard("ingest", s.otlpMetrics)) // OTLP/HTTP (JSON) - agent'sız .NET/Java/Python/Go

	// Yönetim uçları
	mux.HandleFunc("GET /api/overview", s.guard("admin", s.overview))
	mux.HandleFunc("GET /api/assets", s.guard("admin", s.listAssets))
	mux.HandleFunc("POST /api/assets", s.guard("admin", s.createAsset))
	mux.HandleFunc("DELETE /api/assets/{id}", s.guard("admin", s.deleteAsset))
	mux.HandleFunc("PATCH /api/assets/{id}", s.guard("admin", s.patchAsset))
	mux.HandleFunc("GET /api/dashboard", s.guard("admin", s.dashboard))
	mux.HandleFunc("GET /api/settings", s.guard("admin", s.settingsInfo))
	mux.HandleFunc("GET /api/assets/{id}/metrics", s.guard("admin", s.assetMetrics))
	mux.HandleFunc("GET /api/assets/{id}/impact", s.guard("admin", s.assetImpact))
	mux.HandleFunc("GET /api/topology", s.guard("admin", s.getTopology))
	mux.HandleFunc("GET /api/topology/stats", s.guard("admin", s.topologyStats))
	mux.HandleFunc("POST /api/topology/edges", s.guard("admin", s.createEdge))
	mux.HandleFunc("PUT /api/topology/edges/{id}", s.guard("admin", s.updateEdge))
	mux.HandleFunc("DELETE /api/topology/edges/{id}", s.guard("admin", s.deleteEdge))
	mux.HandleFunc("GET /api/probes", s.guard("admin", s.listProbes))
	mux.HandleFunc("POST /api/probes", s.guard("admin", s.createProbe))
	mux.HandleFunc("DELETE /api/probes/{id}", s.guard("admin", s.deleteProbe))
	mux.HandleFunc("GET /api/scenarios", s.guard("admin", s.listScenarios))
	mux.HandleFunc("POST /api/scenarios", s.guard("admin", s.createScenario))
	mux.HandleFunc("POST /api/scenarios/test", s.guard("admin", s.testScenario))
	mux.HandleFunc("DELETE /api/scenarios/{id}", s.guard("admin", s.deleteScenario))
	mux.HandleFunc("POST /api/scenarios/{id}/run", s.guard("admin", s.runScenarioNow))
	mux.HandleFunc("POST /api/scenarios/{id}/retry-login", s.guard("admin", s.retryLogin))
	mux.HandleFunc("POST /api/scenarios/{id}/ack-changes", s.guard("admin", s.ackScenarioChanges))
	mux.HandleFunc("GET /api/scenarios/{id}/runs", s.guard("admin", s.listScenarioRuns))
	mux.HandleFunc("POST /api/recorder", s.guard("admin", s.recorderStart))
	mux.HandleFunc("POST /api/recorder/analyze-login", s.guard("admin", s.analyzeLogin))
	mux.HandleFunc("GET /api/recorder/{id}", s.guard("admin", s.recorderState))
	mux.HandleFunc("GET /api/recorder/{id}/frame", s.guard("admin", s.recorderFrame))
	mux.HandleFunc("POST /api/recorder/{id}/input", s.guard("admin", s.recorderInput))
	mux.HandleFunc("POST /api/recorder/{id}/stop", s.guard("admin", s.recorderStop))
	mux.HandleFunc("GET /api/scenarios/{id}/runs/{run}/shots/{n}", s.guard("admin", s.scenarioShot))
	mux.HandleFunc("GET /api/alert-rules", s.guard("admin", s.listRules))
	mux.HandleFunc("POST /api/alert-rules", s.guard("admin", s.createRule))
	mux.HandleFunc("PUT /api/alert-rules/{id}", s.guard("admin", s.updateRule))
	mux.HandleFunc("DELETE /api/alert-rules/{id}", s.guard("admin", s.deleteRule))
	mux.HandleFunc("GET /api/alerts", s.guard("admin", s.listAlerts))
	mux.HandleFunc("GET /api/license", s.guard("admin", s.getLicense))
	mux.HandleFunc("PUT /api/license", s.guard("admin", s.putLicense))
	mux.HandleFunc("GET /api/keys", s.guard("admin", s.listKeys))
	mux.HandleFunc("POST /api/keys", s.guard("admin", s.createKey))
	mux.HandleFunc("DELETE /api/keys/{id}", s.guard("admin", s.deleteKey))
	mux.HandleFunc("GET /api/audit", s.guard("admin", s.listAudit))

	mux.Handle("/", http.FileServerFS(s.Static))
	return secure(mux)
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self' data: blob:; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("yanıt yazılamadı: %v", err)
	}
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "geçersiz JSON gövdesi: "+err.Error())
		return false
	}
	return true
}

func (s *Server) audit(ctx context.Context, tenant int64, actor, action, detail string) {
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO audit_log (tenant_id, actor, action, detail) VALUES ($1, $2, $3, $4)`, tenant, actor, action, detail); err != nil {
		log.Printf("denetim kaydı yazılamadı: %v", err)
	}
}

// Bootstrap ilk çalıştırmada varsayılan kiracıyı, lisansı, yönetici anahtarını ve örnek alarm kurallarını oluşturur.
func (s *Server) Bootstrap(ctx context.Context, adminKey string, assetLimit int) (int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var tenant int64
	if _, err := tx.ExecContext(ctx, `INSERT INTO tenants (name) VALUES ('default') ON CONFLICT (name) DO NOTHING`); err != nil {
		return 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&tenant); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO licenses (tenant_id, mode, asset_limit) VALUES ($1, $2, $3) ON CONFLICT (tenant_id) DO NOTHING`,
		tenant, s.Mode, assetLimit); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO api_keys (tenant_id, label, role, key_hash) VALUES ($1, 'bootstrap-admin', 'admin', $2) ON CONFLICT (key_hash) DO NOTHING`,
		tenant, HashKey(adminKey)); err != nil {
		return 0, err
	}
	var rules int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM alert_rules WHERE tenant_id = $1`, tenant).Scan(&rules); err != nil {
		return 0, err
	}
	if rules == 0 {
		seed := []struct {
			name, metric, cmp string
			thr               float64
			win               int
			sev               string
		}{
			{"Yüksek CPU kullanımı", "host.cpu_pct", ">", 90, 120, "warning"},
			{"Yüksek bellek kullanımı", "host.mem_pct", ">", 90, 120, "warning"},
			{"Disk dolmak üzere", "host.disk_pct", ">", 90, 300, "critical"},
			{"Servis erişilemiyor", "probe.up", "<", 1, 30, "critical"},
			{"Yavaş yanıt", "probe.latency_ms", ">", 1000, 120, "warning"},
			{"Senaryo başarısız", "scenario.up", "<", 1, 60, "critical"},
			{"Yavaş senaryo", "scenario.duration_ms", ">", 5000, 120, "warning"},
		}
		for _, r := range seed {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
				tenant, r.name, r.metric, r.cmp, r.thr, r.win, r.sev); err != nil {
				return 0, err
			}
		}
	}
	return tenant, tx.Commit()
}
