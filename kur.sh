#!/usr/bin/env bash
# APP_MON kurulum (Linux): Docker'i denetler (gerekirse kurar), .env'yi rastgele parolalarla olusturur, sistemi baslatir.
# Kullanim: ./kur.sh            kur ve baslat
#           ./kur.sh --env-only sadece .env olustur
set -euo pipefail
cd "$(dirname "$0")"

rand() { if command -v openssl >/dev/null 2>&1; then openssl rand -hex "$1"; else head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; fi; }

make_env() {
  if [ -f .env ]; then echo ".env zaten var, degistirilmedi."; return; fi
  umask 077
  cat > .env <<ENV
POSTGRES_PASSWORD=$(rand 16)
APM_ADMIN_KEY=$(rand 24)
APM_MODE=onprem
APM_ASSET_LIMIT=25
APM_PORT=8080
APM_RETENTION_DAYS=14
APM_BROWSER_ENABLED=true
ENV
  chmod 600 .env
  echo ".env olusturuldu (rastgele parola ve giris anahtari)."
}

if [ "${1:-}" = "--env-only" ]; then make_env; exit 0; fi

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker bulunamadi."
  read -r -p "Docker resmi betikle (get.docker.com) kurulsun mu? [e/H] " ans
  if [ "${ans:-H}" = "e" ] || [ "${ans:-H}" = "E" ]; then
    curl -fsSL https://get.docker.com | sh
    [ "$(id -u)" -ne 0 ] && sudo usermod -aG docker "$USER" && echo "Not: grubun gecerli olmasi icin oturumu kapatip acin; o zamana kadar sudo kullanilir."
  else
    echo "Docker'i kurup tekrar calistirin: https://docs.docker.com/engine/install/"; exit 1
  fi
fi

DOCKER="docker"
if ! docker info >/dev/null 2>&1; then
  if sudo -n true 2>/dev/null || [ -t 0 ]; then DOCKER="sudo docker"; fi
  $DOCKER info >/dev/null 2>&1 || { echo "[HATA] Docker calismiyor (sudo systemctl start docker)."; exit 1; }
fi
$DOCKER compose version >/dev/null 2>&1 || { echo "[HATA] 'docker compose' eklentisi yok. docker-compose-plugin paketini kurun."; exit 1; }

make_env
PORT="$(grep -E '^APM_PORT=' .env | cut -d= -f2 || true)"; PORT="${PORT:-8080}"

echo "APP_MON baslatiliyor (ilk seferde imaj derlenir, birkac dakika surebilir)..."
$DOCKER compose up -d --build

echo -n "Sunucunun hazir olmasi bekleniyor"
for _ in $(seq 1 40); do
  if curl -fs "http://localhost:${PORT}/healthz" >/dev/null 2>&1 || wget -qO- "http://localhost:${PORT}/healthz" >/dev/null 2>&1; then
    echo; echo "APP_MON hazir:  http://localhost:${PORT}"
    echo "Giris anahtari: $(grep -E '^APM_ADMIN_KEY=' .env | cut -d= -f2)   (.env dosyasinda)"
    exit 0
  fi
  echo -n "."; sleep 2
done
echo; echo "[UYARI] 80 saniyede hazir olmadi. Loglar:  $DOCKER compose logs server"; exit 1
