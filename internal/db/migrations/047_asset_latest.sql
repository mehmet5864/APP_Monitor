-- Her (asset, metrik) için en son değer: arayüzün "son 10 dakikanın metriklerini tara" sorgularını ortadan kaldırır (v3.8.0).
CREATE TABLE IF NOT EXISTS asset_latest (
    asset_id  bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name      text NOT NULL,
    tenant_id bigint NOT NULL,
    value     double precision NOT NULL,
    time      timestamptz NOT NULL,
    PRIMARY KEY (asset_id, name)
);
CREATE INDEX IF NOT EXISTS asset_latest_tenant ON asset_latest (tenant_id, name);
INSERT INTO asset_latest (asset_id, name, tenant_id, value, time)
SELECT DISTINCT ON (asset_id, name) asset_id, name, tenant_id, value, time FROM metrics
 WHERE time > now() - interval '1 hour'
 ORDER BY asset_id, name, time DESC
ON CONFLICT DO NOTHING;
