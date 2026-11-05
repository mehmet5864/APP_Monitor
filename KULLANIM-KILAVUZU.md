# APP_MON Kullanım Kılavuzu

APP_MON; sunucu, servis, web sitesi ve ağ cihazlarınızı izleyen, sorun olduğunda alarm üreten ve bildirim gönderen bir izleme (APM) platformudur.

## 1. Kurulum ve güncelleme
**Docker (Windows):** proje klasöründe PowerShell ile `.\baslat-docker.bat` (ilk kurulum), `.\guncelle-docker.bat` (güncelleme), `.\durdur-docker.bat` (durdurma). Güncelleme veriyi silmez; veritabanı değişiklikleri açılışta otomatik ve yıkıcı olmayan şekilde uygulanır.
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

## 7. Sorun giderme
- **Yeni özellik görünmüyor:** `.\guncelle-docker.bat` çalıştırın, tarayıcıda Ctrl+F5.
- **SNMP "zaman aşımı":** servis çalışıyor mu, topluluk adı, izinli yönetici listesi, UDP 161 güvenlik duvarı.
- **Bildirim gitmiyor:** Test gönder'i deneyin; kanalın son hatasına bakın; bakım penceresi aktif olabilir; en düşük önem alarmın önemini geçiyor olabilir.
- **Docker'da MAPI:** desteklenmez; SMTP/Graph/EWS kullanın.

## 8. Henüz olmayanlar
SSH/WinRM ile ölçüm, SNMP trap, çoklu OID/şablon, raporlar/SLA, log ve veritabanı izleme, keşif, durum sayfası, LDAP, yedek/dışa aktarma sonraki aşamalardadır.
