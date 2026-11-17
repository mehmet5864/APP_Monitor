-- v3.14.0: saldırı yüzeyi envanteri. Ajanın gönderdiği dinleyen portlar (süreç, çalışma zamanı, sürüm) ve uygulama
-- bağımlılıkları (yalnızca paket adı + sürüm). Her gönderimde asset'in kaydı yenilenir; ham dosya içeriği saklanmaz.
CREATE TABLE IF NOT EXISTS asset_inventory (
    asset_id   bigint      PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
    tenant_id  bigint      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    listeners  jsonb       NOT NULL DEFAULT '[]',
    deps       jsonb       NOT NULL DEFAULT '[]',
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS asset_inventory_tenant_idx ON asset_inventory (tenant_id);
