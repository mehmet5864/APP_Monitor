// APP_MON agent: tek ikili dosya, yalnızca standart kütüphane (Linux ve Windows).
// Ana makine metriklerini (CPU, bellek, takas, disk, ağ, süreç sayısı, açılış süresi) ve izlenen süreçleri
// periyodik olarak sunucuya gönderir. Sunucuya ulaşılamazsa metrikleri bellekte tamponlar ve bağlantı gelince
// zaman damgalarıyla birlikte gönderir. Kurulum: `appmon-agent -install -server URL -key KEY`.
package main

import (
	"appmon/internal/agentver"
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Version ajanın kendi sürümüdür; sunucu sürümünden bağımsızdır.
var Version = agentver.Agent // ajanın kendi sürümü (internal/agentver); sunucu sürümünden bağımsızdır

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

type metric struct {
	Name  string     `json:"name"`
	Value float64    `json:"value"`
	Time  *time.Time `json:"time,omitempty"`
}

// settings tüm ayarları toplar (bayrak > yapılandırma dosyası > ortam değişkeni > varsayılan).
type settings struct {
	Server, Key, Name, Watch, CAFile, Log string
	Interval                              time.Duration
	Insecure                              bool
	NoApps, Root, NoUpdate, NoWeb         bool            // NoApps: uygulama keşfini kapat; Root: Linux servisini root çalıştır (günlükler ve tüm süreç portları için)
	NoConns, NoOTLP                       bool            // NoConns: bağlantı tabanlı servis haritası verisi gönderme; NoOTLP: yerel OTLP alıcısını kapat
	OTLPListen                            string          // yerel OTLP alıcı adresi (varsayılan 127.0.0.1:4318)
	PGRemote                              bool            // PGDSN uzaktan (Veri ayarı) verildi
	PGDSN                                 string          // APM_PG_DSN: PostgreSQL sorgu istatistikleri için bağlantı (yalnızca ajanda kalır)
	NoRemote                              bool            // APM_REMOTE_CONFIG=off: uzaktan veri ayarını kabul etme
	Send                                  map[string]bool // SEND_* satırları (nil: hepsi açık)
}

func main() {
	host, _ := os.Hostname()
	server := flag.String("server", "", "APP_MON sunucu adresi (ör. http://apm.firma.local:8080)")
	key := flag.String("key", "", "agent API anahtarı (ingest yetkili)")
	name := flag.String("name", "", "asset adı (varsayılan: bilgisayar adı)")
	watch := flag.String("watch", "", "izlenecek süreç adları, virgülle (ör. nginx,sshd): proc.<ad>.up / proc.<ad>.count")
	logSpec := flag.String("log", "", "günlük izleme: ad::dosya::regex (birden çok için ;; ile ayırın) -> log.<ad>.count")
	interval := flag.Duration("interval", 0, "gönderim aralığı (varsayılan 15s)")
	caFile := flag.String("ca", "", "kurumsal CA sertifikası (PEM)")
	insecure := flag.Bool("insecure", false, "TLS sertifikasını doğrulama (önerilmez)")
	cfgPath := flag.String("config", "", "yapılandırma dosyası (ANAHTAR=değer satırları)")
	install := flag.Bool("install", false, "ajanı bu makineye servis olarak kur ve başlat")
	uninstall := flag.Bool("uninstall", false, "kurulu ajanı kaldır")
	noApps := flag.Bool("no-apps", false, "uygulama keşfini kapat (süreç/servis/port listesi ve otomatik günlük sayımı gönderilmez)")
	root := flag.Bool("root", false, "Linux kurulumunda ajanı root çalıştır (otomatik günlük okuma ve tüm süreç portları için gerekir)")
	noConns := flag.Bool("no-conns", false, "bağlantı tabanlı servis haritası verisini (süreçlerin giden TCP bağlantıları) gönderme")
	noOTLP := flag.Bool("no-otlp", false, "yerel OpenTelemetry (OTLP) trace alıcısını kapat")
	otlpAddr := flag.String("otlp-listen", "", "yerel OTLP alıcı adresi (varsayılan 127.0.0.1:4318)")
	noWeb := flag.Bool("no-weblog", false, "web sunucusu (nginx/Apache/IIS) erişim günlüklerini okumayı kapat")
	noUpdate := flag.Bool("no-update", false, "uzaktan güncellemeyi kapat (sunucu yeni ajan sürümü önerse de kendini güncellemez)")
	webSetupF := flag.Bool("web-setup", false, "Linux: nginx/Apache'ye istek süresini içeren ajan erişim günlüğü ekle (riskler için bkz. çıktı)")
	webUndoF := flag.Bool("web-undo", false, "Linux: -web-setup'ın eklediği ayarı kaldır")
	dryRun := flag.Bool("dry-run", false, "-web-setup ile: yalnızca ne yapılacağını göster")
	yes := flag.Bool("yes", false, "-web-setup ile: onay sormadan devam et")
	apply := flag.Bool("apply-update", false, "(iç kullanım) bekleyen güncellemeyi uygula; systemd ExecStartPre ile çalışır")
	once := flag.Bool("once", false, "bir kez ölçüp JSON olarak yazdır ve çık (sınama)")
	printConf := flag.String("print-config", "", "örnek yapılandırma dosyasını yazdır (linux veya windows) ve çık")
	ver := flag.Bool("version", false, "sürümü yazdır")
	flag.Parse()
	if *ver {
		fmt.Println("appmon-agent", Version, runtime.GOOS+"/"+runtime.GOARCH)
		return
	}
	if *printConf != "" {
		fmt.Print(confTemplate(*printConf))
		return
	}
	if *webSetupF || *webUndoF {
		var err error
		if *webUndoF {
			err = webUndo()
		} else {
			err = webSetup(*dryRun, *yes)
		}
		if err != nil {
			log.Fatalf("%v", err)
		}
		return
	}
	if *apply {
		if err := applyUpdate(); err != nil {
			log.Fatalf("güncelleme uygulanamadı: %v", err)
		}
		return
	}
	if *once {
		c := newCollector()
		time.Sleep(1200 * time.Millisecond)
		out, _ := json.MarshalIndent(c.collect(), "", "  ")
		fmt.Println(string(out))
		return
	}

	cfg := settings{Name: host, Interval: 15 * time.Second}
	sendConf := map[string]string{}
	if p := *cfgPath; p != "" || defaultConfig() != "" {
		if p == "" {
			p = defaultConfig()
		}
		if m, err := readConf(p); err == nil {
			cfg.apply(m)
			for k, v := range m {
				if strings.HasPrefix(k, "SEND_") {
					sendConf[k] = v
				}
			}
		} else if *cfgPath != "" {
			log.Fatalf("yapılandırma okunamadı: %v", err)
		}
	}
	cfg.apply(map[string]string{"APM_SERVER": os.Getenv("APM_SERVER"), "APM_KEY": os.Getenv("APM_KEY"), "APM_NAME": os.Getenv("APM_NAME"),
		"APM_WATCH": os.Getenv("APM_WATCH"), "APM_LOG": os.Getenv("APM_LOG"), "APM_INTERVAL": os.Getenv("APM_INTERVAL"), "APM_CA_FILE": os.Getenv("APM_CA_FILE"), "APM_INSECURE": os.Getenv("APM_INSECURE"), "APM_APPS": os.Getenv("APM_APPS"), "APM_ROOT": os.Getenv("APM_ROOT"), "APM_UPDATE": os.Getenv("APM_UPDATE"), "APM_WEBLOG": os.Getenv("APM_WEBLOG"), "APM_CONNS": os.Getenv("APM_CONNS"), "APM_REMOTE_CONFIG": os.Getenv("APM_REMOTE_CONFIG"), "APM_PG_DSN": os.Getenv("APM_PG_DSN"), "APM_OTLP": os.Getenv("APM_OTLP")})
	for k, v := range sendFromEnv() {
		sendConf[k] = v
	}
	if len(sendConf) == 0 && *install { // yeniden kurulum: mevcut dosyadaki SEND_* seçimlerini koru
		if p := sendConfFile(); p != "" {
			if m, err := readConf(p); err == nil {
				for k, v := range m {
					if strings.HasPrefix(k, "SEND_") {
						sendConf[k] = v
					}
				}
			}
		}
	}
	cfg.applySend(sendConf)
	if !cfg.NoRemote && !*install {
		if rc := loadRemote(); rc != nil {
			cfg.applyRemote(rc)
			remoteVer = rc.V
		}
	}
	cfg.apply(map[string]string{"APM_SERVER": *server, "APM_KEY": *key, "APM_NAME": *name, "APM_WATCH": *watch, "APM_LOG": *logSpec, "APM_CA_FILE": *caFile})
	if *interval > 0 {
		cfg.Interval = *interval
	}
	if *insecure {
		cfg.Insecure = true
	}
	if *noApps {
		cfg.NoApps = true
	}
	if *root {
		cfg.Root = true
	}
	if *noUpdate {
		cfg.NoUpdate = true
	}
	if *noWeb {
		cfg.NoWeb = true
	}
	if *noConns {
		cfg.NoConns = true
	}
	if *noOTLP {
		cfg.NoOTLP = true
	}
	if *otlpAddr != "" {
		cfg.OTLPListen = *otlpAddr
	}

	if *uninstall {
		if err := doUninstall(); err != nil {
			log.Fatalf("kaldırma başarısız: %v", err)
		}
		return
	}
	if cfg.Server == "" {
		cfg.Server = "http://localhost:8080"
	}
	if cfg.Key == "" {
		log.Fatal("API anahtarı zorunlu (-key, APM_KEY veya yapılandırma dosyasında APM_KEY)")
	}
	if cfg.Log != "" {
		if _, err := parseLogSpecs(cfg.Log); err != nil {
			log.Fatalf("günlük izleme ayarı hatalı: %v", err)
		}
	}
	if *install {
		if err := doInstall(cfg); err != nil {
			log.Fatalf("kurulum başarısız: %v", err)
		}
		return
	}
	run(cfg)
}

func (s *settings) apply(m map[string]string) {
	set := func(dst *string, k string) {
		if v := strings.TrimSpace(m[k]); v != "" {
			*dst = v
		}
	}
	set(&s.Server, "APM_SERVER")
	set(&s.Key, "APM_KEY")
	set(&s.Name, "APM_NAME")
	set(&s.Watch, "APM_WATCH")
	set(&s.Log, "APM_LOG")
	set(&s.CAFile, "APM_CA_FILE")
	set(&s.PGDSN, "APM_PG_DSN")
	if v := strings.TrimSpace(m["APM_INTERVAL"]); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= time.Second {
			s.Interval = d
		} else if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			s.Interval = time.Duration(n) * time.Second
		}
	}
	if v := strings.ToLower(strings.TrimSpace(m["APM_INSECURE"])); v == "1" || v == "true" {
		s.Insecure = true
	}
	if v := strings.ToLower(strings.TrimSpace(m["APM_APPS"])); v == "0" || v == "off" || v == "false" {
		s.NoApps = true
	}
	if v := strings.ToLower(strings.TrimSpace(m["APM_WEBLOG"])); v == "0" || v == "off" || v == "false" {
		s.NoWeb = true
	}
	if v := strings.ToLower(strings.TrimSpace(m["APM_CONNS"])); v == "0" || v == "off" || v == "false" {
		s.NoConns = true
	}
	if v := strings.TrimSpace(m["APM_OTLP"]); v != "" {
		switch strings.ToLower(v) {
		case "0", "off", "false":
			s.NoOTLP = true
		case "1", "on", "true":
		default:
			s.OTLPListen = v
		}
	}
	if v := strings.TrimSpace(m["APM_REMOTE_CONFIG"]); v != "" && isOff(v) {
		s.NoRemote = true
	}
	if v := strings.ToLower(strings.TrimSpace(m["APM_UPDATE"])); v == "0" || v == "off" || v == "false" {
		s.NoUpdate = true
	}
	if v := strings.ToLower(strings.TrimSpace(m["APM_ROOT"])); v == "1" || v == "true" {
		s.Root = true
	}
}

// readConf ANAHTAR=değer satırlarını okur (# ile başlayanlar yorumdur).
func readConf(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		if i := strings.IndexByte(ln, '='); i > 0 {
			m[strings.TrimSpace(ln[:i])] = strings.Trim(strings.TrimSpace(ln[i+1:]), `"`)
		}
	}
	return m, nil
}

func newClient(cfg settings) *http.Client {
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment}
	tc := &tls.Config{InsecureSkipVerify: cfg.Insecure} //nolint:gosec // kullanıcı bilerek açar
	if cfg.CAFile != "" {
		if pem, err := os.ReadFile(cfg.CAFile); err == nil {
			pool, _ := x509.SystemCertPool()
			if pool == nil {
				pool = x509.NewCertPool()
			}
			pool.AppendCertsFromPEM(pem)
			tc.RootCAs = pool
		} else {
			log.Printf("CA dosyası okunamadı: %v", err)
		}
	}
	tr.TLSClientConfig = tc
	return &http.Client{Timeout: 15 * time.Second, Transport: tr}
}

const maxBuffered = 2000 // en fazla bu kadar metrik bellekte tutulur (~15 sn aralıkla ~2-3 saat)

func run(cfg settings) {
	endpoint := strings.TrimRight(cfg.Server, "/") + "/api/ingest"
	client := newClient(cfg)
	c := newCollector()
	watch := splitList(cfg.Watch)
	logs := initLogs(cfg.Log)
	initSelfSHA()
	readUpdateErr()
	var pending []metric
	var scanner *appScanner
	var lastApps, lastDB, lastInv time.Time
	if d, err := time.ParseDuration(os.Getenv("APM_INVENTORY_EVERY")); err == nil && d >= time.Second {
		invEvery = d // sınama için kısaltılabilir
	}
	lastInv = time.Now().Add(-invEvery + min(20*time.Second, invEvery)) // ilk envanter ~20 sn sonra
	if d, err := time.ParseDuration(os.Getenv("APM_APPS_EVERY")); err == nil && d >= time.Second {
		appsEvery = d
	}
	if !cfg.NoApps {
		scanner = newAppScanner()
		scanner.scan() // CPU yüzdesi için başlangıç örneği ve günlük konumları (çıktı atılır)
		lastApps = time.Now()
	}
	var web *webLogs
	var lastWeb time.Time
	if !cfg.NoWeb {
		web = newWebLogs(cfg.Name)
		web.refresh(webCandidates())
		lastWeb = time.Now()
	}
	if !cfg.NoOTLP {
		addr := cfg.OTLPListen
		if addr == "" {
			addr = "127.0.0.1:4318"
		}
		startOTLP(addr, newOTLPProxy(cfg.Server, cfg.Key, client))
	}
	cleanupOld()
	log.Printf("agent %s başladı: %s -> %s (her %s)", Version, cfg.Name, endpoint, cfg.Interval)
	if os.Getenv("APM_NO_JITTER") == "" { // toplu yeniden başlatmada 500 ajan aynı saniyede bağlanmasın
		span := cfg.Interval
		if span > 20*time.Second {
			span = 20 * time.Second
		}
		time.Sleep(time.Duration(rand.Int63n(int64(span) + 1)))
	}
	fails := 0
	for ; ; time.Sleep(cfg.Interval) {
		now := time.Now()
		ms := cfg.filterHost(c.collect())
		if cfg.sends("SEND_WATCH") {
			ms = append(ms, watchMetrics(watch)...)
		}
		if cfg.sends("SEND_LOGCOUNT") {
			ms = append(ms, logMetrics(logs)...)
		}
		var report *appsReport
		if scanner != nil {
			ms = append(ms, scanner.logs.metrics()...)
			if now.Sub(lastApps) >= appsEvery {
				rep, am := scanner.scan()
				report, lastApps = &rep, now
				ms = append(ms, am...)
			}
		}
		var conns *connReport
		if scanner != nil && !cfg.NoConns && report != nil { // uygulama keşfiyle aynı sıklıkta
			conns = collectConns()
		}
		if cfg.sends("SEND_INVENTORY") && now.Sub(lastInv) >= invEvery {
			lastInv = now
			if inv := collectInventory(); inv != nil {
				pendingInventory = inv
			}
		}
		var dbs []dbReport
		if cfg.PGDSN != "" && cfg.sends("SEND_DBSTATS") && now.Sub(lastDB) >= appsEvery {
			dbs, lastDB = collectDBStats(splitDSNs(cfg.PGDSN)), now
		}
		var access []accessEntry
		if web != nil {
			if now.Sub(lastWeb) >= 60*time.Second {
				web.refresh(webCandidates())
				lastWeb = now
			}
			web.poll()
			access = web.agg.take()
		}
		ms = append(ms, metric{Name: "agent.up", Value: 1})
		for i := range ms {
			t := now
			ms[i].Time = &t
		}
		pending = append(pending, ms...)
		if len(pending) > maxBuffered {
			pending = pending[len(pending)-maxBuffered:]
		}
		for len(pending) > 0 {
			n := len(pending)
			if n > 400 {
				n = 400
			}
			weblabel = ""
			if web != nil {
				weblabel = web.note
			}
			if err := sendWith(client, endpoint, cfg, pending[:n], report, access, conns, dbs); err != nil {
				fails++
				wait := backoffFor(fails, err)
				log.Printf("gönderim başarısız (%d metrik tamponda): %v; %s sonra yeniden denenecek", len(pending), err, wait.Round(time.Second))
				time.Sleep(wait)
				break
			}
			fails = 0
			report, access, conns, dbs = nil, nil, nil, nil // uygulama listesi ve erişim özeti yalnızca ilk parçayla gider
			pending = pending[n:]
			if len(pending) > 0 { // birikmiş tampon: sunucuyu boğmamak için parçalar arasında kısa ara
				time.Sleep(jitter(400 * time.Millisecond))
			}
		}
		if web != nil && len(access) > 0 {
			web.agg.restore(access) // gönderilemedi: bir sonraki tura kalır
		}
		handleRemote(cfg)
		tryUpdate(client, cfg)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// watchMetrics izlenen süreçler için proc.<ad>.up (0/1) ve proc.<ad>.count üretir.
func watchMetrics(names []string) []metric {
	if len(names) == 0 {
		return nil
	}
	have := processCounts()
	var out []metric
	for _, n := range names {
		key := strings.TrimSuffix(strings.ToLower(n), ".exe")
		cnt := have[key]
		if cnt == 0 && len(key) > 15 { // Linux comm adı 15 karakterle sınırlıdır
			cnt = have[key[:15]]
		}
		up := 0.0
		if cnt > 0 {
			up = 1
		}
		out = append(out, metric{Name: "proc." + n + ".up", Value: up}, metric{Name: "proc." + n + ".count", Value: float64(cnt)})
	}
	return out
}

func send(client *http.Client, endpoint string, cfg settings, ms []metric) error {
	return sendWith(client, endpoint, cfg, ms, nil, nil, nil, nil)
}

// sendWith metriklerle birlikte (varsa) uygulama keşfi raporunu da gönderir.
var weblabel string // labels.weblog: web günlüğü durum özeti

// pendingInventory gönderilmeyi bekleyen envanterdir; başarılı gönderimden sonra temizlenir.
var pendingInventory *invReport

func sendWith(client *http.Client, endpoint string, cfg settings, ms []metric, apps *appsReport, access []accessEntry, conns *connReport, dbs []dbReport) error {
	labels := map[string]string{"os": runtime.GOOS, "arch": runtime.GOARCH, "agent_version": Version}
	if selfSHA != "" && !cfg.NoUpdate {
		labels["agent_sha"] = selfSHA // sunucu bu özetle uzaktan güncelleme gerekip gerekmediğini anlar
	}
	cfgLabels(cfg, labels)
	if lastUpdateErr != "" {
		labels["update_error"] = lastUpdateErr
	}
	if weblabel != "" {
		labels["weblog"] = trunc(weblabel, 160)
	}
	payload := map[string]any{
		"asset":   map[string]any{"name": cfg.Name, "kind": "host", "labels": labels},
		"metrics": ms,
	}
	if apps != nil {
		payload["apps"] = apps
	}
	if len(access) > 0 {
		payload["access"] = access
	}
	if conns != nil {
		payload["conns"] = conns
	}
	if len(dbs) > 0 {
		payload["dbstats"] = dbs
	}
	inv := pendingInventory
	if inv != nil {
		payload["inventory"] = inv
	}
	raw, _ := json.Marshal(payload)
	resp, err := post(client, endpoint, cfg.Key, raw)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 && inv != nil && pendingInventory == inv {
		pendingInventory = nil
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		// 4xx (yetki/lisans) hatasında tamponu birikmesin diye başarılı sayılmaz ama kalıcı hata loglanır.
		he := &httpErr{code: resp.StatusCode, msg: strings.TrimSpace(string(msg))}
		if n, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && n > 0 && n <= 300 {
			he.retry = time.Duration(n) * time.Second
		}
		return he
	}
	if body, err := io.ReadAll(io.LimitReader(resp.Body, 16384)); err == nil {
		if o := decodeOffer(body); o != nil {
			pendingOffer = o
		}
		if c := decodeConf(body); c != nil && !cfg.NoRemote {
			pendingConf = c
		}
	}
	return nil
}

// httpErr sunucunun hata yanıtıdır; Retry-After varsa ajan o kadar bekler (sunucu yoğunken yük yayılır).
type httpErr struct {
	code  int
	msg   string
	retry time.Duration
}

func (e *httpErr) Error() string { return fmt.Sprintf("HTTP %d: %s", e.code, e.msg) }

// jitter d süresini %50–%100 aralığında rastgele kısaltır; ajanların aynı anda gönderim yapmasını önler.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d/2 + time.Duration(rand.Int63n(int64(d/2)+1))
}

// backoffFor ardışık hata sayısına göre bekleme süresi verir (5 sn, 10, 20, 40, en çok 60 sn; jitter'lı).
// Sunucu Retry-After verdiyse o süre esas alınır.
func backoffFor(fails int, err error) time.Duration {
	d := 5 * time.Second << uint(min(fails-1, 4))
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	var he *httpErr
	if errors.As(err, &he) && he.retry > d {
		d = he.retry
	}
	return jitter(d)
}

// gzipOK: sunucu gzip gövdeyi reddederse (eski sürüm) oturum boyunca sıkıştırma kapatılır.
var gzipOK = true

func gz(b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

// post gövdeyi gzip ile sıkıştırarak yollar; eski sunucu 400 verirse düz JSON ile bir kez daha dener.
func post(client *http.Client, endpoint, key string, raw []byte) (*http.Response, error) {
	do := func(body []byte, enc bool) (*http.Response, error) {
		req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", key)
		if enc {
			req.Header.Set("Content-Encoding", "gzip")
		}
		return client.Do(req)
	}
	if !gzipOK || len(raw) < 512 {
		return do(raw, false)
	}
	resp, err := do(gz(raw), true)
	if err == nil && (resp.StatusCode == 400 || resp.StatusCode == 415) {
		resp.Body.Close()
		gzipOK = false
		log.Printf("sunucu sıkıştırılmış gövdeyi kabul etmedi; düz JSON'a dönüldü (sunucuyu güncelleyin)")
		return do(raw, false)
	}
	return resp, err
}
