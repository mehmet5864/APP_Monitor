-- Tarayıcı (gerçek Chromium) senaryoları: kayıt, periyodik oynatma, sayfa parmak izi farkı, ağ bağımlılıkları.
ALTER TABLE scenarios
    ADD COLUMN kind            text NOT NULL DEFAULT 'http' CHECK (kind IN ('http', 'browser')),
    ADD COLUMN last_changed_at timestamptz;

-- Adım başına son sayfa yapısı parmak izi (bir sonraki çalıştırmayla karşılaştırmak için taban çizgisi).
CREATE TABLE scenario_step_prints (
    scenario_id bigint  NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
    step_idx    integer NOT NULL,
    hash        text    NOT NULL,
    items       jsonb   NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scenario_id, step_idx)
);

-- Adım ekran görüntüleri (yalnızca son çalıştırmalar için tutulur).
CREATE TABLE scenario_shots (
    run_id   bigint  NOT NULL REFERENCES scenario_runs(id) ON DELETE CASCADE,
    step_idx integer NOT NULL,
    jpeg     bytea   NOT NULL,
    PRIMARY KEY (run_id, step_idx)
);

-- Senaryo çalışırken sayfanın ağ üzerinden eriştiği alan adları (otomatik keşfedilen bağımlılıklar).
CREATE TABLE scenario_hosts (
    scenario_id bigint  NOT NULL REFERENCES scenarios(id) ON DELETE CASCADE,
    host        text    NOT NULL,
    requests    integer NOT NULL DEFAULT 0,
    failed      integer NOT NULL DEFAULT 0,
    errors      integer NOT NULL DEFAULT 0,
    last_seen   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scenario_id, host)
);

INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity)
SELECT t.id, 'Sayfa yapısı değişti', 'scenario.changes', '>', 0, 300, 'info' FROM tenants t
WHERE NOT EXISTS (SELECT 1 FROM alert_rules r WHERE r.tenant_id = t.id AND r.metric = 'scenario.changes');
