@echo off
rem APP_MON'u durdurur. Verileriniz korunur; baslat-docker.bat ile kaldiginiz yerden devam edersiniz.
rem Tum verileri de silmek icin:  durdur-docker.bat /sil
setlocal
cd /d "%~dp0"

docker info >nul 2>&1
if errorlevel 1 (
  echo [HATA] Docker calismiyor; durdurulacak bir sey bulunamadi.
  pause
  exit /b 1
)

if /i "%~1"=="/sil" goto wipe

echo APP_MON durduruluyor ^(veriler korunur^)...
docker compose down
if errorlevel 1 (
  echo [HATA] Durdurulamadi.
  pause
  exit /b 1
)
echo Durduruldu.
exit /b 0

:wipe
echo.
echo DIKKAT: Tum APP_MON verileri ^(asset'ler, metrikler, alarmlar, senaryolar, API anahtarlari^) KALICI olarak silinecek.
set /p CONFIRM=Devam etmek icin EVET yazin: 
if /i not "%CONFIRM%"=="EVET" (
  echo Iptal edildi, hicbir sey silinmedi.
  exit /b 0
)
docker compose down -v
if errorlevel 1 (
  echo [HATA] Silinemedi.
  pause
  exit /b 1
)
echo Durduruldu ve tum veriler silindi.
exit /b 0
