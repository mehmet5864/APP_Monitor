# APP_MON

Uygulama izleme ve performans ölçme (APM) platformunun çalışan ilk iskeleti. Tek Go sunucusu, PostgreSQL, gömülü web arayüzü ve isteğe bağlı agent'tan oluşur.

## Alınan kararlar

| Konu | Karar |
| --- | --- |
| Dağıtım | SaaS ve on-premise aynı kod tabanı. `APM_MODE=saas` veya `onprem`. Tüm tablolar `tenant_id` taşır; on-premise tek `default` kiracı kullanır. |
| Lisanslama | İzlenen benzersiz asset sayısı (sunucu, servis, veritabanı, URL). Limit dolunca yeni asset kabul edilmez (HTTP 402), mevcutlar izlenmeye devam eder. |
| Veritabanı | PostgreSQL 16 (metrikler dahil). Büyük ölçekte TimescaleDB veya ClickHouse'a geçiş planlanabilir. |
| İzlenecek diller | .NET, Java, Python, Go. |
| Topoloji | Asset'ler düğüm, `topology_edges` tablosu yönlü kenarlardır; etki ve bağımlılık sorguları PostgreSQL özyinelemeli CTE ile çalışır (eklenti gerekmez). Haritada sağlıksız asset'lerin olası kök nedeni vurgulanır. |
| Toplama | Önce agent'sız: sunucu tarafı probe'lar (HTTP, TCP, PostgreSQL) ve OpenTelemetry (OTLP/HTTP). Agent yalnızca ana makine metrikleri için, tek dosyalık ve bağımlılıksız. |

## Kurulum (tek komut)

Gereksinim: Docker (Windows'ta Docker Desktop, Linux'ta Docker Engine + compose eklentisi).

- **Windows:** `kur.bat` dosyasına çift tıklayın. `.env` yoksa rastgele parola ve giriş anahtarıyla oluşturur, sistemi başlatır, tarayıcıyı açar ve giriş anahtarını yazar.
- **Linux:** `chmod +x *.sh && ./kur.sh`. Docker yoksa resmi betikle kurmayı önerir; `.env`'yi (izinleri 600) oluşturur, sistemi başlatır, adresi ve giriş anahtarını yazar. Sonraki işlemler: `./guncelle.sh` (yeni kod, veriler korunur), `./durdur.sh` (`--sil` verileri de siler).
- Elle kurulum için `.env.example` dosyasını `.env` olarak kopyalayıp değerleri değiştirin. `.env` Git'e eklenmez.

## Hızlı başlangıç (Docker Desktop)

1. `.env` dosyasındaki `POSTGRES_PASSWORD` ve `APM_ADMIN_KEY` değerlerini değiştirin.
2. Bu klasörde çalıştırın:

   ```
   docker compose up -d --build
   ```

3. Tarayıcıda `http://localhost:8080` adresini açın ve `.env` içindeki `APM_ADMIN_KEY` ile giriş yapın.
4. Sağlık kontrolü: `http://localhost:8080/healthz`

Windows'ta komut yazmadan kullanmak için üç dosya vardır (çift tıklayın):

- `baslat-docker.bat`: Docker'ı kontrol eder, imajı derler, başlatır, sağlık kontrolünü bekler ve tarayıcıyı açar.
- `guncelle-docker.bat`: yeni kodu derleyip yeniden başlatır. Veriler korunur, yeni migration'lar otomatik uygulanır.
- `durdur-docker.bat`: durdurur (veriler kalır). `durdur-docker.bat /sil` EVET yazarsanız verileri de siler.

Elle: durdurmak için `docker compose down`; verilerle birlikte silmek için `docker compose down -v`.

## Agent'sız izleme (tercih edilen)

**Probe:** Arayüzde Probe'lar sayfasından HTTP, TCP veya PostgreSQL hedefi ekleyin. Sunucuya hiçbir şey kurulmaz. Her probe `probe.up` ve `probe.latency_ms` metriklerini üretir. Her hedef için ayrı asset adı kullanmanız önerilir; aynı asset'e birden fazla probe bağlarsanız metrikler ortalanır.

**Docker'da ana makine uygulamaları:** APP_MON konteyner içinde çalıştığı için `localhost` ve `127.0.0.1` konteynerin kendisidir. Windows veya Mac'te çalışan bir uygulamayı izlemek için hedefe `host.docker.internal` yazın (ör. `host.docker.internal:43130`). Uygulama yalnızca `127.0.0.1`'e bağlıysa ve yine bağlanılamıyorsa `0.0.0.0`'a bağlayın veya güvenlik duvarında izin verin. `APM_MODE=saas` iken özel ağ adresleri bilerek engellenir.

**OpenTelemetry (.NET, Java, Python, Go):** Uygulamanın ortam değişkenleri:

```
OTEL_EXPORTER_OTLP_ENDPOINT=http://<sunucu>:8080
OTEL_EXPORTER_OTLP_PROTOCOL=http/json
OTEL_EXPORTER_OTLP_HEADERS=X-API-Key=<agent anahtarı>
OTEL_SERVICE_NAME=odeme-servisi
```

`telemetry.sdk.language` kaynak özelliğinden dil otomatik algılanır. Agent anahtarını arayüzde "Lisans ve anahtarlar" sayfasından oluşturun.

Metrikler **OTLP/HTTP JSON** kabul eder (`POST /v1/metrics`, gauge ve sum). Birçok SDK varsayılan olarak protobuf gönderir; metrik için bir OpenTelemetry Collector'ı `otlphttp` exporter ve `encoding: json` ile araya koyun. **Trace'ler** (`POST /v1/traces`) hem JSON hem protobuf kabul eder (aşağıda yama83).

## Agent (isteğe bağlı)

CPU, bellek, disk, yük ve çalışma süresi gönderir. Yalnızca standart kütüphane kullanır.

```
docker build --target agent-bin --output ./dist .
APM_SERVER=http://<sunucu>:8080 APM_KEY=<agent anahtarı> ./dist/appmon-agent
```

Bu çıktı Linux (amd64) içindir. Başka platformlar için `GOOS`/`GOARCH` ile derleyin: `go build ./cmd/agent`. Linux dışında şimdilik yalnızca "agent çalışıyor" sinyali gönderilir; Windows ve macOS metrikleri eklenecek.

## Probe türleri

| Tür | Hedef | Ölçülen / ek metrik |
|---|---|---|
| `http` | URL | durum kodu, gecikme |
| `tcp` | host:port | bağlantı; isteğe bağlı gönder/bekle (banner, Redis PING) |
| `icmp` | host/IP | ortalama RTT, `probe.packet_loss_pct` (paket sayısı seçilebilir) |
| `snmp` | host[:161] | v1/v2c/v3 GET; `probe.snmp_value` (sayısal OID); topluluk adı ve v3 parolaları gizli saklanır |
| `host` | host[:161] | SNMP ile CPU/RAM/Disk: `host.cpu_pct`, `host.mem_pct`, `host.disk_pct` (en dolu disk), `host.disk_pct.<ad>` (her disk), `host.uptime_sec` |
| `dns` | alan adı | çözümleme süresi; kayıt türü A/AAAA/MX/TXT/CNAME/NS, özel sunucu, beklenen yanıt; `probe.dns_answers` |
| `tls` | host[:443] | el sıkışma; `probe.cert_days_left` (sertifika bitimine kalan gün) |
| `udp` | host:port | gönder/yanıt bekle |
| `ntp` | host[:123] | `probe.ntp_offset_ms` (saat sapması) |
| `postgres` | bağlantı dizesi | `SELECT 1` |

ICMP: sunucu kök olmadan çalıştığı için ayrıcalıksız ICMP soketi kullanılır; `docker-compose.yml` içindeki `net.ipv4.ping_group_range` ayarı bunu sağlar (ham soket için `NET_RAW` de olur). SaaS modunda tüm türler özel/yerel ağ adreslerine engellidir. Hazır alarm şablonları: paket kaybı, sertifika süresi, NTP sapması.

## Probe seçenekleri

HTTP probe'ları için yöntem (GET/HEAD/POST/PUT/PATCH/DELETE/OPTIONS), özel başlıklar, gövde, beklenen durum kodu (`200`, `200-299`, `200,204,301-302`; boşsa 200-399), yanıtta aranacak metin, TLS doğrulama ve yönlendirme izleme ayarlanabilir. Hata mesajlarına yanıt gövdesi veya adres yazılmaz.

## Senaryolar (bot)

**Senaryolar** sayfasında izlenen uygulamadaki işlemler sıralı HTTP adımları olarak tanımlanır (en fazla 20 adım). APP_MON bunları belirlenen aralıkla (en az 30 sn) çalıştırır; her adımın süresi ve doğruluğu ölçülür, ilk başarısız adımda akış durur.

- Çerezler adımlar arasında taşınır. Bir yanıttan değer alınır (`token = json:data.token`, `rid = header:X-Request-Id`, `urun = regex:ürün: (\w+)`) ve sonraki adımlarda `{{token}}` ile kullanılır; senaryo değişkenleri de `{{ad}}` ile kullanılır.
- Her adımda beklenen durum, aranacak metin ve en fazla süre (ms) denetlenir.
- "Dene" butonu kaydetmeden çalıştırır; "Şimdi çalıştır" kayıtlı senaryoyu geçmişe yazarak çalıştırır.
- Metrikler `scenario.up` ve `scenario.duration_ms`; hazır alarm kuralları: "Senaryo başarısız" (kritik), "Yavaş senaryo" (uyarı). Senaryo asset sayısı lisansta bir asset sayılır.
- Docker içinden ana makinedeki uygulamaya `host.docker.internal` ile erişin.
- Başlıklar, gövde ve değişkenler (parola/token) veritabanında **düz metin** saklanır, arayüz ve API bunları geri göstermez. Sınırlı yetkili bir test kullanıcısı kullanın.

## Tarayıcıdan kayıt ve oynatma (gömülü tarayıcı)

**Senaryolar → Tarayıcıdan kaydet:** sunucuda başsız bir Chromium açılır ve canlı görüntüsü panelin içinde görünür. Başlangıç adresini yazıp "Kaydı başlat" deyin; tarayıcıda tıklayın, yazın, Enter/Tab basın. Tıklamalar, yazılanlar ve tuşlar adım olarak kaydedilir. "Kaydı bitir" deyince adımları düzenlersiniz (ad, sayfada bulunması gereken metin, en fazla süre, silme), "Dene" ile oynatır, "Senaryoyu kaydet" ile aralık (en az 60 sn) belirlersiniz.

- Her çalıştırma temiz bir tarayıcı profilinde başlar (çerez yok); giriş adımları kayda dahil olmalıdır. Öğeler önce CSS seçiciyle, bulunamazsa görünen metinle bulunur.
- **Parola alanlarına yazılanlar** adımda değil ayrı değişkende (`secret_1`) tutulur, arayüze geri gönderilmez. Veritabanında düz metindir.
- Ölçülen: her adımın süresi (tıklamadan ağın sakinleşmesine kadar), sayfa HTTP hatası, beklenen metin, en fazla süre.
- **Sayfa yapısı değişikliği:** her adımda sayfanın yapısal parmak izi (başlıklar, form alanları, düğmeler, bağlantı yolları, betik alan adları) saklanır ve önceki çalıştırmayla karşılaştırılır; eklenen/kaybolan öğeler çalıştırma ayrıntısında görünür, "Sayfa yapısı değişti" alarmı (bilgi) açılır ve topolojide düğüm işaretlenir. Fiyat, tarih gibi değişken metinler parmak izine girmez.
- **Otomatik ağ bağımlılıkları:** sayfanın eriştiği alan adları topolojiye kesikli "dış bağımlılık" düğümü olarak eklenir (lisanstan asset düşmez). Ağ hatası olanlar kırmızı görünür.
- Her adımın ekran görüntüsü son 10 çalıştırma için saklanır.
- Aynı anda en çok 2 kayıt oturumu ve 2 oynatma çalışır; boşta kalan kayıt oturumu 10 dk sonra kapanır.
- Güvenlik: `APM_BROWSER_ENABLED` SaaS modunda varsayılan **kapalıdır**, çünkü tarayıcı özel ağ (SSRF) engelini uygulayamaz; on-premise'te açıktır. Docker imajında Chromium `--no-sandbox` ile çalışır.
- Sınırlar: açılır liste (select) menüleri, dosya yükleme/indirme, yeni sekme/pencere (aynı sekmede açılır), iframe içi işlemler, sürükle-bırak ve çok faktörlü doğrulama desteklenmez; dinamik sınıf/ID kullanan sayfalarda seçici bozulabilir (metin yedeği devreye girer).

## Kimlik doğrulama, görünüm ve ayarlar

- **Kimlik doğrulama paneli** (kayıt sonrası inceleme ekranı): açılırsa senaryo her çalışmada otomatik giriş yapar.
  - *Form modu*: kullanıcı adı/parola giriş formuna otomatik yazılır; kayıttaki giriş adımları kaldırılabilir.
  - *Adım modu*: "giriş bloğunun parçası" işaretli adımlar yalnızca oturum yoksa çalışır.
  - Oturum çerezleri saklanır ve yeniden kullanılır; süresi dolarsa sunucu bir kez yeniden giriş yapar. "Kontrol URL'si" giriş formunu (veya giriş yapılmış metni) gösteren bir sayfa olmalıdır.
  - Kısıt: MFA/CAPTCHA desteklenmez. Parola ve çerezler şimdilik veritabanında **düz metin** saklanır; Vault entegrasyonu yapılmadı (Ayarlar'da yer tutucu var). Yetkisi sınırlı test kullanıcıları kullanın.
- **Asset türleri ve topoloji şekilleri**: web sitesi, web servisi, servis, veritabanı, sunucu, ağ, önbellek, kuyruk (ve otomatik "dış bağımlılık"). Tür asset detayından değiştirilir (`PATCH /api/assets/{id}`).
- **Etkileşimli harita** (Genel bakış ve Topoloji): tüm uygulamalar türüne özgü şekillerle çizilir; kutular sürüklenebilir (yerleşim bu tarayıcıda saklanır, "Yerleşimi sıfırla" ile döner, klavyede ok tuşlarıyla da taşınır). Bir kutuya tıklayınca ona bağlı/bağımlı tüm bileşenlere yakınlaşılır, diğerleri soluklaşır ve kutuların içinde istatistikler görünür (24 sa erişilebilirlik, gecikme, CPU/bellek/disk veya son veri zamanı, açık alarm). Veri: `GET /api/topology/stats`.
- **Hareket**: grafik çizgisi çizilerek açılır, çubuklar büyür, KPI sayıları sayarak gelir; haritada oklar bağımlılık yönünde akar, erişilemeyen asset'in noktası nabız atar, kök neden halkası döner. İşletim sisteminde "hareketi azalt" açıksa tüm animasyonlar kapanır.
- **Kontrol edilen sayfalar** (Genel bakış): tarayıcı senaryolarının son kontrolündeki sayfa küçük görüntü olarak gösterilir; çerçeve rengi durumu belirtir (yeşil sağlıklı, sarı sayfa 24 saat içinde değişti, kırmızı başarısız, kesikli gri bekliyor). Başarısız kontrolde son sağlam görüntü gri tonlu gösterilir. Tıklayınca büyür. Yalnızca son 10 çalıştırmanın görüntüsü saklanır.
- **Genel bakış**: KPI kartları, 24 saatlik erişilebilirlik ve alarm grafikleri (üzerine gelince ayrıntı, "tablo olarak göster"), türe göre dağılım, en yavaş hedefler, senaryo sağlık şeridi.
- **Ayarlar**: tema (sistem/açık/koyu), renk paleti (Varsayılan, Okyanus, Orman, Gün batımı, Gül, Grafit), isteğe bağlı vurgu rengi; tercihler tarayıcıda saklanır.
- **Sayfa değişikliği alarmı** ("Sayfa yapısı değişti"): her çalıştırma bir öncekiyle (son iki kontrol) karşılaştırılır. Yanlış pozitifleri azaltmak için: slayt gösterisi/sayaç/saat/reklam gibi dinamik bileşenler (class/id'sinde carousel, slider, counter, visitor, ticker... geçenler, `aria-live`, iframe) hiç ölçülmez; sayılar `#` olarak normalleştirilir; adım içinde 0,6 sn arayla alınan iki örnekte ortak olmayan öğeler atılır; ve kendiliğinden gelip giden öğeler (2 değişimden sonra) "dinamik" sayılıp yok sayılır (çalıştırma ayrıntısında "n öğe dinamik içerik sayıldı" görünür). Kısıt: bir değişiklik ilk kez görüldüğünde mutlaka bildirilir.

## Alarm kuralları

Alarmlar sayfasında kurallar düzenlenebilir (ad, metrik, koşul, eşik, pencere, önem), geçici olarak kapatılıp açılabilir (`PUT /api/alert-rules/{id}`). Kural değişince veya kapanınca o kuralın açık alarmları kapatılır. "Hazır şablonlar": sunucu (CPU/bellek/disk), probe (erişilemiyor/yavaş yanıt), senaryo (başarısız/yavaş/sayfa değişti) için tek tıkla ekleme, grup olarak ekleme ve "Düzenleyip ekle".

## API özeti

Kimlik doğrulama: `X-API-Key` veya `Authorization: Bearer`. İki rol vardır: `admin` (her şey) ve `agent` (yalnızca veri gönderir).

| Uç | Rol | Açıklama |
| --- | --- | --- |
| `POST /api/ingest` | agent | Agent heartbeat ve metrikleri |
| `POST /v1/metrics` | agent | OTLP/HTTP JSON metrikleri |
| `POST /v1/traces` | agent | OTLP/HTTP trace (JSON/protobuf, gzip) |
| `GET /api/traces`, `/api/traces/{id}`, `/api/traces/services`, `/api/traces/operations`, `/api/traces/map` | read | trace listesi, ayrıntı (şelale), servis RED özeti, işlem dökümü, servis haritası |
| `GET /api/overview`, `/api/assets`, `/api/assets/{id}/metrics` | admin | Genel bakış, envanter, zaman serisi |
| `GET /api/topology`, `POST /api/topology/edges`, `DELETE /api/topology/edges/{id}`, `GET /api/assets/{id}/impact` | admin | Topoloji grafı, bağlantı yönetimi, etki ve bağımlılık analizi |
| `GET/POST/DELETE /api/probes` | admin | Agent'sız izleme (HTTP seçenekleriyle) |
| `POST /api/recorder`, `GET /api/recorder/{id}[/frame]`, `POST /api/recorder/{id}/input`, `POST /api/recorder/{id}/stop` | admin | Gömülü tarayıcı kaydı |
| `GET /api/scenarios/{id}/runs/{run}/shots/{n}` | admin | Adım ekran görüntüsü |
| `GET/POST/DELETE /api/scenarios`, `POST /api/scenarios/test`, `POST /api/scenarios/{id}/run`, `GET /api/scenarios/{id}/runs` | admin | HTTP senaryo botu |
| `GET/POST/DELETE /api/alert-rules`, `GET /api/alerts` | admin | Alarm kuralları ve alarmlar |
| `GET/PUT /api/license` | admin | Asset limiti ve geçerlilik |
| `GET/POST/DELETE /api/keys` | admin | API anahtarları (anahtar yalnızca oluşturulurken görünür) |
| `GET /api/audit` | admin | Denetim günlüğü |

## Geliştirme

Go 1.24 ve bir PostgreSQL gerekir.

```
set APM_DB_DSN=postgres://appmon:appmon@localhost:5432/appmon?sslmode=disable
set APM_ADMIN_KEY=gelistirme-anahtari
go run ./cmd/server
go test ./...
```

Ortam değişkenleri: `APM_ADDR` (`:8080`), `APM_DB_DSN`, `APM_MODE`, `APM_ADMIN_KEY`, `APM_ASSET_LIMIT` (25, yalnızca ilk kurulumda), `APM_RETENTION_DAYS` (14), `APM_BLOCK_PRIVATE_PROBES` (SaaS'ta varsayılan `true`).

## Doğrulananlar

Geliştirme ortamında PostgreSQL 16 ile uçtan uca denendi: migration, anahtar rolleri (agent anahtarı yönetim API'sine erişemez), agent heartbeat'i, OTLP JSON alımı (.NET dili algılandı), HTTP/TCP/PostgreSQL probe'ları, alarmın açılması, lisans limitinin hem probe hem agent hem OTLP yolunda uygulanması (4. asset 402 döndü, mevcut asset veri göndermeye devam etti), SaaS modunda özel ağ engeli (birim testi), topoloji grafı (bağlantı ekleme, yinelenen bağlantı için 409, etki ve bağımlılık sorguları, kök neden hesabı ve birim testi), HTTP probe seçenekleri, çok adımlı senaryo (çerez + token çıkarımı, yanlış parolada alarm açılıp düzelince kapanması, gizli değerlerin API'de görünmemesi), arayüzün tüm sayfaları ve telefon genişliğinde yatay taşma olmaması.

**Denenmedi:** Docker imaj derlemesi ve `docker compose` (geliştirme ortamında Docker çalışmıyordu). İlk `docker compose up --build` çıktısını kontrol edin.

## Henüz yapılmayanlar

Gereksinim listesindeki şunlar bu iskelette **yok**:

- ML tabanlı anomali tespiti (baseline), AI kök neden analizi, alarm gruplama. Alarmlar şimdilik pencere ortalaması ve sabit eşiktir.
- Log toplama, gerçek kullanıcı izleme (RUM), trace'lerden alarm üretme. Topoloji bağlantıları elle tanımlanır; yalnızca tarayıcı senaryolarının eriştiği dış alan adları otomatik eklenir.
- ITSM/Slack entegrasyonları ve auto-remediation. Alarmlar yalnızca arayüzde görünür.
- SSO/AD/LDAP. Kimlik doğrulama şimdilik API anahtarıdır; kullanıcı tabanlı RBAC yoktur.
- SLA/SLO raporları, maliyet yönetimi, özelleştirilebilir dashboard'lar.
- SaaS kiracı kaydı ve faturalama. Veri modeli çok kiracılıdır ama yalnızca `default` kiracı oluşturulur.

Üretime çıkmadan önce yapılması gerekenler:

- TLS: sunucu düz HTTP dinler; bir ters vekil (nginx, Caddy, Traefik) arkasına koyun.
- Lisans: `PUT /api/license` yöneticiye açıktır. On-premise için imzalı lisans dosyası, SaaS için faturalama sistemi bu ucu yerine koymalıdır.
- Probe hedefleri, probe/senaryo başlıkları, gövdeleri ve değişkenleri veritabanında düz metindir (arayüzde maskelenir veya gösterilmez). Şifrelenmiş saklama eklenmeli.
- API anahtarı denemelerine hız sınırı yoktur.
- Metrik tablosu bölümlenmemiştir; yüksek hacimde TimescaleDB veya ClickHouse gerekir.

## Topoloji haritası

Arayüzde **Topoloji** sayfası, asset'leri bağımlılık yönünde katmanlı bir grafta çizer: üstte kullanıcıya yakın servisler, altta veritabanı ve sunucular. Düğüm rengi asset durumunu, rozet açık alarm sayısını gösterir. Kesikli kırmızı çerçeve **olası kök nedeni** işaretler: sağlıksız (erişilemiyor veya çevrimdışı) olup bağımlı olduğu hiçbir asset sağlıksız olmayan düğümler. Bir düğüme tıklayınca düşerse etkilenecek asset'ler ve bağımlılıkları listelenir.

Bağlantılar şimdilik elle tanımlanır ("odeme-api, odeme-db'ye bağımlıdır"). Trace verisinden otomatik keşif sonraki adımdır. Sorgular ortak bir grafik katmanına yazıldığı için ilişki sayısı çok büyürse Apache AGE (PostgreSQL graf eklentisi, özel imaj gerekir) veya Neo4j'ye geçmek mümkündür; bu iskelette ikisi de kullanılmıyor, böylece tek bir PostgreSQL yeterli kalıyor.

## Klasör yapısı

```
cmd/server/        sunucu (API + arayüz + arka plan işleri)
cmd/agent/         agent
internal/app/      HTTP API, kimlik doğrulama, lisans, alım (ingest/OTLP), arka plan işleri
internal/app/topology.go  topoloji grafı, etki analizi (recursive CTE), kök neden hesabı
internal/probe/    HTTP/TCP/PostgreSQL probe'ları ve SaaS ağ koruması
internal/cdp/      bağımlılıksız Chrome DevTools istemcisi ve Chromium başlatıcı
internal/browser/  tarayıcı kaydı, oynatma, sayfa parmak izi/fark, ağ bağımlılıkları
internal/scenario/ çok adımlı HTTP senaryo motoru (çerez, değişken, çıkarım, doğrulama)
internal/db/       bağlantı ve gömülü migration'lar
web/static/        arayüz (derleme gerektirmeyen HTML/CSS/JS)
```

## Sayfa değişti uyarısı: neden ve temizleme

- Panodaki "Kontrol edilen sayfalar" bölümü en üsttedir; sağlıklı sayfalar önce, ardından değişen, bekleyen ve başarısız olanlar sıralanır.
- "Sayfa değişti" kartında **Neden değişti?** kutusu, son değişiklikte eklenen (+) ve kaybolan (−) öğeleri gösterir.
- **Uyarıyı temizle** (`POST /api/scenarios/{id}/ack-changes`) rozeti kaldırır, ilgili açık alarmı kapatır ve son bir saatin değişiklik ölçümlerini siler; yeni bir değişiklik olursa yeniden işaretlenir.
- Parmak izi biçimi sürümlüdür (migrasyon 009): sürüm yükseltmesinden sonraki ilk çalıştırma yeni taban çizgisi olur, sahte değişiklik üretmez.
- Assetler, Alarmlar, Probe'lar ve Lisans sayfalarına özet kartları eklendi.

## Topoloji: bağlantı değiştirme ve silme

Topoloji sayfasındaki **Bağlantılar** tablosunda elle tanımlanmış her bağlantı için **Değiştir** (kaynak, ilişki, hedef; `PUT /api/topology/edges/{id}`) ve **Sil** düğmeleri bulunur. Otomatik (dış servis) bağlantılar değiştirilemez.

## Tarayıcıdan kayıtta kimlik doğrulama

"Tarayıcıdan kaydet" başlangıcında **Bu sistemde kimlik doğrulama (giriş) var** seçilip kullanıcı adı ve parola girilirse APP_MON girişi kayıt başlamadan kendisi yapar; giriş **kayda adım olarak eklenmez**, kayıt oturum açılmış sayfadan başlar. Parola yanlışsa kayıt başlamaz ve hata gösterilir. Senaryo kaydedilirken bu bilgiler otomatik giriş ayarı olarak önceden doldurulur (oturum geçerliyse giriş atlanır, düşmüşse yeniden yapılır).

## Hesap kilitlenmesine karşı giriş koruması

Otomatik giriş (form modu) art arda **3 kez** başarısız olursa (kullanıcı adı/parola hatalı, giriş sayfası hata veriyor, giriş tamamlanmıyor) APP_MON girişi **durdurur**: senaryo "Giriş bilgileri hatalı" hatasıyla başarısız görünür (alarm üretir) ve siteye bir daha giriş denemesi yapılmaz. Başarılı bir giriş (veya geçerli oturum) sayacı sıfırlar. Bir çalıştırmada en çok bir giriş denemesi yapılır. Parolayı değiştirdiyseniz Senaryolar sayfasında **Girişi yeniden dene** ile yeni bilgileri girin (`POST /api/scenarios/{id}/retry-login`); sayaç sıfırlanır. Kayıt başlatırken ve "Dene" butonunda tek deneme yapılır, sayaç etkilenmez. Migrasyon 010.

## Giriş ekranı analizi

Kayıt başlatırken "Bu sistemde kimlik doğrulama var" seçilince **Giriş ekranını analiz et** düğmesi giriş sayfasını açar (hiçbir veri göndermeden) ve hangi bilgilerin girilmesi gerektiğini açıklar: kullanıcı adı/parola alanları, giriş düğmesi, ek/zorunlu alanlar ve otomatik girişi engelleyebilecek durumlar (captcha, tek kullanımlık kod/2 adımlı doğrulama, SSO düğmeleri, iki adımlı giriş ekranı). Bulunan alan seçicileri boşsa "Alan seçicileri" kutularına önerilir (`POST /api/recorder/analyze-login`).

## Senaryo düzenleme

Senaryolar listesinde **Düzenle**: ad, aralık, zaman aşımı, etkin/TLS ayarları, adım alanları (ad, adres, yöntem, beklenen durum/metin, en çok süre, seçici…), adım sırası/silme ve (tarayıcı senaryolarında) kimlik doğrulama ayarları değiştirilebilir (`PUT /api/scenarios/{id}`). Başlık, gövde, değişken ve gizli değerler korunur; parola alanı boş bırakılırsa eski parola kalır. Adımlar değişirse sayfa değişikliği taban çizgisi sıfırlanır; kimlik bilgileri değişirse kayıtlı oturum silinir. "HTTP adımlarıyla elle tanımla" bölümü varsayılan olarak kapalıdır, başlığına tıklayınca açılır.

## yama18 — filtreler, uyarı zili, sayfa içi arama/çıktı, raporlar, probe düzenleme

- **Assetler ve Probe'lar listeleri**: arama + durum/tür/koleksiyon/sonuç filtreleri, "x / y kayıt" sayacı.
- **Probe değiştir** (`PUT /api/probes/{id}`): aralık, zaman aşımı, hedef, etkin, HTTP yöntemi/durum/metin/başlık/gövde, TLS ve yönlendirme. Boş bırakılan hedef/başlık/gövde/SNMP community mevcut değeri korur. Tür ve asset değişmez.
- **Üst çubuk**: sayfa içi arama (`/` kısayolu, vurgulu eşleşmeler), yazdır (her sayfa, `@media print`), sağ üstte açık alarm sayısıyla **uyarı zili** (açılır liste, 15 sn'de bir yenilenir).
- **Alarm ve raporlar > Raporlar** (önizlemeli, yazdır/PDF; v2.8.0'dan önce Ayarlar'daydı): Yönetici özeti (executive), Erişilebilirlik, Alarmlar, Envanter, Probe'lar, Topoloji, Denetim kaydı.

- **Dashboard "Kontrol edilen sayfalar"**: kartlar sürükle-bırak ile sıralanır; sağ tık veya × ile dashboard'dan kaldırılır ("Tümünü geri getir" bağlantısı). Sıra/gizleme tarayıcıda (localStorage) saklanır; senaryo silinmez.
- **Uygulama haritası**: bir uygulama seçilince yanında "Topolojide göster" ve "Kontrol edilen sayfalara ekle" düğmeleri çıkar (tarayıcı senaryosu yoksa Senaryolar sayfasına yönlendirir; zaten ekliyse "Kontrol edilen sayfalarda ✓" görünür).
- **Uygulama haritası (Genel bakış)**: varsayılan olarak yalnızca web uygulamaları (web sitesi/servisi) görünür; birine tıklayınca ona bağlı bileşenler açılır, "Yalnızca web uygulamaları" ile kapanır. Topoloji sekmesi tüm bileşenleri gösterir.
- **Yönlendirmeler ve sertifikalar**: kaydedici/yeniden oynatma tarayıcısı HTTP 302, meta ve JS yönlendirmelerini ve başka alan adlarına geçişi izler; kurum içi (self-signed/özel CA) https sertifika hatalarını yok sayar. Sıkı doğrulama için `APM_BROWSER_STRICT_TLS=true`.

## yama25 — Apache arkasında, internetsiz tek komut kurulum
- `deploy/apache/kur-apache.sh` + `README-KURULUM.txt`: Apache (apache2/httpd) ters vekil + systemd + PostgreSQL kurulumu, internetsiz. Paket: `appmon-apache.tar.gz` (ikili dosyalar + betik). Kullanım: `sudo ./kur-apache.sh`.
- Arayüz artık Google Fonts yüklemez (kapalı ağda gecikme olmasın); sistem yazı tipleri kullanılır, CSP dış kaynağa izin vermez.

## yama26 — Yetki rolleri

| Rol | Yetki |
|---|---|
| admin (Yönetici) | Her şey: lisans, API anahtarları, denetim, yapılandırma |
| operator (İşletmeci) | Asset/probe/senaryo/alarm kuralı ekleme-değiştirme-silme; anahtar, lisans, denetim yok |
| viewer (İzleyici) | Salt okunur; yazma düğmeleri kilitli, sunucu 403 döner |
| agent (Ajan) | Yalnızca veri gönderimi (ingest) |

Rol, API anahtarı oluştururken seçilir (Lisans ve anahtarlar). Üst bar'da rol rozeti görünür; yetkisiz sayfalar menüden gizlenir. Sunucu tarafı denetimi esastır, arayüz kilidi yalnızca kolaylıktır.

### Mevcut kurulumu güncelleme (veri korunur)
Migration `011_roles.sql` açılışta otomatik uygulanır; yalnızca `api_keys.role` kısıtını genişletir, veri silmez. Mevcut anahtarlar (admin/agent) aynen çalışır.
1. Önerilen yedek: `sudo -u postgres pg_dump appmon > appmon-yedek.sql` (gömülü PG ise paketteki pg_dump ile, port 5433).
2. Yeni paketi aç, `sudo ./kur-apache.sh` çalıştır — `/etc/appmon/appmon.env`, veritabanı ve anahtar korunur; sadece ikili dosya değişir.

## yama27 — Sunucu kaynakları probe'u (agent'sız CPU / RAM / Disk) ve SNMPv3

- Yeni probe türü **Sunucu kaynakları (host)**: hedef sunucunun SNMP'sinden (Host Resources MIB) CPU, RAM ve tüm diskler okunur. Linux için `snmpd`, Windows için "SNMP Hizmeti" açık olmalı. Ölçümler agent'ın kullandığı `host.*` adlarıyla yazıldığı için hazır alarm kuralları (Yüksek CPU, Yüksek bellek, Disk dolmak üzere) hiçbir ayar yapmadan çalışır. Her disk ayrıca `host.disk_pct.<ad>` olarak yazılır (ör. `host.disk_pct./var`, `host.disk_pct.C:\`); bölüme özel kural için bu adı kullanın.
- **SNMPv3 (USM)**: kimlik doğrulama MD5, SHA, SHA-224/256/384/512; gizlilik DES, AES-128. noAuthNoPriv, authNoPriv ve authPriv düzeyleri. `snmp` ve `host` probe'larında geçerli. Parolalar (en az 8 karakter) ve topluluk adı gizli saklanır; düzenlerken boş bırakılırsa eski değer korunur.
- Linux RAM: `Cached memory` ve `Memory buffers` satırları kullanılan bellekten düşülür. CPU: `hrProcessorLoad` ortalaması (yoksa UCD `ssCpuIdle`).
- Veritabanı: `012_host_probe.sql` yalnızca tür kısıtını genişletir; mevcut veri değişmez, açılışta otomatik uygulanır.
- Henüz yok: SSH ile ölçüm, WinRM, SNMP trap alımı.

## yama28 — Alarm panosu

Alarmlar sayfasının üstünde **Alarm panosu**: genel durum şeridi, sorunlu hizmetlerin kartları, sağlıklı hizmetler ve son 24 saatin alarm grafiği. Hizmetler en kötü durumuna göre renk ve şekille gösterilir: Kritik = kırmızı sekizgen, Erişilemiyor = kırmızı çarpılı daire, Uyarı = sarı üçgen, Bilgi = mavi daire, Sağlıklı = yeşil daire. Yalnızca arayüz değişikliğidir; veritabanı ve API değişmedi.

## yama29 — Bildirimler, bakım pencereleri, üstlenme, kapsam, grup/etiket, görsel yenileme

- **Bildirim kanalları** (Bildirimler sayfası, yalnızca admin): SMTP (STARTTLS/SSL), Microsoft Graph, Exchange EWS (Basic), Windows MAPI (Outlook, yalnızca Windows'ta yerel çalışan APP_MON), Webhook, Slack, Teams, Telegram. Her kanalda en düşük önem ve "çözüldü bildirimi" seçeneği vardır; **Test gönder** düğmesi vardır. Parola/secret/token alanları salt-yazılır: okunurken maskelenir, düzenlemede boş bırakılırsa eski değer korunur.
- **Bakım pencereleri**: tek seferlik, günlük veya haftalık; hizmet/grup/etiket kapsamlı. Pencere sırasında alarm açılır ama bildirim gönderilmez, "bakımda" etiketi görünür.
- **Üstlenme (ack)**: açık alarmda "Üstleniyorum" / geri al.
- **Kural kapsamı**: kural tüm hizmetlere veya seçili hizmet / grup / etikete uygulanır.
- **Grup ve etiket**: Assetler sayfasında düzenlenir, filtrelenir.
- **Ortam değişkeni** `APM_PUBLIC_URL`: bildirimlerdeki bağlantının adresi (ör. `https://apm.sirket.local`).
- **Veritabanı**: `013_notifications.sql` yalnızca yeni tablo/sütun ekler; mevcut veri değişmez, açılışta otomatik uygulanır.
- MAPI Docker/Linux'ta çalışmaz (SMTP/Graph/EWS kullanın). EWS modern kimlik doğrulamayı (OAuth) desteklemez.
- Kullanım kılavuzu: `KULLANIM-KILAVUZU.md`; uygulamada menüde **Yardım** sayfası olarak da açılır (`web/static/kilavuz.md`, KILAVUZ değişince ikisini de güncelleyin).

## yama30 — SIEM'e log gönderimi, Yardım sayfası

- **SIEM hedefleri** (Bildirimler sayfası, yalnızca admin): denetim kayıtları ve alarm açılış/düzelme olayları Syslog UDP/TCP/TLS veya HTTP (Splunk HEC, genel JSON) ile iletilir. Biçim: RFC 5424, CEF, JSON. Facility, app-name, alarm önem eşiği ayarlanır; "Deneme gönder" vardır. Token gizli saklanır (boş bırakılırsa korunur). SaaS modunda özel ağ adresleri engellenir.
- Bakım penceresindeki alarmlar SIEM'e iletilir ("bakım penceresinde" notuyla); bildirim kanalları ise susturulur.
- **Yardım** menüsü: kılavuz uygulama içinden okunur (`web/static/kilavuz.md`; `KULLANIM-KILAVUZU.md` ile aynı tutulur).
- Veritabanı: `014_siem.sql` yalnızca yeni tablo ekler; veri değişmez, açılışta otomatik uygulanır.

## yama31 — Kısmi erişim (sarı), sürüm numarası, çevrimdışı güncelleme paketi

- **Kısmen erişilemiyor (degraded)**: bir asset'in tüm probe ve senaryolarının son sonucuna bakılır; hepsi başarılı = up, hepsi başarısız = down, karışık = degraded (sarı). Hazır kurallar: "Servis erişilemiyor" (`asset.down > 0`, kritik) ve yeni "Servis kısmen erişilemiyor" (`asset.degraded > 0`, uyarı). Migration `015_degraded.sql`: durum kısıtını genişletir, `probes.last_ok` ekler, değiştirilmemiş hazır kuralı günceller ve uyarı kuralını ekler; veri silinmez.
- **Sürüm**: `internal/version` (varsayılan v1.0.0, `-ldflags "-X appmon/internal/version.Version=..."` ile değişir). Üst çubukta, Ayarlar > Sistem'de ve `/healthz` yanıtında görünür.
- **Çevrimdışı (RHEL) güncelleme paketi**: `./paketle-guncelleme.sh [sürüm]` → `dist/appmon-guncelleme-<sürüm>.tar.gz` (sunucu+agent ikilileri, `guncelle-apache.sh`, SHA256SUMS, kılavuz). Sunucuda `sudo ./guncelle-apache.sh`: yedek → yükle → yeniden başlat → sağlık kontrolü → başarısızsa otomatik geri dönüş. Ayrıntı: `deploy/apache/OKU-GUNCELLEME.txt`.

## yama32 — Kurum içi CA desteği (v1.0.1)

- `APM_CA_FILE`: PEM dosyasındaki ek sertifika yetkileri sistem havuzuna eklenir; http/tls probe'ları ve senaryolar bu CA ile imzalı https sitelerine güvenir. Docker'da `./certs/ca.pem` (compose ile `/certs` olarak bağlanır). Dosya yoksa yalnızca uyarı yazılır.
- "unknown authority / x509" hatalarına çözüm ipucu eklendi (CA'yı tanıtın veya probe'ta doğrulamayı kapatın).

## yama33 — Arayüzden güncelleme (v1.0.2)

- **Güncelleme** sayfası (admin): paket (.tar.gz) yüklenir → sunucu doğrular (`internal/update`: güvenli açma, manifest + SHA256, ELF, mimari, sürüm geri gitmeme, isteğe bağlı Ed25519 imza) ve `APM_UPDATE_DIR/staged-<id>` altına hazırlar → "Uygula" `apply.json` bırakır.
- **Uygulayıcı**: root yetkili systemd hizmeti (`appmon-update.path` → `appmon-update.service` → `/usr/local/sbin/appmon-guncelle --uygula-istegi`, `guncelle-apache.sh`'in kurulu kopyası): sağlama/imza yeniden doğrulama → DB yedeği → ikili değiştirme → yeniden başlatma → sağlık kontrolü → başarısızsa otomatik geri dönüş; sonuç `status.json`. Paketin içindeki betik asla çalıştırılmaz.
- Kurulum: güncelleme paketindeki `guncelle-apache.sh` bir kez elle çalıştırılır (hizmeti, `APM_UPDATE_DIR` ve isteğe bağlı `APM_UPDATE_PUBKEY` ayarını kurar). Docker'da kapalıdır.
- İmza: `./paketle-guncelleme.sh --anahtar-uret`, `--imzala`, sunucuda `guncelle-apache.sh --imza-anahtari`.
- API: `GET/POST/DELETE /api/system/update`, `POST /api/system/update/apply` (yalnızca admin).

## yama34 — Sayfa değişikliği ayrıntıları (v1.0.3)

- "Sayfa değişti" rozetleri tıklanabilir; ayrıntı penceresi adım/sayfa bazında eklenen ve kaldırılan öğeleri (başlık, buton, bağlantı, form, giriş, script) okunur Türkçe etiketlerle ve zamanıyla gösterir.
- API: `GET /api/scenarios/{id}/last-change` (read yetkisi).

## yama35 — Kart ayrıntıları (v1.0.4)

- Genel bakıştaki "Kontrol edilen sayfalar" kartlarında durum rozeti/hata metni tıklanınca ayrıntı penceresi: hata sebebi + başarısız adım, sayfa değişiklikleri, son çalıştırma özeti.

## yama36 — Kart rengi son 3 kontrole göre (v1.0.5)

- Genel bakış kartları: son 3 çalıştırma hepsi başarısız = kırmızı, karışık = sarı (aralıklı hata), hepsi başarılı = yeşil. Yalnızca görünümü etkiler; alarm kuralları değişmedi.

## yama37 — Topoloji ana cihaz görünümü (v1.0.6)

- Topoloji varsayılan olarak yalnızca host/network asset'lerini gösterir; seçilince o cihaz + bağlıları + üzerinde çalışanlar (runs_on, dolaylı dahil). Anahtar düğmesiyle tam görünüm. Yalnızca arayüz.

## yama38 — Topoloji hata/uyarı ayrıntıları (v1.0.7)

- Topolojide seçilen asset için açık alarmlar, hatalı probe/senaryolar ve sayfa değişikliği uyarıları listelenir (mevcut API'lerle; yalnızca arayüz).

## yama39 — Genel bakış sırası + makine kaynak widget'ı (v1.0.8)

- Sıra: Genel bakış, Kontrol edilen sayfalar, Makineler (CPU/RAM/Disk), uygulama haritası ve diğerleri. Yalnızca arayüz (mevcut /api/assets verisi).

## yama40 — Hareketli geçişli arka plan (v1.0.9)

- `body::before` ile aksan renginden türeyen, yavaş kayan degrade; prefers-reduced-motion ve yazdırmada kapalı. Yalnızca CSS.

## yama41 — Alarmları temizle, topoloji hizmet seçici (v1.0.10)

- Genel bakıştan Uygulama haritası kaldırıldı.
- Zil açılır penceresinde "Alarmları temizle": `POST /api/alerts/clear` (write yetkisi, denetim kaydı `alert.clear`).
- Topolojide hizmet açılır listesi: seçilenin tüm doğrudan/dolaylı bağlıları gösterilir.

## yama42 — 16:9 görünüm (v1.0.11)

- Genel bakış için 1920x1080 sabit sahne, ekrana ölçeklenen, tam ekran duvar/TV modu. Yalnızca arayüz.

## yama43 — Ayarlar: sayfa oranı 16:9 çerçeve (v1.0.12)

- Ayarlar > Görünüm > Sayfa oranı: Standart / 16:9 çerçeve (`apm_layout`, `theme.js` ile erken uygulanır). Yalnızca arayüz.

## yama44 — Çözünürlüğe uyumlu geniş düzen, başlık hizası (v1.0.13)

- 16:9 çerçeve kaldırıldı; Ayarlar > Sayfa düzeni: Otomatik (geniş ekranda çok sütun) / Dar. Genel bakışta tüm widget başlıkları kutu dışında. Üst menü 1400-2300 px arasında taşmaz.

## yama45 — Ajanlar (v1.1.0)

- `cmd/agent`: Linux + Windows (saf Go, syscall ile), servis kurulumu (`-install`/`-uninstall`), çevrimdışı tampon, süreç izleme (`-watch`), disk başına metrik, ağ trafiği, CA/TLS seçenekleri.
- Sunucu: `GET /api/agent/files`, `GET /agent/dl/{ad}` (`APM_AGENT_DIR`), Ajanlar sayfası. Docker imajı ve RHEL paketleri Windows ajanını da içerir.

## yama46 — Gelişmiş SNMP, trap alıcısı, yol izleme (v1.2.0)

- Probe türü `snmpx` (şablonlar, arayüz trafiği, özel OID, sayaç→hız), SNMP v1/v2c trap alıcısı (`APM_SNMP_TRAP_ADDR`, `APM_SNMP_TRAP_COMMUNITY`, tablo `snmp_traps`), yol izleme (`POST /api/trace`, probe türü `trace`), Ağ araçları sayfası. Migration 016 (trap tablosu) ve 017 (probe türü kısıtı) yalnızca ekler.

## yama47 — SLA raporu, kullanıcı hesapları, LDAP (v1.3.0)

- SLA: `sla_daily` günlük özet (800 gün), asset başına hedef, `GET /api/sla`, `/api/sla.csv`, `PUT /api/assets/{id}/sla`, SLA sayfası.
- Hesaplar: `users` tablosu, `POST /api/login`, `/api/logout`, `/api/users*`, `/api/me/password`; PBKDF2 parola, süreli oturum (api_keys.user_id/expires_at), hesap ve IP kilidi.
- LDAP bind girişi: `APM_LDAP_URL`, `APM_LDAP_DN_TEMPLATE`, `APM_LDAP_DEFAULT_ROLE` (`internal/ldap`).
- Migration 018 (SLA), 019 (kullanıcılar) yalnızca ekler.

## yama48 — Faz D: DB izleme, log izleme, keşif, yedek, durum sayfası, anomali (v1.4.0)

- `pgstats` probe türü (`db.*`), ajanda `-log ad::dosya::regex` (`log.<ad>.*`), ağ keşfi (`/api/discovery`), yapılandırma yedeği (`/api/export`, `/api/import`), herkese açık `/status` (`/api/public/status`, `APM_STATUS_TITLE`), anomali işi (`anomaly.*_z` + kapalı hazır kurallar).
- Migration 020-022 yalnızca ekler (kısıt genişletme, sütun, kapalı kurallar).

## yama49 — Sayfa değişikliği uyarılarında yoksay kuralları (v1.5.0)

- Tarayıcı senaryosunda adım başına "değeri yoksay" (maskele) ve "öğeyi yoksay" kuralları: `GET/POST /api/scenarios/{id}/ignores`, `DELETE /api/scenarios/{id}/ignores/{rid}`. Kural eklemek parola (hesap oturumu) veya API anahtarının yeniden girilmesini ister; denetim kaydı tutulur.
- Kurallar saklanan ham parmak izine karşılaştırma anında uygulanır; parmak izi sürümü değişmez, ilk kontrol yeniden taban çizgisi olmaz.
- Docker: traceroute için `Dockerfile`'da `setcap cap_net_raw+ep`, `docker-compose.yml`'de `cap_add: NET_RAW` (ham ICMP soketi hatası düzeltmesi).
- Denetim sayfasında başlık süzgeçleri: `GET /api/audit` artık `actor`, `action`, `q`, `from`, `to`, `limit` (≤1000) alır; `GET /api/audit/facets` benzersiz kullanıcı/işlem listesi verir.
- Migration 023 yalnızca tablo ekler.

## yama50 — Senaryo kartında hızlı probe durumu (v1.6.0)

- Senaryolara isteğe bağlı `probe_id` (aynı asset'in probe'u). Seçilmezse davranış değişmez.
- `/api/dashboard` senaryo öğesine `probe` ekler (ok, fail_streak, fresh, maint). Arayüz: probe 2 ardışık hata + taze veri + bakımda değil ise kart kırmızı, kartta neden satırı.
- Probe'larda `fail_streak` sayacı (migration 024, ayrıca `scenarios.probe_id`, probe silinince boşalır).

## yama51 — Probe seçici düzeltmesi (v1.6.1)

- Senaryo formundaki "Hızlı durum probe'u" listesi yalnızca aynı asset'in probe'larını gösterdiği için probe ve senaryo farklı asset adlarındaysa boş geliyordu. Artık tüm probe'lar listelenir (aynı asset önce, diğerleri "Başka asset'ler" altında); sunucu yalnızca probe'un kiracıya ait olmasını ister. Bakım penceresi hem senaryonun hem probe'un asset'i için kontrol edilir.

## yama52 — Genel bakıştan ilgili yere gitme (v1.6.2)

- Genel bakış widget'ları tıklanabilir: KPI kutuları, açık alarm satırları (alarm vurgulanır), asset durumu/türü, en yavaş hedefler, makine kartları, senaryo satırları ve bölüm başlıkları.
- `/api/dashboard` `slowest` öğesine `asset_id` eklendi.
- Makine kartından asset ayrıntısı açma hatası düzeltildi (hashchange seçimi sıfırlıyordu).

## yama53 — Kayıtlı senaryo formu düzeltmesi (v1.6.3)

- Tarayıcı kaydından senaryo oluşturma formunda "probesAll is not defined" hatası giderildi (probe listesi `recorderPanel`e iletilmiyordu; v1.6.0 ile gelen hata).

## yama54 — Kasa, SSH ile CPU/RAM/disk ve disk ayrıntısı (v1.7.0)

- **Yerleşik kasa:** `secrets` tablosu (migration 025), AES-256-GCM (kayıt kimliği/kiracı AAD'ye bağlı), ana anahtar veritabanı dışında: `APM_MASTER_KEY` → `APM_MASTER_KEY_FILE` → `/etc/appmon/master.key` → `APM_DATA_DIR`/`APM_UPDATE_DIR` altında `master.key` (yoksa üretilir). API: `GET /api/vault` (admin, durum), `GET /api/secrets` (işletmeci+, değer yok), `POST/PUT/DELETE /api/secrets` (admin; silme yeniden doğrulama ister, kullanımdaki kayıt silinemez). Arayüz: **Kasa** sayfası.
- **SSH host probe'u:** `host` türünde `method: "ssh"` (`ssh_user`, `secret_id`, `host_key`). Sistemin `ssh` istemcisi kullanılır (`APM_SSH_BIN` ile yol verilebilir), tek sabit salt-okunur uzak komut, sunucu anahtarı ilk bağlantıda sabitlenir (TOFU), adres bir kez çözülüp IP'ye bağlanılır. SaaS modunda kapalı. Yeni metrikler: `host.mem_used_gb/total_gb`, `host.disk_used_gb.<mnt>`, `host.disk_total_gb.<mnt>`.
- **Disk ayrıntısı:** `GET /api/assets/{id}/disks`; makine kartında Disk çubuğu ve asset ayrıntısında "Disk ayrıntısı" tüm bağlama noktalarını gösterir.
- **Dağıtım:** Docker imajına `openssh-client`, `/data` birimi (`appmon-data`) ve `APM_MASTER_KEY` aktarımı; RHEL kur/güncelle betikleri `/etc/appmon/master.key` üretir ve `openssh-clients` yoksa uyarır; `paket-indir-rhel.sh` `openssh-clients` paketini de indirir.
- SSH kimlik bilgisi olarak kasadaki `ssh_key` veya `password` kaydı seçilebilir (parola `SSH_ASKPASS` ile verilir, `sshpass` gerekmez).
- SSH probe'ları yedek dışa aktarmaya girmez; mevcut düz metin sırlar (SNMP topluluğu vb.) henüz kasaya taşınmadı.

## yama55 — Halka grafikler, yeni paletler ve geniş ekran düzeni (v1.7.1)

- `donut()` / `donutPanel()` yardımcıları (SVG, CSP uyumlu): Genel bakış (asset durumu, tür, senaryo), Assetler, Probe'lar, Alarmlar.
- 7 yeni palet (lavender, mint, sand, coral, nord, plum, neon) ve 5 yeni vurgu rengi; kategorik grafik renkleri `--c1..--c8` (doğrulanmış, açık/koyu).
- ≥1500 px: çift sütun sayfalar (Ayarlar, Kasa, Bildirimler, Lisans); `#view[data-page]` özniteliği; tablo hücrelerinde `overflow-wrap: break-word` ve düğme sütunu `nowrap`. Yalnızca ön yüz değişti (sunucu/veritabanı değişikliği yok).

## yama56 — Keşfet (web sitesi keşfi), sayfalama, menü/Genel düzeni (v1.8.0)

- Yeni `internal/crawl` paketi: robots.txt ayrıştırıcı (RFC 9309), CDP ile sınırlı gezgin (sayfa/form/iframe, kaynak, XHR/fetch uç noktaları, dış alan adları, TLS günü), sitemap tohumları, gömülü yol sözlükleri (`wordlists/kisa.txt`, `genis.txt`; hassas dosya yolları yok, test ile korunur), SPA taban çizgisi. Form gönderilmez; tehlikeli bağlantılar (`logout`, `delete`…) gezilmez.
- Migration `026_crawl.sql` (`crawl_jobs`). API: `GET/POST /api/crawl`, `GET /api/crawl/{id}`, `POST .../approve|reject` (admin), `DELETE` (iptal/sil), `GET /api/crawl/wordlists`. Operatör isteği yönetici onayına düşer; sahiplik beyanı zorunlu; saatte 10 istek, tek eşzamanlı iş, sunucu yeniden başlarken yarım işler kapatılır.
- Oturumlu tarama: `browser.Authenticate` (senaryonun giriş ayarı, aynı alan adı şartı, hesap kilidi sayaçları).
- Sayfalama: `table(..., pageSize)` 50 satır; Alarmlar, Denetim (sunucu tarafı `limit/offset`), senaryo geçmişi, trap, Keşfet. Alarm listesi üst sınırı 200→1000.
- Sol kenar çubuğu kaldırıldı (menü üstte); Genel bakışta kartlar eşit yükseklikte, senaryo halkası yan yana.

## yama57 — Açılır menü grupları (v1.8.1)

- `NAV_GROUPS` ile 16 sekme 4 açılır gruba (İzleme, Alarm ve raporlar, Araçlar, Yönetim) ayrıldı; `buildNav()` rol süzgecini korur, boş grup gizlenir; `aria-expanded`, Esc ve odak kaybında kapanma, geçerli sayfanın grubu `.cur`. Yalnızca ön yüz.

## yama58 — Genel bakışta makine seçimi (v1.8.2)

- Migration `027_asset_overview.sql`: `assets.show_overview boolean NOT NULL DEFAULT true` (mevcut makineler görünmeye devam eder). `GET /api/assets` `overview` döndürür; `PATCH /api/assets/{id}` artık `kind` ve/veya `show_overview` kabul eder; `POST /api/probes` isteğe bağlı `show_overview` alır (host probe formunda onay kutusu).
- Arayüz: Makineler bölümü yalnızca `overview !== false` olanları gösterir; asset detayında "Genel bakışta göster" kutusu.


## yama59 — Çok kiracılı yapı (v2.0.0)

- Migration `028_multitenant.sql`: `tenants.slug/disabled/is_root`, `licenses` kota sütunları (probe/kullanıcı/aralık/saklama/izinler), küresel benzersiz kullanıcı adı. Mevcut kurulum ana kiracı ("default", `is_root`) olarak yükselir.
- `internal/app/tenants.go`: `/api/tenants` (liste/oluştur/güncelle/sil), `/api/tenants/{id}/admins`; `Auth.Platform`, guard seviyesi `platform` (kiracı yönetimi, `PUT /api/license`, `/api/system/update*`).
- Kota denetimi: probe/asset/kullanıcı sayısı, en kısa aralık, tarayıcı/Keşfet/SSH izinleri; kiracı başına saklama süresi; durdurulan kiracı zamanlayıcıdan çıkarılır, girişi 403.
- Durum sayfası kiracıya özel: `/status/{slug}`, `GET /api/public/status?t=slug`.
- Ortam: `APM_MAX_TENANTS` (varsayılan 10). Arayüz: Kiracılar sayfası, Lisans sayfasında salt-okunur kota, üst çubukta kiracı adı.
- Test: `APM_TEST_DSN=... go test ./internal/app -run TestTenantIsolation` (boş veritabanı gerekir; uygulamalı çapraz-kiracı erişim, kota, durdurma, silme).

## yama60 — Önceki sürüme dön (v2.0.1)

- `guncelle-apache.sh`: güncelleme sırasında `$UPD/prev.json` (önceki sürüm, yedek yolu) yazılır; `apply.json` içindeki `"action":"rollback"` isteği `.prev` ikilisini geri koyar, sağlık kontrolü başarısızsa mevcut sürümü geri yükler; durum `reverted`. Otomatik geri alma `prev.json`'u siler.
- API: `POST /api/system/update/rollback` (platform), `GET /api/system/update` yanıtında `prev`; denetim `system.update.rollback`. Arayüz: Güncelleme sayfasında "Önceki sürüme dön" düğmesi.
- Test: `TestRollbackRequest`; betik geçici dizinle simüle edilerek denendi.

## yama61 — İmzalı lisans dosyası (v2.1.0)

- Migration `029_license_file.sql`: `licenses.lic_id/lic_customer/lic_issued_at`, `platform_secrets` (kasa ile mühürlü Ed25519 tohumu).
- `internal/app/licfile.go`: `.lic` = `{format:"APPMON-LIC-1", payload(base64 JSON), sig}`; `POST /api/tenants/{id}/license-file` (platform, indirilir), `POST /api/license/file` (kiracı yöneticisi, 64 KB); kiracı kısa adı eşleşmeli, süre/tarih denetimi; lisanslı kiracıda `PUT /api/license` ve kota alanlı `PUT /api/tenants/{id}` 409.
- Arayüz: Kiracılar'da "Lisans üret", Lisans sayfasında .lic yükleme + salt-okunur kota.
- Testler: `TestLicenseSignVerify`, `TestLicenseFileFlow` (APM_TEST_DSN gerekir).

## yama62 — İzlemeyi duraklat / başlat (v2.2.0)

- Migration `030_monitoring_pause.sql`: `tenants.paused/paused_at/paused_by`.
- `internal/app/monitoring.go`: `GET /api/monitoring`, `PUT /api/monitoring` (admin, parola/API anahtarı yeniden doğrulaması); `/api/overview` yanıtında `monitoring`. Zamanlayıcı (probe, senaryo, alarm değerlendirmesi) duraklatılmış kiracıyı atlar; Keşfet isteği ve senaryo "şimdi çalıştır" 409.
- Arayüz: Genel bakışta durum rozeti + düğme, parola diyaloğu, tüm sayfalarda uyarı şeridi. Test: `TestMonitoringPause`.

## yama63 — İngilizce dil seçeneği (v2.2.1)

- `web/static/i18n.js` (yeni): sözlük tabanlı istemci tarafı çeviri (tam eşleşme, `{#}` şablonları, DOM gözlemcisi, alert/confirm/prompt). Dil düğmesi zilin yanında; seçim `localStorage apm_lang`.
- `index.html`, `status.html`: betik eklendi; `app.js`, `reports.js`, `status.js`: tarih/sayı biçimi `window.LOCALE`; yardım sayfası çevrilmez (`data-notr`).
- Migration yok.

## yama64 — Yenilenen kılavuz ve yardım sayfası (v2.2.2)

- `KULLANIM-KILAVUZU.md` / `web/static/kilavuz.md`: sayfa sayfa yeniden yazıldı (hızlı başlangıç, kavramlar, her sayfa, ortam değişkenleri ve metrik başvurusu, SSS, güvenlik notları, sürüm geçmişi).
- `web/static/app.js` yardım sayfası: Markdown çevirici numaralı/iç içe liste, kod bloğu, `>` uyarı kutusu ve vurguyu destekler; kılavuzda arama, bölüm bazlı içindekiler (okunan bölüm vurgulanır), "başa dön" düğmesi. `style.css`: yardım stilleri. Migration yok.

## yama65 — Ajansız uygulama performansı (APM) (v2.3.0)

- Yeni **İzleme › Performans** sayfası (4 sekme): erişim günlüğünden uygulama/uç nokta p50/p95/p99 ve hata oranı, `pg_stat_statements` yavaş sorgular, HTTP gecikme kırılımı (DNS/TCP/TLS/ilk bayt/indirme), SSH ile bağlantı/RTT haritası ve "Topolojiye ekle" önerisi.
- Syslog alıcısı (`internal/app/syslog.go`, UDP+TCP): `APM_SYSLOG_ADDR` ile açılır (varsayılan kapalı; Docker'da .env içine APM_SYSLOG_ADDR=:1514, port docker-compose.yml'de yayımlı), `APM_SYSLOG_ALLOW`, `APM_SYSLOG_RATE`. Anahtar→kiracı eşleme, hız sınırı, yol normalizasyonu, sorgu dizesi saklanmaz.
- Migration 031: `db_statements`, `db_stmt_state`, `host_conns`, `host_addrs`, `http_endpoint_stats`, `probes.last_timing`; `assets.collection` 'logs' kabul eder. Yeni metrikler: `app.*`, `db.slow_queries`, `db.top_query_mean_ms`, `host.conns`, `host.conn_rtt_ms`, `probe.http_*_ms`.
- EN çeviri, kılavuz (Performans bölümü, ortam değişkenleri, metrikler) güncellendi.

## yama66 — Değişiklik-nedensellik motoru (v2.4.0)

- `internal/app/rca.go`: `GET /api/alerts/{id}/rca`. Alarmlı asset ve topoloji bağımlılıkları (≤3 seviye, ≤40 asset) için dakikalık ölçümlerde medyan/MAD tabanlı ani değişim tespiti, başlangıç zamanı, zaman sırası + topoloji yakınlığı ile puanlama (yüksek/orta/düşük güven) ve denetim günlüğündeki yönetim değişiklikleri. Kural tabanlı; dış servis yok.
- Arayüz: alarm satırlarında "Neden?" düğmesi (satır içi panel). EN çeviri, kılavuz bölümü, testler (`rca_test.go`). Migration yok.

## yama67 — Saldırı yüzeyi sapması (v2.5.0)

- Yeni **İzleme › Saldırı yüzeyi** sayfası. `internal/app/surface.go`: referans öğrenme + sapma tespiti (dinleyen port: SSH `ss -tln`; uç nokta ve 4xx patlaması: erişim günlüğü; TLS sürümü, güvenlik başlıkları, banner: HTTP probe). İlk gözlem referans, uç noktalar için 24 sa öğrenme; gözden geçirme (not + denetim kaydı), referans sıfırlama; `surface.drift` ölçümü ve "Güvenlik" alarm şablonu.
- Probe: `ConnSnapshot.Listen/ListenOK`, `HTTPSurface` (TLS sürümü, 5 güvenlik başlığı, Server banner). Pasif: ek tarama yok.
- Migration 032: `surface_state`, `surface_items`, `surface_events`. API: `GET /api/surface/events|baseline`, `POST /api/surface/events/{id}/review`, `POST /api/surface/reset`. Testler `surface_test.go`. EN çeviri ve kılavuz bölümü.

## yama68 — Kanıt zinciri (v2.6.0)

- `internal/app/evidence.go`: denetim günlüğü kiracı başına SHA-256 hash zinciri (`audit()` artık `auditChained`: danışma kilidi + `audit_head`), zincir doğrulama (değişiklik/araya ekleme/silme/kuyruk silme tespiti), Ed25519 ile imzalı aylık kanıt raporu (SLA, alarmlar, yönetim değişiklikleri, zincir başı), paket indirme ve doğrulama, OpenSSL ile bağımsız doğrulama.
- Migration 033: `audit_log.prev_hash/hash`, `audit_head`, `evidence_keys` (özel anahtar kasa ile şifreli), `evidence_reports`. Arayüz: Alarm ve raporlar › Kanıt zinciri (yönetici). Testler `evidence_test.go`. EN çeviri ve kılavuz bölümü.

## yama69 — Genel bakış pano şablonları ve Performans widget'ları (v2.6.1)

- Genel bakış widget tabanlı yapıldı (12 sütunlu ızgara): 9 mevcut + 5 yeni widget (uygulama performansı, p95 sıralaması, yavaş sorgular, gecikme kırılımı, saldırı yüzeyi). Yeni widget'lar mevcut API'leri kullanır; sunucu tarafı değişikliği ve migration yok.
- 5 hazır şablon (Operasyon, Performans, Güvenlik, NOC, Yönetici özeti); düzenleme modunda sırala/boyutlandır/ekle/kaldır, "Yeni pano olarak kaydet" (en çok 20, tarayıcıda `apm_dash_layouts`). Düzenleme sırasında 10 sn'lik otomatik yenileme durur.
- EN çeviri (c12), kılavuz ("Pano şablonları ve widget'lar") ve sürüm tablosu güncellendi.

## yama70 — Eski SSH sunucusu uyumluluğu (v2.6.2)

- `probe.Options.SSHLegacy` (`ssh_legacy`): host probe'unda SSH için isteğe bağlı kutu; `KexAlgorithms/HostKeyAlgorithms/PubkeyAcceptedKeyTypes/Ciphers/MACs` için `+` ile eski algoritmalar (modern olanlar yerinde, varsayılan kapalı, probe başına).
- `sshError`: "no matching ..." hatalarında sunucunun sunduğu algoritmaları gösteren ve kutuyu/RHEL 9 `update-crypto-policies` ipucunu veren mesaj. Testler: `TestSSHLegacyOptionAndNegotiationHint`, `TestValidateOptionsSSH`. Migration yok.

## yama71 — SSH uzak çıkış kodu (v2.6.3)

- Uzak betik `; true` ile biter; ssh 255 dışı bir kodla dönse bile çıktı geldiyse ölçümler okunur (eski makinelerde `hostname -I` hatası "exit status 4"e yol açıyordu). Çıktı yoksa mesaj çıkış kodunu ve nedeni söyler. Test: `TestSSHRemoteExitCodeTolerated`.

## yama72 — SSH veri seçimi ve Sunucu ayrıntısı (v2.7.0)

- `probe.Options.Collect` (cpu, mem, swap, disk, uptime, load, conns, listen, addr) ve `Mounts`: uzak betik `buildSSHScript` ile yalnızca seçilenler için kurulur (hâlâ sabit, salt-okunur, tek tırnaksız, `; true` ile biter). `collect` boşsa eski davranış. Yeni metrikler: `host.swap_*`, `host.load1/5/15`. Dizin filtresi `mountWanted`.
- `ConnSnapshot.SkipConns/SkipAddrs`: bağlantı/adres seçilmediyse mevcut `host_conns`/`host_addrs` kayıtları silinmez, `host.conns` yazılmaz.
- `GET /api/assets/{id}/hostinfo` (`hostinfo.go`) ve arayüzde **Sunucu ayrıntısı** penceresi (özet, dizinler, IP, portlar, bağlantılar). Probe formunda veri kutuları ve dizin alanı. Doğrulama: `handlers.go` (collect/mounts). Testler: `TestSSHCollectSelection`, `TestSSHMountFilter`, `TestValidateOptionsSSHCollect`. Migration yok.

## yama73 — Probe ayrıntıları (v2.7.1)

- `probe.Details`/`Result.Detail`: her probe türü son ölçümün ayrıntılarını (HTTP durum/başlıklar, TLS sertifika alanları, DNS cevapları, ICMP min/ort/maks, NTP stratum/sapma, SNMP OID/değer, yol izleme sıçramaları, PostgreSQL sürümü, host yöntemi) toplar; gizli değer yazılmaz.
- Migration 034: `probes.last_detail`. `GET /api/probes/{id}/detail` (`hostinfo.go`): ayrıntılar, durum, 24 saat özeti (asset bazlı), son metrikler. Arayüz: Probe'lar sayfasında **Ayrıntı** düğmesi.
- Testler: `detail_test.go` (HTTP, TCP, TLS sertifikası, `Details` sınırları).

## yama74 — Asset/topoloji/liste iyileştirmeleri ve probe widget'ları (v2.7.2)

- **Assetler:** grup/etiket hücreye tıklayınca satır içinde düzenlenir (alttaki form kalktı); satır sonunda tür listesi, **Ayrıntı** ve **Sil** düğmeleri.
- **Topoloji:** hiyerarşi üstten alta (altyapı üstte); yakınlaştırma (+/−/Sığdır, Ctrl+tekerlek, sürükleyerek kaydırma); harita yüksekliği 760; bağlantısız asset'ler ızgarada, geniş katmanlar satırlara bölünür; kayıtlı konum anahtarı `apm_topo_pos2` (eski konumlar sıfırlandı).
- **Bağımlılık yayılımı:** `GET /api/topology` düğümlerine `impacted` ve `impacted_by` eklendi (`impactedBy`, `topology.go`): bağımlılık zincirinde down/offline varsa bağlı asset haritada kırmızı görünür. Asset'in gerçek durumu ve `root_causes` değişmez.
- **Listeler:** `table()` sayfalamayı üst ve alta koyar, 8 satırdan uzun listelere tüm satırlarda çalışan arama süzgeci ekler; süzgeç ve sayfa otomatik yenilemede korunur. Denetim günlüğünde üst sayfa düğmeleri.
- **Pano widget'ları:** Probe durumu, Hatalı probe'lar, Probe türleri, Sertifika süreleri, Sağlıksız asset'ler, Gruplara göre asset durumu, Kök neden ve etkilenenler; yeni "Probe'lar" şablonu, NOC/Güvenlik/Yönetici şablonları güncellendi.
- Test: `TestImpactedBy` (`topology_test.go`). Kılavuz ve İngilizce çeviri güncellendi.

## yama75 — Ajan uygulama keşfi (v2.7.3)

- **Ajan:** süreçleri adına göre toplar, servisleri ve dinlenen portları bulur; CPU/bellek (`proc.<ad>.cpu_pct/.mem_mb`, en yoğun 10) ve bilinen günlüklerde (nginx, httpd, postgresql, mysql, redis, mongodb, sshd, sistem) hata/uyarı sayısı (`log.auto_<ad>.count/.warn`) gönderir. Yalnızca sayılar ve maskeli son hata örneği. `-no-apps`/`APM_APPS=off`, `-root`/`APM_ROOT`, `APM_APPS_EVERY`.
- **Sunucu:** migration 035 `asset_apps`; `/api/ingest` isteğe bağlı `apps`; `GET /api/assets/{id}/apps`.
- **Arayüz:** Assetler → Ayrıntı → "Uygulamalar (ajan)".
- Test: `cmd/agent/apps_test.go`, `internal/app/agentapps_test.go`. Windows ajan kodu yalnız derleme denetiminden geçti.

## yama76 — Asset ayrıntısı pencere (v2.7.4)

- Assetler → **Ayrıntı** (veya satıra tıklama) probe ayrıntısı gibi pencere açar (`assetDialog`); sayfa altındaki bölüm kalktı. Esc/Kapat ile kapanır; Genel bakış'taki makine kartından gelişte de pencere açılır.

## yama77 — Ajanları uzaktan güncelleme (v2.7.5)

- **Ajan:** heartbeat etiketinde `agent_sha` (kendi SHA-256'sı) bildirir; yanıttaki `update` teklifini (`name`, `sha256`) uygular. Linux: istek dosyası + `ExecStartPre=-+ … -apply-update` (root, sunucudan indirir, SHA-256 ve `-version` denetimi, ikiliyi değiştirir); systemd birimine `StateDirectory=appmon-agent` eklendi. Windows: SYSTEM indirir, çalışan exe'yi yeniden adlandırıp değiştirir, çıkar; görev ayarları (süre sınırı yok, başarısızlıkta 1 dk'da yeniden başlat) kurulumda yapılır. `-no-update` / `APM_UPDATE=off`.
- **Sunucu:** migration 036 (`assets.agent_update_req`, `tenants.agent_auto_update`); `GET /api/agent/updates`, `POST /api/agent/update` (`asset_ids`/`all`/`cancel`), `PUT /api/agent/auto-update` (yönetici); ingest yanıtına `update` teklifi.
- **Arayüz:** Ajanlar → makineler tablosunda Güncelleme sütunu, Güncelle / Tümünü güncelle / Vazgeç, Otomatik güncelle.
- Test: `cmd/agent/update_test.go`, `internal/app/agentupdate_test.go`. Windows kodu yalnız derleme denetiminden geçti.

## yama78 — Ajan web günlüğü, ajan sürümü, pano çizgileri (v2.7.6, ajan 1.1.0)

- **Ajan 1.1.0:** nginx/Apache (`/var/log/nginx`, `/var/log/httpd`, `/var/log/apache2`, `/var/log/appmon/access.log`) ve IIS (`inetpub\logs\LogFiles\W3SVC*`) erişim günlüklerini okur; uç nokta özetini (istek, 4xx/5xx, gecikme kovaları, süresiz istek sayısı) ingest'teki `access` alanıyla yollar. Combined/common, appmon KV ve IIS W3C biçimleri. `-no-weblog` / `APM_WEBLOG=off`. Durum `labels.weblog`.
- **`-web-setup` / `-web-undo` / `-dry-run` / `-yes` (Linux, root):** `appmon-access.conf` yazar (log_format `appmon_fmt`, `/var/log/appmon/access.log`), `nginx -t` / `httpd -t` ile sınar, reload eder; hata olursa geri alır; sanal sunucu düzeyi `access_log`/`CustomLog` satırlarını listeler; logrotate + SELinux etiketi. Riskler Ajanlar sayfasında ve komut çıktısında.
- **Sunucu:** migration 037 (`http_endpoint_stats.untimed`); `mergeAgentAccess` (ajan özetleri syslog ile aynı 5 dk toplamlarına eklenir); süresiz isteklerde ortalama/p50/p95/p99 üretilmez ("süre yok"); `InitAccess` yazma döngüsünü syslog alıcısından bağımsız başlatır; ortak `internal/weblog` paketi (kovalar, yol normalleştirme).
- **Ajan sürümü:** `internal/version/agent.go` (şimdi `internal/agentver`; `Agent = "1.1.0"`, SemVer, yalnızca ajan kodu değişince artar); paketleme artık `-X main.Version` vermez, böylece sunucu sürümü değişince ajan ikilisi (SHA-256) değişmez. `GET /api/agent/updates` → `bundled_version`.
- **Arayüz:** Ajanlar: Web günlüğü sütunu ve risk uyarılı "Web sunucusu erişim günlüğü" paneli; Genel bakış: bölümler arası belirgin, temaya uyumlu ayırıcı çizgiler.
- Test: `cmd/agent/weblog_test.go`, `internal/app/agentaccess_test.go`.

## yama79 — Windows ajan güncellemesi sonrası yeniden başlatma (v2.7.7, ajan 1.1.1)

- **Hata:** Windows ajanı kendini güncelleyip çıkınca yeniden başlatma yalnızca zamanlanmış görevin "başarısızlıkta yeniden başlat" ayarına bırakılmıştı; bu ayar eski kurulumlarda yoktu, ajan geri gelmiyordu.
- **Ajan 1.1.1:** değişimden sonra ajan, kendi sürecinden bağımsız küçük bir yardımcıyla ~4 sn sonra `schtasks /Run` çalıştırır (görev ayarına bağımlı değil; zaten çalışıyorsa zararsız). Açılışta `appmon-agent.old.exe` silinir.
- Not: 1.1.0 ajanı yüklü makineler bu güncellemeyi alırken yine eski davranışı gösterebilir; ajan geri gelmezse görev bir kez elle başlatılır (`Start-ScheduledTask -TaskName 'APP_MON Agent'`). 1.1.1'den sonrası otomatik.

## yama80 — Gecikme kırılımı yorumları (v2.7.8)

- İzleme → Performans → Gecikme kırılımı: "Yorum" sütunu artık davranışsal. Aşama payı + mutlak süre eşikleriyle (DNS 100, TCP 150, TLS 250, sunucu yanıtı 800, indirme 500 ms) durum etiketi (Sağlıklı / İzlenmeli / uyarı / yavaş / Ölçüm yok), yorum ve olası neden–bakılacak yer üretir; "en büyük pay" ayrıca yazılır.
- Yeni desenler: yeni bağlantı maliyeti yüksek (bağlantı + TLS baskın, sunucu hızlı), tek aşamada toplanmayan yavaşlık. Yalnızca arayüz değişti (`app.js`, `style.css`, `i18n.js`); ajan 1.1.1 aynı.

## yama81 — Performans: DNS, Ağ servisleri, Yol izleme (DSCP) (v2.7.9)

- **API:** `GET /api/perf/network?window=1h|24h|7d` — dns/tls/tcp/icmp/udp/ntp/trace probe'larının son durumu, dönem istatistikleri (ort., p95, başarı %, önceki dönem ortalaması) ve son ek ölçümler (sertifika günü, NTP sapması, yol metrikleri). Bir asset'te birden çok probe varsa `shared: true`.
- **Arayüz:** Performans'a DNS, Ağ servisleri ve Yol izleme sekmeleri; davranışsal yorumlar (eşikler kılavuzda). DNS'te aynı ad birden çok sunucuda izleniyorsa cevap karşılaştırması.
- **İşaretli paket:** trace probe seçeneği `dscp` (0-63) ve `POST /api/trace` `dscp` alanı; ham ICMP soketine `IP_TOS` yazılır. Sıçrama başına DSCP, "TTL doldu" cevabının geri alıntıladığı IP başlığından okunur (`dscp_in`); `mark_changed_ttl` işaretin ilk değiştiği sıçrama. Sıçramaya `jitter_ms` ve eşleşen asset adı (`asset`) eklendi. Yeni metrik `net.path.dscp_changed_ttl`.
- **Ajan 1.1.2:** ajanın sürüm sabiti `internal/agentver` paketine taşındı (eskiden `internal/version/agent.go`). Ajan ikilisi eskiden sunucu sürüm dizgisini de bağladığı için her sunucu sürümünde SHA-256'sı değişiyor ve aynı ajan koduna rağmen "güncelleme var" çıkıyordu; artık sunucu sürümü ajan ikilisini etkilemez (doğrulandı: sürüm dizgisi değişince aynı SHA-256). Bu sürümde ajanlar bir kez güncellenir (1.1.1 → 1.1.2); bundan sonra yalnızca ajan kodu değişince güncelleme çıkar.

## yama82 — Raporlar taşındı, SLA zaman filtreleri ve SLA sıfırlama (v2.8.0)

- **Raporlar:** Ayarlar sayfasındaki raporlar "Alarm ve raporlar › Raporlar" sayfasına taşındı (`reports.js` aynı; yeni görünüm `raporlar`).
- **SLA saatlik özet:** migrasyon `038_sla_hourly.sql` — `sla_hourly(asset_id, hr, hod, dow, observed_s, down_s, maint_s, degraded_s, incidents)`; günlük özet artık saatlik özetten türetilir (aynı sonuç). 400 gün tutulur; açılışta ham ölçüm süresi kadar geriye doldurulur.
- **API:** `GET /api/sla` ve `/api/sla.csv` yeni parametreler: `from`/`to` (`YYYY-AA-GG` veya `YYYY-AA-GGTSS:DD`), `hours=8-17|7-24|22-6`, `days=1,2,3,4,5` (0 = Pazar), `group=hour|day|week`, `asset`. Süzgeçsiz tam gün aralığı eski günlük yolu kullanır. Yanıta `filter`, `legacy`, `hourly_from` ve `series` eklendi.
- **Arayüz:** dönem (ay/bugün/son 24 saat/hafta/7/30 gün), zaman filtresi (7/24, mesai 08-17 Pzt–Cum, 07-24, özel saat ve gün), gruplama (saatlik/günlük/haftalık kırılım).
- **SLA sıfırlama:** `POST /api/sla/reset {asset_ids | all, password | api_key}` (yalnızca yönetici, parola/anahtar yeniden istenir, IP başına deneme sınırı). `assets.sla_reset_at` kolonu: sıfırlama anından önceki ölçümler özetlere girmez (yeniden özetleme geri getirmez); `sla_daily` ve `sla_hourly` satırları silinir; denetim kaydı `sla_reset`.

## yama83 — Dağıtık izleme: OTLP trace (v3.0.0)

- **Alım:** `POST /v1/traces` (agent rolü). OTLP/HTTP **protobuf** (bağımlılıksız asgari ayrıştırıcı, `internal/app/otlp_pb.go`) ve **JSON**; `Content-Encoding: gzip`; istek başına en çok 10.000 span, açılmış gövde en çok 32 MiB. Her `service.name` asset olarak otomatik kaydedilir (lisans sınırına tabidir). Span `(tenant, trace_id, span_id)` ile tekildir; yeniden gönderim çoğaltmaz. Geçersiz kimlikli span'lar atlanır.
- **Veri:** migrasyon `039_traces.sql` — `spans` (kiracıya `ON DELETE CASCADE` bağlı; trace_id, span_id, parent_id, service, name, kind, start_time, dur_us, status, status_msg, attrs jsonb). En çok 14 gün saklanır (`purgeSpans`, lisans saklama süresi daha kısaysa o).
- **API:** `GET /api/traces` (`since=15m|1h|6h|24h|7d`, `service`, `status=error`, `min_ms`, `q`, `sort=slow`), `GET /api/traces/{id}` (span'lar ve başlangıca göre `offset_us`), `/api/traces/services` (RED: çağrı, hata, p50/p95/p99 — giriş span'ları: sunucu/tüketici veya kök), `/api/traces/operations?service=`, `/api/traces/map` (servisler arası ebeveyn-çocuk geçişleri + çocuğu olmayan istemci span'larından veritabanı/dış servis kenarları).
- **Arayüz:** İzleme › Dağıtık izleme — Trace'ler (süzgeçler + şelale + span öznitelikleri), Servisler, Servis haritası; OpenTelemetry yönergesi.
- **Testler:** protobuf ayrıştırıcı, JSON+protobuf+gzip alım, idempotency, süzgeçler, ayrıntı, servis özeti, harita, saklama.

## yama84 — Kendi kendini izleme (v3.1.0)

- **Ne yapar:** `guard` ara katmanı, kimliği doğrulanmış her API isteğini sunucu span'ı olarak (`appmon-server`, kind=2) `spans` tablosuna yazar; OpenTelemetry kütüphanesi gerekmez (`internal/app/selftrace.go`). Span adı `r.Pattern`; öznitelikler: `http.request.method`, `url.path` (sorgu dizesi yok), `http.response.status_code`, `enduser.role`, `service.version`. 5xx → hata durumu; 4xx hata sayılmaz.
- **Bağlam:** gelen W3C `traceparent` ayrıştırılır; geçerliyse span o trace'e ve ebeveyn span'a bağlanır, `sampled` bayrağı varsa her zaman saklanır.
- **Örnekleme:** hata (5xx), ≥ 1 sn yavaş ve çağıranın örneklediği istekler her zaman; diğerleri `APM_SELF_TRACE_SAMPLE` (varsayılan 0,2). `APM_SELF_TRACE=0` kapatır. Atlananlar: `/v1/traces`, `/v1/metrics`, `/api/ingest`, `/api/traces*`.
- **Maliyet:** span'lar bellek içi kuyrukta (4096) toplanır, 2 sn'de bir toplu yazılır; kuyruk doluysa span atılır, istek yavaşlamaz. Servis asset sayılmaz; kiracı yalnızca kendi isteklerini görür.
- **Testler:** `traceparent` ayrıştırma, örnekleme 0/1, bağlama, 4xx/5xx durumu, asset oluşmaması, servis özetinde görünme.

## yama85 — Trace kurulum betikleri (v3.2.0)

- **Betikler:** `web/static/kurulum/otel-kurulum.sh` (bash, Linux/systemd) ve `otel-kurulum.ps1` (PowerShell 5.1+, Windows servisi/IIS). Her ikisi: bağlantı + anahtar denemesi, gerçek deneme trace'i, ortam değişkeni yazma (`--unit`/`-WindowsService`/`-IIS`/`--env-file`), dile göre ajan/paket kurulumu (Java, Node, Python, .NET), `--undo`, `--dry-run`. Betikler yalnızca ASCII içerir (Windows PowerShell 5 BOM'suz UTF-8'i bozmasın diye).
- **Dağıtım:** `GET /kurulum/otel-kurulum.sh|.ps1` (kimlik doğrulamasız; sır içermez). `__APPMON_URL__` yer tutucusu `APM_PUBLIC_URL` veya isteğin adresiyle (Host doğrulanır) doldurulur.
- **Güvenlik:** ajan uzaktan komut çalıştırmaz; kurulum, hedef sistemde yöneticinin bilinçli çalıştırdığı bir betiktir. Linux drop-in dosyası 0640, ortam dosyası 0600; Windows'ta yalnızca betiğin eklediği anahtarlar izlenip geri alınır.
- **Arayüz:** Dağıtık izleme › "Trace nasıl gönderilir?" bölümüne indirme bağlantıları ve kopyalanabilir komutlar.
- **Test durumu:** Linux betiği sandbox'ta uçtan uca denendi (bağlantı, yanlış anahtar, drop-in, env-file, undo, dry-run, geçersiz parametreler). PowerShell betiği ve dile özel kurulumlar (internet gerektirir) bu ortamda çalıştırılamadı; ilk kullanımda `-DryRun` ile deneyin.

## yama86 — Ajan: yerel OTLP alıcısı ve bağlantı haritası (v3.3.0, ajan 1.2.0)

- **Yerel OTLP alıcısı:** ajan `127.0.0.1:4318` üzerinde `POST /v1/traces|metrics|logs` ve `GET /healthz` sunar; gelen isteği olduğu gibi (gzip dahil) sunucuya, ajanın anahtarıyla iletir. 32 MB'lık bellek tamponu (dolunca en eski düşer), 408/429/5xx/ağ hatasında üstel geri çekilmeyle tekrar, diğer 4xx'te atma + günlük. Loopback dışı adreste uyarı verir. Ayar: `APM_OTLP` / `-otlp-listen` / `-no-otlp`.
- **Bağlantı haritası:** ajan her uygulama raporuyla birlikte süreç bazlı giden TCP bağlantı özetini (`conns`) yollar (Linux: `/proc/net/tcp*` + `/proc/*/fd`; Windows: `netstat -ano`). Sunucu `net_edges`/`net_hosts` tablolarında (migration 040) saklar, 15 dk içinde güncellenen kenarlardan `conn_edges` üretir; `/api/traces/map` yanıtına eklenir. Doğrulama: en çok 300 kenar, geçerli IP, port 1–65535, ad ≤ 60 karakter. Ayar: `APM_CONNS=off` / `-no-conns`.
- **Arayüz:** Servis haritasında Kaynak seçicisi (birleşik / yalnızca trace / yalnızca bağlantı); bağlantı okları kesikli.
- **Kurulum betikleri:** `otel-kurulum.sh/.ps1` yerel ajanı (`/healthz`) bulursa anahtarsız çalışır (`--via-agent` / `-ViaAgent`).
- **Test durumu:** birim testleri + sandbox'ta ajan → sunucu uçtan uca denendi (via-agent trace, apm-srv→postgres bağlantı kenarı). Windows `netstat` yolu yalnızca derleme/ayrıştırma testindedir; Windows'ta ilk kullanımda doğrulayın. Ajan güncellemesi normal ajan güncelleme akışıyla (1.2.0) gelir.

## yama87 — Ajan yapılandırma dosyası, sıkıştırma, Topoloji sekmeleri (v3.4.0, ajan 1.3.0)

- **Ajan ilkesi:** ajan artık "hafif" bir sayaç değil, makinedeki veri toplayıcıdır; bir uygulamadan veri gerekiyorsa mümkün olduğunda ajan çeker ve sıkıştırarak yollar. "Hafif ajan" ifadesi arayüz ve belgelerden kaldırıldı.
- **Yapılandırma dosyası:** `-install` Linux'ta `/etc/appmon-agent.conf`, Windows'ta `%ProgramData%\appmon-agent\agent.conf` dosyasına tüm `SEND_*` satırlarını açıklamalarıyla yazar (host CPU/bellek/disk/ağ/süreç/açık kalma, `APM_WATCH`, `APM_LOG`, uygulama keşfi, bağlantı haritası, web günlüğü, yerel OTLP, uzaktan güncelleme). Satırın başına `#` konursa o veri gönderilmez; dosyada hiç `SEND_*` yoksa (eski yapılandırma) her şey gönderilir. Yeniden kurulumda seçimler korunur. `agent.up` kalp atışı her zaman gider.
- **Sıkıştırma:** ajan `/api/ingest` gövdesini gzip ile yollar (512 bayt üstü); sunucu `Content-Encoding: gzip` kabul eder (açılmış boyut ≤ 16 MiB). Eski sunucu 400 verirse ajan düz JSON'a döner.
- **Arayüz:** Topoloji'de Harita ve Senaryolar ayrı sekmelerde.
- **Örnek config dosyaları (v3.4.1):** `deploy/agent/appmon-agent.conf` (Linux) ve `agent.conf` (Windows); `/kurulum/appmon-agent-*.conf` adresinden indirilir, güncelleme paketinde `agent-config/` altındadır, `appmon-agent -print-config linux|windows` ile üretilir.

## yama89 — Uzaktan ajan veri ayarı, EDR uyarısı (v3.5.0, ajan 1.4.0)

- **Uzaktan ayar:** Ajanlar tablosunda satır sonunda "Veri ayarı" (onay kutuları; katalog `internal/agentcfg` — ajan ve sunucu aynı listeyi kullanır). `PUT /api/agent/config` (`asset_ids` + `send` veya `clear`), `GET /api/agent/config`. Migration 041 `agent_config` (ajan başına seçim + sürüm). Ajan heartbeat'inde `cfg_v` (uygulanan sürüm), `cfg_send` (etkin türler), `cfg_lock` etiketlerini bildirir; sunucu fark varsa yanıta `config:{v,send}` ekler, satır silindiyse `v:0` ile geri aldırır.
- **Ajan:** ayarı `remote.json` (Linux durum dizini / `%ProgramData%\appmon-agent`) olarak saklar, servisi yeniden başlatır (systemd `Restart=always` / zamanlanmış görev); başlangıçta yerel `SEND_*` üstüne uygular. `APM_REMOTE_CONFIG=off` yerel kilittir. Servis olarak çalışmayan ajan uygulamaz.
- **Arayüz:** Ajanlar sayfasında "Antivirüs / EDR izinleri" uyarı bölümü (Windows ve Linux).
- **Performans sayfası ajanla (aynı sürüm):** `dbstats` yükü (ajan `APM_PG_DSN` ile PostgreSQL `pg_stat_statements` okur; migration 042 `agent_db_state/agent_db_statements`; Performans › Veritabanı sorguları probe verisiyle birleşik), `conns.in` + giden özet → `agent_host_conns`/`host_addrs` (Performans › Bağlantılar), Performans › Uygulamalar'a trace giriş span'lerinden satırlar (`source: trace`). Yeni veri türü `SEND_DBSTATS`.

## yama106 — Ayrıntılı yorumlar ve Asset kartı nedenleri (v3.18.0)

- Saldırı yüzeyi › Sapmalar ve Referans durum, Asset kartı › Güvenlik ve Olaylar (Sapma kaydı) satırlarına kural tabanlı "ne oldu / neden önemli / ne yapılmalı" yorumu (`surfInsight`, `baseInsight`, `richCell`).
- Asset kartı › Genel: Kritik/Uyarı rozetinin altında tüm nedenler ve önerilen çözümler tablosu (alarm, zafiyet, riskli port, destek dışı sürüm, sapma, probe/senaryo hatası, SLA; ilgili sekmeye bağlantı). Yalnızca arayüz değişikliği.

## yama105 — Pano widget'ları: özet şerit, kareler, detay paneli; SSL alarm renkleri (v3.17.0)

- Ağ ve altyapı widget'ları (SSL, DNS, NTP, TCP/UDP, ICMP, yol izleme, veritabanı) ortak `fleet()` bileşenine taşındı: üstte özet şerit (dilime tıklayınca süzer), her probe için küçük kare (50-500 probe sığar), kareye veya "Listeyi aç"a tıklayınca sağdan detay paneli (`sidePanel`). Hâlâ tüm probe'ları kapsar.
- SSL kare renkleri `probe.cert_days_left` alarm kurallarının eşiklerinden ve açık alarmlardan gelir (kural yoksa varsayılan 14 gün uyarı / 3 gün kritik); karede alarm açık (dolu nokta) / üstlenildi (halka) işareti, panelde alarm, üstlenen ve not.
- Eski "Sertifika süreleri (TLS)" widget'ı kaldırıldı (veri kaynağında sertifika süresi olmadığı için hep boş görünüyordu); kayıtlı panolardaki `cert_expiry` otomatik "SSL sertifikaları" widget'ına çevrilir. Yalnızca arayüz değişikliği.

## yama104 — Ağ ve altyapı pano widget'ları (v3.16.0)

- Yeni widget'lar: SSL sertifikaları, DNS, NTP, TCP/UDP, ICMP, yol izleme, veritabanı probe'ları. Her biri ilgili türdeki tüm probe'ları gösterir (halka + ısı kutuları + aranabilir/sayfalı tam liste); kutular Asset kartına gider.
- Yeni hazır pano şablonu: "Ağ ve altyapı servisleri". Yalnızca arayüz değişikliği (mevcut `/api/perf/network` ve `/api/probes`).

## yama103 — Asset kartı (v3.15.0)

- Assetler'de (ve alarm listesi, topoloji paneli, kart içi bağlantılardan) asset'e tıklayınca `#kart/<id>` sayfası: Genel, Yazılım envanteri, Topoloji ve bağlantılar, Performans, Güvenlik, Olaylar sekmeleri (tembel yüklenir, her sekmede Yorum).
- Yalnızca arayüz değişikliği; mevcut API'ler asset'e göre süzülür. Saldırı yüzeyi › Envanter ayrıntısı ortak `invDetail` ile kartta da kullanılır.

## yama102 — Saldırı yüzeyi envanteri: port, uygulama, sürüm, CVE (v3.14.0)

- Ajan v1.6.0: dinleyen port + süreç + çalışma zamanı sürümü + bağımlılık manifest (yalnızca ad/sürüm) bildirir (`SEND_INVENTORY`, `APM_INVENTORY_EVERY`, `APM_INVENTORY_DIRS`). **Ajanların güncellenmesi gerekir.**
- Sunucu: migration 050 `asset_inventory`; EOL takvimi (`eol.json` ile geçersiz kılınır); OSV kayıtlarıyla CVE eşleme (`APM_ADVISORY_DIR`, `APM_ADVISORY_AUTO=1`); `GET /api/inventory`; metrikler `inventory.eol_count/vuln_critical/vuln_high`; ağdan erişilebilir portlar sapma tespitine beslenir.
- Arayüz: Saldırı yüzeyi › Envanter sekmesi (makine özeti, yorum, ayrıntıya inme), alarm şablonları.

## yama101 — Servis haritası ve senaryo bağımlılıkları (v3.13.0)

- Dağıtık izleme › Servis haritası: kutular ada göre genişler (IP/adres kısaltılmaz), sürüklenebilir (yerleşim tarayıcıda `apm_tmap_pos`; "Yerleşimi sıfırla"), çerçeve rengi duruma göre (sağlıklı / uyarı / çok yavaş / hata oranı ≥ %5), kutu içinde p95 ve hata oranı, renk açıklaması.
- Topoloji › Senaryo bağımlılıkları: kutuya tıklayınca o ve bağlı kutular yakınlaşır; altta senaryo hata iletisi ve olası neden yorumu (`errorCause`), dış alan adı için ağ/HTTP hata yorumu (`hostCause`).
- Yalnızca arayüz değişikliği.

## yama100 — Docker: log ve NetFlow ayarları (v3.12.2)

- `docker-compose.yml` `APM_LOG_EVENTS`, `APM_LOG_MIN_LEVEL`, `APM_LOG_RETENTION_DAYS`, `APM_LOG_RATE`, `APM_NETFLOW_ADDR`, `APM_NETFLOW_ALLOW` değişkenlerini sunucuya iletir ve NetFlow için `2055/udp` portunu yayınlar. Önceden `.env`'e yazılan bu değerler Docker'da sunucuya ulaşmıyordu. Açmak için `.env` içine `APM_LOG_EVENTS=1` (veya `APM_NETFLOW_ADDR=:2055`) yazıp `docker compose up -d` çalıştırın.

## yama99 — Loglar sekmesi boş veride çökmesi (v3.12.1)

- Log yokken `/api/logs` `services: null` döndürüyordu; Dağıtık izleme › Loglar "Yüklenemedi" hatası veriyordu. Sunucu artık boş liste döndürür, arayüz de boş/null listelere dayanıklı (Loglar, Servisler, Trace listesi).

## yama98 — İzleme sayfalarında yorum ve sadeleşme (v3.12.0)

- Performans › Uygulamalar: "Yorum" sütunu (hata oranı, gecikme, yavaş kuyruk), p95 ve istek hızı için orantılı çubuklar; p50/p99/en yavaş değer uygulama adının altına taşındı.
- Performans › Veritabanı sorguları: önce veritabanı başına tek satırlık özet (toplam süre çubuğu, yavaş sorgu sayısı, yorum); satıra tıklayınca o veritabanının sorguları, sorgu başına yorum (yavaşlıyor, baskın, yavaş) ve süre payı çubuğuyla açılır.
- Performans › Bağlantılar (RTT ve haritada olmayan bağımlılık yorumu), Ağ akışı (en çok gönderen payı, baskın port, tanımsız adresler) ve Dağıtık izleme › Servisler (hata, gecikme, sessizlik) yorum aldı.
- Yalnızca arayüz değişikliği; sunucu, veritabanı ve ajan değişmedi.

## yama97 — NetFlow/IPFIX toplayıcı (v3.11.0)

- Migration 049 `flow_agg`; `internal/netflow` (v5/v9/IPFIX çözücü, yalnızca stdlib; şablon önbelleği, seçenek şablonundan örnekleme, kurumsal ve değişken uzunluklu alanlar, bozuk paketlere dayanıklı) ve `internal/app/flows.go` (UDP toplayıcı, izin listesi, bellekte özet, üst-N + "diğer", IP→asset eşleme, `flow.tx_bps`/`flow.rx_bps`, saklama temizliği, `GET /api/flows/summary`).
- Arayüz: Performans › Ağ akışı; Topoloji'de `flow_edges` kaplaması (yerleşimi ve kök nedeni etkilemez).
- Varsayılan kapalı (`APM_NETFLOW_ADDR`).

## yama96 — Log–trace ilişkilendirme (v3.10.0)

- Migration 048 `log_events`; `internal/app/logevents.go`: OTLP logs (JSON/protobuf), `POST /api/logs`, `GET /api/logs`, `GET /api/traces/{id}/logs` (kesin + ±2 sn yaklaşık eşleşme), maskeleme, hız sınırı, saklama temizliği.
- Varsayılan kapalı (`APM_LOG_EVENTS=1`); asset oluşturmaz. Arayüz: Dağıtık izleme › Loglar sekmesi ve trace ayrıntısında İlgili loglar.

## yama95 — Raporlar: oluşturucu ve yeni bölümler (v3.9.0)

- `web/static/reports.js`: bölüm kayıt sistemi (`SECTIONS`), özel rapor oluşturucu, kayıtlı şablonlar (tarayıcı depolaması), dönem/grup kapsamı, önceki dönem karşılaştırması, CSV (`;`, UTF-8 BOM) ve JSON dışa aktarma. Sunucu tarafı değişmedi; mevcut API uçları kullanılır.
- Yeni bölümler: performans, yavaş sorgular, senaryolar (bot koruması dahil), sertifikalar, ajanlar, saldırı yüzeyi, kök neden.

## yama94 — Bot koruması uyarısı (v3.8.1)

- `internal/browser/botblock.go`: WAF/bot koruması engel sayfalarını (Türkçe e-ticaret sayfaları, Cloudflare, Imperva, Akamai, PerimeterX, kısa 403/429) algılar.
- Senaryo oynatmada açıklayıcı hata; kayıt ekranında canlı uyarı. Sertifika hatası değildir; çözüm sitenin sahibinden IP izin listesi veya HTTP probe kullanmaktır.

## yama93 — Ölçek paketi 3: veri hacmi (v3.8.0)

- Migration 046: `metrics` tablosu günlük bölümlere (PARTITION BY RANGE) ayrıldı; mevcut veri `metrics_legacy` bölümü olarak korunur (2,2 milyon satırda ~0,4 sn). Eski bölümler saklama süresi dolunca tek komutla silinir. **Geri alınamaz: güncellemeden önce `pg_dump` alın.**
- Migration 047: `asset_latest` tablosu; son değerler buradan okunur.
- Toplu metrik yazımı (unnest), ajan özeti `GET /api/agent/summary`, `APM_OFFLINE_AGENT_SEC` / `APM_OFFLINE_WARMUP_SEC`.
- Arayüz: aranabilir seçim listesi, harita otomatik yenilemeyi durdurma, uyarlanabilir yenileme aralığı, kurala göre alarm süzgeci.
- Topoloji: Senaryolar sekmesi kaldırıldı (İzleme menüsünde); senaryoların keşfettiği dış servisler ana haritadan ayrı "Senaryo bağımlılıkları" haritasında gösterilir.

## yama92 — Ölçek paketi 2 (v3.7.0)

- **C1** Ajanlar sayfası: arama, durum/güncelleme/veri ayarı/grup süzgeci, "daha fazla göster" sayfalaması (100'er), çoklu seçim; seçilenlere toplu güncelleme ve toplu Veri ayarı (`PUT /api/agent/config` çoklu `asset_ids`; veritabanı bağlantısı toplu ayarlanamaz).
- **C3** kademeli güncelleme: migration 045 `agent_rollouts`; `POST /api/agent/rollout` (`asset_ids|all`, `batch` 1–200, `halt_on_fail`), `GET /api/agent/rollout`, `POST /api/agent/rollout/{pause|resume|cancel}`. `rolloutJob` 15 sn'de bir dalgayı değerlendirir: dalga üyesi sunucudaki dosyayla aynı SHA'ya geçince "güncellendi", `update_error` bildirince veya 15 dk dolunca "hatalı" (çevrimdışıysa "atlandı"); hata varsa kuyruk duraklar, hatalı ajanın isteği geri alınır. Aynı anda tek etkin kademeli güncelleme.
- **C2** `POST /api/probes/bulk` ve `/api/scenarios/bulk` (`ids`, `action: enable|disable|delete|set`, `interval_sec`, `timeout_ms`; tarayıcı senaryosu en az 60 sn); `POST /api/probes/import` (en çok 2000 satır; her satır `createProbe` doğrulama/kota/lisans denetiminden geçer, `dry_run` ile doğrulama). Arayüzde Probe ve Senaryolar tablolarında çoklu seçim + toplu çubuk, Probe'lar sayfasında CSV penceresi.
- **E2** `ingestAllow`: (kiracı, asset adı) başına jeton kovası (`APM_INGEST_RATE`, varsayılan 2/sn, patlama 20); aşılırsa 429 + Retry-After (ajan 1.5.0 buna uyar). Diğer assetler etkilenmez.

## yama91 — Ölçek paketi 1 (v3.6.0, ajan 1.5.0)

500 ajan / 1000 probe / 150 senaryo yükü için güvenlik ağı:
- **A2** migration 044: `metrics (tenant_id, name, time DESC)` indeksi; `evaluateAlerts` her kuralı pencere/4 sıklığında (15–60 sn) çalıştırır (`ruleDue`).
- **B1** probe'lar paylaşılan yuva havuzuyla (`APM_PROBE_CONCURRENCY`, varsayılan 50) çalışır; zamanlayıcı turu beklemez, yalnızca boş yuva kadar probe seçilir.
- **B2** HTTP senaryoları (`APM_SCENARIO_CONCURRENCY`) ve tarayıcı senaryoları (`APM_BROWSER_CONCURRENCY`) ayrı kuyruklardadır; `GET /api/scenarios/capacity` tahmini tur süresini verir, Senaryolar sayfası aşımda uyarır.
- **D1** `queueNotify`: aynı (kiracı, kural, olay) için `APM_NOTIFY_GROUP_SEC` içinde gelen bildirimler tek mesajda toplanır (ilk 15 varlık adı + "ve N tane daha").
- **A4** `/api/ingest` yuva sınırı (`APM_INGEST_CONCURRENCY`); dolunca 503 + `Retry-After`. Ajan 1.5.0: açılış jitter'ı (`APM_NO_JITTER` ile kapatılır), hata sonrası üstel geri çekilme (Retry-After'a uyar), tamponu parçalar arası beklemeyle boşaltır. DB havuzu `APM_DB_MAX_CONNS` (varsayılan 40).

## yama90 — Veri ayarında veritabanı bağlantısı (v3.5.1)

- `PUT /api/agent/config` gövdesine `db:{host,port,name,user,password,sslmode}` (veya `remove:true`); migration 043 `agent_config.db_conf/db_pass`. Parola `Vault.Seal` ile (AAD: ajan asset kimliği) saklanır, listede yalnızca `password_set` döner. Parola yalnızca TLS/`X-Forwarded-Proto: https`/loopback isteğinde kaydedilir; teklif (`config.db.dsn`) yalnızca güvenli heartbeat yanıtında iletilir, aksi halde ajan eski ayarda bekler. Tek ajan sınırı; denetim kaydına parola yazılmaz.
- Ajan DSN'i `remote.json` içinde (0600) saklar; `applyRemote` yerel `APM_PG_DSN`'nin yerine koyar, etiket `cfg_db=remote`.
