-- Senaryo kartında hızlı durum için isteğe bağlı probe eşleşmesi; probe ardışık hata sayacı.
ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS probe_id bigint REFERENCES probes(id) ON DELETE SET NULL;
ALTER TABLE probes ADD COLUMN IF NOT EXISTS fail_streak integer NOT NULL DEFAULT 0;
