-- v2.6.0: kanıt zinciri.
--  * audit_log.prev_hash/hash : kiracı başına SHA-256 hash zinciri (her kayıt bir öncekine bağlanır)
--  * audit_head               : kiracının zincir başı (son kayıt kimliği, hash, sayı); kuyruk silinmesini yakalar
--  * evidence_keys            : rapor imzalama anahtarı (Ed25519); özel anahtar kasa ile şifreli
--  * evidence_reports         : üretilmiş imzalı kanıt raporları

ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS prev_hash text;
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS hash text;

CREATE TABLE IF NOT EXISTS audit_head (
    tenant_id  bigint PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    last_id    bigint NOT NULL,
    hash       text NOT NULL,
    entries    bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS evidence_keys (
    id             bigserial PRIMARY KEY,
    key_id         text NOT NULL UNIQUE,
    public_key     bytea NOT NULL,
    private_sealed bytea NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS evidence_reports (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    label       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    created_by  text NOT NULL DEFAULT '',
    head_id     bigint NOT NULL DEFAULT 0,
    head_hash   text NOT NULL DEFAULT '',
    key_id      text NOT NULL,
    report_json text NOT NULL,
    signature   text NOT NULL
);
CREATE INDEX IF NOT EXISTS evidence_reports_tenant ON evidence_reports (tenant_id, created_at DESC);
