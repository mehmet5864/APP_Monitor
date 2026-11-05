#!/usr/bin/env bash
# Kapali ortam (RHEL) guncelleme paketi uretir:  ./paketle-guncelleme.sh [surum]   (varsayilan: internal/version'daki surum)
# Cikti: dist/appmon-guncelleme-<surum>.tar.gz   (Go ve internet olan bir makinede calistirin)
set -euo pipefail
cd "$(dirname "$0")"
VER="${1:-$(sed -n 's/.*Version = "\(.*\)".*/\1/p' internal/version/version.go)}"
[ -n "$VER" ] || { echo "surum bulunamadi"; exit 1; }
OUT="dist/appmon-guncelleme"; rm -rf "$OUT"; mkdir -p "$OUT/bin"
LD="-s -w -X appmon/internal/version.Version=$VER"
for A in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$A go build -trimpath -ldflags="$LD" -o "$OUT/bin/appmon-server-linux-$A" ./cmd/server
  CGO_ENABLED=0 GOOS=linux GOARCH=$A go build -trimpath -ldflags="-s -w" -o "$OUT/bin/appmon-agent-linux-$A" ./cmd/agent
done
cp deploy/apache/guncelle-apache.sh "$OUT/"; chmod +x "$OUT/guncelle-apache.sh"
cp deploy/apache/OKU-GUNCELLEME.txt KULLANIM-KILAVUZU.md "$OUT/"
sed -i "s/vSURUM/$VER/" "$OUT/OKU-GUNCELLEME.txt"
echo "$VER" > "$OUT/SURUM"
(cd "$OUT" && sha256sum bin/* > SHA256SUMS)
tar -C dist -czf "dist/appmon-guncelleme-$VER.tar.gz" appmon-guncelleme
echo "Hazir: dist/appmon-guncelleme-$VER.tar.gz"; ls -la "dist/appmon-guncelleme-$VER.tar.gz"
