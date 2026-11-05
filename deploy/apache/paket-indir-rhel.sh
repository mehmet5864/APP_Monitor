#!/usr/bin/env bash
# INTERNETLI bir makinede calistirin (hedef sunucuyla AYNI RHEL/Rocky/Alma ana surumu: 8 veya 9).
# paketler/ klasorune su paketleri ve eksik bagimliliklarini indirir:
#   PostgreSQL sunucusu, Google Chrome (tarayici senaryolari) + kutuphaneleri, yazi tipleri,
#   curl/openssl/SELinux araclari, istege bagli mod_ssl (HTTPS icin).
#   ./paket-indir-rhel.sh 9              (hedef 9 ise)       ./paket-indir-rhel.sh 8 --ssl
#   secenekler: --ssl (mod_ssl ekle)   --no-chrome (Chrome'u paketleme)   --no-pg (PostgreSQL'i paketleme)
# Docker varsa temiz bir Rocky konteyneri, yoksa RHEL/Rocky/Alma makinede bos installroot kullanilir.
set -euo pipefail
cd "$(dirname "$0")"; V="${1:-9}"; shift || true
SSL=0; CHROME=1; PG=1
for a in "$@"; do case "$a" in --ssl) SSL=1;; --no-chrome) CHROME=0;; --no-pg) PG=0;; esac; done
HAVE_DOCKER=0; command -v docker >/dev/null 2>&1 && HAVE_DOCKER=1
if [ "$HAVE_DOCKER" = 0 ] && ! command -v dnf >/dev/null 2>&1; then echo "Docker veya dnf (RHEL/Rocky/Alma) gerekli."; exit 1; fi
mkdir -p paketler
PKGS="curl openssl policycoreutils fontconfig dejavu-sans-fonts liberation-sans-fonts liberation-mono-fonts"
[ "$PG" = 1 ] && PKGS="$PKGS postgresql-server postgresql"
[ "$SSL" = 1 ] && PKGS="$PKGS mod_ssl"
CHROME_STEP=""
if [ "$CHROME" = 1 ]; then
  ARCH="$(uname -m)"; case "$ARCH" in x86_64) CR=x86_64;; *) echo "Chrome RPM'i yalnizca x86_64 icin otomatik indirilir; --no-chrome kullanin."; exit 1;; esac
  CHROME_STEP="curl -fsSL -o /tmp/chrome.rpm https://dl.google.com/linux/direct/google-chrome-stable_current_$CR.rpm && cp /tmp/chrome.rpm /out/google-chrome-stable.rpm && PKGS=\"\$PKGS /tmp/chrome.rpm\";"
fi
if [ "$HAVE_DOCKER" = 1 ]; then
docker run --rm -v "$PWD/paketler:/out" "rockylinux:$V" bash -c "
  set -e
  PKGS='$PKGS'
  $CHROME_STEP
  dnf -y install dnf-plugins-core >/dev/null
  dnf install -y --downloadonly --downloaddir=/out --setopt=install_weak_deps=False \$PKGS
"
else
  # Docker yok: bos bir installroot'a gore coz (hedefe en yakin sonuc), indirmeler paketler/ icine
  R="$(mktemp -d)"; PKGS_L="$PKGS"
  if [ "$CHROME" = 1 ]; then curl -fsSL -o paketler/google-chrome-stable.rpm https://dl.google.com/linux/direct/google-chrome-stable_current_x86_64.rpm; PKGS_L="$PKGS_L $PWD/paketler/google-chrome-stable.rpm"; fi
  sudo dnf install -y --installroot="$R" --releasever="$V" --downloadonly --downloaddir="$PWD/paketler" --setopt=install_weak_deps=False $PKGS_L
  sudo rm -rf "$R"; sudo chown -R "$(id -u):$(id -g)" paketler
fi
echo "Paketler hazir: $(ls paketler/*.rpm | wc -l) dosya, $(du -sh paketler | cut -f1) -> paketler/"
echo "Simdi tum appmon-apache klasorunu hedef sunucuya tasiyip: sudo ./kur-apache.sh"
