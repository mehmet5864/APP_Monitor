-- SLA: günlük erişilebilirlik özeti (ham ölçüm saklama süresinden bağımsız, uzun süre tutulur) ve asset başına hedef.
-- Yalnızca yeni tablo ve sütun ekler; mevcut veri değişmez.
CREATE TABLE IF NOT EXISTS sla_daily (
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id   bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    day        date NOT NULL,
    observed_s integer NOT NULL DEFAULT 0,
    down_s     integer NOT NULL DEFAULT 0,
    maint_s    integer NOT NULL DEFAULT 0,
    degraded_s integer NOT NULL DEFAULT 0,
    incidents  integer NOT NULL DEFAULT 0,
    PRIMARY KEY (asset_id, day)
);
CREATE INDEX IF NOT EXISTS sla_daily_tenant_day ON sla_daily (tenant_id, day);
ALTER TABLE assets ADD COLUMN IF NOT EXISTS sla_target double precision;
