package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"appmon/internal/probe"
)

// Saldırı yüzeyi sapması: normal izleme verisinden (SSH dinleyen portlar, erişim günlüğü uç noktaları, HTTP probe'unun
// gördüğü TLS sürümü / güvenlik başlıkları / banner) bir referans küme öğrenilir; sonradan görülen yeni veya kaybolan
// öğeler "sapma" olarak kaydedilir. İlk gözlem referans olur (olay üretmez); uç noktalar için ayrıca öğrenme süresi vardır.

const (
	surfLearnEndpoints = 24 * time.Hour // yeni uygulamada uç noktalar bu süre boyunca yalnızca öğrenilir
	surfMaxEndpoints   = 500            // uygulama başına izlenen en çok uç nokta
	surfBurstMin       = 40             // 4xx patlaması için son 10 dk'da en az 4xx sayısı
)

type surfOpts struct {
	learn       time.Duration // ilk gözlemden sonra olay üretmeden öğrenme süresi
	removeAfter int           // bu kadar gözlemde görünmeyen öğe kaldırılmış sayılır (0: asla)
	max         int           // en çok öğe (0: sınırsız)
}

// riskyPorts yüzeyde görünmemesi gereken, sık hedeflenen servis portlarıdır.
var riskyPorts = map[string]string{"21": "FTP", "23": "Telnet", "69": "TFTP", "135": "RPC", "139": "NetBIOS", "445": "SMB", "1433": "MSSQL",
	"1521": "Oracle", "2375": "Docker API", "3306": "MySQL", "3389": "RDP", "5432": "PostgreSQL", "5900": "VNC", "6379": "Redis",
	"9200": "Elasticsearch", "11211": "Memcached", "27017": "MongoDB"}

var riskyPathWords = []string{"admin", "debug", "actuator", "phpmyadmin", ".env", ".git", "swagger", "console", "backup", "wp-login", "shell", "manager"}

var secHeaderOrder = []string{"strict-transport-security", "content-security-policy", "x-frame-options", "x-content-type-options", "referrer-policy"}

// surfaceSeverity bir sapmanın önemini belirler.
func surfaceSeverity(kind, item, change string) string {
	switch kind {
	case "port":
		if change == "added" {
			if _, ok := riskyPorts[item]; ok {
				return "critical"
			}
			return "warning"
		}
		return "info"
	case "endpoint":
		l := strings.ToLower(item)
		for _, w := range riskyPathWords {
			if strings.Contains(l, w) {
				return "critical"
			}
		}
		return "warning"
	case "tls":
		if change == "added" {
			if item == "TLS 1.0" || item == "TLS 1.1" {
				return "critical"
			}
			return "warning" // sürüm değişti (örn. 1.3 → 1.2)
		}
		return "info"
	case "header":
		if change == "removed" {
			return "warning"
		}
		return "info"
	case "banner":
		return "info"
	case "scan":
		return "warning"
	}
	return "info"
}

// surfaceObserve (asset, tür) için gözlenen öğe kümesini referansla karşılaştırır, sapmaları kaydeder.
// details öğe → kısa açıklama (isteğe bağlı).
func (s *Server) surfaceObserve(ctx context.Context, tenant, asset int64, kind string, items []string, details map[string]string, o surfOpts) {
	if len(items) == 0 && o.removeAfter == 0 {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	var started time.Time
	if err := tx.QueryRowContext(ctx, `INSERT INTO surface_state (tenant_id, asset_id, kind) VALUES ($1,$2,$3)
		ON CONFLICT (tenant_id, asset_id, kind) DO UPDATE SET kind = EXCLUDED.kind RETURNING started_at`, tenant, asset, kind).Scan(&started); err != nil {
		return
	}
	var existing int
	tx.QueryRowContext(ctx, `SELECT count(*) FROM surface_items WHERE tenant_id = $1 AND asset_id = $2 AND kind = $3`, tenant, asset, kind).Scan(&existing)
	// İlk gözlem (referans yok) ve öğrenme süresi içindeyken olay üretilmez.
	quiet := existing == 0 || time.Since(started) < o.learn
	have := map[string]int{}
	rows, err := tx.QueryContext(ctx, `SELECT item, missed FROM surface_items WHERE tenant_id = $1 AND asset_id = $2 AND kind = $3`, tenant, asset, kind)
	if err != nil {
		return
	}
	for rows.Next() {
		var it string
		var m int
		if rows.Scan(&it, &m) == nil {
			have[it] = m
		}
	}
	rows.Close()
	seen := map[string]bool{}
	event := func(item, change string) {
		if quiet {
			return
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO surface_events (tenant_id, asset_id, kind, item, change, severity, detail) VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT DO NOTHING`, tenant, asset, kind, item, change, surfaceSeverity(kind, item, change), details[item]); err != nil {
			log.Printf("saldırı yüzeyi olayı yazılamadı: %v", err)
		}
	}
	for _, it := range items {
		if it == "" || seen[it] {
			continue
		}
		seen[it] = true
		if _, ok := have[it]; ok {
			tx.ExecContext(ctx, `UPDATE surface_items SET last_seen = now(), missed = 0 WHERE tenant_id = $1 AND asset_id = $2 AND kind = $3 AND item = $4`, tenant, asset, kind, it)
			continue
		}
		if o.max > 0 && len(have) >= o.max {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO surface_items (tenant_id, asset_id, kind, item) VALUES ($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, asset, kind, it); err != nil {
			return
		}
		have[it] = 0
		event(it, "added")
	}
	if o.removeAfter > 0 {
		for it, m := range have {
			if seen[it] {
				continue
			}
			if m+1 >= o.removeAfter {
				tx.ExecContext(ctx, `DELETE FROM surface_items WHERE tenant_id = $1 AND asset_id = $2 AND kind = $3 AND item = $4`, tenant, asset, kind, it)
				event(it, "removed")
			} else {
				tx.ExecContext(ctx, `UPDATE surface_items SET missed = missed + 1 WHERE tenant_id = $1 AND asset_id = $2 AND kind = $3 AND item = $4`, tenant, asset, kind, it)
			}
		}
	}
	tx.Commit()
}

// surfaceFromConns SSH ile okunan dinleyen portları gözlemler.
func (s *Server) surfaceFromConns(ctx context.Context, tenant, asset int64, snap *probe.ConnSnapshot) {
	if snap == nil || !snap.ListenOK {
		return
	}
	items := make([]string, 0, len(snap.Listen))
	det := map[string]string{}
	for _, p := range snap.Listen {
		k := strconv.Itoa(p)
		items = append(items, k)
		if n, ok := riskyPorts[k]; ok {
			det[k] = n
		}
	}
	s.surfaceObserve(ctx, tenant, asset, "port", items, det, surfOpts{removeAfter: 3})
}

// surfaceFromHTTP HTTP probe'unun gördüğü TLS sürümünü, güvenlik başlıklarını ve banner'ı gözlemler.
func (s *Server) surfaceFromHTTP(ctx context.Context, tenant, asset int64, sf *probe.HTTPSurface) {
	if sf == nil {
		return
	}
	if sf.TLS != "" {
		s.surfaceObserve(ctx, tenant, asset, "tls", []string{sf.TLS}, nil, surfOpts{removeAfter: 2})
		// Başlık kümesi yalnızca HTTPS yanıtlarında anlamlıdır (HSTS vb.).
		s.surfaceObserve(ctx, tenant, asset, "header", sf.Headers, nil, surfOpts{removeAfter: 2})
	}
	if sf.Banner != "" {
		s.surfaceObserve(ctx, tenant, asset, "banner", []string{sf.Banner}, nil, surfOpts{removeAfter: 2})
	}
}

// surfaceFromAccess bir uygulamanın erişim günlüğünde görülen uç noktaları gözlemler ve 4xx patlamasını (tarama izi) arar.
func (s *Server) surfaceFromAccess(ctx context.Context, tenant, asset int64, endpoints []string) {
	items := make([]string, 0, len(endpoints))
	for _, e := range endpoints {
		if !strings.HasPrefix(e, "OTHER ") {
			items = append(items, e)
		}
	}
	s.surfaceObserve(ctx, tenant, asset, "endpoint", items, nil, surfOpts{learn: surfLearnEndpoints, max: surfMaxEndpoints})
	s.surfaceBurst(ctx, tenant, asset)
}

// BurstDetected son iki dilimdeki 4xx sayısı eşik üstündeyse, istek oranının çoğunu oluşturuyorsa ve önceki
// dilimlerin medyanının çok üstündeyse true döner (tarama/kaba kuvvet izi). prev: önceki dilimlerin 4xx sayıları.
func BurstDetected(recent4, recentReqs int64, prev []int64) bool {
	if recent4 < surfBurstMin || recentReqs <= 0 || float64(recent4)/float64(recentReqs) < 0.5 {
		return false
	}
	p := make([]float64, len(prev))
	for i, v := range prev {
		p[i] = float64(v)
	}
	m := median(p) * 2 // son iki dilim ~ iki kat süre
	if m < 1 {
		m = 1
	}
	return float64(recent4) >= 8*m
}

func (s *Server) surfaceBurst(ctx context.Context, tenant, asset int64) {
	rows, err := s.DB.QueryContext(ctx, `SELECT bucket, sum(err4xx), sum(reqs) FROM http_endpoint_stats
		WHERE tenant_id = $1 AND asset_id = $2 AND bucket >= now() - interval '24 hours' GROUP BY bucket ORDER BY bucket DESC`, tenant, asset)
	if err != nil {
		return
	}
	defer rows.Close()
	var recent4, recentReqs int64
	var prev []int64
	i := 0
	for rows.Next() {
		var b time.Time
		var e4, rq int64
		if rows.Scan(&b, &e4, &rq) != nil {
			continue
		}
		if i < 2 {
			recent4 += e4
			recentReqs += rq
		} else {
			prev = append(prev, e4)
		}
		i++
	}
	if len(prev) < 12 { // yeterli geçmiş (≥1 saat) yoksa karar verme
		return
	}
	if BurstDetected(recent4, recentReqs, prev) {
		s.DB.ExecContext(ctx, `INSERT INTO surface_events (tenant_id, asset_id, kind, item, change, severity, detail) VALUES ($1,$2,'scan','4xx patlaması','burst','warning',$3)
			ON CONFLICT DO NOTHING`, tenant, asset, "son 10 dk: "+strconv.FormatInt(recent4, 10)+" adet 4xx / "+strconv.FormatInt(recentReqs, 10)+" istek")
	}
}

// surfaceMetrics gözden geçirilmemiş (uyarı/kritik) sapma sayısını surface.drift ölçümü olarak yazar; böylece
// "surface.drift > 0" alarm kuralı mevcut bildirim hattını kullanır. Yeni gözden geçirilen asset'e 0 yazılır (alarm kapanır).
func (s *Server) surfaceMetrics(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tenant_id, asset_id, count(*) FILTER (WHERE NOT reviewed AND severity <> 'info')
		FROM surface_events WHERE NOT reviewed OR reviewed_at > now() - interval '2 hours' GROUP BY tenant_id, asset_id`)
	if err != nil {
		return
	}
	type row struct {
		tenant, asset int64
		n             int
	}
	var list []row
	for rows.Next() {
		var r row
		if rows.Scan(&r.tenant, &r.asset, &r.n) == nil {
			list = append(list, r)
		}
	}
	rows.Close()
	if len(list) == 0 {
		return
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	now := time.Now()
	for _, r := range list {
		if err := insertMetrics(ctx, tx, r.tenant, r.asset, []point{{"surface.drift", float64(r.n), now}}); err != nil {
			log.Printf("surface.drift yazılamadı: %v", err)
			return
		}
	}
	tx.Commit()
}

// ---- API ----

// GET /api/surface/events?state=open|all
func (s *Server) surfaceEvents(w http.ResponseWriter, r *http.Request, a Auth) {
	q := `SELECT e.id, e.asset_id, a.name, e.kind, e.item, e.change, e.severity, e.detail, e.at, e.reviewed, e.reviewed_by, e.reviewed_at, e.note
	      FROM surface_events e JOIN assets a ON a.id = e.asset_id WHERE e.tenant_id = $1`
	if r.URL.Query().Get("state") != "all" {
		q += ` AND NOT e.reviewed`
	}
	rows, err := s.DB.QueryContext(r.Context(), q+` ORDER BY e.reviewed, CASE e.severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, e.at DESC LIMIT 500`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	open, crit := 0, 0
	for rows.Next() {
		var id, aid int64
		var asset, kind, item, change, sev, detail, by, note string
		var at time.Time
		var rev bool
		var revAt sql.NullTime
		if rows.Scan(&id, &aid, &asset, &kind, &item, &change, &sev, &detail, &at, &rev, &by, &revAt, &note) != nil {
			continue
		}
		it := map[string]any{"id": id, "asset_id": aid, "asset": asset, "kind": kind, "item": item, "change": change, "severity": sev,
			"detail": detail, "at": at, "reviewed": rev}
		if rev {
			it["reviewed_by"], it["note"] = by, note
			if revAt.Valid {
				it["reviewed_at"] = revAt.Time
			}
		} else {
			open++
			if sev == "critical" {
				crit++
			}
		}
		list = append(list, it)
	}
	writeJSON(w, 200, map[string]any{"events": list, "open": open, "critical": crit})
}

// POST /api/surface/events/{id}/review {"note": "..."} -> sapma gözden geçirildi (kabul edildi).
func (s *Server) surfaceReview(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if len(body.Note) > 300 {
		body.Note = body.Note[:300]
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE surface_events SET reviewed = true, reviewed_by = $3, reviewed_at = now(), note = $4
		WHERE id = $1 AND tenant_id = $2 AND NOT reviewed`, id, a.TenantID, a.Label, body.Note)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "sapma bulunamadı veya zaten gözden geçirildi")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "surface.review", strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/surface/baseline -> asset başına referans özeti.
func (s *Server) surfaceBaseline(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT i.asset_id, a.name, i.kind, i.item FROM surface_items i JOIN assets a ON a.id = i.asset_id
		WHERE i.tenant_id = $1 ORDER BY a.name, i.kind, i.item`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	type ent struct {
		ID    int64
		Name  string
		Items map[string][]string
	}
	byAsset := map[int64]*ent{}
	for rows.Next() {
		var id int64
		var name, kind, item string
		if rows.Scan(&id, &name, &kind, &item) != nil {
			continue
		}
		e := byAsset[id]
		if e == nil {
			e = &ent{id, name, map[string][]string{}}
			byAsset[id] = e
		}
		e.Items[kind] = append(e.Items[kind], item)
	}
	out := []map[string]any{}
	for _, e := range byAsset {
		m := map[string]any{"asset_id": e.ID, "asset": e.Name}
		for k, v := range e.Items {
			if k == "endpoint" {
				m["endpoints"] = len(v)
				continue
			}
			sort.Strings(v)
			m[k+"s"] = v
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["asset"].(string) < out[j]["asset"].(string) })
	writeJSON(w, 200, out)
}

// POST /api/surface/reset {"asset_id": N} -> referansı sıfırlar; yüzey yeniden öğrenilir (sapma üretilmez).
func (s *Server) surfaceReset(w http.ResponseWriter, r *http.Request, a Auth) {
	var body struct {
		AssetID int64 `json:"asset_id"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AssetID <= 0 {
		fail(w, 400, "geçersiz asset")
		return
	}
	var one int
	if s.DB.QueryRowContext(r.Context(), `SELECT 1 FROM assets WHERE id = $1 AND tenant_id = $2`, body.AssetID, a.TenantID).Scan(&one) != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	for _, q := range []string{`DELETE FROM surface_items WHERE tenant_id = $1 AND asset_id = $2`, `DELETE FROM surface_state WHERE tenant_id = $1 AND asset_id = $2`,
		`UPDATE surface_events SET reviewed = true, reviewed_by = '(referans sıfırlandı)', reviewed_at = now() WHERE tenant_id = $1 AND asset_id = $2 AND NOT reviewed`} {
		if _, err := s.DB.ExecContext(r.Context(), q, a.TenantID, body.AssetID); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	s.audit(r.Context(), a.TenantID, a.Label, "surface.reset", strconv.FormatInt(body.AssetID, 10))
	w.WriteHeader(http.StatusNoContent)
}
