#!/usr/bin/env bash
# APP_MON - kurulu sistemi YENI SURUME guncelle (internetsiz / kapali ag). Veri ve ayarlar korunur.
#   sudo ./guncelle-apache.sh              yedek al, yeni surumu yukle, yeniden baslat, saglik kontrolu yap
#   sudo ./guncelle-apache.sh --yedeksiz   veritabani yedegini atla (onerilmez)
#   sudo ./guncelle-apache.sh --geri-al    bir onceki ikili dosyaya don (veritabani yedegi elle geri yuklenir)
# Veritabani degisiklikleri (migration) yeni surum acilirken otomatik ve yalnizca ekleyici olarak uygulanir.
set -euo pipefail
cd "$(dirname "$0")"

APP_DIR="${APM_APP_DIR:-/opt/appmon}"; ENV_FILE="${APM_ENV_FILE:-/etc/appmon/appmon.env}"; BACKUP_DIR="${APM_BACKUP_DIR:-/var/backups/appmon}"
NOBACKUP=0; ROLLBACK=0
for a in "$@"; do
  case "$a" in
    --yedeksiz) NOBACKUP=1;;
    --geri-al) ROLLBACK=1;;
    -h|--help) sed -n 2,6p "$0"; exit 0;;
    *) echo "Bilinmeyen secenek: $a"; exit 2;;
  esac
done

say()  { printf '\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[UYARI]\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31m[HATA]\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

[ "${APM_TEST:-0}" = 1 ] || [ "$(id -u)" -eq 0 ] || die "root olarak calistirin: sudo ./guncelle-apache.sh"
[ -x "$APP_DIR/bin/appmon-server" ] || die "APP_MON kurulu degil ($APP_DIR/bin/appmon-server yok). Ilk kurulum icin kur-apache.sh kullanin."
[ -f "$ENV_FILE" ] || die "$ENV_FILE bulunamadi; bu bir guncelleme paketidir, ilk kurulum icin tam paketi kullanin."
have systemctl || die "systemd gerekli."

PORT="$(grep -E '^APM_ADDR=' "$ENV_FILE" | sed 's/.*://' | tr -d '[:space:]' || true)"; PORT="${PORT:-18080}"
health() { if have curl; then curl -s --max-time 3 "http://127.0.0.1:$PORT/healthz" 2>/dev/null || true; else systemctl is-active --quiet appmon && echo '{"status":"ok"}' || true; fi; }
wait_health() { local i out; for i in $(seq 1 40); do out="$(health)"; case "$out" in *'"status":"ok"'*) echo "$out"; return 0;; esac; sleep 1; done; return 1; }

if [ "$ROLLBACK" = 1 ]; then
  [ -x "$APP_DIR/bin/appmon-server.prev" ] || die "Onceki surum bulunamadi ($APP_DIR/bin/appmon-server.prev)."
  say "Onceki surume donuluyor"
  mv -f "$APP_DIR/bin/appmon-server.prev" "$APP_DIR/bin/appmon-server"
  systemctl restart appmon
  wait_health >/dev/null || { journalctl -u appmon -n 20 --no-pager || true; die "Servis acilmadi."; }
  say "Geri donuldu. (Veritabani degismedi; gerekirse $BACKUP_DIR icindeki yedegi elle geri yukleyin.)"; exit 0
fi

ARCH="$(uname -m)"; case "$ARCH" in x86_64|amd64) GOARCH=amd64;; aarch64|arm64) GOARCH=arm64;; *) die "Desteklenmeyen islemci: $ARCH";; esac
NEW="bin/appmon-server-linux-$GOARCH"
[ -f "$NEW" ] || die "$NEW bulunamadi (paket eksik/bozuk)."
[ "$(head -c4 "$NEW" | od -An -c | tr -d ' ')" = '177ELF' ] || die "$NEW gecerli bir Linux ikili dosyasi degil (aktarim sirasinda bozulmus olabilir)."
if [ -f SHA256SUMS ] && have sha256sum; then
  sha256sum -c SHA256SUMS --quiet || die "SHA256SUMS dogrulamasi basarisiz: paket bozuk. Dosyayi yeniden aktarin."
  say "Paket butunlugu dogrulandi (SHA256)"
fi
NEWVER="$(cat SURUM 2>/dev/null || echo bilinmiyor)"
OLDVER="$(health | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"; OLDVER="${OLDVER:-eski (surum bilgisi yok)}"
say "Guncelleme: $OLDVER -> $NEWVER"

# --- 1) Veritabani yedegi ---
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
  # son 7 yedek kalsin
  ls -1t "$BACKUP_DIR"/appmon-*.sql.gz 2>/dev/null | tail -n +8 | xargs -r rm -f
else
  warn "Veritabani yedegi atlandi (--yedeksiz)."
fi

# --- 2) Ikili dosyalari degistir ---
say "Yeni surum yukleniyor"
install -d -m 755 "$APP_DIR/bin" "$APP_DIR/dist"
cp -p "$APP_DIR/bin/appmon-server" "$APP_DIR/bin/appmon-server.prev"
install -m 755 "$NEW" "$APP_DIR/bin/appmon-server.new"
mv -f "$APP_DIR/bin/appmon-server.new" "$APP_DIR/bin/appmon-server"
for a in bin/appmon-agent-linux-*; do [ -e "$a" ] && install -m 755 "$a" "$APP_DIR/dist/$(basename "$a")"; done
have restorecon && restorecon -R "$APP_DIR" 2>/dev/null || true

# --- 3) Yeniden baslat ve dogrula ---
say "Servis yeniden baslatiliyor"
systemctl restart appmon
if RES="$(wait_health)"; then
  VER="$(printf '%s' "$RES" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
  say "Guncelleme tamam. Calisan surum: ${VER:-$NEWVER}"
  echo "  Eski surume donmek icin: sudo ./guncelle-apache.sh --geri-al"
  echo "  Gunluk: journalctl -u appmon -f"
else
  warn "Yeni surum saglik kontrolunden gecemedi; onceki surume donuluyor."
  journalctl -u appmon -n 25 --no-pager 2>/dev/null || true
  mv -f "$APP_DIR/bin/appmon-server.prev" "$APP_DIR/bin/appmon-server"
  systemctl restart appmon
  wait_health >/dev/null && die "Guncelleme basarisiz, ONCEKI SURUM geri yuklendi ve calisiyor. Yukaridaki gunluge bakin." \
    || die "Guncelleme basarisiz ve onceki surum de acilmadi. Veritabani yedegi: ${OUT:-yok}. journalctl -u appmon -n 50"
fi
