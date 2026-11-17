-- v2.1.0: imzalı lisans dosyası (.lic). Lisans dosyasıyla gelen kotalar elle değiştirilemez.
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS lic_id text NOT NULL DEFAULT '';
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS lic_customer text NOT NULL DEFAULT '';
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS lic_issued_at timestamptz;
CREATE TABLE IF NOT EXISTS platform_secrets (
    name  text PRIMARY KEY,
    value bytea NOT NULL
);
