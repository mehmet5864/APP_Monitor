-- HTTP probe'larına gelişmiş seçenekler.
ALTER TABLE probes
    ADD COLUMN http_method      text    NOT NULL DEFAULT 'GET' CHECK (http_method IN ('GET','HEAD','POST','PUT','PATCH','DELETE','OPTIONS')),
    ADD COLUMN headers          jsonb   NOT NULL DEFAULT '{}',
    ADD COLUMN body             text    NOT NULL DEFAULT '',
    ADD COLUMN expect_status    text    NOT NULL DEFAULT '',
    ADD COLUMN expect_text      text    NOT NULL DEFAULT '',
    ADD COLUMN verify_tls       boolean NOT NULL DEFAULT true,
    ADD COLUMN follow_redirects boolean NOT NULL DEFAULT true;

-- Senaryolar: çok adımlı HTTP "bot" akışları. steps ve vars gizli değer içerebilir; API yanıtlarında gösterilmez.
CREATE TABLE scenarios (
    id               bigserial PRIMARY KEY,
    tenant_id        bigint  NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset_id         bigint  NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    name             text    NOT NULL,
    interval_sec     integer NOT NULL DEFAULT 300 CHECK (interval_sec >= 30),
    timeout_ms       integer NOT NULL DEFAULT 10000 CHECK (timeout_ms BETWEEN 500 AND 60000),
    enabled          boolean NOT NULL DEFAULT true,
    verify_tls       boolean NOT NULL DEFAULT true,
    vars             jsonb   NOT NULL DEFAULT '{}',
    steps            jsonb   NOT NULL,
    last_run         timestamptz,
    last_ok          boolean,
    last_error       text    NOT NULL DEFAULT '',
    last_duration_ms double precision,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, name)
);

CREATE TABLE scenario_runs (
    id          bigserial PRIMARY KEY,
    tenant_id   bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    scenario_id bigint NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
    at          timestamptz NOT NULL DEFAULT now(),
    ok          boolean NOT NULL,
    duration_ms double precision NOT NULL,
    error       text NOT NULL DEFAULT '',
    steps       jsonb NOT NULL DEFAULT '[]'
);
CREATE INDEX scenario_runs_scenario_at ON scenario_runs (scenario_id, at DESC);
CREATE INDEX scenario_runs_at ON scenario_runs (at);

-- Mevcut kiracılara senaryo alarm kuralları (yeni kurulumlarda başlangıç kurulumu ekler).
INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity)
SELECT t.id, 'Senaryo başarısız', 'scenario.up', '<', 1, 60, 'critical' FROM tenants t
WHERE NOT EXISTS (SELECT 1 FROM alert_rules r WHERE r.tenant_id = t.id AND r.metric = 'scenario.up');
INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity)
SELECT t.id, 'Yavaş senaryo', 'scenario.duration_ms', '>', 5000, 120, 'warning' FROM tenants t
WHERE NOT EXISTS (SELECT 1 FROM alert_rules r WHERE r.tenant_id = t.id AND r.metric = 'scenario.duration_ms');
