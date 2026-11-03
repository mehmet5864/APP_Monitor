@echo off
rem Proje dosyalari guncellendikten sonra APP_MON'u yeniden derleyip yeniden baslatir.
rem Veritabani verileriniz korunur; yeni veritabani degisiklikleri (migration) otomatik uygulanir.
setlocal
cd /d "%~dp0"

docker info >nul 2>&1
if errorlevel 1 (
  echo [HATA] Docker calismiyor. Once Docker Desktop'i acin, sonra tekrar deneyin.
  pause
  exit /b 1
)

if not exist ".env" (
  echo [HATA] .env dosyasi bulunamadi.
  pause
  exit /b 1
)

set "APM_PORT=8080"
for /f "usebackq tokens=1,* delims==" %%a in (`findstr /b /c:"APM_PORT=" .env`) do set "APM_PORT=%%b"

echo [1/3] Imaj yeniden derleniyor ^(temel imajlar da guncellenir^)...
docker compose build --pull
if errorlevel 1 (
  echo.
  echo [HATA] Derleme basarisiz. Yukaridaki hata mesajini inceleyin.
  pause
  exit /b 1
)

echo [2/3] Yeni surum baslatiliyor...
docker compose up -d
if errorlevel 1 (
  echo.
  echo [HATA] Baslatilamadi.
  pause
  exit /b 1
)

echo [3/3] Eski imaj artiklari temizleniyor...
docker image prune -f >nul 2>&1

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
echo Guncelleme tamam: http://localhost:%APM_PORT%
echo Tarayicida Ctrl+F5 ile sayfayi yenileyin.
exit /b 0

:timeout
echo.
echo [UYARI] Sunucu 80 saniyede hazir olmadi. Loglara bakin:  docker compose logs server
pause
exit /b 1
