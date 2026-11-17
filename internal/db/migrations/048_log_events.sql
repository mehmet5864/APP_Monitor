-- v3.10.0: log–trace ilişkilendirme. Yalnızca seçilen seviyedeki (varsayılan uyarı ve üstü), kısaltılmış ve maskelenmiş
-- satırlar saklanır; APM_LOG_EVENTS=1 olmadıkça hiçbir şey yazılmaz.
CREATE TABLE IF NOT EXISTS log_events (
    id        bigserial   PRIMARY KEY,
    tenant_id bigint      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    time      timestamptz NOT NULL,
    service   text        NOT NULL DEFAULT '',
    host      text        NOT NULL DEFAULT '',
    level     smallint    NOT NULL DEFAULT 2,   -- 0 hata ayıklama, 1 bilgi, 2 uyarı, 3 hata, 4 ölümcül
    message   text        NOT NULL DEFAULT '',
    trace_id  text        NOT NULL DEFAULT '',
    span_id   text        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS log_events_trace_idx ON log_events (tenant_id, trace_id) WHERE trace_id <> '';
CREATE INDEX IF NOT EXISTS log_events_time_idx ON log_events (tenant_id, time DESC);
CREATE INDEX IF NOT EXISTS log_events_service_idx ON log_events (tenant_id, service, time DESC);
