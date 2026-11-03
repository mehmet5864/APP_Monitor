-- APP_MON ilk şema. Her tablo tenant_id taşır: SaaS'ta çok kiracılık, on-premise'de tek "default" kiracı.

CREATE TABLE tenants (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Lisans: izlenebilecek asset sayısını belirler.
CREATE TABLE licenses (
    tenant_id   bigint PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    mode        text NOT NULL CHECK (mode IN ('saas', 'onprem')),
    asset_limit integer NOT NULL CHECK (asset_limit >= 0),
    valid_until timestamptz,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE api_keys (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    label      text NOT NULL,
    role       text NOT NULL CHECK (role IN ('admin', 'agent')),
    key_hash   text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE assets (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    kind       text NOT NULL CHECK (kind IN ('host', 'service', 'database', 'url')),
    language   text NOT NULL DEFAULT 'none' CHECK (language IN ('none', 'dotnet', 'java', 'python', 'go')),
    collection text NOT NULL CHECK (collection IN ('agent', 'agentless')),
    status     text NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'up', 'down', 'offline')),
    last_seen  timestamptz,
    labels     jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE metrics (
    time      timestamptz NOT NULL,
    tenant_id bigint NOT NULL,
    asset_id  bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name      text NOT NULL,
    value     double precision NOT NULL
);
CREATE INDEX metrics_asset_name_time ON metrics (asset_id, name, time DESC);
CREATE INDEX metrics_time ON metrics (time);

CREATE TABLE probes (
    id           bigserial PRIMARY KEY,
    tenant_id    bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id     bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    type         text NOT NULL CHECK (type IN ('http', 'tcp', 'postgres')),
    target       text NOT NULL,
    interval_sec integer NOT NULL DEFAULT 30 CHECK (interval_sec >= 5),
    timeout_ms   integer NOT NULL DEFAULT 5000 CHECK (timeout_ms BETWEEN 100 AND 60000),
    enabled      boolean NOT NULL DEFAULT true,
    last_run     timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alert_rules (
    id         bigserial PRIMARY KEY,
    tenant_id  bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name       text NOT NULL,
    metric     text NOT NULL,
    comparator text NOT NULL CHECK (comparator IN ('>', '<')),
    threshold  double precision NOT NULL,
    window_sec integer NOT NULL DEFAULT 60 CHECK (window_sec >= 10),
    severity   text NOT NULL DEFAULT 'warning' CHECK (severity IN ('info', 'warning', 'critical')),
    enabled    boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE alerts (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    rule_id     bigint NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    asset_id    bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    state       text NOT NULL CHECK (state IN ('open', 'resolved')),
    value       double precision NOT NULL,
    opened_at   timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);
-- Bir kural + asset çifti için aynı anda tek açık alarm.
CREATE UNIQUE INDEX alerts_one_open ON alerts (rule_id, asset_id) WHERE state = 'open';

CREATE TABLE audit_log (
    id        bigserial PRIMARY KEY,
    tenant_id bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor     text NOT NULL,
    action    text NOT NULL,
    detail    text NOT NULL DEFAULT '',
    at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_tenant_at ON audit_log (tenant_id, at DESC);
