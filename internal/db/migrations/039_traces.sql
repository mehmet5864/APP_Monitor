-- v3.0.0: OTLP dağıtık izleme. Her satır bir span; trace = aynı trace_id'li span'lar.
CREATE TABLE IF NOT EXISTS spans (
    tenant_id   bigint      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    trace_id    text        NOT NULL,
    span_id     text        NOT NULL,
    parent_id   text        NOT NULL DEFAULT '',
    service     text        NOT NULL,
    name        text        NOT NULL,
    kind        smallint    NOT NULL DEFAULT 0,   -- 0 belirsiz, 1 iç, 2 sunucu, 3 istemci, 4 üretici, 5 tüketici
    start_time  timestamptz NOT NULL,
    dur_us      bigint      NOT NULL DEFAULT 0,
    status      smallint    NOT NULL DEFAULT 0,   -- 0 belirsiz, 1 tamam, 2 hata
    status_msg  text        NOT NULL DEFAULT '',
    attrs       jsonb       NOT NULL DEFAULT '{}',
    PRIMARY KEY (tenant_id, trace_id, span_id)
);
CREATE INDEX IF NOT EXISTS spans_time_idx ON spans (tenant_id, start_time DESC);
CREATE INDEX IF NOT EXISTS spans_service_idx ON spans (tenant_id, service, start_time DESC);
