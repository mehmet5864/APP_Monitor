-- Yerleşik kasa: şifreli gizli değerler (AES-256-GCM; ana anahtar veritabanı dışındadır).
CREATE TABLE IF NOT EXISTS secrets (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name        text NOT NULL,
    kind        text NOT NULL DEFAULT 'ssh_key',   -- ssh_key | password | token
    note        text NOT NULL DEFAULT '',
    blob        bytea NOT NULL,
    key_id      text NOT NULL DEFAULT '',          -- şifrelemede kullanılan ana anahtarın parmak izi
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);
