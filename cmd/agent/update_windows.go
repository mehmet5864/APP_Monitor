//go:build windows

package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// cleanupOld önceki güncellemeden kalan appmon-agent.old.exe dosyasını siler (çalışan ajan artık yeni dosyadır).
func cleanupOld() { _ = os.Remove(filepath.Join(progDir(), "appmon-agent.old.exe")) }

// restartViaTask birkaç saniye sonra zamanlanmış görevi başlatan, ajan süreciyle bağı olmayan bir yardımcı çalıştırır.
// Görevin "başarısızlıkta yeniden başlat" ayarı eski kurulumlarda yoktur; bu yüzden yeniden başlatma buna bağlı bırakılmaz.
// Görev zaten çalışıyorsa schtasks /Run zararsızca başarısız olur (tek örnek kuralı).
func restartViaTask() {
	cmdline := `cmd /C "ping -n 4 127.0.0.1 >nul & schtasks /Run /TN "` + taskName + `""` // ham komut satırı: Go'nun tırnak kaçışı cmd'yi bozar
	for _, flags := range []uint32{0x200 | 0x8 | 0x01000000, 0x200 | 0x8} {               // yeni süreç grubu | ayrık | iş nesnesinden ayrıl (olmazsa onsuz)
		c := exec.Command("cmd")
		c.SysProcAttr = &syscall.SysProcAttr{CmdLine: cmdline, CreationFlags: flags, HideWindow: true}
		if err := c.Start(); err == nil {
			_ = c.Process.Release()
			return
		}
	}
}

func readUpdateErr() {}

func applyUpdate() error { return fmt.Errorf("yalnızca Linux'ta kullanılır") }

// Windows'ta ajan SYSTEM olarak çalışır: yeni dosyayı kendisi indirir, çalışan dosyayı yeniden adlandırır (Windows buna izin verir),
// yenisini yerine koyar, görevi birkaç saniye sonra başlatacak yardımcıyı çalıştırır ve 1 koduyla çıkar (görevin yeniden başlatma ayarı yedek olarak kalır).
func platformUpdate(client *http.Client, cfg settings, o *updateOffer) (int, error) {
	self, err := os.Executable()
	if err != nil {
		return 0, err
	}
	dst := filepath.Join(progDir(), "appmon-agent.exe")
	if !strings.EqualFold(self, dst) {
		return 0, fmt.Errorf("ajan zamanlanmış görev olarak kurulu değil (elle çalıştırılan ajan kendini güncellemez)")
	}
	nw := filepath.Join(progDir(), "appmon-agent.new.exe")
	if err := downloadVerified(client, strings.TrimRight(cfg.Server, "/")+"/agent/dl/"+o.Name, nw, o.SHA256); err != nil {
		return 0, err
	}
	old := filepath.Join(progDir(), "appmon-agent.old.exe")
	_ = os.Remove(old)
	if err := os.Rename(dst, old); err != nil {
		os.Remove(nw)
		return 0, err
	}
	if err := os.Rename(nw, dst); err != nil {
		_ = os.Rename(old, dst)
		return 0, err
	}
	restartViaTask()
	return 1, nil
}

// Uzaktan yapılandırma (remote.go) için: durum dizini, servis denetimi ve yeniden başlatma (zamanlanmış görev).
func remoteDir() string { return dataDir() }
func canRestart() bool {
	self, err := os.Executable()
	return err == nil && strings.EqualFold(filepath.Dir(self), progDir())
}
func restartSelf() { restartViaTask(); os.Exit(0) }
