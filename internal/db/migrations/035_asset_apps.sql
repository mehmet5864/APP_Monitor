-- Ajanın keşfettiği uygulamalar (süreç/servis/port) ve otomatik günlük özeti: asset başına son anlık görüntü.
CREATE TABLE IF NOT EXISTS asset_apps (
    asset_id   bigint PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
    tenant_id  bigint NOT NULL,
    apps       jsonb  NOT NULL DEFAULT '[]'::jsonb,
    logs       jsonb  NOT NULL DEFAULT '[]'::jsonb,
    updated_at timestamptz NOT NULL DEFAULT now()
);
