#!/usr/bin/env bash
# Kapali ortam (RHEL) guncelleme paketi uretir (arayuzden yuklenebilir + elle uygulanabilir).
#   ./paketle-guncelleme.sh [surum] [--imzala ozel-anahtar.pem] [--not "surum notu"]
#   ./paketle-guncelleme.sh --anahtar-uret [klasor]     Ed25519 imza anahtar cifti uretir (varsayilan: ./imza)
# Cikti: dist/appmon-guncelleme-<surum>.tar.gz   (Go ve openssl olan bir makinede calistirin)
# Imza: kurulumda genel anahtar tanitilirsa (guncelle-apache.sh --imza-anahtari genel.pem) yalnizca imzali paketler kabul edilir.
set -euo pipefail
cd "$(dirname "$0")"
if [ "${1:-}" = "--anahtar-uret" ]; then
  D="${2:-imza}"; mkdir -p "$D"; chmod 700 "$D"
  [ ! -e "$D/ozel-anahtar.pem" ] || { echo "$D/ozel-anahtar.pem zaten var; uzerine yazilmadi."; exit 1; }
  openssl genpkey -algorithm ed25519 -out "$D/ozel-anahtar.pem"; chmod 600 "$D/ozel-anahtar.pem"
  openssl pkey -in "$D/ozel-anahtar.pem" -pubout -out "$D/genel-anahtar.pem"
  echo "Uretildi: $D/ozel-anahtar.pem (GIZLI: yalnizca paketleme makinesinde kalsin, kimseyle paylasmayin, yedekleyin)"
  echo "          $D/genel-anahtar.pem (sunucuya tanitilir: sudo ./guncelle-apache.sh --imza-anahtari genel-anahtar.pem)"
  exit 0
fi
VER=""; SIGN="${APM_SIGN_KEY:-}"; NOTE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --imzala) SIGN="$2"; shift 2;;
    --not) NOTE="$2"; shift 2;;
    *) VER="$1"; shift;;
  esac
done
VER="${VER:-$(sed -n 's/.*Version = "\(.*\)".*/\1/p' internal/version/version.go)}"
echo "$VER" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' || { echo "surum vANA.ARA.YAMA biciminde olmali (ornek v1.0.2): $VER"; exit 1; }
[ -z "$SIGN" ] || [ -f "$SIGN" ] || { echo "imza anahtari bulunamadi: $SIGN"; exit 1; }
OUT="dist/appmon-guncelleme"; rm -rf "$OUT"; mkdir -p "$OUT/bin"
LD="-s -w -X appmon/internal/version.Version=$VER"
for A in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$A go build -trimpath -ldflags="$LD" -o "$OUT/bin/appmon-server-linux-$A" ./cmd/server
  CGO_ENABLED=0 GOOS=linux GOARCH=$A go build -trimpath -ldflags="-s -w" -o "$OUT/bin/appmon-agent-linux-$A" ./cmd/agent
done
cp deploy/apache/guncelle-apache.sh "$OUT/"; chmod +x "$OUT/guncelle-apache.sh"
cp deploy/apache/OKU-GUNCELLEME.txt KULLANIM-KILAVUZU.md "$OUT/"
echo "$VER" > "$OUT/SURUM"
(cd "$OUT" && sha256sum bin/* > SHA256SUMS)
# manifest.json (arayuzden yukleme icin): surum + dosya sagiama toplamlari
esc() { printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g' | tr -d '\n\r'; }
{
  printf '{"format":1,"product":"appmon","version":"%s","created":"%s","notes":"%s","files":{' "$VER" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(esc "$NOTE")"
  first=1
  while read -r sum file; do
    [ "$first" = 1 ] || printf ','; first=0
    printf '"%s":"%s"' "$file" "$sum"
  done < "$OUT/SHA256SUMS"
  printf '}}'
} > "$OUT/manifest.json"
if [ -n "$SIGN" ]; then
  openssl pkeyutl -sign -inkey "$SIGN" -rawin -in "$OUT/manifest.json" -out "$OUT/manifest.sig"
  echo "Paket IMZALANDI ($SIGN)"
else
  echo "UYARI: paket imzasiz (sunucuda imza dogrulamasi aciksa kabul edilmez). Imzalamak icin --imzala ozel-anahtar.pem"
fi
sed -i "s/vSURUM/$VER/" "$OUT/OKU-GUNCELLEME.txt"
tar -C dist -czf "dist/appmon-guncelleme-$VER.tar.gz" appmon-guncelleme
echo "Hazir: dist/appmon-guncelleme-$VER.tar.gz"; ls -la "dist/appmon-guncelleme-$VER.tar.gz"
