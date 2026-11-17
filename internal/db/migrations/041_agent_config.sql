-- v3.5.0: ajan başına uzaktan veri ayarı (hangi veri türleri gönderilsin). Satır yoksa ajan yerel yapılandırmasını kullanır.
CREATE TABLE IF NOT EXISTS agent_config (
    asset_id   bigint      PRIMARY KEY REFERENCES assets(id)  ON DELETE CASCADE,
    tenant_id  bigint      NOT NULL    REFERENCES tenants(id) ON DELETE CASCADE,
    send       text        NOT NULL DEFAULT '',   -- virgüllü SEND_* anahtarları
    version    integer     NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by text        NOT NULL DEFAULT ''
);
