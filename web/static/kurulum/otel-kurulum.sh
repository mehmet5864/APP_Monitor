#!/usr/bin/env bash
# APP_MON - Linux icin OpenTelemetry (dagitik izleme) kurulum betigi
#
# Ne yapar:
#   1) APP_MON'a baglantiyi ve anahtari dener, gercek bir deneme trace'i gonderir (uygulamaya dokunmaz).
#   2) Istenirse bir systemd servisine OpenTelemetry'yi ekler (ortam degiskenleri + dile gore ajan/paket kurulumu).
#   3) --undo ile eklenenleri geri alir.
#
# Ornekler:
#   ./otel-kurulum.sh --service siparis --key ANAHTAR                       # yalnizca baglanti + deneme trace'i
#   (bu makinede APP_MON ajani >= 1.2.0 varsa --key gerekmez: --via-agent otomatik secilir)
#   sudo ./otel-kurulum.sh --service siparis --key ANAHTAR --unit siparis   # systemd servisini yapilandir
#   sudo ./otel-kurulum.sh --service siparis --key ANAHTAR --unit siparis --restart
#   sudo ./otel-kurulum.sh --service siparis --unit siparis --undo
#   ./otel-kurulum.sh --service siparis --key ANAHTAR --env-file /etc/siparis.otel.env   # docker/baska baslatici icin
#
# Betik APP_MON sunucusundan indirildiginde sunucu adresi hazir gomuludur.

set -euo pipefail

SERVER="${APPMON_URL:-__APPMON_URL__}"
KEY="${APPMON_KEY:-}"
SERVICE=""
UNIT=""
LANG_SEL="auto"
SAMPLE=""
JAR=""
PY="python3"
ENVFILE=""
RESTART=0
UNDO=0
DRY=0
NOTEST=0
VIAAGENT=0
AGENT_URL="${APPMON_AGENT_URL:-http://127.0.0.1:4318}"
INSECURE=0
ROOT="${APPMON_ROOT:-}"          # yalnizca test icin: dosya yollarina onek
HOMEDIR="${APPMON_OTEL_DIR:-/opt/appmon-otel}"

usage() { sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }
say()  { printf '%s\n' "$*"; }
warn() { printf 'UYARI: %s\n' "$*" >&2; }
die()  { printf 'HATA: %s\n' "$*" >&2; exit 1; }
run()  { if [ "$DRY" = 1 ]; then printf '[kuru calisma] %s\n' "$*"; else "$@"; fi; }

while [ $# -gt 0 ]; do
  case "$1" in
    --server) SERVER="$2"; shift 2;;
    --key) KEY="$2"; shift 2;;
    --service) SERVICE="$2"; shift 2;;
    --unit) UNIT="$2"; shift 2;;
    --lang) LANG_SEL="$2"; shift 2;;
    --sample) SAMPLE="$2"; shift 2;;
    --jar) JAR="$2"; shift 2;;
    --python) PY="$2"; shift 2;;
    --env-file) ENVFILE="$2"; shift 2;;
    --restart) RESTART=1; shift;;
    --undo) UNDO=1; shift;;
    --dry-run) DRY=1; shift;;
    --no-test) NOTEST=1; shift;;
    --via-agent) VIAAGENT=1; shift;;
    --insecure) INSECURE=1; shift;;
    -h|--help) usage 0;;
    *) die "bilinmeyen secenek: $1 (yardim: --help)";;
  esac
done

SERVER="${SERVER%/}"
# Ajan (>= 1.2.0) bu makinede yerel OTLP alicisi acar: anahtar gerekmez, ajan sunucuya iletir.
if [ "$VIAAGENT" = 0 ] && [ -z "$KEY" ] && command -v curl >/dev/null 2>&1 && curl -sS --max-time 2 "$AGENT_URL/healthz" 2>/dev/null | grep -q appmon-agent; then
  VIAAGENT=1; say "Yerel APP_MON ajani bulundu ($AGENT_URL): trace'ler ajan uzerinden gonderilecek, anahtar gerekmiyor."
fi
if [ "$VIAAGENT" = 1 ]; then OTLP="$AGENT_URL"; else
  case "$SERVER" in http://*|https://*) ;; *) die "gecerli bir sunucu adresi gerekli: --server https://appmon.ornek.local";; esac
  OTLP="$SERVER"
fi
[ -n "$SERVICE" ] || die "--service (APP_MON'da gorunecek servis adi) gerekli"
printf '%s' "$SERVICE" | grep -Eq '^[A-Za-z0-9._-]{1,100}$' || die "servis adi yalnizca harf, rakam, nokta, tire ve alt cizgi icermeli"
[ -n "$UNIT" ] && { printf '%s' "$UNIT" | grep -Eq '^[A-Za-z0-9@._:-]{1,200}$' || die "gecersiz systemd birim adi"; }
if [ -n "$SAMPLE" ]; then
  awk -v s="$SAMPLE" 'BEGIN{ exit !(s ~ /^(0(\.[0-9]+)?|1(\.0+)?)$/) }' || die "--sample 0 ile 1 arasinda olmali (ornek 0.1)"
fi
case "$LANG_SEL" in auto|java|node|python|dotnet|none) ;; *) die "--lang auto|java|node|python|dotnet|none olmali";; esac
CURL_OPTS="-sS --max-time 20"; [ "$INSECURE" = 1 ] && CURL_OPTS="$CURL_OPTS -k"

DROPIN_DIR=""; DROPIN=""
if [ -n "$UNIT" ]; then
  U="${UNIT%.service}"
  DROPIN_DIR="$ROOT/etc/systemd/system/$U.service.d"
  DROPIN="$DROPIN_DIR/appmon-otel.conf"
fi

# ---------- geri alma ----------
if [ "$UNDO" = 1 ]; then
  [ -n "$UNIT" ] || [ -n "$ENVFILE" ] || die "--undo icin --unit veya --env-file verin"
  if [ -n "$UNIT" ]; then
    if [ -f "$DROPIN" ]; then
      run rm -f "$DROPIN"; run rmdir "$DROPIN_DIR" 2>/dev/null || true
      [ -n "$ROOT" ] || run systemctl daemon-reload
      say "OpenTelemetry ayarlari kaldirildi: $UNIT. Etkili olmasi icin servisi yeniden baslatin: systemctl restart $UNIT"
      [ "$RESTART" = 1 ] && [ -z "$ROOT" ] && run systemctl restart "$UNIT"
    else
      say "Kaldirilacak ayar bulunamadi ($DROPIN)."
    fi
  fi
  if [ -n "$ENVFILE" ]; then run rm -f "$ENVFILE"; say "Ortam dosyasi silindi: $ENVFILE"; fi
  exit 0
fi

# ---------- baglanti ve deneme trace'i ----------
hex() { head -c "$1" /dev/urandom | od -An -vtx1 | tr -d ' \n'; }
test_connection() {
  command -v curl >/dev/null 2>&1 || { warn "curl yok; baglanti denemesi atlandi"; return 0; }
  [ "$VIAAGENT" = 1 ] || [ -n "$KEY" ] || die "--key (APP_MON'dan olusturdugunuz agent anahtari) gerekli (veya APPMON_KEY ortam degiskeni)"
  say "APP_MON baglantisi deneniyor: $OTLP/v1/traces"
  local code
  local -a HDR=(); [ "$VIAAGENT" = 1 ] || HDR=(-H "X-API-Key: $KEY")
  # shellcheck disable=SC2086
  code=$(curl $CURL_OPTS -o /dev/null -w '%{http_code}' -X POST "$OTLP/v1/traces" ${HDR[@]+"${HDR[@]}"} -H 'Content-Type: application/json' -d '{"resourceSpans":[]}' 2>/tmp/appmon-otel.err) || {
    cat /tmp/appmon-otel.err >&2 2>/dev/null; die "sunucuya ulasilamadi. Adres, port, guvenlik duvari ve (kendi imzali sertifikada) --insecure secenegini kontrol edin"; }
  case "$code" in
    200) say "  baglanti ve anahtar dogru (HTTP 200)";;
    401|403) die "anahtar reddedildi (HTTP $code): anahtarin rolu 'agent' olmali ve dogru kopyalanmali";;
    *) die "beklenmeyen yanit: HTTP $code";;
  esac
  local now tid sid body
  now=$(date +%s%N); tid=$(hex 16); sid=$(hex 8)
  body=$(printf '{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"%s"}}]},"scopeSpans":[{"spans":[{"traceId":"%s","spanId":"%s","name":"appmon-kurulum-denemesi","kind":2,"startTimeUnixNano":"%s","endTimeUnixNano":"%s","attributes":[{"key":"appmon.test","value":{"stringValue":"true"}}]}]}]}]}' \
    "$SERVICE" "$tid" "$sid" "$((now - 50000000))" "$now")
  # shellcheck disable=SC2086
  code=$(curl $CURL_OPTS -o /dev/null -w '%{http_code}' -X POST "$OTLP/v1/traces" ${HDR[@]+"${HDR[@]}"} -H 'Content-Type: application/json' -d "$body") || die "deneme trace'i gonderilemedi"
  [ "$code" = 200 ] || die "deneme trace'i reddedildi (HTTP $code); lisans asset siniri dolmus olabilir"
  say "  deneme trace'i gonderildi. APP_MON > Izleme > Dagitik izleme sayfasinda '$SERVICE' servisini goreceksiniz."
}
[ "$NOTEST" = 1 ] || test_connection

# ---------- ortam degiskenleri ----------
declare -a ENVV=()
add() { ENVV+=("$1=$2"); }
add OTEL_SERVICE_NAME "$SERVICE"
add OTEL_EXPORTER_OTLP_TRACES_ENDPOINT "$OTLP/v1/traces"
add OTEL_EXPORTER_OTLP_TRACES_PROTOCOL "http/protobuf"
[ "$VIAAGENT" = 1 ] || add OTEL_EXPORTER_OTLP_HEADERS "X-API-Key=$KEY"
add OTEL_METRICS_EXPORTER "none"
add OTEL_LOGS_EXPORTER "none"
if [ -n "$SAMPLE" ]; then add OTEL_TRACES_SAMPLER "parentbased_traceidratio"; add OTEL_TRACES_SAMPLER_ARG "$SAMPLE"; fi

if [ -z "$UNIT" ] && [ -z "$ENVFILE" ]; then
  say ""
  say "Uygulamaniza su ortam degiskenlerini verin (dile ozel ajan/paket icin --unit veya --lang kullanin):"
  for e in "${ENVV[@]}"; do say "  $e"; done
  say ""
  say "Servisi otomatik yapilandirmak icin: sudo $0 --service $SERVICE --key ... --unit <systemd-birimi>"
  exit 0
fi

# ---------- dil algilama ----------
detect_lang() {
  local pid exe cmd
  pid=$(systemctl show -p MainPID --value "$UNIT" 2>/dev/null || echo 0)
  [ "${pid:-0}" != 0 ] || return 1
  exe=$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)
  cmd=$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)
  case "$exe $cmd" in
    *java*) echo java;; *node*) echo node;; *dotnet*) echo dotnet;; *python*) echo python;; *) return 1;;
  esac
}
LANGUAGE="$LANG_SEL"
if [ "$LANGUAGE" = auto ] && [ -z "$UNIT" ]; then LANGUAGE="none"
elif [ "$LANGUAGE" = auto ]; then
  if LANGUAGE=$(detect_lang); then say "Dil algilandi: $LANGUAGE"
  else LANGUAGE="none"; warn "dil algilanamadi (servis calismiyor olabilir). Yalnizca ortam degiskenleri yazilacak; ajan/paket icin --lang java|node|python|dotnet verin."; fi
fi

fetch() { # url hedef
  command -v curl >/dev/null 2>&1 || die "curl gerekli"
  run mkdir -p "$(dirname "$2")"
  # shellcheck disable=SC2086
  run curl $CURL_OPTS -fL -o "$2" "$1" || die "indirilemedi: $1 (internet yok ise dosyayi elle koyup ilgili secenegi kullanin)"
}

case "$LANGUAGE" in
  java)
    if [ -z "$JAR" ]; then
      JAR="$HOMEDIR/opentelemetry-javaagent.jar"
      [ -f "$ROOT$JAR" ] || fetch "https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases/latest/download/opentelemetry-javaagent.jar" "$ROOT$JAR"
    fi
    add JAVA_TOOL_OPTIONS "-javaagent:$JAR";;
  node)
    command -v npm >/dev/null 2>&1 || die "npm bulunamadi (Node.js kurulu olmali)"
    run npm install -g @opentelemetry/api @opentelemetry/auto-instrumentations-node
    NR=$(npm root -g 2>/dev/null || echo /usr/lib/node_modules)
    add NODE_OPTIONS "--require $NR/@opentelemetry/auto-instrumentations-node/register";;
  python)
    command -v "$PY" >/dev/null 2>&1 || die "$PY bulunamadi (--python ile uygulamanin yorumlayicisini/venv'ini verin)"
    run "$PY" -m pip install opentelemetry-distro opentelemetry-exporter-otlp
    run "$PY" -m opentelemetry.instrumentation.bootstrap -a install || run opentelemetry-bootstrap -a install || warn "opentelemetry-bootstrap calistirilamadi"
    AI=$("$PY" -c 'import os,opentelemetry.instrumentation.auto_instrumentation as m;print(os.path.dirname(m.__file__))' 2>/dev/null || true)
    if [ -n "$AI" ]; then add PYTHONPATH "$AI"; else [ "$DRY" = 1 ] || warn "otomatik enstrumantasyon dizini bulunamadi; uygulamayi 'opentelemetry-instrument <komut>' ile baslatin"; fi;;
  dotnet)
    INST="$HOMEDIR/otel-dotnet-auto-install.sh"
    fetch "https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation/releases/latest/download/otel-dotnet-auto-install.sh" "$ROOT$INST"
    run chmod +x "$ROOT$INST"
    run env OTEL_DOTNET_AUTO_HOME="$HOMEDIR/dotnet" "$ROOT$INST"
    if [ "$DRY" != 1 ] && [ -f "$HOMEDIR/dotnet/instrument.sh" ]; then
      while IFS= read -r line; do ENVV+=("$line"); done < <(env -i bash -c ". '$HOMEDIR/dotnet/instrument.sh' >/dev/null 2>&1; env" | grep -E '^(CORECLR_|DOTNET_|OTEL_DOTNET_AUTO_HOME=|LD_PRELOAD=)' || true)
    fi;;
  none) ;;
esac

# ---------- yazma ----------
if [ -n "$UNIT" ]; then
  if [ "$DRY" = 1 ]; then
    say "[kuru calisma] $DROPIN dosyasina yazilacak:"; printf '  Environment="%s"\n' "${ENVV[@]}"
  else
    mkdir -p "$DROPIN_DIR"
    umask 077
    { echo "# APP_MON OpenTelemetry (otel-kurulum.sh) - kaldirmak icin: otel-kurulum.sh --service $SERVICE --unit $UNIT --undo"; echo "[Service]"
      for e in "${ENVV[@]}"; do printf 'Environment="%s"\n' "$(printf '%s' "$e" | sed 's/\\/\\\\/g; s/"/\\"/g')"; done; } > "$DROPIN"
    chmod 0640 "$DROPIN"
    say "Yazildi: $DROPIN (anahtar icerdigi icin yalnizca root okuyabilir)"
    if [ -z "$ROOT" ]; then
      systemctl daemon-reload
      if [ "$RESTART" = 1 ]; then systemctl restart "$UNIT"; say "Servis yeniden baslatildi: $UNIT"
      else say "Etkili olmasi icin servisi yeniden baslatin: sudo systemctl restart $UNIT"; fi
    fi
  fi
fi
if [ -n "$ENVFILE" ]; then
  if [ "$DRY" = 1 ]; then say "[kuru calisma] $ENVFILE dosyasina yazilacak:"; printf '  %s\n' "${ENVV[@]}"
  else
    umask 077; printf '%s\n' "${ENVV[@]}" > "$ENVFILE"; chmod 0600 "$ENVFILE"
    say "Yazildi: $ENVFILE  (docker: --env-file $ENVFILE | systemd: EnvironmentFile=$ENVFILE)"
    [ "$LANGUAGE" = none ] || warn "dile ozel ajan/paket yollari bu makineye gore yazildi; konteyner icinde ayni yollar yoksa uygulama imajina ajani ekleyin"
  fi
fi
say "Bitti. Birkac istek uretip APP_MON > Izleme > Dagitik izleme sayfasini kontrol edin."
