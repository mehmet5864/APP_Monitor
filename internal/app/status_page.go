package app

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

type statusItem struct {
	Name   string     `json:"name"`
	Status string     `json:"status"` // up | degraded | down | unknown
	Uptime *float64   `json:"uptime_pct"`
	Days   []*float64 `json:"days"` // son 90 gün, eskiden yeniye; veri yoksa null
}

type statusDoc struct {
	Title     string       `json:"title"`
	Updated   time.Time    `json:"updated"`
	Overall   string       `json:"overall"`
	Items     []statusItem `json:"items"`
	Published bool         `json:"published"`
}

type statusEntry struct {
	at  time.Time
	doc *statusDoc
}

var statusCache = struct {
	sync.Mutex
	m map[int64]statusEntry
}{m: map[int64]statusEntry{}}

// buildStatus işaretli asset'lerin anlık durumunu ve 90 günlük erişilebilirliğini derler (varsayılan kiracı).
func (s *Server) buildStatus(ctx context.Context, tenant int64) (*statusDoc, error) {
	doc := &statusDoc{Title: s.StatusTitle, Updated: time.Now().UTC(), Items: []statusItem{}}
	if doc.Title == "" {
		doc.Title = "Hizmet durumu"
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, CASE WHEN status_name <> '' THEN status_name ELSE name END, status FROM assets WHERE tenant_id = $1 AND public_status ORDER BY lower(CASE WHEN status_name <> '' THEN status_name ELSE name END)`, tenant)
	if err != nil {
		return nil, err
	}
	type ent struct {
		id int64
		it *statusItem
	}
	var list []ent
	for rows.Next() {
		var id int64
		var it statusItem
		var st string
		if rows.Scan(&id, &it.Name, &st) != nil {
			continue
		}
		switch st {
		case "up", "degraded", "down":
			it.Status = st
		case "offline":
			it.Status = "down"
		default:
			it.Status = "unknown"
		}
		it.Days = make([]*float64, 90)
		list = append(list, ent{id, &it})
	}
	rows.Close()
	doc.Published = len(list) > 0
	today := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local)
	for _, e := range list {
		dr, err := s.DB.QueryContext(ctx, `SELECT day, observed_s, down_s, maint_s FROM sla_daily WHERE asset_id = $1 AND day > $2::date - 90 ORDER BY day`, e.id, today.Format("2006-01-02"))
		if err != nil {
			return nil, err
		}
		var obsT, downT int
		for dr.Next() {
			var d time.Time
			var obs, down, maint int
			if dr.Scan(&d, &obs, &down, &maint) != nil {
				continue
			}
			idx := 89 - int(today.Sub(time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.Local)).Hours()/24+0.5)
			if idx < 0 || idx > 89 || obs-maint <= 0 {
				continue
			}
			v := slaAvail(obs, down, maint)
			e.it.Days[idx] = &v
			obsT += obs - maint
			if x := down - maint; x > 0 {
				downT += x
			}
		}
		dr.Close()
		if obsT > 0 {
			u := 100 * (1 - float64(downT)/float64(obsT))
			e.it.Uptime = &u
		}
		doc.Items = append(doc.Items, *e.it)
	}
	doc.Overall = "up"
	for _, it := range doc.Items {
		if it.Status == "down" {
			doc.Overall = "down"
			break
		}
		if it.Status == "degraded" || it.Status == "unknown" {
			doc.Overall = "degraded"
		}
	}
	return doc, nil
}

// publicStatus GET /api/public/status — kimlik gerektirmez; yalnızca işaretli asset'leri gösterir. 30 sn önbelleğe alınır.
func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	tenant, ok := s.tenantBySlug(r.Context(), strings.ToLower(strings.TrimSpace(r.URL.Query().Get("t"))))
	if !ok {
		fail(w, 404, "durum sayfası bulunamadı")
		return
	}
	statusCache.Lock()
	if e, ok := statusCache.m[tenant]; ok && time.Since(e.at) < 30*time.Second {
		statusCache.Unlock()
		w.Header().Set("Cache-Control", "public, max-age=30")
		writeJSON(w, 200, e.doc)
		return
	}
	statusCache.Unlock()
	doc, err := s.buildStatus(r.Context(), tenant)
	if err != nil {
		fail(w, 500, "durum alınamadı")
		return
	}
	statusCache.Lock()
	statusCache.m[tenant] = statusEntry{time.Now(), doc}
	statusCache.Unlock()
	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, 200, doc)
}

// listStatusAssets GET /api/status-page — yönetici: tüm asset'ler ve yayın durumları.
func (s *Server) listStatusAssets(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, name, status_name, public_status FROM assets WHERE tenant_id = $1 ORDER BY lower(name)`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, sn string
		var pub bool
		if rows.Scan(&id, &name, &sn, &pub) == nil {
			list = append(list, map[string]any{"id": id, "name": name, "status_name": sn, "public": pub})
		}
	}
	writeJSON(w, 200, map[string]any{"title": s.StatusTitle, "assets": list})
}

// setStatusAsset PUT /api/assets/{id}/status-page {public, name}
func (s *Server) setStatusAsset(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Public bool   `json:"public"`
		Name   string `json:"name"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Name) > 100 {
		fail(w, 400, "görünen ad en fazla 100 karakter olabilir")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE assets SET public_status = $1, status_name = $2 WHERE id = $3 AND tenant_id = $4`, req.Public, req.Name, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "asset bulunamadı")
		return
	}
	statusCache.Lock()
	delete(statusCache.m, a.TenantID)
	statusCache.Unlock()
	s.audit(r.Context(), a.TenantID, a.Label, "status_page", req.Name)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}
