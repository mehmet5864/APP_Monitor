-- Parmak izi biçimi değişince (örn. sürüm yükseltmesi) eski taban çizgisiyle karşılaştırıp sahte "sayfa değişti" üretmemek için.
ALTER TABLE scenario_step_prints ADD COLUMN fp_version integer NOT NULL DEFAULT 1;
