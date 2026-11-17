-- v2.3.0: ajansız uygulama performansı (APM) görünürlüğü.
--  * db_statements / db_stmt_state : PostgreSQL pg_stat_statements özeti (yavaş sorgular)
--  * probes.last_timing           : HTTP gecikme kırılımı (DNS, TCP, TLS, ilk bayt, indirme)
--  * host_conns / host_addrs      : SSH ile okunan bağlantılar ve makine adresleri (bağımlılık önerisi)
--  * http_endpoint_stats          : syslog ile gelen erişim günlüklerinden uç nokta özetleri (5 dk dilimler)

ALTER TABLE assets DROP CONSTRAINT IF EXISTS assets_collection_check;
ALTER TABLE assets ADD CONSTRAINT assets_collection_check CHECK (collection IN ('agent', 'agentless', 'logs'));

ALTER TABLE probes ADD COLUMN IF NOT EXISTS last_timing jsonb NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS db_stmt_state (
    probe_id   bigint PRIMARY KEY REFERENCES probes(id) ON DELETE CASCADE,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    available  boolean NOT NULL DEFAULT false,
    note       text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS db_statements (
    probe_id    bigint NOT NULL REFERENCES probes(id) ON DELETE CASCADE,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    queryid     text NOT NULL,
    query       text NOT NULL,
    calls       bigint NOT NULL DEFAULT 0,
    total_ms    double precision NOT NULL DEFAULT 0,
    mean_ms     double precision NOT NULL DEFAULT 0,
    max_ms      double precision NOT NULL DEFAULT 0,
    rows_total  bigint NOT NULL DEFAULT 0,
    prev_calls  bigint NOT NULL DEFAULT 0,
    prev_total  double precision NOT NULL DEFAULT 0,
    prev_at     timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (probe_id, queryid)
);
CREATE INDEX IF NOT EXISTS db_statements_tenant ON db_statements (tenant_id);

CREATE TABLE IF NOT EXISTS host_conns (
    probe_id   bigint NOT NULL REFERENCES probes(id) ON DELETE CASCADE,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id   bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    direction  text NOT NULL CHECK (direction IN ('out', 'in')),
    peer_ip    text NOT NULL,
    port       integer NOT NULL,
    conns      integer NOT NULL,
    rtt_ms     double precision,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (probe_id, direction, peer_ip, port)
);
CREATE INDEX IF NOT EXISTS host_conns_tenant ON host_conns (tenant_id);

CREATE TABLE IF NOT EXISTS host_addrs (
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id  bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    ip        text NOT NULL,
    PRIMARY KEY (tenant_id, asset_id, ip)
);

CREATE TABLE IF NOT EXISTS http_endpoint_stats (
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id  bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    bucket    timestamptz NOT NULL,
    endpoint  text NOT NULL,
    reqs      bigint NOT NULL DEFAULT 0,
    err4xx    bigint NOT NULL DEFAULT 0,
    err5xx    bigint NOT NULL DEFAULT 0,
    sum_ms    double precision NOT NULL DEFAULT 0,
    max_ms    double precision NOT NULL DEFAULT 0,
    b0 bigint NOT NULL DEFAULT 0, b1 bigint NOT NULL DEFAULT 0, b2 bigint NOT NULL DEFAULT 0, b3 bigint NOT NULL DEFAULT 0,
    b4 bigint NOT NULL DEFAULT 0, b5 bigint NOT NULL DEFAULT 0, b6 bigint NOT NULL DEFAULT 0, b7 bigint NOT NULL DEFAULT 0,
    b8 bigint NOT NULL DEFAULT 0, b9 bigint NOT NULL DEFAULT 0, b10 bigint NOT NULL DEFAULT 0, b11 bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, asset_id, bucket, endpoint)
);
CREATE INDEX IF NOT EXISTS http_endpoint_stats_time ON http_endpoint_stats (tenant_id, bucket);
