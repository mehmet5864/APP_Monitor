<#
.SYNOPSIS
APP_MON - Windows icin OpenTelemetry (dagitik izleme) kurulum betigi (PowerShell 5.1+).

.DESCRIPTION
1) APP_MON'a baglantiyi ve anahtari dener, gercek bir deneme trace'i gonderir (uygulamaya dokunmaz).
2) Istenirse bir Windows servisine veya IIS'e OpenTelemetry'yi ekler (ortam degiskenleri + dile gore ajan/paket kurulumu).
3) -Undo ile eklenenleri geri alir.
Yonetici PowerShell gerekir (servis/IIS yapilandirirken). Betik APP_MON sunucusundan indirildiginde sunucu adresi hazir gomuludur.

.EXAMPLE
.\otel-kurulum.ps1 -Service siparis -Key ANAHTAR
Yalnizca baglanti + deneme trace'i. Bu makinede APP_MON ajani (>= 1.2.0) calisiyorsa -Key gerekmez (-ViaAgent otomatik secilir).

.EXAMPLE
.\otel-kurulum.ps1 -Service siparis -Key ANAHTAR -WindowsService SiparisSvc -Language dotnet -Restart
Bir Windows servisini yapilandirir.

.EXAMPLE
.\otel-kurulum.ps1 -Service siparis -Key ANAHTAR -IIS -Language dotnet -Restart
IIS uygulamalari icin (.NET). Servis adi IIS site adindan alinir.

.EXAMPLE
.\otel-kurulum.ps1 -Service siparis -WindowsService SiparisSvc -Undo -Restart
Eklenenleri geri alir.

.EXAMPLE
powershell -ExecutionPolicy Bypass -File .\otel-kurulum.ps1 -Service siparis -Key ANAHTAR
Betigi dosya olarak calistirma (yurutme ilkesi engelliyorsa bu bicimi kullanin).
#>
param(
  [string]$Server = $(if ($env:APPMON_URL) { $env:APPMON_URL } else { '__APPMON_URL__' }),
  [string]$Key = $env:APPMON_KEY,
  [Parameter(Mandatory = $true)][string]$Service,
  [string]$WindowsService = '',
  [switch]$IIS,
  [ValidateSet('auto', 'java', 'node', 'python', 'dotnet', 'none')][string]$Language = 'auto',
  [string]$Sample = '',
  [string]$Jar = '',
  [string]$Python = 'python',
  [switch]$Restart,
  [switch]$Undo,
  [switch]$DryRun,
  [switch]$NoTest,
  [switch]$ViaAgent,
  [switch]$Insecure
)

$ErrorActionPreference = 'Stop'
$Server = $Server.TrimEnd('/')
$Home2 = Join-Path $env:ProgramData 'AppMon\otel'
$RegBase = 'HKLM:\SYSTEM\CurrentControlSet\Services'

function Say($m) { Write-Host $m }
function Warn($m) { Write-Warning $m }
function Die($m) { Write-Host ("HATA: " + $m) -ForegroundColor Red; exit 1 }
function Do-It($desc, [scriptblock]$sb) {
  if ($DryRun) { Say ("[kuru calisma] " + $desc) } else { & $sb }
}

# Ajan (>= 1.2.0) bu makinede yerel OTLP alicisi acar: anahtar gerekmez, ajan sunucuya iletir.
$AgentUrl = if ($env:APPMON_AGENT_URL) { $env:APPMON_AGENT_URL.TrimEnd('/') } else { 'http://127.0.0.1:4318' }
if (-not $ViaAgent -and -not $Key) {
  try {
    $hz = Invoke-WebRequest -Uri "$AgentUrl/healthz" -UseBasicParsing -TimeoutSec 2
    if ($hz.Content -match 'appmon-agent') { $ViaAgent = [switch]$true; Say "Yerel APP_MON ajani bulundu ($AgentUrl): trace'ler ajan uzerinden gonderilecek, anahtar gerekmiyor." }
  } catch { }
}
if ($ViaAgent) { $Otlp = $AgentUrl } else {
  if ($Server -notmatch '^https?://') { Die 'gecerli bir sunucu adresi gerekli: -Server https://appmon.ornek.local' }
  $Otlp = $Server
}
if ($Service -notmatch '^[A-Za-z0-9._-]{1,100}$') { Die 'servis adi yalnizca harf, rakam, nokta, tire ve alt cizgi icermeli' }
if ($WindowsService -and $WindowsService -notmatch '^[A-Za-z0-9 ._-]{1,200}$') { Die 'gecersiz Windows servis adi' }
if ($Sample -and ($Sample -notmatch '^(0(\.[0-9]+)?|1(\.0+)?)$')) { Die '-Sample 0 ile 1 arasinda olmali (ornek 0.1)' }

try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }
if ($Insecure) {
  try {
    Add-Type -TypeDefinition 'using System.Net; using System.Security.Cryptography.X509Certificates; public class AppMonTrustAll : ICertificatePolicy { public bool CheckValidationResult(ServicePoint sp, X509Certificate cert, WebRequest req, int problem) { return true; } }' -ErrorAction SilentlyContinue
    [Net.ServicePointManager]::CertificatePolicy = New-Object AppMonTrustAll
  } catch { }
}

function Test-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

# ---------- geri alma ----------
function Get-Env($path) {
  $v = (Get-ItemProperty -Path $path -Name Environment -ErrorAction SilentlyContinue).Environment
  if ($null -eq $v) { return @() }
  return @($v)
}
function Set-EnvList($path, $list) {
  if ($list.Count -eq 0) { Remove-ItemProperty -Path $path -Name Environment -ErrorAction SilentlyContinue }
  else { New-ItemProperty -Path $path -Name Environment -PropertyType MultiString -Value ([string[]]$list) -Force | Out-Null }
}
function Get-Added($path) {
  $v = (Get-ItemProperty -Path $path -Name AppMonOtelKeys -ErrorAction SilentlyContinue).AppMonOtelKeys
  if ($null -eq $v) { return @() }
  return @($v)
}
function Remove-Otel($path, $label) {
  if (-not (Test-Path $path)) { Say ("Bulunamadi: " + $label); return }
  $added = Get-Added $path
  if ($added.Count -eq 0) { Say ("Kaldirilacak APP_MON ayari yok: " + $label); return }
  $keep = @(Get-Env $path | Where-Object { $e = $_; -not ($added | Where-Object { $e -like ($_ + '=*') }) })
  Do-It ("$label ortam degiskenleri geri alinacak") {
    Set-EnvList $path $keep
    Remove-ItemProperty -Path $path -Name AppMonOtelKeys -ErrorAction SilentlyContinue
  }
  Say ("Geri alindi: " + $label)
}

if ($Undo) {
  if (-not (Test-Admin)) { Die 'yonetici PowerShell gerekli' }
  if (-not $WindowsService -and -not $IIS) { Die '-Undo icin -WindowsService veya -IIS verin' }
  if ($WindowsService) { Remove-Otel "$RegBase\$WindowsService" ("Windows servisi " + $WindowsService) }
  if ($IIS) {
    foreach ($n in 'W3SVC', 'WAS') { Remove-Otel "$RegBase\$n" ("IIS ($n)") }
    if (Get-Module -ListAvailable -Name OpenTelemetry.DotNet.Auto -ErrorAction SilentlyContinue) { Say 'Not: .NET otomatik enstrumantasyonu tamamen kaldirmak icin Unregister-OpenTelemetryForIIS / Uninstall-OpenTelemetryCore komutlarini kullanin.' }
  }
  if ($Restart) {
    if ($WindowsService) { Do-It "Restart-Service $WindowsService" { Restart-Service -Name $WindowsService -Force } }
    if ($IIS) { Do-It 'iisreset' { iisreset | Out-Null } }
  } else { Say 'Etkili olmasi icin servisi/IIS yeniden baslatin.' }
  exit 0
}

# ---------- baglanti ve deneme trace'i ----------
function Hex($n) { $b = New-Object byte[] $n; (New-Object Security.Cryptography.RNGCryptoServiceProvider).GetBytes($b); -join ($b | ForEach-Object { $_.ToString('x2') }) }
function Send-Trace($body) {
  try {
    $r = Invoke-WebRequest -Uri "$Otlp/v1/traces" -Method Post -Body $body -ContentType 'application/json' -Headers $(if ($ViaAgent) { @{} } else { @{ 'X-API-Key' = $Key } }) -UseBasicParsing -TimeoutSec 20
    return [int]$r.StatusCode
  } catch {
    if ($_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }
    Write-Host $_.Exception.Message -ForegroundColor Yellow
    return 0
  }
}
if (-not $NoTest) {
  if (-not $ViaAgent -and -not $Key) { Die '-Key (APP_MON''dan olusturdugunuz agent anahtari) gerekli (veya APPMON_KEY ortam degiskeni)' }
  Say "APP_MON baglantisi deneniyor: $Otlp/v1/traces"
  $code = Send-Trace '{"resourceSpans":[]}'
  if ($code -eq 0) { Die 'sunucuya ulasilamadi. Adres, port, guvenlik duvari ve (kendi imzali sertifikada) -Insecure secenegini kontrol edin' }
  if ($code -eq 401 -or $code -eq 403) { Die "anahtar reddedildi (HTTP $code): anahtarin rolu 'agent' olmali ve dogru kopyalanmali" }
  if ($code -ne 200) { Die "beklenmeyen yanit: HTTP $code" }
  Say '  baglanti ve anahtar dogru (HTTP 200)'
  $now = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds() * 1000000
  $body = '{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"' + $Service + '"}}]},"scopeSpans":[{"spans":[{"traceId":"' + (Hex 16) + '","spanId":"' + (Hex 8) + '","name":"appmon-kurulum-denemesi","kind":2,"startTimeUnixNano":"' + ($now - 50000000) + '","endTimeUnixNano":"' + $now + '","attributes":[{"key":"appmon.test","value":{"stringValue":"true"}}]}]}]}]}'
  $code = Send-Trace $body
  if ($code -ne 200) { Die "deneme trace'i reddedildi (HTTP $code); lisans asset siniri dolmus olabilir" }
  Say "  deneme trace'i gonderildi. APP_MON > Izleme > Dagitik izleme sayfasinda '$Service' servisini goreceksiniz."
}

# ---------- ortam degiskenleri ----------
$envv = [ordered]@{}
$envv['OTEL_SERVICE_NAME'] = $Service
$envv['OTEL_EXPORTER_OTLP_TRACES_ENDPOINT'] = "$Otlp/v1/traces"
$envv['OTEL_EXPORTER_OTLP_TRACES_PROTOCOL'] = 'http/protobuf'
if (-not $ViaAgent) { $envv['OTEL_EXPORTER_OTLP_HEADERS'] = "X-API-Key=$Key" }
$envv['OTEL_METRICS_EXPORTER'] = 'none'
$envv['OTEL_LOGS_EXPORTER'] = 'none'
if ($Sample) { $envv['OTEL_TRACES_SAMPLER'] = 'parentbased_traceidratio'; $envv['OTEL_TRACES_SAMPLER_ARG'] = $Sample }
if ($IIS) { $envv.Remove('OTEL_SERVICE_NAME') }  # IIS'te servis adi site/uygulama adindan turetilir

if (-not $WindowsService -and -not $IIS) {
  Say ''
  Say 'Uygulamaniza su ortam degiskenlerini verin (dile ozel ajan/paket icin -WindowsService veya -IIS kullanin):'
  foreach ($k in $envv.Keys) { Say ("  " + $k + "=" + $envv[$k]) }
  Say ''
  Say ("Servisi otomatik yapilandirmak icin: .\otel-kurulum.ps1 -Service $Service -Key ... -WindowsService <servis-adi> -Language <dil>")
  exit 0
}
if (-not (Test-Admin)) { Die 'yonetici PowerShell gerekli (servis/IIS yapilandirmasi icin)' }
if ($WindowsService -and -not (Test-Path "$RegBase\$WindowsService")) { Die "Windows servisi bulunamadi: $WindowsService (Get-Service ile adini kontrol edin)" }

# ---------- dil ----------
if ($Language -eq 'auto') {
  if ($IIS) { $Language = 'dotnet'; Say 'IIS icin dil: dotnet (baska dil icin -Language verin)' }
  else { $Language = 'none'; Warn 'dil algilanamadi; yalnizca ortam degiskenleri yazilacak. Ajan/paket icin -Language java|node|python|dotnet verin.' }
}
function Download($url, $dest) {
  Do-It "indir: $url -> $dest" {
    New-Item -ItemType Directory -Force -Path (Split-Path $dest) | Out-Null
    try { Invoke-WebRequest -Uri $url -OutFile $dest -UseBasicParsing -TimeoutSec 120 } catch { Die "indirilemedi: $url (internet yok ise dosyayi elle koyup ilgili secenegi kullanin)" }
  }
}

$extra = [ordered]@{}
switch ($Language) {
  'java' {
    if (-not $Jar) {
      $Jar = Join-Path $Home2 'opentelemetry-javaagent.jar'
      if (-not (Test-Path $Jar)) { Download 'https://github.com/open-telemetry/opentelemetry-java-instrumentation/releases/latest/download/opentelemetry-javaagent.jar' $Jar }
    }
    $extra['JAVA_TOOL_OPTIONS'] = "-javaagent:$Jar"
  }
  'node' {
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { Die 'npm bulunamadi (Node.js kurulu olmali)' }
    Do-It 'npm install -g @opentelemetry/api @opentelemetry/auto-instrumentations-node' { npm install -g '@opentelemetry/api' '@opentelemetry/auto-instrumentations-node' | Out-Null }
    $nr = (npm root -g).Trim()
    $extra['NODE_OPTIONS'] = "--require `"$nr\@opentelemetry\auto-instrumentations-node\register`""
  }
  'python' {
    if (-not (Get-Command $Python -ErrorAction SilentlyContinue)) { Die "$Python bulunamadi (-Python ile uygulamanin yorumlayicisini/venv'ini verin)" }
    Do-It "$Python -m pip install opentelemetry-distro opentelemetry-exporter-otlp" { & $Python -m pip install opentelemetry-distro opentelemetry-exporter-otlp | Out-Null }
    Do-It "$Python -m opentelemetry.instrumentation.bootstrap -a install" { & $Python -m opentelemetry.instrumentation.bootstrap -a install | Out-Null }
    if (-not $DryRun) {
      $ai = (& $Python -c 'import os,opentelemetry.instrumentation.auto_instrumentation as m;print(os.path.dirname(m.__file__))').Trim()
      if ($ai) { $extra['PYTHONPATH'] = $ai } else { Warn "otomatik enstrumantasyon dizini bulunamadi; uygulamayi 'opentelemetry-instrument <komut>' ile baslatin" }
    }
  }
  'dotnet' {
    $psm = Join-Path $Home2 'OpenTelemetry.DotNet.Auto.psm1'
    if (-not (Test-Path $psm)) { Download 'https://github.com/open-telemetry/opentelemetry-dotnet-instrumentation/releases/latest/download/OpenTelemetry.DotNet.Auto.psm1' $psm }
    if (-not $DryRun) {
      Import-Module $psm -Force
      Install-OpenTelemetryCore
      if ($IIS) { Register-OpenTelemetryForIIS }
      else { Register-OpenTelemetryForWindowsService -WindowsServiceName $WindowsService -OTelServiceName $Service }
    } else { Say '[kuru calisma] Install-OpenTelemetryCore ve Register-OpenTelemetryFor... calistirilacak' }
  }
}

# ---------- yazma (mevcut ortam degiskenleri korunur; eklenenler geri alma icin kaydedilir) ----------
function Apply-Env($path, $label, $vars) {
  $cur = @(Get-Env $path)
  $added = @(Get-Added $path)
  foreach ($k in $vars.Keys) {
    $cur = @($cur | Where-Object { $_ -notlike ($k + '=*') }) + ($k + '=' + $vars[$k])
    if ($added -notcontains $k) { $added += $k }
  }
  Do-It ("$label ortam degiskenleri yazilacak: " + (($vars.Keys) -join ', ')) {
    Set-EnvList $path $cur
    New-ItemProperty -Path $path -Name AppMonOtelKeys -PropertyType MultiString -Value ([string[]]$added) -Force | Out-Null
  }
  Say ("Yazildi: " + $label + " (anahtar kayit defterinde, yalnizca yonetici/SYSTEM okuyabilir)")
}
$all = [ordered]@{}
foreach ($k in $envv.Keys) { $all[$k] = $envv[$k] }
foreach ($k in $extra.Keys) { $all[$k] = $extra[$k] }
if ($WindowsService) { Apply-Env "$RegBase\$WindowsService" ("Windows servisi " + $WindowsService) $all }
if ($IIS) { foreach ($n in 'W3SVC', 'WAS') { Apply-Env "$RegBase\$n" ("IIS ($n)") $all } }

if ($Restart) {
  if ($WindowsService) { Do-It "Restart-Service $WindowsService" { Restart-Service -Name $WindowsService -Force }; Say ("Servis yeniden baslatildi: " + $WindowsService) }
  if ($IIS) { Do-It 'iisreset' { iisreset | Out-Null }; Say 'IIS yeniden baslatildi.' }
} else {
  if ($WindowsService) { Say ("Etkili olmasi icin: Restart-Service " + $WindowsService) }
  if ($IIS) { Say 'Etkili olmasi icin: iisreset' }
}
Say 'Bitti. Birkac istek uretip APP_MON > Izleme > Dagitik izleme sayfasini kontrol edin.'
