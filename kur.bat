@echo off
rem APP_MON kurulum (Windows): Docker'i denetler, .env'yi rastgele parolalarla olusturur, sistemi baslatir.
setlocal EnableDelayedExpansion
cd /d "%~dp0"

echo === APP_MON kurulum ===
where docker >nul 2>&1
if errorlevel 1 (
  echo [HATA] Docker bulunamadi. Docker Desktop'i kurun: https://www.docker.com/products/docker-desktop
  echo Kurduktan sonra Docker Desktop'i acin ve bu dosyayi tekrar calistirin.
  pause
  exit /b 1
)

if exist ".env" (
  echo .env zaten var, degistirilmedi.
) else (
  echo .env olusturuluyor ^(rastgele parola ve giris anahtari^)...
  for /f %%i in ('powershell -NoProfile -Command "[guid]::NewGuid().ToString('N')"') do set "PGPASS=%%i"
  for /f %%i in ('powershell -NoProfile -Command "[guid]::NewGuid().ToString('N')+[guid]::NewGuid().ToString('N')"') do set "ADMKEY=%%i"
  if not defined PGPASS ( echo [HATA] Rastgele deger uretilemedi. & pause & exit /b 1 )
  > .env echo POSTGRES_PASSWORD=!PGPASS!
  >> .env echo APM_ADMIN_KEY=!ADMKEY!
  >> .env echo APM_MODE=onprem
  >> .env echo APM_ASSET_LIMIT=25
  >> .env echo APM_PORT=8080
  >> .env echo APM_RETENTION_DAYS=14
  >> .env echo APM_BROWSER_ENABLED=true
)

call "%~dp0baslat-docker.bat"
if errorlevel 1 exit /b 1

for /f "usebackq tokens=1,* delims==" %%a in (`findstr /b /c:"APM_ADMIN_KEY=" .env`) do echo Giris anahtari: %%b
exit /b 0
