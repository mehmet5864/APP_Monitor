-- Alarm kuralları (tenant_id, name, time) ile sorgular; 500+ ajanda tam tarama yerine indeks kullanılır.
CREATE INDEX IF NOT EXISTS metrics_tenant_name_time ON metrics (tenant_id, name, time DESC);
