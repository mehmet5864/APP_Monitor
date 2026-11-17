-- v2.2.0: kiracı genelinde izlemeyi duraklatma (probe, senaryo, Keşfet ve alarm değerlendirmesi).
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS paused boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS paused_at timestamptz;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS paused_by text NOT NULL DEFAULT '';
