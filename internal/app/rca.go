package app

import (
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Değişiklik-nedensellik motoru: bir alarm açıldığında, alarmlı asset'in kendisi ve (topolojideki) bağımlılıkları
// için ölçümlerde alarmdan önce başlayan ani değişimleri bulur, ilişkilendirir ve puanlayıp sıralar.
// Tamamen kural tabanlıdır (medyan/MAD farkı + zaman sırası + topoloji yakınlığı); dış servis veya model gerekmez.

// RPt bir ölçüm noktasıdır (dakikalık ortalama).
type RPt struct {
	T time.Time
	V float64
}

// Shift bir ölçümde tespit edilen ani değişimdir.
type Shift struct {
	Before, After float64
	Onset         time.Time
	Mag           float64 // değişimin gürültüye oranı (0-10)
}

const (
	rcaBaseWindow   = 60 * time.Minute // karşılaştırma için geriye bakış
	rcaGap          = 15 * time.Minute // temel pencerenin bittiği an (alarm anından geriye)
	rcaRecentWindow = 5 * time.Minute  // "son durum" penceresi (alarm anından geriye)
	rcaAfter        = 3 * time.Minute  // alarmdan sonra bakılan süre
)

// rcaSkip anlamlı bir "değişim" üretmeyen (sayaç, boyut, anomali türevi) ölçümlerdir.
func rcaSkip(name string) bool {
	switch name {
	case "host.uptime_sec", "db.size_mb", "db.deadlocks", "db.xact_commit", "db.xact_rollback":
		return true
	}
	return strings.HasPrefix(name, "anomaly.") || strings.HasSuffix(name, "_total") || strings.HasPrefix(name, "app.req_per_min")
}

func rcaMinAbs(name string) float64 {
	switch {
	case strings.HasSuffix(name, ".up"), strings.HasSuffix(name, ".down"), strings.HasSuffix(name, "changed"), name == "asset.degraded":
		return 0.5
	case strings.HasSuffix(name, "_ms"):
		return 20
	case strings.HasSuffix(name, "_pct"):
		return 10
	case strings.HasSuffix(name, "_s"):
		return 5
	}
	return 1
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	n := len(c)
	if n%2 == 1 {
		return c[n/2]
	}
	return (c[n/2-1] + c[n/2]) / 2
}

// DetectShift pts noktalarını alarm anına göre değerlendirir: [at-60dk, at-15dk) temel, [at-5dk, at+3dk] son durum.
func DetectShift(name string, pts []RPt, at time.Time) (Shift, bool) {
	var base, recent []float64
	for _, p := range pts {
		switch {
		case !p.T.Before(at.Add(-rcaBaseWindow)) && p.T.Before(at.Add(-rcaGap)):
			base = append(base, p.V)
		case !p.T.Before(at.Add(-rcaRecentWindow)) && !p.T.After(at.Add(rcaAfter)):
			recent = append(recent, p.V)
		}
	}
	if len(base) < 3 || len(recent) < 1 {
		return Shift{}, false
	}
	b, r := median(base), median(recent)
	dev := make([]float64, len(base))
	for i, v := range base {
		d := v - b
		if d < 0 {
			d = -d
		}
		dev[i] = d
	}
	spread := median(dev) * 1.4826
	d := r - b
	ad := d
	if ad < 0 {
		ad = -ad
	}
	minAbs := rcaMinAbs(name)
	floor := spread * 4
	if floor < minAbs {
		floor = minAbs
	}
	if ad < floor {
		return Shift{}, false
	}
	ab := b
	if ab < 0 {
		ab = -ab
	}
	if ab > 0 && ad < 0.3*ab {
		return Shift{}, false
	}
	// Başlangıç: sapmanın yarısını aşan ve sonraki noktada da sürenin ilki.
	onset := time.Time{}
	var tail []RPt
	for _, p := range pts {
		if !p.T.Before(at.Add(-rcaBaseWindow)) && !p.T.After(at.Add(rcaAfter)) {
			tail = append(tail, p)
		}
	}
	sort.Slice(tail, func(i, j int) bool { return tail[i].T.Before(tail[j].T) })
	dev2 := func(v float64) bool {
		x := v - b
		if x < 0 {
			x = -x
		}
		return x >= ad/2 && (v-b)*d > 0
	}
	for i, p := range tail {
		if dev2(p.V) && (i == len(tail)-1 || dev2(tail[i+1].V)) {
			onset = p.T
			break
		}
	}
	if onset.IsZero() {
		onset = tail[len(tail)-1].T
	}
	den := spread
	if den < minAbs {
		den = minAbs
	}
	mag := ad / den
	if minAbs == 0.5 { // bayrak türü ölçümler (0/1): her çevrilme güçlü kanıttır
		mag = 8
	} else if lo, hi := minf(ab, absf(r)), maxf(ab, absf(r)); lo > 0 && hi/lo >= 2 {
		if m2 := 2 * math.Log2(hi/lo); m2 > mag { // büyük oransal sıçrama
			mag = m2
		}
	}
	if mag > 10 {
		mag = 10
	}
	return Shift{Before: b, After: r, Onset: onset, Mag: mag}, true
}

// rcaKind ölçüm adından, arayüzde gösterilecek değişim türünü çıkarır.
func rcaKind(name string) string {
	switch {
	case name == "asset.down" || strings.HasSuffix(name, ".up"):
		return "down"
	case strings.HasPrefix(name, "net.path."):
		return "route"
	case strings.HasPrefix(name, "db."):
		return "db"
	case strings.HasPrefix(name, "app."):
		return "app"
	case strings.HasPrefix(name, "host.conn"):
		return "conns"
	case strings.HasSuffix(name, "_ms") || strings.Contains(name, "latency"):
		return "latency"
	case strings.HasSuffix(name, "_pct"):
		return "load"
	case strings.Contains(name, "cert"):
		return "cert"
	}
	return "other"
}

// RCAScore bir değişimin kök neden olma olasılığını gösteren puandır.
func RCAScore(sh Shift, kind string, at time.Time, depth int) float64 {
	lead := at.Sub(sh.Onset).Seconds() // pozitif: değişim alarmdan önce başladı
	tf := 0.3
	if lead >= -30 {
		l := lead
		if l > 600 {
			l = 600
		}
		if l < 0 {
			l = 0
		}
		tf = 1 + l/1200
	}
	df := 1.0
	if depth > 0 && depth <= 3 {
		df = 1.2
	}
	kf := 1.0
	switch kind {
	case "down":
		kf = 2
	case "route", "cert":
		kf = 1.5
	}
	return sh.Mag * tf * df * kf
}

func rcaConfidence(score float64) string {
	switch {
	case score >= 8:
		return "high"
	case score >= 4:
		return "medium"
	}
	return "low"
}

type rcaCause struct {
	Asset      string    `json:"asset"`
	AssetID    int64     `json:"asset_id"`
	Depth      int       `json:"depth"` // 0: alarmlı asset, 1+: bağımlılık seviyesi
	Metric     string    `json:"metric"`
	Kind       string    `json:"kind"`
	Before     float64   `json:"before"`
	After      float64   `json:"after"`
	Onset      time.Time `json:"onset"`
	LeadSec    int       `json:"lead_sec"` // alarmdan kaç saniye önce başladı (negatif: sonra)
	Score      float64   `json:"score"`
	Confidence string    `json:"confidence"`
}

// GET /api/alerts/{id}/rca -> alarmın olası nedenleri.
func (s *Server) alertRCA(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var assetID int64
	var assetName, metric string
	var opened time.Time
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT al.asset_id, a.name, r.metric, al.opened_at FROM alerts al JOIN alert_rules r ON r.id = al.rule_id
		 JOIN assets a ON a.id = al.asset_id WHERE al.id = $1 AND al.tenant_id = $2`, id, a.TenantID).Scan(&assetID, &assetName, &metric, &opened)
	if err != nil {
		fail(w, 404, "alarm bulunamadı")
		return
	}
	// Aday asset'ler: alarmlı asset + bağımlılıkları (en çok 3 seviye, en çok 40 asset).
	type cand struct {
		name  string
		depth int
	}
	cands := map[int64]cand{assetID: {assetName, 0}}
	deps, err := s.traverse(r, a.TenantID, assetID, "source_asset_id", "target_asset_id")
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	for _, d := range deps {
		if len(cands) >= 40 {
			break
		}
		dep := d["depth"].(int)
		if dep > 3 {
			continue
		}
		cands[d["id"].(int64)] = cand{d["name"].(string), dep}
	}
	ids := make([]int64, 0, len(cands))
	for k := range cands {
		ids = append(ids, k)
	}
	from, to := opened.Add(-rcaBaseWindow), opened.Add(rcaAfter)
	if now := time.Now(); to.After(now) {
		to = now
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT asset_id, name, date_trunc('minute', time) AS m, avg(value) FROM metrics
		 WHERE tenant_id = $1 AND asset_id = ANY($2::bigint[]) AND time >= $3 AND time <= $4
		 GROUP BY asset_id, name, m`, a.TenantID, pqInt64Array(ids), from, to)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	type key struct {
		asset int64
		name  string
	}
	series := map[key][]RPt{}
	for rows.Next() {
		var aid int64
		var name string
		var m time.Time
		var v float64
		if rows.Scan(&aid, &name, &m, &v) == nil {
			k := key{aid, name}
			series[k] = append(series[k], RPt{m, v})
		}
	}
	rows.Close()
	causes := []rcaCause{}
	for k, pts := range series {
		if rcaSkip(k.name) || (k.asset == assetID && k.name == metric) {
			continue
		}
		sh, ok := DetectShift(k.name, pts, opened)
		if !ok {
			continue
		}
		c := cands[k.asset]
		kind := rcaKind(k.name)
		sc := RCAScore(sh, kind, opened, c.depth)
		causes = append(causes, rcaCause{Asset: c.name, AssetID: k.asset, Depth: c.depth, Metric: k.name, Kind: kind,
			Before: sh.Before, After: sh.After, Onset: sh.Onset, LeadSec: int(opened.Sub(sh.Onset).Seconds()),
			Score: float64(int(sc*10)) / 10, Confidence: rcaConfidence(sc)})
	}
	sort.Slice(causes, func(i, j int) bool {
		if causes[i].Score != causes[j].Score {
			return causes[i].Score > causes[j].Score
		}
		return causes[i].Metric < causes[j].Metric
	})
	if len(causes) > 8 {
		causes = causes[:8]
	}
	// Yönetim değişiklikleri (denetim günlüğü): alarmdan önceki 60 dk ile sonraki 1 dk.
	type chg struct {
		At     time.Time `json:"at"`
		Actor  string    `json:"actor"`
		Action string    `json:"action"`
		Detail string    `json:"detail"`
	}
	changes := []chg{}
	if ar, err := s.DB.QueryContext(r.Context(),
		`SELECT at, actor, action, detail FROM audit_log WHERE tenant_id = $1 AND at >= $2 AND at <= $3
		 AND action NOT LIKE 'login%' AND action NOT LIKE 'alert.%' AND action NOT LIKE 'key.%' AND action NOT LIKE 'license.%' ORDER BY at DESC LIMIT 8`, a.TenantID, from, opened.Add(time.Minute)); err == nil {
		for ar.Next() {
			var c chg
			if ar.Scan(&c.At, &c.Actor, &c.Action, &c.Detail) == nil {
				changes = append(changes, c)
			}
		}
		ar.Close()
	}
	writeJSON(w, 200, map[string]any{"alert_id": id, "asset": assetName, "asset_id": assetID, "metric": metric, "opened_at": opened,
		"checked_assets": len(cands), "causes": causes, "changes": changes})
}

// pqInt64Array bigint[] parametresi için PostgreSQL dizi sabiti üretir (yalnızca sayılardan oluşur).
func pqInt64Array(v []int64) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatInt(x, 10))
	}
	b.WriteByte('}')
	return b.String()
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
