-- v2.5.0: saldırı yüzeyi sapması. Referans (baseline) durum ile sonraki gözlemler karşılaştırılır.
--  * surface_state  : (asset, tür) için ilk gözlem zamanı (öğrenme süresi başlangıcı)
--  * surface_items  : referans kümesi (dinleyen portlar, uç noktalar, TLS sürümü, güvenlik başlıkları, banner)
--  * surface_events : tespit edilen sapmalar (eklendi/kaldırıldı/patlama); gözden geçirilince reviewed olur

CREATE TABLE IF NOT EXISTS surface_state (
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id   bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    kind       text NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, asset_id, kind)
);

CREATE TABLE IF NOT EXISTS surface_items (
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id   bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    kind       text NOT NULL,
    item       text NOT NULL,
    first_seen timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now(),
    missed     integer NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, asset_id, kind, item)
);

CREATE TABLE IF NOT EXISTS surface_events (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id    bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    item        text NOT NULL,
    change      text NOT NULL CHECK (change IN ('added', 'removed', 'burst')),
    severity    text NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    detail      text NOT NULL DEFAULT '',
    at          timestamptz NOT NULL DEFAULT now(),
    reviewed    boolean NOT NULL DEFAULT false,
    reviewed_by text NOT NULL DEFAULT '',
    reviewed_at timestamptz,
    note        text NOT NULL DEFAULT ''
);
-- Aynı sapma gözden geçirilene kadar tek kayıt olur.
CREATE UNIQUE INDEX IF NOT EXISTS surface_events_open ON surface_events (tenant_id, asset_id, kind, item, change) WHERE NOT reviewed;
CREATE INDEX IF NOT EXISTS surface_events_tenant_at ON surface_events (tenant_id, at DESC);
