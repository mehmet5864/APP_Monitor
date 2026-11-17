-- Kullanıcı hesapları (yerel parola veya LDAP) ve süreli oturum anahtarları. Mevcut API anahtarları aynen çalışır.
CREATE TABLE IF NOT EXISTS users (
    id           bigserial PRIMARY KEY,
    tenant_id    bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    username     text NOT NULL,
    pass_hash    text NOT NULL DEFAULT '',
    role         text NOT NULL CHECK (role IN ('admin', 'operator', 'viewer')),
    source       text NOT NULL DEFAULT 'local' CHECK (source IN ('local', 'ldap')),
    enabled      boolean NOT NULL DEFAULT true,
    fail_count   integer NOT NULL DEFAULT 0,
    locked_until timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_login   timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS users_tenant_username ON users (tenant_id, lower(username));
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS user_id bigint REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS expires_at timestamptz;
