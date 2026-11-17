// Package agentcfg ajanın gönderebildiği veri türlerinin tek kaynağıdır: ajan (yapılandırma dosyası, uzaktan ayar)
// ve sunucu (Ajanlar sayfasındaki onay kutuları) aynı listeyi kullanır.
package agentcfg

// Item gönderilebilen bir veri türüdür. Key yapılandırma dosyasındaki SEND_* satırı adıdır.
type Item struct {
	Key   string `json:"key"`
	Group string `json:"group"`
	Desc  string `json:"desc"`
}

var Items = []Item{
	{"SEND_HOST_CPU", "Makine", "CPU kullanımı (host.cpu_pct)"},
	{"SEND_HOST_MEM", "Makine", "Bellek ve swap kullanımı (host.mem_pct, host.swap_pct)"},
	{"SEND_HOST_DISK", "Makine", "Disk doluluğu, her sürücü/bağlama noktası (host.disk_pct)"},
	{"SEND_HOST_LOAD", "Makine", "Yük ortalaması, Linux (host.load1, host.load5, host.load15)"},
	{"SEND_HOST_NET", "Makine", "Ağ trafiği, bayt/sn (host.net_rx_bps, host.net_tx_bps)"},
	{"SEND_HOST_PROCS", "Makine", "Süreç sayısı (host.procs)"},
	{"SEND_HOST_UPTIME", "Makine", "Açık kalma süresi (host.uptime_s)"},
	{"SEND_WATCH", "İzleme", "APM_WATCH ile izlenen süreçlerin var/yok ve adet metrikleri (proc.<ad>.*)"},
	{"SEND_LOGCOUNT", "İzleme", "APM_LOG ile tanımlı günlük desenlerinin eşleşme sayıları (log.<ad>.count)"},
	{"SEND_APPS", "Uygulama", "Uygulama keşfi: çalışan süreç/servis/port listesi, uygulama CPU/bellek metrikleri, otomatik günlük sayımı"},
	{"SEND_CONNS", "Uygulama", "Bağlantı tabanlı servis haritası: süreçlerin giden TCP bağlantı özeti (SEND_APPS ile birlikte çalışır)"},
	{"SEND_INVENTORY", "Uygulama", "Saldırı yüzeyi envanteri: dinleyen portlar, süreç, çalışma zamanı sürümü ve uygulama bağımlılıkları (yalnızca paket adı ve sürüm; 30 dakikada bir)"},
	{"SEND_WEBLOG", "Uygulama", "Web sunucusu (nginx/Apache/IIS) erişim günlüğünden istek/hata/gecikme özeti (içerik/IP gönderilmez)"},
	{"SEND_DBSTATS", "Uygulama", "Veritabanı sorgu istatistikleri: PostgreSQL pg_stat_statements özeti (yalnızca APM_PG_DSN tanımlıysa; parola sunucuya gitmez)"},
	{"SEND_OTLP", "Özellik", "Yerel OpenTelemetry alıcısı: uygulamaların trace'lerini 127.0.0.1:4318'den alıp sunucuya iletir"},
	{"SEND_UPDATE", "Özellik", "Uzaktan ajan güncellemesi (sunucunun önerdiği sürüme kendini günceller)"},
}

// Valid anahtar bilinen bir veri türü mü.
func Valid(key string) bool {
	for _, it := range Items {
		if it.Key == key {
			return true
		}
	}
	return false
}
