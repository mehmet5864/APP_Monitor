//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const taskName = "APP_MON Agent"

func progDir() string  { return filepath.Join(os.Getenv("ProgramFiles"), "APP_MON Agent") }
func dataDir() string  { return filepath.Join(os.Getenv("ProgramData"), "appmon-agent") }
func confPath() string { return filepath.Join(dataDir(), "agent.conf") }

// Windows'ta ayarlar ProgramData altındaki yapılandırma dosyasından okunur (kurulumsa).
func defaultConfig() string {
	if _, err := os.Stat(confPath()); err == nil {
		return confPath()
	}
	return ""
}

func sendConfFile() string { return confPath() }

func sh(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func doInstall(c settings) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(progDir(), 0o755); err != nil {
		return fmt.Errorf("yönetici olarak çalıştırın: %w", err)
	}
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	dst := filepath.Join(progDir(), "appmon-agent.exe")
	_ = sh("schtasks", "/End", "/TN", taskName) // varsa durdur (güncelleme)
	if !strings.EqualFold(self, dst) {
		in, err := os.Open(self)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return fmt.Errorf("ikili kopyalanamadı (ajan çalışıyor olabilir): %w", err)
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		out.Close()
	}
	if err := os.WriteFile(confPath(), []byte(confText(c)), 0o600); err != nil {
		return err
	}
	// Yapılandırma dosyası anahtarı içerir: yalnızca SYSTEM ve Yöneticiler okuyabilsin.
	_ = sh("icacls", confPath(), "/inheritance:r", "/grant:r", "*S-1-5-18:F", "*S-1-5-32-544:F")
	tr := fmt.Sprintf(`"%s" -config "%s"`, dst, confPath())
	if err := sh("schtasks", "/Create", "/TN", taskName, "/SC", "ONSTART", "/RU", "SYSTEM", "/RL", "HIGHEST", "/F", "/TR", tr); err != nil {
		return err
	}
	// schtasks varsayılanı 72 saat sonra görevi durdurur ve yeniden başlatmaz. Sınırı kaldır; ajan çıkarsa (güncelleme sonrası dahil) 1 dk içinde yeniden başlat.
	ps := fmt.Sprintf(`$s = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Seconds 0) -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable; Set-ScheduledTask -TaskName '%s' -Settings $s | Out-Null`, taskName)
	if err := sh("powershell", "-NoProfile", "-NonInteractive", "-Command", ps); err != nil {
		fmt.Println("uyarı: görev ayarları (yeniden başlatma) yapılamadı; uzaktan güncelleme sonrası ajanı elle başlatmanız gerekebilir:", err)
	}
	if err := sh("schtasks", "/Run", "/TN", taskName); err != nil {
		return err
	}
	fmt.Println("Kuruldu ve başlatıldı: zamanlanmış görev '"+taskName+"' (açılışta SYSTEM olarak çalışır). Dosyalar:", dst, "ve", confPath())
	return nil
}

func doUninstall() error {
	_ = sh("schtasks", "/End", "/TN", taskName)
	if err := sh("schtasks", "/Delete", "/TN", taskName, "/F"); err != nil {
		fmt.Println("uyarı:", err)
	}
	_ = os.RemoveAll(dataDir())
	// Çalışan ikili kendini silemeyebilir; görev durdurulduktan sonra dizini kaldırmayı dene.
	_ = os.RemoveAll(progDir())
	fmt.Println("Kaldırıldı.")
	return nil
}

func confText(c settings) string {
	var b strings.Builder
	w := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s=%s\r\n", k, v)
		}
	}
	w("APM_SERVER", c.Server)
	w("APM_KEY", c.Key)
	w("APM_NAME", c.Name)
	w("APM_WATCH", c.Watch)
	w("APM_LOG", c.Log)
	w("APM_CA_FILE", c.CAFile)
	w("APM_PG_DSN", c.PGDSN)
	w("APM_INTERVAL", fmt.Sprintf("%d", int(c.Interval.Seconds())))
	if c.Insecure {
		w("APM_INSECURE", "true")
	}
	if c.NoApps {
		w("APM_APPS", "off")
	}
	if c.NoUpdate {
		w("APM_UPDATE", "off")
	}
	if c.NoWeb {
		w("APM_WEBLOG", "off")
	}
	if c.NoConns {
		w("APM_CONNS", "off")
	}
	if c.NoRemote {
		w("APM_REMOTE_CONFIG", "off")
	}
	if c.NoOTLP {
		w("APM_OTLP", "off")
	} else if c.OTLPListen != "" {
		w("APM_OTLP", c.OTLPListen)
	}
	return b.String() + confExtras(c)
}
