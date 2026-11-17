//go:build linux

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Linux'ta ajan yetkisiz (DynamicUser) çalışır ve /usr/local/bin'e yazamaz. Bu yüzden ajan yalnızca "update.json" (ad + SHA-256)
// bırakıp çıkar; systemd'nin root olarak çalıştırdığı ExecStartPre=+ ... -apply-update adımı dosyayı yapılandırılmış sunucudan
// kendisi indirir, SHA-256'yı denetler ve ikiliyi değiştirir. Böylece ajan hesabı ele geçirilse bile root'a kod yazdıramaz.

func stateDirs() []string {
	var d []string
	if v := os.Getenv("STATE_DIRECTORY"); v != "" {
		d = append(d, strings.Split(v, ":")[0])
	}
	return append(d, "/var/lib/appmon-agent", "/var/lib/private/appmon-agent")
}

func firstStateDir() string { return stateDirs()[0] }

func platformUpdate(client *http.Client, cfg settings, o *updateOffer) (int, error) {
	if os.Getenv("INVOCATION_ID") == "" {
		return 0, fmt.Errorf("ajan systemd servisi olarak çalışmıyor (elle çalıştırılan ajan kendini güncellemez)")
	}
	dir := firstStateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	b, _ := json.Marshal(o)
	if err := os.WriteFile(filepath.Join(dir, "update.json"), b, 0o644); err != nil {
		return 0, fmt.Errorf("güncelleme isteği yazılamadı: %w", err)
	}
	return 0, nil // systemd Restart=always yeniden başlatır; ExecStartPre=+ değişimi yapar
}

// readUpdateErr önceki -apply-update hatasını okur (varsa 30 dk geri çekilme uygular).
func cleanupOld() {}

func readUpdateErr() {
	for _, d := range stateDirs() {
		p := filepath.Join(d, "update.err")
		if st, err := os.Stat(p); err == nil {
			if b, err := os.ReadFile(p); err == nil {
				lastUpdateErr = trunc(strings.TrimSpace(string(b)), 120)
				nextUpdateTry = st.ModTime().Add(30 * time.Minute)
			}
			return
		}
	}
}

// applyUpdate (-apply-update): root olarak, hizmet başlamadan önce çalışır.
func applyUpdate() error {
	var dir, jf string
	for _, d := range stateDirs() {
		if _, err := os.Stat(filepath.Join(d, "update.json")); err == nil {
			dir, jf = d, filepath.Join(d, "update.json")
			break
		}
	}
	if jf == "" {
		return nil
	}
	errFile := filepath.Join(dir, "update.err")
	fail := func(err error) error {
		_ = os.WriteFile(errFile, []byte(err.Error()), 0o644)
		_ = os.Remove(jf)
		return err
	}
	b, err := os.ReadFile(jf)
	if err != nil {
		return fail(err)
	}
	var o updateOffer
	if err := json.Unmarshal(b, &o); err != nil {
		return fail(err)
	}
	if err := validOffer(&o); err != nil {
		return fail(err)
	}
	cfg := settings{Interval: 15 * time.Second}
	if m, err := readConf(linConf); err == nil {
		cfg.apply(m)
	}
	cfg.apply(map[string]string{"APM_SERVER": os.Getenv("APM_SERVER"), "APM_CA_FILE": os.Getenv("APM_CA_FILE"), "APM_INSECURE": os.Getenv("APM_INSECURE")})
	if cfg.Server == "" {
		return fail(fmt.Errorf("sunucu adresi yok"))
	}
	if cfg.NoUpdate {
		return fail(fmt.Errorf("uzaktan güncelleme bu ajanda kapalı"))
	}
	tmp := linBin + ".dl"
	if err := downloadVerified(newClient(cfg), strings.TrimRight(cfg.Server, "/")+"/agent/dl/"+o.Name, tmp, o.SHA256); err != nil {
		return fail(err)
	}
	if err := os.Rename(tmp, linBin); err != nil {
		os.Remove(tmp)
		return fail(err)
	}
	_ = os.Remove(jf)
	_ = os.Remove(errFile)
	log.Printf("ajan güncellendi: %s", o.SHA256[:12])
	return nil
}

// Uzaktan yapılandırma (remote.go) için: durum dizini, servis denetimi ve yeniden başlatma (systemd Restart=always).
func remoteDir() string { return firstStateDir() }
func canRestart() bool  { return os.Getenv("INVOCATION_ID") != "" }
func restartSelf()      { os.Exit(0) }
