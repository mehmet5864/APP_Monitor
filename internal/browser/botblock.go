package browser

import "strings"

// botMarkers bot koruması / WAF engel sayfalarında görülen kalıplardır (küçük harfli).
var botMarkers = []string{
	"güvensiz olarak algılandığı", "bağlantınız güvensiz", // Türkiye e-ticaret WAF sayfaları
	"verify you are human", "verifying you are human", "checking if the site connection is secure", // Cloudflare
	"attention required! | cloudflare", "just a moment...",
	"pardon our interruption", "request unsuccessful. incapsula", // Imperva
	"press & hold to confirm you are", "px-captcha", // HUMAN/PerimeterX
	"unusual traffic from your computer", "automated queries",
	"access denied", "you don't have permission to access", // Akamai (reference # ile birlikte)
	"are you a robot", "robot olmadığınızı", "bot olmadığınızı",
}

// BotBlocked sayfa metni veya başlığı bir bot koruması engel sayfasına benziyorsa kısa bir açıklama döndürür.
// Yanlış alarmı azaltmak için kalıplar yalnızca kısa sayfalarda (engel sayfaları kısadır) aranır.
func BotBlocked(title, text string, status int) string {
	t := strings.ToLower(title + "\n" + text)
	if len(text) > 4000 {
		return ""
	}
	for _, m := range botMarkers {
		if strings.Contains(t, m) {
			return "Bot koruması engelledi: site, APP_MON'un tarayıcısını otomasyon olarak algılayıp sayfayı göstermedi. Bu bir sertifika/ağ sorunu değildir; sitenin sahibinden APP_MON sunucusunun IP'sini izin listesine almasını isteyin (üçüncü taraf sitelerde tarayıcı senaryosu yerine HTTP probe kullanın)"
		}
	}
	if (status == 403 || status == 429) && len(text) < 1500 {
		return "Site HTTP " + map[int]string{403: "403", 429: "429"}[status] + " döndürdü; olası bot koruması / hız sınırı. Sitenin sahibinden APP_MON sunucusunun IP'sini izin listesine almasını isteyin"
	}
	return ""
}
