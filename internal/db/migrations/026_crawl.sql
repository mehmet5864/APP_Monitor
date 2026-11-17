-- Web sitesi keşfi (Keşfet): iş kuyruğu, yönetici onayı ve sonuçlar.
CREATE TABLE IF NOT EXISTS crawl_jobs (
    id            bigserial PRIMARY KEY,
    tenant_id     bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    url           text NOT NULL,
    options       jsonb NOT NULL DEFAULT '{}',
    status        text NOT NULL DEFAULT 'pending',   -- pending | approved | running | done | failed | rejected | cancelled
    requested_by  text NOT NULL DEFAULT '',
    decided_by    text NOT NULL DEFAULT '',
    decided_at    timestamptz,
    note          text NOT NULL DEFAULT '',
    scenario_id   bigint REFERENCES scenarios(id) ON DELETE SET NULL,
    progress      text NOT NULL DEFAULT '',
    error         text NOT NULL DEFAULT '',
    result        jsonb,
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    finished_at   timestamptz
);
CREATE INDEX IF NOT EXISTS crawl_jobs_tenant_idx ON crawl_jobs (tenant_id, id DESC);
