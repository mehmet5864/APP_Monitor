package browser

import "testing"

func TestBotBlocked(t *testing.T) {
	if BotBlocked("", "Bağlantınız güvensiz olarak algılandığı için siteye erişemiyorsunuz.\nReferans Kodu: 0.49", 200) == "" {
		t.Error("Türkçe engel sayfası algılanmadı")
	}
	if BotBlocked("Just a moment...", "Checking if the site connection is secure", 403) == "" {
		t.Error("Cloudflare algılanmadı")
	}
	if BotBlocked("", "Forbidden", 403) == "" {
		t.Error("kısa 403 algılanmadı")
	}
	if BotBlocked("Ürünler", "Sepetim Kampanyalar Giriş yap", 200) != "" {
		t.Error("normal sayfa yanlış algılandı")
	}
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'a'
	}
	if BotBlocked("", "access denied "+string(long), 200) != "" {
		t.Error("uzun sayfada algılanmamalı")
	}
}
