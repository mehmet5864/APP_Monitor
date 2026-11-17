// Package app APP_MON sunucusunun HTTP API'sini, kimlik doğrulamasını ve arka plan işlerini içerir.
package app

import (
	"appmon/internal/siem"
	"appmon/internal/vault"
	"appmon/internal/version"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"appmon/internal/browser"
	"appmon/internal/cdp"
)

type Server struct {
	DB            *sql.DB
	Mode          string // "saas" veya "onprem"
	BlockPrivate  bool   // probe'ların özel ağlara erişimini engeller (SaaS'ta açık)
	RetentionDays int
	MaxTenants    int // 0 = 10
	Static        fs.FS
	TrapsOn       bool      // SNMP trap alıcısı açık mı
	accessOnce    sync.Once // InitAccess
	SyslogOn      bool      // erişim günlüğü (syslog) alıcısı açık mı
	SyslogAddr    string    // dinlenen adres
	sys           *syslogState
	syslogStat    *SyslogStats
	syslogOnce    sync.Once
	slaBackfilled bool
	StatusTitle   string // herkese açık durum sayfası başlığı
	LDAP          LDAPSettings
	AgentDir      string            // ajan ikili dosyalarının klasörü (boş = indirme kapalı)
	UpdateDir     string            // arayüzden güncelleme: hazırlama klasörü (boş = kapalı)
	UpdatePub     ed25519.PublicKey // doluysa yalnızca bu anahtarla imzalı paketler kabul edilir
	UpdatePubBad  bool              // genel anahtar yapılandırıldı ama okunamadı: güncelleme reddedilir
	PublicURL     string            // bildirimlerdeki bağlantı için dış adres (APM_PUBLIC_URL), boş olabilir

	Vault    *vault.Vault // yerleşik kasa (nil ise kapalı; nedeni VaultErr)
	VaultErr string

	Browser        *browser.Manager // gömülü tarayıcı kaydı (nil ise özellik kapalı)
	BrowserOptions cdp.Options
	drafts         draftStore

	// Ölçek ayarları (0 = varsayılan): aynı anda probe / senaryo / ingest sayısı ve bildirim gruplama penceresi
	ProbeConc, ScenarioConc, IngestConc int
	OfflineAgentSec, OfflineWarmupSec   int // ajan çevrimdışı eşiği (sn, 0=90) ve yeniden başlama sonrası ısınma (sn, 0=120, <0=kapalı)
	startedAt                           time.Time
	FlowsOn                             bool // NetFlow/IPFIX toplayıcı açık mı
	flows                               *flowState
	AdvisoryDir                         string // zafiyet (OSV) kayıtlarının dizini (APM_ADVISORY_DIR); eol.json da buradan okunur
	AdvisoryAuto                        bool   // APM_ADVISORY_AUTO=1: OSV arşivlerini günde bir indir
	adv                                 advStore
	LogOn                               bool   // günlük satırı saklama (APM_LOG_EVENTS=1)
	LogMin                              string // en düşük saklanan seviye (debug|info|warn|error|fatal; boş = warn)
	LogRetentionDays, LogRate           int    // saklama günü (0 = 3) ve kiracı başına saniyedeki satır sınırı (0 = 200, <0 sınırsız)
	logmu                               sync.Mutex
	lograte                             map[int64][2]int64
	IngestRate                          int // asset başına saniyedeki ingest isteği (0 = 2, <0 = sınırsız)
	NotifyGroup                         time.Duration
	scale                               scaleState

	SelfTrace       bool    // APP_MON kendi API isteklerini trace olarak kaydeder (APM_SELF_TRACE)
	SelfTraceSample float64 // hızlı ve başarılı isteklerin saklanma oranı (APM_SELF_TRACE_SAMPLE)
	self            selfState
}

type Auth struct {
	TenantID int64
	UserID   int64 // oturum bir kullanıcı hesabına aitse dolu
	Role     string
	Label    string
	Platform bool // kök kiracının yöneticisi: kiracıları ve sunucu geneli ayarları yönetir
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
	var uid sql.NullInt64
	var root bool
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT k.tenant_id, k.role, k.label, k.user_id, t.is_root FROM api_keys k JOIN tenants t ON t.id = k.tenant_id
		  WHERE k.key_hash = $1 AND (k.expires_at IS NULL OR k.expires_at > now()) AND NOT t.disabled`, HashKey(key)).Scan(&a.TenantID, &a.Role, &a.Label, &uid, &root)
	a.UserID = uid.Int64
	a.Platform = root && a.Role == "admin"
	return a, err == nil
}

// Rol seviyeleri. admin: her şey; operator: izleme verilerini değiştirir (asset, probe, senaryo, topoloji, kural);
// viewer: yalnızca okur; agent: yalnızca veri gönderir.
var roleRank = map[string]int{"viewer": 1, "operator": 2, "admin": 3}

// RoleLabel arayüz ve hata iletileri için Türkçe rol adı.
func RoleLabel(r string) string {
	switch r {
	case "admin":
		return "yönetici"
	case "operator":
		return "işletmeci"
	case "viewer":
		return "izleyici"
	case "agent":
		return "agent"
	}
	return r
}

// allowed rolün istenen seviyeye izni olup olmadığını döndürür.
// level: "any" (geçerli her anahtar), "ingest" (admin ve agent), "read" (izleyici ve üstü),
// "write" (işletmeci ve üstü), "admin" (yalnızca yönetici).
func allowed(level, role string) bool {
	switch level {
	case "any":
		return true
	case "ingest":
		return role == "admin" || role == "agent"
	case "read":
		return roleRank[role] >= roleRank["viewer"]
	case "write":
		return roleRank[role] >= roleRank["operator"]
	case "admin":
		return role == "admin"
	}
	return false
}

// guard kimlik doğrular ve rol seviyesini denetler (allowed).
func (s *Server) guard(level string, h handler) http.HandlerFunc { return s.guardMax(level, 0, h) }

// guardMax guard ile aynıdır; istek gövdesi sınırı max bayttır (0 = varsayılan).
func (s *Server) guardMax(level string, max int64, h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "geçersiz veya eksik API anahtarı")
			return
		}
		if level == "platform" {
			if !a.Platform {
				fail(w, http.StatusForbidden, "bu işlem yalnızca platform yöneticisine açıktır")
				return
			}
		} else if !allowed(level, a.Role) {
			need := map[string]string{"read": "izleyici", "write": "işletmeci", "admin": "yönetici", "ingest": "agent"}[level]
			fail(w, http.StatusForbidden, fmt.Sprintf("bu işlem için %s yetkisi gerekir (rolünüz: %s)", need, RoleLabel(a.Role)))
			return
		}
		maxBody := int64(1 << 20)
		if level == "ingest" {
			maxBody = 8 << 20
		}
		if max > 0 {
			maxBody = max
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		if fin := s.selfSpanStart(&w, r, a); fin != nil {
			defer fin()
		}
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
		writeJSON(w, 200, map[string]string{"status": "ok", "mode": s.Mode, "version": version.Version})
	})

	// Toplama uçları (agent anahtarı yeterli)
	mux.HandleFunc("POST /api/ingest", s.guard("ingest", s.ingest))
	mux.HandleFunc("POST /v1/metrics", s.guard("ingest", s.otlpMetrics)) // OTLP/HTTP (JSON) - agent'sız .NET/Java/Python/Go
	mux.HandleFunc("GET /api/flows/summary", s.guard("read", s.flowSummary))
	mux.HandleFunc("POST /v1/logs", s.guard("ingest", s.otlpLogs)) // OTLP/HTTP günlük (JSON/protobuf, gzip)
	mux.HandleFunc("POST /api/logs", s.guard("ingest", s.postLogs))
	mux.HandleFunc("GET /api/logs", s.guard("read", s.listLogs))
	mux.HandleFunc("GET /api/traces/{id}/logs", s.guard("read", s.traceLogs))
	mux.HandleFunc("POST /v1/traces", s.guard("ingest", s.otlpTraces)) // OTLP/HTTP trace (JSON/protobuf, gzip)
	mux.HandleFunc("GET /kurulum/otel-kurulum.sh", s.otelScript("otel-kurulum.sh"))
	mux.HandleFunc("GET /kurulum/otel-kurulum.ps1", s.otelScript("otel-kurulum.ps1"))
	mux.HandleFunc("GET /kurulum/appmon-agent-linux.conf", s.otelScript("appmon-agent-linux.conf")) // ajan örnek yapılandırması (sır içermez)
	mux.HandleFunc("GET /kurulum/appmon-agent-windows.conf", s.otelScript("appmon-agent-windows.conf"))
	mux.HandleFunc("GET /api/traces", s.guard("read", s.listTraces))
	mux.HandleFunc("GET /api/traces/services", s.guard("read", s.traceServices))
	mux.HandleFunc("GET /api/traces/operations", s.guard("read", s.traceOperations))
	mux.HandleFunc("GET /api/traces/map", s.guard("read", s.traceMap))
	mux.HandleFunc("GET /api/traces/{id}", s.guard("read", s.getTrace))

	// Yönetim uçları
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("GET /api/auth/config", s.authConfig)
	mux.HandleFunc("POST /api/logout", s.guard("any", s.logout))
	mux.HandleFunc("POST /api/me/password", s.guard("any", s.changeOwnPassword))
	mux.HandleFunc("GET /api/users", s.guard("admin", s.listUsers))
	mux.HandleFunc("POST /api/users", s.guard("admin", s.createUser))
	mux.HandleFunc("PUT /api/users/{id}", s.guard("admin", s.updateUser))
	mux.HandleFunc("DELETE /api/users/{id}", s.guard("admin", s.deleteUser))
	mux.HandleFunc("GET /api/me", s.guard("any", s.me))
	mux.HandleFunc("GET /api/overview", s.guard("read", s.overview))
	mux.HandleFunc("GET /api/assets", s.guard("read", s.listAssets))
	mux.HandleFunc("POST /api/assets", s.guard("write", s.createAsset))
	mux.HandleFunc("DELETE /api/assets/{id}", s.guard("write", s.deleteAsset))
	mux.HandleFunc("PATCH /api/assets/{id}", s.guard("write", s.patchAsset))
	mux.HandleFunc("GET /api/dashboard", s.guard("read", s.dashboard))
	mux.HandleFunc("GET /api/settings", s.guard("read", s.settingsInfo))
	mux.HandleFunc("GET /api/assets/{id}/metrics", s.guard("read", s.assetMetrics))
	mux.HandleFunc("GET /api/assets/{id}/impact", s.guard("read", s.assetImpact))
	mux.HandleFunc("GET /api/assets/{id}/apps", s.guard("read", s.assetApps))
	mux.HandleFunc("GET /api/topology", s.guard("read", s.getTopology))
	mux.HandleFunc("GET /api/topology/stats", s.guard("read", s.topologyStats))
	mux.HandleFunc("POST /api/topology/edges", s.guard("write", s.createEdge))
	mux.HandleFunc("PUT /api/topology/edges/{id}", s.guard("write", s.updateEdge))
	mux.HandleFunc("DELETE /api/topology/edges/{id}", s.guard("write", s.deleteEdge))
	mux.HandleFunc("GET /api/probes", s.guard("read", s.listProbes))
	mux.HandleFunc("POST /api/probes", s.guard("write", s.createProbe))
	mux.HandleFunc("POST /api/probes/bulk", s.guard("write", s.probesBulk))
	mux.HandleFunc("POST /api/probes/import", s.guardMax("write", 4<<20, s.probesImport))
	mux.HandleFunc("POST /api/scenarios/bulk", s.guard("write", s.scenariosBulk))
	mux.HandleFunc("GET /api/probes/{id}/detail", s.guard("read", s.probeDetail))
	mux.HandleFunc("PUT /api/probes/{id}", s.guard("write", s.updateProbe))
	mux.HandleFunc("DELETE /api/probes/{id}", s.guard("write", s.deleteProbe))
	mux.HandleFunc("GET /api/scenarios/capacity", s.guard("read", s.scenarioCapacity))
	mux.HandleFunc("GET /api/scenarios", s.guard("read", s.listScenarios))
	mux.HandleFunc("POST /api/scenarios", s.guard("write", s.createScenario))
	mux.HandleFunc("POST /api/scenarios/test", s.guard("write", s.testScenario))
	mux.HandleFunc("PUT /api/scenarios/{id}", s.guard("write", s.updateScenario))
	mux.HandleFunc("DELETE /api/scenarios/{id}", s.guard("write", s.deleteScenario))
	mux.HandleFunc("POST /api/scenarios/{id}/run", s.guard("write", s.runScenarioNow))
	mux.HandleFunc("POST /api/scenarios/{id}/retry-login", s.guard("write", s.retryLogin))
	mux.HandleFunc("GET /api/scenarios/{id}/last-change", s.guard("read", s.scenarioLastChange))
	mux.HandleFunc("POST /api/scenarios/{id}/ack-changes", s.guard("write", s.ackScenarioChanges))
	mux.HandleFunc("GET /api/assets/{id}/disks", s.guard("read", s.assetDisks))
	mux.HandleFunc("GET /api/assets/{id}/hostinfo", s.guard("read", s.assetHostInfo))
	mux.HandleFunc("GET /api/vault", s.guard("admin", s.vaultStatus))
	mux.HandleFunc("GET /api/secrets", s.guard("write", s.listSecrets))
	mux.HandleFunc("POST /api/secrets", s.guard("admin", s.createSecret))
	mux.HandleFunc("PUT /api/secrets/{id}", s.guard("admin", s.updateSecret))
	mux.HandleFunc("DELETE /api/secrets/{id}", s.guard("admin", s.deleteSecret))
	mux.HandleFunc("GET /api/scenarios/{id}/ignores", s.guard("read", s.listIgnores))
	mux.HandleFunc("POST /api/scenarios/{id}/ignores", s.guard("write", s.addIgnore))
	mux.HandleFunc("DELETE /api/scenarios/{id}/ignores/{rid}", s.guard("write", s.deleteIgnore))
	mux.HandleFunc("GET /api/scenarios/{id}/runs", s.guard("read", s.listScenarioRuns))
	mux.HandleFunc("POST /api/recorder", s.guard("write", s.recorderStart))
	mux.HandleFunc("POST /api/recorder/analyze-login", s.guard("write", s.analyzeLogin))
	mux.HandleFunc("GET /api/recorder/{id}", s.guard("write", s.recorderState))
	mux.HandleFunc("GET /api/recorder/{id}/frame", s.guard("write", s.recorderFrame))
	mux.HandleFunc("POST /api/recorder/{id}/input", s.guard("write", s.recorderInput))
	mux.HandleFunc("POST /api/recorder/{id}/stop", s.guard("write", s.recorderStop))
	mux.HandleFunc("GET /api/scenarios/{id}/runs/{run}/shots/{n}", s.guard("read", s.scenarioShot))
	mux.HandleFunc("GET /api/alert-rules", s.guard("read", s.listRules))
	mux.HandleFunc("POST /api/alert-rules", s.guard("write", s.createRule))
	mux.HandleFunc("PUT /api/alert-rules/{id}", s.guard("write", s.updateRule))
	mux.HandleFunc("DELETE /api/alert-rules/{id}", s.guard("write", s.deleteRule))
	mux.HandleFunc("GET /api/alerts", s.guard("read", s.listAlerts))
	mux.HandleFunc("GET /api/agent/files", s.guard("read", s.agentFiles))
	mux.HandleFunc("GET /agent/dl/{name}", s.agentDownload)
	mux.HandleFunc("GET /api/agent/updates", s.guard("admin", s.agentUpdates))
	mux.HandleFunc("POST /api/agent/update", s.guard("admin", s.agentUpdateRequest))
	mux.HandleFunc("GET /api/agent/config", s.guard("admin", s.agentConfigList))
	mux.HandleFunc("PUT /api/agent/config", s.guard("admin", s.agentConfigSet))
	mux.HandleFunc("GET /api/agent/summary", s.guard("admin", s.agentSummary))
	mux.HandleFunc("POST /api/agent/rollout", s.guard("admin", s.rolloutStart))
	mux.HandleFunc("GET /api/agent/rollout", s.guard("admin", s.rolloutGet))
	mux.HandleFunc("POST /api/agent/rollout/{action}", s.guard("admin", s.rolloutAction))
	mux.HandleFunc("PUT /api/agent/auto-update", s.guard("admin", s.agentAutoUpdate))
	mux.HandleFunc("POST /api/alerts/clear", s.guard("write", s.clearAlerts))
	mux.HandleFunc("GET /api/evidence/chain", s.guard("admin", s.evidenceChain))
	mux.HandleFunc("GET /api/evidence/public-key", s.guard("admin", s.evidencePublicKey))
	mux.HandleFunc("GET /api/evidence/reports", s.guard("admin", s.listEvidence))
	mux.HandleFunc("POST /api/evidence/reports", s.guard("admin", s.createEvidence))
	mux.HandleFunc("GET /api/evidence/reports/{id}/package", s.guard("admin", s.downloadEvidence))
	mux.HandleFunc("POST /api/evidence/verify", s.guardMax("admin", 32<<20, s.verifyEvidence))
	mux.HandleFunc("GET /api/inventory", s.guard("read", s.inventoryList))
	mux.HandleFunc("GET /api/inventory/{id}/deps", s.guard("read", s.inventoryDeps))
	mux.HandleFunc("POST /api/inventory/advisories/reload", s.guard("admin", s.inventoryReload))
	mux.HandleFunc("GET /api/surface/events", s.guard("read", s.surfaceEvents))
	mux.HandleFunc("GET /api/surface/baseline", s.guard("read", s.surfaceBaseline))
	mux.HandleFunc("POST /api/surface/events/{id}/review", s.guard("write", s.surfaceReview))
	mux.HandleFunc("POST /api/surface/reset", s.guard("write", s.surfaceReset))
	mux.HandleFunc("GET /api/alerts/{id}/rca", s.guard("read", s.alertRCA))
	mux.HandleFunc("POST /api/alerts/{id}/ack", s.guard("write", s.ackAlert))
	mux.HandleFunc("DELETE /api/alerts/{id}/ack", s.guard("write", s.unackAlert))
	mux.HandleFunc("PUT /api/assets/{id}/meta", s.guard("write", s.putAssetMeta))
	mux.HandleFunc("GET /api/maintenance", s.guard("read", s.listMaintenance))
	mux.HandleFunc("POST /api/maintenance", s.guard("write", s.createMaintenance))
	mux.HandleFunc("DELETE /api/maintenance/{id}", s.guard("write", s.deleteMaintenance))
	mux.HandleFunc("GET /api/system/update", s.guard("platform", s.updateInfo))
	mux.HandleFunc("POST /api/system/update", s.guardMax("platform", 420<<20, s.updateUpload))
	mux.HandleFunc("POST /api/system/update/apply", s.guard("platform", s.updateApply))
	mux.HandleFunc("DELETE /api/system/update", s.guard("platform", s.updateCancel))
	mux.HandleFunc("GET /api/monitoring", s.guard("read", s.getMonitoring))
	mux.HandleFunc("PUT /api/monitoring", s.guard("admin", s.setMonitoring))
	mux.HandleFunc("POST /api/tenants/{id}/license-file", s.guard("platform", s.issueLicenseFile))
	mux.HandleFunc("POST /api/license/file", s.guardMax("admin", 64<<10, s.uploadLicenseFile))
	mux.HandleFunc("POST /api/system/update/rollback", s.guard("platform", s.updateRollback))
	mux.HandleFunc("GET /api/siem-targets", s.guard("admin", s.listSIEM))
	mux.HandleFunc("POST /api/siem-targets", s.guard("admin", s.createSIEM))
	mux.HandleFunc("PUT /api/siem-targets/{id}", s.guard("admin", s.updateSIEM))
	mux.HandleFunc("DELETE /api/siem-targets/{id}", s.guard("admin", s.deleteSIEM))
	mux.HandleFunc("POST /api/siem-targets/{id}/test", s.guard("admin", s.testSIEM))
	mux.HandleFunc("GET /api/notification-channels", s.guard("admin", s.listChannels))
	mux.HandleFunc("POST /api/notification-channels", s.guard("admin", s.createChannel))
	mux.HandleFunc("PUT /api/notification-channels/{id}", s.guard("admin", s.updateChannel))
	mux.HandleFunc("DELETE /api/notification-channels/{id}", s.guard("admin", s.deleteChannel))
	mux.HandleFunc("POST /api/notification-channels/{id}/test", s.guard("admin", s.testChannel))
	mux.HandleFunc("GET /api/license", s.guard("read", s.getLicense))
	mux.HandleFunc("GET /api/tenants", s.guard("platform", s.listTenants))
	mux.HandleFunc("POST /api/tenants", s.guard("platform", s.createTenant))
	mux.HandleFunc("PUT /api/tenants/{id}", s.guard("platform", s.updateTenant))
	mux.HandleFunc("DELETE /api/tenants/{id}", s.guard("platform", s.deleteTenant))
	mux.HandleFunc("POST /api/tenants/{id}/admins", s.guard("platform", s.addTenantAdmin))
	mux.HandleFunc("PUT /api/license", s.guard("platform", s.putLicense))
	mux.HandleFunc("GET /api/keys", s.guard("admin", s.listKeys))
	mux.HandleFunc("POST /api/keys", s.guard("admin", s.createKey))
	mux.HandleFunc("DELETE /api/keys/{id}", s.guard("admin", s.deleteKey))
	mux.HandleFunc("GET /api/sla", s.guard("read", s.getSLA))
	mux.HandleFunc("GET /api/sla.csv", s.guard("read", s.getSLACSV))
	mux.HandleFunc("PUT /api/assets/{id}/sla", s.guard("write", s.setSLATarget))
	mux.HandleFunc("POST /api/sla/reset", s.guard("admin", s.resetSLA))
	mux.HandleFunc("GET /api/crawl/wordlists", s.guard("write", s.crawlWordlists))
	mux.HandleFunc("GET /api/crawl", s.guard("write", s.crawlList))
	mux.HandleFunc("POST /api/crawl", s.guard("write", s.crawlCreate))
	mux.HandleFunc("GET /api/crawl/{id}", s.guard("write", s.crawlGet))
	mux.HandleFunc("POST /api/crawl/{id}/approve", s.guard("admin", s.crawlDecide(true)))
	mux.HandleFunc("POST /api/crawl/{id}/reject", s.guard("admin", s.crawlDecide(false)))
	mux.HandleFunc("DELETE /api/crawl/{id}", s.guard("write", s.crawlDelete))
	mux.HandleFunc("POST /api/discovery", s.guard("admin", s.startDiscovery))
	mux.HandleFunc("GET /api/discovery/{id}", s.guard("admin", s.getDiscovery))
	mux.HandleFunc("DELETE /api/discovery/{id}", s.guard("admin", s.cancelDiscovery))
	mux.HandleFunc("GET /api/export", s.guard("admin", s.exportConfig))
	mux.HandleFunc("POST /api/import", s.guardMax("admin", 16<<20, s.importConfig))
	mux.HandleFunc("GET /status/{slug}", func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, s.Static, "status.html") })
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) { http.ServeFileFS(w, r, s.Static, "status.html") })
	mux.HandleFunc("GET /api/public/status", s.publicStatus)
	mux.HandleFunc("GET /api/status-page", s.guard("admin", s.listStatusAssets))
	mux.HandleFunc("PUT /api/assets/{id}/status-page", s.guard("admin", s.setStatusAsset))
	mux.HandleFunc("GET /api/traps", s.guard("read", s.listTraps))
	mux.HandleFunc("GET /api/perf/apps", s.guard("read", s.perfApps))
	mux.HandleFunc("GET /api/perf/endpoints", s.guard("read", s.perfEndpoints))
	mux.HandleFunc("GET /api/perf/queries", s.guard("read", s.perfQueries))
	mux.HandleFunc("GET /api/perf/timing", s.guard("read", s.perfTiming))
	mux.HandleFunc("GET /api/perf/network", s.guard("read", s.perfNetwork))
	mux.HandleFunc("GET /api/perf/conns", s.guard("read", s.perfConns))
	mux.HandleFunc("POST /api/trace", s.guard("write", s.traceNow))
	mux.HandleFunc("GET /api/audit", s.guard("admin", s.listAudit))
	mux.HandleFunc("GET /api/audit/facets", s.guard("admin", s.auditFacets))

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
			"default-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data: blob:; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
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
	if err := s.auditChained(ctx, tenant, actor, action, detail); err != nil {
		log.Printf("denetim kaydı yazılamadı: %v", err)
	}
	s.siemEmit(tenant, siem.Event{Type: "audit", Severity: "info", Actor: actor, Action: action, Detail: detail})
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
	// "default" kiracı kök (platform) kiracıdır (v2.0.0).
	if _, err := tx.ExecContext(ctx, `UPDATE tenants SET is_root = true, slug = COALESCE(slug, 'default') WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM tenants WHERE is_root)`, tenant); err != nil {
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
			{"Servis erişilemiyor", "asset.down", ">", 0, 30, "critical"},
			{"Servis kısmen erişilemiyor", "asset.degraded", ">", 0, 30, "warning"},
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
