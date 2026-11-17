-- v2.7.6: ajanın okuduğu erişim günlüklerinde istek süresi olmayabilir (nginx/Apache varsayılan biçimi). untimed: süresiz istek sayısı;
-- ortalama gecikme (reqs - untimed) üzerinden hesaplanır.
ALTER TABLE http_endpoint_stats ADD COLUMN IF NOT EXISTS untimed bigint NOT NULL DEFAULT 0;
