#!/usr/bin/env bash
# Yeni kodu derleyip yeniden baslatir. Veriler korunur, yeni migration'lar otomatik uygulanir.
set -euo pipefail
cd "$(dirname "$0")"
DOCKER="docker"; docker info >/dev/null 2>&1 || DOCKER="sudo docker"
$DOCKER compose up -d --build
echo "Guncellendi."
