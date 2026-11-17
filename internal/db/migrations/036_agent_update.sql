-- v2.7.5: ajanları uzaktan güncelleme. assets.agent_update_req: yönetici "güncelle" dedi; tenants.agent_auto_update: sunucudaki ajan dosyası farklıysa kendiliğinden güncelle.
ALTER TABLE assets ADD COLUMN IF NOT EXISTS agent_update_req boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS agent_auto_update boolean NOT NULL DEFAULT false;
