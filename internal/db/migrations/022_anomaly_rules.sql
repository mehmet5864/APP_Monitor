-- Anomali tespiti için hazır alarm kuralları (KAPALI gelir; Alarmlar sayfasından açılır). Yalnızca ekleme yapar.
INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity, enabled)
SELECT t.id, v.name, v.metric, '>', 6, 600, 'warning', false
  FROM tenants t,
       (VALUES ('Anormal gecikme (anomali)', 'anomaly.latency_z'),
               ('Anormal CPU kullanımı (anomali)', 'anomaly.cpu_z'),
               ('Anormal bellek kullanımı (anomali)', 'anomaly.mem_z')) AS v(name, metric)
 WHERE NOT EXISTS (SELECT 1 FROM alert_rules r WHERE r.tenant_id = t.id AND r.name = v.name);
