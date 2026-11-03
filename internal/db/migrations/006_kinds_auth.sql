-- Topolojide ayrı şekillerle gösterilecek yeni asset türleri.
ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_kind_check;
ALTER TABLE assets ADD CONSTRAINT assets_kind_check
    CHECK (kind IN ('host', 'service', 'database', 'url', 'website', 'webservice', 'network', 'cache', 'queue'));

-- Tarayıcı senaryolarında kimlik doğrulama: yapılandırma (parola dahil, API'de gösterilmez) ve saklanan oturum çerezleri.
ALTER TABLE scenarios
    ADD COLUMN auth       jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN session    jsonb,
    ADD COLUMN session_at timestamptz;
