-- Topoloji grafı: asset'ler düğüm, bu tablo yönlü kenarlardır.
-- source_asset_id, target_asset_id'ye bağımlıdır (kaynak -> hedef). Örnek: odeme-api -> odeme-db.
-- Etki ve bağımlılık sorguları özyinelemeli CTE ile çalışır; ek bir eklenti gerekmez.

CREATE TABLE topology_edges (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_asset_id bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    target_asset_id bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    kind            text NOT NULL DEFAULT 'depends_on' CHECK (kind IN ('depends_on', 'runs_on')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    CHECK (source_asset_id <> target_asset_id),
    UNIQUE (source_asset_id, target_asset_id, kind)
);
CREATE INDEX topology_edges_tenant ON topology_edges (tenant_id);
CREATE INDEX topology_edges_target ON topology_edges (target_asset_id);
