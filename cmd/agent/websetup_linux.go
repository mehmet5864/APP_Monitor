//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	appmonLogDir  = "/var/log/appmon"
	logrotateFile = "/etc/logrotate.d/appmon-access"
	logrotateConf = appmonLogDir + "/*.log {\n    daily\n    rotate 3\n    compress\n    missingok\n    notifempty\n    copytruncate\n}\n"
)

type webServer struct {
	kind    string // nginx | apache
	conf    string // yazılacak dosya
	content string
	test    []string
	reload  []string
	enable  []string // Debian: a2enconf
	disable []string
	scan    []string // geçersiz kılma taraması yapılacak dosyalar
}

func runOut(args ...string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func globAll(patterns ...string) []string {
	var out []string
	for _, p := range patterns {
		m, _ := filepath.Glob(p)
		out = append(out, m...)
	}
	return out
}

func detectWebServers() []webServer {
	var list []webServer
	if _, err := exec.LookPath("nginx"); err == nil {
		if st, err := os.Stat("/etc/nginx/conf.d"); err == nil && st.IsDir() {
			list = append(list, webServer{kind: "nginx", conf: "/etc/nginx/conf.d/appmon-access.conf", content: nginxConf(),
				test: []string{"nginx", "-t"}, reload: []string{"systemctl", "reload", "nginx"},
				scan: globAll("/etc/nginx/nginx.conf", "/etc/nginx/conf.d/*.conf", "/etc/nginx/sites-enabled/*", "/etc/nginx/default.d/*.conf")})
		}
	}
	// RHEL: httpd + conf.d
	if _, err := exec.LookPath("httpd"); err == nil {
		if st, err := os.Stat("/etc/httpd/conf.d"); err == nil && st.IsDir() {
			list = append(list, webServer{kind: "apache", conf: "/etc/httpd/conf.d/appmon-access.conf", content: apacheConf(),
				test: []string{"httpd", "-t"}, reload: []string{"systemctl", "reload", "httpd"},
				scan: globAll("/etc/httpd/conf/httpd.conf", "/etc/httpd/conf.d/*.conf")})
		}
	} else if _, err := exec.LookPath("apache2ctl"); err == nil { // Debian/Ubuntu
		if st, err := os.Stat("/etc/apache2/conf-available"); err == nil && st.IsDir() {
			list = append(list, webServer{kind: "apache", conf: "/etc/apache2/conf-available/appmon-access.conf", content: apacheConf(),
				test: []string{"apache2ctl", "configtest"}, reload: []string{"systemctl", "reload", "apache2"},
				enable: []string{"a2enconf", "appmon-access"}, disable: []string{"a2disconf", "appmon-access"},
				scan: globAll("/etc/apache2/apache2.conf", "/etc/apache2/sites-enabled/*", "/etc/apache2/conf-enabled/*")})
		}
	}
	return list
}

func prepareLogDir() error {
	if err := os.MkdirAll(appmonLogDir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(appmonLogDir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(appmonLogDir, "access.log")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	f.Close()
	_ = os.Chmod(p, 0o644)
	if _, err := exec.LookPath("chcon"); err == nil {
		if out, err := runOut("selinuxenabled"); err == nil || out == "" {
			if e := exec.Command("selinuxenabled").Run(); e == nil {
				_, _ = runOut("chcon", "-R", "-t", "httpd_log_t", appmonLogDir)
			}
		}
	}
	return os.WriteFile(logrotateFile, []byte(logrotateConf), 0o644)
}

func confirm() bool {
	fmt.Print("Devam etmek için 'evet' yazın: ")
	s, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(s), "evet")
}

func webSetup(dry, yes bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root yetkisi gerekir (sudo ile çalıştırın)")
	}
	servers := detectWebServers()
	if len(servers) == 0 {
		return fmt.Errorf("nginx veya Apache (httpd/apache2) ve ayar dizini bulunamadı; bu makinede yapılacak bir şey yok")
	}
	fmt.Println(webRiskText)
	fmt.Println()
	for _, s := range servers {
		fmt.Printf("Yazılacak: %s (%s)\n---\n%s---\n", s.conf, s.kind, s.content)
	}
	if dry {
		fmt.Println("(-dry-run: hiçbir şey değiştirilmedi)")
		for _, s := range servers {
			printOverrides(s)
		}
		return nil
	}
	if !yes && !confirm() {
		return fmt.Errorf("iptal edildi")
	}
	if err := prepareLogDir(); err != nil {
		return fmt.Errorf("günlük dizini hazırlanamadı: %w", err)
	}
	var failed []string
	for _, s := range servers {
		if err := os.WriteFile(s.conf, []byte(s.content), 0o644); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", s.kind, err))
			continue
		}
		if len(s.enable) > 0 {
			if out, err := runOut(s.enable...); err != nil {
				os.Remove(s.conf)
				failed = append(failed, fmt.Sprintf("%s: %v %s", s.kind, err, out))
				continue
			}
		}
		if out, err := runOut(s.test...); err != nil {
			rollback(s)
			failed = append(failed, fmt.Sprintf("%s: ayar sınaması başarısız, dosya silindi, hiçbir şey değişmedi:\n%s", s.kind, out))
			continue
		}
		if out, err := runOut(s.reload...); err != nil {
			rollback(s)
			failed = append(failed, fmt.Sprintf("%s: yeniden yükleme başarısız, dosya silindi: %v %s", s.kind, err, out))
			_, _ = runOut(s.reload...)
			continue
		}
		fmt.Printf("✔ %s: günlük eklendi ve ayar yeniden yüklendi.\n", s.kind)
		printOverrides(s)
	}
	fmt.Println("\nAjan, /var/log/appmon/access.log dosyasını bir dakika içinde okumaya başlar (Performans → Uygulamalar). Ajanı -root ile kurmadıysanız dosya herkesçe okunabilir olduğu için gerekmez.")
	if len(failed) > 0 {
		return fmt.Errorf("bazı adımlar başarısız:\n - %s", strings.Join(failed, "\n - "))
	}
	return nil
}

func rollback(s webServer) {
	if len(s.disable) > 0 {
		_, _ = runOut(s.disable...)
	}
	os.Remove(s.conf)
}

func printOverrides(s webServer) {
	ov := scanOverrides(s.kind, s.scan, s.conf)
	if len(ov) == 0 {
		return
	}
	line := nginxLine
	if s.kind == "apache" {
		line = apacheLine
	}
	fmt.Printf("\n⚠ %s: kendi erişim günlüğü tanımlayan satırlar bulundu. Bu sunucular otomatik kapsanmaz; ilgili sunucu bloğuna şu satırı ekleyin:\n    %s\n", s.kind, line)
	for _, o := range ov {
		fmt.Println("   ", o)
	}
}

func webUndo() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("root yetkisi gerekir (sudo ile çalıştırın)")
	}
	var errs []string
	for _, s := range detectWebServers() {
		if _, err := os.Stat(s.conf); err != nil {
			continue
		}
		rollback(s)
		if out, err := runOut(s.test...); err != nil {
			errs = append(errs, fmt.Sprintf("%s: ayar sınaması başarısız (yeniden yüklenmedi): %s", s.kind, out))
			continue
		}
		if out, err := runOut(s.reload...); err != nil {
			errs = append(errs, fmt.Sprintf("%s: yeniden yükleme başarısız: %v %s", s.kind, err, out))
			continue
		}
		fmt.Printf("✔ %s: ajan günlüğü kaldırıldı ve ayar yeniden yüklendi.\n", s.kind)
	}
	os.Remove(logrotateFile)
	fmt.Println("Not: sanal sunuculara elle eklediğiniz appmon_fmt satırlarını da silmeniz gerekir; /var/log/appmon dizini silinmedi.")
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}
