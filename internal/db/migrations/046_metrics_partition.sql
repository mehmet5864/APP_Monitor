-- metrics tablosunu zaman aralığına göre bölümlü (partition) tabloya çevirir (v3.8.0).
-- Eski tablo "metrics_legacy" adıyla, bugünün sonuna kadar olan bölüm olarak bağlanır; veri taşınmaz ve kaybolmaz.
-- Yeni günler günlük bölümlere yazılır; saklama süresi dolan bölümler DROP ile anında silinir.
-- Not: eski tabloda CHECK doğrulaması ve yabancı anahtar doğrulaması için tablo bir kez taranır; çok büyük kurulumlarda
-- bu adım dakikalar sürebilir (sunucu açılırken, trafik almadan önce çalışır). Güncellemeden önce yedek alın.
CREATE OR REPLACE FUNCTION appmon_metrics_ensure(ahead int) RETURNS int AS $$
DECLARE
    last_hi timestamptz;
    nxt     timestamptz;
    nm      text;
    created int := 0;
BEGIN
    SELECT max((regexp_match(pg_get_expr(c.relpartbound, c.oid), 'TO \(''([^'']+)''\)'))[1]::timestamptz) INTO last_hi
      FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
     WHERE i.inhparent = 'metrics'::regclass AND pg_get_expr(c.relpartbound, c.oid) <> 'DEFAULT';
    IF last_hi IS NULL THEN
        last_hi := date_trunc('day', now());
    END IF;
    WHILE last_hi < date_trunc('day', now()) + make_interval(days => ahead + 1) LOOP
        nxt := last_hi + interval '1 day';
        nm := 'metrics_p' || to_char(last_hi AT TIME ZONE 'UTC', 'YYYYMMDD_HH24');
        EXECUTE format('CREATE TABLE IF NOT EXISTS %I PARTITION OF metrics FOR VALUES FROM (%L) TO (%L)', nm, last_hi, nxt);
        last_hi := nxt;
        created := created + 1;
    END LOOP;
    RETURN created;
END
$$ LANGUAGE plpgsql;

-- Üst sınırı cutoff'tan küçük olan bölümleri siler (DROP); silinen bölüm sayısını döndürür.
CREATE OR REPLACE FUNCTION appmon_metrics_drop_before(cutoff timestamptz) RETURNS int AS $$
DECLARE
    r       record;
    dropped int := 0;
BEGIN
    FOR r IN
        SELECT c.oid::regclass::text AS rel,
               (regexp_match(pg_get_expr(c.relpartbound, c.oid), 'TO \(''([^'']+)''\)'))[1]::timestamptz AS hi
          FROM pg_inherits i JOIN pg_class c ON c.oid = i.inhrelid
         WHERE i.inhparent = 'metrics'::regclass AND pg_get_expr(c.relpartbound, c.oid) <> 'DEFAULT'
    LOOP
        IF r.hi IS NOT NULL AND r.hi < cutoff THEN
            EXECUTE 'DROP TABLE ' || r.rel;
            dropped := dropped + 1;
        END IF;
    END LOOP;
    RETURN dropped;
END
$$ LANGUAGE plpgsql;

DO $mig$
DECLARE
    cut timestamptz := date_trunc('day', now()) + interval '1 day';
    fk  record;
BEGIN
    IF (SELECT relkind FROM pg_class WHERE oid = 'metrics'::regclass) = 'p' THEN
        RETURN; -- zaten bölümlü
    END IF;
    ALTER TABLE metrics RENAME TO metrics_legacy;
    ALTER INDEX IF EXISTS metrics_asset_name_time RENAME TO metrics_legacy_asset_name_time;
    ALTER INDEX IF EXISTS metrics_time RENAME TO metrics_legacy_time;
    ALTER INDEX IF EXISTS metrics_tenant_name_time RENAME TO metrics_legacy_tenant_name_time;
    FOR fk IN SELECT conname FROM pg_constraint WHERE conrelid = 'metrics_legacy'::regclass AND contype = 'f' LOOP
        EXECUTE format('ALTER TABLE metrics_legacy DROP CONSTRAINT %I', fk.conname);
    END LOOP;
    EXECUTE format('ALTER TABLE metrics_legacy ADD CONSTRAINT metrics_legacy_time_chk CHECK (time < %L)', cut);

    CREATE TABLE metrics (
        time      timestamptz NOT NULL,
        tenant_id bigint NOT NULL,
        asset_id  bigint NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
        name      text NOT NULL,
        value     double precision NOT NULL
    ) PARTITION BY RANGE (time);
    CREATE INDEX metrics_asset_name_time ON metrics (asset_id, name, time DESC);
    CREATE INDEX metrics_tenant_name_time ON metrics (tenant_id, name, time DESC);
    CREATE INDEX metrics_time ON metrics (time);

    EXECUTE format('ALTER TABLE metrics ATTACH PARTITION metrics_legacy FOR VALUES FROM (MINVALUE) TO (%L)', cut);
    ALTER TABLE metrics_legacy DROP CONSTRAINT metrics_legacy_time_chk;
    CREATE TABLE metrics_default PARTITION OF metrics DEFAULT;
END
$mig$;

SELECT appmon_metrics_ensure(7);
