#!/usr/bin/env bash
# Durdurur (veriler kalir). './durdur.sh --sil' verileri de siler.
set -euo pipefail
cd "$(dirname "$0")"
DOCKER="docker"; docker info >/dev/null 2>&1 || DOCKER="sudo docker"
if [ "${1:-}" = "--sil" ]; then
  read -r -p "TUM VERILER SILINECEK. Onaylamak icin EVET yazin: " ans
  [ "$ans" = "EVET" ] || { echo "Iptal edildi."; exit 1; }
  $DOCKER compose down -v
else
  $DOCKER compose down
fi
