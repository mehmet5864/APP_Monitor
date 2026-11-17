package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Toplu işlemler (C2): 1000 probe / 150 senaryo yönetimi için çoklu seçim işlemleri ve CSV/JSON ile içe aktarma.

type bulkReq struct {
	IDs         []int64 `json:"ids"`
	Action      string  `json:"action"` // enable | disable | delete | set
	IntervalSec int     `json:"interval_sec"`
	TimeoutMS   int     `json:"timeout_ms"`
}

func (s *Server) decodeBulk(w http.ResponseWriter, r *http.Request) (bulkReq, bool) {
	var req bulkReq
	if !decode(w, r, &req) {
		return req, false
	}
	if len(req.IDs) == 0 || len(req.IDs) > 5000 {
		fail(w, 400, "ids en az 1, en fazla 5000 olmalı")
		return req, false
	}
	switch req.Action {
	case "enable", "disable", "delete", "set":
	default:
		fail(w, 400, "action enable, disable, delete veya set olmalı")
		return req, false
	}
	return req, true
}

// POST /api/probes/bulk
func (s *Server) probesBulk(w http.ResponseWriter, r *http.Request, a Auth) {
	req, ok := s.decodeBulk(w, r)
	if !ok {
		return
	}
	ids := pq.Array(req.IDs)
	var n int64
	var err error
	exec := func(q string, args ...any) {
		var res interface{ RowsAffected() (int64, error) }
		res, err = s.DB.ExecContext(r.Context(), q, args...)
		if err == nil {
			n, _ = res.RowsAffected()
		}
	}
	switch req.Action {
	case "enable", "disable":
		exec(`UPDATE probes SET enabled = $3 WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, a.TenantID, ids, req.Action == "enable")
	case "delete":
		exec(`DELETE FROM probes WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, a.TenantID, ids)
	case "set":
		if req.IntervalSec == 0 && req.TimeoutMS == 0 {
			fail(w, 400, "interval_sec veya timeout_ms verin")
			return
		}
		if req.IntervalSec != 0 && req.IntervalSec < 5 {
			fail(w, 400, "interval_sec en az 5 olmalı")
			return
		}
		if req.TimeoutMS != 0 && (req.TimeoutMS < 100 || req.TimeoutMS > 60000) {
			fail(w, 400, "timeout_ms 100 ile 60000 arasında olmalı")
			return
		}
		if q, qe := s.quotasOf(r.Context(), a.TenantID); qe == nil && req.IntervalSec != 0 && req.IntervalSec < q.MinInterval {
			fail(w, 400, fmt.Sprintf("bu kiracıda en kısa probe aralığı %d sn", q.MinInterval))
			return
		}
		exec(`UPDATE probes SET interval_sec = COALESCE(NULLIF($3,0), interval_sec), timeout_ms = COALESCE(NULLIF($4,0), timeout_ms)
		      WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, a.TenantID, ids, req.IntervalSec, req.TimeoutMS)
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "probe.bulk_"+req.Action, strconv.FormatInt(n, 10)+" probe")
	writeJSON(w, 200, map[string]any{"updated": n})
}

// POST /api/scenarios/bulk
func (s *Server) scenariosBulk(w http.ResponseWriter, r *http.Request, a Auth) {
	req, ok := s.decodeBulk(w, r)
	if !ok {
		return
	}
	ids := pq.Array(req.IDs)
	var n int64
	var err error
	exec := func(q string, args ...any) {
		var res interface{ RowsAffected() (int64, error) }
		res, err = s.DB.ExecContext(r.Context(), q, args...)
		if err == nil {
			n, _ = res.RowsAffected()
		}
	}
	switch req.Action {
	case "enable", "disable":
		exec(`UPDATE scenarios SET enabled = $3 WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, a.TenantID, ids, req.Action == "enable")
	case "delete":
		exec(`DELETE FROM scenarios WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, a.TenantID, ids)
	case "set":
		if req.IntervalSec < 30 {
			fail(w, 400, "interval_sec en az 30 olmalı")
			return
		}
		// tarayıcı senaryolarında en az 60 sn
		exec(`UPDATE scenarios SET interval_sec = CASE WHEN kind = 'browser' THEN GREATEST($3, 60) ELSE $3 END
		      WHERE tenant_id = $1 AND id = ANY($2::bigint[])`, a.TenantID, ids, req.IntervalSec)
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "scenario.bulk_"+req.Action, strconv.FormatInt(n, 10)+" senaryo")
	writeJSON(w, 200, map[string]any{"updated": n})
}

type importRow struct {
	Asset       string `json:"asset"`
	Type        string `json:"type"`
	Target      string `json:"target"`
	IntervalSec int    `json:"interval_sec"`
	TimeoutMS   int    `json:"timeout_ms"`
}

// POST /api/probes/import: {"rows":[{asset,type,target,interval_sec,timeout_ms}]} (en çok 2000 satır).
// Her satır createProbe ile aynı doğrulama, kota ve lisans denetiminden geçer; satır başına sonuç döner.
func (s *Server) probesImport(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		Rows   []importRow `json:"rows"`
		DryRun bool        `json:"dry_run"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Rows) == 0 || len(req.Rows) > 2000 {
		fail(w, 400, "rows en az 1, en fazla 2000 olmalı")
		return
	}
	type result struct {
		Row   int    `json:"row"`
		OK    bool   `json:"ok"`
		Error string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(req.Rows))
	created, failed := 0, 0
	for i, row := range req.Rows {
		res := result{Row: i + 1}
		row.Asset, row.Type, row.Target = strings.TrimSpace(row.Asset), strings.ToLower(strings.TrimSpace(row.Type)), strings.TrimSpace(row.Target)
		if row.Asset == "" {
			row.Asset = row.Target
		}
		if row.Type == "" || row.Target == "" {
			res.Error = "type ve target zorunlu"
		} else if req.DryRun {
			if err := validateTarget(row.Type, row.Target); err != nil {
				res.Error = err.Error()
			}
		} else {
			body, _ := json.Marshal(map[string]any{"asset_name": row.Asset, "type": row.Type, "target": row.Target, "interval_sec": row.IntervalSec, "timeout_ms": row.TimeoutMS})
			rr := httptest.NewRecorder()
			hr := httptest.NewRequest("POST", "/api/probes", bytes.NewReader(body)).WithContext(r.Context())
			s.createProbe(rr, hr, a)
			if rr.Code >= 300 {
				var e struct {
					Error string `json:"error"`
				}
				json.Unmarshal(rr.Body.Bytes(), &e)
				res.Error = e.Error
				if res.Error == "" {
					res.Error = "HTTP " + strconv.Itoa(rr.Code)
				}
			}
		}
		res.OK = res.Error == ""
		if res.OK {
			created++
		} else {
			failed++
		}
		results = append(results, res)
	}
	if !req.DryRun {
		s.audit(r.Context(), a.TenantID, a.Label, "probe.import", fmt.Sprintf("%d eklendi, %d hatalı", created, failed))
	}
	writeJSON(w, 200, map[string]any{"created": created, "failed": failed, "dry_run": req.DryRun, "results": results})
}

// ---- Ingest oran sınırı (E2): asset adı başına jeton kovası; hatalı bir ajan sunucuyu dolduramaz ----

type bucket struct {
	tokens float64
	at     time.Time
}

// ingestAllow (kiracı, asset adı) için bir istek hakkı verir. Hız saniyede IngestRate (varsayılan 2), patlama 20.
// IngestRate < 0 ise sınır kapalıdır.
func (s *Server) ingestAllow(tenant int64, name string) (ok bool, retryAfter int) {
	rate := s.IngestRate
	if rate == 0 {
		rate = 2
	}
	if rate < 0 {
		return true, 0
	}
	const burst = 20.0
	key := strconv.FormatInt(tenant, 10) + "|" + name
	now := time.Now()
	st := s.sems()
	st.rateMu.Lock()
	defer st.rateMu.Unlock()
	if st.rate == nil {
		st.rate = map[string]*bucket{}
	}
	if len(st.rate) > 20000 { // boşta kalan kovaları temizle
		for k, b := range st.rate {
			if now.Sub(b.at) > 10*time.Minute {
				delete(st.rate, k)
			}
		}
	}
	b := st.rate[key]
	if b == nil {
		b = &bucket{tokens: burst, at: now}
		st.rate[key] = b
	}
	b.tokens += now.Sub(b.at).Seconds() * float64(rate)
	if b.tokens > burst {
		b.tokens = burst
	}
	b.at = now
	if b.tokens < 1 {
		return false, int((1-b.tokens)/float64(rate)) + 1
	}
	b.tokens--
	return true, 0
}
