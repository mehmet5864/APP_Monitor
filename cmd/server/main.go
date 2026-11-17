// APP_MON sunucusu: API, web arayüzü, agentless probe'lar ve alarm değerlendirmesi tek ikili dosyada.
package main

import (
	"appmon/internal/probe"
	"appmon/internal/update"
	"appmon/internal/vault"
	"context"
	"crypto/ed25519"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"appmon/internal/app"
	"appmon/internal/browser"
	"appmon/internal/cdp"
	"appmon/internal/db"
	"appmon/web"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	mode := env("APM_MODE", "onprem")
	if mode != "saas" && mode != "onprem" {
		log.Fatalf("APM_MODE saas veya onprem olmalı (verilen: %q)", mode)
	}
	dsn := env("APM_DB_DSN", "postgres://appmon:appmon@localhost:5432/appmon?sslmode=disable")
	limit, err := strconv.Atoi(env("APM_ASSET_LIMIT", "25"))
	if err != nil || limit < 0 {
		log.Fatal("APM_ASSET_LIMIT geçerli bir sayı olmalı")
	}
	retention, err := strconv.Atoi(env("APM_RETENTION_DAYS", "14"))
	if err != nil || retention < 1 {
		log.Fatal("APM_RETENTION_DAYS en az 1 olmalı")
	}
	blockDefault := "false"
	if mode == "saas" {
		blockDefault = "true"
	}
	block, _ := strconv.ParseBool(env("APM_BLOCK_PRIVATE_PROBES", blockDefault))

	adminKey := os.Getenv("APM_ADMIN_KEY")
	if adminKey == "" {
		adminKey = app.NewKey()
		log.Printf("APM_ADMIN_KEY tanımlı değil; bu çalıştırma için üretilen yönetici anahtarı: %s", adminKey)
	}

	db.MaxOpen = atoiEnv("APM_DB_MAX_CONNS", 40) // ingest ayrı yuva sınırıyla çalışır; arayüz için boş bağlantı kalır
	database, err := db.Open(dsn)
	if err != nil {
		log.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		log.Fatalf("migration: %v", err)
	}

	if ca := os.Getenv("APM_CA_FILE"); ca != "" {
		if n, err := probe.LoadCAFile(ca); err != nil {
			log.Printf("uyarı: APM_CA_FILE (%s) yüklenemedi: %v", ca, err)
		} else {
			log.Printf("kurum CA dosyası yüklendi: %s (%d sertifika yetkisi eklendi)", ca, n)
		}
	}
	var updPub ed25519.PublicKey
	updBad := false
	if f := os.Getenv("APM_UPDATE_PUBKEY"); f != "" {
		if k, err := update.LoadPublicKey(f); err != nil {
			log.Printf("uyarı: APM_UPDATE_PUBKEY (%s) okunamadı: %v — arayüzden güncelleme kapalı", f, err)
			updBad = true
		} else {
			updPub = k
			log.Printf("güncelleme paketleri için imza doğrulaması açık (%s)", f)
		}
	}
	srv := &app.Server{AdvisoryDir: advisoryDir(), AdvisoryAuto: os.Getenv("APM_ADVISORY_AUTO") == "1", LogOn: os.Getenv("APM_LOG_EVENTS") == "1", LogMin: os.Getenv("APM_LOG_MIN_LEVEL"), LogRetentionDays: atoiEnv("APM_LOG_RETENTION_DAYS", 3), LogRate: atoiEnv("APM_LOG_RATE", 200), ProbeConc: atoiEnv("APM_PROBE_CONCURRENCY", 50), ScenarioConc: atoiEnv("APM_SCENARIO_CONCURRENCY", 10), IngestConc: atoiEnv("APM_INGEST_CONCURRENCY", 16), IngestRate: atoiEnv("APM_INGEST_RATE", 2), OfflineAgentSec: atoiEnv("APM_OFFLINE_AGENT_SEC", 90), OfflineWarmupSec: atoiEnv("APM_OFFLINE_WARMUP_SEC", 120), NotifyGroup: time.Duration(atoiEnv("APM_NOTIFY_GROUP_SEC", 20)) * time.Second, AgentDir: os.Getenv("APM_AGENT_DIR"), UpdateDir: os.Getenv("APM_UPDATE_DIR"), UpdatePub: updPub, UpdatePubBad: updBad, DB: database, Mode: mode, BlockPrivate: block, RetentionDays: retention, MaxTenants: atoiEnv("APM_MAX_TENANTS", 10), Static: web.Static(), PublicURL: strings.TrimRight(os.Getenv("APM_PUBLIC_URL"), "/"),
		SelfTrace: env("APM_SELF_TRACE", "1") != "0", SelfTraceSample: sampleEnv("APM_SELF_TRACE_SAMPLE", 0.2)}
	probe.SSHBin = os.Getenv("APM_SSH_BIN") // boş = PATH'teki ssh
	// Yerleşik kasa: ana anahtar APM_MASTER_KEY, APM_MASTER_KEY_FILE, /etc/appmon/master.key veya veri dizini.
	if v, err := vault.Load(vault.Config{Env: os.Getenv("APM_MASTER_KEY"), File: os.Getenv("APM_MASTER_KEY_FILE"), DataDir: env("APM_DATA_DIR", os.Getenv("APM_UPDATE_DIR"))}); err != nil {
		srv.VaultErr = err.Error()
		log.Printf("kasa kapalı: %v (açmak için APM_MASTER_KEY veya APM_MASTER_KEY_FILE tanımlayın: openssl rand -base64 32)", err)
	} else {
		srv.Vault = v
		if v.Generated {
			log.Printf("kasa: yeni ana anahtar üretildi (%s). Bu dosyayı güvenli bir yere YEDEKLEYİN; kaybolursa kasadaki değerler geri alınamaz.", v.Path)
		} else {
			log.Printf("kasa açık (anahtar kaynağı: %s, parmak izi: %s)", v.Source, v.KeyID)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Tarayıcı kaydı/oynatması: SaaS'ta varsayılan kapalı (tarayıcı özel ağ engelini uygulayamaz), on-premise'te açık.
	browserDefault := "true"
	if mode == "saas" {
		browserDefault = "false"
	}
	if on, _ := strconv.ParseBool(env("APM_BROWSER_ENABLED", browserDefault)); on {
		srv.BrowserOptions = cdp.Options{Path: os.Getenv("APM_CHROME_PATH")}
		if path, err := cdp.FindChrome(srv.BrowserOptions.Path); err != nil {
			log.Printf("uyarı: tarayıcı özellikleri açık ama %v", err)
		} else {
			log.Printf("tarayıcı özellikleri açık (Chromium: %s)", path)
		}
		srv.Browser = browser.NewManager(srv.BrowserOptions)
		go srv.Browser.Reap(ctx)
	}
	if _, err := srv.Bootstrap(ctx, adminKey, limit); err != nil {
		log.Fatalf("başlangıç kurulumu: %v", err)
	}
	srv.CrawlRecover()
	if ta := os.Getenv("APM_SNMP_TRAP_ADDR"); ta != "" {
		comm := env("APM_SNMP_TRAP_COMMUNITY", "")
		if comm == "" {
			log.Printf("uyarı: APM_SNMP_TRAP_ADDR tanımlı ama APM_SNMP_TRAP_COMMUNITY boş — trap alıcısı kapalı")
		} else {
			var tid int64
			if err := database.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&tid); err == nil {
				if err := srv.StartTraps(ctx, ta, comm, tid); err != nil {
					log.Printf("uyarı: trap alıcısı başlatılamadı: %v", err)
				} else {
					srv.TrapsOn = true
				}
			}
		}
	}
	srv.StartAdvisories(ctx)
	if fa := os.Getenv("APM_NETFLOW_ADDR"); fa != "" {
		var tid int64
		if err := database.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&tid); err == nil {
			var allow []*net.IPNet
			for _, c := range strings.Split(os.Getenv("APM_NETFLOW_ALLOW"), ",") {
				if c = strings.TrimSpace(c); c == "" {
					continue
				}
				if !strings.Contains(c, "/") {
					if strings.Contains(c, ":") {
						c += "/128"
					} else {
						c += "/32"
					}
				}
				if _, n, err := net.ParseCIDR(c); err == nil {
					allow = append(allow, n)
				} else {
					log.Printf("uyarı: APM_NETFLOW_ALLOW değeri geçersiz: %q", c)
				}
			}
			if len(allow) == 0 {
				log.Printf("uyarı: APM_NETFLOW_ALLOW boş — her kaynaktan NetFlow paketi kabul edilir; yalnızca ihracatçı cihazlara izin verin")
			}
			cfg := app.FlowConfig{Addr: fa, Allow: allow, Tenant: tid, Bucket: time.Duration(atoiEnv("APM_NETFLOW_BUCKET_SEC", 300)) * time.Second,
				TopN: atoiEnv("APM_NETFLOW_TOPN", 500), Retention: atoiEnv("APM_NETFLOW_RETENTION_DAYS", 7), Sampling: uint32(atoiEnv("APM_NETFLOW_SAMPLING", 1))}
			if err := srv.StartFlows(ctx, cfg); err != nil {
				log.Printf("uyarı: NetFlow toplayıcı başlatılamadı: %v", err)
			}
		}
	}
	if sa := os.Getenv("APM_SYSLOG_ADDR"); sa != "" {
		var allow []*net.IPNet
		for _, c := range strings.Split(os.Getenv("APM_SYSLOG_ALLOW"), ",") {
			if c = strings.TrimSpace(c); c == "" {
				continue
			}
			if !strings.Contains(c, "/") {
				if strings.Contains(c, ":") {
					c += "/128"
				} else {
					c += "/32"
				}
			}
			if _, n, err := net.ParseCIDR(c); err == nil {
				allow = append(allow, n)
			} else {
				log.Printf("uyarı: APM_SYSLOG_ALLOW değeri geçersiz: %q", c)
			}
		}
		rate, _ := strconv.Atoi(os.Getenv("APM_SYSLOG_RATE"))
		if err := srv.StartSyslog(ctx, sa, allow, rate); err != nil {
			log.Printf("uyarı: erişim günlüğü alıcısı başlatılamadı: %v", err)
		}
	}
	srv.StatusTitle = os.Getenv("APM_STATUS_TITLE")
	srv.LDAP = app.LDAPSettings{URL: os.Getenv("APM_LDAP_URL"), DNTemplate: os.Getenv("APM_LDAP_DN_TEMPLATE"), DefaultRole: env("APM_LDAP_DEFAULT_ROLE", "viewer")}
	if srv.LDAP.URL != "" {
		log.Printf("LDAP girişi açık: %s", srv.LDAP.URL)
	}
	srv.InitAccess(ctx)
	app.SetBrowserConcurrency(atoiEnv("APM_BROWSER_CONCURRENCY", 2))
	srv.RunJobs(ctx)

	httpSrv := &http.Server{
		Addr:              env("APM_ADDR", ":8080"),
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("APP_MON dinliyor: %s (mod: %s, asset limiti: %d)", httpSrv.Addr, mode, limit)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutdown)
}

// advisoryDir zafiyet (OSV) kayıtlarının dizinidir: APM_ADVISORY_DIR, yoksa veri dizini (APM_DATA_DIR) veya güncelleme dizininin üstü.
func advisoryDir() string {
	if d := os.Getenv("APM_ADVISORY_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("APM_DATA_DIR"); d != "" {
		return filepath.Join(d, "advisories")
	}
	if d := os.Getenv("APM_UPDATE_DIR"); d != "" {
		return filepath.Join(filepath.Dir(filepath.Clean(d)), "advisories")
	}
	return ""
}

func atoiEnv(k string, d int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return d
}

// sampleEnv 0-1 arası ondalık bir ortam değişkenini okur; geçersizse varsayılanı döndürür.
func sampleEnv(k string, def float64) float64 {
	v, err := strconv.ParseFloat(os.Getenv(k), 64)
	if err != nil || v < 0 || v > 1 {
		return def
	}
	return v
}
