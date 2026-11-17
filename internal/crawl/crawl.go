// Package crawl bir web sitesini (gerçek tarayıcıyla, robots.txt'e uyarak, sınırlı sayıda sayfa gezerek) keşfeder:
// sayfalar, formlar, yüklenen kaynaklar, arka plan API çağrıları, dış alan adları ve (isteğe bağlı) yaygın yol sözlüğü.
// Hiçbir form gönderilmez, hiçbir öğeye tıklanmaz; yalnızca GET ile gezilir. Güvenlik açığı taraması yapmaz.
package crawl

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"appmon/internal/browser"
	"appmon/internal/cdp"
	"appmon/internal/probe"
)

const (
	AgentName  = "appmon"
	UATag      = "APP_MON-Discovery/1"
	MaxPagesHi = 100
	MaxDepthHi = 3
)

// Config tek bir keşif çalıştırmasının ayarıdır.
type Config struct {
	Start        string
	Spider       bool
	Wordlist     string   // "", "kisa", "genis"
	Extra        []string // kullanıcının eklediği yollar
	MaxPages     int
	MaxDepth     int
	Delay        time.Duration
	BlockPrivate bool
	Chrome       cdp.Options
	// Oturumlu tarama (isteğe bağlı): senaryonun giriş ayarı.
	Auth    *browser.Auth
	Steps   []browser.Step
	Vars    map[string]string
	Cookies []browser.Cookie
	Timeout time.Duration // adım zaman aşımı
	// Progress ilerlemeyi bildirir.
	Progress func(msg string)
	// OnLogin giriş denemesinin sonucunu bildirir (hesap kilidi sayaçları için).
	OnLogin func(failed bool)
}

type Form struct {
	Action   string   `json:"action"`
	Method   string   `json:"method"`
	Fields   []string `json:"fields"`
	Password bool     `json:"password"`
}

type Page struct {
	URL    string   `json:"url"`
	Title  string   `json:"title"`
	Status int      `json:"status"`
	Depth  int      `json:"depth"`
	MS     float64  `json:"ms"`
	Links  int      `json:"links"`
	Error  string   `json:"error,omitempty"`
	Forms  []Form   `json:"forms,omitempty"`
	Frames []string `json:"frames,omitempty"`
	Auth   bool     `json:"auth,omitempty"` // oturum açıkken bulundu
}

type Resource struct {
	URL    string  `json:"url"`
	Type   string  `json:"type"`
	Host   string  `json:"host"`
	Status int     `json:"status"`
	Bytes  int64   `json:"bytes"`
	MS     float64 `json:"ms"`
	Pages  int     `json:"pages"`
	Failed bool    `json:"failed,omitempty"`
	Third  bool    `json:"third"`
}

type Endpoint struct {
	Method string  `json:"method"`
	URL    string  `json:"url"`
	Status int     `json:"status"`
	MS     float64 `json:"ms"`
	Count  int     `json:"count"`
	Third  bool    `json:"third"`
}

type HostInfo struct {
	Host     string `json:"host"`
	Requests int    `json:"requests"`
	Failed   int    `json:"failed"`
	Errors   int    `json:"errors"`
	Third    bool   `json:"third"`
	TLSDays  *int   `json:"tls_days,omitempty"`
}

type Hit struct {
	Path     string `json:"path"`
	Status   int    `json:"status"`
	Bytes    int64  `json:"bytes"`
	Redirect string `json:"redirect,omitempty"`
}

type Result struct {
	Start         string     `json:"start"`
	Robots        string     `json:"robots"` // "uyuldu", "yok (izin)", "alınamadı"
	RobotsSkipped []string   `json:"robots_skipped,omitempty"`
	Pages         []Page     `json:"pages"`
	Resources     []Resource `json:"resources"`
	Endpoints     []Endpoint `json:"endpoints"`
	Hosts         []HostInfo `json:"hosts"`
	Hits          []Hit      `json:"hits,omitempty"`
	Truncated     bool       `json:"truncated,omitempty"`
	Notes         []string   `json:"notes,omitempty"`
	Authed        bool       `json:"authed,omitempty"`
	Seconds       float64    `json:"seconds"`
}

var (
	// Oturumu kapatan veya veri değiştirebilecek bağlantılar ASLA gezilmez (GET olsa bile).
	dangerRe = regexp.MustCompile(`(?i)(log-?out|sign-?out|sign-?off|cikis|çıkış|oturumu-?kapat|delete|remove|destroy|unsubscribe|revoke|deactivate|disable|reset-?password|/purge|/wipe)`)
	skipExt  = map[string]bool{".pdf": true, ".zip": true, ".gz": true, ".tar": true, ".rar": true, ".7z": true, ".exe": true, ".dmg": true, ".msi": true,
		".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".svg": true, ".webp": true, ".ico": true, ".css": true, ".js": true, ".map": true,
		".woff": true, ".woff2": true, ".ttf": true, ".eot": true, ".mp4": true, ".mp3": true, ".avi": true, ".mov": true, ".webm": true,
		".xls": true, ".xlsx": true, ".doc": true, ".docx": true, ".ppt": true, ".pptx": true, ".csv": true, ".xml": true, ".json": true, ".txt": true, ".iso": true}
)

const pageJS = `(() => {
  const abs = u => { try { return new URL(u, location.href).href } catch (e) { return '' } };
  const links = [...document.querySelectorAll('a[href],area[href]')].map(a => abs(a.getAttribute('href'))).filter(Boolean);
  const forms = [...document.forms].slice(0, 20).map(f => ({
    action: abs(f.getAttribute('action') || location.href), method: (f.getAttribute('method') || 'get').toLowerCase(),
    password: !!f.querySelector('input[type=password]'),
    fields: [...f.elements].filter(e => e.name && !['hidden', 'submit', 'button', 'reset', 'image'].includes(e.type)).slice(0, 20).map(e => e.type || e.tagName.toLowerCase())
  }));
  const frames = [...document.querySelectorAll('iframe[src]')].slice(0, 20).map(i => abs(i.getAttribute('src'))).filter(Boolean);
  return { title: (document.title || '').slice(0, 200), links: [...new Set(links)].slice(0, 600), forms, frames };
})()`

// keyOf aynı yolun yalnızca sorgu değeri farklı varyantlarını tek sayar (takvim/sayfalama sonsuz döngülerine karşı).
func keyOf(u *url.URL) string {
	k := u.Scheme + "://" + u.Host + u.EscapedPath()
	if u.RawQuery != "" {
		var ks []string
		for q := range u.Query() {
			ks = append(ks, q)
		}
		sort.Strings(ks)
		k += "?" + strings.Join(ks, "&")
	}
	return k
}

// Norm bağlantıyı başlangıç adresine göre normalleştirir; http(s) dışı ve parça (#) içerenleri temizler.
func Norm(raw string, base *url.URL) *url.URL {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil
	}
	u.Fragment, u.User = "", nil
	u.Host = strings.ToLower(u.Host)
	if h, p, err := net.SplitHostPort(u.Host); err == nil && ((u.Scheme == "http" && p == "80") || (u.Scheme == "https" && p == "443")) {
		u.Host = h
	}
	if u.Path == "" {
		u.Path = "/"
	}
	return u
}

func sameSite(a, b string) bool {
	ha, hb := strings.Split(strings.TrimSuffix(a, "."), ":")[0], strings.Split(strings.TrimSuffix(b, "."), ":")[0]
	if ha == hb {
		return true
	}
	la, lb := strings.Split(ha, "."), strings.Split(hb, ".")
	if len(la) < 2 || len(lb) < 2 || net.ParseIP(ha) != nil || net.ParseIP(hb) != nil {
		return false
	}
	return strings.Join(la[len(la)-2:], ".") == strings.Join(lb[len(lb)-2:], ".")
}

// Run keşfi yürütür.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	t0 := time.Now()
	start := Norm(cfg.Start, nil)
	if start == nil {
		return nil, errors.New("adres http:// veya https:// ile başlamalı")
	}
	if cfg.MaxPages <= 0 || cfg.MaxPages > MaxPagesHi {
		cfg.MaxPages = 30
	}
	if cfg.MaxDepth < 0 || cfg.MaxDepth > MaxDepthHi {
		cfg.MaxDepth = 2
	}
	if cfg.Delay < 200*time.Millisecond {
		cfg.Delay = 500 * time.Millisecond
	}
	if cfg.Delay > 10*time.Second {
		cfg.Delay = 10 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	say := func(m string) {
		if cfg.Progress != nil {
			cfg.Progress(m)
		}
	}
	res := &Result{Start: start.String(), Pages: []Page{}, Resources: []Resource{}, Endpoints: []Endpoint{}, Hosts: []HostInfo{}}
	hc := probe.NewClient(10*time.Second, cfg.BlockPrivate, false, false, nil)
	ua := "Mozilla/5.0 (compatible; " + UATag + ")"

	// 1) robots.txt — her zaman, her mod için
	say("robots.txt okunuyor")
	rb := fetchRobots(ctx, hc, start, ua)
	switch {
	case rb.DenyAll:
		res.Robots = "alınamadı"
		res.Notes = append(res.Notes, "robots.txt alınamadı (sunucu hatası/ağ); kurala göre güvenli tarafta kalınıp tarama yapılmadı.")
		res.Seconds = time.Since(t0).Seconds()
		return res, nil
	case rb.Found:
		res.Robots = "uyuldu"
	default:
		res.Robots = "yok (izin)"
	}
	if rb.Delay > 0 {
		if d := time.Duration(rb.Delay * float64(time.Second)); d > cfg.Delay {
			cfg.Delay = min(d, 10*time.Second)
			res.Notes = append(res.Notes, fmt.Sprintf("robots.txt Crawl-delay=%.0f sn: istekler arası bekleme buna göre ayarlandı.", rb.Delay))
		}
	}
	skipped := map[string]bool{}
	allowed := func(u *url.URL) bool {
		pq := u.EscapedPath()
		if u.RawQuery != "" {
			pq += "?" + u.RawQuery
		}
		if rb.Allowed(pq) {
			return true
		}
		if len(skipped) < 200 {
			skipped[u.Path] = true
		}
		return false
	}

	var hostStats = map[string]*HostInfo{}
	host := func(h string) *HostInfo {
		x := hostStats[h]
		if x == nil {
			x = &HostInfo{Host: h, Third: !sameSite(h, start.Host)}
			hostStats[h] = x
		}
		return x
	}

	// 2) Tarayıcı ile gezinme
	if cfg.Spider {
		if err := spider(ctx, cfg, res, start, rb, allowed, host, ua, say); err != nil {
			return nil, err
		}
	}
	// 3) Yol sözlüğü (yalnızca anonim, robots.txt'e uyarak)
	if cfg.Wordlist != "" || len(cfg.Extra) > 0 {
		wordlist(ctx, cfg, res, start, hc, allowed, say)
	}
	for p := range skipped {
		res.RobotsSkipped = append(res.RobotsSkipped, p)
	}
	sort.Strings(res.RobotsSkipped)

	// 4) Alan adı özeti + TLS süresi
	for _, x := range hostStats {
		res.Hosts = append(res.Hosts, *x)
	}
	sort.Slice(res.Hosts, func(i, j int) bool {
		if res.Hosts[i].Third != res.Hosts[j].Third {
			return !res.Hosts[i].Third
		}
		return res.Hosts[i].Requests > res.Hosts[j].Requests
	})
	if start.Scheme == "https" && cfg.Spider && len(res.Hosts) == 0 {
		res.Hosts = append(res.Hosts, HostInfo{Host: start.Host})
	}
	say("TLS sertifika süreleri okunuyor")
	for i := range res.Hosts {
		if i >= 15 || ctx.Err() != nil {
			break
		}
		if d, ok := tlsDays(ctx, res.Hosts[i].Host, cfg.BlockPrivate); ok {
			res.Hosts[i].TLSDays = &d
		}
	}
	res.Seconds = time.Since(t0).Seconds()
	return res, ctx.Err()
}

func fetchRobots(ctx context.Context, hc *http.Client, start *url.URL, ua string) *Robots {
	u := *start
	u.Path, u.RawQuery = "/robots.txt", ""
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", ua)
	resp, err := hc.Do(req)
	if err != nil {
		return &Robots{DenyAll: true}
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 200:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		return ParseRobots(string(b), AgentName)
	case resp.StatusCode >= 500:
		return &Robots{DenyAll: true}
	default: // 3xx yönlendirme istemcide kapalı; 4xx: dosya yok → serbest
		return &Robots{}
	}
}

// ---- tarayıcı ile gezinme ----

type netRec struct {
	typ, url, method string
	start            float64
	status           int
	bytes            int64
	ms               float64
	failed           bool
	page             string
}

func spider(ctx context.Context, cfg Config, res *Result, start *url.URL, rb *Robots, allowed func(*url.URL) bool, host func(string) *HostInfo, ua string, say func(string)) error {
	if cfg.BlockPrivate {
		if err := publicHost(start.Hostname()); err != nil {
			return err
		}
	}
	b, err := cdp.Launch(ctx, cfg.Chrome)
	if err != nil {
		return err
	}
	defer b.Close()
	pg, err := browser.NewPage(ctx, b.Page)
	if err != nil {
		return err
	}
	var real string
	if pg.Eval(ctx, "navigator.userAgent", &real) == nil && real != "" {
		b.Page.Call(ctx, "Network.setUserAgentOverride", map[string]any{"userAgent": real + " " + UATag}, nil)
	}

	var mu sync.Mutex
	recs := map[string]*netRec{}
	curPage := ""
	b.Page.On("Network.requestWillBeSent", func(raw json.RawMessage) {
		var e struct {
			RequestID string  `json:"requestId"`
			Type      string  `json:"type"`
			Timestamp float64 `json:"timestamp"`
			Request   struct {
				URL    string `json:"url"`
				Method string `json:"method"`
			} `json:"request"`
		}
		if json.Unmarshal(raw, &e) != nil || (!strings.HasPrefix(e.Request.URL, "http://") && !strings.HasPrefix(e.Request.URL, "https://")) {
			return
		}
		mu.Lock()
		if len(recs) < 6000 {
			recs[e.RequestID] = &netRec{typ: e.Type, url: e.Request.URL, method: e.Request.Method, start: e.Timestamp, page: curPage}
		}
		mu.Unlock()
	})
	b.Page.On("Network.responseReceived", func(raw json.RawMessage) {
		var e struct {
			RequestID string `json:"requestId"`
			Response  struct {
				Status int `json:"status"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &e) == nil {
			mu.Lock()
			if r := recs[e.RequestID]; r != nil {
				r.status = e.Response.Status
			}
			mu.Unlock()
		}
	})
	b.Page.On("Network.loadingFinished", func(raw json.RawMessage) {
		var e struct {
			RequestID string  `json:"requestId"`
			Timestamp float64 `json:"timestamp"`
			Length    float64 `json:"encodedDataLength"`
		}
		if json.Unmarshal(raw, &e) == nil {
			mu.Lock()
			if r := recs[e.RequestID]; r != nil {
				r.bytes, r.ms = int64(e.Length), (e.Timestamp-r.start)*1000
			}
			mu.Unlock()
		}
	})
	b.Page.On("Network.loadingFailed", func(raw json.RawMessage) {
		var e struct {
			RequestID string `json:"requestId"`
			Canceled  bool   `json:"canceled"`
		}
		if json.Unmarshal(raw, &e) == nil && !e.Canceled {
			mu.Lock()
			if r := recs[e.RequestID]; r != nil {
				r.failed = true
			}
			mu.Unlock()
		}
	})

	authed := false
	if cfg.Auth != nil && cfg.Auth.Enabled {
		say("oturum açılıyor")
		o := browser.Options{StepTimeout: cfg.Timeout, Auth: cfg.Auth, Cookies: cfg.Cookies}
		r, err := browser.Authenticate(ctx, pg, cfg.Steps, cfg.Vars, o)
		if cfg.OnLogin != nil && (r.LoginFailed || r.Session == "login") {
			cfg.OnLogin(r.LoginFailed)
		}
		if err != nil {
			return fmt.Errorf("oturum açılamadı: %w", err)
		}
		authed, res.Authed = true, true
		mu.Lock()
		recs = map[string]*netRec{} // giriş trafiğini envantere katma
		mu.Unlock()
	}

	type item struct {
		u     *url.URL
		depth int
	}
	queue := []item{{start, 0}}
	seen := map[string]int{keyOf(start): 1}
	enqueue := func(u *url.URL, d int) {
		if u == nil || u.Host != start.Host || d > cfg.MaxDepth {
			return
		}
		if skipExt[strings.ToLower(path.Ext(u.Path))] || dangerRe.MatchString(u.Path+"?"+u.RawQuery) {
			return
		}
		k := keyOf(u)
		if seen[k] >= 1 {
			return
		}
		seen[k]++
		queue = append(queue, item{u, d})
	}
	// sitemap.xml adreslerini tohum olarak ekle (robots'a uyanlar)
	for _, sm := range append(rb.Sitemaps, start.Scheme+"://"+start.Host+"/sitemap.xml") {
		if su := Norm(sm, start); su != nil && su.Host == start.Host && allowed(su) {
			for _, loc := range sitemapLocs(ctx, cfg, su) {
				if lu := Norm(loc, start); lu != nil && allowed(lu) {
					enqueue(lu, 1)
				}
			}
		}
	}

	for len(queue) > 0 && len(res.Pages) < cfg.MaxPages {
		if ctx.Err() != nil {
			break
		}
		it := queue[0]
		queue = queue[1:]
		if !allowed(it.u) {
			continue
		}
		say(fmt.Sprintf("sayfa %d/%d: %s", len(res.Pages)+1, cfg.MaxPages, it.u.Path))
		mu.Lock()
		curPage = it.u.String()
		mu.Unlock()
		p := Page{URL: it.u.String(), Depth: it.depth, Auth: authed}
		t1 := time.Now()
		pctx, cancel := context.WithTimeout(ctx, cfg.Timeout+15*time.Second)
		if err := pg.Navigate(pctx, p.URL); err != nil {
			p.Error = err.Error()
		} else {
			pg.WaitIdle(pctx, time.Now(), 700*time.Millisecond, cfg.Timeout)
			p.MS = float64(time.Since(t1).Microseconds()) / 1000
			var info struct {
				Title  string   `json:"title"`
				Links  []string `json:"links"`
				Forms  []Form   `json:"forms"`
				Frames []string `json:"frames"`
			}
			if pg.Eval(pctx, pageJS, &info) == nil {
				p.Title, p.Forms, p.Frames = info.Title, info.Forms, info.Frames
				cur := Norm(pg.URL(), nil)
				base := it.u
				if cur != nil {
					base = cur
					if cur.Host != start.Host { // yönlendirme dışarı çıktı: bu sayfadan bağlantı izleme
						p.Error = "başka bir alan adına yönlendirdi: " + cur.Host
						info.Links = nil
					}
				}
				for _, l := range info.Links {
					if u := Norm(l, base); u != nil {
						p.Links++
						enqueue(u, it.depth+1)
					}
				}
			}
		}
		cancel()
		mu.Lock()
		for _, r := range recs {
			if r.page == p.URL && r.typ == "Document" && r.status != 0 && (p.Status == 0 || r.url == p.URL) {
				p.Status = r.status
			}
		}
		mu.Unlock()
		res.Pages = append(res.Pages, p)
		select {
		case <-time.After(cfg.Delay):
		case <-ctx.Done():
		}
	}
	if len(queue) > 0 {
		res.Truncated = true
		res.Notes = append(res.Notes, fmt.Sprintf("Sayfa sınırına (%d) ulaşıldı; %d adres gezilmedi.", cfg.MaxPages, len(queue)))
	}

	// ağ kayıtlarını envantere çevir
	rm, em := map[string]*Resource{}, map[string]*Endpoint{}
	pagesOf := map[string]map[string]bool{}
	mu.Lock()
	defer mu.Unlock()
	for _, r := range recs {
		u := Norm(r.url, nil)
		if u == nil {
			continue
		}
		h := host(u.Host)
		h.Requests++
		if r.failed {
			h.Failed++
		}
		if r.status >= 400 {
			h.Errors++
		}
		clean := *u
		clean.RawQuery = ""
		third := !sameSite(u.Host, start.Host)
		switch r.typ {
		case "XHR", "Fetch":
			k := r.method + " " + clean.String()
			e := em[k]
			if e == nil {
				e = &Endpoint{Method: r.method, URL: clean.String(), Third: third}
				em[k] = e
			}
			e.Count++
			e.Status, e.MS = r.status, max(e.MS, r.ms)
		case "Document":
		default:
			k := r.typ + " " + clean.String()
			x := rm[k]
			if x == nil {
				x = &Resource{URL: clean.String(), Type: r.typ, Host: u.Host, Third: third}
				rm[k] = x
				pagesOf[k] = map[string]bool{}
			}
			x.Status, x.Bytes, x.MS, x.Failed = r.status, max(x.Bytes, r.bytes), max(x.MS, r.ms), x.Failed || r.failed
			pagesOf[k][r.page] = true
			x.Pages = len(pagesOf[k])
		}
	}
	for _, x := range rm {
		res.Resources = append(res.Resources, *x)
	}
	for _, x := range em {
		res.Endpoints = append(res.Endpoints, *x)
	}
	sort.Slice(res.Resources, func(i, j int) bool { return res.Resources[i].MS > res.Resources[j].MS })
	sort.Slice(res.Endpoints, func(i, j int) bool { return res.Endpoints[i].Count > res.Endpoints[j].Count })
	if len(res.Resources) > 600 {
		res.Resources = res.Resources[:600]
		res.Notes = append(res.Notes, "Kaynak listesi en yavaş 600 ile sınırlandı.")
	}
	return nil
}

var locRe = regexp.MustCompile(`<loc>\s*([^<\s]+)\s*</loc>`)

func sitemapLocs(ctx context.Context, cfg Config, u *url.URL) []string {
	hc := probe.NewClient(8*time.Second, cfg.BlockPrivate, false, true, nil)
	req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; "+UATag+")")
	resp, err := hc.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out []string
	for _, m := range locRe.FindAllStringSubmatch(string(b), 200) {
		out = append(out, m[1])
	}
	return out
}

// ---- yol sözlüğü ----

func wordlist(ctx context.Context, cfg Config, res *Result, start *url.URL, _ *http.Client, allowed func(*url.URL) bool, say func(string)) {
	hc := probe.NewClient(8*time.Second, cfg.BlockPrivate, false, false, nil)
	words := append(Words(cfg.Wordlist), cfg.Extra...)
	if len(words) > 400 {
		words = words[:400]
	}
	get := func(p string) (int, int64, string, string) {
		u := *start
		u.Path, u.RawQuery = p, ""
		req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; "+UATag+")")
		resp, err := hc.Do(req)
		if err != nil {
			return 0, 0, "", ""
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 32*1024))
		sum := sha256.Sum256(b)
		return resp.StatusCode, int64(len(b)), hex.EncodeToString(sum[:8]), resp.Header.Get("Location")
	}
	// "her yola 200 dönen" siteleri (SPA) için taban çizgisi
	bs, _, bh, _ := get("/appmon-yok-" + hex.EncodeToString(sha256.New().Sum(nil))[:8])
	for i, w := range words {
		if ctx.Err() != nil {
			break
		}
		u := *start
		u.Path = w
		if dangerRe.MatchString(w) || !allowed(&u) {
			continue
		}
		if i%10 == 0 {
			say(fmt.Sprintf("sözlük %d/%d", i, len(words)))
		}
		st, n, hsh, loc := get(w)
		switch st {
		case 200, 201, 204, 301, 302, 307, 308, 401, 403, 405:
			if st == bs && hsh == bh && st == 200 {
				break // taban çizgisiyle aynı içerik: "yok" sayfası
			}
			res.Hits = append(res.Hits, Hit{Path: w, Status: st, Bytes: n, Redirect: loc})
		}
		select {
		case <-time.After(cfg.Delay):
		case <-ctx.Done():
		}
	}
	if bs == 200 {
		res.Notes = append(res.Notes, "Site var olmayan yollara da 200 döndürüyor (tek sayfa uygulaması olabilir); taban çizgisiyle aynı içerik dönenler elendi.")
	}
}

// ---- yardımcılar ----

func publicHost(h string) error {
	ips, err := net.LookupIP(h)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("%s çözümlenemedi", h)
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			return errors.New("bu modda özel ağ adreslerine tarama yapılamaz")
		}
	}
	return nil
}

func tlsDays(ctx context.Context, hostport string, block bool) (int, bool) {
	h, p, err := net.SplitHostPort(hostport)
	if err != nil {
		h, p = hostport, "443"
	}
	d := &net.Dialer{Timeout: 5 * time.Second, Control: probe.Guard(block)}
	c, err := tls.DialWithDialer(d, "tcp", net.JoinHostPort(h, p), &tls.Config{InsecureSkipVerify: true, ServerName: h}) //nolint:gosec // yalnızca sertifika bitişi okunur
	if err != nil {
		return 0, false
	}
	defer c.Close()
	certs := c.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return 0, false
	}
	return int(time.Until(certs[0].NotAfter).Hours() / 24), true
}
