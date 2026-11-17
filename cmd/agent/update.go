package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Uzaktan güncelleme (v2.7.4): sunucu heartbeat yanıtında "update" teklifi verirse ajan yeni ikili dosyayı sunucudan indirir,
// SHA-256'sını doğrular, çalıştığını denetler, kendini değiştirir ve yeniden başlar. Hata olursa mevcut ajan çalışmaya devam eder.

type updateOffer struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

const maxAgentBin = 120 << 20 // 120 MB üstü ikili reddedilir

var (
	selfSHA       string
	pendingOffer  *updateOffer
	lastUpdateErr string
	nextUpdateTry time.Time
	agentNameRe   = regexp.MustCompile(`^appmon-agent-(linux|windows)-(amd64|arm64)(\.exe)?$`)
	sha256Re      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// fileSHA256 bir dosyanın SHA-256'sını (hex) verir.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func initSelfSHA() {
	if p, err := os.Executable(); err == nil {
		if s, err := fileSHA256(p); err == nil {
			selfSHA = s
		}
	}
}

// expectedAgentName bu platformun ajan dosyası adıdır.
func expectedAgentName() string {
	n := "appmon-agent-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

// validOffer teklifin adı ve özetinin beklenen biçimde olduğunu denetler.
func validOffer(o *updateOffer) error {
	if o == nil || !agentNameRe.MatchString(o.Name) || o.Name != expectedAgentName() {
		return fmt.Errorf("teklif bu platform için değil")
	}
	if !sha256Re.MatchString(o.SHA256) {
		return fmt.Errorf("teklifteki SHA-256 geçersiz")
	}
	if o.Size < 0 || o.Size > maxAgentBin {
		return fmt.Errorf("teklifteki boyut geçersiz")
	}
	return nil
}

// downloadVerified url'yi dst'ye indirir, SHA-256'yı denetler, çalıştırılabilir yapar ve `-version` ile çalıştığını doğrular.
func downloadVerified(client *http.Client, url, dst, wantSHA string) error {
	cl := *client
	cl.Timeout = 5 * time.Minute
	resp, err := cl.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("indirme: HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxAgentBin+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
		return err
	}
	if n > maxAgentBin {
		os.Remove(dst)
		return fmt.Errorf("indirilen dosya çok büyük")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSHA {
		os.Remove(dst)
		return fmt.Errorf("SHA-256 uyuşmuyor (beklenen %s…, gelen %s…)", wantSHA[:12], got[:12])
	}
	_ = os.Chmod(dst, 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, dst, "-version").CombinedOutput(); err != nil || !strings.HasPrefix(string(out), "appmon-agent") {
		os.Remove(dst)
		return fmt.Errorf("yeni ikili çalışmadı: %v", err)
	}
	return nil
}

func trunc(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// tryUpdate bekleyen teklifi uygular. Başarılı olursa süreç yeniden başlamak üzere çıkar; hata olursa 10 dk sonra yeniden denenir.
func tryUpdate(client *http.Client, cfg settings) {
	o := pendingOffer
	pendingOffer = nil
	if o == nil || cfg.NoUpdate || o.SHA256 == selfSHA || time.Now().Before(nextUpdateTry) {
		return
	}
	code, err := platformUpdate(client, cfg, o)
	if err != nil {
		lastUpdateErr = trunc(err.Error(), 120)
		nextUpdateTry = time.Now().Add(10 * time.Minute)
		log.Printf("güncelleme başarısız: %v", err)
		return
	}
	log.Printf("ajan güncellendi (%s…); yeniden başlıyor", o.SHA256[:12])
	os.Exit(code)
}

// decodeOffer heartbeat yanıtındaki güncelleme teklifini okur.
func decodeOffer(body []byte) *updateOffer {
	var r struct {
		Update *updateOffer `json:"update"`
	}
	if json.Unmarshal(body, &r) != nil || validOffer(r.Update) != nil {
		return nil
	}
	return r.Update
}
