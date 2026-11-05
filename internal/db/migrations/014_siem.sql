-- Aşama 1: SIEM'e log iletimi. Yalnızca yeni tablo ekler; mevcut veri değişmez.
CREATE TABLE IF NOT EXISTS siem_targets (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    config     jsonb NOT NULL DEFAULT '{}',
    enabled    boolean NOT NULL DEFAULT true,
    last_error text NOT NULL DEFAULT '',
    last_sent  timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
