-- Hesap kilitlenmesini önlemek için art arda başarısız otomatik girişler sayılır; sınıra ulaşınca giriş durdurulur.
ALTER TABLE scenarios ADD COLUMN login_fails integer NOT NULL DEFAULT 0;
ALTER TABLE scenarios ADD COLUMN auth_blocked boolean NOT NULL DEFAULT false;
