-- Kademeli ajan güncellemesi: kuyruk dalgalar halinde ilerler (v3.7.0).
CREATE TABLE IF NOT EXISTS agent_rollouts (
    id           bigserial PRIMARY KEY,
    tenant_id    bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    state        text NOT NULL DEFAULT 'running' CHECK (state IN ('running', 'paused', 'done', 'canceled')),
    batch        int NOT NULL DEFAULT 25,
    halt_on_fail boolean NOT NULL DEFAULT true,
    queue        bigint[] NOT NULL DEFAULT '{}',
    wave         bigint[] NOT NULL DEFAULT '{}',
    wave_no      int NOT NULL DEFAULT 0,
    wave_started timestamptz,
    total        int NOT NULL DEFAULT 0,
    done         int NOT NULL DEFAULT 0,
    failed       int NOT NULL DEFAULT 0,
    skipped      int NOT NULL DEFAULT 0,
    note         text NOT NULL DEFAULT '',
    created_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS agent_rollouts_active ON agent_rollouts (tenant_id) WHERE state IN ('running', 'paused');
