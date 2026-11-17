package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"appmon/internal/notify"
)

// Ölçek ayarları: 500 ajan / 1000 probe / 150 senaryo düzeyinde zamanlayıcıların boğulmaması için.
// Varsayılanlar küçük kurulumlarda eski davranışa yakındır; APM_*_CONCURRENCY ile yükseltilir.

type scaleState struct {
	once                sync.Once
	probe, scen, ingest chan struct{}
	evalAt              sync.Map // kural id -> son değerlendirme zamanı
	notifyMu            sync.Mutex
	notifyBuf           map[string]*notifyGroup
	cache               sync.Map // anahtar -> cacheEntry (kısa ömürlü yanıt önbelleği)
	rateMu              sync.Mutex
	rate                map[string]*bucket
}

func clampInt(v, def, lo, hi int) int {
	if v <= 0 {
		v = def
	}
	if v < lo {
		v = lo
	}
	if v > hi {
		v = hi
	}
	return v
}

func (s *Server) sems() *scaleState {
	s.scale.once.Do(func() {
		s.scale.probe = make(chan struct{}, clampInt(s.ProbeConc, 50, 1, 1000))
		s.scale.scen = make(chan struct{}, clampInt(s.ScenarioConc, 10, 1, 200))
		s.scale.ingest = make(chan struct{}, clampInt(s.IngestConc, 16, 1, 500))
	})
	return &s.scale
}

// SetBrowserConcurrency aynı anda oynatılacak tarayıcı senaryosu sayısını ayarlar (açılışta, RunJobs'tan önce).
func SetBrowserConcurrency(n int) {
	browserSem = make(chan struct{}, clampInt(n, 2, 1, 16))
}

// acquireIngest ingest için bir yuva ayırır; doluysa kısa süre bekler, olmazsa false döner (503 + Retry-After).
func (s *Server) acquireIngest(ctx context.Context) (release func(), ok bool) {
	sem := s.sems().ingest
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	default:
	}
	t := time.NewTimer(3 * time.Second)
	defer t.Stop()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	case <-t.C:
	case <-ctx.Done():
	}
	return nil, false
}

// evalEvery kural penceresine göre ne sıklıkla değerlendirileceğini verir: 15 sn ile 60 sn arası (pencere/4).
func evalEvery(winSec int) time.Duration {
	d := time.Duration(winSec/4) * time.Second
	if d < 15*time.Second {
		d = 15 * time.Second
	}
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

// ruleDue kural şimdi değerlendirilmeli mi; evet ise zamanı işaretler.
func (s *Server) ruleDue(id int64, winSec int) bool {
	now := time.Now()
	if v, ok := s.sems().evalAt.Load(id); ok {
		if now.Sub(v.(time.Time)) < evalEvery(winSec)-time.Second {
			return false
		}
	}
	s.sems().evalAt.Store(id, now)
	return true
}

// ---- Bildirim gruplama (D1) ----

type notifyItem struct {
	asset string
	val   float64
}

type notifyGroup struct {
	tenant int64
	event  string
	rule   string
	sev    string
	thr    float64
	cmp    string
	items  []notifyItem
	timer  *time.Timer
}

// queueNotify aynı kuraldan kısa sürede gelen bildirimleri tek mesajda toplar. NotifyGroup<=0 ise hemen gönderir.
func (s *Server) queueNotify(tenant int64, event, rule, sev, assetName string, val, thr float64, cmp string) {
	if s.NotifyGroup <= 0 {
		s.flushNotify(&notifyGroup{tenant: tenant, event: event, rule: rule, sev: sev, thr: thr, cmp: cmp,
			items: []notifyItem{{assetName, val}}})
		return
	}
	st := s.sems()
	key := fmt.Sprintf("%d|%s|%s", tenant, event, rule)
	st.notifyMu.Lock()
	defer st.notifyMu.Unlock()
	if st.notifyBuf == nil {
		st.notifyBuf = map[string]*notifyGroup{}
	}
	g := st.notifyBuf[key]
	if g == nil {
		g = &notifyGroup{tenant: tenant, event: event, rule: rule, sev: sev, thr: thr, cmp: cmp}
		st.notifyBuf[key] = g
		g.timer = time.AfterFunc(s.NotifyGroup, func() {
			st.notifyMu.Lock()
			delete(st.notifyBuf, key)
			st.notifyMu.Unlock()
			s.flushNotify(g)
		})
	}
	g.items = append(g.items, notifyItem{assetName, val})
}

// flushNotify toplanan bildirimi kanallara gönderir: tek öğe ise eski biçimde, çok öğe ise özet olarak.
func (s *Server) flushNotify(g *notifyGroup) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var m notify.Message
	valStr := func(v float64) string { return fmt.Sprintf("%g (%s %g)", v, g.cmp, g.thr) }
	if len(g.items) == 1 {
		it := g.items[0]
		subject := g.rule + " — " + it.asset
		text := fmt.Sprintf("%s hizmetinde \"%s\" alarmı açıldı.", it.asset, g.rule)
		if g.event == "resolved" {
			text = fmt.Sprintf("%s hizmetindeki \"%s\" alarmı düzeldi.", it.asset, g.rule)
			subject = g.rule + " düzeldi — " + it.asset
		}
		m = notify.Message{Event: g.event, Severity: g.sev, Subject: subject, Text: text, Asset: it.asset, Rule: g.rule,
			Value: valStr(it.val), When: time.Now(), Link: s.publicLink()}
	} else {
		names := make([]string, 0, len(g.items))
		for _, it := range g.items {
			names = append(names, it.asset)
		}
		sort.Strings(names)
		shown := names
		if len(shown) > 15 {
			shown = shown[:15]
		}
		list := strings.Join(shown, ", ")
		if len(names) > len(shown) {
			list += fmt.Sprintf(" ve %d tane daha", len(names)-len(shown))
		}
		subject := fmt.Sprintf("%s — %d varlıkta", g.rule, len(names))
		text := fmt.Sprintf("\"%s\" alarmı %d varlıkta açıldı: %s", g.rule, len(names), list)
		if g.event == "resolved" {
			subject = fmt.Sprintf("%s düzeldi — %d varlıkta", g.rule, len(names))
			text = fmt.Sprintf("\"%s\" alarmı %d varlıkta düzeldi: %s", g.rule, len(names), list)
		}
		m = notify.Message{Event: g.event, Severity: g.sev, Subject: subject, Text: text,
			Asset: fmt.Sprintf("%d varlık", len(names)), Rule: g.rule, Value: fmt.Sprintf("%s %g", g.cmp, g.thr),
			When: time.Now(), Link: s.publicLink()}
	}
	s.sendToChannels(ctx, g.tenant, m, g.event == "resolved")
}

// ---- Kapasite özeti (B1/B2): arayüzde "tahmini tur süresi" uyarısı için ----

// GET /api/scenarios/capacity
func (s *Server) scenarioCapacity(w http.ResponseWriter, r *http.Request, a Auth) {
	var browserN, httpN int
	var avgMS, avgInt float64
	s.DB.QueryRowContext(r.Context(),
		`SELECT count(*) FILTER (WHERE kind = 'browser'), count(*) FILTER (WHERE kind <> 'browser'),
		        COALESCE(avg(NULLIF(last_duration_ms,0)) FILTER (WHERE kind = 'browser'), 0),
		        COALESCE(avg(interval_sec) FILTER (WHERE kind = 'browser'), 0)
		 FROM scenarios WHERE tenant_id = $1 AND enabled`, a.TenantID).Scan(&browserN, &httpN, &avgMS, &avgInt)
	conc := cap(browserSem)
	if avgMS < 5000 {
		avgMS = 30000 // henüz ölçüm yoksa tipik bir tarayıcı senaryosu süresi varsayılır
	}
	est := 0.0
	if browserN > 0 {
		est = float64(browserN) * avgMS / 1000 / float64(conc)
	}
	writeJSON(w, 200, map[string]any{
		"browser_scenarios": browserN, "other_scenarios": httpN, "browser_concurrency": conc,
		"avg_browser_ms": int(avgMS), "est_round_sec": int(est), "avg_interval_sec": int(avgInt),
		"overloaded":        browserN > 0 && avgInt > 0 && est > avgInt,
		"probe_concurrency": cap(s.sems().probe), "scenario_concurrency": cap(s.sems().scen),
	})
}

var _ = http.StatusOK

type cacheEntry struct {
	at time.Time
	b  []byte
}

// cacheGet kısa ömürlü yanıt önbelleğinden okur (ağır özet sorguları 10 sn'de bir yenilenen sayfalarda tekrar tekrar çalışmasın).
func (s *Server) cacheGet(key string, ttl time.Duration) ([]byte, bool) {
	if v, ok := s.sems().cache.Load(key); ok {
		if e := v.(cacheEntry); time.Since(e.at) < ttl {
			return e.b, true
		}
	}
	return nil, false
}

func (s *Server) cacheSet(key string, b []byte) {
	s.sems().cache.Store(key, cacheEntry{at: time.Now(), b: b})
}
