-- v3.5.0: ajanın (ajan 1.4.0) gönderdiği veritabanı sorgu istatistikleri ve makine bağlantı özetleri (Performans sayfası).
CREATE TABLE IF NOT EXISTS agent_db_state (
    asset_id   bigint      NOT NULL REFERENCES assets(id)  ON DELETE CASCADE,
    tenant_id  bigint      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    db         text        NOT NULL DEFAULT '',
    available  boolean     NOT NULL DEFAULT false,
    note       text        NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, db)
);

CREATE TABLE IF NOT EXISTS agent_db_statements (
    asset_id    bigint NOT NULL REFERENCES assets(id)  ON DELETE CASCADE,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    db          text   NOT NULL DEFAULT '',
    queryid     text   NOT NULL,
    query       text   NOT NULL,
    calls       bigint NOT NULL DEFAULT 0,
    total_ms    double precision NOT NULL DEFAULT 0,
    mean_ms     double precision NOT NULL DEFAULT 0,
    max_ms      double precision NOT NULL DEFAULT 0,
    rows_total  bigint NOT NULL DEFAULT 0,
    prev_calls  bigint NOT NULL DEFAULT 0,
    prev_total  double precision NOT NULL DEFAULT 0,
    prev_at     timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, db, queryid)
);
CREATE INDEX IF NOT EXISTS agent_db_statements_tenant ON agent_db_statements (tenant_id);

CREATE TABLE IF NOT EXISTS agent_host_conns (
    asset_id   bigint  NOT NULL REFERENCES assets(id)  ON DELETE CASCADE,
    tenant_id  bigint  NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    direction  text    NOT NULL CHECK (direction IN ('out', 'in')),
    peer_ip    text    NOT NULL,
    port       integer NOT NULL,
    conns      integer NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, direction, peer_ip, port)
);
CREATE INDEX IF NOT EXISTS agent_host_conns_tenant ON agent_host_conns (tenant_id);
