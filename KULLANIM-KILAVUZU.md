# APP_MON Kullanım Kılavuzu

APP_MON; sunucu, servis, web sitesi ve ağ cihazlarınızı izleyen, sorun olduğunda alarm üreten ve bildirim gönderen bir izleme (APM) platformudur.

## 1. Kurulum ve güncelleme
**Docker (Windows):** proje klasöründe PowerShell ile `.\baslat-docker.bat` (ilk kurulum), `.\guncelle-docker.bat` (güncelleme), `.\durdur-docker.bat` (durdurma). Güncelleme veriyi silmez; veritabanı değişiklikleri açılışta otomatik ve yıkıcı olmayan şekilde uygulanır.
**Sürüm:** üst çubukta (On-premise etiketinin yanında) ve Ayarlar > Sistem'de görünür (ör. v1.0.0).
**Güncelleme (arayüzden):** yönetici olarak **Güncelleme** sayfasını açın, `appmon-guncelleme-vX.Y.Z.tar.gz` paketini seçip **Yükle ve doğrula**'ya basın (paket açılmadan, olduğu gibi yüklenir). Sürüm, sağlama toplamları ve (açıksa) imza doğrulanır; **Güncellemeyi uygula** ile veritabanı yedeği alınır, yeni sürüm yüklenir, servis yeniden başlar ve sayfa yenilenir. Yeni sürüm açılmazsa eski sürüme otomatik dönülür ("Geri alındı"). Daha eski sürüme geçilemez.
**İlk kurulum (RHEL, bir kez):** arayüzden güncelleme hizmeti, güncelleme paketindeki betikle kurulur: `tar xzf appmon-guncelleme-vX.tar.gz; cd appmon-guncelleme; sha256sum -c SHA256SUMS; sudo ./guncelle-apache.sh`. Sonraki güncellemeler arayüzden yapılır. Elle geri alma: `sudo appmon-guncelle --geri-al`. Docker kurulumunda arayüzden güncelleme kapalıdır (`.\guncelle-docker.bat`).
**Paket imzası (önerilir):** paketleme makinesinde `./paketle-guncelleme.sh --anahtar-uret` ile Ed25519 anahtar çifti üretin (özel anahtar gizli kalır), sunucuya `sudo ./guncelle-apache.sh --imza-anahtari genel-anahtar.pem` ile genel anahtarı tanıtın, paketleri `./paketle-guncelleme.sh vX.Y.Z --imzala ozel-anahtar.pem` ile imzalayın. İmza açıkken imzasız/değiştirilmiş paketler reddedilir.
**Linux / çevrimdışı kurulum:** paketteki `kur.sh` veya `kur-apache.sh`. Mevcut kurulumda ayar dosyası, veritabanı ve anahtarlar korunur; yalnızca ikili dosya değişir. Güncellemeden önce yedek alın: `pg_dump appmon > appmon-yedek.sql`.
**Ortam değişkenleri:** `APM_PUBLIC_URL` bildirimlerdeki bağlantı adresidir (ör. `https://apm.sirket.local`).

## 2. Giriş ve roller
Anahtar oluştururken rol seçilir (Lisans ve anahtarlar sayfası):
| Rol | Yetki |
|---|---|
| admin | Her şey (kanallar, anahtarlar, ayarlar) |
| operator | Okuma + yazma (probe, kural, bakım, üstlenme) |
| viewer | Yalnızca okuma |
| agent | Yalnızca veri gönderimi |
Yetkisiz sayfalar menüden gizlenir; asıl denetim sunucudadır (403).

## 3. Genel bakış, Assetler, Topoloji
- **Genel bakış:** durum özeti. **Assetler:** izlenen sunucu/cihazlar; her birine **grup** (tek) ve **etiket** (çoklu) atanabilir, listede filtrelenir. **Topoloji:** ilişkiler.
- Asset sayısı lisansa göre sınırlıdır (üst barda "5 / 25 asset").

## 4. Probe'lar (izleme türleri)
Probe'lar sayfasından eklenir; her probe aralıkla (sn) çalışır.
- **HTTP/TCP/Ping/TLS sertifika/NTP:** erişilebilirlik, gecikme, sertifika süresi, saat sapması.
- **SNMP:** tek OID okur (v1, v2c, v3).
- **Sunucu kaynakları (host):** SNMP ile CPU, RAM, diskler (`host.cpu_pct`, `host.mem_pct`, `host.disk_pct`, `host.disk_pct.<ad>`, `host.uptime_sec`). Agent kurmadan çalışır; hazır alarm kuralları doğrudan işler.
- **Senaryo:** web sitesi adım adım test.
**SNMPv3:** güvenlik düzeyi noAuthNoPriv / authNoPriv / authPriv; kimlik doğrulama MD5, SHA, SHA-224/256/384/512; gizlilik DES, AES-128. Parolalar en az 8 karakter; düzenlerken boş bırakırsanız eski değer korunur.
**Windows hedef:** "SNMP Hizmeti" kurun, topluluk adı ve izinli yöneticiyi tanımlayın, güvenlik duvarında UDP 161'i açın. **Linux:** `snmpd` kurun, `rocommunity` ekleyin. Docker içinden ana makineye erişmek için hedef olarak `host.docker.internal` kullanın.

## 5. Alarmlar
**Alarm panosu:** sorunlu hizmetler renk/şekil ile (Kritik kırmızı sekizgen, Erişilemiyor çarpılı daire, Uyarı sarı üçgen, Bilgi mavi daire, Sağlıklı yeşil).
**Genel bakış sırası:** üstte Genel bakış kutuları, ardından Kontrol edilen sayfalar, ardından **Makineler · CPU / RAM / Disk** widget'ı (ölçümü gelen makineler; en yüklü olan başta; %75 üzeri sarı, %90 üzeri kırmızı; karta tıklayınca Assetler sayfasında o makine açılır), sonra diğer bölümler.

**Topoloji görünümü:** varsayılan olarak yalnızca ana cihazlar (tür: Sunucu/host ve Ağ) görünür. Bir cihaza tıklayınca o cihaz, doğrudan bağlı olduğu/bağlı olan bileşenler ve üzerinde çalışan her şey (ör. site, veritabanı) gösterilir. "Ana cihazlara dön" ile geri dönülür; "Tüm asset'leri göster" eski tam görünümü açar. Hiç sunucu/ağ türünde asset yoksa tümü gösterilir.

**Topoloji hata/uyarı ayrıntıları:** bir cihaza/asset'e tıklayınca haritanın altında "Hata ve uyarılar" kutusu çıkar: açık alarmlar (önem, kural, ölçüm değeri, süre), hatalı probe'lar ve senaryolar (hata metniyle) ve "sayfa değişti" uyarıları ("Ayrıntılar" ile değişiklik listesi). Sorun yoksa kutu görünmez.

**Kart renkleri (Kontrol edilen sayfalar):** son 3 kontrolün hepsi başarısızsa **kırmızı**, en az biri başarısız (ama hepsi değil) ise **sarı** ("Aralıklı hata"), son 3'ü de başarılıysa **yeşil**. Tek seferlik geçici kesintiler kırmızı alarm görüntüsü oluşturmaz.

**Kart ayrıntıları (Genel bakış > Kontrol edilen sayfalar):** bir kartın durum rozetine (Sağlıklı / Sayfa değişti / Başarısız) veya hata metnine tıklayınca ayrıntı penceresi açılır: hata sebebi ve başarısız adım, sayfa değişiklikleri, son çalıştırmanın adım adım özeti. Kartın kendisine tıklamak ekran görüntüsünü büyütür.

**Sayfa değişti ayrıntıları:** tarayıcı senaryosunda bir sayfanın yapısı değiştiğinde "Sayfa değişti" rozeti çıkar. Rozete (Genel bakış, Senaryolar listesi) veya "Ayrıntıları göster" düğmesine tıklayınca hangi adımda/sayfada neyin **eklendiği** ya da **kaldırıldığı** (başlık, buton, bağlantı, form, giriş alanı, harici script) okunur biçimde, tespit zamanıyla birlikte listelenir. Karşılaştırma bir önceki çalıştırmayadır; "Uyarıyı temizle" işareti kaldırır.

**Kısmen erişilemiyor (sarı):** bir hizmetin birden çok probe/senaryosu varsa (ör. ana sayfa + alt sayfalar/portlar) durum hepsine bakılarak belirlenir: hepsi başarılı = Çalışıyor, hepsi başarısız = Erişilemiyor (kırmızı, "Servis erişilemiyor" kritik alarmı), bir kısmı başarısız = **Kısmen erişilemiyor** (sarı, "Servis kısmen erişilemiyor" uyarı alarmı). Yani sayfa açılıyor ama bazı alt bileşenlere gidilemiyorsa kırmızı değil sarı görünür. Tek probe'lu hizmetlerde davranış değişmez.
**Kurallar:** ad, metrik, koşul (>, <...), eşik, pencere (sn), önem. Kural pencere içindeki **ortalama** değeri eşikle karşılaştırır; 15 sn'de bir değerlendirilir. Düzenlenen kuralın açık alarmları kapanır, koşul sürüyorsa yeniden açılır. "Hazır şablonlar" ile tek tıkla eklenir.
**Kapsam:** hizmetler (boş = hepsi), grup, etiket ile kuralı sınırlayın.
**Üstlenme:** açık alarmda "Üstleniyorum" ile sahiplenin; "Geri al" ile bırakın. Kimin ne zaman üstlendiği listede görünür.
**Bakım pencereleri:** tek seferlik / günlük / haftalık; hizmet, grup veya etiket kapsamı. Pencere sırasında alarm yine kaydedilir ("bakımda" etiketi) fakat bildirim gönderilmez.

## 6. Bildirimler (admin)
Bildirimler sayfasından kanal ekleyin; her kanalda **en düşük önem**, **çözüldü bildirimi** ve **Test gönder** vardır. Gizli alanlar kaydedildikten sonra gösterilmez.
| Kanal | Gerekenler |
|---|---|
| SMTP | Sunucu, port (587 STARTTLS / 465 SSL / 25), kullanıcı, parola, gönderen, alıcılar |
| Microsoft Graph | Tenant ID, Client ID, Client Secret, gönderen posta kutusu (Azure'da Mail.Send uygulama izni) |
| Exchange EWS | EWS adresi (…/EWS/Exchange.asmx), kullanıcı, parola (yalnızca Basic) |
| Windows MAPI | Outlook yüklü ve oturum açık Windows'ta APP_MON yerel çalışmalı; Docker/Linux'ta çalışmaz |
| Webhook | URL (JSON POST) |
| Slack / Teams | Gelen webhook URL'si |
| Telegram | Bot token ve chat ID |
Alıcılar virgülle ayrılır. Hata olursa kanalın son hatası kartta görünür.

### SIEM'e log gönderimi (admin)
Bildirimler sayfasının altındaki **SIEM'e log gönderimi** bölümünden hedef eklenir. Denetim kayıtları (kim neyi değiştirdi) ve alarmlar (açılış/düzelme) olay anında iletilir; bakım penceresindeki alarmlar da iletilir ("bakım penceresinde" notuyla).
| Ayar | Açıklama |
|---|---|
| Protokol | Syslog UDP (514), Syslog TCP (514), Syslog TLS (6514) veya HTTP (Splunk HEC / genel JSON) |
| Biçim | RFC 5424 (yapılandırılmış veri ile), CEF (ArcSight, QRadar, Sentinel), JSON |
| Facility / uygulama adı | Syslog facility (varsayılan 16 = local0) ve app-name (varsayılan appmon) |
| Kaynaklar | Denetim kayıtları ve/veya alarmlar; alarmlar için en düşük önem seçilebilir |
| HTTP | Adres + kimlik doğrulama (Splunk HEC token veya Bearer); HTTP'de gövde JSON'dur |
- Önem eşlemesi: kritik = syslog 2 / CEF 9, uyarı = 4 / 6, bilgi = 6 / 3.
- **Deneme gönder** ile hedefe örnek olay gönderilir; hata olursa hedef kartında "Son hata" görünür.
- Syslog UDP'de teslim garantisi yoktur; güvenilir iletim için TCP veya TLS seçin. Kendinden imzalı sertifikada "TLS sertifika doğrulamasını atla" işaretlenebilir.
- Token gizli saklanır; düzenlerken boş bırakılırsa eski değer korunur.

## 7. Sorun giderme
- **Yeni özellik görünmüyor:** `.\guncelle-docker.bat` çalıştırın, tarayıcıda Ctrl+F5.
- **SNMP "zaman aşımı":** servis çalışıyor mu, topluluk adı, izinli yönetici listesi, UDP 161 güvenlik duvarı.
- **"x509: certificate signed by unknown authority" (https probe'u):** site kurum içi (özel) bir CA veya kendinden imzalı sertifika kullanıyor. İki çözüm var:
  - **Önerilen — kurum CA'sını tanıtın:** CA sertifikasını PEM olarak alın. Docker: dosyayı proje klasöründe `certs\ca.pem` adıyla koyup `.\guncelle-docker.bat` çalıştırın. RHEL/Linux: dosyayı sunucuya kopyalayıp `/etc/appmon/appmon.env` içine `APM_CA_FILE=/etc/appmon/ca.pem` ekleyin ve `systemctl restart appmon` yapın (alternatif: `cp ca.pem /etc/pki/ca-trust/source/anchors/ && update-ca-trust`, ardından servisi yeniden başlatın). Birden çok CA için aynı dosyaya art arda ekleyebilirsiniz. Uygulama başlarken günlükte "kurum CA dosyası yüklendi" yazar.
  - **Hızlı çözüm:** probe'u düzenleyip **"Sertifika zincirini doğrula"** kutusunu kapatın (yalnızca o probe için; sertifika kalan gün sayısı yine ölçülür).
- **SIEM'e olay düşmüyor:** hedefte Deneme gönder'i deneyin; port/protokolü ve güvenlik duvarını kontrol edin; UDP'de hata görünmeyebilir, TCP kullanın.
- **Bildirim gitmiyor:** Test gönder'i deneyin; kanalın son hatasına bakın; bakım penceresi aktif olabilir; en düşük önem alarmın önemini geçiyor olabilir.
- **Docker'da MAPI:** desteklenmez; SMTP/Graph/EWS kullanın.

## 8. Henüz olmayanlar
SSH/WinRM ile ölçüm, SNMP trap, çoklu OID/şablon, raporlar/SLA, log ve veritabanı izleme, keşif, durum sayfası, LDAP, yedek/dışa aktarma sonraki aşamalardadır.
