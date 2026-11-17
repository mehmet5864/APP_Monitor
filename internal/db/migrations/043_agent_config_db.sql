-- v3.5.1: ajan başına isteğe bağlı veritabanı bağlantısı (kullanıcı/parola). Parola Kasa ile şifrelenir (Seal), arayüze geri verilmez.
ALTER TABLE agent_config ADD COLUMN IF NOT EXISTS db_conf jsonb;
ALTER TABLE agent_config ADD COLUMN IF NOT EXISTS db_pass bytea;
