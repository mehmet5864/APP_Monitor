-- Probe'un son çalıştırmasındaki hata mesajı (başarılıysa boş).
ALTER TABLE probes ADD COLUMN last_error text NOT NULL DEFAULT '';
