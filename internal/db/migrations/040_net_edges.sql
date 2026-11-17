-- v3.3.0: ajanın bildirdiği giden TCP bağlantıları (süreç → süreç / adres) ve makine IP'leri; bağlantı tabanlı servis haritası.
CREATE TABLE IF NOT EXISTS net_edges (
    tenant_id  bigint      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id   bigint      NOT NULL REFERENCES assets(id)  ON DELETE CASCADE,
    src        text        NOT NULL,
    dst        text        NOT NULL DEFAULT '',
    addr       text        NOT NULL,
    port       integer     NOT NULL,
    conns      integer     NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, src, dst, addr, port)
);
CREATE INDEX IF NOT EXISTS net_edges_tenant_idx ON net_edges (tenant_id, updated_at DESC);
CREATE TABLE IF NOT EXISTS net_hosts (
    asset_id   bigint      PRIMARY KEY REFERENCES assets(id) ON DELETE CASCADE,
    tenant_id  bigint      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    ips        text[]      NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now()
);
