-- Sayfa değişikliği alarmlarında yanlış pozitifleri azaltmak için: her parmak izi öğesinin kaç kez eklenip kaybolduğu.
-- İki veya daha çok kez gelip giden öğe (dönen slayt, canlı sayaç vb.) "dinamik" sayılır ve karşılaştırmada yok sayılır.
ALTER TABLE scenario_step_prints ADD COLUMN flips jsonb NOT NULL DEFAULT '{}';
