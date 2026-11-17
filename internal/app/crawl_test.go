package app

import (
	"context"
	"strings"
	"testing"
)

func TestCrawlValidate(t *testing.T) {
	s := &Server{}
	ok := func(r crawlReq) error { return s.crawlValidate(context.Background(), 1, &r) }
	base := crawlReq{URL: "https://ornek.com", crawlOpts: crawlOpts{Wordlist: "kisa", Declared: true}}
	if err := ok(base); err != nil {
		t.Fatalf("geçerli istek reddedildi: %v", err)
	}
	cases := map[string]func(r *crawlReq){
		"beyan":    func(r *crawlReq) { r.Declared = false },
		"seçenek":  func(r *crawlReq) { r.Wordlist = "" },
		"adres":    func(r *crawlReq) { r.URL = "ftp://x" },
		"sözlük":   func(r *crawlReq) { r.Wordlist = "yok" },
		"tarayıcı": func(r *crawlReq) { r.Spider = true }, // s.Browser nil
		"ek yol":   func(r *crawlReq) { r.Extra = []string{"/a b"} },
		"oturumlu": func(r *crawlReq) { r.ScenarioID = 5 }, // spider kapalı
	}
	for name, mod := range cases {
		r := base
		mod(&r)
		if err := ok(r); err == nil {
			t.Errorf("%s: hata beklenirdi", name)
		}
	}
	r := base
	r.MaxPages, r.MaxDepth, r.DelayMS = 9999, 99, 1
	if err := s.crawlValidate(context.Background(), 1, &r); err != nil || r.MaxPages != 100 || r.MaxDepth != 3 || r.DelayMS != 500 {
		t.Errorf("sınırlar uygulanmadı: %+v %v", r, err)
	}
	if got := crawlSummary(crawlOpts{Spider: true, Wordlist: "kisa"}); !strings.Contains(got, "gezgin") {
		t.Error(got)
	}
}
