package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"appmon/internal/browser"
)

const browserRunLimit = 80 * time.Second

var browserSem = make(chan struct{}, 2) // aynı anda en çok 2 tarayıcı oynatması

var errBrowserOff = errors.New("tarayıcı özelliği kapalı: APM_BROWSER_ENABLED=true ayarlayın ve imajda Chromium bulunsun")

// ---- Kayıt taslakları: gizli değerler istemciye gitmez, sunucuda tutulur ----

type draft struct {
	tenant  int64
	secrets map[string]string
	steps   []browser.Step
	expires time.Time
}

type draftStore struct {
	mu sync.Mutex
	m  map[string]draft
}

func (d *draftStore) put(tenant int64, steps []browser.Step, secrets map[string]string) string {
	b := make([]byte, 12)
	rand.Read(b)
	id := hex.EncodeToString(b)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.m == nil {
		d.m = map[string]draft{}
	}
	for k, v := range d.m {
		if time.Now().After(v.expires) {
			delete(d.m, k)
		}
	}
	d.m[id] = draft{tenant: tenant, secrets: secrets, steps: steps, expires: time.Now().Add(30 * time.Minute)}
	return id
}

func (d *draftStore) get(id string, tenant int64) (draft, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	x, ok := d.m[id]
	if !ok || x.tenant != tenant || time.Now().After(x.expires) {
		return draft{}, false
	}
	return x, true
}

// applyDraft taslağın gizli değişkenlerini (ve adımları boşsa adımları) yükün içine ekler.
func (s *Server) applyDraft(w http.ResponseWriter, p *scenarioPayload, a Auth) bool {
	if p.DraftID == "" {
		return true
	}
	d, ok := s.drafts.get(p.DraftID, a.TenantID)
	if !ok {
		fail(w, 400, "kayıt taslağı bulunamadı veya süresi doldu; kaydı yeniden yapın")
		return false
	}
	if p.Kind == "" {
		p.Kind = "browser"
	}
	if p.Vars == nil {
		p.Vars = map[string]string{}
	}
	for k, v := range d.secrets {
		p.Vars[k] = v
	}
	if len(p.BSteps) == 0 {
		p.BSteps = d.steps
	}
	return true
}

// ---- Oynatma ----

type browserExtra struct {
	res     browser.Result
	changes int
	prints  map[int][]string // diff için karşılaştırılan önceki parmak izleri
	flips   map[int]map[string]int
}

// volatileFlips: bu kadar kez eklenip kaybolan öğe dinamik içerik sayılır ve değişiklik alarmına girmez.
const volatileFlips = 2

func (b *browserExtra) persist(ctx context.Context, tx *sql.Tx, sc scenarioRow, runID int64) error {
	for i, st := range b.res.Steps {
		if len(st.Shot) > 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO scenario_shots (run_id, step_idx, jpeg) VALUES ($1,$2,$3)`, runID, i, st.Shot); err != nil {
				return err
			}
		}
		if st.OK && len(st.Items) > 0 {
			items, _ := json.Marshal(st.Items)
			fl, _ := json.Marshal(capFlips(b.flips[i]))
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO scenario_step_prints (scenario_id, step_idx, hash, items, flips, fp_version) VALUES ($1,$2,$3,$4,$5,$6)
				 ON CONFLICT (scenario_id, step_idx) DO UPDATE SET hash = EXCLUDED.hash, items = EXCLUDED.items, flips = EXCLUDED.flips, fp_version = EXCLUDED.fp_version, updated_at = now()`,
				sc.id, i, st.Hash, string(items), string(fl), browser.FPVersion); err != nil {
				return err
			}
		}
	}
	for host, h := range b.res.Hosts {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO scenario_hosts (scenario_id, host, requests, failed, errors) VALUES ($1,$2,$3,$4,$5)
			 ON CONFLICT (scenario_id, host) DO UPDATE SET requests = EXCLUDED.requests, failed = EXCLUDED.failed, errors = EXCLUDED.errors, last_seen = now()`,
			sc.id, host, h.Requests, h.Failed, h.Errors); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM scenario_hosts WHERE scenario_id = $1 AND last_seen < now() - interval '7 days'`, sc.id); err != nil {
		return err
	}
	if b.changes > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE scenarios SET last_changed_at = now() WHERE id = $1`, sc.id); err != nil {
			return err
		}
	}
	return nil
}

func browserError(steps []browser.Step, err error) string {
	msg := err.Error()
	if len(msg) > 400 {
		msg = msg[:400]
	}
	for _, st := range steps {
		if st.Action == "goto" && hasLocalhost(st.URL) && isConnFailure(msg) {
			return msg + dockerHint
		}
	}
	return msg
}

// replayBrowser tarayıcı akışını oynatır. Saklanan oturum geçerli sayılıp akış yine de başarısız olduysa
// (oturum arada düşmüş olabilir) bir kez temiz girişle yeniden dener.
func (s *Server) replayBrowser(ctx context.Context, steps []browser.Step, vars map[string]string, timeoutMS int, shots bool, auth *browser.Auth, cookies []browser.Cookie) (browser.Result, error) {
	o := s.browserOptions(timeoutMS, shots)
	if auth != nil && auth.Enabled {
		o.Auth, o.Cookies = auth, cookies
	}
	res, err := browser.Replay(ctx, steps, vars, o)
	if err != nil && res.Session == "reused" && ctx.Err() == nil {
		o.ForceLogin = true
		return browser.Replay(ctx, steps, vars, o)
	}
	return res, err
}

func (s *Server) browserOptions(timeoutMS int, shots bool) browser.Options {
	return browser.Options{Chrome: s.BrowserOptions, StepTimeout: time.Duration(timeoutMS) * time.Millisecond, Shots: shots}
}

// capFlips sayaç tablosunu sınırlı tutar (en çok 400 öğe).
func capFlips(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	if len(m) > 400 {
		out := map[string]int{}
		for k, v := range m {
			if v >= volatileFlips && len(out) < 400 {
				out[k] = v
			}
		}
		return out
	}
	return m
}

// toOuts adım sonuçlarını önceki çalıştırmayla (son iki kontrol) karşılaştırır. Daha önce en az volatileFlips kez
// gelip giden öğeler dinamik sayılır, farktan çıkarılır. Güncellenmiş sayaçlar ikinci değer olarak döner.
func toOuts(res browser.Result, prev map[int][]string, flips map[int]map[string]int) ([]stepOut, int, map[int]map[string]int) {
	outs := make([]stepOut, len(res.Steps))
	changes := 0
	newFlips := map[int]map[string]int{}
	for i, f := range flips {
		c := map[string]int{}
		for k, v := range f {
			c[k] = v
		}
		newFlips[i] = c
	}
	for i, r := range res.Steps {
		o := stepOut{Name: r.Name, OK: r.OK, MS: r.MS, Error: r.Error, Page: r.Page, Hash: r.Hash, Shot: len(r.Shot) > 0}
		if p, ok := prev[i]; ok && r.OK {
			added, removed := browser.Diff(p, r.Items)
			if newFlips[i] == nil {
				newFlips[i] = map[string]int{}
			}
			keep := func(list []string) []string {
				var out []string
				for _, it := range list {
					if newFlips[i][it] < volatileFlips {
						out = append(out, it)
					} else {
						o.Ignored++
					}
					newFlips[i][it]++
				}
				return out
			}
			o.Added, o.Removed = keep(added), keep(removed)
			changes += len(o.Added) + len(o.Removed)
		}
		outs[i] = o
	}
	return outs, changes, newFlips
}

func sumMS(outs []stepOut) time.Duration {
	var t float64
	for _, o := range outs {
		t += o.MS
	}
	return time.Duration(t * float64(time.Millisecond))
}

func (s *Server) executeBrowser(ctx context.Context, sc scenarioRow, maxDur time.Duration) ([]stepOut, bool, time.Duration, string) {
	fin := func(outs []stepOut, ok bool, dur time.Duration, msg string, bx *browserExtra) ([]stepOut, bool, time.Duration, string) {
		s.recordScenario(ctx, sc, outs, ok, dur, msg, bx)
		return outs, ok, dur, msg
	}
	if s.Browser == nil {
		return fin(nil, false, 0, errBrowserOff.Error(), nil)
	}
	select {
	case browserSem <- struct{}{}:
		defer func() { <-browserSem }()
	case <-ctx.Done():
		return fin(nil, false, 0, "tarayıcı oynatması için sıra beklenirken iptal edildi", nil)
	}
	total := time.Duration(len(sc.bsteps))*time.Duration(sc.timeoutMS)*time.Millisecond + 30*time.Second
	if maxDur > 0 && total > maxDur {
		total = maxDur
	}
	var auth *browser.Auth
	var cookies []browser.Cookie
	var authRaw, sessRaw []byte
	if err := s.DB.QueryRowContext(ctx, `SELECT auth, session FROM scenarios WHERE id = $1`, sc.id).Scan(&authRaw, &sessRaw); err == nil {
		var au browser.Auth
		if json.Unmarshal(authRaw, &au) == nil && au.Enabled {
			auth = &au
			total *= 2 // oturum düşmüşse bir kez yeniden giriş denenir
			json.Unmarshal(sessRaw, &cookies)
		}
	}
	if maxDur > 0 && total > maxDur {
		total = maxDur
	}
	runCtx, cancel := context.WithTimeout(ctx, total)
	defer cancel()

	prev := map[int][]string{}
	flips := map[int]map[string]int{}
	if rows, err := s.DB.QueryContext(ctx, `SELECT step_idx, items, flips, fp_version FROM scenario_step_prints WHERE scenario_id = $1`, sc.id); err == nil {
		for rows.Next() {
			var idx, ver int
			var raw, fraw []byte
			var items []string
			if rows.Scan(&idx, &raw, &fraw, &ver) == nil && json.Unmarshal(raw, &items) == nil {
				if ver != browser.FPVersion {
					continue // eski biçimli parmak izi: karşılaştırma yapma, bu çalıştırma yeni taban çizgisi olsun
				}
				prev[idx] = items
				var fm map[string]int
				if json.Unmarshal(fraw, &fm) == nil {
					flips[idx] = fm
				}
			}
		}
		rows.Close()
	}
	res, err := s.replayBrowser(runCtx, sc.bsteps, sc.vars, sc.timeoutMS, true, auth, cookies)
	if err == nil && auth != nil && len(res.Cookies) > 0 {
		if b, e := json.Marshal(res.Cookies); e == nil {
			s.DB.ExecContext(ctx, `UPDATE scenarios SET session = $1, session_at = now() WHERE id = $2`, string(b), sc.id)
		}
	}
	outs, changes, newFlips := toOuts(res, prev, flips)
	dur := sumMS(outs)
	errMsg := ""
	if err != nil {
		errMsg = browserError(sc.bsteps, err)
	}
	return fin(outs, err == nil, dur, errMsg, &browserExtra{res: res, changes: changes, prints: prev, flips: newFlips})
}

// testBrowser tarayıcı senaryosunu kaydetmeden dener.
func (s *Server) testBrowser(w http.ResponseWriter, r *http.Request, p scenarioPayload) {
	if s.Browser == nil {
		fail(w, http.StatusServiceUnavailable, errBrowserOff.Error())
		return
	}
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(browserRunLimit + 15*time.Second))
	select {
	case browserSem <- struct{}{}:
		defer func() { <-browserSem }()
	case <-r.Context().Done():
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), browserRunLimit)
	defer cancel()
	res, err := s.replayBrowser(ctx, p.BSteps, p.Vars, p.TimeoutMS, false, p.Auth, nil)
	outs, _, _ := toOuts(res, nil, nil)
	msg := ""
	if err != nil {
		msg = browserError(p.BSteps, err)
	}
	writeJSON(w, 200, resultJSON(outs, err == nil, sumMS(outs), msg))
}

// ---- Kayıt API'si ----

func (s *Server) recorderOr503(w http.ResponseWriter) bool {
	if s.Browser == nil {
		fail(w, http.StatusServiceUnavailable, errBrowserOff.Error())
		return false
	}
	return true
}

func (s *Server) recorderSession(w http.ResponseWriter, r *http.Request, a Auth) (*browser.Session, bool) {
	if !s.recorderOr503(w) {
		return nil, false
	}
	sess, ok := s.Browser.Get(r.PathValue("id"), a.TenantID)
	if !ok {
		fail(w, http.StatusNotFound, "kayıt oturumu bulunamadı veya süresi doldu")
		return nil, false
	}
	return sess, true
}

func (s *Server) recorderStart(w http.ResponseWriter, r *http.Request, a Auth) {
	if !s.recorderOr503(w) {
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &req) {
		return
	}
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(60 * time.Second))
	sess, err := s.Browser.Start(r.Context(), a.TenantID, req.URL)
	if err != nil {
		code := 400
		if errors.Is(err, browser.ErrBusy) {
			code = http.StatusTooManyRequests
		}
		fail(w, code, err.Error())
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "recorder.start", "")
	writeJSON(w, 201, map[string]any{"id": sess.ID, "width": browser.ViewW, "height": browser.ViewH})
}

func (s *Server) recorderState(w http.ResponseWriter, r *http.Request, a Auth) {
	if sess, ok := s.recorderSession(w, r, a); ok {
		writeJSON(w, 200, sess.State())
	}
}

func (s *Server) recorderFrame(w http.ResponseWriter, r *http.Request, a Auth) {
	sess, ok := s.recorderSession(w, r, a)
	if !ok {
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	frame, seq, closed := sess.Frame(r.Context(), after, 3*time.Second)
	w.Header().Set("X-Frame-Seq", strconv.FormatInt(seq, 10))
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case closed:
		w.WriteHeader(http.StatusGone)
	case frame == nil:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(frame)
	}
}

func (s *Server) recorderInput(w http.ResponseWriter, r *http.Request, a Auth) {
	sess, ok := s.recorderSession(w, r, a)
	if !ok {
		return
	}
	var in browser.Input
	if !decode(w, r, &in) {
		return
	}
	if err := sess.Input(r.Context(), in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, sess.State())
}

func (s *Server) recorderStop(w http.ResponseWriter, r *http.Request, a Auth) {
	if !s.recorderOr503(w) {
		return
	}
	steps, secrets, err := s.Browser.Stop(r.PathValue("id"), a.TenantID)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}
	names := make([]string, 0, len(secrets))
	for k := range secrets {
		names = append(names, k)
	}
	sort.Strings(names)
	id := s.drafts.put(a.TenantID, steps, secrets)
	s.audit(r.Context(), a.TenantID, a.Label, "recorder.stop", strconv.Itoa(len(steps))+" adım")
	writeJSON(w, 200, map[string]any{"draft_id": id, "steps": steps, "secret_names": names})
}

// GET /api/scenarios/{id}/runs/{run}/shots/{n}
func (s *Server) scenarioShot(w http.ResponseWriter, r *http.Request, a Auth) {
	sid, ok1 := pathID(r)
	run, e2 := strconv.ParseInt(r.PathValue("run"), 10, 64)
	n, e3 := strconv.Atoi(r.PathValue("n"))
	if !ok1 || e2 != nil || e3 != nil {
		fail(w, 400, "geçersiz parametre")
		return
	}
	var jpeg []byte
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT sh.jpeg FROM scenario_shots sh JOIN scenario_runs r ON r.id = sh.run_id
		 WHERE r.id = $1 AND r.scenario_id = $2 AND r.tenant_id = $3 AND sh.step_idx = $4`, run, sid, a.TenantID, n).Scan(&jpeg)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "ekran görüntüsü yok (yalnızca son 10 çalıştırma saklanır)")
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(jpeg)
}
