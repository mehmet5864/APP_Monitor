-- v2.0.0: çok kiracılı işletim. Mevcut tek "default" kiracı kök (platform) kiracı olur; hiçbir veri değişmez.
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS slug text;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS disabled boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS is_root boolean NOT NULL DEFAULT false;
UPDATE tenants SET slug = 'default', is_root = true WHERE name = 'default' AND slug IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS tenants_slug_uq ON tenants (lower(slug)) WHERE slug IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS tenants_root_uq ON tenants (is_root) WHERE is_root;

-- Kiracı başına kotalar (0 = sınırsız; min_interval_sec ve retention_days için 0 = sistem varsayılanı).
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS probe_limit integer NOT NULL DEFAULT 0 CHECK (probe_limit >= 0);
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS user_limit integer NOT NULL DEFAULT 0 CHECK (user_limit >= 0);
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS min_interval_sec integer NOT NULL DEFAULT 5 CHECK (min_interval_sec >= 5);
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS retention_days integer NOT NULL DEFAULT 0 CHECK (retention_days >= 0);
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS allow_browser boolean NOT NULL DEFAULT true;
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS allow_crawl boolean NOT NULL DEFAULT true;
ALTER TABLE licenses ADD COLUMN IF NOT EXISTS allow_ssh boolean NOT NULL DEFAULT true;

-- Kullanıcı adları tüm kiracılarda tekil: giriş ekranı kiracı seçtirmez.
CREATE UNIQUE INDEX IF NOT EXISTS users_username_global ON users (lower(username));
