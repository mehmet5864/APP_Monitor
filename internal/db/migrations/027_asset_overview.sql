-- Genel bakıştaki "Makineler · CPU / RAM / Disk" bölümünde hangi asset'lerin görüneceği.
ALTER TABLE assets ADD COLUMN IF NOT EXISTS show_overview boolean NOT NULL DEFAULT true;
