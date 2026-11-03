package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"

	"appmon/internal/browser"
	"appmon/internal/scenario"
)

const manualRunLimit = 25 * time.Second // HTTP sunucusunun yazma zaman aşımının (30 sn) altında

type scenarioPayload struct {
	AssetID     int64             `json:"asset_id"`
	AssetName   string            `json:"asset_name"`
	AssetKind   string            `json:"asset_kind"`
	Language    string            `json:"language"`
	Name        string            `json:"name"`
	IntervalSec int               `json:"interval_sec"`
	TimeoutMS   int               `json:"timeout_ms"`
	VerifyTLS   *bool             `json:"verify_tls"`
	Vars        map[string]string `json:"vars"`
	Steps       []scenario.Step   `json:"steps"`
	// Tarayıcı senaryoları
	Kind    string         `json:"kind"`
	BSteps  []browser.Step `json:"browser_steps"`
	Auth    *browser.Auth  `json:"auth"`
	DraftID string         `json:"draft_id"`
}

func (p *scenarioPayload) isBrowser() bool { return p.Kind == "browser" }

// prepare varsayılanları uygular ve senaryoyu doğrular. requireName false ise (deneme çalıştırması) ad aranmaz.
func (p *scenarioPayload) prepare(requireName bool) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Kind == "" {
		p.Kind = "http"
	}
	if p.Kind != "http" && p.Kind != "browser" {
		return errors.New("kind http veya browser olmalı")
	}
	if requireName && (p.Name == "" || len(p.Name) > 200) {
		return errors.New("senaryo adı zorunlu ve en fazla 200 karakter olmalı")
	}
	if p.IntervalSec == 0 {
		p.IntervalSec = 300
	}
	if p.TimeoutMS == 0 {
		p.TimeoutMS = 10000
	}
	if p.IntervalSec < 30 || p.TimeoutMS < 500 || p.TimeoutMS > 60000 {
		return errors.New("interval_sec en az 30, timeout_ms 500 ile 60000 arasında olmalı")
	}
	if p.Vars == nil {
		p.Vars = map[string]string{}
	}
	if p.isBrowser() {
		if p.IntervalSec < 60 {
			return errors.New("tarayıcı senaryolarında interval_sec en az 60 olmalı")
		}
		if err := browser.Validate(p.BSteps, p.Vars); err != nil {
			return err
		}
		return browser.ValidateAuth(p.Auth, p.BSteps)
	}
	scenario.Normalize(p.Steps)
	return scenario.Validate(p.Steps, p.Vars)
}

func (p *scenarioPayload) verifyTLS() bool { return p.VerifyTLS == nil || *p.VerifyTLS }

type scenarioRow struct {
	id, tenant, asset int64
	name              string
	timeoutMS         int
	verifyTLS         bool
	vars              map[string]string
	steps             []scenario.Step
	kind              string
	bsteps            []browser.Step
}

// decodeSteps kaydedilmiş adımları senaryo türüne göre çözer.
func (sc *scenarioRow) decodeSteps(raw []byte) error {
	if sc.kind == "browser" {
		return json.Unmarshal(raw, &sc.bsteps)
	}
	return json.Unmarshal(raw, &sc.steps)
}

func (s *Server) loadScenario(ctx context.Context, tenant, id int64) (scenarioRow, error) {
	sc := scenarioRow{id: id, tenant: tenant}
	var vars, steps []byte
	err := s.DB.QueryRowContext(ctx,
		`SELECT asset_id, name, timeout_ms, verify_tls, vars, steps, kind FROM scenarios WHERE id = $1 AND tenant_id = $2`,
		id, tenant).Scan(&sc.asset, &sc.name, &sc.timeoutMS, &sc.verifyTLS, &vars, &steps, &sc.kind)
	if err != nil {
		return sc, err
	}
	if err := json.Unmarshal(vars, &sc.vars); err != nil {
		return sc, err
	}
	return sc, sc.decodeSteps(steps)
}

// scenarioError ilk başarısız adımın hatasını döndürür; localhost adreslerinde Docker ipucu ekler.
func scenarioError(steps []scenario.Step, err error) string {
	msg := err.Error()
	if len(msg) > 400 {
		msg = msg[:400]
	}
	for _, st := range steps {
		if hasLocalhost(st.URL) && isConnFailure(msg) {
			return msg + dockerHint
		}
	}
	return msg
}

// stepOut HTTP ve tarayıcı adım sonuçları için ortak çıktı biçimidir.
type stepOut struct {
	Name    string   `json:"name"`
	OK      bool     `json:"ok"`
	Status  int      `json:"status,omitempty"`
	MS      float64  `json:"ms"`
	Error   string   `json:"error,omitempty"`
	Page    string   `json:"page,omitempty"`
	Hash    string   `json:"hash,omitempty"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Ignored int      `json:"ignored,omitempty"` // dinamik sayılıp yok sayılan öğe sayısı
	Shot    bool     `json:"shot,omitempty"`
}

func httpOuts(rs []scenario.Result) []stepOut {
	out := make([]stepOut, len(rs))
	for i, r := range rs {
		out[i] = stepOut{Name: r.Name, OK: r.OK, Status: r.Status, MS: r.MS, Error: r.Error}
	}
	return out
}

// executeScenario senaryoyu çalıştırır ve sonucu (çalıştırma geçmişi, metrikler, asset durumu) kaydeder.
func (s *Server) executeScenario(ctx context.Context, sc scenarioRow, maxDur time.Duration) ([]stepOut, bool, time.Duration, string) {
	if sc.kind == "browser" {
		return s.executeBrowser(ctx, sc, maxDur)
	}
	total := time.Duration(len(sc.steps))*time.Duration(sc.timeoutMS)*time.Millisecond + 5*time.Second
	if maxDur > 0 && total > maxDur {
		total = maxDur
	}
	runCtx, cancel := context.WithTimeout(ctx, total)
	defer cancel()
	results, dur, err := scenario.Run(runCtx, sc.steps, scenario.Options{
		Timeout: time.Duration(sc.timeoutMS) * time.Millisecond, BlockPrivate: s.BlockPrivate,
		SkipTLSVerify: !sc.verifyTLS, Vars: sc.vars,
	})
	ok := err == nil
	errMsg := ""
	if err != nil {
		errMsg = scenarioError(sc.steps, err)
	}
	outs := httpOuts(results)
	s.recordScenario(ctx, sc, outs, ok, dur, errMsg, nil)
	return outs, ok, dur, errMsg
}

func (s *Server) recordScenario(ctx context.Context, sc scenarioRow, results []stepOut, ok bool, dur time.Duration, errMsg string, bx *browserExtra) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	now := time.Now()
	ms := float64(dur.Microseconds()) / 1000
	up, status := 0.0, "down"
	if ok {
		up, status = 1, "up"
	}
	stepsJSON, _ := json.Marshal(results)
	var runID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO scenario_runs (tenant_id, scenario_id, ok, duration_ms, error, steps) VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
		sc.tenant, sc.id, ok, ms, errMsg, string(stepsJSON)).Scan(&runID); err != nil {
		return
	}
	pts := []point{{"scenario.up", up, now}}
	if ok {
		pts = append(pts, point{"scenario.duration_ms", ms, now})
	}
	if bx != nil {
		if err := bx.persist(ctx, tx, sc, runID); err != nil {
			return
		}
		pts = append(pts, point{"scenario.changes", float64(bx.changes), now})
	}
	if err := insertMetrics(ctx, tx, sc.tenant, sc.asset, pts); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx, `UPDATE assets SET status = $2, last_seen = now() WHERE id = $1`, sc.asset, status); err != nil {
		return
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE scenarios SET last_run = now(), last_ok = $2, last_error = $3, last_duration_ms = $4 WHERE id = $1`,
		sc.id, ok, errMsg, ms); err != nil {
		return
	}
	tx.Commit()
}

// ---- API ----

func stepSummary(steps []scenario.Step) []map[string]any {
	out := make([]map[string]any, 0, len(steps))
	for _, st := range steps {
		names := make([]string, 0, len(st.Headers))
		for k := range st.Headers {
			names = append(names, k)
		}
		sort.Strings(names)
		ex := make([]string, 0, len(st.Extract))
		for _, e := range st.Extract {
			ex = append(ex, fmt.Sprintf("%s = %s:%s", e.Var, e.From, e.Path))
		}
		out = append(out, map[string]any{"name": st.Name, "method": st.Method, "url": st.URL, "expect_status": st.ExpectStatus,
			"expect_text": st.ExpectText, "max_ms": st.MaxMS, "header_names": names, "has_body": st.Body != "", "extract": ex})
	}
	return out
}

func (s *Server) listScenarios(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT sc.id, sc.asset_id, a.name, sc.name, sc.interval_sec, sc.timeout_ms, sc.enabled, sc.verify_tls, sc.vars, sc.steps,
		        sc.last_run, sc.last_ok, sc.last_error, sc.last_duration_ms, sc.kind, sc.last_changed_at, sc.auth, sc.session_at
		 FROM scenarios sc JOIN assets a ON a.id = sc.asset_id WHERE sc.tenant_id = $1 ORDER BY a.name, sc.id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id, assetID int64
		var asset, name, lastErr string
		var interval, timeout int
		var enabled, verify bool
		var varsRaw, stepsRaw []byte
		var lastRun sql.NullTime
		var lastOK sql.NullBool
		var lastDur sql.NullFloat64
		var kind string
		var changedAt, sessionAt sql.NullTime
		var authRaw []byte
		if rows.Scan(&id, &assetID, &asset, &name, &interval, &timeout, &enabled, &verify, &varsRaw, &stepsRaw, &lastRun, &lastOK, &lastErr, &lastDur, &kind, &changedAt, &authRaw, &sessionAt) != nil {
			continue
		}
		var vars map[string]string
		var steps []scenario.Step
		json.Unmarshal(varsRaw, &vars)
		var summaries []map[string]any
		if kind == "browser" {
			var bs []browser.Step
			json.Unmarshal(stepsRaw, &bs)
			for _, st := range bs {
				summaries = append(summaries, map[string]any{"name": st.Name, "method": strings.ToUpper(st.Action), "summary": st.Summary()})
			}
		} else {
			json.Unmarshal(stepsRaw, &steps)
			summaries = stepSummary(steps)
		}
		varNames := make([]string, 0, len(vars))
		for k := range vars {
			varNames = append(varNames, k)
		}
		sort.Strings(varNames)
		it := map[string]any{"id": id, "asset_id": assetID, "asset": asset, "name": name, "interval_sec": interval,
			"timeout_ms": timeout, "enabled": enabled, "verify_tls": verify, "var_names": varNames,
			"steps": summaries, "last_error": lastErr, "kind": kind}
		if changedAt.Valid {
			it["last_changed_at"] = changedAt.Time
		}
		var au browser.Auth
		if json.Unmarshal(authRaw, &au) == nil && au.Enabled { // parola ve selector ayrıntıları dışarı verilmez
			sum := map[string]any{"mode": au.Mode, "username": au.Username, "check_url": au.CheckURL}
			if sessionAt.Valid {
				sum["session_at"] = sessionAt.Time
			}
			it["auth"] = sum
		}
		if lastRun.Valid {
			it["last_run"] = lastRun.Time
		}
		if lastOK.Valid {
			it["last_ok"] = lastOK.Bool
		}
		if lastDur.Valid {
			it["last_duration_ms"] = lastDur.Float64
		}
		list = append(list, it)
	}
	writeJSON(w, 200, list)
}

func (s *Server) createScenario(w http.ResponseWriter, r *http.Request, a Auth) {
	var p scenarioPayload
	if !decode(w, r, &p) {
		return
	}
	if !s.applyDraft(w, &p, a) {
		return
	}
	if err := p.prepare(true); err != nil {
		fail(w, 400, err.Error())
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()

	assetID := p.AssetID
	if assetID == 0 {
		name := strings.TrimSpace(p.AssetName)
		if name == "" || len(name) > 200 {
			fail(w, 400, "asset_id veya asset_name gerekli")
			return
		}
		kind := p.AssetKind
		if kind == "" {
			kind = "service"
			if p.isBrowser() {
				kind = "website"
			}
		}
		if !validKind(kind) {
			fail(w, 400, "asset_kind geçersiz")
			return
		}
		assetID, _, err = s.ensureAsset(r.Context(), tx, a.TenantID, name, kind, normalizeLanguage(p.Language), "agentless")
		if err != nil {
			if code, ok := licenseStatus(err); ok {
				fail(w, code, err.Error())
				return
			}
			fail(w, 500, "veritabanı hatası")
			return
		}
		if p.AssetKind != "" {
			tx.ExecContext(r.Context(), `UPDATE assets SET kind = $1 WHERE id = $2 AND tenant_id = $3`, p.AssetKind, assetID, a.TenantID)
		}
	} else {
		var one int
		if err := tx.QueryRowContext(r.Context(), `SELECT 1 FROM assets WHERE id = $1 AND tenant_id = $2`, assetID, a.TenantID).Scan(&one); err != nil {
			fail(w, 404, "asset bulunamadı")
			return
		}
	}
	varsJSON, _ := json.Marshal(p.Vars)
	var stepsJSON []byte
	if p.isBrowser() {
		stepsJSON, _ = json.Marshal(p.BSteps)
	} else {
		stepsJSON, _ = json.Marshal(p.Steps)
	}
	authJSON := []byte("{}")
	if p.isBrowser() && p.Auth != nil && p.Auth.Enabled {
		authJSON, _ = json.Marshal(p.Auth)
	}
	var id int64
	err = tx.QueryRowContext(r.Context(),
		`INSERT INTO scenarios (tenant_id, asset_id, name, interval_sec, timeout_ms, verify_tls, vars, steps, kind, auth)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		a.TenantID, assetID, p.Name, p.IntervalSec, p.TimeoutMS, p.verifyTLS(), string(varsJSON), string(stepsJSON), p.Kind, string(authJSON)).Scan(&id)
	if err != nil {
		if isUniqueViolation(err) {
			fail(w, http.StatusConflict, "bu isimde bir senaryo zaten var")
			return
		}
		fail(w, 500, "veritabanı hatası")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "scenario.create", p.Name)
	writeJSON(w, 201, map[string]any{"id": id, "asset_id": assetID})
}

func (s *Server) deleteScenario(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var name string
	err := s.DB.QueryRowContext(r.Context(), `DELETE FROM scenarios WHERE id = $1 AND tenant_id = $2 RETURNING name`, id, a.TenantID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "senaryo bulunamadı")
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "scenario.delete", name)
	w.WriteHeader(http.StatusNoContent)
}

func resultJSON(results []stepOut, ok bool, dur time.Duration, errMsg string) map[string]any {
	return map[string]any{"ok": ok, "duration_ms": float64(dur.Microseconds()) / 1000, "error": errMsg, "steps": results}
}

// POST /api/scenarios/{id}/run - kayıtlı senaryoyu hemen çalıştırır ve geçmişe yazar.
func (s *Server) runScenarioNow(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	sc, err := s.loadScenario(r.Context(), a.TenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "senaryo bulunamadı")
		return
	}
	if err != nil {
		fail(w, 500, "senaryo okunamadı")
		return
	}
	limit := manualRunLimit
	if sc.kind == "browser" {
		limit = browserRunLimit
		http.NewResponseController(w).SetWriteDeadline(time.Now().Add(limit + 15*time.Second))
	}
	results, good, dur, errMsg := s.executeScenario(r.Context(), sc, limit)
	writeJSON(w, 200, resultJSON(results, good, dur, errMsg))
}

// POST /api/scenarios/test - kaydetmeden dener (senaryo yazarken). Hiçbir şey saklanmaz.
func (s *Server) testScenario(w http.ResponseWriter, r *http.Request, a Auth) {
	var p scenarioPayload
	if !decode(w, r, &p) {
		return
	}
	if !s.applyDraft(w, &p, a) {
		return
	}
	if err := p.prepare(false); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if p.isBrowser() {
		s.testBrowser(w, r, p)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), manualRunLimit)
	defer cancel()
	results, dur, err := scenario.Run(ctx, p.Steps, scenario.Options{
		Timeout: time.Duration(p.TimeoutMS) * time.Millisecond, BlockPrivate: s.BlockPrivate,
		SkipTLSVerify: !p.verifyTLS(), Vars: p.Vars,
	})
	errMsg := ""
	if err != nil {
		errMsg = scenarioError(p.Steps, err)
	}
	writeJSON(w, 200, resultJSON(httpOuts(results), err == nil, dur, errMsg))
}

// GET /api/scenarios/{id}/runs - son 20 çalıştırma.
func (s *Server) listScenarioRuns(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, at, ok, duration_ms, error, steps FROM scenario_runs WHERE scenario_id = $1 AND tenant_id = $2 ORDER BY id DESC LIMIT 20`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var runID int64
		var at time.Time
		var good bool
		var dur float64
		var errMsg string
		var raw []byte
		if rows.Scan(&runID, &at, &good, &dur, &errMsg, &raw) != nil {
			continue
		}
		list = append(list, map[string]any{"id": runID, "at": at, "ok": good, "duration_ms": dur, "error": errMsg, "steps": json.RawMessage(raw)})
	}
	writeJSON(w, 200, list)
}

func isUniqueViolation(err error) bool {
	var pe *pq.Error
	return errors.As(err, &pe) && pe.Code == "23505"
}
