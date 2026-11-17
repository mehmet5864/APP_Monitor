package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"appmon/internal/browser"
	"appmon/internal/crawl"
)

// Web sitesi keşfi (Keşfet). Güvenlik çerçevesi:
//   - her seçenek kapalı gelir ve ayrı ayrı bilinçli seçilir (spider / yol sözlüğü / oturumlu sayfalar),
//   - başlatan "siteyi izleme yetkim var" beyanını onaylar; yönetici değilse iş yönetici onayına düşer,
//   - robots.txt her zaman uygulanır; bekleme, sayfa ve derinlik sınırları zorunludur,
//   - hiçbir form gönderilmez, oturumu kapatan/silen bağlantılar gezilmez,
//   - aynı anda tek keşif çalışır; saatte en çok 10 iş istenebilir.

var crawlSem = make(chan struct{}, 1)

type crawlRuns struct {
	mu sync.Mutex
	m  map[int64]context.CancelFunc
}

var crawlActive = crawlRuns{m: map[int64]context.CancelFunc{}}

type crawlOpts struct {
	Spider     bool     `json:"spider"`
	Wordlist   string   `json:"wordlist"`
	Extra      []string `json:"extra,omitempty"`
	MaxPages   int      `json:"max_pages"`
	MaxDepth   int      `json:"max_depth"`
	DelayMS    int      `json:"delay_ms"`
	ScenarioID int64    `json:"scenario_id,omitempty"` // oturumlu tarama: giriş ayarı bu senaryodan alınır
	Declared   bool     `json:"declared"`
}

type crawlReq struct {
	URL string `json:"url"`
	crawlOpts
}

func (s *Server) crawlValidate(ctx context.Context, tenant int64, req *crawlReq) error {
	u := crawl.Norm(req.URL, nil)
	if u == nil || len(req.URL) > 2000 {
		return errors.New("adres http:// veya https:// ile başlamalı")
	}
	req.URL = u.String()
	if !req.Spider && req.Wordlist == "" && len(req.Extra) == 0 {
		return errors.New("en az bir seçenek işaretleyin (siteyi gez veya yol sözlüğü)")
	}
	if req.Spider && s.Browser == nil {
		return errBrowserOff
	}
	if q, err := s.quotasOf(ctx, tenant); err == nil {
		if !q.AllowCrawl {
			return tenantFeatureErr("Keşfet")
		}
		if req.Spider && !q.AllowBrowser {
			return tenantFeatureErr("Tarayıcı ile gezme")
		}
	}
	if req.Wordlist != "" && len(crawl.Words(req.Wordlist)) == 0 {
		return errors.New("bilinmeyen sözlük")
	}
	if len(req.Extra) > 100 {
		return errors.New("en çok 100 ek yol girilebilir")
	}
	var clean []string
	for _, p := range req.Extra {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		if len(p) > 200 || strings.ContainsAny(p, " \t\r\n?#") {
			return errors.New("geçersiz ek yol: " + p)
		}
		clean = append(clean, p)
	}
	req.Extra = clean
	if !req.Declared {
		return errors.New("bu sitenin sahibi veya izleme yetkilisi olduğunuzu onaylamalısınız")
	}
	if req.MaxPages <= 0 {
		req.MaxPages = 30
	}
	req.MaxPages = min(req.MaxPages, crawl.MaxPagesHi)
	if req.MaxDepth <= 0 {
		req.MaxDepth = 2
	}
	req.MaxDepth = min(req.MaxDepth, crawl.MaxDepthHi)
	if req.DelayMS < 200 {
		req.DelayMS = 500
	}
	req.DelayMS = min(req.DelayMS, 10000)
	if req.ScenarioID != 0 {
		if !req.Spider {
			return errors.New("oturumlu tarama için \"siteyi gez\" seçeneği de işaretli olmalı")
		}
		auth, _, _, blocked, err := s.crawlAuth(ctx, tenant, req.ScenarioID)
		if err != nil {
			return err
		}
		if blocked {
			return errors.New("bu senaryonun otomatik girişi durdurulmuş (hesap kilitlenmesin diye); önce senaryodan \"Girişi yeniden dene\" deyin")
		}
		chk := auth.CheckURL
		if chk == "" {
			return errors.New("senaryonun giriş denetim adresi yok")
		}
		if cu, err := url.Parse(chk); err != nil || !strings.EqualFold(cu.Host, u.Host) {
			return errors.New("oturumlu tarama yalnızca senaryonun giriş yaptığı alan adında yapılabilir (kimlik bilgisi başka siteye gitmesin diye)")
		}
	}
	return nil
}

// crawlAuth senaryonun giriş ayarını, saklanan çerezlerini, adımlarını ve kilit durumunu okur.
func (s *Server) crawlAuth(ctx context.Context, tenant, id int64) (*browser.Auth, []browser.Cookie, []browser.Step, bool, error) {
	var authRaw, sessRaw, stepsRaw []byte
	var kind string
	var blocked bool
	err := s.DB.QueryRowContext(ctx, `SELECT auth, session, steps, kind, auth_blocked FROM scenarios WHERE id = $1 AND tenant_id = $2`, id, tenant).Scan(&authRaw, &sessRaw, &stepsRaw, &kind, &blocked)
	if err != nil {
		return nil, nil, nil, false, errors.New("seçilen senaryo bulunamadı")
	}
	var au browser.Auth
	if kind != "browser" || json.Unmarshal(authRaw, &au) != nil || !au.Enabled {
		return nil, nil, nil, false, errors.New("seçilen senaryo giriş (kimlik doğrulama) kullanan bir tarayıcı senaryosu değil")
	}
	var cookies []browser.Cookie
	json.Unmarshal(sessRaw, &cookies)
	var steps []browser.Step
	json.Unmarshal(stepsRaw, &steps)
	return &au, cookies, steps, blocked, nil
}

// POST /api/crawl
func (s *Server) crawlCreate(w http.ResponseWriter, r *http.Request, a Auth) {
	if s.isPaused(r.Context(), a.TenantID) {
		fail(w, 409, errPaused.Error())
		return
	}
	var req crawlReq
	if !decode(w, r, &req) {
		return
	}
	if err := s.crawlValidate(r.Context(), a.TenantID, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var recent, pending int
	s.DB.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE created_at > now() - interval '1 hour'), count(*) FILTER (WHERE status = 'pending') FROM crawl_jobs WHERE tenant_id = $1`, a.TenantID).Scan(&recent, &pending)
	if recent >= 10 {
		fail(w, 429, "saatte en çok 10 keşif istenebilir")
		return
	}
	if pending >= 20 {
		fail(w, 429, "onay bekleyen çok sayıda keşif var; önce yönetici bunları sonuçlandırsın")
		return
	}
	status := "pending"
	if a.Role == "admin" {
		status = "approved" // yönetici beyanı kendisi onaylar; denetim kaydında görünür
	}
	opts, _ := json.Marshal(req.crawlOpts)
	var scen any
	if req.ScenarioID != 0 {
		scen = req.ScenarioID
	}
	var id int64
	err := s.DB.QueryRowContext(r.Context(),
		`INSERT INTO crawl_jobs (tenant_id, url, options, status, requested_by, scenario_id, decided_by, decided_at)
		 VALUES ($1,$2,$3,$4,$5,$6, CASE WHEN $4 = 'approved' THEN $5 ELSE '' END, CASE WHEN $4 = 'approved' THEN now() END) RETURNING id`,
		a.TenantID, req.URL, opts, status, a.Label, scen).Scan(&id)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "crawl.request", fmt.Sprintf("#%d %s [%s] durum=%s", id, req.URL, crawlSummary(req.crawlOpts), status))
	if status == "approved" {
		s.crawlStart(id, a.TenantID)
	}
	writeJSON(w, 201, map[string]any{"id": id, "status": status})
}

func crawlSummary(o crawlOpts) string {
	var p []string
	if o.Spider {
		p = append(p, "gezgin")
	}
	if o.Wordlist != "" {
		p = append(p, "sözlük:"+o.Wordlist)
	}
	if len(o.Extra) > 0 {
		p = append(p, fmt.Sprintf("ek yol:%d", len(o.Extra)))
	}
	if o.ScenarioID != 0 {
		p = append(p, "oturumlu(senaryo #"+strconv.FormatInt(o.ScenarioID, 10)+")")
	}
	return strings.Join(p, ", ")
}

// GET /api/crawl
func (s *Server) crawlList(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, url, options, status, requested_by, decided_by, decided_at, note, progress, error, created_at, started_at, finished_at,
		        COALESCE(jsonb_array_length(result->'pages'),0), COALESCE(jsonb_array_length(result->'hits'),0)
		 FROM crawl_jobs WHERE tenant_id = $1 ORDER BY id DESC LIMIT 100`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var u, st, by, dby, note, prog, errS string
		var optsRaw []byte
		var decAt, started, finished sql.NullTime
		var created time.Time
		var np, nh int
		if rows.Scan(&id, &u, &optsRaw, &st, &by, &dby, &decAt, &note, &prog, &errS, &created, &started, &finished, &np, &nh) != nil {
			continue
		}
		var o crawlOpts
		json.Unmarshal(optsRaw, &o)
		it := map[string]any{"id": id, "url": u, "options": o, "summary": crawlSummary(o), "status": st, "requested_by": by, "decided_by": dby, "note": note,
			"progress": prog, "error": errS, "created_at": created, "pages": np, "hits": nh}
		if finished.Valid {
			it["finished_at"] = finished.Time
		}
		if started.Valid {
			it["started_at"] = started.Time
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

// GET /api/crawl/{id}
func (s *Server) crawlGet(w http.ResponseWriter, r *http.Request, a Auth) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var u, st, errS string
	var res, optsRaw []byte
	if err := s.DB.QueryRowContext(r.Context(), `SELECT url, status, error, result, options FROM crawl_jobs WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&u, &st, &errS, &res, &optsRaw); err != nil {
		fail(w, 404, "keşif bulunamadı")
		return
	}
	var o crawlOpts
	json.Unmarshal(optsRaw, &o)
	out := map[string]any{"id": id, "url": u, "status": st, "error": errS, "options": o}
	if len(res) > 0 {
		out["result"] = json.RawMessage(res)
	}
	writeJSON(w, 200, out)
}

// POST /api/crawl/{id}/approve | reject  (yönetici)
func (s *Server) crawlDecide(approve bool) handler {
	return func(w http.ResponseWriter, r *http.Request, a Auth) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var req struct {
			Note string `json:"note"`
		}
		if r.ContentLength != 0 && !decode(w, r, &req) {
			return
		}
		if len(req.Note) > 300 {
			req.Note = req.Note[:300]
		}
		to := "rejected"
		if approve {
			to = "approved"
		}
		res, err := s.DB.ExecContext(r.Context(), `UPDATE crawl_jobs SET status = $3, decided_by = $4, decided_at = now(), note = $5 WHERE id = $1 AND tenant_id = $2 AND status = 'pending'`, id, a.TenantID, to, a.Label, req.Note)
		if err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			fail(w, 409, "keşif onay beklemiyor")
			return
		}
		s.audit(r.Context(), a.TenantID, a.Label, "crawl."+map[bool]string{true: "approve", false: "reject"}[approve], "#"+strconv.FormatInt(id, 10))
		if approve {
			s.crawlStart(id, a.TenantID)
		}
		writeJSON(w, 200, map[string]any{"id": id, "status": to})
	}
}

// DELETE /api/crawl/{id}: çalışanı durdurur; bekleyen/biten kaydı siler (yalnızca isteyen veya yönetici).
func (s *Server) crawlDelete(w http.ResponseWriter, r *http.Request, a Auth) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var by, st string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT requested_by, status FROM crawl_jobs WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&by, &st); err != nil {
		fail(w, 404, "keşif bulunamadı")
		return
	}
	if a.Role != "admin" && by != a.Label {
		fail(w, 403, "yalnızca isteği yapan veya yönetici iptal edebilir")
		return
	}
	if st == "running" || st == "approved" {
		crawlActive.mu.Lock()
		cancel := crawlActive.m[id]
		crawlActive.mu.Unlock()
		if cancel != nil {
			cancel()
			s.audit(r.Context(), a.TenantID, a.Label, "crawl.cancel", "#"+strconv.FormatInt(id, 10))
			writeJSON(w, 200, map[string]any{"id": id, "status": "cancelling"})
			return
		}
	}
	s.DB.ExecContext(r.Context(), `DELETE FROM crawl_jobs WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	s.audit(r.Context(), a.TenantID, a.Label, "crawl.delete", "#"+strconv.FormatInt(id, 10))
	w.WriteHeader(204)
}

// GET /api/crawl/wordlists
func (s *Server) crawlWordlists(w http.ResponseWriter, r *http.Request, a Auth) {
	out := []map[string]any{}
	for _, l := range crawl.Lists {
		out = append(out, map[string]any{"id": l.ID, "name": l.Name, "count": len(crawl.Words(l.ID)), "paths": crawl.Words(l.ID)})
	}
	writeJSON(w, 200, map[string]any{"lists": out, "spider": s.Browser != nil, "block_private": s.BlockPrivate})
}

// crawlStart onaylı işi arka planda çalıştırır.
func (s *Server) crawlStart(id, tenant int64) {
	ctx, cancel := context.WithCancel(context.Background())
	crawlActive.mu.Lock()
	crawlActive.m[id] = cancel
	crawlActive.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			crawlActive.mu.Lock()
			delete(crawlActive.m, id)
			crawlActive.mu.Unlock()
		}()
		s.crawlRun(ctx, id, tenant)
	}()
}

func (s *Server) crawlFinish(id int64, status, errMsg string, res *crawl.Result) {
	var raw any
	if res != nil {
		if b, err := json.Marshal(res); err == nil {
			raw = string(b)
		}
	}
	s.DB.Exec(`UPDATE crawl_jobs SET status = $2, error = $3, result = $4, progress = '', finished_at = now() WHERE id = $1`, id, status, errMsg, raw)
}

func (s *Server) crawlRun(ctx context.Context, id, tenant int64) {
	select { // aynı anda tek keşif
	case crawlSem <- struct{}{}:
		defer func() { <-crawlSem }()
	case <-ctx.Done():
		s.crawlFinish(id, "failed", "iptal edildi", nil)
		return
	}
	var u string
	var optsRaw []byte
	if err := s.DB.QueryRow(`UPDATE crawl_jobs SET status = 'running', started_at = now() WHERE id = $1 AND status = 'approved' RETURNING url, options`, id).Scan(&u, &optsRaw); err != nil {
		return
	}
	var o crawlOpts
	json.Unmarshal(optsRaw, &o)
	cfg := crawl.Config{Start: u, Spider: o.Spider, Wordlist: o.Wordlist, Extra: o.Extra, MaxPages: o.MaxPages, MaxDepth: o.MaxDepth,
		Delay: time.Duration(o.DelayMS) * time.Millisecond, BlockPrivate: s.BlockPrivate, Chrome: s.BrowserOptions, Timeout: 15 * time.Second}
	var last time.Time
	cfg.Progress = func(m string) {
		if time.Since(last) > 700*time.Millisecond {
			last = time.Now()
			s.DB.Exec(`UPDATE crawl_jobs SET progress = $2 WHERE id = $1`, id, m)
		}
	}
	if o.ScenarioID != 0 {
		au, cookies, steps, blocked, err := s.crawlAuth(ctx, tenant, o.ScenarioID)
		if err != nil || blocked {
			msg := "oturumlu tarama için senaryo kullanılamıyor"
			if err != nil {
				msg = err.Error()
			}
			s.crawlFinish(id, "failed", msg, nil)
			return
		}
		var vars map[string]string
		var vraw []byte
		if s.DB.QueryRow(`SELECT vars FROM scenarios WHERE id = $1`, o.ScenarioID).Scan(&vraw) == nil {
			json.Unmarshal(vraw, &vars)
		}
		cfg.Auth, cfg.Cookies, cfg.Steps, cfg.Vars = au, cookies, steps, vars
		cfg.OnLogin = func(failed bool) { // hesap kilitlenmesin: senaryoyla aynı sayaç
			if failed {
				s.DB.Exec(`UPDATE scenarios SET login_fails = login_fails + 1, auth_blocked = (login_fails + 1 >= $2) WHERE id = $1`, o.ScenarioID, maxLoginFails)
			} else {
				s.DB.Exec(`UPDATE scenarios SET login_fails = 0 WHERE id = $1`, o.ScenarioID)
			}
		}
	}
	rctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	if o.Spider {
		select {
		case browserSem <- struct{}{}:
			defer func() { <-browserSem }()
		case <-rctx.Done():
		}
	}
	res, err := crawl.Run(rctx, cfg)
	switch {
	case ctx.Err() != nil:
		s.crawlFinish(id, "failed", "iptal edildi", res)
	case err != nil && rctx.Err() != nil:
		s.crawlFinish(id, "failed", "zaman aşımı (12 dk)", res)
	case err != nil:
		s.crawlFinish(id, "failed", err.Error(), res)
	default:
		s.crawlFinish(id, "done", "", res)
	}
	s.audit(context.Background(), tenant, "sistem", "crawl.finish", "#"+strconv.FormatInt(id, 10))
}

// CrawlRecover sunucu açılışında çağrılır.
func (s *Server) CrawlRecover() { s.crawlRecover() }

// crawlRecover sunucu yeniden başlarken yarım kalan işleri kapatır.
func (s *Server) crawlRecover() {
	s.DB.Exec(`UPDATE crawl_jobs SET status = 'failed', error = 'sunucu yeniden başladı', progress = '', finished_at = now() WHERE status IN ('running','approved')`)
}
