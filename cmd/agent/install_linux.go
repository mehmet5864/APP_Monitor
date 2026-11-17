//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

const (
	linBin  = "/usr/local/bin/appmon-agent"
	linConf = "/etc/appmon-agent.conf"
	linUnit = "/etc/systemd/system/appmon-agent.service"
)

// Linux'ta ayarlar systemd EnvironmentFile ile ortam değişkeni olarak gelir.
func defaultConfig() string { return "" }

// sendConfFile: yeniden kurulumda kullanıcının # ile kapattığı verilerin korunması için okunur.
func sendConfFile() string { return linConf }

func confText(c settings) string {
	var b strings.Builder
	w := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
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
	if c.Root {
		w("APM_ROOT", "true")
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

const unitText = `[Unit]
Description=APP_MON ajan
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/appmon-agent.conf
ExecStartPre=-+/usr/local/bin/appmon-agent -apply-update
ExecStart=/usr/local/bin/appmon-agent
StateDirectory=appmon-agent
Restart=always
RestartSec=10
DynamicUser=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
`

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func doInstall(c settings) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root yetkisi gerekir: sudo ./appmon-agent -install ...")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemd bulunamadı; ajanı elle çalıştırın (APM_SERVER/APM_KEY ortam değişkenleri ile)")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self != linBin {
		if err := copyFile(self, linBin, 0o755); err != nil {
			return fmt.Errorf("ikili kopyalanamadı: %w", err)
		}
	}
	if err := os.WriteFile(linConf, []byte(confText(c)), 0o600); err != nil {
		return err
	}
	unit := unitText
	if c.Log != "" || c.Root {
		// Günlük dosyaları genelde root'a aittir: bu kurulumda ajan root olarak çalışır ve yapılandırmayı kendisi okur
		// (EnvironmentFile regex'teki ters eğik çizgileri bozar).
		unit = strings.Replace(unit, "EnvironmentFile=/etc/appmon-agent.conf\nExecStartPre=-+/usr/local/bin/appmon-agent -apply-update\nExecStart=/usr/local/bin/appmon-agent\n", "ExecStartPre=-+/usr/local/bin/appmon-agent -apply-update\nExecStart=/usr/local/bin/appmon-agent -config /etc/appmon-agent.conf\n", 1)
		unit = strings.Replace(unit, "DynamicUser=yes\n", "", 1)
	}
	if err := os.WriteFile(linUnit, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", "--now", "appmon-agent"}, {"restart", "appmon-agent"}} {
		if out, err := exec.Command("systemctl", a...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %s: %v: %s", strings.Join(a, " "), err, out)
		}
	}
	fmt.Println("Kuruldu ve başlatıldı: appmon-agent (systemd). Durum: systemctl status appmon-agent | Günlük: journalctl -u appmon-agent -f")
	return nil
}

func doUninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root yetkisi gerekir")
	}
	_ = exec.Command("systemctl", "disable", "--now", "appmon-agent").Run()
	for _, f := range []string{linUnit, linConf, linBin} {
		_ = os.Remove(f)
	}
	_ = exec.Command("systemctl", "daemon-reload").Run()
	fmt.Println("Kaldırıldı.")
	return nil
}
