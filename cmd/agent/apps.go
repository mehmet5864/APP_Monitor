package main

import (
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Uygulama keşfi (v2.7.3): ajan, makinedeki çalışan süreçleri (adına göre toplanmış), servisleri ve dinlenen portları
// bulur; en yoğun uygulamalar için CPU/bellek metriği üretir; bilinen uygulamaların günlüklerinde hata/uyarı satırlarını
// sayar. Günlük içeriği gönderilmez: yalnızca sayılar ve kısa, parola/anahtar maskeli son hata örneği gider.

type appInfo struct {
	Name  string  `json:"name"`
	Kind  string  `json:"kind"` // process | service
	State string  `json:"state,omitempty"`
	Count int     `json:"count,omitempty"`
	CPU   float64 `json:"cpu_pct"`
	MemMB float64 `json:"mem_mb"`
	Ports []int   `json:"ports,omitempty"`
}

type logInfo struct {
	App      string `json:"app"`
	Path     string `json:"path"`
	Readable bool   `json:"readable"`
	Sample   string `json:"sample,omitempty"`
}

type appsReport struct {
	Apps []appInfo `json:"apps"`
	Logs []logInfo `json:"logs"`
}

// procSample işletim sistemi katmanının verdiği tek süreç ölçümü.
type procSample struct {
	pid    int
	name   string  // küçük harf, .exe'siz
	cpuSec float64 // süreç başından beri toplam CPU saniyesi
	rssKB  uint64
}

var appsEvery = 60 * time.Second // sınama için APM_APPS_EVERY ile kısaltılabilir

const (
	maxApps     = 60 // raporlanan süreç grubu sayısı
	maxServices = 80
	topMetrics  = 10 // CPU/bellek metriği üretilen uygulama sayısı
)

type appScanner struct {
	prev   map[int]float64
	prevAt time.Time
	logs   *autoLogs
}

func newAppScanner() *appScanner {
	return &appScanner{prev: map[int]float64{}, logs: newAutoLogs()}
}

var safeNameRe = regexp.MustCompile(`[^a-z0-9_.-]+`)

// safeName metrik adında kullanılabilir kısa ad üretir.
func safeName(n string) string {
	n = safeNameRe.ReplaceAllString(strings.ToLower(n), "_")
	if len(n) > 40 {
		n = n[:40]
	}
	return strings.Trim(n, "_.-")
}

// aggregate süreç örneklerini ada göre toplar; CPU yüzdesi önceki taramaya göre bir çekirdeğin yüzdesidir.
func (s *appScanner) aggregate(samples []procSample, now time.Time) map[string]*appInfo {
	dt := now.Sub(s.prevAt).Seconds()
	cur := make(map[int]float64, len(samples))
	out := map[string]*appInfo{}
	for _, p := range samples {
		if p.name == "" {
			continue
		}
		a := out[p.name]
		if a == nil {
			a = &appInfo{Name: p.name, Kind: "process", State: "running"}
			out[p.name] = a
		}
		a.Count++
		a.MemMB += float64(p.rssKB) / 1024
		cur[p.pid] = p.cpuSec
		if pv, ok := s.prev[p.pid]; ok && dt >= 1 && p.cpuSec >= pv {
			a.CPU += (p.cpuSec - pv) / dt * 100
		}
	}
	s.prev, s.prevAt = cur, now
	return out
}

// scan uygulama listesini ve günlük özetini üretir; ms en yoğun uygulamaların metrikleridir.
func (s *appScanner) scan() (appsReport, []metric) {
	now := time.Now()
	samples := readProcs()
	agg := s.aggregate(samples, now)
	ports := listenPorts()
	pidName := map[int]string{}
	for _, p := range samples {
		pidName[p.pid] = p.name
	}
	for pid, ps := range ports {
		if a := agg[pidName[pid]]; a != nil {
			a.Ports = mergePorts(a.Ports, ps)
		}
	}
	list := make([]*appInfo, 0, len(agg))
	names := map[string]bool{}
	for n, a := range agg {
		a.CPU, a.MemMB = round1(a.CPU), round1(a.MemMB)
		list = append(list, a)
		names[n] = true
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CPU != list[j].CPU && (list[i].CPU > 5 || list[j].CPU > 5) {
			return list[i].CPU > list[j].CPU
		}
		return list[i].MemMB > list[j].MemMB
	})
	var ms []metric
	rep := appsReport{Apps: []appInfo{}, Logs: []logInfo{}}
	for i, a := range list {
		if i >= maxApps {
			break
		}
		rep.Apps = append(rep.Apps, *a)
		if i < topMetrics {
			if n := safeName(a.Name); n != "" {
				ms = append(ms, metric{Name: "proc." + n + ".cpu_pct", Value: a.CPU}, metric{Name: "proc." + n + ".mem_mb", Value: a.MemMB})
			}
		}
	}
	for i, sv := range listServices() {
		if i >= maxServices {
			break
		}
		rep.Apps = append(rep.Apps, sv)
	}
	s.logs.refresh(names)
	rep.Logs = s.logs.info()
	return rep, ms
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func mergePorts(a, b []int) []int {
	seen := map[int]bool{}
	for _, p := range a {
		seen[p] = true
	}
	for _, p := range b {
		if !seen[p] {
			seen[p] = true
			a = append(a, p)
		}
	}
	sort.Ints(a)
	if len(a) > 12 {
		a = a[:12]
	}
	return a
}

// ---- Günlük hata/uyarı sayımı ----

var (
	errLineRe  = regexp.MustCompile(`(?i)\b(error|fatal|critical|crit|panic|exception|segfault|failed)\b`)
	warnLineRe = regexp.MustCompile(`(?i)\b(warn|warning)\b`)
	secretRe   = regexp.MustCompile(`(?i)(pass(?:word|wd)?|pwd|secret|token|api[_-]?key|authorization|bearer)(["'\s:=]+)[^\s"',;&]+`)
	longTokRe  = regexp.MustCompile(`[A-Za-z0-9+/_-]{32,}`)
)

// maskLine günlük satırını örnek olarak göndermeden önce kısaltır ve gizli görünen değerleri maskeler.
func maskLine(s string) string {
	s = strings.TrimSpace(s)
	s = secretRe.ReplaceAllString(s, "$1$2***")
	s = longTokRe.ReplaceAllString(s, "***")
	if utf8.RuneCountInString(s) > 200 {
		r := []rune(s)
		s = string(r[:200]) + "…"
	}
	return s
}

type autoLogs struct {
	w map[string]*logWatch // uygulama -> izleyici
}

func newAutoLogs() *autoLogs { return &autoLogs{w: map[string]*logWatch{}} }

// refresh çalışan uygulamalara uygun, bulunan günlük dosyaları için izleyici açar (yol değiştiyse yeniler).
func (a *autoLogs) refresh(running map[string]bool) {
	for _, d := range autoLogDefs() {
		if !d.always {
			found := false
			for _, p := range d.procs {
				if running[p] {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		path := pickLogPath(d.paths)
		if path == "" {
			continue
		}
		if cur := a.w[d.app]; cur != nil && cur.path == path {
			continue
		} else if cur != nil {
			cur.close()
		}
		w := &logWatch{name: "auto_" + d.app, path: path, re: errLineRe, warnRe: warnLineRe, sampleOn: true}
		w.poll() // başlangıç konumu: dosya sonu (geçmiş sayılmaz)
		a.w[d.app] = w
	}
}

// metrics her 15 sn'lik turda yeni satırları sayar: log.auto_<uygulama>.count (hata), .warn (uyarı), .readable.
func (a *autoLogs) metrics() []metric {
	var ms []metric
	apps := make([]string, 0, len(a.w))
	for k := range a.w {
		apps = append(apps, k)
	}
	sort.Strings(apps)
	for _, app := range apps {
		w := a.w[app]
		m, _, ok := w.poll()
		r := 0.0
		if ok {
			r = 1
			ms = append(ms, metric{Name: "log." + w.name + ".count", Value: float64(m)}, metric{Name: "log." + w.name + ".warn", Value: float64(w.lastWarn)})
		}
		ms = append(ms, metric{Name: "log." + w.name + ".readable", Value: r})
	}
	return ms
}

func (a *autoLogs) info() []logInfo {
	out := []logInfo{}
	apps := make([]string, 0, len(a.w))
	for k := range a.w {
		apps = append(apps, k)
	}
	sort.Strings(apps)
	for _, app := range apps {
		w := a.w[app]
		out = append(out, logInfo{App: app, Path: w.path, Readable: w.readable, Sample: w.sample})
	}
	return out
}

type autoLogDef struct {
	app    string
	procs  []string
	paths  []string // ters sırada denenmez; ilk bulunan (glob'larda en yeni) seçilir
	always bool
}
