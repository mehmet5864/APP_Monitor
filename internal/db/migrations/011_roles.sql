-- Yetki rolleri: admin (yönetici), operator (işletmeci), viewer (izleyici), agent (yalnızca veri gönderir).
-- Mevcut anahtarlar ve veriler aynen korunur; yalnızca izin verilen rol listesi genişletilir.
DO $$
DECLARE c text;
BEGIN
  FOR c IN SELECT conname FROM pg_constraint
           WHERE conrelid = 'api_keys'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%role%'
  LOOP
    EXECUTE format('ALTER TABLE api_keys DROP CONSTRAINT %I', c);
  END LOOP;
END $$;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_role_check CHECK (role IN ('admin', 'operator', 'viewer', 'agent'));
