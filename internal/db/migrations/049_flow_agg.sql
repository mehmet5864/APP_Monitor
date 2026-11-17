-- v3.11.0: NetFlow/IPFIX özetleri. Ham akış saklanmaz; her aralıkta en çok N konuşma (kalanı "diğer") yazılır.
CREATE TABLE IF NOT EXISTS flow_agg (
    tenant_id  bigint      NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    bucket     timestamptz NOT NULL,
    exporter   text        NOT NULL,
    src        text        NOT NULL,
    dst        text        NOT NULL,
    dst_port   integer     NOT NULL DEFAULT 0,
    proto      smallint    NOT NULL DEFAULT 0,
    bytes      bigint      NOT NULL DEFAULT 0,
    packets    bigint      NOT NULL DEFAULT 0,
    flows      integer     NOT NULL DEFAULT 0,
    src_asset  bigint      REFERENCES assets(id) ON DELETE SET NULL,
    dst_asset  bigint      REFERENCES assets(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS flow_agg_time_idx ON flow_agg (tenant_id, bucket DESC);
CREATE INDEX IF NOT EXISTS flow_agg_src_asset_idx ON flow_agg (src_asset, bucket DESC) WHERE src_asset IS NOT NULL;
CREATE INDEX IF NOT EXISTS flow_agg_dst_asset_idx ON flow_agg (dst_asset, bucket DESC) WHERE dst_asset IS NOT NULL;
