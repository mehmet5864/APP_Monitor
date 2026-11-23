# APP_MON Kullanım Kılavuzu

APP_MON; sunucu, servis, web sitesi, veritabanı ve ağ cihazlarınızı izleyen, sorun olduğunda alarm üreten ve bildirim gönderen bir uygulama izleme (APM) platformudur. Bu kılavuz önce hızlı bir başlangıç, ardından her sayfanın ayrıntılı açıklamasını, kurulum/yapılandırma başvurusunu ve sorun gidermeyi içerir. Sağdaki içindekiler listesinden bölümlere atlayabilir, üstteki kutudan kılavuzda arama yapabilirsiniz.

> **İpucu:** Sürümünüz üst çubukta (**ON-PREMISE** etiketinin yanında) ve Ayarlar › Sistem'de görünür. Yeni bir özellik görünmüyorsa güncelleme tamamlanmamış olabilir; bkz. *Sorun giderme*.

## Hızlı başlangıç

İlk izlemenizi yaklaşık 15 dakikada kurabilirsiniz:

1. **Giriş yapın.** Tarayıcıda sunucu adresini açın; yönetici API anahtarı (`APM_ADMIN_KEY`) ile ya da size verilen kullanıcı adı ve parola ile girin.
2. **Bir probe ekleyin.** *İzleme › Probe'lar* sayfasında bir web adresi (HTTP), TCP portu, ping veya TLS sertifikası ekleyin. Her probe belirlediğiniz aralıkla (sn) çalışır ve bir **asset** oluşturur.
3. **Hazır alarm kurallarını açın.** *Alarm ve raporlar › Alarmlar* sayfasında **Hazır şablonlar** ile "servis erişilemiyor", yüksek CPU/bellek gibi kuralları tek tıkla ekleyin.
4. **Bildirim kanalı ekleyin.** *Alarm ve raporlar › Bildirimler* sayfasında e-posta, Slack, Teams, Telegram veya webhook ekleyip **Test gönder** ile deneyin.
5. **Sunucuları izleyin.** CPU/RAM/disk için *Araçlar › Ajanlar* sayfasındaki tek komutla ajan kurun (ya da SNMP/SSH ile ajansız izleyin).
6. **Sonucu görün.** *Genel bakış* sayfası durumu tek bakışta gösterir; sorun olursa sağ üstteki zil simgesinde kırmızı sayı belirir.

## Temel kavramlar

| Kavram | Anlamı |
|---|---|
| Asset | İzlenen her şey: sunucu, servis, veritabanı, URL, ağ cihazı. Lisans, benzersiz asset sayısına göre verilir. |
| Probe | APP_MON'un sunucu tarafından belirli aralıkla yaptığı kontrol (HTTP, TCP, ping, SNMP, DNS, TLS, NTP, UDP, PostgreSQL, SSH vb.). Hedefe bir şey kurulmaz. |
| Ajan | İzlenen makineye kurulan küçük program; CPU, bellek, disk, ağ ve süreç verisini sunucuya gönderir. |
| Senaryo | Gerçek bir tarayıcıyla web sitesinde adım adım yapılan test (giriş yapma, sayfa açma, metin kontrolü). |
| Metrik | Ölçülen sayı (ör. `host.cpu_pct`, `http.latency_ms`). Alarm kuralları metriklere yazılır. |
| Alarm kuralı | "Şu metrik, şu pencerede, şu eşiği aşarsa alarm aç" tanımı. |
| Alarm | Kuralın tetiklenmesiyle açılan kayıt; koşul düzelince kendiliğinden kapanır. |
| Bakım penceresi | Planlı çalışma sırasında bildirim gönderilmemesi için tanımlanan zaman aralığı. |
| Kiracı | Aynı kurulumda birbirinden yalıtılmış kurum (yalnızca çok kiracılı kurulumda). |
| Kasa | SSH anahtarı, parola, token gibi sırların şifreli saklandığı yer. |

## Arayüzü tanıma

### Üst çubuk

Soldan sağa: **arama** (asset ve sayfa arama), **yazdır**, **kullanıcı ve rol rozeti**, **TR/EN dil düğmesi**, **zil** (açık alarm sayısı), **asset sayacı** (ör. "5 / 25 asset", lisans kullanımı) ve **Çıkış**. Ayrıca sürüm ve kurulum modu etiketi bulunur.

- **Dil (TR/EN):** Zilin yanındaki düğme arayüzü Türkçe ile İngilizce arasında değiştirir. Seçim tarayıcıda saklanır (`apm_lang`) ve giriş sayfası ile herkese açık durum sayfasını da kapsar. Çeviri tarayıcıda, `web/static/i18n.js` sözlüğüyle yapılır; sunucudaki veri değişmez. Denetim kaydı ayrıntıları, alarm kuralı adları ve sizin girdiğiniz ad/veriler girildiği gibi kalır. Bu kılavuz yalnızca Türkçedir.
- **Zil:** Açık alarmları listeler. **Alarmları temizle** düğmesi tüm açık alarmları kapatır (onay ister, denetim kaydına yazılır, yazma yetkisi gerekir). Sorunun koşulu sürüyorsa alarm bir sonraki değerlendirmede yeniden açılır.
- **Duraklatma şeridi:** İzleme duraklatıldıysa tüm sayfaların üstünde sarı bir uyarı görünür (bkz. *İzlemeyi duraklat / başlat*).

### Menü grupları

Menü her ekran genişliğinde üsttedir ve gruplanmıştır. Gruba fareyle gelince ya da tıklayınca açılır; klavyede Enter/Boşluk açar, Esc kapatır. Açık sayfanın grubu vurgulanır. Rolünüzün göremediği sayfalar menüde yer almaz; tek sayfası kalan grup doğrudan bağlantı olur.

| Grup | Sayfalar |
|---|---|
| Genel bakış | Genel bakış |
| İzleme | Assetler, Topoloji, Probe'lar, Performans, Dağıtık izleme, Saldırı yüzeyi, Senaryolar |
| Alarm ve raporlar | Alarmlar, SLA, Raporlar, Kanıt zinciri, Bildirimler |
| Araçlar | Ağ araçları, Ajanlar |
| Yönetim | Kullanıcılar, Kiracılar, Lisans ve anahtarlar, Kasa, Denetim, Güncelleme, Ayarlar |
| Yardım | Bu kılavuz |

### Roller

Anahtar veya kullanıcı oluştururken rol seçilir:

| Rol | Yetki |
|---|---|
| admin | Her şey: kanallar, anahtarlar, kullanıcılar, ayarlar, kasa, denetim |
| operator | Okuma + yazma: probe, kural, bakım, senaryo, alarm üstlenme |
| viewer | Yalnızca okuma |
| agent | Yalnızca veri gönderimi (ajanlar için) |

Yetkisiz sayfalar menüden gizlenir; asıl denetim sunucudadır (yetkisiz istek 403 döner).

### Görünüm tercihleri

*Yönetim › Ayarlar › Görünüm* bölümünden:

- **Tema:** açık/koyu. **Palet:** Lavanta, Nane, Kum, Mercan, Nordik, Erik, Neon; **vurgu rengi:** Turuncu, Pembe, Yeşil, Kırmızı, Camgöbeği. Grafik renkleri açık/koyu temaya göre ayrı, renk körlüğüne dayanıklı bir dizidir.
- **Sayfa düzeni:** varsayılan **Otomatik** ekran çözünürlüğüne uyar; geniş ekranlarda Genel bakış 12 sütunluk ızgaraya yayılır (≥1500 px grafikler ikişerli, ≥2100 px dörderli). **Dar** eski ortalanmış 1180 px düzenidir. Tercihler tarayıcıda saklanır.
- Arka plan, vurgu rengine göre yavaşça hareket eden bir degrade kullanır; işletim sisteminde "hareketi azalt" açıksa animasyon durur, yazdırmada gösterilmez.

## Uzun listeler: sayfalama ve arama

50'den fazla satırı olan listelerde (Performans, Alarm geçmişi vb.) sayfa düğmeleri hem tablonun **üstünde** hem altında bulunur. 8 satırı aşan listelerin üstünde **Bu listede ara…** kutusu vardır; arama yalnızca görünen sayfada değil tüm satırlarda yapılır. Arama metni ve sayfa, 10 saniyelik otomatik yenilemede korunur. Denetim günlüğünde sayfa düğmeleri üstte de vardır.

## Genel bakış

Durumun tek bakışta özeti. Üstte KPI kutuları (Erişilebilirlik 24 saat, Açık alarm, Asset, Ort. yanıt süresi, Senaryolar), ardından **Kontrol edilen sayfalar**, **Makineler · CPU / RAM / Disk** ve diğer bölümler gelir.

### Her widget tıklanabilir

Klavyeyle Tab + Enter de çalışır:

- **Üst kutular:** Erişilebilirlik → SLA; Açık alarm → Alarmlar; Asset → Assetler (erişilemeyen varsa "Erişilemiyor" süzgeciyle); Ort. yanıt süresi → Probe'lar; Senaryolar → Senaryolar.
- **Açık alarmlar tablosundaki satır** Alarmlar sayfasına gider, aynı alarmı vurgular ve görünür kaydırır.
- **Asset durumu** çubuğu ve etiketleri Assetler'i o durumla süzer; "Türe göre" çubukları türle süzer.
- **En yavaş hedefler** ve **Makineler** kartları ilgili asset'in ayrıntısını açar.
- **Senaryo sağlığı** satırı Senaryolar'a gider; başlıklardaki "→" bağlantıları ilgili sayfayı açar.

### Pano şablonları ve widget'lar

Genel bakışın üstündeki **Pano** listesinden hazır bir şablon seçersiniz; **✎ Panoyu düzenle** ile widget ekler, çıkarır, sıralar ve boyutlandırırsınız.

| Hazır şablon | İçerik |
|---|---|
| Operasyon (varsayılan) | KPI'lar, sayfalar, makineler, erişilebilirlik, durum, alarmlar, yavaş hedefler, senaryolar, açık alarmlar |
| Performans | KPI'lar, uygulama performansı, en yavaş uygulamalar (p95), gecikme kırılımı, yavaş sorgular, makineler |
| Güvenlik | KPI'lar, saldırı yüzeyi sapmaları, sertifika süreleri, açık alarmlar, alarmlar, erişilebilirlik |
| NOC (alarm odaklı) | KPI'lar, açık alarmlar, sağlıksız asset'ler, kök neden ve etkilenenler, probe durumu, hatalı probe'lar, makineler, sayfalar |
| Probe'lar | KPI'lar, probe durumu, probe türleri, sertifika süreleri, hatalı probe'lar, en yavaş hedefler, erişilebilirlik |
| Yönetici özeti | KPI'lar, erişilebilirlik, durum, gruplara göre asset durumu, alarmlar, en yavaş uygulamalar |

- **Performans widget'ları:** Uygulama performansı (istek/dk, hata, p50/p95/p99), En yavaş uygulamalar (p95), Yavaş veritabanı sorguları, Gecikme kırılımı (HTTP), Saldırı yüzeyi sapmaları. Veri yoksa widget ne yapılacağını söyler; satıra tıklayınca ilgili sayfa açılır.
- **Probe ve diğer widget'lar:** Probe durumu (çalışıyor/hatalı/bekliyor/kapalı), Hatalı probe'lar (hata metniyle), Probe türleri, Sertifika süreleri (TLS probe'larının kalan günü; 14 günden azsa uyarır), Sağlıksız asset'ler, Gruplara göre asset durumu, Kök neden ve etkilenenler (topolojiden).
- **Düzenleme:** her widget'ta ▲ ▼ (sırala), **Genişlik** (4, 6, 8 veya 12 / 12 sütun) ve × (kaldır) vardır; panoda olmayanlar "Widget ekle" satırından eklenir.
- **Kaydetme:** hazır şablonlar değiştirilmez; **Yeni pano olarak kaydet…** ile kendi panonuz (en çok 20) oluşur, sonra **Kaydet** ile güncellenir, **Panoyu sil** ile silinir. Son seçtiğiniz pano açılışta gelir.
- Panolar **tarayıcıda** saklanır (kullanıcıya ve tarayıcıya özeldir). 16:9 görünüm ayrı ve sabittir.

### Kontrol edilen sayfalar (senaryo kartları)

Her kart bir tarayıcı senaryosunun son durumunu ve ekran görüntüsünü gösterir.

| Renk | Anlamı |
|---|---|
| Yeşil | Son 3 kontrolün hepsi başarılı |
| Sarı | En az biri başarısız ama hepsi değil ("Aralıklı hata"); veya sayfa yapısı değişti |
| Kırmızı | Son 3 kontrolün hepsi başarısız; veya bağlı hızlı probe üst üste 2 kez erişilemedi |
| Gri | Henüz veri yok |

Tek seferlik geçici kesinti kırmızı alarm görüntüsü oluşturmaz. Bir kartın **durum rozetine** (Sağlıklı / Sayfa değişti / Başarısız) veya hata metnine tıklayınca ayrıntı penceresi açılır: hata sebebi ve başarısız adım, sayfa değişiklikleri, son çalıştırmanın adım adım özeti. Kartın kendisine tıklamak ekran görüntüsünü büyütür.

### Makineler · CPU / RAM / Disk

Ölçümü gelen makineler; en yüklü olan başta. %75 üzeri **sarı**, %90 üzeri **kırmızı**. Karta tıklayınca Assetler'de o makine açılır; **Disk** çubuğuna tıklayınca tüm bağlama noktaları (doluluk %, kullanılan/toplam GB) listelenir.

Hangi makinelerin görüneceğini siz seçersiniz: yeni host probe eklerken "Bu makinenin CPU / RAM / Disk değerlerini Genel bakışta göster" kutusu işaretli gelir; sonradan Assetler › makine › **Genel bakışta göster** ile değiştirilir. Ajanla veri gönderen makineler de aynı kutuyla yönetilir; değişiklikler denetim günlüğüne yazılır.

### 16:9 görünüm (duvar/TV panosu)

Genel bakış başlığındaki **16:9 görünüm** düğmesi sayfayı 1920×1080 oranında, kaydırmasız tek ekrana sığan bir panoya çevirir: üstte KPI'lar, ortada kontrol edilen sayfalar ve makine kaynakları, altta açık alarmlar; saat gösterilir, veriler otomatik yenilenir. Tam ekrana geçer; **✕ Çık** veya Esc ile çıkılır. Ekran oranı farklıysa sahne oranı korunarak ölçeklenir. Tercih tarayıcıda saklanır.

### İzlemeyi duraklat / başlat (yönetici)

Genel bakışın sağ üstündeki **⏸ İzlemeyi duraklat** düğmesi kiracının tüm izlemesini tek tıkla durdurur; bakım, taşınma veya hedef sistem kesintilerinde yanlış alarmı önler.

- Duraklatma ve yeniden başlatma **yönetici parolanızı** (API anahtarıyla girdiyseniz anahtarı) yeniden ister; işlem denetim kaydına `monitoring.pause` / `monitoring.resume` olarak yazılır.
- Duraklatılınca: probe'lar ve senaryolar çalışmaz, yeni Keşfet isteği ve "şimdi çalıştır" reddedilir, alarm değerlendirmesi ve yeni bildirimler durur (açık alarmlar olduğu gibi kalır). Ajan ve OTLP verisi alınmaya devam eder.
- Tek tek probe/senaryo açık-kapalı ayarlarına dokunulmaz; yeniden başlatınca hepsi kaldığı yerden devam eder (birikmiş işler aynı anda çalışmaz).
- Ayar kiracıya özeldir; diğer kiracıları etkilemez.

## Assetler

İzlenen sunucu/cihazların listesi. Her asset'e bir **grup** (tek) ve **etiket** (çoklu) atanabilir; listede durum, tür, grup ve etiketle süzülür.

- **Grup / etiket:** satırdaki *Grup / etiket* hücresine tıklayınca hücre yerinde düzenlenir (Enter kaydeder, Esc vazgeçer); düzenleme sürerken sayfa kendiliğinden yenilenmez.
- **Satır sonu:** her satırın sonunda **tür** listesi (topolojideki şekil), **Ayrıntı** ve **Sil** düğmeleri bulunur. Silmek asset'in tüm metriklerini de siler ve onay ister.
- **Ayrıntı:** ölçüm grafikleri, açık alarmlar, bağlı probe'lar, **Sunucu ayrıntısı** ve **Genel bakışta göster** kutusu alttaki bölümde açılır; **Kapat** ile kapanır.

- **Durumlar:** Çalışıyor, Kısmen erişilemiyor, Erişilemiyor, Çevrimdışı (ajan veri göndermiyor).
- **Kısmen erişilemiyor (sarı):** bir hizmetin birden çok probe/senaryosu varsa (ana sayfa + alt sayfalar/portlar) durum hepsine bakılarak belirlenir: hepsi başarılı = Çalışıyor; hepsi başarısız = Erişilemiyor (kırmızı, "Servis erişilemiyor" kritik alarmı); bir kısmı başarısız = **Kısmen erişilemiyor** (sarı, "Servis kısmen erişilemiyor" uyarı alarmı). Tek probe'lu hizmetlerde davranış değişmez.
- **Asset limiti:** lisansa göre sınırlıdır (üst çubukta "5 / 25 asset"). Yeni bir asset adı girerseniz lisanstan bir birim düşer; limit dolunca yeni asset kabul edilmez, mevcutlar izlenmeye devam eder.

## Topoloji

Asset'ler arası bağımlılıkları gösteren harita. **Hiyerarşi üstten alta** doğrudur: en üstte altyapı (sunucu, ağ), altında üzerinde çalışan veya ona bağımlı uygulamalar ve veritabanları durur. Oklar bir asset'ten bağımlı olduğu asset'e gider.

- **Bağımlılık yayılımı:** bir asset çalışmıyorsa (Erişilemiyor/Çevrimdışı), ona doğrudan veya dolaylı bağımlı olan her asset (örn. sunucusu düşen uygulama ve onun veritabanı) haritada otomatik **kırmızı** (kesikli çerçeve, "← sunucu adı") görünür. Bu yalnızca haritadaki gösterimdir; asset'in kendi durumu ve alarmları değişmez. Kök neden ipucu yalnızca gerçekten çalışmayanları sayar.
- **Yakınlaştırma:** sağ üstteki **+ / − / Sığdır** düğmeleri veya Ctrl + fare tekerleği ile yakınlaştırılır; boş alanı sürükleyerek harita kaydırılır. Harita yüksekliği artırıldı (760 px).
- **Yerleşim:** bağlantısı olmayan asset'ler hiyerarşinin altında ızgara halinde toplanır; çok geniş katmanlar satırlara bölünür. Kayıtlı elle yerleşimler bu sürümde sıfırlandı ("Yerleşimi sıfırla" ile de sıfırlanır). 40 asset'e kadar hepsi gösterilir; fazlasında varsayılan yalnızca ana cihazlardır.

- **Görünüm:** 40 asset'e kadar tümü gösterilir; daha fazlasında varsayılan yalnızca ana cihazlardır (tür: Sunucu/host ve Ağ). Bir cihaza tıklayınca o cihaz, doğrudan bağlı bileşenler ve üzerinde çalışan her şey (site, veritabanı) gösterilir. **Ana cihazlara dön** ile geri dönülür; **Tüm asset'leri göster** tam görünümü açar. Hiç sunucu/ağ türünde asset yoksa tümü gösterilir.
- **Hizmet seçici:** "Hizmet" listesinden bir hizmet seçince o hizmet ve ona doğrudan/dolaylı bağlı her şey (hem bağımlı olduğu hem ona bağımlı olanlar) gösterilir.
- **Hata ve uyarılar kutusu:** bir cihaza/asset'e tıklayınca haritanın altında açık alarmlar (önem, kural, değer, süre), hatalı probe/senaryolar (hata metniyle) ve "sayfa değişti" uyarıları ("Ayrıntılar" ile değişiklik listesi) görünür. Sorun yoksa kutu görünmez.
- **Kök neden ipucu:** sağlıksız asset'ler arasında, bağımlı olduğu hiçbir asset sağlıksız olmayanlar (olası kök neden) ayrıca belirtilir.

## Probe'lar

*İzleme › Probe'lar* sayfasından eklenir. Her probe aralıkla (sn) çalışır; liste ve halka grafik türe ve son sonuca göre süzülür.

### Probe türleri

| Tür | Ne ölçer |
|---|---|
| HTTP | Erişilebilirlik, gecikme, durum kodu, beklenen metin (`expect_text`), yönlendirme |
| TCP | Port açık mı, bağlantı süresi |
| Ping (ICMP) | Erişilebilirlik, gecikme, kayıp |
| TLS sertifikası | Sertifikanın kalan gün sayısı, zincir doğrulaması |
| DNS | Ad çözümleme süresi ve sonucu |
| UDP, NTP | UDP port yanıtı; saat sapması |
| SNMP | Tek OID okur (v1, v2c, v3) |
| SNMP gelişmiş (`snmpx`) | Şablonlar ve özel OID'ler (arayüz trafiği, Cisco, UPS…) |
| Sunucu kaynakları (host) | SNMP veya SSH ile CPU, RAM, disk |
| PostgreSQL (`pgstats`) | Veritabanı sağlık metrikleri |
| Yol izleme | Hedefe giden IPv4 yolu, sıçrama sayısı, rota değişimi |
| Senaryo (hızlı probe) | Bkz. *Senaryolar* |

> **İpucu:** SaaS modunda özel ağ hedefleri (`APM_BLOCK_PRIVATE_PROBES`) engellenir; kendi ağınızı izleyecekseniz on-premise modda çalıştırın.

### HTTP/HTTPS ve kurum sertifikaları

İç ağdaki siteler genelde kurum CA'sı veya kendinden imzalı sertifika kullanır; bu durumda probe "x509: certificate signed by unknown authority" hatası verir. İki çözüm:

- **Önerilen — kurum CA'sını tanıtın.** CA sertifikasını PEM olarak alın. Docker: dosyayı proje klasörüne `certs\ca.pem` adıyla koyup `.\guncelle-docker.bat` çalıştırın. RHEL/Linux: dosyayı sunucuya kopyalayıp `/etc/appmon/appmon.env` içine `APM_CA_FILE=/etc/appmon/ca.pem` ekleyin ve `systemctl restart appmon` yapın (alternatif: `cp ca.pem /etc/pki/ca-trust/source/anchors/ && update-ca-trust`, ardından servisi yeniden başlatın). Birden çok CA için aynı dosyaya art arda ekleyebilirsiniz. Uygulama başlarken günlükte "kurum CA dosyası yüklendi" yazar.
- **Hızlı çözüm:** probe'u düzenleyip **Sertifika zincirini doğrula** kutusunu kapatın (yalnızca o probe için; sertifika kalan gün sayısı yine ölçülür).

### SNMP ve SNMPv3

SNMPv3 güvenlik düzeyleri: noAuthNoPriv, authNoPriv, authPriv. Kimlik doğrulama: MD5, SHA, SHA-224/256/384/512. Gizlilik: DES, AES-128. Parolalar en az 8 karakter olmalı; düzenlerken boş bırakırsanız eski değer korunur.

- **Windows hedef:** "SNMP Hizmeti"ni kurun, topluluk adı ve izinli yöneticiyi tanımlayın, güvenlik duvarında UDP 161'i açın.
- **Linux hedef:** `snmpd` kurun, `rocommunity` ekleyin.
- Docker içinden ana makineye erişmek için hedef olarak `host.docker.internal` kullanın.

### SNMP gelişmiş (`snmpx`)

Probe türü "SNMP gelişmiş": bir şablon seçin ve/veya özel OID ekleyin.

- **Arayüz trafiği şablonu:** her arayüz için `net.<ad>.up`, `in_bps`, `out_bps`, `in_err_ps`, `out_err_ps`, `util_pct` (64 bit sayaçlar varsa onlar kullanılır; ilk ölçümde hız yoktur). "Arayüz filtresi" regex'i ile arayüzler seçilir (en çok 64).
- **Linux net-snmp:** CPU, RAM, swap, yük (`host.*`). **Cisco:** CPU, bellek, en yüksek sıcaklık. **UPS (RFC 1628):** pil, yük, şebeke durumu (`snmp.ups_*`).
- **Özel OID:** ad, OID, çarpan ve "Sayaç" (saniyedeki artışa çevir) alanları; değer `snmp.<ad>` metriği olur. En çok 30 OID. Kimlik bilgileri diğer SNMP probe'larıyla aynıdır; gizli alanlar listede gösterilmez.

### Sunucu kaynakları (SNMP veya SSH)

Probe türü "Sunucu kaynakları" CPU, RAM ve diskleri ajan kurmadan okur: `host.cpu_pct`, `host.mem_pct`, `host.disk_pct`, `host.disk_pct.<ad>`, `host.uptime_sec`. Hazır alarm kuralları bu metrikleri doğrudan işler.

**SSH ile (hedefe hiçbir şey kurulmaz):**

1. Hedefte ayrıcalıksız bir kullanıcı açın (ör. `appmon`) ve parolasız bir anahtar üretin: `ssh-keygen -t ed25519 -N "" -f appmon_key`. `appmon_key.pub` içeriğini hedefte `~appmon/.ssh/authorized_keys` dosyasına ekleyin.
2. `appmon_key` (özel anahtar) içeriğini **Kasa**'ya "SSH özel anahtarı" olarak ekleyin; özel anahtar dosyasını sonra silin.
3. Probe formunda hedefi (`host` veya `host:port`), SSH kullanıcısını ve kasadaki anahtar (veya parola) kaydını seçin. Aralığı 60 sn ve üzeri tutun (her ölçüm bir SSH oturumu açar, ~2 sn sürer).

Ayrıntılar:

- APP_MON tarafında **OpenSSH istemcisi** (`ssh`) gerekir (RHEL: `openssh-clients`; Docker imajında hazır).
- Uzak makinede yalnızca sabit, salt-okunur bir komut çalışır (`/proc/stat`, `/proc/meminfo`, `/proc/uptime`, `df`); kullanıcı girdisi komuta karışmaz.
- Parola korumalı anahtar desteklenmez; bunun yerine kasaya "Parola" kaydı ekleyip probe'ta seçebilirsiniz (hedefte `PasswordAuthentication yes` gerekir; anahtar daha güvenlidir). Parola `SSH_ASKPASS` düzeneğiyle verilir, komut satırında veya ortamda görünmez.
- **Sunucu anahtarı sabitleme:** ilk başarılı bağlantıda hedefin SSH sunucu anahtarı öğrenilir ve probe'a sabitlenir (denetim günlüğüne `probe.hostkey`). Sonraki bağlantılarda anahtar uyuşmazsa probe hata verir (ortadaki adam koruması). Hedef yeniden kurulduysa **Parmak izini sıfırla** işaretleyip kaydedin; hedef adresi değişirse sabitleme otomatik sıfırlanır.
- Üretilen ek metrikler: `host.mem_used_gb`, `host.mem_total_gb`, her bağlama noktası için `host.disk_pct./var`, `host.disk_used_gb./var`, `host.disk_total_gb./var`. Yalnızca Linux hedefler; tmpfs/overlay gibi sanal dosya sistemleri dışarıda bırakılır. SaaS modunda SSH ile ölçüm kapalıdır.

### Probe ayrıntıları

Probe'lar sayfasında her satırdaki **Ayrıntı** düğmesi o probe'un son ölçümünü açar: durum, hedef, aralık, son çalışma, hata mesajı, son 24 saat özeti (erişilebilirlik, kontrol sayısı, ortalama/p95/en yüksek gecikme), **son ölçümün ayrıntıları** ve son ölçüm metrikleri.

| Tür | Ayrıntılar |
|---|---|
| HTTP/HTTPS | durum kodu, protokol (HTTP/1.1, HTTP/2), TLS sürümü, Server başlığı, içerik türü ve uzunluğu, yönlendirme adresi, mevcut güvenlik başlıkları |
| TLS sertifikası | sahibi, veren, geçerlilik başlangıcı/bitişi, alternatif adlar (SAN), imza ve anahtar algoritması, zincir uzunluğu, TLS sürümü, şifre takımı |
| DNS | sorgulanan ad, kayıt türü, DNS sunucusu, dönen kayıt sayısı ve cevaplar |
| TCP / UDP | bağlanılan (çözümlenen) adres, gönderilen/aranan metin var mı, UDP yanıt boyutu |
| Ping (ICMP) | çözümlenen adres, gönderilen/alınan paket, kayıp, en az/ortalama/en çok gecikme |
| NTP | çözümlenen adres, stratum, işaretli saat sapması, gidiş-dönüş gecikmesi, sunucu saati |
| SNMP | sürüm, OID, okunan değer; SNMP gelişmiş: şablon ve ölçülen değer sayısı |
| Yol izleme | hedef IP ve her sıçramanın adresi, gecikmesi ve kaybı |
| PostgreSQL / pgstats | sunucu sürümü ve veritabanı adı |
| Host (SSH/SNMP) | yöntem, SSH kullanıcısı, uyumluluk ve dizin seçimi, ölçülen değer sayısı (makine verileri **Sunucu ayrıntısı** penceresinde) |

- Ayrıntılar her çalışmada güncellenir; probe başarısız olursa o ana kadar toplananlar ve hata mesajı görünür, hiçbir şey toplanamadıysa son başarılı ölçümün ayrıntısı korunur.
- Gizli bilgi (parola, topluluk adı, bağlantı dizesi, anahtar) ayrıntılara hiçbir zaman yazılmaz.
- Gecikme ve erişilebilirlik ölçümleri asset bazlıdır; aynı asset'in birden çok probe'u varsa özet hepsini kapsar.

### SSH ile toplanacak veriler ve dizin seçimi

SSH yöntemli host probe'unda **Toplanacak veriler (SSH)** kutularından hangi verilerin okunacağını seçersiniz. Seçilmeyen veri için uzak makinede hiç komut çalıştırılmaz.

| Veri | Metrikler / sonuç |
|---|---|
| CPU | `host.cpu_pct` |
| RAM | `host.mem_pct`, `host.mem_used_gb`, `host.mem_total_gb` |
| Swap | `host.swap_pct`, `host.swap_used_gb`, `host.swap_total_gb` |
| Disk (dizinler) | `host.disk_pct` (en dolu) ve her dizin için `host.disk_pct.<dizin>`, `host.disk_used_gb.<dizin>`, `host.disk_total_gb.<dizin>` |
| Çalışma süresi | `host.uptime_sec` |
| Yük ortalaması | `host.load1`, `host.load5`, `host.load15` |
| Açık bağlantılar | `host.conns`, `host.conn_rtt_ms` ve Performans sayfasındaki bağlantı haritası |
| Dinleyen portlar | Saldırı yüzeyi sapma tespiti |
| IP adresleri | Makinenin adresleri (bağlantı haritasında makineleri eşleştirir) |

- Varsayılan seçim: CPU, RAM, Disk, Çalışma süresi, Açık bağlantılar, Dinleyen portlar, IP adresleri (önceki sürümlerdeki davranış). Swap ve Yük ortalaması isteğe bağlıdır. En az bir veri seçili kalır.
- **Kaydedilecek dizinler:** virgülle ayrılmış bağlama noktaları (ör. `/, /var, /data`). Boşsa tüm dizinler kaydedilir; en çok 40 dizin.
- Bir veriyi kapatırsanız o ölçümler artık yazılmaz; eski kayıtlar saklama süresine göre silinir. Bağlantı veya adres seçimi kapatılırsa mevcut bağlantı haritası ve adres kayıtları korunur.
- Toplanan her şeyi **Sunucu ayrıntısı** penceresinde görürsünüz: Asset sayfasındaki **Sunucu ayrıntısı** düğmesi (veya Genel bakışta makine kartındaki Disk çubuğu) CPU/RAM/Swap özetini, çalışma süresini, yük ortalamasını, dizin tablosunu, IP adreslerini, dinleyen portları ve en yoğun bağlantıları gösterir.
- SNMP yönteminde seçim yalnızca CPU, RAM ve Disk'tir.

### Eski SSH sunucularına bağlanma

Modern OpenSSH istemcisi, eski sunucuların (RHEL 5/6, eski ağ cihazları) kullandığı zayıf algoritmaları varsayılan olarak kapatır; bağlantı `no matching key exchange method found` / `no matching host key type found` gibi hatalarla düşer. Host probe'unda (SSH yöntemi) **Eski SSH sunucusu uyumluluğu** kutusunu işaretleyin: `diffie-hellman-group14/group1/group-exchange-sha1`, `ssh-rsa`/`ssh-dss`, CBC şifreleri ve `hmac-sha1` modern olanlara **ek olarak** açılır.

- Kutu varsayılan olarak **kapalıdır** ve yalnızca işaretlenen probe için geçerlidir; bu algoritmalar zayıftır, yalnızca eski cihazlar için açın. Sunucu anahtarı sabitleme (parmak izi) açık kalır.
- Hata mesajı sunucunun sunduğu algoritmaları gösterir ve kutuyu önerir.
- **RHEL 9** sunucusunda SHA-1 imzaları sistem politikasıyla da kapalıdır; kutu yetmezse APP_MON sunucusunda `sudo update-crypto-policies --set DEFAULT:SHA1` çalıştırın (sunucu genelini gevşetir). Docker'da istemci konteynerdeki OpenSSH'tır ve genellikle daha yenidir.

- **"SSH hatası: exit status N"**: ssh kendi hatalarında 255 döner; başka bir sayı uzaktaki komutun çıkış kodudur. v2.6.3'ten itibaren uzak betiğin son komutu (eski makinelerde `hostname -I`) hata verse de ölçümler okunur; çıktı hiç gelmezse mesaj çıkış kodunu ve olası nedeni (kısıtlı shell, `/proc` erişimi) söyler.

### PostgreSQL sağlık metrikleri (`pgstats`)

Hedef biçimi: `postgres://kullanici:parola@host:5432/veritabani`. Erişimin yanı sıra `db.connections`, `db.connections_pct`, `db.cache_hit_pct`, `db.deadlocks`, `db.size_mb`, `db.longest_query_s`, `db.idle_in_tx_s`, `db.waiting_locks` ve yedek sunucuda `db.repl_lag_s` üretir (ör. alarm: `db.connections_pct > 85`). Yalnızca okuma sorguları çalışır; salt okunur bir izleme kullanıcısı (`pg_monitor` rolü) önerilir. Parola listede maskelenir. MySQL ve SQL Server için sürücü yoktur.

### Yol izleme

Probe'lar › "Yol izleme" türü hedefe giden yolu sürekli izler: `net.path.hops`, `net.path.rtt_ms`, `net.path.loss_pct` ve rota değişince 1 olan `net.path.changed` (alarm: `net.path.changed > 0`). Tek seferlik izleme için *Ağ araçları* sayfası kullanılır.

## Performans (ajansız APM)

İzleme › Performans sayfası, hedef sistemlere **ajan veya kod değişikliği olmadan** uygulama performansını gösterir. Dört sekmesi vardır.

### Uygulamalar (erişim günlüğü)

Web sunucusu veya ters vekil her istek için tek satır log'u syslog (UDP veya TCP, satır sonu ile) ile APP_MON'a gönderir. APP_MON uygulama başına istek/dk, hata oranı (5xx), ortalama, p50/p95/p99 ve en yavaş süreyi hesaplar; bir satıra tıklayınca **uç nokta** ayrıntısı (toplam süreye katkıya göre sıralı) ve zaman serisi açılır. Yüzdelikler 12 kovalı histogramdan tahmin edilir.

Alıcı varsayılan olarak **kapalıdır**; sunucuda `APM_SYSLOG_ADDR` (ör. `:1514`) tanımlayarak açın. Docker'da `.env` dosyasına `APM_SYSLOG_ADDR=:1514` yazıp `.\guncelle-docker.bat` çalıştırın (1514 UDP/TCP portu docker-compose.yml'de yayımlanır; farklı dış port için `APM_SYSLOG_PORT`). Satır biçimi:

```
appmon_key=ANAHTAR app=uygulama-adi m=GET uri=/siparis/42?x=1 st=200 rt=0.123
```

`appmon_key`, Lisans ve anahtarlar sayfasında üretilen agent (veya yönetici) rollü anahtardır; kiracıyı belirler. `app` uygulama adıdır. Süre için `rt` (saniye), `rt_ms` veya `rt_us` kullanılır.

- **nginx:** `log_format appmon 'appmon_key=ANAHTAR app=uygulama m=$request_method uri=$request_uri st=$status rt=$request_time';` ve `access_log syslog:server=SUNUCU:1514,tag=appmon appmon;`
- **Apache:** `LogFormat "appmon_key=ANAHTAR app=uygulama m=%m uri=%U st=%>s rt_us=%D" appmon` ve `CustomLog "|/usr/bin/logger -n SUNUCU -P 1514 -t appmon" appmon`
- **HAProxy:** `log-format "appmon_key=ANAHTAR app=uygulama m=%HM uri=%HPO st=%ST rt_ms=%Tr"`

Sayfadaki "Nasıl bağlanır?" bölümü bu örnekleri sunucu adresinizle doldurur.

#### Örnek: Apache'yi APP_MON ile aynı RHEL sunucusunda bağlama

1. **Anahtar üretin:** Lisans ve anahtarlar sayfasından agent (veya yönetici) rollü bir anahtar oluşturun ve kopyalayın.
2. **Alıcıyı açın** (sunucuda root ile, dizin fark etmez):
   ```
   echo 'APM_SYSLOG_ADDR=:1514' >> /etc/appmon/appmon.env
   systemctl restart appmon
   ss -lun | grep 1514
   ```
   `ss` çıktısında 1514 görünmelidir. Aynı sunucuda olduğu için güvenlik duvarında port açmak gerekmez.
3. **Apache ayarını ekleyin:** `/etc/httpd/conf.d/appmon.conf` dosyasını oluşturun (ANAHTAR ve uygulama adını değiştirin):
   ```
   LogFormat "appmon_key=ANAHTAR app=uygulama m=%m uri=%U st=%>s rt_us=%D" appmon
   CustomLog "|/usr/bin/logger -n 127.0.0.1 -P 1514 -t appmon" appmon
   ```
   Siteler kendi `<VirtualHost>` içinde `CustomLog` kullanıyorsa `CustomLog` satırını her birine ekleyin ve `app=` adını site başına farklı yazın.
4. **Uygulayın:** `apachectl configtest` ardından `systemctl reload httpd`.
5. **Sınayın:**
   ```
   logger -n 127.0.0.1 -P 1514 -t appmon "appmon_key=ANAHTAR app=deneme m=GET uri=/test st=200 rt_us=5000"
   ```
   Yaklaşık bir dakika sonra Performans › Uygulamalar'da `deneme` görünür. Görünmezse aynı sayfadaki alıcı sayaçlarına bakın: **yetkisiz** artıyorsa anahtar veya rolü yanlıştır, **geçersiz** artıyorsa satır biçimi hatalıdır (`app` adında `/` veya boşluk, `st` veya süre eksik), hiçbiri artmıyorsa `APM_SYSLOG_ADDR` uygulanmamıştır (adım 2).

Docker kurulumunda adım 2 yerine `.env` dosyasına `APM_SYSLOG_ADDR=:1514` yazıp `.\guncelle-docker.bat` çalıştırın.

**Gizlilik ve sınırlar:** sorgu dizesi (`?…`) hiç saklanmaz; yoldaki sayılar, UUID ve uzun belirteçler `{id}`, `{uuid}`, `{token}` olarak gruplanır. Bir uygulamada en çok 300 uç nokta, bir kiracıda en çok 50 uygulama izlenir (fazlası `OTHER /*`). Kiracı başına saniyede 2000 satır sınırı vardır (`APM_SYSLOG_RATE`); `APM_SYSLOG_ALLOW` ile kabul edilen kaynak ağlar (CIDR) kısıtlanabilir. TCP'de satır sonu (`\n`) ayracı gerekir. **Anahtar düz metin gider:** log ağını özel tutun. Uygulamalar "logs" türünde asset olarak oluşur ve **lisans asset sayısına dahildir**. Üretilen metrikler: `app.req_per_min`, `app.err_pct`, `app.avg_ms`, `app.p50_ms`, `app.p95_ms`, `app.p99_ms` (alarm kuralı yazılabilir, ör. `app.p95_ms > 1000`). Uç nokta özetleri saklama süresi sonunda silinir.

### Veritabanı sorguları

"PostgreSQL sağlık (`pgstats`)" probe'u 5 dakikada bir `pg_stat_statements` okur ve en yavaş/en yoğun sorguları listeler (toplam, ortalama, son dönem ortalaması, çağrı sayısı). Ortalaması 500 ms ve üzeri olan sorgular "Yavaş" işaretlenir. Hedef veritabanında `shared_preload_libraries = 'pg_stat_statements'` ve `CREATE EXTENSION pg_stat_statements;` gerekir; yoksa sayfa nedenini yazar. Sorgu metinleri parametresizdir (`$1`, `$2`…), değerler görünmez. Metrikler: `db.slow_queries`, `db.top_query_mean_ms`.

### Gecikme kırılımı

HTTP probe'larının son ölçümü DNS, TCP bağlantısı, TLS, sunucu yanıtı (ilk bayt) ve indirme aşamalarına ayrılır; yorum sütunu yalnızca "en yavaş aşama" demez, davranışı yorumlar: aşamanın toplam içindeki payına ve mutlak süresine bakar (eşikler: DNS 100 ms, TCP 150 ms, TLS 250 ms, sunucu yanıtı 800 ms, indirme 500 ms). Sonuç bir durum etiketi (Sağlıklı / İzlenmeli / uyarı / yavaş), bir yorum ve olası nedenle neye bakılacağı olarak gösterilir. Tanınan desenler: ad çözümleme gecikmesi, bağlantı kurulumu yavaşlığı, TLS el sıkışması yavaşlığı, sunucu işleme gecikmesi (ağ değil uygulama/veritabanı), yanıt indirme gecikmesi, "yeni bağlantı maliyeti yüksek" (keep-alive/TLS oturum devamı kapalı olabilir) ve tek aşamada toplanmayan genel yavaşlık. Ek ayar gerekmez; `probe.http_dns_ms`, `probe.http_connect_ms`, `probe.http_tls_ms`, `probe.http_ttfb_ms`, `probe.http_transfer_ms` metrikleri yazılır.

### DNS, Ağ servisleri ve Yol izleme sekmeleri

Performans sayfasında HTTP dışındaki probe'lar için üç sekme vardır; hepsinde dönem (1 saat / 24 saat / 7 gün) seçilir ve her satırın yanında bir durum etiketi ile davranışsal yorum bulunur.

- **DNS:** DNS probe'larının cevap süresi (ortalama, p95), başarı oranı ve son sorgu. Kurumsal DNS sunucularını ayrı probe/asset olarak (DNS sunucusu alanı dolu, sorgulanacak ad ve kayıt türü ile) ekleyin. Yorumlar: cevap vermiyor (başarı %10 altı), kararsız (%95 altı), son sorgu başarısız, yavaş (p95 150 ms üzeri), gecikme yüksek (ortalama 40 ms / p95 60 ms üzeri), son dönemde yavaşlıyor (önceki döneme göre 2 kat ve 20 ms üzeri), dalgalı gecikme (p95 ortalamanın 4 katı). Aynı ad ve kayıt türü birden çok sunucuda izleniyorsa cevaplar karşılaştırılır; farklıysa "Sunucular farklı cevap veriyor" uyarısı çıkar (yük dağıtımlı DNS'te beklenen olabilir).
- **Ağ servisleri:** TLS (kalan sertifika günü: 30 gün altı uyarı, 7 gün altı kritik, dolmuş), NTP (saat sapması: 100 ms üzeri uyarı, 500 ms üzeri kritik), TCP, ICMP ve UDP probe'ları; her tür için kendi eşikleri ve olası neden–bakılacak yer önerileri.
- **Yol izleme:** *Anlık izleme* formuyla hedefe (B noktası) giden yol izlenir; isteğe bağlı olarak pakete bir **DSCP işareti** (BE, CS1, AF21, AF31, AF41, CS3, CS5, EF, CS6) yazılır. Her sıçramada cihazın adresi, ters DNS adı, APP_MON'da tanımlı bir asset'e (host adresi veya probe hedefi olarak) aitse asset adı, gidiş-dönüş gecikmesi, bir önceki sıçramaya göre artış, dalgalanma, kayıp ve varışta okunan DSCP gösterilir. Yönlendiricilerin "TTL doldu" cevabında geri alıntıladığı paket başlığından DSCP okunduğu için işaretin **hangi sıçramada değiştiği veya silindiği** görülür. Yorumlar: hedefe ulaşılamadı, işaret yolda değişti/korundu/doğrulanamadı, belirgin gecikme artışı, hedefte paket kaybı. *Sürekli izlenen yollar* tablosu "Yol izleme" probe'larını gösterir; probe'a DSCP işareti de seçilebilir (`net.path.dscp_changed_ttl` metriği işaretin değiştiği sıçramayı verir, 0 = değişmedi).
- **Sınırlar:** Ölçüm APP_MON sunucusundan hedefe doğrudur. Tek yön süresi ölçülemez (uçlarda saat eşitliği gerekir); gidiş-dönüşün yarısı yaklaşık değerdir. Yönlendiriciler ICMP "TTL doldu" cevaplarına düşük öncelik verdiği için ara sıçramalardaki süre ve kayıp yanıltıcı olabilir; güvenilir olan uçtan uca süredir. Ham ICMP yetkisi (Linux sunucu) gerekir. Probe ölçümleri asset düzeyinde saklandığı için bir asset'te birden çok probe varsa süre ve başarı istatistikleri ortaktır.

### Bağlantılar (bağımlılık önerisi)

SSH ile izlenen Linux makinelerde (salt okunur komutlar: `ss`, `hostname -I`) kurulu TCP bağlantıları okunur: kim kime hangi portla bağlı ve TCP RTT. Karşı adres APP_MON'daki bir makine veya probe hedefiyle **tek ve net** eşleşirse "Önerilen bağımlılık" olarak listelenir; **Topolojiye ekle** ile siz onaylarsınız, otomatik eklenmez. Eşleşmeyenler "Tanınmayan hedefler"de görünür. Gösterilen gecikme ağ (TCP RTT) gecikmesidir. Metrikler: `host.conns`, `host.conn_rtt_ms`. `ss` yoksa bu bölüm boş kalır.

## Dağıtık izleme (OpenTelemetry trace)

İzleme › Dağıtık izleme sayfası, bir isteğin servisler arasında izlediği yolu **span'lar** halinde gösterir: hangi servis ne kadar sürdü, hata nerede oluştu, hangi servis hangi servisi veya veritabanını çağırıyor. Veri, uygulamadaki OpenTelemetry SDK'sından (veya bir OpenTelemetry Collector'dan) **agent kurmadan** alınır.

**En kolay yol: kurulum betiği.** Hedef sistemde elle ortam değişkeni ve ajan kurmak yerine APP_MON'dan indirilen betiği çalıştırın; sunucu adresi betiğe hazır gömülüdür. Betik: (1) sunucuya bağlantıyı ve anahtarı dener, (2) gerçek bir deneme trace'i gönderir (Dağıtık izleme'de hemen görünür, uygulamaya dokunmaz), (3) istenirse servisi OpenTelemetry için yapılandırır, (4) `--undo` ile geri alır. Önce "Lisans ve anahtarlar" sayfasından **agent** rolünde bir anahtar oluşturun.

| | Linux (`otel-kurulum.sh`, root) | Windows (`otel-kurulum.ps1`, yönetici PowerShell 5.1+) |
|---|---|---|
| İndirme | `curl -fsSL https://<sunucu>/kurulum/otel-kurulum.sh -o otel-kurulum.sh && chmod +x otel-kurulum.sh` | `Invoke-WebRequest https://<sunucu>/kurulum/otel-kurulum.ps1 -OutFile otel-kurulum.ps1` |
| Yalnızca bağlantı + deneme trace'i | `./otel-kurulum.sh --service siparis --key ANAHTAR` | `powershell -ExecutionPolicy Bypass -File .\otel-kurulum.ps1 -Service siparis -Key ANAHTAR` |
| Servisi yapılandır | `sudo ./otel-kurulum.sh --service siparis --key ANAHTAR --unit siparis --restart` (systemd drop-in) | `... -WindowsService SiparisSvc -Language dotnet -Restart` (servisin kayıt defteri `Environment` değeri) |
| IIS | — | `... -IIS -Language dotnet -Restart` (W3SVC ve WAS; servis adı IIS site adından türer) |
| Docker / başka başlatıcı | `--env-file /etc/siparis.otel.env` (ortam dosyası yazar) | — (ortam değişkenleri ekrana yazılır) |
| Geri al | `--unit siparis --undo` | `-WindowsService SiparisSvc -Undo -Restart` |

**Ajan üzerinden (anahtarsız, önerilen):** Hedef makinede APP_MON ajanı (1.2.0 veya üstü) çalışıyorsa ajan `127.0.0.1:4318` adresinde yerel bir OTLP alıcısı açar. Uygulama trace'lerini bu yerel adrese gönderir, ajan sunucuya iletir (geçici kesintide 32 MB'a kadar tamponlar, tekrar dener). Kurulum betiği ajanı kendisi bulur (`--via-agent` / `-ViaAgent` ile zorlanabilir) ve **anahtar istemez**; ortam değişkeni olarak yerel adresi yazar. Alıcı yalnızca yerel makineden erişilebilir olacak biçimde açılır; farklı adres için ajan ayarı `APM_OTLP=<adres:port>` (veya `-otlp-listen`), kapatmak için `APM_OTLP=off` (`-no-otlp`).

**Bağlantıdan servis haritası (SDK gerektirmez):** Ajan, süreçlerin giden TCP bağlantılarını izler ve yalnızca *kaynak süreç → hedef süreç/adres/port* özetini yollar (içerik yok). Dağıtık izleme › Servis haritası sayfasında **Kaynak** seçicisiyle *birleşik*, *yalnızca trace* veya *yalnızca bağlantı* görünümü seçilir; bağlantı okları kesikli çizilir ve "N bağlantı" etiketi taşır. Hedef, APP_MON'a kayıtlı başka bir makinedeki dinleyen portla eşleşirse `süreç@makine` olarak adlandırılır, aksi halde `ip:port (servis)` dış bağımlılık olur. Sınırlar: gecikme ve hata oranı yoktur (bunlar için trace gerekir), yalnızca giden bağlantılar ve en çok 300 kenar. Linux'ta başka kullanıcıların süreçlerini görmek için ajan root çalışmalıdır (varsayılan kurulum böyledir); kapatmak için `APM_CONNS=off` (`-no-conns`). Windows'ta `netstat -ano` kullanılır.


Ortak seçenekler: `--lang`/`-Language` `auto|java|node|python|dotnet|none` (Linux'ta çalışan servisin sürecinden algılanır), `--sample`/`-Sample 0.1` (örnekleme), `--jar`/`-Jar` (Java ajanını internetten indirmek yerine yerel dosya), `--python`/`-Python` (uygulamanın yorumlayıcısı veya venv'i), `--dry-run`/`-DryRun` (hiçbir şey yazmadan ne yapılacağını göster), `--no-test`/`-NoTest`, `--insecure`/`-Insecure` (kendi imzalı sertifikada).

Dile göre betik şunları kurar: **Java** OpenTelemetry Java ajanı (`JAVA_TOOL_OPTIONS=-javaagent:...`); **Node.js** `@opentelemetry/auto-instrumentations-node` (`NODE_OPTIONS=--require ...`); **Python** `opentelemetry-distro` + dışa aktarıcı (`PYTHONPATH` ile otomatik yükleme); **.NET** resmi otomatik enstrümantasyon yükleyicisi (Windows'ta servis/IIS kaydını da yapar). Paket ve ajan indirmeleri **internet** (GitHub, pip, npm) gerektirir; internet yoksa Java için `--jar`, diğerleri için iç ayna (mirror) kullanın veya aşağıdaki elle yöntemi uygulayın. Go için otomatik yöntem yoktur (kod değişikliği gerekir). Betik yazdığı değişiklikleri kaydeder: Linux'ta `/etc/systemd/system/<birim>.service.d/appmon-otel.conf` (yalnızca root okur), Windows'ta servisin `Environment` değerine yalnızca kendi eklediği anahtarları ekler ve `AppMonOtelKeys` ile izler; mevcut ortam değişkenleri korunur. API anahtarı bu dosya/kayıt defterinde düz metin durur, bu yüzden uygulama için ayrı bir agent anahtarı kullanın. Betikler sır içermez ve kimlik doğrulaması olmadan indirilir.

**Elle gönderme.** SDK'yı bu sunucuya yönlendirin (agent rolünde bir anahtar gerekir; "Lisans ve anahtarlar" sayfasından oluşturulur):

```
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://<sunucu>/v1/traces
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf      # veya http/json
OTEL_EXPORTER_OTLP_HEADERS=X-API-Key=<agent anahtarı>
OTEL_SERVICE_NAME=siparis-servisi
```

OTLP/HTTP **protobuf** ve **JSON** kabul edilir; gzip (`Content-Encoding: gzip`) desteklenir. Tek istekte en fazla 10.000 span gönderilebilir. Aynı span tekrar gönderilirse çoğalmaz. Her `service.name` otomatik bir **asset** olarak (tür: servis, toplama: agentless) kaydedilir ve lisanstaki asset sayısına girer; lisans doluysa istek reddedilir.

**Sekmeler**

| Sekme | İçerik |
|---|---|
| Trace'ler | Son trace'ler: zaman, kök işlem, toplam süre, span ve hata sayısı, katılan servisler. Servis, durum (hatalı), en az süre, metin (trace kimliği veya işlem adı) ve sıralama (en yeni / en yavaş) ile süzülür. Satıra tıklayınca **şelale görünümü** açılır: her span iç içe girintilidir, çubuk başlangıç ve süreyi gösterir; span'e tıklayınca öznitelikleri (ör. `db.statement`, `http.route`) ve hata iletisi görünür. |
| Servisler | Servis başına RED özeti: çağrı sayısı, hız (/dk), hata oranı, p50/p95/p99 süre. Satıra tıklayınca işlem bazında dökümü açılır. "Çağrı" servisin dışarıdan aldığı sunucu/tüketici veya kök span'lardır. |
| Servis haritası | Servisler arası çağrı grafiği: bir servisin span'ı başka bir servisin span'ının çocuğuysa aralarında bağımlılık vardır. Çocuğu olmayan istemci span'ları (`db.system`, `peer.service`, `messaging.system`, `server.address` özniteliği olanlar) kesikli kutu olarak veritabanı/dış servis gösterir. Ok kalınlığı çağrı sayısı, kırmızı ok %5 üzeri hata oranıdır. |

**Loglar ve trace ilişkisi (v3.10.0).** *İzleme › Dağıtık izleme › Loglar* sekmesi ve her trace'in ayrıntısındaki **İlgili loglar** bölümü, bir isteğin logları ile trace'ini birleştirir. Varsayılan **kapalıdır**: sunucuya `APM_LOG_EVENTS=1` verip yeniden başlatın.
- **Gönderme:** OpenTelemetry kullanan uygulamalar günlükleri `POST /v1/logs` (OTLP/HTTP, JSON veya protobuf, gzip) ile gönderir; SDK'da `OTEL_LOGS_EXPORTER=otlp` ve aynı uç nokta/anahtar ayarlanır. Başka araçlar (Fluent Bit, Vector, kendi kodunuz) `POST /api/logs` ile `{"logs":[{"time","service","host","level","message","trace_id","span_id"}]}` gönderir. Anahtar rolü "ingest" olmalıdır.
- **Eşleşme:** satırın `trace_id`'si (OTLP alanı, `trace_id`/`traceId` özniteliği, ya da mesaj metnindeki `trace_id=…` / `traceparent: 00-…`) trace ile aynıysa kesin eşleşir. `trace_id` yoksa ayrıntıda, aynı servislerde trace süresince (±2 sn) kaydedilen satırlar "yaklaşık eşleşme" olarak ayrı gösterilir.
- **Ne saklanır:** yalnızca `APM_LOG_MIN_LEVEL` (varsayılan `warn`) ve üstü satırlar; mesaj en çok 300 karaktere kısaltılır, e-posta, parola/token/`Authorization` değerleri, uzun belirteçler ve 9+ haneli sayılar maskelenir. `APM_LOG_RETENTION_DAYS` (varsayılan 3) sonra silinir; kiracı başına saniyede `APM_LOG_RATE` (varsayılan 200) satır, fazlası atılır. Günlük alımı asset oluşturmaz ve lisansa sayılmaz.
- **Boyut:** günde 100 bin hata satırı yaklaşık 40 MB tutar; saklama süresi ve seviye eşiğiyle sınırlanır.

**APP_MON'un kendi izi.** APP_MON, kimliği doğrulanmış API isteklerini kendi trace deposuna `appmon-server` servisi olarak yazar (OpenTelemetry kurulumu gerekmez; asset sayılmaz, kiracı yalnızca kendi isteklerini görür). Hatalı (5xx) ve yavaş (≥ 1 sn) istekler her zaman, çağıranın `traceparent` başlığıyla örneklenmiş istekler her zaman, diğerleri `APM_SELF_TRACE_SAMPLE` oranında (varsayılan 0,2) saklanır. Gelen istekte W3C `traceparent` başlığı varsa APP_MON'un span'ı çağıran servisin trace'ine bağlanır. Span adı yol kalıbıdır (`GET /api/sla`); öznitelikler: yöntem, yol (sorgu dizesi yazılmaz), durum kodu, rol, sürüm. Trace uç noktaları, trace alımı, metrik alımı ve `/api/ingest` izlenmez. Kapatmak için `APM_SELF_TRACE=0`.

**Saklama ve sınırlar.** Trace'ler en fazla 14 gün saklanır (lisans saklama süresi daha kısaysa o geçerlidir). Süre 7 günü aşamaz; başlangıcı 7 günden eski veya 5 dakikadan ilerideki span'ların zamanı alım anına çekilir. Geçersiz kimlikli (32/16 hane onaltılık olmayan) span'lar atlanır. Örnekleme (sampling) uygulama tarafında yapılır; sunucu gelen her span'i saklar. Trace verisi şimdilik alarm kurallarına bağlı değildir.

## Saldırı yüzeyi

İzleme › Saldırı yüzeyi sayfası, **zaten toplanan izleme verisinden** bir referans öğrenir ve sonradan ortaya çıkan sapmaları listeler. Hiçbir port taraması veya saldırı denemesi yapılmaz; sistemler yalnızca normal probe/günlük verisiyle izlenir.

| Tür | Kaynak | Sapma örneği |
|---|---|---|
| Dinleyen port | SSH ile izlenen makineler (`ss -tln`; yalnızca loopback dışı adresler) | Yeni açılan port (3389, 23, 6379 gibi riskli portlar **kritik**); 3 gözlem boyunca kaybolan port (bilgi) |
| Uç nokta | Erişim günlüğü (Performans) | Referansta olmayan yeni uç nokta (`admin`, `phpmyadmin`, `.env`, `actuator` … içerenler **kritik**) |
| TLS sürümü | HTTPS probe'ları | TLS 1.0/1.1 görülmesi (**kritik**); 1.3 → 1.2 gibi düşme (uyarı) |
| Güvenlik başlığı | HTTPS probe'ları | HSTS, CSP, X-Frame-Options, X-Content-Type-Options veya Referrer-Policy kaybı (uyarı) |
| Banner | HTTP `Server` başlığı | Sürüm bilgisi değişti (bilgi) |
| Tarama izi | Erişim günlüğü | Son 10 dk'da çok sayıda 4xx (en az 40, istekle çoğunluğu, önceki 24 saatin medyanının ≥8 katı) |

**Referans nasıl oluşur:** Bir asset için bir türün **ilk gözlemi** referans olur ve sapma üretmez. Uç noktalar yeni bir uygulama için ek olarak 24 saat öğrenilir. Bir uygulamada en çok 500 uç nokta izlenir. Referansı sıfırlamak (planlı büyük değişiklik sonrası) için *Referansı sıfırla* düğmesini kullanın; yüzey yeniden öğrenilir ve açık sapmalar kapanır.

**Sapmayı yönetme:** Planlı bir değişikliği *Gözden geçirdim* ile (isteğe bağlı notla) kapatın; kayıt kimin ne zaman gözden geçirdiğiyle saklanır ve denetim günlüğüne yazılır. Aynı sapma gözden geçirilene kadar tek kayıt olarak kalır.

**Alarm ve bildirim:** Gözden geçirilmemiş uyarı/kritik sapma sayısı her dakika `surface.drift` ölçümü olarak yazılır. Alarmlar › şablonlar › *Güvenlik (saldırı yüzeyi)* altındaki kuralı ekleyin (`surface.drift > 0`); alarm mevcut bildirim kanallarına ve SIEM'e gider, sapmalar gözden geçirilince kapanır. **Sınırlar:** referans, gözlemlenen veriye dayanır; izlenmeyen bir makinenin veya HTTP probe'u olmayan bir sitenin yüzeyi görünmez. Bu bir zafiyet tarayıcısı değildir.

### Sapma ve referans yorumları

Sapmalar ve Referans durum tablolarındaki **Yorum** sütunu her satır için üç şey söyler: **Ne oldu** (örn. "Yeni dinleyen port: 3306 (MySQL)"), **Neden önemli** (riskin açıklaması) ve **Ne yapılmalı** (önerilen çözüm). Yeni/kaybolan port, riskli port, yeni uç nokta, riskli yol (admin, debug, .env, swagger vb.), eski TLS sürümü ve sürüm değişimi, güvenlik başlığı kaybı, sunucu bilgisi (banner) değişimi ve 4xx tarama izi için ayrı yorumlar vardır. Referans durum satırı; referansa girmiş riskli port, eski TLS, hiç görülmeyen güvenlik başlığı ve veri eksikliği için uyarır (ilk gözlem referans sayıldığından bunlar sapma üretmez). Aynı yorumlar Asset kartı › Güvenlik sekmesinde ve Olaylar zaman çizelgesindeki "Sapma kaydı" satırlarında da görünür. Yorumlar kural tabanlıdır (sabit metin); yapay zeka çağrısı yapılmaz, çevrimdışı kurulumda da çalışır. Öneriler genel güvenlik tavsiyesidir, kurumunuzun değişiklik sürecinin yerine geçmez.

## Saldırı yüzeyi › Envanter

Sapmalar sekmesinin yanındaki **Envanter** sekmesi, makinelerde **ajanın** topladığı bilgiyi gösterir (ajan v1.6.0+; Ajanlar sayfasında "Saldırı yüzeyi envanteri" veri türü açık olmalıdır, `SEND_INVENTORY`). Ajan 30 dakikada bir (`APM_INVENTORY_EVERY`, saniye) şunları gönderir:

- **Dinleyen portlar:** port, süreç adı, ağdan mı yoksa yalnızca yerelden mi (127.0.0.1) erişilebildiği, kapsayıcı olup olmadığı (Linux: `/proc`; Windows: yalnızca port + süreç adı).
- **Çalışma zamanı sürümü:** Node.js, Java, PHP, Python, Ruby, .NET, nginx, Apache httpd, PostgreSQL, MySQL/MariaDB, Redis, MongoDB, OpenSSH, Go. Sürüm, yalnızca beyaz listedeki `--version` çağrısıyla alınır.
- **Bağımlılıklar:** süreç çalışma dizinindeki manifest dosyalarından **yalnızca paket adı ve sürüm**: `package-lock.json`/`package.json`, `composer.lock`, `requirements.txt`/`poetry.lock`/`Pipfile.lock`, `go.mod`, `*.deps.json`, jar dosya adları, Go ikili bilgisi. Ek tarama kökleri için `APM_INVENTORY_DIRS` (iki nokta üst üste ile ayrılmış). Dosya içeriği sunucuya gönderilmez.

Sayfa makine başına özet (dinleyen servis, destek dışı sürüm, bağımlılık, zafiyet) ve **Yorum** sütunu gösterir; satıra tıklayınca portlar, sürüm destek durumu, zafiyetler ve bağımlılıklar açılır.

**Destek durumu:** üretici takvimine göre (Destekli / Destek bitiyor (180 gün) / Destek dışı). `eol.json` ile (`APM_DATA_DIR`) takvim geçersiz kılınabilir.

**Zafiyet (CVE) eşleme:** sunucu, OSV biçimindeki kayıtları `APM_ADVISORY_DIR` dizininden (yoksa veri dizini) yükler. İnternete çıkabilen sunucuda `APM_ADVISORY_AUTO=1` ekosistem arşivlerini (npm, Packagist, PyPI, Maven, Go, NuGet) günlük indirir; kapalı ağda aynı `all.zip` dosyalarını osv.dev'den elle dizine koyup "Zafiyet kayıtlarını yenile"ye basın. Eşleşme paket adı + sürüm aralığına dayanır; Java'da jar adı, package.json'da bildirilen sürüm kullanıldığından sonuçlar **tahmini**dir. Bu bir zafiyet tarayıcısının yerini tutmaz.

Alarm metrikleri: `inventory.eol_count`, `inventory.vuln_critical`, `inventory.vuln_high` (şablonlar: Güvenlik grubu). API: `GET /api/inventory`, `GET /api/inventory/{asset}/deps`, `POST /api/inventory/advisories/reload` (yönetici).

## Asset kartı

İzleme › Assetler'de bir asset'e (veya Alarmlar, Topoloji panelindeki "Asset kartı" düğmesi ve kart içindeki bağlantılara) tıklayınca **Asset kartı** açılır; adres `#kart/<asset no>` olduğundan bağlantı paylaşılabilir. Üstte durum ve anlık kutular (açık alarm, CPU, bellek, disk, yanıt süresi), altında sekmeler vardır; sekmeler açılınca veri çeker:

| Sekme | İçerik |
|---|---|
| **Genel** | Durum rozeti **Kritik / Uyarı / Sağlıklı** ve altında **Nedenler ve önerilen çözümler** tablosu: durumu etkileyen her etken (erişilemiyor, açık alarmlar, kritik/yüksek zafiyet, riskli açık port, destek dışı sürüm, saldırı yüzeyi sapması, hatalı probe/senaryo, SLA) ayrı satırda, nedeni ve önerilen çözümüyle; **Aç** düğmesi ilgili sekmeye götürür. Ardından özet kutuları (bağımlılık, etkilenen, dinleyen servis, zafiyet, saldırı yüzeyi, SLA), tanım alanları (IP adresleri, açık kalma süresi, yük, bellek, kurulu bağlantı), bağlı probe ve senaryolar |
| **Yazılım envanteri** | Dinleyen portlar, süreç, çalışma zamanı sürümü ve destek durumu, zafiyetler, bağımlılıklar (Saldırı yüzeyi › Envanter ile aynı veri) ve ajanın süreç/uygulama listesi |
| **Topoloji ve bağlantılar** | Bana bağımlı olanlar → asset → benim bağımlı olduklarım (kutuya tıklayınca o asset'in kartı), düşerse etkilenenler ve bağımlı olduğu zincir, ajanın gözlediği ağ bağlantıları (topolojide tanımlı olup olmadığı ile) |
| **Performans** | Metrik eğilimleri (1 sa / 6 sa / 24 sa), uygulama istek/hata/p95 ve yorum, en yoğun uç noktalar, en çok zaman harcayan sorgular |
| **Güvenlik** | Açık sapmalar, ağdan erişilebilir ve riskli portlar, sertifika kalan gün, zafiyet özeti, öğrenilmiş referans |
| **Olaylar** | Alarmlar (açık ve çözülen), sapmalar, senaryo olayları ve günlük saklama açıksa uyarı/hata günlükleri tek zaman çizelgesinde; tür filtresi |

Kartta yeni veri toplanmaz; mevcut sayfaların verisi bu asset'e göre süzülüp bir araya getirilir. Ajansız assetlerde (web sitesi, servis) envanter ve sunucu metrikleri bulunmaz; bu sekmeler ne eksik olduğunu söyler. Eski "Asset ayrıntısı" penceresi (genel bakışta göster, disk ayrıntısı) Genel sekmesindeki düğmeyle hâlâ açılır.

## Pano widget'ları: ağ ve altyapı servisleri

Genel bakış › Panoyu düzenle › "Widget ekle" ile (veya hazır **Ağ ve altyapı servisleri** şablonuyla) şu widget'lar eklenir. Hepsi ilgili türdeki **tüm** probe'ları kapsar ve yüzlerce probe'da bile pano üzerinde az yer kaplar:

- **Özet şerit:** durumların sayısı renkli bir şeritte gösterilir; bir dilime (ya da altındaki etikete) tıklayınca kareler o duruma göre süzülür, tekrar tıklayınca süzme kalkar.
- **Kareler:** her probe bir karedir; üzerine gelince ad, hedef ve durum görünür.
- **Detay paneli:** kareye tıklayınca sağdan panel açılır (durum, hedef, son çalıştırma, probe'un tüm ayrıntı satırları, hata iletisi, **Asset kartı** ve **Probe'lara git** düğmeleri). **Listeyi aç** düğmesi (süzme varsa süzülmüş) aranabilir ve sayfalı tam listeyi aynı panelde açar; satıra tıklayınca ayrıntı, "← Liste" ile geri dönülür. Esc veya Kapat ile kapanır.

**SSL renkleri alarm sürecine göredir:** kare ve şerit rengi `probe.cert_days_left` alarm kurallarının eşiklerinden (Kritik / Uyarı / Bilgi) ve o asset için açık alarmdan gelir; süresi dolmuş sertifika her zaman kırmızıdır. Alarm kuralı hiç tanımlı değilse varsayılan eşikler kullanılır (14 gün uyarı, 3 gün kritik) ve widget bunu not eder. Karede dolu nokta **alarm açık**, boş halka **alarm üstlenildi** demektir; panelde alarmın kuralı, açılış zamanı, üstlenen kişi ve notu görünür. Alarmlar › Kurallar bölümündeki "Sertifika süresi doluyor" ve "Sertifika kritik" şablonları bu renkleri belirler. (Eski "Sertifika süreleri (TLS)" widget'ı kaldırıldı; kayıtlı panolardaki karşılığı bu widget'a çevrilir.)

| Widget | Gösterir |
|---|---|
| **SSL sertifikaları (tümü)** | Alarm kurallarına göre renkli kareler (süresi doldu / kritik / uyarı / bilgi / alarm yok / ölçülemedi); panelde sahibi, veren, bitiş tarihi, kalan süre, alarm ve üstlenen. En yakın bitiş ilk karede. |
| **DNS kontrolleri (tümü)** | Sorgu, kayıt türü, DNS sunucusu, dönen cevaplar, süre ve durum; hatalılar önde. |
| **Saat sapması · NTP (tümü)** | İşaretli sapma orta çizgiden sağa/sola uzayan çubukla (±500 ms), stratum. |
| **Port erişimi · TCP/UDP (tümü)** | Her hedef için erişilebilirlik kutusu, süre ve başarı oranı. |
| **Ping · ICMP (tümü)** | Paket kaybı ve gecikme çubukları. |
| **Yol izleme (tümü)** | Sıçrama sayısı, RTT, kayıp ve rota değişimi. |
| **Veritabanı probe'ları (tümü)** | PostgreSQL erişim ve sağlık (pgstats) probe'larının durumu ve hata iletisi. |

Veri, Performans sayfasının kullandığı `GET /api/perf/network` (son 1 saat) ve probe listesinden gelir; yeni toplama yoktur. Yalnızca etkin probe'lar listelenir. Aynı asset üzerinde birden çok probe varsa, probe'a ayrılamayan gecikme/sıçrama değerleri "—" görünür. Sertifika zincirini doğrulayamayan (kendinden imzalı, süresi dolmuş) hedeflerde bitiş tarihi okunamaz; widget bu durumda "ölçülemedi" ve hata iletisini gösterir.

## Senaryolar

*İzleme › Senaryolar* sayfasında gerçek bir Chromium tarayıcısıyla web sitesi adım adım test edilir (giriş, gezinme, metin/öğe kontrolü). Her çalıştırmada ekran görüntüsü alınır, sayfa yapısı bir öncekiyle karşılaştırılır. Senaryo çalıştırma geçmişi 50'şerli sayfalarla listelenir.

### Sayfa değişti uyarısı

Bir sayfanın yapısı değiştiğinde **Sayfa değişti** rozeti çıkar. Rozete (Genel bakış, Senaryolar listesi) veya **Ayrıntıları göster** düğmesine tıklayınca hangi adımda/sayfada neyin **eklendiği** ya da **kaldırıldığı** (başlık, buton, bağlantı, form, giriş alanı, harici script) tespit zamanıyla listelenir. Karşılaştırma bir önceki çalıştırmayadır; **Uyarıyı temizle** işareti kaldırır.

### Yoksayma kuralları (istisnalar)

Her seferinde değişen öğeler (ziyaretçi sayacı, token, tarih, reklam) için istisna tanımlanır. Değişen satırın yanındaki düğmeler:

- **Değeri yoksay:** Öğe sayfada durmaya devam etmelidir; yalnızca değeri değişince uyarılmaz (sayaç, token, oturum kimliği, tarih). Öğe kaybolursa yine uyarılır. APP_MON eklenen ve kaldırılan benzer öğeyi karşılaştırıp değişen kısmı bulur; token'a benzeyen değerlerde `token?` rozeti görünür.
- **Öğeyi yoksay:** Öğe karşılaştırmaya hiç girmez; eklenmesi veya kaldırılması uyarı üretmez (reklam, banner, slayt).

Kural yalnızca o senaryonun o adımına uygulanır. Gerçek bir değişikliği de gizleyebileceği için ekleme anında **parola** sorulur (yerel veya LDAP hesabı; API anahtarıyla girdiyseniz anahtar yeniden girilir). Yanlış denemeler IP başına sınırlanır ve denetim kaydına yazılır. Çok geniş kurallar reddedilir. Kurallar **Yoksayma kuralları** listesinden (parola gerekmeden) kaldırılır; kural bir sonraki kontrolden itibaren geçerlidir, mevcut uyarı için **Uyarıyı temizle** deyin. Yetki: işletmeci ve yönetici.

### Hızlı durum probe'u (isteğe bağlı)

Senaryo uzun aralıkla (ör. 5 dk) çalıştığı için çöken bir sayfa geç anlaşılabilir. Oluştururken/düzenlerken **Hızlı durum probe'u** olarak bir probe (ör. 30 sn'lik HTTP) seçebilirsiniz. Liste önce aynı asset'in probe'larını, ardından "Başka asset'ler" altında diğerlerini gösterir. Seçilince Genel bakıştaki kart:

- Probe **üst üste 2 kez** başarısız olursa senaryoyu beklemeden **kırmızı** olur ve nedeni yazar. Tek anlık hata kartı boyamaz.
- Probe tekrar başarılı olunca probe kaynaklı kırmızı kalkar; senaryonun kendi sonucu kötüyse kart kırmızı kalır.
- Asset **bakım penceresindeyse** kart boyanmaz ("bakım penceresinde"). Probe verisi eskimişse renk değişmez ("güncel veri yok").

Probe yalnızca "ulaşılamıyor/hata dönüyor" durumunu yakalar; sayfa 200 dönüp içeriği bozuksa bunu ancak `expect_text` tanımlıysa görür. Probe silinirse eşleşme kalkar.

### Keşfet: web sitesi bileşenlerini bulma

*Senaryolar › Keşfet* (işletmeci ve yönetici). Bir adresten başlayıp sitenin sayfalarını, formlarını, yüklenen kaynaklarını (script/CSS/görsel/font), arka plan API çağrılarını (XHR/fetch), dış alan adlarını ve TLS sertifika sürelerini çıkarır; işaretlediklerinizi tek tıkla probe olarak izlemeye alırsınız (sayfa/API/yol → HTTP probe, sertifikalı alan adı → TLS probe).

**Seçenekler (hiçbiri varsayılan seçili gelmez):**

- **Siteyi gez (spider):** sunucudaki Chromium ile aynı alan adındaki sayfaları gezer (tek sayfa uygulamaları dahil). sitemap.xml adresleri tohum olur. Form gönderilmez, düğmeye tıklanmaz; çıkış/silme/sıfırlama bağlantıları ve dosya indirmeleri atlanır.
- **Yaygın yol sözlüğü:** bağlantısı verilmeyen ama sık kullanılan yolları dener (Kısa: 71, Geniş: yaklaşık 175 yol; sağlık/durum/metrik uç noktaları, API belgeleri, giriş sayfaları) ve ek yollar yazabilirsiniz. Yedek, yapılandırma, `.env`, `.git` gibi hassas yollar bilerek yoktur; bu bir güvenlik açığı tarayıcısı değildir. Her yola 200 dönen siteler (SPA) için taban çizgisiyle aynı içerik elenir.
- **Oturumlu sayfalar:** kayıtlı, "giriş gerektirir" ayarlı bir senaryonun giriş bilgisiyle oturum açılır ve giriş sonrası sayfalar gezilir (sonuçta "oturumlu" etiketi). Yalnızca senaryonun giriş yaptığı alan adında çalışır. Salt-okunur, ayrılmış bir test hesabı kullanın.

**Güvenlik çerçevesi:**

- Başlatırken uyarı çıkar; "bu sitenin sahibi olduğumu veya izleme yetkim bulunduğunu beyan ederim" kutusu işaretlenmeden başlatılamaz.
- **Yönetici onayı:** yönetici başlatırsa iş hemen çalışır; işletmeci başlatırsa iş "Onay bekliyor" durumuna düşer ve yönetici Onayla/Reddet der. İstek, onay, red, iptal ve bitiş denetim günlüğüne yazılır.
- **robots.txt her zaman uygulanır** (Disallow/Allow, joker karakterler, Crawl-delay). robots.txt sunucu hatasıyla alınamazsa güvenli tarafta kalınıp tarama yapılmaz. Atlanan yollar "Atlananlar" sekmesindedir.
- Sınırlar: en çok 100 sayfa (varsayılan 30), derinlik ≤3 (varsayılan 2), istekler arası en az 200 ms (varsayılan 500 ms), 12 dk üst süre, aynı anda tek keşif, saatte en çok 10 istek, en çok 20 onay bekleyen. Tarayıcı kimliği `APP_MON-Discovery/1` olarak eklenir.
- SaaS modunda özel ağ adreslerine tarama yapılamaz.

**Sonuç sekmeleri:** Sayfalar, API çağrıları, Kaynaklar (en yavaş 600), Alan adları (kendi/dış, hata sayısı, sertifika günü), Sözlük bulguları, Atlananlar.

## Alarmlar

### Alarm panosu

Sorunlu hizmetler renk ve şekille gösterilir (renk körlüğüne dayanıklı):

| Önem | Gösterim |
|---|---|
| Kritik | Kırmızı sekizgen |
| Erişilemiyor | Çarpılı daire |
| Uyarı | Sarı üçgen |
| Bilgi | Mavi daire |
| Sağlıklı | Yeşil |

Halka grafik açık alarmların önem dağılımını gösterir; dilime tıklamak listeyi süzer. Liste (açık/kapanan) 50'şerli sayfalarla gösterilir.

### Kurallar

Bir kural: **ad, metrik, koşul** (>, <, …), **eşik**, **pencere (sn)** ve **önem** içerir. Kural, pencere içindeki **ortalama** değeri eşikle karşılaştırır; 15 sn'de bir değerlendirilir. Düzenlenen kuralın açık alarmları kapanır, koşul sürüyorsa yeniden açılır. **Hazır şablonlar** ile tek tıkla eklenir.

- **Kapsam:** hizmetler (boş = hepsi), grup ve etiketle kuralı sınırlayın.
- **Üstlenme:** açık alarmda **Üstleniyorum** ile sahiplenin, **Geri al** ile bırakın; kimin ne zaman üstlendiği listede görünür.
- **Örnekler:** `asset.down > 0` (30 sn), `host.cpu_pct > 90`, `host.disk_pct > 85`, `proc.nginx.up < 1`, `db.connections_pct > 85`, `net.path.changed > 0`, `log.nginx_hata.count > 10`.

### Alarmda "Neden?" (kök neden analizi)

Alarmlar sayfasında (ve Genel bakıştaki açık alarm listesinde) her alarmın yanındaki **Neden?** düğmesi, alarmın olası nedenini gösterir. APP_MON alarmlı asset'in ve topolojide bağımlı olduğu asset'lerin (en çok 3 seviye) ölçümlerine bakar; alarmdan önceki bir saatin başındaki duruma göre ani değişimleri bulur, ne zaman başladıklarını belirler ve şu kurallarla puanlar:

- Değişim alarmdan **önce** başladıysa güçlü, sonra başladıysa zayıf kanıttır (sonuç olabilir).
- Bağımlılıktaki (ör. veritabanı) değişim, alarmlı asset'in kendi değişiminden biraz daha ağırlıklıdır.
- Erişilemezlik (`asset.down`), rota değişimi ve sertifika değişimi ek ağırlık alır.
- Alarmın kendi ölçümü neden sayılmaz; sayaç ve boyut türü ölçümler (`db.xact_*`, `host.uptime_sec` …) dışlanır.

Sonuç "Yüksek / Orta / Düşük güven" etiketli bir sıralı liste ve alarm öncesindeki **yönetim değişiklikleri** (denetim günlüğünden: probe/kural/topoloji değişikliği vb.) olarak gösterilir. Örnek: "demo-db › host.conn_rtt_ms: 20 → 900, alarmdan 6 dk önce başladı — Yüksek güven". Bu bir **tahmindir**, kesin kanıt değildir. Değişimlerin görünmesi için ilgili ölçümlerin (probe, SSH bağlantı verisi, ajan) alarmdan en az ~20 dakika önce toplanıyor olması gerekir. Dış servis veya model kullanılmaz; her şey sunucuda hesaplanır. API: `GET /api/alerts/{id}/rca`.

### Bakım pencereleri

Tek seferlik, günlük veya haftalık; hizmet, grup veya etiket kapsamında. Pencere sırasında alarm yine kaydedilir ("bakımda" etiketi) fakat **bildirim gönderilmez**; SLA hesabında da kesinti sayılmaz.

### Anomali tespiti

Her 2 dakikada, son 5 dakikanın ortalaması son 30 dakika hariç 7 günlük geçmişle karşılaştırılır (medyan ve MAD ile güçlü z-skoru) ve `anomaly.latency_z`, `anomaly.cpu_z`, `anomaly.mem_z` metrikleri yazılır (geçmişte en az 300 örnek gerekir). Hazır kurallar (skor > 6, 10 dk) **KAPALI** gelir; açarak kullanın. Günün saatine/haftanın gününe göre mevsimsellik hesaba katılmaz (iş saati/gece farkı olan seriler yanlış alarm verebilir; eşiği yükseltin). Geçmiş, ham ölçüm saklama süresiyle (varsayılan 14 gün) sınırlıdır.

## Kanıt zinciri

Alarm ve raporlar › Kanıt zinciri (yalnızca yönetici), "kayıtlar sonradan değiştirilmedi" iddiasını ispatlanabilir hâle getirir. SLA tartışmalarında, iç denetimde ve kurum denetimlerinde kullanılır.

**Hash zinciri.** Her denetim kaydı (kim, ne, ne zaman) bir önceki kaydın SHA-256 özetiyle bağlanır; zincir kiracı başınadır. Bir kaydın içeriği değiştirilirse, araya kayıt eklenirse veya ortadan kayıt silinirse zincir kırılır; son kayıtlar silinirse "zincir başı" kaydı bunu yakalar. *Zinciri doğrula* düğmesi tümünü baştan sona denetler ve kırılan ilk kaydı gösterir. v2.6.0 öncesindeki kayıtlar zincire dahil değildir ("eski kayıt" olarak sayılır); saklama süresi nedeniyle silinen en eski kayıtlar zinciri bozmaz.

**İmzalı aylık rapor.** *Rapor üret ve imzala* seçilen ay için şunları içeren bir rapor üretir: asset bazında kullanılabilirlik ve SLA hedefi, açılan alarmlar ve süreleri, dönemde yapılan yönetim değişiklikleri (denetim kayıtları), zincir durumu ve zincir başı (son kayıt kimliği + hash). Rapor, sunucunun Ed25519 anahtarıyla imzalanır (özel anahtar kasa ile şifreli saklanır; kasa etkin olmalıdır) ve *İndir (.json)* ile paket olarak alınır. Süren dönem için üretilen rapor "kısmi"dir.

**Doğrulama.** (1) *Raporu doğrula*: paketi seçin; imza, anahtarın bu sunucuya ait olduğu ve raporun zincir başının bugünkü kayıtlarla eşleştiği gösterilir. (2) Sunucudan bağımsız: *Genel anahtarı indir* ile PEM alın ve OpenSSL 3 ile doğrulayın: `jq -r .report_json paket.json > rapor.json`, `jq -r .signature paket.json | base64 -d > imza.bin`, `openssl pkeyutl -verify -pubin -inkey anahtar.pem -rawin -in rapor.json -sigfile imza.bin`.

**Dürüst sınırlar:** Veritabanına tam erişimi olan biri tüm zinciri yeniden üretebilir; bu yüzden raporları **sunucu dışında** (e-posta, arşiv, noter/zaman damgası) saklayın; doğrulama o raporun zincir başıyla karşılaştırır ve yeniden üretilmiş bir zinciri yakalar. Sistem saati yanlışsa kayıt zamanı da yanlış olur. Bu özellik kayıtların *değiştirildiğini tespit eder*, değişmesini engellemez. API: `GET /api/evidence/chain`, `GET/POST /api/evidence/reports`, `GET /api/evidence/reports/{id}/package`, `POST /api/evidence/verify`, `GET /api/evidence/public-key`.

## Bildirimler (yönetici)

Kanal ekleyin; her kanalda **en düşük önem**, **çözüldü bildirimi** ve **Test gönder** vardır. Gizli alanlar kaydedildikten sonra gösterilmez, düzenlerken boş bırakırsanız eski değer korunur. Alıcılar virgülle ayrılır; hata olursa kanalın **son hatası** kartta görünür.

| Kanal | Gerekenler |
|---|---|
| SMTP | Sunucu, port (587 STARTTLS / 465 SSL / 25), kullanıcı, parola, gönderen, alıcılar |
| Microsoft Graph | Tenant ID, Client ID, Client Secret, gönderen posta kutusu (Azure'da Mail.Send uygulama izni) |
| Exchange EWS | EWS adresi (…/EWS/Exchange.asmx), kullanıcı, parola (yalnızca Basic) |
| Windows MAPI | Outlook yüklü ve oturum açık Windows'ta APP_MON yerel çalışmalı; Docker/Linux'ta çalışmaz |
| Webhook | URL (JSON POST) |
| Slack / Teams | Gelen webhook URL'si |
| Telegram | Bot token ve chat ID |

`APM_PUBLIC_URL`, bildirimlerdeki bağlantıların adresidir (ör. `https://apm.sirket.local`).

### SIEM'e log gönderimi

Bildirimler sayfasının altındaki bölümden hedef eklenir. Denetim kayıtları (kim neyi değiştirdi) ve alarmlar (açılış/düzelme) olay anında iletilir; bakım penceresindeki alarmlar da iletilir ("bakım penceresinde" notuyla).

| Ayar | Açıklama |
|---|---|
| Protokol | Syslog UDP (514), Syslog TCP (514), Syslog TLS (6514) veya HTTP (Splunk HEC / genel JSON) |
| Biçim | RFC 5424 (yapılandırılmış veri ile), CEF (ArcSight, QRadar, Sentinel), JSON |
| Facility / uygulama adı | Syslog facility (varsayılan 16 = local0) ve app-name (varsayılan appmon) |
| Kaynaklar | Denetim kayıtları ve/veya alarmlar; alarmlar için en düşük önem seçilebilir |
| HTTP | Adres + kimlik doğrulama (Splunk HEC token veya Bearer); gövde JSON'dur |

- Önem eşlemesi: kritik = syslog 2 / CEF 9, uyarı = 4 / 6, bilgi = 6 / 3.
- **Deneme gönder** hedefe örnek olay yollar; hata olursa hedef kartında "Son hata" görünür.
- Syslog UDP'de teslim garantisi yoktur; güvenilir iletim için TCP veya TLS seçin. Kendinden imzalı sertifikada "TLS sertifika doğrulamasını atla" işaretlenebilir.

## SLA raporu

*Alarm ve raporlar › SLA.* Erişilebilirlik, asset durumu (`asset.down`) ölçümlerinden **süre ağırlıklı** hesaplanır: her ölçüm, bir öncekinden bu yana geçen süreyi (en çok 5 dk) temsil eder; böylece farklı aralıklı probe'lar doğru ağırlıklanır. Bakım pencerelerindeki kesinti saniyeleri hem kesintiden hem gözlem süresinden düşülür.

- Varsayılan hedef **%99,9**; her asset için hedef tablodan değiştirilir (boş bırakmak varsayılana döndürür).
- **Dönem:** seçilen ay, bugün, son 24 saat, bu hafta (Pazartesi'den), son 7 gün veya son 30 gün. **CSV indir** Excel'de açılabilen rapor verir (Türkçe karakterler korunur); zaman filtresi CSV'nin ilk satırında yazılır.
- **Zaman filtresi:** *7/24 (tüm zaman)*, *Mesai saatleri 08:00–17:00 (Pzt–Cum)*, *Geniş mesai 07:00–24:00 (her gün)* veya *Özel saat ve gün*. Özelde başlangıç 0-23, bitiş 0-24 girilir (ör. 8-17, 7-24; gece penceresi için 22-6) ve günler işaretlenir. Yalnızca seçili saat ve günlerdeki ölçümler SLA'ya katılır; filtre dışındaki kesintiler sayılmaz (ör. gece bakımı mesai SLA'sını bozmaz). Saat dilimi sunucunun yerel saat dilimidir.
- **Gruplama:** *Asset başına özet* (varsayılan), *Saatlik*, *Günlük* veya *Haftalık*. Kırılım tablosu tüm asset'lerin toplam (süre ağırlıklı) erişilebilirliğini dönem dönem gösterir; kırmızı çubuk hedefin altındaki dönemdir.
- **Saatlik özet sınırı:** Zaman filtresi ve saatlik/haftalık kırılım, saatlik özetten (`sla_hourly`) hesaplanır. Bu özet v2.8.0 ile birlikte tutulmaya başlar ve açılışta ham ölçümlerin bulunduğu süre kadar geriye doldurulur (varsayılan 14 gün); daha eski aylar yalnızca tam gün/7-24 raporlanabilir. Sayfada "en eski saatlik kayıt" yazılır. Saatlik özet 400 gün tutulur.
- **SLA sıfırlama (yalnızca yönetici):** tablodaki **Sıfırla** düğmesi o asset'in, **Tümünü sıfırla** tüm asset'lerin SLA özetini siler ve hesaplamaya sıfırlama anından başlar (yeni bir sözleşme dönemi veya taşınma sonrası temiz başlangıç için). İşlem **yönetici parolanızı** (API anahtarıyla girdiyseniz anahtarı) yeniden ister; denetim kaydına `sla_reset` olarak yazılır ve geri alınamaz. Ham ölçümler, alarmlar ve denetim kaydı silinmez; durum sayfasındaki 90 günlük erişilebilirlik de sıfırlanır.
- Günlük özetler 800 gün tutulur; ham ölçümler saklama süresi (varsayılan 14 gün) dolunca silinse de eski aylar raporlanabilir. Özet, ham verisi duran günler için açılışta ve her 10 dakikada yenilenir.
- Sınır: durum ölçümü olmayan (probe'suz, yalnızca ajan) asset'ler raporda görünmez.

## Raporlar

*Alarm ve raporlar › Raporlar.* Canlı veriyle üretilen önizlemeli raporlar. Önizleyip **Yazdır / PDF** ile PDF kaydedebilir, **CSV** (Excel için `;` ayraçlı) veya **JSON** olarak indirebilirsiniz. İmzalı ve zincirli aylık rapor için *Kanıt zinciri* sayfasına bakın.

**Özel rapor oluştur.** Sayfanın üstündeki oluşturucuda bölümleri kutularla seçin: Genel özet, Envanter dağılımı, Erişilebilirlik, Alarmlar ve eğilim, Uygulama performansı, Yavaş veritabanı sorguları, Kullanıcı senaryoları, Sertifika süreleri, Ajanlar, Saldırı yüzeyi, Kök neden ve etki. **Dönem** (24 saat / 7 gün / 30 gün) ve **kapsam** (tüm varlıklar veya tek bir grup) seçilir; alarm sayısı ve MTTR önceki eşit uzunluktaki dönemle karşılaştırılır. Seçimi bir ad vererek **şablon** olarak kaydedin (bu tarayıcıda saklanır, en çok 30); hazır şablonlar: Operasyon haftalık, Performans ve veritabanı, Yönetim aylık.

**Hazır raporlar:** Yönetici özeti, Erişilebilirlik, Alarmlar, Envanter, Probe'lar, Topoloji ve Denetim kaydı. Not: uygulama performansı ve veritabanı sorguları bölümleri ancak ajan, erişim günlüğü veya trace verisi varsa dolar; veri yoksa bölüm nedenini yazar. Ajanlar bölümü yönetici yetkisi gerektirir (sürüm dağılımı için).

## Ağ akışı (NetFlow / IPFIX)

*İzleme › Performans › Ağ akışı.* Router, L3 switch veya güvenlik duvarınız **NetFlow v5, v9 veya IPFIX** gönderebiliyorsa APP_MON "kim kiminle ne kadar konuşuyor" bilgisini gösterir: en çok gönderen/alan adresler, protokol ve hedef port dağılımı, en çok konuşan çiftler. Adresi bir asset'e ait olanlar asset adıyla görünür (eşleştirme: ajanın bildirdiği adresler ve IP olan probe hedefleri).

**Açmak (varsayılan kapalı):** sunucuya şu ortam değişkenlerini verip yeniden başlatın:
- `APM_NETFLOW_ADDR=:2055` — dinlenecek UDP adresi.
- `APM_NETFLOW_ALLOW=192.0.2.1,192.0.2.2` — kabul edilen ihracatçı cihazların IP'leri (CIDR de olur). **Boş bırakırsanız her kaynaktan paket kabul edilir; UDP sahteleştirilebildiği için mutlaka doldurun.**
- İsteğe bağlı: `APM_NETFLOW_BUCKET_SEC` (özet aralığı, varsayılan 300), `APM_NETFLOW_TOPN` (aralık ve ihracatçı başına saklanan konuşma, varsayılan 500), `APM_NETFLOW_RETENTION_DAYS` (varsayılan 7), `APM_NETFLOW_SAMPLING` (cihaz örnekleme oranı bildirmiyorsa çarpan, varsayılan 1).
- Cihazda "flow export" hedefini APP_MON sunucusunun IP'si ve bu port yapın; güvenlik duvarında UDP'ye izin verin. **Docker'da** UDP portunu yayınlamak için `docker-compose.override.yml` içine `server` altında `ports: ["2055:2055/udp"]` ekleyin ve `environment:` ile değişkenleri verin.

**Ne saklanır:** ham akış saklanmaz. Her aralıkta ve ihracatçı başına en çok N konuşma (kaynak, hedef, hedef port, protokol, bayt, paket) tabloya yazılır, kalanı tek "diğer" satırında toplanır; saklama süresi sonunda silinir. Örnekleme (sampling) bildiren cihazlarda değerler örnekleme oranıyla çarpılır. Yaklaşık boyut: 5 dk aralıkta 500 konuşma ≈ 15 MB/gün/ihracatçı.

**Topoloji ve alarm:** iki ucu da tanımlı asset olan konuşmalar Topoloji sayfasında **Ağ akışlarını göster** düğmesiyle noktalı çizgiler olarak görünür; bunlar bağımlılık sayılmaz, kök neden analizini ve yerleşimi etkilemez. Adresi eşleşen asset'ler için `flow.tx_bps` ve `flow.rx_bps` metrikleri yazılır (alarm kuralı yazılabilir, ör. `flow.tx_bps > 500000000`). Bir NetFlow ihracatçısı kendi başına asset sayılmaz.

**Sınırlar:** yalnızca IPv4/IPv6 adres, port, protokol, bayt ve paket okunur (AS, arayüz, TOS yoktur); sFlow desteklenmez; ihracatçı şablonlarını periyodik olarak yeniden göndermelidir (v9/IPFIX), aksi halde "şablon bekleniyor" görünür. Cihaz yalnızca kendi üzerinden geçen trafiği görür. SaaS modunda ihracatçıların sunucuya doğrudan UDP ile erişmesi gerekir; genellikle on-prem için kullanılır.

## Ağ araçları

*Araçlar › Ağ araçları.*

- **Yolu izle:** hedef girin; TTL artırılarak IPv4 yolu çıkarılır, her sıçrama için adres, ad, gecikme ve kayıp gösterilir. Ham ICMP yetkisi gerekir (Docker'da varsayılan; RHEL'de systemd birimi `CAP_NET_RAW` verir). Güvenlik duvarları ICMP "süre doldu" yanıtını engelliyorsa o sıçrama `*` görünür; dönüş yolu farklı olabilir; yalnızca IPv4.
- **Ağ keşfi (yönetici):** CIDR girin (en çok /22); her adrese ping ve yaygın portlar denenir, bulunanlar listelenir; seçtiklerinizi tek tıkla ICMP ve açık portlar için TCP probe olarak ekleyebilirsiniz. Arka planda çalışır, iptal edilebilir. Yalnızca yetkili olduğunuz ağlarda kullanın; güvenlik sistemlerinde alarm üretebilir. SaaS modunda kapalıdır; yalnızca IPv4.
- **SNMP trap'leri:** alıcıyı açmak için sunucuya `APM_SNMP_TRAP_ADDR` (ör. `:1162`) ve `APM_SNMP_TRAP_COMMUNITY` verin; cihazlarda trap hedefini bu sunucunun UDP trap portuna ayarlayın. Topluluk adı uyuşmayan paketler atılır. Kaynak IP bir probe hedefiyle eşleşirse trap o asset'e bağlanır, `snmp.trap` metriği yazılır ve SIEM'e olay gider. Liste saklama süresince tutulur. 1024 altı portlar (162) ayrıcalık ister; ayrıcalıksız çalışırken 1162 kullanıp cihazlarda o portu verin veya yönlendirin. v3 trap ve Inform yanıtı yoktur.

Yol izleme ve keşif yalnızca yetkili olduğunuz hedeflerde kullanılmalıdır.

## Ajanlar

*Araçlar › Ajanlar* (yönetici). Ajan; bağımlılığı olmayan tek dosyalık Windows/Linux programıdır. Sunucuya ulaşılamazsa ölçümleri tamponlar ve bağlantı gelince gerçek zaman damgalarıyla gönderir. İzlenen makine otomatik asset olarak eklenir (lisansa sayılır).

**Gönderdiği ölçümler:** CPU, bellek, takas, disk (her sürücü/bağlama noktası: `host.disk_pct:<yol>`; ana disk `host.disk_pct`), ağ trafiği (`host.net_rx_bps`, `host.net_tx_bps`), süreç sayısı, açılış süresi ve (Linux) yük ortalaması. `-watch nginx,sshd` ile belirtilen süreçler için `proc.<ad>.up` (1/0) ve `proc.<ad>.count` üretilir (ör. kural `proc.nginx.up < 1`).

**Kurulum:**

1. Sayfada işletim sistemi, mimari, sunucu adresi ve (isteğe bağlı) asset adı / izlenecek süreçleri girin.
2. **Yeni ajan anahtarı oluştur** ile ajan rolünde bir anahtar üretin.
3. Çıkan komutu hedef makinede çalıştırın (Windows: yönetici PowerShell, Linux: root).

**Gönderilen verileri seçme (yapılandırma dosyası):** Kurulum, ajanın yapılandırma dosyasına (Linux `/etc/appmon-agent.conf`, Windows `C:\ProgramData\appmon-agent\agent.conf`) gönderilebilen tüm verileri `SEND_*` satırları olarak yazar. **Bir satırın başına `#` koyarsanız o veri sunucuya gönderilmez**; değişiklikten sonra ajanı yeniden başlatın (Linux `systemctl restart appmon-agent`, Windows görevi durdurup başlatın). Dosyada hiç `SEND_*` satırı yoksa (eski kurulum) her şey gönderilir. Yeniden kurulumda seçimleriniz korunur.

**Uzaktan veri ayarı (Ajanlar sayfası, ajan 1.4.0+):** *Araçlar › Ajanlar* tablosunda her ajan satırının sonunda **Veri ayarı** düğmesi vardır. Açılan pencerede yukarıdaki veri türleri onay kutuları olarak listelenir (Makine, İzleme, Uygulama, Özellik grupları); işaretlediğiniz veriler gönderilir. **Kaydet ve uygula** ile ayar sunucuda saklanır, ajan bir sonraki gönderiminde alır, `remote.json` olarak kalıcı yazar ve servisi birkaç saniyeliğine yeniden başlatır; durum sütununda *Uzaktan · bekliyor* → *Uzaktan · uygulandı* görünür. "Bu seçimi tüm ajanlara uygula" ile tüm ajanlara tek seferde verilir. **Yerel yapılandırmaya dön** uzaktan ayarı kaldırır; ajan makinedeki dosyadaki `SEND_*` seçimlerine döner. Uzaktan ayar yerel dosyadaki `SEND_*` seçimlerinin yerine geçer; makine sahibi bunu `APM_REMOTE_CONFIG=off` (yapılandırma dosyasında) ile yasaklayabilir — o makine "Yerel kilit" gösterir ve ayar almaz. `APM_APPS=off` gibi sabit kapatmalar uzaktan açılamaz. Ajan servis olarak çalışmıyorsa (elle başlatılmışsa) uzaktan ayar uygulanmaz. Her değişiklik denetim kaydına yazılır (`agent.config_set`, `agent.config_clear`).

**Veritabanı bağlantısını Veri ayarı'ndan verme (isteğe bağlı):** Veri ayarı penceresinde "Veritabanı bağlantısı (PostgreSQL)" alanlarına sunucu, port, veritabanı, kullanıcı, parola ve `sslmode` girilirse ajan bu bilgilerle bağlanıp sorgu istatistiklerini gönderir; ajan yapılandırma dosyasına `APM_PG_DSN` yazmak gerekmez (uzaktan verilen bağlantı yereldekinin yerine geçer). Güvenlik: parola Kasa ile şifreli saklanır, arayüze ve API'ye geri verilmez ("Kayıtlı" görünür); yalnızca HTTPS (veya yerel makineden) kaydedilebilir ve ajana yalnızca HTTPS bağlantıda iletilir; yalnızca tek ajan için tanımlanır; ajan bunu 0600 izinli `remote.json` dosyasında tutar; makinede `APM_REMOTE_CONFIG=off` varsa kabul edilmez; denetim kaydına parola yazılmaz. Kullanıcıya yalnızca `pg_monitor` rolünü verin.

**Antivirüs / EDR:** Ajanlar sayfasındaki "Antivirüs / EDR izinleri" bölümü, uç noktada güvenlik ürünü varsa verilecek izinleri (dosya/klasör, SYSTEM zamanlanmış görev, `netstat -ano`, süreç ve `/proc` okuma, yerel `127.0.0.1:4318`, kendini güncelleme, SELinux, TLS denetimi) listeler. İzin vermek istemediğiniz özellikleri Veri ayarı ile kapatabilirsiniz.

**Performans sayfasını ajanla doldurma (ajan 1.4.0+):**

| Sayfa | Veri nereden gelir | Ajanda gereken |
|---|---|---|
| Performans › Uygulamalar | Web sunucusu erişim günlükleri (ajan okur) ve uygulamaların OpenTelemetry trace'leri (giriş span'leri; APP_MON'un kendi `appmon-server` istekleri dahil). Trace kaynaklı satırlar "· trace" etiketi taşır, tıklayınca Dağıtık izleme'ye gider. | `SEND_WEBLOG` (nginx/Apache/IIS makinesinde), `SEND_OTLP` + uygulamanın trace göndermesi (kurulum betiği) |
| Performans › Veritabanı sorguları | PostgreSQL `pg_stat_statements` (toplam/ortalama süre, çağrı, son dönem ortalaması) | Veritabanı makinesindeki ajan yapılandırmasında `APM_PG_DSN=postgres://izleme:PAROLA@127.0.0.1:5432/veritabani?sslmode=disable` (birden çok için `;;`) ve `SEND_DBSTATS`. Parola yalnızca ajanda kalır; sunucuya sorgu özeti gider. İzleme kullanıcısına `pg_monitor` rolü verin; PostgreSQL'de `shared_preload_libraries = 'pg_stat_statements'` ve `CREATE EXTENSION pg_stat_statements` gerekir. Eksikse sayfada nedeni yazar. |
| Performans › Bağlantılar | Makinenin giden ve gelen TCP bağlantı özeti (IP:port ve sayı) ile makine adresleri; ajan makineleri birbirine eşlenip topolojiye bağımlılık önerilir | `SEND_CONNS` + `SEND_APPS` (Linux'ta tüm süreçleri görmek için root) |

**Örnek dosya:** Tüm seçenekleri içeren hazır dosyayı Ajanlar sayfasındaki bağlantılardan (`/kurulum/appmon-agent-linux.conf`, `/kurulum/appmon-agent-windows.conf`; sunucu adresi gömülü gelir), RHEL/güncelleme paketindeki `agent-config/` klasöründen veya `appmon-agent -print-config linux|windows` komutundan alabilirsiniz. `APM_KEY` satırına ajan anahtarını yazıp `sudo ./appmon-agent -config appmon-agent.conf -install` (Windows: `appmon-agent.exe -config agent.conf -install`) ile kurun; dosya yerine yazılır ve `SEND_*` seçimleriniz korunur.

| Satır | Gönderilen veri |
|---|---|
| `SEND_HOST_CPU` / `_MEM` / `_DISK` / `_LOAD` / `_NET` / `_PROCS` / `_UPTIME` | Makine CPU, bellek+swap, disk, yük ortalaması, ağ trafiği, süreç sayısı, açık kalma süresi |
| `SEND_WATCH` | `APM_WATCH` süreçlerinin var/yok ve adet metrikleri |
| `SEND_LOGCOUNT` | `APM_LOG` günlük desenlerinin eşleşme sayıları |
| `SEND_APPS` | Uygulama keşfi (süreç/servis/port listesi, uygulama CPU/bellek, otomatik günlük sayımı) |
| `SEND_CONNS` | Giden TCP bağlantı özeti (servis haritası; `SEND_APPS` ile birlikte) |
| `SEND_WEBLOG` | Web sunucusu erişim günlüğü özeti |
| `SEND_OTLP` | Yerel OpenTelemetry alıcısı (`127.0.0.1:4318`) |
| `SEND_UPDATE` | Uzaktan ajan güncellemesi |

`agent.up` kalp atışı (asset'in çevrimiçi olduğunu gösterir) her zaman gönderilir. Ajan veriyi sunucuya **gzip ile sıkıştırarak** yollar; eski sunucu kabul etmezse otomatik düz JSON'a döner.

Windows'ta ajan SYSTEM olarak çalışan "APP_MON Agent" zamanlanmış görevi, Linux'ta kısıtlı bir systemd servisi (`appmon-agent`) olur. Ayarlar anahtar içerdiği için yalnızca SYSTEM/root'un okuyabildiği dosyada tutulur.

- **Çevrimdışı tampon:** sunucuya ulaşılamazsa ölçümler bellekte tutulur (yaklaşık son 2–3 saat).
- **Kaldırma:** Windows `appmon-agent.exe -uninstall`, Linux `sudo appmon-agent -uninstall`.
- **Kurumsal CA:** HTTPS sunucu için ajana `-ca ca.pem` verilir.
- Ajan dosyaları sunucu paketiyle gelir ve `/agent/dl/…` adresinden indirilir.
- **Uygulama izleme (OTLP):** .NET, Java, Python ve Go uygulamaları OpenTelemetry SDK ile metriği doğrudan sunucuya yollar; sunucuya bir şey kurulmaz.

### Ajanları uzaktan güncelleme

*Araçlar › Ajanlar › Ajanla izlenen makineler.* Sunucu güncellendiğinde yeni ajan dosyaları da sunucuyla gelir (`/agent/dl/…`). Her ajan kendi SHA-256 özetini bildirir; sunucudaki dosyadan farklıysa satırda **Yeni sürüm var** görünür.

- **Güncelle:** satırdaki düğme o ajan için istek açar; **Tümünü güncelle** tüm uygun ajanlar için açar. Ajan en geç bir sonraki gönderiminde (varsayılan 15 sn) yeni dosyayı bu sunucudan indirir, SHA-256'sını ve çalıştığını doğrular, kendini değiştirir ve yeniden başlar. Hata olursa eski ajan çalışmaya devam eder, satırda hata görünür ve 10 dk (Linux'ta 30 dk) sonra yeniden denenir. **Vazgeç** bekleyen isteği geri alır.
- **Otomatik güncelle:** işaretlenirse sunucudaki ajan dosyası değiştiğinde ajanlar istek beklemeden güncellenir (varsayılan kapalı).
- **Linux:** ajan yetkisiz çalıştığından yalnızca bir istek dosyası bırakıp çıkar; systemd'nin root olarak çalıştırdığı `ExecStartPre=+ … -apply-update` adımı dosyayı yapılandırılmış sunucudan kendisi indirir, SHA-256'yı denetler ve ikiliyi değiştirir (ajan hesabı ele geçirilse bile root'a kod yazdıramaz). Yalnızca systemd servisi olarak kurulu ajanlar güncellenir.
- **Windows:** ajan SYSTEM olarak yeni dosyayı indirir, çalışan dosyayı yeniden adlandırıp yenisini yerine koyar ve çıkar; birkaç saniye sonra zamanlanmış görevi kendisi yeniden başlatır (ajan 1.1.1 ve sonrası; eski kurulumlarda görevin "başarısızlıkta yeniden başlat" ayarı olmasa da çalışır). Eski dosya `appmon-agent.old.exe` yeni ajan açılırken silinir (ajan 1.1.1'den itibaren). Ajan 1.1.0 ile güncellenen bir Windows makinede ajan geri gelmezse görevi bir kez elle başlatın: `Start-ScheduledTask -TaskName 'APP_MON Agent'`.
- **Kapatma:** ajanı `-no-update` (veya `APM_UPDATE=off`) ile kurun; bu ajanlar sunucu teklif etse de güncellenmez ve listede "desteklenmiyor" görünür.
- **İlk geçiş:** v2.7.5'ten önceki ajanlar uzaktan güncellemeyi bilmez; bir kez elle yeniden kurulmaları (`-install`) gerekir. Yeniden kurulum systemd birimini de yeniler. Sonraki sürümlerde uzaktan güncellenir.
- **Ajan sürümü** sunucu sürümünden bağımsızdır (ANA.ARA.YAMA; yeni yetenek = ARA, düzeltme = YAMA, uyumsuz protokol = ANA) ve yalnızca ajan kodu değişince artar; sunucu sürümü değiştiği halde ajan değişmediyse ajan dosyası aynı kalır ve "Yeni sürüm var" çıkmaz. Sunucudaki ajan sürümü Ajanlar sayfasında yazar. `1.0.0` = v2.7.5 ajanı, `1.1.0` = web günlüğü okuma.
- Denetim günlüğüne `agent.update_request`, `agent.update_cancel` ve `agent.auto_update` işlenir.

### Web sunucusu erişim günlüğü (nginx, Apache, IIS)

Ajan kurulu makinede web sunucusunun erişim günlüğünü kendiliğinden bulur ve okur; uç nokta bazında istek sayısı, 4xx/5xx ve gecikme özetini **Performans → Uygulamalar** sayfasına gönderir. Satırın kendisi, IP adresi ve sorgu dizesi gönderilmez; yollar normalleştirilir (`/siparis/123` → `/siparis/{id}`). Uygulama adı: ajanın kendi günlüğünde sanal sunucu adı, IIS'te site adı, nginx/Apache varsayılan günlüğünde `<asset>-nginx` / `<asset>-apache`. Her ajanın "Web günlüğü" durumu Araçlar › Ajanlar tablosunda görünür.

- **IIS (Windows):** `C:\inetpub\logs\LogFiles\W3SVC*` günlükleri okunur; W3C günlüğü `time-taken` (ms) alanını zaten içerdiğinden ek ayar gerekmez.
- **nginx / Apache (Linux):** varsayılan "combined" günlükte istek süresi yoktur; yalnızca istek sayısı ve hata oranı gelir, uygulama satırında "süre yok" yazar. p50/p95/p99 için süreyi de yazan ikinci günlük eklenir:
  - `sudo appmon-agent -web-setup -dry-run` ne yapacağını gösterir, hiçbir şeyi değiştirmez.
  - `sudo appmon-agent -web-setup` risk uyarısını gösterir, onay ister, `appmon-access.conf` dosyasını ekler (nginx `conf.d/`, RHEL Apache `conf.d/`, Debian Apache `conf-available/` + `a2enconf`), ayarı sınar ve yeniden yükler. `-yes` onay sormaz.
  - `sudo appmon-agent -web-undo` ekleneni kaldırır.
- **Riskler:** (1) üretimdeki web sunucusunun ayarına dosya eklenir ve ayar yeniden yüklenir; yeniden yüklemeden önce `nginx -t` / `httpd -t` ile sınanır, hata çıkarsa dosya silinir ve hiçbir şey değişmez — yine de önce üretim dışında deneyin. (2) Sanal sunucuda kendi `access_log` / `CustomLog` satırı varsa o sunucu otomatik kapsanmaz; komut bu satırları listeler ve eklenecek satırı yazar. Mevcut hiçbir ayar dosyası değiştirilmez. (3) Her istek için ikinci bir günlük satırı yazılır (ek disk/G/Ç); `/var/log/appmon/access.log` günlük döndürülür, 3 gün tutulur. (4) Yerel günlükte tam adres (sorgu dizesi dahil) saklanır. (5) SELinux açıksa dizine `httpd_log_t` etiketi verilir. Root gerekir.
- **Kapatma:** ajanı `-no-weblog` (veya `APM_WEBLOG=off`) ile kurun. Günlük dosyası yetki nedeniyle okunamıyorsa Web günlüğü sütununda "okunamıyor" yazar; ajanı `-root` ile kurun.
- Yeni uygulama adları lisans sayısına dahildir (en çok 50 uygulama/kiracı, uygulama başına 300 uç nokta).

### Log izleme

Ajan komutuna `-log "ad::dosya::regex"` eklenir (birden çok için `;;`). Her turda dosyaya eklenen yeni satırlar sayılır: `log.<ad>.count` (eşleşen), `log.<ad>.lines`, `log.<ad>.readable`. Başlangıçta geçmiş satırlar sayılmaz; log dönmesi/kısalması ve yarım satır doğru ele alınır. Örnek kural: `log.nginx_hata.count > 10`. Satır içeriği sunucuya gönderilmez, yalnızca sayılar gider; en çok 20 log. Linux'ta log dosyaları genelde root'a ait olduğundan log izlemeli kurulumda ajan root olarak çalışır.

### Uygulama keşfi (ajan kurulunca otomatik)

Ajan kurulduğunda ayrıca bir şey yapmanız gerekmez; her dakika makinedeki uygulamaları bulup sunucuya bildirir. Assetler → asset'in **Ayrıntı** sayfasında **Uygulamalar (ajan)** bölümü görünür.

- **Süreçler:** aynı adlı süreçler tek satırda toplanır (adet, CPU = bir çekirdeğin yüzdesi, bellek MB, dinlediği portlar). En yoğun 10 uygulama için `proc.<ad>.cpu_pct` ve `proc.<ad>.mem_mb` metriği üretilir; alarm kuralında kullanılabilir.
- **Servisler:** Linux'ta systemd, Windows'ta Windows servisleri ve durumları listelenir.
- **Günlük sayıları:** nginx, httpd/apache, postgresql, mysql/mariadb, redis, mongodb, sshd ve sistem günlüğü bulunursa son 1 saatteki hata ve uyarı sayısı gösterilir (`log.auto_<ad>.count` hata, `.warn` uyarı, `.readable`). Günlük içeriği gönderilmez; yalnızca sayılar ve parola/anahtar maskelenmiş, 200 karaktere kısaltılmış **son hata örneği** gider.
- **Yetki:** Linux'ta ajan varsayılan olarak yetkisiz kullanıcıyla çalışır; root'a ait günlükler "okunamıyor" görünür ve başka kullanıcıların süreç portları eksik kalabilir. Tam görünüm için ajanı `-root` ile kurun.
- **Kapatma:** `-no-apps` (veya `APM_APPS=off`). `APM_ROOT=true` ajanı root çalıştırır. Windows'ta otomatik günlük okuma yoktur; `-log` ile dosya izleme kullanılır.
- Bu özelliği almak için mevcut ajanları v2.7.3 ajanıyla yeniden kurun.

## Kullanıcılar ve giriş (yönetici)

- **Giriş yöntemleri:** API anahtarı veya "Kullanıcı adı ile giriş". Hesap oluştururken kullanıcı adı, en az 10 karakterli parola ve rol (izleyici/işletmeci/yönetici) verilir. Kullanıcılar kendi parolasını *Ayarlar › Hesabım*'dan değiştirir.
- **Güvenlik:** parolalar PBKDF2-SHA256 (600.000 tur, rastgele tuz) ile saklanır. Giriş sonrası 12 saatlik oturum anahtarı verilir; çıkışta sunucudan silinir. 5 hatalı denemede hesap 5 dakika kilitlenir (yönetici **Kilidi aç** diyebilir); aynı IP'den 10 dakikada 20 hatalı deneme engellenir. Rol değişince, parola belirlenince veya hesap devre dışı bırakılınca kullanıcının açık oturumları kapanır. Giriş denemeleri denetim günlüğüne yazılır.
- **API anahtarları** aynen çalışır; ajanlar ve otomasyonlar anahtar kullanmaya devam eder. Oturum anahtarları "Lisans ve anahtarlar" listesinde görünmez.

### LDAP / Active Directory girişi

Sunucu ortam değişkenleri:

| Değişken | Açıklama |
|---|---|
| `APM_LDAP_URL` | `ldaps://dc.sirket.local:636` veya `ldap://` ile StartTLS |
| `APM_LDAP_DN_TEMPLATE` | Kullanıcı adı `%s` yerine geçer: AD için `%s@sirket.local` ya da `SIRKET\%s`; OpenLDAP için `uid=%s,ou=people,dc=sirket,dc=local` |
| `APM_LDAP_DEFAULT_ROLE` | LDAP kullanıcısının varsayılan rolü (varsayılan `viewer`) |

Kurum CA'sı `APM_CA_FILE` ile tanıtılır. Dizin sunucusu parolayı doğrular (bind); roller APP_MON'da tutulur: LDAP kullanıcısı ilk girişte varsayılan rolle otomatik eklenir, rolü Kullanıcılar sayfasından değiştirilir. Aynı adda yerel hesap varsa o kullanılır. Boş parola ve DN'ye enjeksiyon riskli karakterler reddedilir. **Sınırlar:** grup üyeliğinden rol eşleme ve dizin araması yoktur (yalnızca doğrudan bind); düz metin `ldap://` desteklenmez (TLS zorunlu); gerçek bir AD/OpenLDAP'a karşı henüz denenmedi, sahte bir LDAPS sunucusuyla doğrulandı.

## Lisans ve anahtarlar

- **Lisans:** on-premise kurulumda her benzersiz izlenen asset (sunucu, servis, veritabanı, URL) bir lisans birimi kullanır; limit dolunca yeni asset kabul edilmez, mevcutlar izlenmeye devam eder. Asset limiti ve geçerlilik tarihi bu sayfadan (yönetici) değiştirilir; imzalı lisans yüklüyse limitler dosyadan okunur ve elle değiştirilemez.
- **API anahtarları:** oluştururken rol seçilir; anahtar yalnızca oluşturulurken bir kez gösterilir.

### İmzalı lisans dosyası (.lic)

Kiracı kotaları ana sunucuda üretilen imzalı bir **.lic** dosyasıyla verilir.

1. **Üretim (platform yöneticisi):** Kiracılar › ilgili kiracı › **Lisans üret**. Müşteri adı, limitler, özellik izinleri ve (isteğe bağlı) geçerlilik tarihi girilir; `appmon-<kiracı>-<tarih>.lic` indirilir. Dosya sunucuda saklanmaz; Ed25519 imza anahtarı ana sunucuda kasa ana anahtarıyla mühürlü tutulur. Kasa etkin olmalıdır.
2. **İletim:** Dosyayı kiracı yöneticisine iletin.
3. **Yükleme (kiracı yöneticisi):** Yönetim › Lisans ve anahtarlar › **Lisans dosyasını yükle**. Dosya yalnızca adı geçen kiracıya yüklenir; imzası geçersiz, değiştirilmiş, süresi dolmuş veya daha eski tarihli dosya reddedilir.
4. **Etki:** Limitler dosyadan okunur; elle değiştirilemez (yeni kota için yeni .lic üretin). Yüklenmemiş kiracı mevcut elle girilmiş kotalarla çalışmaya devam eder.
5. **Ölçüler:** Asıl lisans ölçüsü **asset sayısıdır**. Probe, kullanıcı sayısı, en kısa tarama aralığı ve saklama süresi koruyucu kotalardır; tarayıcı senaryoları, Keşfet ve SSH izleme ayrı izin bayraklarıdır.
6. **Denetim:** `license.issue` ve `license.install` kayıtları yazılır.

> **Dikkat:** Kasa ana anahtarı kaybolursa imza anahtarı çözülemez ve yeni lisans üretilemez/doğrulanamaz; anahtarı yedekleyin.

## Kasa (yönetici)

Kasa; SSH anahtarı, parola ve token gibi gizli değerleri **AES-256-GCM** ile şifreli saklar. Ana anahtar veritabanında değil, sunucuda ayrı bir dosyada/ortam değişkenindedir; yalnızca veritabanı yedeği ele geçirilse bile değerler okunamaz. Değerler arayüzden **bir daha gösterilmez**; probe'lar kasadan kayıt seçer. Ekleme/değiştirme/silme yalnızca yöneticiye aittir ve denetim günlüğüne yazılır (silmek parola/API anahtarı ister; probe tarafından kullanılan kayıt silinemez).

**Ana anahtar nereden alınır** (sırayla): `APM_MASTER_KEY` ortam değişkeni → `APM_MASTER_KEY_FILE` dosyası → `/etc/appmon/master.key` → veri dizinindeki `master.key` (`APM_DATA_DIR`; Docker'da `/data`). Hiçbiri yoksa ve veri dizini yazılabiliyorsa ilk açılışta otomatik üretilir. Anahtar 32 bayttır: base64 veya 64 karakter hex (`openssl rand -base64 32`).

> **Dikkat:** Anahtar dosyasını yedekleyin. Kaybolursa kasadaki değerler geri alınamaz (probe'lar "SSH anahtarı alınamadı" hatası verir; değerleri yeniden girmek gerekir).

Kasaya taşınmayanlar: SNMP topluluk adları, PostgreSQL parolaları, senaryo değişkenleri, LDAP/SIEM/bildirim sırları mevcut yerlerinde (veritabanında) durur. SSH probe'ları yedek dışa aktarmaya girmez (anahtar kasada tutulur; içe aktarmada elle yeniden eklenir).

## Denetim günlüğü (yönetici)

Kim neyi, ne zaman değiştirdi kaydı. Tablo başlıklarının altındaki süzgeç satırı:

- **Zaman:** başlangıç/bitiş tarihi. **Kullanıcı** ve **İşlem:** açılır liste (günlükteki mevcut değerler). **Ayrıntı:** metin arama (ayrıntıda ve işlem adında, büyük/küçük harf duyarsız).
- Süzgeçler birlikte uygulanır ve sunucuda çalışır; "Süzgeçleri temizle" hepsini sıfırlar. Liste 50'şerli sayfalarla gösterilir.
- API: `GET /api/audit?actor=&action=&q=&from=&to=&limit=` ve süzgeç değerleri için `GET /api/audit/facets`.

Başlıca işlem adları: `asset.create`, `probe.*`, `monitoring.pause/resume`, `license.issue/install`, `system.update.*`, `tenant.*`, `user.*`, `login`, `login.fail`. Denetim kayıtları SIEM'e de iletilebilir.

## Ayarlar

- **Görünüm:** tema, palet, vurgu rengi, sayfa düzeni (bkz. *Görünüm tercihleri*).
- **Hesabım:** kendi parolanızı değiştirin.
- **Sistem:** sürüm, mod, saklama süresi. Sunucu davranışı (mod, saklama, tarayıcı) ortam değişkenleriyle (`.env`) ayarlanır; lisans ve anahtarlar "Lisans ve anahtarlar" sayfasındadır.
- **Yapılandırma yedeği (yönetici):** asset, probe, alarm kuralı ve bakım pencerelerini JSON olarak indirir/yükler. Parolalar, SNMP toplulukları, HTTP başlıkları, bildirim/SIEM kanalları ve senaryolar dahil değildir. İçe aktarma yalnızca eksikleri ekler (var olanı değiştirmez/silmez); gizli değeri olan probe'lar KAPALI eklenir, SNMPv3 ve parolalı PostgreSQL hedefleri atlanıp raporda listelenir. Metrik geçmişi dahil her şey için veritabanı yedeği (`pg_dump`) alın.
- **Herkese açık durum sayfası:** asset'leri işaretleyin (isteğe bağlı görünen ad verin); `https://sunucu/status` adresi giriş gerektirmeden anlık durumu ve son 90 gün erişilebilirliğini gösterir (30 sn önbellek). Yalnızca işaretli asset'lerin adları ve durumları görünür; adres/hata ayrıntısı gösterilmez. Başlık: `APM_STATUS_TITLE`. Sayfayı internete açmadan önce görünecek adları kontrol edin.

## Kiracılar (platform yöneticisi)

Tek kurulumda birden çok kurum (kiracı) ayrı ayrı çalışabilir; en fazla 10 (`APM_MAX_TENANTS`).

- **Ana kiracı:** Yükseltme öncesi tüm veriniz "default" adlı ana kiracıdadır. Ana kiracının yöneticisi **platform yöneticisidir**: **Kiracılar** ve **Güncelleme** sayfalarını yalnızca o görür; lisansı yalnızca o değiştirir.
- **Kiracı oluşturma:** Kiracılar › Yeni kiracı. Ad, (isteğe bağlı) durum sayfası adresi, ilk yönetici kullanıcı adı/parolası ve kotalar girilir.
- **Kota (kiracı başına):** asset limiti, probe limiti (0 = asset limiti kadar), kullanıcı limiti, en kısa tarama aralığı (sn), saklama süresi (gün; 0 = sunucu varsayılanı), tarayıcı senaryosu / Keşfet / SSH izleme izinleri, geçerlilik tarihi. Aşıldığında işlem reddedilir. Kiracı yöneticisi Lisans sayfasında kotalarını salt-okunur görür.
- **İzolasyon:** Her kiracı yalnızca kendi asset, probe, senaryo, alarm, kullanıcı, API anahtarı, kasa, bildirim ve denetim kayıtlarını görür; başka kiracının kimliğiyle yapılan istekler reddedilir (otomatik test: `TestTenantIsolation`).
- **Kullanıcı adları** tüm kiracılarda benzersizdir.
- **Durdurma / silme:** Durdurulan kiracının oturumları kapanır, girişi ve izlemesi durur; veri silinmez. Silme için kiracı adı + parola/anahtar gerekir ve tüm veri kalıcı olarak gider.
- **Durum sayfası:** Her kiracının sayfası `/status/<adres>` altındadır; ana kiracı `/status` ve `/status/default` ile devam eder.
- **Yönetici ekleme:** Kiracının yöneticisi parolasını kaybettiyse Kiracılar › Yönetici ekle.
- **Bilinen sınırlar:** LDAP ile ilk kez giriş yapan kullanıcılar ana kiracıya düşer; ağ keşfi kiracı yöneticisi yetkisindedir; kasa ana anahtarı ortaktır (kayıtlar kiracıya bağlı şifrelenir); tarayıcı/Keşfet eşzamanlılık sınırları sunucu genelindedir.

## Kurulum, güncelleme ve yedekleme

### Docker (Windows)

Proje klasöründe PowerShell ile:

| Komut | İş |
|---|---|
| `.\baslat-docker.bat` | İlk kurulum ve başlatma |
| `.\guncelle-docker.bat` | Güncelleme (veriyi silmez; veritabanı değişiklikleri açılışta otomatik ve yıkıcı olmayan şekilde uygulanır) |
| `.\durdur-docker.bat` | Durdurma |

> **Dikkat:** Güncelleme dosyalarını klasöre açarken `.env` dosyanızı **ezmeyin**. `.env` içindeki veritabanı parolası değişirse "password authentication failed" hatası alırsınız.

### RHEL / Linux (çevrimdışı)

- **İlk kurulum:** paketteki `kur.sh` veya `kur-apache.sh`. Mevcut kurulumda ayar dosyası, veritabanı ve anahtarlar korunur; yalnızca ikili dosya değişir.
- **Arayüzden güncelleme hizmetini bir kez kurma:** `tar xzf appmon-guncelleme-vX.tar.gz; cd appmon-guncelleme; sha256sum -c SHA256SUMS; sudo ./guncelle-apache.sh`. Sonraki güncellemeler arayüzden yapılır.
- **Yedek:** güncellemeden önce `pg_dump appmon > appmon-yedek.sql`.

### Güncelleme sayfası (platform yöneticisi)

1. **Güncelleme** sayfasını açın, `appmon-guncelleme-vX.Y.Z.tar.gz` paketini seçip **Yükle ve doğrula**'ya basın (paket açılmadan, olduğu gibi yüklenir). Sürüm, sağlama toplamları ve (açıksa) imza doğrulanır.
2. **Güncellemeyi uygula:** veritabanı yedeği alınır, yeni sürüm yüklenir, servis yeniden başlar ve sayfa yenilenir.
3. Yeni sürüm açılmazsa eski sürüme **otomatik** dönülür ("Geri alındı"). Daha eski sürüme geçilemez.

**Önceki sürüme dön:** Güncelleme sonrası yeni sürüm açılıyor ama kararsız çalışıyorsa **Önceki sürüme dön (vX.Y.Z)** düğmesiyle tek tıkla önceki uygulama dosyasına dönülür.

- Düğme bir güncelleme uygulandıktan sonra görünür; dönüşten sonra kaybolur.
- Yalnızca uygulama dosyası değişir; **veritabanı olduğu gibi kalır**. Migration'lar yalnızca ekleyici olduğundan eski sürüm mevcut veriyle çalışır. Gerekirse güncelleme öncesi alınan yedek elle geri yüklenebilir.
- Komut satırı: `sudo appmon-guncelle --geri-al`. İşlem denetim kaydına `system.update.rollback` olarak yazılır.
- Dönüş yapılacak sunucuda önce v2.0.1 veya sonrası betiğin kurulu olması gerekir.
- Docker kurulumunda arayüzden güncelleme/dönüş yoktur; önceki sürümün dosyalarıyla `guncelle-docker.bat` çalıştırın.

### Paket imzası (önerilir)

Paketleme makinesinde `./paketle-guncelleme.sh --anahtar-uret` ile Ed25519 anahtar çifti üretin (özel anahtar gizli kalır). Sunucuya `sudo ./guncelle-apache.sh --imza-anahtari genel-anahtar.pem` ile genel anahtarı tanıtın ve paketleri `./paketle-guncelleme.sh vX.Y.Z --imzala ozel-anahtar.pem` ile imzalayın. İmza açıkken imzasız/değiştirilmiş paketler reddedilir.

## Ortam değişkenleri başvurusu

Docker'da `.env` dosyasına, RHEL'de `/etc/appmon/appmon.env` dosyasına yazılır. Değişiklikten sonra servisi/konteyneri yeniden başlatın.

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `APM_ADMIN_KEY` | — | Yönetici API anahtarı (ilk giriş) |
| `APM_MODE` | `onprem` | `onprem` veya `saas` (saas'ta özel ağ hedefleri, keşif ve SSH kapalı) |
| `APM_DB_DSN` | yerel PostgreSQL | Veritabanı bağlantı dizesi |
| `APM_ADDR` / `APM_PORT` | `:8080` / `8080` | Dinleme adresi / Docker'daki dış port |
| `APM_ASSET_LIMIT` | `25` | Varsayılan asset limiti (imzalı lisans yoksa) |
| `APM_RETENTION_DAYS` | `14` | Ham ölçüm saklama süresi (gün) |
| `APM_SELF_TRACE` | `1` | `0` ise APP_MON kendi API isteklerini trace olarak kaydetmez |
| `APM_SELF_TRACE_SAMPLE` | `0.2` | Hızlı ve başarılı isteklerin saklanma oranı (0-1); hata ve yavaş istekler her zaman saklanır |
| `APM_PUBLIC_URL` | — | Bildirim bağlantılarının adresi |
| `APM_BROWSER_ENABLED` | `true` | Tarayıcı senaryoları ve Keşfet için Chromium |
| `APM_PROBE_CONCURRENCY` | `50` | Aynı anda çalışan probe sayısı (1000 probe için 50–100 önerilir) |
| `APM_SCENARIO_CONCURRENCY` | `10` | Aynı anda çalışan HTTP senaryosu sayısı |
| `APM_BROWSER_CONCURRENCY` | `2` | Aynı anda oynatılan tarayıcı senaryosu sayısı (her biri ~300 MB bellek; 1–16) |
| `APM_INGEST_CONCURRENCY` | `16` | Aynı anda işlenen ajan gönderimi; dolunca ajana 503 + Retry-After döner |
| `APM_DB_MAX_CONNS` | `40` | Veritabanı bağlantı havuzu (PostgreSQL `max_connections` değerini aşmayın) |
| `APM_INGEST_RATE` | `2` | Bir asset'in saniyedeki en çok ingest isteği (patlama 20); aşılırsa 429 + Retry-After. `-1` = sınırsız |
| `APM_NOTIFY_GROUP_SEC` | `20` | Aynı kuraldan bu süre içinde gelen alarm bildirimleri tek mesajda toplanır (0 = kapalı) |
| `APM_OFFLINE_AGENT_SEC` | `90` | Ajan bu süre sessiz kalırsa çevrimdışı sayılır |
| `APM_OFFLINE_WARMUP_SEC` | `120` | Sunucu yeniden başlayınca bu süre boyunca ajanlar çevrimdışı işaretlenmez |
| `APM_NETFLOW_ADDR` | *(kapalı)* | NetFlow/IPFIX UDP dinleme adresi (ör. `:2055`) |
| `APM_NETFLOW_ALLOW` | *(hepsi)* | Kabul edilen ihracatçı IP/CIDR listesi (virgülle) |
| `APM_NETFLOW_BUCKET_SEC` | `300` | Akış özet aralığı (sn, en az 60) |
| `APM_NETFLOW_TOPN` | `500` | Aralık ve ihracatçı başına saklanan konuşma sayısı |
| `APM_NETFLOW_RETENTION_DAYS` | `7` | Akış özetlerinin saklama süresi |
| `APM_NETFLOW_SAMPLING` | `1` | Cihaz örnekleme bildirmiyorsa çarpan |
| `APM_LOG_EVENTS` | *(kapalı)* | `1` ise OTLP/JSON günlük satırları saklanır (log–trace ilişkilendirme) |
| `APM_LOG_MIN_LEVEL` | `warn` | Saklanan en düşük seviye (`debug`, `info`, `warn`, `error`, `fatal`) |
| `APM_LOG_RETENTION_DAYS` | `3` | Günlük satırlarının saklama süresi (gün) |
| `APM_LOG_RATE` | `200` | Kiracı başına saniyede kabul edilen günlük satırı (fazlası atılır; <0 sınırsız) |
| `APM_CHROME_PATH`, `APM_CHROME_NO_SANDBOX` | — | Chromium yolu / sandbox'sız çalıştırma |
| `APM_BROWSER_STRICT_TLS` | `false` | Tarayıcı senaryolarında sertifika hatalarını reddet |
| `APM_BLOCK_PRIVATE_PROBES` | moda bağlı | Özel ağ hedeflerini engelle |
| `APM_CA_FILE` | — | Kurum CA sertifikası (PEM) |
| `APM_MASTER_KEY`, `APM_MASTER_KEY_FILE`, `APM_DATA_DIR` | — | Kasa ana anahtarı kaynakları |
| `APM_SSH_BIN` | `ssh` | OpenSSH istemcisi yolu |
| `APM_SNMP_TRAP_ADDR`, `APM_SNMP_TRAP_COMMUNITY` | kapalı | SNMP trap alıcısı |
| `APM_LDAP_URL`, `APM_LDAP_DN_TEMPLATE`, `APM_LDAP_DEFAULT_ROLE` | kapalı | LDAP girişi |
| `APM_SYSLOG_ADDR`, `APM_SYSLOG_ALLOW`, `APM_SYSLOG_RATE` | kapalı / tümü / `2000` | Erişim günlüğü (syslog) alıcısı: adres, izinli CIDR listesi, kiracı başına satır/sn |
| `APM_STATUS_TITLE` | — | Herkese açık durum sayfası başlığı |
| `APM_MAX_TENANTS` | `10` | En çok kiracı sayısı |
| `APM_UPDATE_DIR`, `APM_UPDATE_PUBKEY` | — | Arayüzden güncelleme klasörü / paket imza anahtarı |

## Metrik başvurusu

| Metrik | Kaynak | Anlam |
|---|---|---|
| `asset.down`, `asset.degraded` | Probe/senaryo | Asset erişilemiyor / kısmen erişilemiyor (1/0) |
| `host.cpu_pct`, `host.mem_pct` | Ajan, SNMP, SSH | CPU ve bellek yüzdesi |
| `host.disk_pct`, `host.disk_pct.<ad>` | Ajan, SNMP, SSH | Disk doluluk (en dolu / her bağlama noktası) |
| `host.uptime_sec`, `host.net_rx_bps`, `host.net_tx_bps` | Ajan | Açılış süresi, ağ trafiği |
| `proc.<ad>.up`, `proc.<ad>.count` | Ajan (`-watch`) | İzlenen sürecin çalışma durumu ve sayısı |
| `log.<ad>.count/lines/readable` | Ajan (`-log`) | Log eşleşme sayısı, satır sayısı, okunabilirlik |
| `log.auto_<ad>.count/warn/readable` | Ajan (otomatik) | Bilinen uygulama günlüğünde hata / uyarı satırı sayısı |
| `proc.<ad>.cpu_pct/mem_mb` | Ajan (otomatik) | En yoğun 10 uygulamanın CPU (% çekirdek) ve belleği |
| `net.<ad>.in_bps/out_bps/util_pct/up` | SNMP gelişmiş | Arayüz trafiği ve doluluğu |
| `snmp.<ad>`, `snmp.trap`, `snmp.ups_*` | SNMP | Özel OID, trap, UPS değerleri |
| `db.connections_pct`, `db.cache_hit_pct`, `db.deadlocks`, `db.size_mb`, `db.repl_lag_s` … | PostgreSQL | Veritabanı sağlığı |
| `net.path.hops/rtt_ms/loss_pct/changed` | Yol izleme | Rota ölçümleri |
| `app.req_per_min/err_pct/avg_ms/p50_ms/p95_ms/p99_ms` | Erişim günlüğü | Uygulama istek hızı, hata oranı, süre yüzdelikleri |
| `db.slow_queries`, `db.top_query_mean_ms` | pgstats | Yavaş sorgu sayısı, en yavaş ortalama |
| `host.conns`, `host.conn_rtt_ms` | SSH | Kurulu bağlantı sayısı, ortalama TCP RTT |
| `probe.http_<aşama>_ms` | HTTP probe | DNS/TCP/TLS/ilk bayt/indirme süreleri |
| `surface.drift` | Saldırı yüzeyi | Gözden geçirilmemiş uyarı/kritik sapma sayısı |
| `inventory.eol_count`, `inventory.vuln_critical`, `inventory.vuln_high` | Envanter | Destek dışı sürüm sayısı; kritik/yüksek zafiyet sayısı |
| `anomaly.latency_z/cpu_z/mem_z` | Anomali | Güçlü z-skoru |

## Sorun giderme ve SSS

**Yeni özellik görünmüyor.** Docker'da `.\guncelle-docker.bat` çalıştırın, ardından tarayıcıda Ctrl+F5 yapın (eski dosyalar önbellekte kalabilir). Üst çubuktaki sürüm numarasını kontrol edin.

**Docker'da "password authentication failed for user appmon".** `.env` dosyası güncelleme sırasında değiştirilmiş olabilir; veritabanının oluşturulduğu parolayla `.env`'deki `POSTGRES_PASSWORD` aynı olmalıdır. Eski `.env` dosyanızı geri koyup `.\guncelle-docker.bat` çalıştırın.

**SNMP "zaman aşımı".** Servis çalışıyor mu, topluluk adı doğru mu, izinli yönetici listesinde APP_MON var mı, UDP 161 güvenlik duvarında açık mı kontrol edin. "SNMP yanıtı yok" mesajı adres, port, kimlik bilgisi veya erişim izninden biri yanlış demektir.

**HTTPS probe'unda "x509: certificate signed by unknown authority".** Bkz. *HTTP/HTTPS ve kurum sertifikaları*.

**Bildirim gitmiyor.** *Test gönder*'i deneyin; kanalın son hatasına bakın; bakım penceresi aktif olabilir; kanalın "en düşük önem" ayarı alarmın önemini geçiyor olabilir; izleme duraklatılmış olabilir.

**SIEM'e olay düşmüyor.** Hedefte *Deneme gönder*'i deneyin; port, protokol ve güvenlik duvarını kontrol edin; UDP'de hata görünmeyebilir, TCP kullanın.

**Alarm hiç açılmıyor.** İzleme duraklatılmış mı (sarı şerit)? Kural kapsamı doğru mu? Pencere ve eşik makul mü (kural pencere **ortalamasını** karşılaştırır)? Asset bakım penceresinde mi?

**Alarm kapanmıyor.** Koşul hâlâ sürüyor olabilir; zildeki **Alarmları temizle** koşul sürdükçe alarmı yeniden açtırır.

**Genel bakışta makinem görünmüyor.** Assetler › makine › **Genel bakışta göster** kutusunu işaretleyin; makinede CPU/RAM/Disk ölçümü gelmeli.

**Senaryo kartı hep kırmızı/sarı.** Karttaki durum rozetine tıklayıp hata sebebini ve başarısız adımı okuyun; sayfa her seferinde değişiyorsa *Yoksayma kuralları*nı kullanın.

**Docker'da MAPI.** Desteklenmez; SMTP/Graph/EWS kullanın.

**Kasa "SSH anahtarı alınamadı".** Ana anahtar değişmiş veya kaybolmuş olabilir; `master.key` yedeğini geri koyun ya da kasa kayıtlarını yeniden girin.

**Lisans dosyası reddedildi.** Dosyanın o kiracı için üretildiğini, süresinin dolmadığını ve daha eski tarihli olmadığını doğrulayın.

**İzleme neden beklediğimden yavaş?** Lisans/kota "en kısa tarama aralığı" probe aralığını sınırlıyor olabilir.

## Güvenlik notları

- Gizli değerler (parola, SSH anahtarı, token) arayüzde bir daha gösterilmez; kasa AES-256-GCM kullanır.
- Hassas işlemler (izlemeyi duraklatma, yoksayma kuralı ekleme, kasa kaydı silme, kiracı silme) parola veya API anahtarı yeniden girişi ister ve denetim kaydına yazılır.
- Ağ keşfi, yol izleme ve Keşfet yalnızca yetkili olduğunuz hedeflerde kullanılmalıdır; Keşfet robots.txt'e uyar ve beyan ister.
- Durum sayfasını internete açmadan önce görünecek asset adlarını gözden geçirin.
- Güncelleme paketlerini imzalayın; Docker'da `.env` dosyasını sürüm kontrolüne koymayın ve paylaşmayın.
- Veritabanı yedeği (`pg_dump`) ve kasa ana anahtarı yedeği düzenli alınmalıdır.

## Sürüm geçmişi (özet)

| Sürüm | Yenilik |
|---|---|
| v1.7 | Kasa, SSH ile sunucu kaynakları, halka grafikler, yeni paletler |
| v1.8 | Keşfet, sayfalama, açılır menü grupları, Genel bakışta makine seçimi |
| v2.0.0 | Çok kiracılı yapı, platform yöneticisi, kiracı kotaları |
| v2.0.1 | Önceki sürüme dönüş |
| v2.1.0 | İmzalı lisans dosyası (.lic) |
| v2.2.0 | İzlemeyi duraklat / başlat |
| v2.2.1 | İngilizce dil seçeneği |
| v2.2.2 | Yenilenen kılavuz; yardım sayfasında arama, iç içe içindekiler, uyarı kutuları |
| v2.3.0 | Performans sayfası: ajansız APM (erişim günlüğü, yavaş sorgular, gecikme kırılımı, bağlantı haritası) |
| v2.4.0 | Alarmlarda "Neden?": değişiklik-nedensellik (kök neden) analizi |
| v2.5.0 | Saldırı yüzeyi sapması: port, uç nokta, TLS, güvenlik başlığı, tarama izi |
| v2.6.0 | Kanıt zinciri: hash zincirli denetim günlüğü, imzalı aylık kanıt raporu, doğrulama |
| v2.6.1 | Genel bakış: Performans widget'ları, hazır pano şablonları, widget ekle/sırala/boyutlandır, kayıtlı panolar |
| v2.6.2 | Eski SSH sunucusu uyumluluğu (SSH probe'unda zayıf algoritmaları ek olarak açma) ve algoritma uyuşmazlığı ipuçları |
| v2.6.3 | SSH probe: uzak komutun çıkış koduna dayanıklılık (eski makinelerde "exit status 4") |
| v2.7.0 | SSH probe: toplanacak veri seçimi (swap, yük, çalışma süresi, bağlantı, port, IP), dizin filtresi ve Sunucu ayrıntısı penceresi |
| v2.7.1 | Probe ayrıntıları: tüm probe türleri için son ölçümün ayrıntı penceresi (sertifika, DNS cevapları, HTTP başlıkları, ICMP istatistikleri, sıçramalar...) |
| v3.7.0 | **Ölçek paketi 2:** Ajanlar sayfasında arama/süzgeç/sayfalama ve çoklu seçim (toplu veri ayarı ve güncelleme), kademeli (dalga dalga) ajan güncellemesi, Probe ve Senaryolarda çoklu seçim + toplu etkinleştir/durdur/aralık/sil, probe CSV içe aktarma, asset başına ingest oran sınırı |
| v3.18.0 | **Ayrıntılı yorumlar:** Saldırı yüzeyi sapma ve referans satırlarında "ne oldu / neden önemli / ne yapılmalı"; Asset kartı › Genel'de Kritik/Uyarı nedenleri ve önerilen çözümler. |
| v3.17.0 | **Pano widget'ları v2:** özet şerit + kareler + sağ detay paneli (yüzlerce probe'a ölçeklenir); SSL renkleri alarm kuralları ve açık alarmlara göre; eski "Sertifika süreleri (TLS)" widget'ı kaldırıldı. |
| v3.16.0 | **Pano widget'ları:** SSL, DNS, NTP, TCP/UDP, ICMP, yol izleme ve veritabanı probe'larının tümünü gösteren görsel widget'lar ve "Ağ ve altyapı servisleri" şablonu. |
| v3.15.0 | **Asset kartı:** asset'e tıklayınca Genel, Yazılım envanteri, Topoloji ve bağlantılar, Performans, Güvenlik ve Olaylar sekmeleriyle tek sayfa. |
| v3.14.0 | **Saldırı yüzeyi › Envanter:** ajan dinleyen port + süreç + çalışma zamanı sürümü + bağımlılık (manifest) bildirir; destek dışı sürüm uyarısı; OSV tabanlı CVE eşleme (ajan v1.6.0). |
| v3.13.0 | **Servis haritası:** tam ad/adres, sürüklenebilir kutular, duruma göre çerçeve rengi. **Senaryo bağımlılıkları:** tıklayınca yakınlaşma, hata iletisi ve olası neden yorumu. |
| v3.12.2 | Docker: log ve NetFlow ortam değişkenleri `.env` üzerinden sunucuya iletilir (`APM_LOG_EVENTS=1`, `APM_NETFLOW_ADDR=:2055`). |
| v3.12.1 | Loglar sekmesi log verisi yokken hata vermesin diye düzeltildi. |
| v3.12.0 | **İzleme sayfalarında yorum:** Uygulamalar, Veritabanı sorguları (veritabanı özeti → tıklayınca sorgular), Bağlantılar, Ağ akışı ve Trace servislerinde "Yorum" sütunu/paneli ve orantılı çubuklar; karmaşayı azaltmak için ayrıntılar satıra tıklayınca açılır. |
| v3.11.0 | **Ağ akışı (NetFlow/IPFIX):** NetFlow v5/v9 ve IPFIX UDP toplayıcı, aralık özetleri (en çok N konuşma), Performans › Ağ akışı sayfası, asset eşleştirme, `flow.rx_bps`/`flow.tx_bps` metrikleri, Topolojide ağ akışı kaplaması (varsayılan kapalı: `APM_NETFLOW_ADDR`) |
| v3.10.0 | **Log–trace ilişkilendirme:** OTLP günlükleri (`/v1/logs`) ve JSON (`/api/logs`) alımı, trace ayrıntısında "İlgili loglar", Loglar sekmesi, mesaj maskeleme, kısa saklama ve hız sınırı (varsayılan kapalı: `APM_LOG_EVENTS=1`) |
| v3.9.0 | **Raporlar:** özel rapor oluşturucu (bölüm seçimi, dönem, grup kapsamı, önceki dönemle karşılaştırma), kayıtlı şablonlar, yeni bölümler (performans, veritabanı sorguları, senaryolar, sertifikalar, ajanlar, saldırı yüzeyi, kök neden), CSV ve JSON dışa aktarma |
| v3.8.1 | **Bot koruması uyarısı:** Bir site (ör. büyük e-ticaret siteleri) tarayıcı senaryosunu otomasyon sanıp engel sayfası gösterirse kayıt ekranında kırmızı uyarı çıkar ve senaryo "sertifika/ağ hatası" yerine "Bot koruması engelledi" açıklamasıyla başarısız olur |
| v3.8.0 | **Ölçek paketi 3 (veri hacmi):** `metrics` tablosu günlük bölümlere ayrıldı (eski bölümler tek seferde silinir), son değerler `asset_latest` tablosundan okunur, toplu metrik yazımı, Ajanlar sayfasında özet kutuları, çevrimdışı eşiği ve ısınma süresi ayarlanabilir, 30+ seçenekte aranabilir seçim listesi, haritada otomatik yenilemeyi durdurma, **senaryoların keşfettiği dış servisler haritada ayrı gösterilir**. **Güncellemeden önce `pg_dump` ile yedek alın (migration 046 geri alınamaz).** |
| v3.6.0 | **Ajan 1.5.0 · Ölçek paketi 1:** alarm sorgusu için indeks, alarm kuralları pencereye göre 15–60 sn'de değerlendirilir, probe/senaryo/tarayıcı eşzamanlılığı ayarlanabilir ve yavaş probe'lar turu bekletmez, ingest yuva sınırı (503 + Retry-After), ajanda jitter ve geri çekilme, aynı kuraldan gelen alarmlar tek bildirimde, Senaryolar'da kapasite uyarısı |
| v3.5.1 | Veri ayarı'nda isteğe bağlı veritabanı kullanıcı/parola (Kasa'da şifreli, yalnızca HTTPS, tek ajan) |
| v3.5.0 | **Ajan 1.4.0:** Performans sayfası ajanla dolar (PostgreSQL sorgu istatistikleri `APM_PG_DSN`, gelen/giden bağlantı özeti, trace kaynaklı uygulamalar); Ajanlar sayfasından uzaktan veri ayarı (onay kutuları, tek ajan veya tümü, yerel kilit `APM_REMOTE_CONFIG=off`); antivirüs/EDR izin uyarısı |
| v3.4.1 | Ajan örnek yapılandırma dosyaları (`agent-config/`, `/kurulum/appmon-agent-*.conf`, `-print-config`) |
| v3.4.0 | **Ajan 1.3.0:** yapılandırma dosyasında `SEND_*` ile gönderilecek veri seçimi (`#` = gönderme), gzip sıkıştırmalı gönderim; Topoloji'de Harita ve Senaryolar ayrı sekmeler; "hafif ajan" ifadesi kaldırıldı |
| v3.3.0 | **Ajan 1.2.0:** ajan yerel OTLP alıcısı (`127.0.0.1:4318`, trace'leri sunucuya iletir, anahtarsız kurulum) ve giden TCP bağlantılarından servis haritası; haritada Kaynak seçicisi (birleşik/trace/bağlantı); kurulum betikleri ajanı otomatik bulur (`--via-agent`) |
| v3.2.0 | Dağıtık izleme: Linux ve Windows kurulum betikleri (`/kurulum/otel-kurulum.sh`, `.ps1`): bağlantı + deneme trace'i, systemd/Windows servisi/IIS yapılandırma, dile göre ajan kurulumu, geri alma |
| v3.1.0 | **Kendi kendini izleme:** APP_MON API isteklerini `appmon-server` servisi olarak trace'ler (`traceparent` desteği, örnekleme, `APM_SELF_TRACE`) |
| v3.0.0 | **Dağıtık izleme:** OTLP/HTTP trace alımı (protobuf/JSON, gzip), trace listesi ve şelale görünümü, servis RED özeti, servis haritası |
| v2.8.0 | Raporlar "Alarm ve raporlar" altına taşındı; SLA zaman filtreleri (mesai, özel saat/gün), saatlik/günlük/haftalık kırılım; parolalı SLA sıfırlama |
| v2.7.9 | Performans: DNS, Ağ servisleri ve Yol izleme (DSCP) sekmeleri; ajan 1.1.2 |
| v2.7.6 | Ajan 1.1.0: nginx/Apache/IIS erişim günlüğünü kendiliğinden okur (Performans → Uygulamalar), `-web-setup`/`-web-undo`, ajan sürümü sunucudan bağımsız; Genel bakış bölümleri çizgilerle ayrıldı |
| v2.7.5 | Ajanlar: uzaktan ajan güncelleme (satır başına / toplu / otomatik; SHA-256 doğrulamalı), `-no-update` |
| v2.7.4 | Assetler: **Ayrıntı** artık tablonun altında değil, probe ayrıntısı gibi pencere olarak açılır |
| v2.7.3 | Ajan uygulama keşfi: süreçler, servisler, portlar, CPU/bellek ve bilinen günlüklerde hata/uyarı sayısı; Assetler ayrıntısında "Uygulamalar (ajan)" |
| v2.7.2 | Assetler: satır içi grup/etiket düzenleme, satır sonunda tür·ayrıntı·sil; Topoloji: üstten alta hiyerarşi, yakınlaştırma, bağımlılık yayılımı (sunucu düşünce bağlılar kırmızı); listelerde üst sayfalama ve arama süzgeci; probe ve diğer pano widget'ları |

## Kapsam dışı olanlar

Log toplama, gerçek kullanıcı izleme (RUM), trace'lerden alarm üretme, WinRM ile ajansız Windows ölçümü, MySQL/SQL Server izleme, SNMP v3 trap, LDAP grup eşlemesi ve kasaya taşınmamış sırlar bu sürümde yoktur.
