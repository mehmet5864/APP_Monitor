#!/usr/bin/env bash
# APP_MON - Apache arkasinda tek komutla kurulum (internetsiz / kapali ag icin).
#   sudo ./kur-apache.sh                     kur ve baslat (HTTP, 80. port)
#   sudo ./kur-apache.sh --port 8081         baska portta yayinla
#   sudo ./kur-apache.sh --servername apm.firma.local --tls-cert /yol/c.pem --tls-key /yol/k.pem
#   sudo ./kur-apache.sh --db-dsn 'postgres://kullanici:parola@dbsunucu:5432/appmon?sslmode=disable'
#   sudo ./kur-apache.sh --db-admin admin    veritabaninda yonetici (superuser) hesap da olustur (parola ekrana yazilir)
#   sudo ./kur-apache.sh --dry-run           hicbir sey degistirmeden ne yapilacagini goster
#   sudo ./kur-apache.sh --kaldir [--purge]  kaldir (--purge: veritabani ve ayarlar da silinir)
# Tekrar calistirmak guvenlidir: ayarlar ve veriler korunur, yeni ikili dosya yuklenir (guncelleme).
set -euo pipefail
cd "$(dirname "$0")"
HERE="$(pwd)"

PORT=80; SERVERNAME=""; TLS_CERT=""; TLS_KEY=""; DB_DSN=""; ASSET_LIMIT=25; INTERNAL_PORT=18080
DRY=0; REMOVE=0; PURGE=0; DB_ADMIN=""; DB_ADMIN_PASS=""
APP_USER=appmon; APP_DIR=/opt/appmon; ENV_FILE=/etc/appmon/appmon.env; UNIT=/etc/systemd/system/appmon.service

while [ $# -gt 0 ]; do
  case "$1" in
    --port) PORT="$2"; shift 2;;
    --servername) SERVERNAME="$2"; shift 2;;
    --tls-cert) TLS_CERT="$2"; shift 2;;
    --tls-key) TLS_KEY="$2"; shift 2;;
    --db-dsn) DB_DSN="$2"; shift 2;;
    --asset-limit) ASSET_LIMIT="$2"; shift 2;;
    --db-admin) DB_ADMIN="$2"; shift 2;;
    --db-admin-pass) DB_ADMIN_PASS="$2"; shift 2;;
    --internal-port) INTERNAL_PORT="$2"; shift 2;;
    --dry-run) DRY=1; shift;;
    --kaldir) REMOVE=1; shift;;
    --purge) PURGE=1; shift;;
    -h|--help) sed -n 2,12p "$0"; exit 0;;
    *) echo "Bilinmeyen secenek: $1 (yardim: --help)"; exit 2;;
  esac
done

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[UYARI]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[HATA]\033[0m %s\n' "$*" >&2; exit 1; }
run()  { if [ "$DRY" = 1 ]; then echo "  + $*"; else "$@"; fi; }
have() { command -v "$1" >/dev/null 2>&1; }
rand() { if have openssl; then openssl rand -hex "$1"; else head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; fi; }

[ "$(id -u)" -eq 0 ] || [ "$DRY" = 1 ] || die "root olarak calistirin: sudo ./kur-apache.sh"
[ -n "$TLS_CERT$TLS_KEY" ] && { [ -n "$TLS_CERT" ] && [ -n "$TLS_KEY" ] || die "--tls-cert ve --tls-key birlikte verilmeli"; [ "$PORT" = 80 ] && PORT=443; }

# --- Apache ---
if have apache2ctl; then FAMILY=debian; APACHECTL=apache2ctl; SVC=apache2
elif have apachectl && [ -d /etc/apache2 ]; then FAMILY=debian; APACHECTL=apachectl; SVC=apache2
elif have httpd; then FAMILY=rhel; APACHECTL="httpd"; SVC=httpd
elif have apachectl; then FAMILY=rhel; APACHECTL=apachectl; SVC=httpd
else die "Apache bulunamadi (apache2/httpd). Once Apache'yi kurun."; fi

CONF_NAME=appmon.conf
if [ "$FAMILY" = debian ]; then CONF=/etc/apache2/sites-available/$CONF_NAME; LOGDIR='${APACHE_LOG_DIR}'; else CONF=/etc/httpd/conf.d/$CONF_NAME; LOGDIR=/var/log/httpd; fi

if [ "$REMOVE" = 1 ]; then
  say "APP_MON kaldiriliyor"
  run systemctl disable --now appmon 2>/dev/null || true
  [ -f /etc/systemd/system/appmon-pg.service ] && run systemctl disable --now appmon-pg 2>/dev/null || true
  [ "$FAMILY" = debian ] && run a2dissite appmon 2>/dev/null || true
  run rm -f "$CONF" "$UNIT" /etc/sysctl.d/90-appmon-ping.conf /etc/apache2/sites-enabled/$CONF_NAME
  run systemctl daemon-reload
  run systemctl reload "$SVC" 2>/dev/null || true
  if [ "$PURGE" = 1 ]; then
    run rm -rf "$APP_DIR" /etc/appmon /var/lib/appmon-pg /etc/systemd/system/appmon-pg.service
    if have psql && [ "$DRY" = 0 ]; then su postgres -c "psql -qc 'DROP DATABASE IF EXISTS appmon' -c 'DROP ROLE IF EXISTS appmon'" || true; fi
    id "$APP_USER" >/dev/null 2>&1 && run userdel "$APP_USER" || true
  else echo "Veri korundu ($APP_DIR, /etc/appmon, veritabani). Tamamen silmek icin --kaldir --purge."; fi
  say "Kaldirildi."; exit 0
fi

ARCH="$(uname -m)"; case "$ARCH" in x86_64|amd64) GOARCH=amd64;; aarch64|arm64) GOARCH=arm64;; *) die "Desteklenmeyen islemci: $ARCH";; esac
[ -x "bin/appmon-server-linux-$GOARCH" ] || die "bin/appmon-server-linux-$GOARCH bulunamadi."
have systemctl || die "systemd gerekli."

# --- Yerel paket kurulumu (internet yok: paketler/ klasorunden) ---
PKGS_DONE=0
ensure_local_pkgs() { # paketler/ icindekilerin hepsini TEK islemde kurar (kendi aralarinda bagimlilik cozulur, kurulu olanlar atlanir)
  [ "$PKGS_DONE" = 1 ] && return 0
  install_local_pkgs paketler && PKGS_DONE=1
}
install_local_pkgs() { # $1 = klasor
  local d="$1" f
  if [ "$FAMILY" = debian ]; then
    f=$(ls "$d"/*.deb 2>/dev/null || true); [ -n "$f" ] || return 1
    # shellcheck disable=SC2086
    run env DEBIAN_FRONTEND=noninteractive dpkg -i $f || run apt-get install -y --no-download -f
  else
    f=$(ls "$d"/*.rpm 2>/dev/null || true); [ -n "$f" ] || return 1
    # shellcheck disable=SC2086
    if have dnf; then run dnf install -y --disablerepo='*' $f; else run rpm -Uvh --replacepkgs $f; fi
  fi
}

say "Apache modulleri"
if [ "$FAMILY" = debian ]; then
  run a2enmod proxy proxy_http headers >/dev/null
  [ -n "$TLS_CERT" ] && run a2enmod ssl >/dev/null
else
  MODS="$($APACHECTL -M 2>/dev/null || true)"
  echo "$MODS" | grep -q proxy_http_module || die "mod_proxy_http yuklu degil (httpd paketiyle gelir; yapilandirmada LoadModule satirini etkinlestirin)."
  echo "$MODS" | grep -q headers_module || die "mod_headers yuklu degil."
  if [ -n "$TLS_CERT" ] && ! echo "$MODS" | grep -q ssl_module; then
    ensure_local_pkgs || true; MODS="$($APACHECTL -M 2>/dev/null || true)"
    echo "$MODS" | grep -q ssl_module || die "mod_ssl yuklu degil. paket-indir-rhel.sh'yi --ssl ile calistirip paketler/ klasorunu doldurun."
  fi
fi


# --- Gomulu (tasinabilir) PostgreSQL: kendi glibc'si ile gelir, RHEL/Rocky/Alma/Debian/Ubuntu fark etmez ---
PGHOME=/opt/appmon/pg; PGDATA=/var/lib/appmon-pg/data; PGUSER=appmon-pg; PGPORT=5433; PGSOCK=/run/appmon-pg
EMBEDDED=0
pgrun() { runuser -u "$PGUSER" -- env LD_LIBRARY_PATH="$PGHOME/lib" PATH=/usr/bin:/bin "$@"; }
setup_embedded_pg() {
  EMBEDDED=1
  say "Gomulu PostgreSQL kuruluyor ($PGHOME)"
  have runuser || die "runuser (util-linux) gerekli."
  id "$PGUSER" >/dev/null 2>&1 || run useradd --system --home-dir /var/lib/appmon-pg --shell /usr/sbin/nologin "$PGUSER"
  run install -d -o root -g root -m 755 /opt/appmon
  if [ "$DRY" = 0 ]; then rm -rf "$PGHOME.new"; cp -a pg "$PGHOME.new"; rm -rf "$PGHOME"; mv "$PGHOME.new" "$PGHOME"; chown -R root:root "$PGHOME"; else echo "  + cp -a pg $PGHOME"; fi
  have restorecon && run restorecon -R /opt/appmon 2>/dev/null || true
  run install -d -o "$PGUSER" -g "$PGUSER" -m 700 /var/lib/appmon-pg
  local BIN="$PGHOME/usr/lib/postgresql/16/bin"
  if [ "$DRY" = 0 ] && [ ! -f "$PGDATA/PG_VERSION" ]; then
    local pw; pw="$(mktemp)"; chown "$PGUSER" "$pw"; rand 16 > "$pw"
    pgrun "$BIN/initdb" -D "$PGDATA" -E UTF8 --locale=C.UTF-8 --auth-local=peer --auth-host=scram-sha-256 --pwfile="$pw" >/dev/null || die "initdb basarisiz."
    rm -f "$pw"
    cat >> "$PGDATA/postgresql.conf" <<CONF
# APP_MON
listen_addresses = '127.0.0.1'
port = $PGPORT
unix_socket_directories = '$PGSOCK'
max_connections = 100
shared_buffers = 256MB
jit = off
timezone = 'UTC'
CONF
  elif [ "$DRY" = 1 ]; then echo "  + initdb -D $PGDATA"; fi
  if [ "$DRY" = 1 ]; then echo "  + $PGDATA/../appmon-pg.service yazilir"; else
cat > /etc/systemd/system/appmon-pg.service <<PGUNIT
[Unit]
Description=APP_MON PostgreSQL (gomulu)
After=network.target

[Service]
User=$PGUSER
Group=$PGUSER
Environment=LD_LIBRARY_PATH=$PGHOME/lib
RuntimeDirectory=appmon-pg
RuntimeDirectoryMode=0755
ExecStart=$BIN/postgres -D $PGDATA
ExecReload=/bin/kill -HUP \$MAINPID
KillMode=mixed
KillSignal=SIGINT
TimeoutSec=120
Restart=on-failure

[Install]
WantedBy=multi-user.target
PGUNIT
  fi
  run systemctl daemon-reload
  run systemctl enable appmon-pg
  run systemctl restart appmon-pg
  if [ "$DRY" = 0 ]; then
    for i in $(seq 1 40); do pgrun "$BIN/psql" -h "$PGSOCK" -p "$PGPORT" -d postgres -qAtc 'select 1' >/dev/null 2>&1 && break; sleep 1; done
    pgrun "$BIN/psql" -h "$PGSOCK" -p "$PGPORT" -d postgres -qAtc 'select 1' >/dev/null 2>&1 || { journalctl -u appmon-pg -n 20 --no-pager || true; die "Gomulu PostgreSQL acilmadi."; }
    PGPASS="$(rand 16)"
    pgrun "$BIN/psql" -h "$PGSOCK" -p "$PGPORT" -d postgres -v ON_ERROR_STOP=1 -qAt <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='appmon') THEN CREATE ROLE appmon LOGIN PASSWORD '$PGPASS';
  ELSE ALTER ROLE appmon WITH LOGIN PASSWORD '$PGPASS'; END IF;
END \$\$;
SELECT 'CREATE DATABASE appmon OWNER appmon' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='appmon')\gexec
SQL
  else PGPASS="DRYRUN"; fi
  DB_DSN="postgres://appmon:${PGPASS}@127.0.0.1:${PGPORT}/appmon?sslmode=disable"
}

# --- Veritabani ---
ENV_EXISTS=0; [ -f "$ENV_FILE" ] && ENV_EXISTS=1
if [ "$ENV_EXISTS" = 0 ]; then
  if [ -z "$DB_DSN" ]; then
    say "PostgreSQL"
    if { ! have psql || ! { systemctl list-unit-files 2>/dev/null | grep -qE '^postgresql'; }; } && [ -d pg/usr/lib/postgresql/16/bin ]; then
      setup_embedded_pg
    else
    if ! have psql || ! { systemctl list-unit-files 2>/dev/null | grep -qE '^postgresql'; }; then
      warn "PostgreSQL bulunamadi; paketler/ klasorunden kurulacak."
      ensure_local_pkgs || die "PostgreSQL kurulu degil ve paketler/ icinde paket yok. Kurulu bir PostgreSQL'iniz varsa --db-dsn verin, yoksa paketler/OKU.txt dosyasina bakin."
      if [ "$FAMILY" = rhel ] && [ ! -f /var/lib/pgsql/data/PG_VERSION ] && have postgresql-setup; then run postgresql-setup --initdb; fi
    fi
    PGSVC="$(systemctl list-unit-files 2>/dev/null | awk '/^postgresql[^ ]*\.service/{print $1; exit}')"
    PGSVC="${PGSVC:-postgresql.service}"
    run systemctl enable --now "$PGSVC"
    if [ "$DRY" = 0 ]; then for i in $(seq 1 30); do su postgres -c "psql -qAt -c 'select 1'" >/dev/null 2>&1 && break; sleep 1; done; fi
    PGPASS="$(rand 16)"
    if [ "$DRY" = 1 ]; then echo "  + (appmon rolu ve veritabani olusturulur)"; else
      su postgres -c "psql -v ON_ERROR_STOP=1 -qAt" <<SQL
DO \$\$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='appmon') THEN CREATE ROLE appmon LOGIN PASSWORD '$PGPASS';
  ELSE ALTER ROLE appmon WITH LOGIN PASSWORD '$PGPASS'; END IF;
END \$\$;
SELECT 'CREATE DATABASE appmon OWNER appmon' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname='appmon')\gexec
SQL
    fi
    DB_DSN="postgres://appmon:${PGPASS}@127.0.0.1:5432/appmon?sslmode=disable"
    if [ "$DRY" = 0 ] && have psql; then
      dbtest() { PGPASSWORD="$PGPASS" psql -h 127.0.0.1 -U appmon -d appmon -qAtc 'select 1' >/dev/null 2>&1; }
      if ! dbtest; then
        # RHEL'de 127.0.0.1 icin varsayilan 'ident' olabilir; parola (md5) yontemine cevir.
        HBA="$(su postgres -c "psql -qAt -c 'show hba_file'" 2>/dev/null || true)"
        if [ -n "$HBA" ] && [ -f "$HBA" ]; then
          cp -p "$HBA" "$HBA.appmon.bak"
          sed -i -E 's/^(host[[:space:]]+all[[:space:]]+all[[:space:]]+(127\.0\.0\.1\/32|::1\/128)[[:space:]]+)ident/\1md5/' "$HBA"
          su postgres -c "psql -qAt -c 'select pg_reload_conf()'" >/dev/null 2>&1 || true; sleep 1
        fi
        dbtest || warn "127.0.0.1 uzerinden parola ile baglanti kurulamadi; pg_hba.conf'ta '127.0.0.1/32 md5' (veya scram-sha-256) satiri gerekebilir."
      fi
    fi
    fi
  fi
fi

# --- Tarayici (yalnizca tarayici senaryolari icin): sistemde varsa o, yoksa pakettekı gomulu Chrome ---
find_chrome() { for c in google-chrome-stable google-chrome chromium chromium-browser chrome; do have "$c" && { command -v "$c"; return 0; }; done; return 1; }
CHROME=""; CHROME="$(find_chrome || true)"
if [ -z "$CHROME" ] && [ -x chrome/chrome-run ]; then
  say "Gomulu (tasinabilir) Chrome kuruluyor (/opt/appmon/chrome)"
  run install -d -o root -g root -m 755 /opt/appmon
  if [ "$DRY" = 0 ]; then rm -rf /opt/appmon/chrome.new; cp -a chrome /opt/appmon/chrome.new; rm -rf /opt/appmon/chrome; mv /opt/appmon/chrome.new /opt/appmon/chrome; chown -R root:root /opt/appmon/chrome; else echo "  + cp -a chrome /opt/appmon/chrome"; fi
  have restorecon && run restorecon -R /opt/appmon 2>/dev/null || true
  CHROME=/opt/appmon/chrome/chrome-run
fi
if [ -z "$CHROME" ] && ls paketler/*.deb paketler/*.rpm >/dev/null 2>&1; then
  say "Tarayici ve bagimliliklari paketler/ klasorunden kuruluyor"; ensure_local_pkgs || true; CHROME="$(find_chrome || true)"
fi
[ -n "$CHROME" ] || warn "Chrome/Chromium bulunamadi: 'tarayici senaryolari' kapali kurulacak (diger her sey calisir). Sonradan kurulursa $ENV_FILE icinde APM_BROWSER_ENABLED=true ve APM_CHROME_PATH ayarlayip 'systemctl restart appmon' yapin."

# --- Kullanici, dosyalar ---
say "Dosyalar yukleniyor ($APP_DIR)"
id "$APP_USER" >/dev/null 2>&1 || run useradd --system --home-dir "$APP_DIR" --shell /usr/sbin/nologin "$APP_USER"
run install -d -o root -g root -m 755 "$APP_DIR" "$APP_DIR/bin" "$APP_DIR/dist"
run install -d -o root -g "$APP_USER" -m 750 /etc/appmon
run install -m 755 "bin/appmon-server-linux-$GOARCH" "$APP_DIR/bin/appmon-server.new"
run mv -f "$APP_DIR/bin/appmon-server.new" "$APP_DIR/bin/appmon-server"
for a in bin/appmon-agent-linux-*; do [ -e "$a" ] && run install -m 755 "$a" "$APP_DIR/dist/$(basename "$a")"; done

ADMIN_KEY=""
if [ "$ENV_EXISTS" = 0 ]; then
  ADMIN_KEY="$(rand 24)"
  say "Ayar dosyasi: $ENV_FILE"
  if [ "$DRY" = 1 ]; then echo "  + $ENV_FILE yazilir"; else
    umask 027
    cat > "$ENV_FILE" <<ENV
APM_DB_DSN=$DB_DSN
APM_ADMIN_KEY=$ADMIN_KEY
APM_MODE=onprem
APM_ADDR=127.0.0.1:$INTERNAL_PORT
APM_ASSET_LIMIT=$ASSET_LIMIT
APM_RETENTION_DAYS=14
APM_BROWSER_ENABLED=$([ -n "$CHROME" ] && echo true || echo false)
APM_CHROME_PATH=$CHROME
APM_CHROME_NO_SANDBOX=true
ENV
    chown root:"$APP_USER" "$ENV_FILE"; chmod 640 "$ENV_FILE"
  fi
else
  say "Mevcut ayarlar korundu ($ENV_FILE)"
  INTERNAL_PORT="$(grep -E '^APM_ADDR=' "$ENV_FILE" | sed 's/.*://' || echo "$INTERNAL_PORT")"; INTERNAL_PORT="${INTERNAL_PORT:-18080}"
fi

# --- systemd ---
say "systemd servisi"
if [ "$DRY" = 1 ]; then echo "  + $UNIT yazilir"; else
cat > "$UNIT" <<UNITEOF
[Unit]
Description=APP_MON uygulama izleme
After=network-online.target postgresql.service appmon-pg.service
Wants=network-online.target

[Service]
User=$APP_USER
Group=$APP_USER
EnvironmentFile=$ENV_FILE
ExecStart=$APP_DIR/bin/appmon-server
Restart=always
RestartSec=3
LimitNOFILE=65536
# ICMP (ping) probe'u icin
AmbientCapabilities=CAP_NET_RAW
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
UNITEOF
printf 'net.ipv4.ping_group_range = 0 2147483647\n' > /etc/sysctl.d/90-appmon-ping.conf
sysctl -q -p /etc/sysctl.d/90-appmon-ping.conf >/dev/null 2>&1 || true
fi
run systemctl daemon-reload
run systemctl enable appmon
run systemctl restart appmon

# --- Apache sanal sunucu ---
say "Apache ayari ($CONF)"
LISTEN_NEEDED=1
if [ "$PORT" = 80 ] || [ "$PORT" = 443 ]; then LISTEN_NEEDED=0; fi
if grep -rqsE "^[[:space:]]*Listen[[:space:]]+(.*:)?$PORT([[:space:]]|$)" /etc/apache2 /etc/httpd 2>/dev/null; then LISTEN_NEEDED=0; fi
SN="${SERVERNAME:-_default_}"
PROTO=http; [ -n "$TLS_CERT" ] && PROTO=https
if [ "$DRY" = 1 ]; then echo "  + $CONF yazilir (port $PORT, $PROTO, ServerName ${SERVERNAME:-yok})"; else
{
  [ "$LISTEN_NEEDED" = 1 ] && echo "Listen $PORT"
  echo "<VirtualHost *:$PORT>"
  [ -n "$SERVERNAME" ] && echo "    ServerName $SERVERNAME"
  if [ -n "$TLS_CERT" ]; then
    echo "    SSLEngine on"; echo "    SSLCertificateFile $TLS_CERT"; echo "    SSLCertificateKeyFile $TLS_KEY"
    echo "    Header always set Strict-Transport-Security \"max-age=31536000\""
  fi
  cat <<VH
    ProxyRequests Off
    ProxyPreserveHost On
    RequestHeader set X-Forwarded-Proto "$PROTO"
    ProxyPass / http://127.0.0.1:$INTERNAL_PORT/ timeout=60 retry=0
    ProxyPassReverse / http://127.0.0.1:$INTERNAL_PORT/
    ErrorLog $LOGDIR/appmon-error.log
    CustomLog $LOGDIR/appmon-access.log combined
</VirtualHost>
VH
} > "$CONF"
fi
[ "$FAMILY" = debian ] && run a2ensite appmon >/dev/null

if have getenforce && [ "$(getenforce 2>/dev/null)" = Enforcing ]; then
  say "SELinux: httpd'nin yerel arka uca baglanmasina izin veriliyor"
  run setsebool -P httpd_can_network_connect 1
fi

if [ "$DRY" = 0 ]; then
  if [ "$APACHECTL" = httpd ]; then TESTCMD="httpd -t"; else TESTCMD="$APACHECTL configtest"; fi
  if ! OUT="$($TESTCMD 2>&1)"; then echo "$OUT"; die "Apache yapilandirmasi hatali ($TESTCMD basarisiz): $CONF -- yukaridaki satirlar gercek hatayi gosterir."; fi
  echo "$OUT" | tail -1
fi
run systemctl enable "$SVC"
run systemctl reload "$SVC" || run systemctl restart "$SVC"


# --- Veritabani yonetici hesabi (istege bagli) ---
if [ -n "$DB_ADMIN" ]; then
  case "$DB_ADMIN" in *[!a-zA-Z0-9_]*|"") die "--db-admin yalnizca harf, rakam ve _ icerebilir.";; esac
  say "Veritabani yonetici hesabi: $DB_ADMIN"
  [ -n "$DB_ADMIN_PASS" ] || DB_ADMIN_PASS="$(rand 12)"
  SQLADM="DO \$\$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='$DB_ADMIN') THEN CREATE ROLE \"$DB_ADMIN\" LOGIN SUPERUSER CREATEDB CREATEROLE PASSWORD '$DB_ADMIN_PASS'; ELSE ALTER ROLE \"$DB_ADMIN\" WITH LOGIN SUPERUSER CREATEDB CREATEROLE PASSWORD '$DB_ADMIN_PASS'; END IF; END \$\$;"
  if [ "$DRY" = 1 ]; then echo "  + yonetici rol olusturulur: $DB_ADMIN"
  elif [ -x "$PGHOME/usr/lib/postgresql/16/bin/psql" ] && [ -f "$PGDATA/PG_VERSION" ]; then
    pgrun "$PGHOME/usr/lib/postgresql/16/bin/psql" -h "$PGSOCK" -p "$PGPORT" -d postgres -v ON_ERROR_STOP=1 -qAtc "$SQLADM" || die "Yonetici rol olusturulamadi."
    DB_ADMIN_HOSTINFO="127.0.0.1 port $PGPORT"
  elif have psql; then
    SQLF="$(mktemp)"; printf '%s\n' "$SQLADM" > "$SQLF"; chmod 644 "$SQLF"
    su postgres -c "psql -v ON_ERROR_STOP=1 -qAt -f $SQLF" || { rm -f "$SQLF"; die "Yonetici rol olusturulamadi."; }
    rm -f "$SQLF"
    DB_ADMIN_HOSTINFO="127.0.0.1 port 5432"
  else warn "psql bulunamadi; yonetici rol olusturulamadi."; fi
fi

# --- Saglik kontrolu ---
if [ "$DRY" = 0 ]; then
  say "Kontrol ediliyor"
  OK=0
  for i in $(seq 1 30); do
    if have curl; then code="$(curl -sk -o /dev/null -w '%{http_code}' "http://127.0.0.1:$INTERNAL_PORT/" || true)"; else code=200; fi
    [ "$code" = 200 ] && { OK=1; break; }; sleep 1
  done
  [ "$OK" = 1 ] || { journalctl -u appmon -n 20 --no-pager || true; die "Servis acilmadi (yukaridaki gunluge bakin)."; }
fi

HOSTSHOW="${SERVERNAME:-$(hostname -I 2>/dev/null | awk '{print $1}')}"; HOSTSHOW="${HOSTSHOW:-sunucu-adresi}"
SUF=""; { [ "$PROTO" = http ] && [ "$PORT" != 80 ]; } || { [ "$PROTO" = https ] && [ "$PORT" != 443 ]; } && SUF=":$PORT"
echo
say "APP_MON hazir"
echo "  Adres        : $PROTO://$HOSTSHOW$SUF/"
if [ -n "$ADMIN_KEY" ]; then echo "  Giris anahtari: $ADMIN_KEY   (kaydedin; $ENV_FILE icinde de var)"; else echo "  Giris anahtari: $ENV_FILE icinde APM_ADMIN_KEY"; fi
[ -n "$DB_ADMIN" ] && [ "$DRY" = 0 ] && echo "  DB yoneticisi: kullanici=$DB_ADMIN parola=$DB_ADMIN_PASS (${DB_ADMIN_HOSTINFO:-yerel}; veritabani: appmon)"
echo "  Servis       : systemctl status appmon   |   gunluk: journalctl -u appmon -f"
echo "  Agent dosyalari: $APP_DIR/dist/"
have firewall-cmd && echo "  Guvenlik duvari acik degilse: firewall-cmd --permanent --add-port=$PORT/tcp && firewall-cmd --reload"
exit 0
