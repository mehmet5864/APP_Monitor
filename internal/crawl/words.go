package crawl

import (
	"embed"
	"strings"
)

//go:embed wordlists/*.txt
var wordFS embed.FS

// Lists kullanılabilir yerleşik sözlükler (kimlik: açıklama).
var Lists = []struct{ ID, Name string }{
	{"kisa", "Kısa — izleme için yaygın uç noktalar (sağlık, durum, metrik, API belgeleri, giriş sayfaları)"},
	{"genis", "Geniş — kısa liste + yaygın yönetim, destek ve belge yolları"},
}

// Words sözlük kimliği için yol listesini döndürür (yorum ve boş satırlar atılır, "/" ile başlar).
func Words(id string) []string {
	b, err := wordFS.ReadFile("wordlists/" + id + ".txt")
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		if !strings.HasPrefix(l, "/") {
			l = "/" + l
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return out
}
