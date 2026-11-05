APP_MON - Apache arkasında tek komutla kurulum (internetsiz)
============================================================
Gereksinimler (hedef Linux sunucu, x86_64, systemd'li):
  - Apache 2.4 (apache2 veya httpd) kurulu; mod_proxy_http ve mod_headers (RHEL'de httpd ile gelir, Debian'da betik açar)
  - Başka HİÇBİR ŞEY gerekmez: PostgreSQL ve Chrome bu paketin içinde, kendi kütüphaneleriyle (taşınabilir) gelir.
    İnternet gerekmez. (Sistemde zaten PostgreSQL/Chrome varsa onlar kullanılır; uzak veritabanı için --db-dsn.)
  - HTTPS kullanacaksanız Apache'de mod_ssl kurulu olmalı (RHEL: mod_ssl paketi).

KURULUM (tek komut):
    tar xzf appmon-apache.tar.gz && cd appmon-apache && sudo ./kur-apache.sh

Sık kullanılanlar:
    sudo ./kur-apache.sh --port 8081                      80 yerine başka portta yayınla
    sudo ./kur-apache.sh --servername apm.firma.local --tls-cert /yol/sertifika.pem --tls-key /yol/anahtar.pem
                                                          HTTPS (443) ile yayınla
    sudo ./kur-apache.sh --db-dsn 'postgres://k:p@dbsunucu:5432/appmon?sslmode=disable'
                                                          mevcut/uzak PostgreSQL kullan
    sudo ./kur-apache.sh --db-admin admin                 veritabanında yönetici (süper kullanıcı) hesap da oluştur;
                                                          parola kurulum sonunda yazılır (--db-admin-pass ile kendiniz verebilirsiniz)
    sudo ./kur-apache.sh --dry-run                        hiçbir şey değiştirmeden ne yapılacağını göster

Ne yapar:
  - /opt/appmon altına ikili dosyayı yükler, "appmon" sistem kullanıcısı ve systemd servisi oluşturur
  - /etc/appmon/appmon.env: rastgele giriş anahtarı ve veritabanı parolası (kurulum sonunda ekrana yazılır)
  - uygulamayı yalnızca 127.0.0.1:18080'de dinletir; Apache sanal sunucusu (appmon.conf) bunu proxy'ler
  - SELinux açıksa httpd_can_network_connect'i etkinleştirir
  - ping (ICMP) probe'u için gerekli izinleri verir

Güncelleme: yeni paketi açıp aynı komutu tekrar çalıştırın (ayarlar ve veriler korunur).
Kaldırma:   sudo ./kur-apache.sh --kaldir            (veri korunur)
            sudo ./kur-apache.sh --kaldir --purge    (veritabanı ve ayarlar da silinir)

Gömülü PostgreSQL: /opt/appmon/pg (veri: /var/lib/appmon-pg, servis: appmon-pg, yalnızca 127.0.0.1:5433).
Yedek: sudo -u appmon-pg env LD_LIBRARY_PATH=/opt/appmon/pg/lib /opt/appmon/pg/usr/lib/postgresql/16/bin/pg_dump -h /run/appmon-pg -p 5433 appmon > yedek.sql

Agent (isteğe bağlı): izlenen sunuculara kurmak için /opt/appmon/dist/appmon-agent-linux-* dosyaları.
Notlar:
  - Güvenlik duvarında Apache portunu açın (firewalld: firewall-cmd --permanent --add-port=80/tcp && firewall-cmd --reload).
  - Apache'de ProxyPass ile alt yol (örn. /appmon) desteklenmez; ayrı sanal sunucu/port kullanın.
  - Kurum içi (self-signed) https sitelerini izleyen tarayıcı senaryoları sertifika hatalarını yok sayar;
    sıkı doğrulama için /etc/appmon/appmon.env içine APM_BROWSER_STRICT_TLS=true ekleyip systemctl restart appmon.
  - Sorun olursa: journalctl -u appmon -f   ve   Apache günlükleri (appmon-error.log)
