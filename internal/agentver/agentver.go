package agentver

// Agent ajanın kendi sürümüdür (sunucu sürümünden bağımsız, SemVer: ANA.ARA.YAMA).
//
//	ANA  : ajan-sunucu protokolü uyumsuz değişti (eski ajan yeni sunucuyla çalışmaz).
//	ARA  : yeni yetenek eklendi (geriye uyumlu).
//	YAMA : hata düzeltmesi.
//
// Bu paket sunucu sürümünü (internal/version) içermez; ajan ikilisi yalnızca bunu bağlar, böylece sunucu sürümü ajan SHA-256'sını etkilemez.
// Yalnızca cmd/agent kodu değiştiğinde artırılır. Sunucu sürümü değişse de ajan kodu aynıysa ikili dosya (ve SHA-256'sı) aynı kalır;
// böylece "güncelleme var" uyarısı yalnızca gerçekten yeni ajan olduğunda çıkar.
//
//	1.0.0 : v2.7.5 ajanı (uzaktan güncelleme, uygulama keşfi)
//	1.1.0 : web sunucusu erişim günlüğü okuma, -web-setup / -web-undo
//	1.1.1 : Windows güncellemesi sonrası ajanı kendisi yeniden başlatır; açılışta appmon-agent.old.exe silinir
//	1.1.2 : ajan ikilisi artık sunucu sürüm dizgisini içermez (ayrı paket); sunucu sürümü değişince ajan SHA-256'sı değişmez
//	1.2.0 : yerel OpenTelemetry (OTLP) trace alıcısı (127.0.0.1:4318) ve bağlantı tabanlı servis haritası verisi (süreçlerin giden TCP bağlantıları)
//	1.3.0 : yapılandırma dosyasında SEND_* satırlarıyla gönderilecek verilerin seçimi (başına # = gönderme); sunucuya gövde gzip sıkıştırılarak gider
//	1.4.0 : uzaktan veri ayarı (Ajanlar sayfasındaki onay kutuları; heartbeat yanıtıyla gelir, remote.json olarak saklanır, APM_REMOTE_CONFIG=off ile yerel kilit); PostgreSQL sorgu istatistikleri (APM_PG_DSN, SEND_DBSTATS); gelen bağlantı özeti
//	1.5.0 : ölçek: açılışta rastgele gecikme (jitter), gönderim hatasında üstel geri çekilme (sunucunun Retry-After değerine uyar), birikmiş tamponu parçalar arasında beklemeyle boşaltır
//	1.6.0 : saldırı yüzeyi envanteri: dinleyen port + süreç + çalışma zamanı sürümü + bağımlılık manifest okuma (SEND_INVENTORY)
const Agent = "1.6.0"
