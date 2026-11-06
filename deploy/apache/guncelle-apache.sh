#!/usr/bin/env bash
# APP_MON - kurulu sistemi YENI SURUME guncelle (internetsiz / kapali ag). Veri ve ayarlar korunur.
#   sudo ./guncelle-apache.sh                      yedek al, yeni surumu yukle, yeniden baslat, saglik kontrolu yap
#   sudo ./guncelle-apache.sh --yedeksiz           veritabani yedegini atla (onerilmez)
#   sudo ./guncelle-apache.sh --geri-al            bir onceki ikili dosyaya don (veritabani yedegi elle geri yuklenir)
#   sudo ./guncelle-apache.sh --imza-anahtari k.pem  paket imzasi dogrulamasini ac (Ed25519 genel anahtar)
# Bu betik ayrica "arayuzden guncelleme" hizmetini kurar (systemd yol birimi); sonraki guncellemeler
# APP_MON arayuzunde Guncelleme sayfasindan paket yuklenerek yapilabilir.
# Veritabani degisiklikleri (migration) yeni surum acilirken otomatik ve yalnizca ekleyici olarak uygulanir.
set -euo pipefail
cd "$(dirname "$0")"
SELF="$(pwd)/$(basename "$0")"

APP_DIR="${APM_APP_DIR:-/opt/appmon}"; ENV_FILE="${APM_ENV_FILE:-/etc/appmon/appmon.env}"; BACKUP_DIR="${APM_BACKUP_DIR:-/var/backups/appmon}"
UPD="${APM_UPDATE_DIR:-/var/lib/appmon/updates}"; PUBKEY="${APM_PUBKEY_FILE:-/etc/appmon/update-pubkey.pem}"
SBIN="${APM_SBIN_DIR:-/usr/local/sbin}"; UNITDIR="${APM_UNIT_DIR:-/etc/systemd/system}"
OWN_APP="-o appmon -g appmon"; OWN_ROOT_APP="-o root -g appmon"; if [ "${APM_TEST:-0}" = 1 ]; then OWN_APP=""; OWN_ROOT_APP=""; fi
NOBACKUP=0; ROLLBACK=0; PKG_DIR="$(pwd)"; STATUS_FILE=""; FROM_UI=0; NEWPUB=""; REQ_RUN=""
while [ $# -gt 0 ]; do
  case "$1" in
    --yedeksiz) NOBACKUP=1; shift;;
    --geri-al) ROLLBACK=1; shift;;
    --imza-anahtari) NEWPUB="$2"; shift 2;;
    --uygula-istegi) FROM_UI=1; shift;;   # (systemd) arayuzden yuklenen paketi uygula
    -h|--help) sed -n 2,9p "$0"; exit 0;;
    *) echo "Bilinmeyen secenek: $1"; exit 2;;
  esac
done

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[UYARI]\033[0m %s\n' "$*"; }
have() { command -v "$1" >/dev/null 2>&1; }
jesc() { printf '%s' "$*" | tr -d '\000-\037' | sed 's/\\/\\\\/g; s/"/\\"/g' | cut -c1-600; }
OLDVER=""; NEWVER=""
status_write() { # $1 durum  $2 mesaj
  [ -n "$STATUS_FILE" ] || return 0
  printf '{"state":"%s","from":"%s","to":"%s","message":"%s","time":"%s"}\n' "$1" "$(jesc "$OLDVER")" "$(jesc "$NEWVER")" "$(jesc "${2:-}")" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$STATUS_FILE.tmp"
  chmod 644 "$STATUS_FILE.tmp"; mv -f "$STATUS_FILE.tmp" "$STATUS_FILE"
}
cleanup() { [ -n "$REQ_RUN" ] && rm -f "$REQ_RUN" || true; }
trap cleanup EXIT
die() { printf '\033[1;31m[HATA]\033[0m %s\n' "$*" >&2; status_write failed "$*"; exit 1; }

# --- Arayuzden istek: yalnizca systemd'nin root hizmeti calistirir ---
if [ "$FROM_UI" = 1 ]; then
  [ "$(id -u)" -eq 0 ] || { echo "root gerekli"; exit 1; }
  REQ="$UPD/apply.json"; [ -f "$REQ" ] || exit 0
  REQ_RUN="$REQ.run"; mv -f "$REQ" "$REQ_RUN"       # yol birimi tekrar tetiklenmesin
  ID="$(sed -n 's/.*"id": *"\([0-9a-f]\{16\}\)".*/\1/p' "$REQ_RUN" | head -1)"
  STATUS_FILE="$UPD/status.json"
  [ -n "$ID" ] && [ -d "$UPD/staged-$ID" ] || die "Gecersiz guncelleme istegi veya hazirlanmis paket yok."
  PKG_DIR="$UPD/staged-$ID"; cd "$PKG_DIR"
  NEWVER="$(cat SURUM 2>/dev/null || true)"
  status_write running "Guncelleme basladi"
fi

[ "${APM_TEST:-0}" = 1 ] || [ "$(id -u)" -eq 0 ] || die "root olarak calistirin: sudo ./guncelle-apache.sh"
[ -x "$APP_DIR/bin/appmon-server" ] || die "APP_MON kurulu degil ($APP_DIR/bin/appmon-server yok). Ilk kurulum icin kur-apache.sh kullanin."
[ -f "$ENV_FILE" ] || die "$ENV_FILE bulunamadi; bu bir guncelleme paketidir, ilk kurulum icin tam paketi kullanin."
have systemctl || die "systemd gerekli."

PORT="$(grep -E '^APM_ADDR=' "$ENV_FILE" | sed 's/.*://' | tr -d '[:space:]' || true)"; PORT="${PORT:-18080}"
health() { if have curl; then curl -s --max-time 3 "http://127.0.0.1:$PORT/healthz" 2>/dev/null || true; else systemctl is-active --quiet appmon && echo '{"status":"ok"}' || true; fi; }
wait_health() { local i out; for i in $(seq 1 "${APM_WAIT:-40}"); do out="$(health)"; case "$out" in *'"status":"ok"'*) echo "$out"; return 0;; esac; sleep 1; done; return 1; }

if [ "$ROLLBACK" = 1 ]; then
  [ -x "$APP_DIR/bin/appmon-server.prev" ] || die "Onceki surum bulunamadi ($APP_DIR/bin/appmon-server.prev)."
  say "Onceki surume donuluyor"
  mv -f "$APP_DIR/bin/appmon-server.prev" "$APP_DIR/bin/appmon-server"
  systemctl restart appmon
  wait_health >/dev/null || { journalctl -u appmon -n 20 --no-pager || true; die "Servis acilmadi."; }
  say "Geri donuldu. (Veritabani degismedi; gerekirse $BACKUP_DIR icindeki yedegi elle geri yukleyin.)"; exit 0
fi

# --- Imza anahtari (istege bagli) ---
if [ -n "$NEWPUB" ]; then
  [ -f "$NEWPUB" ] || die "Anahtar dosyasi bulunamadi: $NEWPUB"
  grep -q 'BEGIN PUBLIC KEY' "$NEWPUB" || die "$NEWPUB bir PEM genel anahtar degil."
  install -d -m 750 $OWN_ROOT_APP "$(dirname "$PUBKEY")" 2>/dev/null || true
  install -m 640 $OWN_ROOT_APP "$NEWPUB" "$PUBKEY"
  say "Paket imza dogrulamasi acildi ($PUBKEY)"
fi

ARCH="$(uname -m)"; case "$ARCH" in x86_64|amd64) GOARCH=amd64;; aarch64|arm64) GOARCH=arm64;; *) die "Desteklenmeyen islemci: $ARCH";; esac
NEW="bin/appmon-server-linux-$GOARCH"
[ -f "$NEW" ] || die "$NEW bulunamadi (paket eksik/bozuk)."
[ "$(head -c4 "$NEW" | od -An -c | tr -d ' ')" = '177ELF' ] || die "$NEW gecerli bir Linux ikili dosyasi degil (aktarim sirasinda bozulmus olabilir)."
if [ -f SHA256SUMS ] && have sha256sum; then
  sha256sum -c SHA256SUMS --quiet || die "SHA256SUMS dogrulamasi basarisiz: paket bozuk. Dosyayi yeniden aktarin."
  say "Paket butunlugu dogrulandi (SHA256)"
fi
if [ -f "$PUBKEY" ]; then
  have openssl || die "Imza dogrulamasi icin openssl gerekli."
  [ -f manifest.json ] && [ -f manifest.sig ] || die "Bu kurulum yalnizca imzali paketleri kabul eder (manifest.sig yok)."
  openssl pkeyutl -verify -pubin -inkey "$PUBKEY" -rawin -in manifest.json -sigfile manifest.sig >/dev/null 2>&1 || die "Paket imzasi gecersiz: paket degistirilmis veya farkli anahtarla imzalanmis."
  say "Paket imzasi dogrulandi"
fi
[ -n "$NEWVER" ] || NEWVER="$(cat SURUM 2>/dev/null || echo bilinmiyor)"
OLDVER="$(health | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"; OLDVER="${OLDVER:-eski (surum bilgisi yok)}"
say "Guncelleme: $OLDVER -> $NEWVER"
status_write running "Yedek aliniyor"

# --- 1) Veritabani yedegi ---
OUT=""
if [ "$NOBACKUP" = 0 ]; then
  say "Veritabani yedegi aliniyor ($BACKUP_DIR)"
  install -d -m 700 "$BACKUP_DIR"
  OUT="$BACKUP_DIR/appmon-$(date +%Y%m%d-%H%M%S).sql.gz"
  PGBIN="${APM_PGBIN:-/opt/appmon/pg/usr/lib/postgresql/16/bin}"
  DSN="$(grep -E '^APM_DB_DSN=' "$ENV_FILE" | cut -d= -f2-)"
  umask 077
  if [ -x "$PGBIN/pg_dump" ] && [ -d /run/appmon-pg ] && have runuser; then
    runuser -u appmon-pg -- env LD_LIBRARY_PATH=/opt/appmon/pg/lib PATH=/usr/bin:/bin "$PGBIN/pg_dump" -h /run/appmon-pg -p 5433 appmon | gzip > "$OUT.tmp" || { rm -f "$OUT.tmp"; die "Yedek alinamadi. Yedeksiz devam etmek icin --yedeksiz kullanin."; }
  elif have pg_dump; then
    pg_dump "$DSN" | gzip > "$OUT.tmp" || { rm -f "$OUT.tmp"; die "Yedek alinamadi. Yedeksiz devam etmek icin --yedeksiz kullanin."; }
  else
    die "pg_dump bulunamadi; yedek alinamadi. Elle yedekleyin ve --yedeksiz ile tekrar deneyin."
  fi
  [ -s "$OUT.tmp" ] || { rm -f "$OUT.tmp"; die "Yedek dosyasi bos."; }
  mv "$OUT.tmp" "$OUT"
  echo "  Yedek: $OUT ($(du -h "$OUT" | cut -f1))"
  ls -1t "$BACKUP_DIR"/appmon-*.sql.gz 2>/dev/null | tail -n +8 | xargs -r rm -f   # son 7 yedek kalsin
else
  warn "Veritabani yedegi atlandi (--yedeksiz)."
fi

# --- 2) Arayuzden guncelleme hizmetini kur (yalniz elle calistirmada) ---
install_updater() {
  say "Arayuzden guncelleme hizmeti kuruluyor"
  install -d -m 750 $OWN_APP "$UPD"
  install -d -m 755 "$SBIN"
  install -m 755 "$SELF" "$SBIN/appmon-guncelle"
  cat > "$UNITDIR/appmon-update.service" <<UNIT
[Unit]
Description=APP_MON arayuzden yuklenen guncellemeyi uygula

[Service]
Type=oneshot
ExecStart=$SBIN/appmon-guncelle --uygula-istegi
TimeoutStartSec=900
UNIT
  cat > "$UNITDIR/appmon-update.path" <<UNIT
[Unit]
Description=APP_MON guncelleme istegi izleyici

[Path]
PathExists=$UPD/apply.json
Unit=appmon-update.service

[Install]
WantedBy=multi-user.target
UNIT
  grep -q '^APM_UPDATE_DIR=' "$ENV_FILE" || echo "APM_UPDATE_DIR=$UPD" >> "$ENV_FILE"
  if [ -f "$PUBKEY" ] && ! grep -q '^APM_UPDATE_PUBKEY=' "$ENV_FILE"; then echo "APM_UPDATE_PUBKEY=$PUBKEY" >> "$ENV_FILE"; fi
  : > "$UPD/READY"; [ -z "$OWN_APP" ] || chown appmon:appmon "$UPD/READY"
  rm -f "$UPD/apply.json" "$UPD/apply.json.run"
  systemctl daemon-reload
  systemctl enable --now appmon-update.path >/dev/null 2>&1 || warn "appmon-update.path etkinlestirilemedi"
  have restorecon && restorecon -R "$SBIN/appmon-guncelle" "$UPD" 2>/dev/null || true
}
[ "$FROM_UI" = 1 ] || install_updater

# --- 3) Ikili dosyalari degistir ---
say "Yeni surum yukleniyor"
status_write running "Yeni surum yukleniyor"
install -d -m 755 "$APP_DIR/bin" "$APP_DIR/dist"
cp -p "$APP_DIR/bin/appmon-server" "$APP_DIR/bin/appmon-server.prev"
install -m 755 "$NEW" "$APP_DIR/bin/appmon-server.new"
mv -f "$APP_DIR/bin/appmon-server.new" "$APP_DIR/bin/appmon-server"
for a in bin/appmon-agent-linux-*; do [ -e "$a" ] && install -m 755 "$a" "$APP_DIR/dist/$(basename "$a")"; done
have restorecon && restorecon -R "$APP_DIR" 2>/dev/null || true

# --- 4) Yeniden baslat ve dogrula ---
say "Servis yeniden baslatiliyor"
status_write running "Servis yeniden baslatiliyor"
systemctl restart appmon
if RES="$(wait_health)"; then
  VER="$(printf '%s' "$RES" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
  say "Guncelleme tamam. Calisan surum: ${VER:-$NEWVER}"
  echo "  Eski surume donmek icin: sudo appmon-guncelle --geri-al   (veya paketteki betikle ./guncelle-apache.sh --geri-al)"
  echo "  Gunluk: journalctl -u appmon -f"
  NEWVER="${VER:-$NEWVER}"
  status_write ok "Guncelleme tamamlandi${OUT:+; yedek: $OUT}"
  if [ "$FROM_UI" = 1 ]; then cd /; rm -rf "$PKG_DIR"; fi
else
  warn "Yeni surum saglik kontrolunden gecemedi; onceki surume donuluyor."
  journalctl -u appmon -n 25 --no-pager 2>/dev/null || true
  mv -f "$APP_DIR/bin/appmon-server.prev" "$APP_DIR/bin/appmon-server"
  systemctl restart appmon
  if wait_health >/dev/null; then
    status_write rolled_back "Yeni surum acilmadi; onceki surum geri yuklendi ve calisiyor. Gunluk: journalctl -u appmon"
    printf '\033[1;31m[HATA]\033[0m Guncelleme basarisiz, ONCEKI SURUM geri yuklendi ve calisiyor. Yukaridaki gunluge bakin.\n' >&2
    exit 1
  else
    die "Guncelleme basarisiz ve onceki surum de acilmadi. Veritabani yedegi: ${OUT:-yok}. journalctl -u appmon -n 50"
  fi
fi
