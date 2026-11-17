package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Uzaktan yapılandırma: yönetici Ajanlar sayfasında hangi verilerin gönderileceğini seçer; sunucu bunu heartbeat yanıtında
// ("config") iletir. Ajan seçimi kalıcı dosyaya (remote.json) yazar ve yeniden başlar; başlangıçta bu dosya yerel SEND_*
// satırlarının üstüne uygulanır. APM_REMOTE_CONFIG=off ile ajan uzaktan ayarı hiç kabul etmez (yerel kilit).

type remoteConf struct {
	V    int       `json:"v"`
	Send []string  `json:"send"`
	DB   *remoteDB `json:"db,omitempty"` // yöneticinin Veri ayarı'nda verdiği PostgreSQL bağlantısı (parola içerir; dosya 0600)
}

type remoteDB struct {
	DSN string `json:"dsn"`
}

var (
	remoteVer   int         // uygulanmış uzaktan ayar sürümü (0: yok)
	pendingConf *remoteConf // yanıtla gelen, henüz uygulanmamış ayar
	confWarned  bool
)

func remotePath() string {
	d := remoteDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "remote.json")
}

func loadRemote() *remoteConf {
	p := remotePath()
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var rc remoteConf
	if json.Unmarshal(b, &rc) != nil || rc.V <= 0 {
		return nil
	}
	return &rc
}

func (s *settings) applyRemote(rc *remoteConf) {
	set := map[string]bool{}
	for _, k := range rc.Send {
		if validKey(k) {
			set[k] = true
		}
	}
	s.Send = set
	s.deriveFlags()
	if rc.DB != nil && rc.DB.DSN != "" {
		s.PGDSN = rc.DB.DSN // uzaktan verilen bağlantı yerel APM_PG_DSN'nin yerine geçer
		s.PGRemote = true
	}
}

func validKey(k string) bool {
	for _, it := range dataItems {
		if it.Key == k {
			return true
		}
	}
	return false
}

// decodeConf heartbeat yanıtındaki "config" alanını okur.
func decodeConf(body []byte) *remoteConf {
	var r struct {
		Config *remoteConf `json:"config"`
	}
	if json.Unmarshal(body, &r) != nil || r.Config == nil || r.Config.V < 0 {
		return nil
	}
	return r.Config
}

// handleRemote bekleyen uzaktan ayarı kaydeder ve (servis olarak çalışıyorsa) yeniden başlar.
func handleRemote(cfg settings) {
	rc := pendingConf
	pendingConf = nil
	if rc == nil || cfg.NoRemote || rc.V == remoteVer {
		return
	}
	p := remotePath()
	if p == "" || !canRestart() {
		if !confWarned {
			confWarned = true
			log.Printf("uzaktan yapılandırma geldi ama ajan servis olarak çalışmıyor; yerel yapılandırma kullanılır")
		}
		return
	}
	if rc.V == 0 { // yönetici uzaktan ayarı kaldırdı: yerel dosyaya dön
		_ = os.Remove(p)
	} else {
		b, _ := json.Marshal(rc)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			log.Printf("uzaktan yapılandırma kaydedilemedi: %v", err)
			return
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			log.Printf("uzaktan yapılandırma kaydedilemedi: %v", err)
			return
		}
	}
	log.Printf("uzaktan yapılandırma v%d alındı (%d veri türü); ajan yeniden başlıyor", rc.V, len(rc.Send))
	restartSelf()
}

func cfgLabels(cfg settings, labels map[string]string) {
	if remoteVer > 0 {
		labels["cfg_v"] = strconv.Itoa(remoteVer)
	}
	labels["cfg_send"] = trunc(cfg.effective(), 300)
	if cfg.PGRemote {
		labels["cfg_db"] = "remote"
	}
	if cfg.NoRemote {
		labels["cfg_lock"] = "1"
	}
	_ = strings.TrimSpace
}
