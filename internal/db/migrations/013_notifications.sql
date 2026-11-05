-- Aşama 1: bildirim kanalları, bakım pencereleri, alarm üstlenme, kural kapsamı, asset grup/etiketleri.
-- Yalnızca ekleme yapar; mevcut veri değişmez.
CREATE TABLE IF NOT EXISTS notification_channels (
    id           bigserial PRIMARY KEY,
    tenant_id    bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name         text NOT NULL,
    kind         text NOT NULL,
    config       jsonb NOT NULL DEFAULT '{}',
    enabled      boolean NOT NULL DEFAULT true,
    min_severity text NOT NULL DEFAULT 'warning' CHECK (min_severity IN ('info', 'warning', 'critical')),
    on_resolve   boolean NOT NULL DEFAULT true,
    last_error   text NOT NULL DEFAULT '',
    last_sent    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS maintenance_windows (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    starts_at  timestamptz NOT NULL,
    ends_at    timestamptz NOT NULL,
    repeat     text NOT NULL DEFAULT '' CHECK (repeat IN ('', 'daily', 'weekly')),
    asset_ids  bigint[] NOT NULL DEFAULT '{}',
    group_name text NOT NULL DEFAULT '',
    tag        text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE alerts ADD COLUMN IF NOT EXISTS ack_by text NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS ack_at timestamptz;
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS ack_note text NOT NULL DEFAULT '';
ALTER TABLE alerts ADD COLUMN IF NOT EXISTS silenced boolean NOT NULL DEFAULT false;

ALTER TABLE assets ADD COLUMN IF NOT EXISTS grp text NOT NULL DEFAULT '';
ALTER TABLE assets ADD COLUMN IF NOT EXISTS tags text[] NOT NULL DEFAULT '{}';

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS asset_ids bigint[] NOT NULL DEFAULT '{}';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS group_name text NOT NULL DEFAULT '';
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS tag text NOT NULL DEFAULT '';
