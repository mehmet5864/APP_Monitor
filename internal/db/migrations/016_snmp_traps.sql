-- SNMP trap kayıtları. Yalnızca yeni tablo ekler; mevcut veri değişmez.
CREATE TABLE IF NOT EXISTS snmp_traps (
    id        bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id  bigint REFERENCES assets(id) ON DELETE SET NULL,
    at        timestamptz NOT NULL DEFAULT now(),
    source    text NOT NULL,
    version   integer NOT NULL,
    trap_oid  text NOT NULL DEFAULT '',
    vars      jsonb NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS snmp_traps_tenant_at ON snmp_traps (tenant_id, at DESC);
