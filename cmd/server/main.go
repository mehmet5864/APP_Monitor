// APP_MON sunucusu: API, web arayüzü, agentless probe'lar ve alarm değerlendirmesi tek ikili dosyada.
package main

import (
	"appmon/internal/probe"
	"appmon/internal/update"
	"context"
	"crypto/ed25519"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	srv := &app.Server{UpdateDir: os.Getenv("APM_UPDATE_DIR"), UpdatePub: updPub, UpdatePubBad: updBad, DB: database, Mode: mode, BlockPrivate: block, RetentionDays: retention, Static: web.Static(), PublicURL: strings.TrimRight(os.Getenv("APM_PUBLIC_URL"), "/")}
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
