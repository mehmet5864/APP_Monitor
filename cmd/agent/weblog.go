package main

import (
	"appmon/internal/weblog"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Web sunucusu erişim günlüğü (v2.7.6, ajan 1.1.0): ajan nginx, Apache ve IIS günlüklerini bulur, her satırı kendisi çözer ve yalnızca
// uç nokta bazında özet (istek sayısı, 4xx/5xx, gecikme kovaları) gönderir. Sorgu dizesi atılır, yollar normalleştirilir ({id}, {uuid}…);
// satır içeriği, IP ve kullanıcı bilgisi sunucuya gitmez. İstek süresi günlükte yoksa (nginx/Apache varsayılan biçimi) yalnızca sayılar gider.

type webRec struct {
	app    string // boşsa kaynağın varsayılan adı
	method string
	path   string
	status int
	ms     float64
	timed  bool
}

type accessEntry struct {
	App      string                 `json:"app"`
	Endpoint string                 `json:"endpoint"`
	Reqs     int64                  `json:"reqs"`
	E4       int64                  `json:"e4"`
	E5       int64                  `json:"e5"`
	Untimed  int64                  `json:"untimed"`
	SumMS    float64                `json:"sum_ms"`
	MaxMS    float64                `json:"max_ms"`
	B        [weblog.NBuckets]int64 `json:"b"`
}

const (
	maxWebApps      = 20
	maxWebEndpoints = 300 // uygulama başına; fazlası "OTHER /*"
	maxWebEntries   = 600 // bir gönderimdeki özet satırı
	maxWebRead      = 8 << 20
	maxWebSources   = 12
)

type epKey struct{ app, ep string }

type webAgg struct {
	m    map[epKey]*accessEntry
	eps  map[string]map[string]bool
	apps map[string]bool
}

func newWebAgg() *webAgg {
	return &webAgg{m: map[epKey]*accessEntry{}, eps: map[string]map[string]bool{}, apps: map[string]bool{}}
}

var appSafeRe = regexp.MustCompile(`[^A-Za-z0-9._:-]+`)

// appSafe uygulama adını sunucunun kabul ettiği biçime getirir.
func appSafe(s string) string {
	s = strings.Trim(appSafeRe.ReplaceAllString(strings.TrimSpace(s), "-"), "-.:_")
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

func (a *webAgg) add(app string, r webRec) {
	if app == "" {
		return
	}
	if !a.apps[app] {
		if len(a.apps) >= maxWebApps {
			return
		}
		a.apps[app] = true
	}
	method := strings.ToUpper(r.method)
	if len(method) < 3 || len(method) > 8 {
		method = "OTHER"
	}
	ep := method + " " + weblog.NormalizePath(r.path)
	set := a.eps[app]
	if set == nil {
		set = map[string]bool{}
		a.eps[app] = set
	}
	if !set[ep] {
		if len(set) >= maxWebEndpoints {
			ep = "OTHER /*"
		} else {
			set[ep] = true
		}
	}
	k := epKey{app, ep}
	e := a.m[k]
	if e == nil {
		e = &accessEntry{App: app, Endpoint: ep}
		a.m[k] = e
	}
	e.Reqs++
	switch {
	case r.status >= 500:
		e.E5++
	case r.status >= 400:
		e.E4++
	}
	if !r.timed {
		e.Untimed++
		return
	}
	e.SumMS += r.ms
	if r.ms > e.MaxMS {
		e.MaxMS = r.ms
	}
	e.B[weblog.BucketIndex(r.ms)]++
}

// merge bir özet satırını (geri koyma) ekler.
func (a *webAgg) merge(x accessEntry) {
	if !a.apps[x.App] {
		if len(a.apps) >= maxWebApps {
			return
		}
		a.apps[x.App] = true
	}
	if a.eps[x.App] == nil {
		a.eps[x.App] = map[string]bool{}
	}
	a.eps[x.App][x.Endpoint] = true
	k := epKey{x.App, x.Endpoint}
	e := a.m[k]
	if e == nil {
		c := x
		a.m[k] = &c
		return
	}
	e.Reqs += x.Reqs
	e.E4 += x.E4
	e.E5 += x.E5
	e.Untimed += x.Untimed
	e.SumMS += x.SumMS
	if x.MaxMS > e.MaxMS {
		e.MaxMS = x.MaxMS
	}
	for i := range e.B {
		e.B[i] += x.B[i]
	}
}

// take birikmiş özetleri (en çok maxWebEntries satır, en çok istekli uç noktalar önce) alır; kalanı bir sonraki tura bırakır.
func (a *webAgg) take() []accessEntry {
	if len(a.m) == 0 {
		return nil
	}
	all := make([]accessEntry, 0, len(a.m))
	for _, e := range a.m {
		all = append(all, *e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Reqs > all[j].Reqs })
	var rest []accessEntry
	if len(all) > maxWebEntries {
		all, rest = all[:maxWebEntries], all[maxWebEntries:]
	}
	a.m, a.eps, a.apps = map[epKey]*accessEntry{}, map[string]map[string]bool{}, map[string]bool{}
	for _, e := range rest {
		a.merge(e)
	}
	return all
}

func (a *webAgg) restore(list []accessEntry) {
	for _, e := range list {
		a.merge(e)
	}
}

// ---- Satır çözücüler ----

var combinedRe = regexp.MustCompile(`^\S+ \S+ \S+ \[[^\]]*\] "(\S+) (\S+)[^"]*" (\d{3}) \S+(.*)$`)
var tailTimeRe = regexp.MustCompile(`(?:^|\s)(rt|request_time|rt_ms|rt_us|time_taken|duration)="?([0-9]+(?:\.[0-9]+)?)`)

// parseAppmon: "appmon h=site m=GET st=200 rt=0.123 uri=/x" (ajanın -web-setup ile yazdığı biçim).
func parseAppmon(line string) (webRec, bool) {
	var r webRec
	if !strings.HasPrefix(line, "appmon ") {
		return r, false
	}
	var host, st, rt, rtms, rtus string
	for _, tok := range strings.Fields(line[7:]) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		switch k {
		case "h":
			host = v
		case "m":
			r.method = v
		case "st":
			st = v
		case "rt":
			rt = v
		case "rt_ms":
			rtms = v
		case "rt_us":
			rtus = v
		case "uri":
			r.path = v
		}
	}
	n, err := strconv.Atoi(st)
	if err != nil || n < 100 || n > 599 {
		return r, false
	}
	r.status = n
	r.app = appSafe(strings.SplitN(host, ":", 2)[0])
	setTime(&r, rt, 1000, rtms, 1, rtus, 0.001)
	return r, true
}

func setTime(r *webRec, rt string, rtMul float64, rtms string, msMul float64, rtus string, usMul float64) {
	for _, c := range []struct {
		v string
		m float64
	}{{rt, rtMul}, {rtms, msMul}, {rtus, usMul}} {
		if c.v == "" {
			continue
		}
		if f, err := strconv.ParseFloat(c.v, 64); err == nil && f >= 0 && f <= 3_600_000 {
			r.ms, r.timed = f*c.m, true
			return
		}
	}
}

// parseCombined: nginx/Apache "combined" (veya "common") biçimi; sonda rt=/request_time=/rt_us= gibi anahtar varsa süre alınır.
func parseCombined(line string) (webRec, bool) {
	var r webRec
	m := combinedRe.FindStringSubmatch(line)
	if m == nil {
		return r, false
	}
	n, _ := strconv.Atoi(m[3])
	if n < 100 || n > 599 {
		return r, false
	}
	r.method, r.path, r.status = m[1], m[2], n
	if t := tailTimeRe.FindStringSubmatch(m[4]); t != nil {
		switch t[1] {
		case "rt_us":
			setTime(&r, "", 0, "", 0, t[2], 0.001)
		case "rt_ms", "time_taken":
			setTime(&r, "", 0, t[2], 1, "", 0)
		default:
			setTime(&r, t[2], 1000, "", 0, "", 0)
		}
	}
	return r, true
}

type iisFields struct{ idx map[string]int }

// parseIIS: W3C genişletilmiş günlük; "#Fields:" başlığından sütunları öğrenir, time-taken milisaniyedir.
func (f *iisFields) parse(line string) (webRec, bool) {
	var r webRec
	if strings.HasPrefix(line, "#") {
		if rest, ok := strings.CutPrefix(line, "#Fields:"); ok {
			f.idx = map[string]int{}
			for i, n := range strings.Fields(rest) {
				f.idx[n] = i
			}
		}
		return r, false
	}
	if f.idx == nil {
		return r, false
	}
	p := strings.Fields(line)
	get := func(n string) string {
		if i, ok := f.idx[n]; ok && i < len(p) {
			return p[i]
		}
		return ""
	}
	n, err := strconv.Atoi(get("sc-status"))
	if err != nil || n < 100 || n > 599 || get("cs-method") == "" {
		return r, false
	}
	r.method, r.path, r.status = get("cs-method"), get("cs-uri-stem"), n
	r.app = appSafe(get("s-sitename"))
	if v := get("time-taken"); v != "" {
		setTime(&r, "", 0, v, 1, "", 0)
	}
	return r, true
}

// load dosyanın başındaki (ilk 16 KB) en son "#Fields:" satırından sütunları öğrenir.
func (f *iisFields) load(path string) {
	fh, err := os.Open(path)
	if err != nil {
		return
	}
	defer fh.Close()
	buf, _ := io.ReadAll(io.LimitReader(fh, 16<<10))
	for _, ln := range strings.Split(string(buf), "\n") {
		if strings.HasPrefix(ln, "#Fields:") {
			f.parse(strings.TrimRight(ln, "\r"))
		}
	}
}

// ---- Dosya takibi ----

type webTail struct {
	path     string
	f        *os.File
	off      int64
	started  bool
	tail     string
	readable bool
}

// lines dosyaya eklenen yeni satırları verir (ilk açılışta dosya sonundan başlar; döndürme/kısalmayı algılar).
func (t *webTail) lines() []string {
	fi, err := os.Stat(t.path)
	if err != nil {
		t.close()
		t.readable = false
		return nil
	}
	if t.f != nil {
		if of, err := t.f.Stat(); err != nil || !os.SameFile(fi, of) || fi.Size() < t.off {
			t.close()
			t.started = true
		}
	}
	if t.f == nil {
		f, err := os.Open(t.path)
		if err != nil {
			t.readable = false
			return nil
		}
		t.f, t.tail = f, ""
		if !t.started {
			t.off, t.started = fi.Size(), true
		} else {
			t.off = 0
		}
	}
	t.readable = true
	if _, err := t.f.Seek(t.off, io.SeekStart); err != nil {
		t.close()
		return nil
	}
	buf, err := io.ReadAll(io.LimitReader(t.f, maxWebRead))
	if err != nil {
		t.close()
		return nil
	}
	t.off += int64(len(buf))
	data := t.tail + string(buf)
	i := strings.LastIndexByte(data, '\n')
	if i < 0 {
		t.tail = data
		if len(t.tail) > 1<<20 {
			t.tail = ""
		}
		return nil
	}
	t.tail = data[i+1:]
	return strings.Split(data[:i], "\n")
}

func (t *webTail) close() {
	if t.f != nil {
		t.f.Close()
		t.f = nil
	}
}

// webCand bulunacak günlük tanımı: server (nginx|apache|iis|appmon), glob'lar; iisDir true ise her dizinde en yeni dosya seçilir.
type webCand struct {
	server string
	globs  []string
	perDir bool
}

type webSource struct {
	server string
	key    string // dosya yolu (IIS'te dizin)
	tail   *webTail
	iis    *iisFields
	def    string // varsayılan uygulama adı (günlükte ad yoksa)
}

type webLogs struct {
	host string
	srcs map[string]*webSource
	agg  *webAgg
	note string // arayüzde gösterilecek durum özeti (labels.weblog)
}

func newWebLogs(host string) *webLogs {
	return &webLogs{host: host, srcs: map[string]*webSource{}, agg: newWebAgg()}
}

// resolve aday tanımlarını gerçek dosya yollarına çevirir.
func resolveCands(cands []webCand) map[string]webCand {
	out := map[string]webCand{} // yol -> aday
	for _, c := range cands {
		var files []string
		for _, g := range c.globs {
			m, _ := filepath.Glob(g)
			files = append(files, m...)
		}
		if c.perDir {
			newest := map[string]string{}
			mt := map[string]time.Time{}
			for _, f := range files {
				fi, err := os.Stat(f)
				if err != nil || fi.IsDir() {
					continue
				}
				d := filepath.Dir(f)
				if fi.ModTime().After(mt[d]) {
					mt[d], newest[d] = fi.ModTime(), f
				}
			}
			files = files[:0]
			for _, f := range newest {
				files = append(files, f)
			}
		}
		for _, f := range files {
			if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
				out[f] = c
			}
		}
	}
	return out
}

// refresh kaynakları (yeniden) bulur. Ajanın kendi yazdığı appmon günlüğü varsa nginx/Apache varsayılan günlükleri atlanır (çift sayım olmasın).
func (w *webLogs) refresh(cands []webCand) {
	found := resolveCands(cands)
	hasOwn := false
	for _, c := range found {
		if c.server == "appmon" {
			hasOwn = true
		}
	}
	keep := map[string]bool{}
	paths := make([]string, 0, len(found))
	for p := range found {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		c := found[p]
		if hasOwn && (c.server == "nginx" || c.server == "apache") {
			continue
		}
		if len(keep) >= maxWebSources {
			break
		}
		key := p
		if c.perDir {
			key = filepath.Dir(p)
		}
		keep[key] = true
		src := w.srcs[key]
		if src == nil {
			src = &webSource{server: c.server, key: key, def: appSafe(w.host + "-" + c.server)}
			if c.server == "iis" {
				src.iis = &iisFields{}
			}
			w.srcs[key] = src
			log.Printf("web erişim günlüğü izleniyor: %s (%s)", p, c.server)
		}
		if src.tail == nil {
			src.tail = &webTail{path: p}
			if src.iis != nil {
				src.iis.load(p) // dosya sonundan başlanacağı için sütun başlığı baştan okunur
			}
		} else if src.tail.path != p { // IIS günlüğü günlük döner: yeni dosya baştan okunur
			src.tail.close()
			src.tail = &webTail{path: p, started: true}
			if src.iis != nil {
				src.iis = &iisFields{}
			}
		}
	}
	for k, s := range w.srcs {
		if !keep[k] {
			if s.tail != nil {
				s.tail.close()
			}
			delete(w.srcs, k)
		}
	}
}

// poll kaynaklardaki yeni satırları özetlere ekler; durum özetini günceller.
func (w *webLogs) poll() {
	var ok, bad []string
	for _, s := range w.srcs {
		if s.tail == nil {
			continue
		}
		for _, ln := range s.tail.lines() {
			ln = strings.TrimRight(ln, "\r")
			if ln == "" {
				continue
			}
			var r webRec
			var good bool
			switch {
			case s.iis != nil:
				r, good = s.iis.parse(ln)
			case strings.HasPrefix(ln, "appmon "):
				r, good = parseAppmon(ln)
			default:
				r, good = parseCombined(ln)
			}
			if !good {
				continue
			}
			app := r.app
			if app == "" {
				app = s.def
			}
			w.agg.add(app, r)
		}
		if s.tail.readable {
			ok = append(ok, s.server)
		} else {
			bad = append(bad, s.tail.path)
		}
	}
	switch {
	case len(bad) > 0:
		w.note = "okunamıyor: " + strings.Join(bad, ", ") + " (ajanı -root ile kurun)"
	case len(ok) > 0:
		w.note = strconv.Itoa(len(ok)) + " günlük okunuyor"
	default:
		w.note = ""
	}
}
