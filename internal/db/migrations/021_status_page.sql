-- Herkese açık durum sayfası: yalnızca işaretli asset'ler yayınlanır. Sütun ekler; veri değişmez.
ALTER TABLE assets ADD COLUMN IF NOT EXISTS public_status boolean NOT NULL DEFAULT false;
ALTER TABLE assets ADD COLUMN IF NOT EXISTS status_name text NOT NULL DEFAULT '';
