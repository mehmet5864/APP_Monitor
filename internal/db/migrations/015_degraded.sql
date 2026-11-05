-- Kısmi erişim durumu: sayfa açılıyor ama bazı alt bileşenlere gidilemiyorsa asset "degraded" (sarı) olur.
-- Yalnızca kısıt genişletir, sütun ekler ve hazır kuralları günceller; mevcut ölçüm verisi değişmez.
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_status_check;
ALTER TABLE assets ADD CONSTRAINT assets_status_check CHECK (status IN ('unknown', 'up', 'down', 'offline', 'degraded'));
ALTER TABLE probes ADD COLUMN IF NOT EXISTS last_ok boolean;

-- Hazır "Servis erişilemiyor" kuralı (kullanıcı değiştirmediyse) tüm bileşenler başarısızken tetiklensin.
UPDATE alert_rules SET metric = 'asset.down', comparator = '>', threshold = 0
 WHERE name = 'Servis erişilemiyor' AND metric = 'probe.up' AND comparator = '<' AND threshold = 1;

-- Hazır uyarı kuralı: kısmi erişim sorunu (sarı). Hazır kuralı kullanan kiracılara eklenir.
INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity)
SELECT t.id, 'Servis kısmen erişilemiyor', 'asset.degraded', '>', 0, 30, 'warning'
  FROM tenants t
 WHERE EXISTS (SELECT 1 FROM alert_rules r WHERE r.tenant_id = t.id AND r.metric = 'asset.down')
   AND NOT EXISTS (SELECT 1 FROM alert_rules r WHERE r.tenant_id = t.id AND r.name = 'Servis kısmen erişilemiyor');
