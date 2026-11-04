# APP_MON

Uygulama izleme ve performans ölçme (APM) platformunun çalışan ilk iskeleti. Tek Go sunucusu, PostgreSQL, gömülü web arayüzü ve isteğe bağlı hafif agent'tan oluşur.

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

Bu sürüm yalnızca **OTLP/HTTP JSON** kabul eder (`POST /v1/metrics`, gauge ve sum). Birçok SDK varsayılan olarak protobuf gönderir; bu durumda bir OpenTelemetry Collector'ı `otlphttp` exporter ve `encoding: json` ile araya koyun. Protobuf desteği sonraki adımdır.

## Hafif agent (isteğe bağlı)

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
| `snmp` | host[:161] | v1/v2c GET; `probe.snmp_value` (sayısal OID); topluluk adı gizli saklanır; SNMPv3 yok |
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
- Dağıtık izleme (trace), log toplama, gerçek kullanıcı izleme (RUM). Topoloji bağlantıları elle tanımlanır; yalnızca tarayıcı senaryolarının eriştiği dış alan adları otomatik eklenir.
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
cmd/agent/         hafif agent
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
- **Ayarlar > Raporlar** (önizlemeli, yazdır/PDF): Yönetici özeti (executive), Erişilebilirlik, Alarmlar, Envanter, Probe'lar, Topoloji, Denetim kaydı.

- **Dashboard "Kontrol edilen sayfalar"**: kartlar sürükle-bırak ile sıralanır; sağ tık veya × ile dashboard'dan kaldırılır ("Tümünü geri getir" bağlantısı). Sıra/gizleme tarayıcıda (localStorage) saklanır; senaryo silinmez.
- **Uygulama haritası**: bir uygulama seçilince yanında "Topolojide göster" ve "Kontrol edilen sayfalara ekle" düğmeleri çıkar (tarayıcı senaryosu yoksa Senaryolar sayfasına yönlendirir; zaten ekliyse "Kontrol edilen sayfalarda ✓" görünür).
