package main

import (
	"appmon/internal/agentcfg"
	"fmt"
	"os"
	"strings"
)

// dataItem, ajanın sunucuya gönderebildiği bir veri türüdür. Yapılandırma dosyasında
// SEND_* satırı olarak yazılır; satırın başına # konursa o veri gönderilmez.
type dataItem = agentcfg.Item

var dataItems = agentcfg.Items

func isOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "off", "false", "no", "hayir":
		return true
	}
	return false
}

// sends: Send tanımlı değilse (eski/boş yapılandırma) her şey gönderilir.
func (s settings) sends(key string) bool {
	if s.Send == nil {
		return true
	}
	return s.Send[key]
}

// applySend yapılandırma/ortamdaki SEND_* satırlarını işler. Hiç SEND_* yoksa varsayılan davranış
// (hepsi açık) korunur; en az bir tane varsa listede olmayan (# ile kapatılmış) veriler gönderilmez.
func (s *settings) applySend(m map[string]string) {
	any := false
	set := map[string]bool{}
	for k, v := range m {
		if strings.HasPrefix(k, "SEND_") {
			any = true
			if !isOff(v) {
				set[k] = true
			}
		}
	}
	if !any {
		return
	}
	s.Send = set
	s.deriveFlags()
}

// deriveFlags Send kümesinden kapatılan özellik bayraklarını türetir (yalnızca kapatır; APM_APPS=off gibi
// sabit kilitler Send ile yeniden açılamaz).
func (s *settings) deriveFlags() {
	if s.Send == nil {
		return
	}
	set := s.Send
	if !set["SEND_APPS"] {
		s.NoApps = true
	}
	if !set["SEND_CONNS"] {
		s.NoConns = true
	}
	if !set["SEND_WEBLOG"] {
		s.NoWeb = true
	}
	if !set["SEND_OTLP"] {
		s.NoOTLP = true
	}
	if !set["SEND_UPDATE"] {
		s.NoUpdate = true
	}
}

// effective ajanın şu an gönderdiği veri türlerini (SEND_ öneki olmadan) virgüllü listeler.
func (s settings) effective() string {
	var out []string
	for _, it := range dataItems {
		if s.sends(it.Key) {
			out = append(out, strings.TrimPrefix(it.Key, "SEND_"))
		}
	}
	return strings.Join(out, ",")
}

// sendFromEnv ortamdaki SEND_* değişkenlerini (Linux'ta systemd EnvironmentFile) bir haritaya toplar.
func sendFromEnv() map[string]string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 && strings.HasPrefix(kv[:i], "SEND_") {
			m[kv[:i]] = kv[i+1:]
		}
	}
	return m
}

func hostItem(name string) string {
	if strings.HasPrefix(name, "host.disk_pct") { // host.disk_pct ve host.disk_pct:<yol>
		return "SEND_HOST_DISK"
	}
	switch name {
	case "host.cpu_pct":
		return "SEND_HOST_CPU"
	case "host.mem_pct", "host.swap_pct":
		return "SEND_HOST_MEM"
	case "host.load1", "host.load5", "host.load15":
		return "SEND_HOST_LOAD"
	case "host.net_rx_bps", "host.net_tx_bps":
		return "SEND_HOST_NET"
	case "host.procs":
		return "SEND_HOST_PROCS"
	case "host.uptime_s":
		return "SEND_HOST_UPTIME"
	}
	return ""
}

// filterHost kapatılmış makine metriklerini ayıklar.
func (s settings) filterHost(ms []metric) []metric {
	if s.Send == nil {
		return ms
	}
	out := ms[:0]
	for _, m := range ms {
		if k := hostItem(m.Name); k != "" && !s.Send[k] {
			continue
		}
		out = append(out, m)
	}
	return out
}

// confExtras yapılandırma dosyasının sonuna eklenen, açıklamalı SEND_* bölümünü üretir.
func confExtras(c settings) string {
	var b strings.Builder
	b.WriteString("\n# ---------------------------------------------------------------------------\n")
	b.WriteString("# Sunucuya gönderilecek veriler. Bir satırın başına # koyarsanız o veri GÖNDERİLMEZ.\n")
	b.WriteString("# (Değişiklikten sonra ajanı yeniden başlatın. agent.up kalp atışı her zaman gönderilir.)\n")
	b.WriteString("# ---------------------------------------------------------------------------\n")
	for _, it := range dataItems {
		on := c.sends(it.Key)
		fmt.Fprintf(&b, "# %s\n", it.Desc)
		if on {
			fmt.Fprintf(&b, "%s=1\n", it.Key)
		} else {
			fmt.Fprintf(&b, "#%s=1\n", it.Key)
		}
	}
	return b.String()
}

// confTemplate -print-config çıktısı: elle düzenlenip -config ile kullanılabilen, tüm seçenekleri içeren örnek dosya.
func confTemplate(goos string) string {
	var b strings.Builder
	b.WriteString("# APP_MON ajan yapılandırma dosyası (örnek)\n")
	if goos == "windows" {
		b.WriteString("# Kurulu konum: C:\\ProgramData\\appmon-agent\\agent.conf   (değişiklikten sonra: schtasks /End /TN \"APP_MON Agent\" ardından schtasks /Run /TN \"APP_MON Agent\")\n")
		b.WriteString("# Bu dosyayla kurulum: appmon-agent.exe -config agent.conf -install   (yönetici PowerShell)\n")
	} else {
		b.WriteString("# Kurulu konum: /etc/appmon-agent.conf   (değişiklikten sonra: sudo systemctl restart appmon-agent)\n")
		b.WriteString("# Bu dosyayla kurulum: sudo ./appmon-agent -config appmon-agent.conf -install\n")
	}
	b.WriteString("# ANAHTAR=değer satırlarıdır; # ile başlayan satır yorumdur.\n\n")
	b.WriteString("# --- Bağlantı ---\n")
	b.WriteString("APM_SERVER=__APPMON_URL__\n")
	b.WriteString("APM_KEY=BURAYA_AGENT_ANAHTARI\n")
	b.WriteString("#APM_NAME=ERP-DB-01\n")
	b.WriteString("#APM_INTERVAL=15\n")
	b.WriteString("#APM_CA_FILE=\n")
	b.WriteString("#APM_INSECURE=true\n")
	b.WriteString("# Yönetici Ajanlar sayfasından gönderilen veri seçimini uzaktan değiştirebilir; bu satır (off) bunu bu makinede yasaklar:\n#APM_REMOTE_CONFIG=off\n")
	b.WriteString("\n# --- İzlenecekler ---\n")
	b.WriteString("#APM_WATCH=nginx,sshd\n")
	b.WriteString("#APM_PG_DSN=postgres://izleme:PAROLA@127.0.0.1:5432/uygulama_db?sslmode=disable   (PostgreSQL sorgu istatistikleri; birden çok için ;; ile ayırın; izleme kullanıcısına pg_monitor rolü verin)\n")
	b.WriteString("#APM_LOG=hata::/var/log/uygulama.log::(?i)error\n")
	if goos != "windows" {
		b.WriteString("#APM_ROOT=true\n")
	}
	b.WriteString(confExtras(settings{}))
	return b.String()
}
