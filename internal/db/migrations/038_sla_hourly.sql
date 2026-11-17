-- SLA için saatlik özet: çalışma saati (ör. 08-17 hafta içi, 07-24) filtreleri ve saatlik/günlük/haftalık kırılım buradan hesaplanır.
-- hod/dow, saatin yerel (sunucu saat dilimi) saati ve haftanın günüdür (0 = Pazar).
CREATE TABLE IF NOT EXISTS sla_hourly (
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id   bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    hr         timestamptz NOT NULL,
    hod        smallint NOT NULL,
    dow        smallint NOT NULL,
    observed_s integer NOT NULL DEFAULT 0,
    down_s     integer NOT NULL DEFAULT 0,
    maint_s    integer NOT NULL DEFAULT 0,
    degraded_s integer NOT NULL DEFAULT 0,
    incidents  integer NOT NULL DEFAULT 0,
    PRIMARY KEY (asset_id, hr)
);
CREATE INDEX IF NOT EXISTS sla_hourly_tenant_hr ON sla_hourly (tenant_id, hr);

-- Yönetici SLA'yı sıfırladığında asset için hesaplamaya bu andan itibaren başlanır (önceki ölçümler SLA'ya katılmaz).
ALTER TABLE assets ADD COLUMN IF NOT EXISTS sla_reset_at timestamptz;
