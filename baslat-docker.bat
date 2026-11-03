@echo off
rem APP_MON'u Docker Desktop'ta baslatir (ilk seferde imaji de derler). Verileriniz korunur.
setlocal
cd /d "%~dp0"

docker info >nul 2>&1
if errorlevel 1 (
  echo [HATA] Docker calismiyor. Once Docker Desktop'i acin, "Engine running" yazisini bekleyin, sonra tekrar deneyin.
  pause
  exit /b 1
)

if not exist ".env" (
  echo [HATA] .env dosyasi bulunamadi. Bu klasorde .env olmali ^(POSTGRES_PASSWORD ve APM_ADMIN_KEY icin^).
  pause
  exit /b 1
)

set "APM_PORT=8080"
for /f "usebackq tokens=1,* delims==" %%a in (`findstr /b /c:"APM_PORT=" .env`) do set "APM_PORT=%%b"

echo APP_MON baslatiliyor...
docker compose up -d --build
if errorlevel 1 (
  echo.
  echo [HATA] Baslatilamadi. Yukaridaki hata mesajini inceleyin.
  pause
  exit /b 1
)

echo Sunucunun hazir olmasi bekleniyor...
set /a TRIES=0
:wait
set /a TRIES+=1
curl -s -o nul -f "http://localhost:%APM_PORT%/healthz" >nul 2>&1
if not errorlevel 1 goto ready
if %TRIES% GEQ 40 goto timeout
timeout /t 2 /nobreak >nul
goto wait

:ready
echo.
echo APP_MON hazir: http://localhost:%APM_PORT%
echo Giris anahtari: .env dosyasindaki APM_ADMIN_KEY
start "" "http://localhost:%APM_PORT%"
exit /b 0

:timeout
echo.
echo [UYARI] Sunucu 80 saniyede hazir olmadi. Loglara bakin:  docker compose logs server
pause
exit /b 1
