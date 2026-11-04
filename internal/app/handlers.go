package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"appmon/internal/probe"
)

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func countBy(ctx context.Context, db *sql.DB, query string, tenant int64) map[string]int {
	out := map[string]int{}
	rows, err := db.QueryContext(ctx, query, tenant)
	if err != nil {
		log.Printf("sayım sorgusu: %v", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			out[k] = n
		}
	}
	return out
}

// ---- Genel bakış ----

func (s *Server) overview(w http.ResponseWriter, r *http.Request, a Auth) {
	ctx, t := r.Context(), a.TenantID
	var limit int
	var validUntil sql.NullTime
	var mode string
	if err := s.DB.QueryRowContext(ctx, `SELECT asset_limit, valid_until, mode FROM licenses WHERE tenant_id = $1`, t).Scan(&limit, &validUntil, &mode); err != nil {
		fail(w, 500, "lisans okunamadı")
		return
	}
	byStatus := countBy(ctx, s.DB, `SELECT status, count(*) FROM assets WHERE tenant_id = $1 GROUP BY status`, t)
	used := 0
	for _, n := range byStatus {
		used += n
	}
	var openAlerts, critical int
	s.DB.QueryRowContext(ctx,
		`SELECT count(*), count(*) FILTER (WHERE r.severity = 'critical')
		 FROM alerts al JOIN alert_rules r ON r.id = al.rule_id WHERE al.tenant_id = $1 AND al.state = 'open'`, t).Scan(&openAlerts, &critical)
	lic := map[string]any{"mode": mode, "asset_limit": limit, "assets_used": used}
	if validUntil.Valid {
		lic["valid_until"] = validUntil.Time
	}
	writeJSON(w, 200, map[string]any{
		"license":       lic,
		"by_status":     byStatus,
		"by_language":   countBy(ctx, s.DB, `SELECT language, count(*) FROM assets WHERE tenant_id = $1 GROUP BY language`, t),
		"by_collection": countBy(ctx, s.DB, `SELECT collection, count(*) FROM assets WHERE tenant_id = $1 GROUP BY collection`, t),
		"open_alerts":   openAlerts,
		"critical":      critical,
	})
}

// ---- Asset'ler ----

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request, a Auth) {
	ctx := r.Context()
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, kind, language, collection, status, last_seen, labels FROM assets WHERE tenant_id = $1 ORDER BY name`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	type item map[string]any
	list := []item{}
	index := map[int64]item{}
	for rows.Next() {
		var id int64
		var name, kind, lang, coll, status string
		var seen sql.NullTime
		var labels []byte
		if err := rows.Scan(&id, &name, &kind, &lang, &coll, &status, &seen, &labels); err != nil {
			continue
		}
		it := item{"id": id, "name": name, "kind": kind, "language": lang, "collection": coll, "status": status,
			"labels": json.RawMessage(labels), "latest": map[string]float64{}}
		if seen.Valid {
			it["last_seen"] = seen.Time
		}
		list = append(list, it)
		index[id] = it
	}
	// Her asset için son 10 dakikadaki en güncel temel metrikler.
	mrows, err := s.DB.QueryContext(ctx,
		`SELECT DISTINCT ON (asset_id, name) asset_id, name, value FROM metrics
		 WHERE tenant_id = $1 AND time > now() - interval '10 minutes'
		   AND name IN ('host.cpu_pct','host.mem_pct','host.disk_pct','probe.latency_ms')
		 ORDER BY asset_id, name, time DESC`, a.TenantID)
	if err == nil {
		defer mrows.Close()
		for mrows.Next() {
			var id int64
			var name string
			var v float64
			if mrows.Scan(&id, &name, &v) == nil {
				if it, ok := index[id]; ok {
					it["latest"].(map[string]float64)[name] = v
				}
			}
		}
	}
	writeJSON(w, 200, list)
}

func (s *Server) createAsset(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct{ Name, Kind, Language string }
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > 200 {
		fail(w, 400, "name zorunlu ve en fazla 200 karakter olmalı")
		return
	}
	if req.Kind == "" {
		req.Kind = "service"
	}
	if !validKind(req.Kind) {
		fail(w, 400, "kind geçersiz")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	id, created, err := s.ensureAsset(r.Context(), tx, a.TenantID, req.Name, req.Kind, normalizeLanguage(req.Language), "agentless")
	if err != nil {
		if code, ok := licenseStatus(err); ok {
			fail(w, code, err.Error())
			return
		}
		fail(w, 500, "veritabanı hatası")
		return
	}
	if !created {
		fail(w, http.StatusConflict, "bu isimde bir asset zaten var")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "asset.create", req.Name)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var name string
	err := s.DB.QueryRowContext(r.Context(), `DELETE FROM assets WHERE id = $1 AND tenant_id = $2 RETURNING name`, id, a.TenantID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "asset bulunamadı")
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "asset.delete", name)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/assets/{id}/metrics?minutes=60 -> {"metric.name": [[unix_ms, value], ...]}
func (s *Server) assetMetrics(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	minutes, _ := strconv.Atoi(r.URL.Query().Get("minutes"))
	if minutes < 1 || minutes > 1440 {
		minutes = 60
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT name, (extract(epoch FROM date_trunc('minute', time)) * 1000)::bigint AS ts, avg(value)
		 FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND time > now() - make_interval(mins => $3)
		 GROUP BY name, ts ORDER BY name, ts`, a.TenantID, id, minutes)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := map[string][][2]float64{}
	for rows.Next() {
		var name string
		var ts int64
		var v float64
		if rows.Scan(&name, &ts, &v) == nil {
			out[name] = append(out[name], [2]float64{float64(ts), v})
		}
	}
	writeJSON(w, 200, out)
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// ---- Probe'lar (agentless) ----

func (s *Server) listProbes(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT p.id, p.asset_id, a.name, p.type, p.target, p.interval_sec, p.timeout_ms, p.enabled, p.last_run, p.last_error,
		        p.http_method, p.headers, p.expect_status, p.expect_text, p.verify_tls, p.follow_redirects, p.options
		 FROM probes p JOIN assets a ON a.id = p.asset_id WHERE p.tenant_id = $1 ORDER BY a.name, p.id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id, assetID int64
		var asset, typ, target, lastErr, method, expStatus, expText string
		var interval, timeout int
		var enabled, verify, follow bool
		var last sql.NullTime
		var headersRaw, optsRaw []byte
		if rows.Scan(&id, &assetID, &asset, &typ, &target, &interval, &timeout, &enabled, &last, &lastErr,
			&method, &headersRaw, &expStatus, &expText, &verify, &follow, &optsRaw) != nil {
			continue
		}
		it := map[string]any{"id": id, "asset_id": assetID, "asset": asset, "type": typ, "target": maskTarget(typ, target),
			"interval_sec": interval, "timeout_ms": timeout, "enabled": enabled, "last_error": lastErr}
		if typ == "http" {
			var hdr map[string]string
			json.Unmarshal(headersRaw, &hdr)
			names := make([]string, 0, len(hdr))
			for k := range hdr {
				names = append(names, k)
			}
			sort.Strings(names)
			it["http_method"], it["expect_status"], it["expect_text"] = method, expStatus, expText
			it["verify_tls"], it["follow_redirects"], it["header_names"] = verify, follow, names
		}
		if typ == "tls" {
			it["verify_tls"] = verify
		}
		var po probe.Options
		if json.Unmarshal(optsRaw, &po) == nil && typ != "http" && typ != "postgres" {
			hasComm := po.Community != ""
			po.Community = "" // topluluk adı gizlidir, listede gösterilmez
			it["options"], it["has_community"] = po, hasComm
		}
		if last.Valid {
			it["last_run"] = last.Time
		}
		list = append(list, it)
	}
	writeJSON(w, 200, list)
}

// maskTarget PostgreSQL bağlantı dizelerindeki parolayı arayüzde göstermez.
func maskTarget(typ, target string) string {
	if typ != "postgres" {
		return target
	}
	if u, err := url.Parse(target); err == nil && u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "gizli")
			return u.String()
		}
	}
	return target
}

func validateTarget(typ, target string) error {
	target = strings.TrimSpace(target)
	if target == "" || len(target) > 1000 {
		return errors.New("target zorunlu")
	}
	switch typ {
	case "http":
		u, err := url.Parse(target)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("http hedefi http:// veya https:// ile başlayan geçerli bir URL olmalı")
		}
	case "tcp":
		if _, _, err := splitHostPort(target); err != nil {
			return errors.New("tcp hedefi host:port biçiminde olmalı")
		}
	case "postgres":
		if !strings.HasPrefix(target, "postgres://") && !strings.HasPrefix(target, "postgresql://") {
			return errors.New("postgres hedefi postgres://kullanici:parola@host:5432/veritabani biçiminde olmalı")
		}
	case "icmp", "dns":
		if !probe.ValidHost(target) {
			return errors.New(typ + " hedefi geçerli bir ad veya IP olmalı (şema/port olmadan)")
		}
	case "snmp", "tls", "ntp":
		host := target
		if h, _, err := splitHostPort(target); err == nil {
			host = h
		}
		if !probe.ValidHost(host) {
			return errors.New(typ + " hedefi host veya host:port biçiminde olmalı")
		}
	case "udp":
		if h, _, err := splitHostPort(target); err != nil || !probe.ValidHost(h) {
			return errors.New("udp hedefi host:port biçiminde olmalı")
		}
	default:
		return errors.New("type " + strings.Join(probe.Types, ", ") + " olmalı")
	}
	return nil
}

// validateOptions türe özgü seçenekleri doğrular ve kullanılmayanları temizler.
func validateOptions(typ string, o *probe.Options) error {
	keep := probe.Options{}
	switch typ {
	case "icmp":
		if o.Count < 0 || o.Count > 10 {
			return errors.New("paket sayısı 1 ile 10 arasında olmalı")
		}
		keep.Count = o.Count
	case "snmp":
		if o.OID != "" && !probe.ValidOID(o.OID) {
			return errors.New("OID 1.3.6.1.2.1.1.3.0 gibi noktalı sayılar olmalı")
		}
		if o.Version != "" && o.Version != "1" && o.Version != "2c" {
			return errors.New("snmp_version 1 veya 2c olmalı")
		}
		if len(o.Community) > 100 {
			return errors.New("topluluk adı en fazla 100 karakter olabilir")
		}
		keep = probe.Options{Community: o.Community, OID: o.OID, Version: o.Version, Expect: o.Expect}
	case "dns":
		switch strings.ToUpper(o.Record) {
		case "", "A", "AAAA", "MX", "TXT", "CNAME", "NS":
		default:
			return errors.New("kayıt türü A, AAAA, MX, TXT, CNAME veya NS olmalı")
		}
		if o.Server != "" {
			h := o.Server
			if hh, _, err := splitHostPort(o.Server); err == nil {
				h = hh
			}
			if !probe.ValidHost(strings.Trim(h, "[]")) {
				return errors.New("DNS sunucusu host veya host:port olmalı")
			}
		}
		keep = probe.Options{Server: o.Server, Record: strings.ToUpper(o.Record), Expect: o.Expect}
	case "tcp", "udp":
		keep = probe.Options{Send: o.Send, Expect: o.Expect}
	}
	if len(keep.Send) > 1024 || len(keep.Expect) > 200 {
		return errors.New("gönderilecek metin en fazla 1024, beklenen metin en fazla 200 karakter olabilir")
	}
	*o = keep
	return nil
}

func splitHostPort(s string) (string, string, error) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("geçersiz")
	}
	if p, err := strconv.Atoi(s[i+1:]); err != nil || p < 1 || p > 65535 {
		return "", "", fmt.Errorf("geçersiz port")
	}
	return s[:i], s[i+1:], nil
}

// POST /api/probes: asset_id verilirse mevcut asset'e, asset_name verilirse (yoksa oluşturularak, lisansa sayılır) bağlanır.
func (s *Server) createProbe(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		AssetID     int64  `json:"asset_id"`
		AssetName   string `json:"asset_name"`
		AssetKind   string `json:"asset_kind"`
		Language    string `json:"language"`
		Type        string `json:"type"`
		Target      string `json:"target"`
		IntervalSec int    `json:"interval_sec"`
		TimeoutMS   int    `json:"timeout_ms"`
		// Yalnızca type=http için
		HTTPMethod      string            `json:"http_method"`
		Headers         map[string]string `json:"headers"`
		Body            string            `json:"body"`
		ExpectStatus    string            `json:"expect_status"`
		ExpectText      string            `json:"expect_text"`
		VerifyTLS       *bool             `json:"verify_tls"`
		FollowRedirects *bool             `json:"follow_redirects"`
		Options         probe.Options     `json:"options"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := validateTarget(req.Type, req.Target); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := validateOptions(req.Type, &req.Options); err != nil {
		fail(w, 400, err.Error())
		return
	}
	optionsJSON, _ := json.Marshal(req.Options)
	if req.Type == "http" {
		req.HTTPMethod = strings.ToUpper(strings.TrimSpace(req.HTTPMethod))
		if req.HTTPMethod == "" {
			req.HTTPMethod = "GET"
		}
		req.ExpectStatus = strings.TrimSpace(req.ExpectStatus)
		switch {
		case !probe.ValidMethod(req.HTTPMethod):
			fail(w, 400, "http_method "+strings.Join(probe.AllowedMethods, ", ")+" olmalı")
			return
		case len(req.Body) > 8192:
			fail(w, 400, "gövde en fazla 8192 karakter olabilir")
			return
		case len(req.ExpectText) > 200:
			fail(w, 400, "beklenen metin en fazla 200 karakter olabilir")
			return
		case req.HTTPMethod == "HEAD" && req.ExpectText != "":
			fail(w, 400, "HEAD yanıtında gövde olmadığı için metin doğrulaması yapılamaz")
			return
		}
		if err := probe.ValidateStatusSpec(req.ExpectStatus); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := probe.ValidateHeaders(req.Headers); err != nil {
			fail(w, 400, err.Error())
			return
		}
	} else {
		req.HTTPMethod, req.Headers, req.Body, req.ExpectStatus, req.ExpectText = "GET", nil, "", "", ""
	}
	if req.Headers == nil {
		req.Headers = map[string]string{}
	}
	verifyTLS, follow := req.VerifyTLS == nil || *req.VerifyTLS, req.FollowRedirects == nil || *req.FollowRedirects
	if req.IntervalSec == 0 {
		req.IntervalSec = 30
	}
	if req.TimeoutMS == 0 {
		req.TimeoutMS = 5000
	}
	if req.IntervalSec < 5 || req.TimeoutMS < 100 || req.TimeoutMS > 60000 {
		fail(w, 400, "interval_sec en az 5, timeout_ms 100 ile 60000 arasında olmalı")
		return
	}
	headersJSON, _ := json.Marshal(req.Headers)
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()

	assetID := req.AssetID
	if assetID == 0 {
		name := strings.TrimSpace(req.AssetName)
		if name == "" || len(name) > 200 {
			fail(w, 400, "asset_id veya asset_name gerekli")
			return
		}
		kind := req.AssetKind
		if kind == "" {
			kind = map[string]string{"http": "website", "tcp": "service", "postgres": "database", "icmp": "host", "snmp": "network", "dns": "service", "tls": "website", "udp": "service", "ntp": "service"}[req.Type]
		}
		if !validKind(kind) {
			fail(w, 400, "asset_kind geçersiz")
			return
		}
		assetID, _, err = s.ensureAsset(r.Context(), tx, a.TenantID, name, kind, normalizeLanguage(req.Language), "agentless")
		if err != nil {
			if code, ok := licenseStatus(err); ok {
				fail(w, code, err.Error())
				return
			}
			fail(w, 500, "veritabanı hatası")
			return
		}
		if req.AssetKind != "" { // açıkça seçilen tür, var olan asset için de geçerlidir
			tx.ExecContext(r.Context(), `UPDATE assets SET kind = $1 WHERE id = $2 AND tenant_id = $3`, req.AssetKind, assetID, a.TenantID)
		}
	} else {
		var one int
		if err := tx.QueryRowContext(r.Context(), `SELECT 1 FROM assets WHERE id = $1 AND tenant_id = $2`, assetID, a.TenantID).Scan(&one); err != nil {
			fail(w, 404, "asset bulunamadı")
			return
		}
	}
	var id int64
	if err := tx.QueryRowContext(r.Context(),
		`INSERT INTO probes (tenant_id, asset_id, type, target, interval_sec, timeout_ms,
		                     http_method, headers, body, expect_status, expect_text, verify_tls, follow_redirects, options)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id`,
		a.TenantID, assetID, req.Type, strings.TrimSpace(req.Target), req.IntervalSec, req.TimeoutMS,
		req.HTTPMethod, string(headersJSON), req.Body, req.ExpectStatus, req.ExpectText, verifyTLS, follow, string(optionsJSON)).Scan(&id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "probe.create", req.Type+" "+maskTarget(req.Type, req.Target))
	writeJSON(w, 201, map[string]any{"id": id, "asset_id": assetID})
}

func (s *Server) deleteProbe(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM probes WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "probe bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "probe.delete", strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusNoContent)
}

// ---- Alarm kuralları ve alarmlar ----

func (s *Server) listRules(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, name, metric, comparator, threshold, window_sec, severity, enabled FROM alert_rules WHERE tenant_id = $1 ORDER BY id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, metric, cmp, sev string
		var thr float64
		var win int
		var enabled bool
		if rows.Scan(&id, &name, &metric, &cmp, &thr, &win, &sev, &enabled) == nil {
			list = append(list, map[string]any{"id": id, "name": name, "metric": metric, "comparator": cmp,
				"threshold": thr, "window_sec": win, "severity": sev, "enabled": enabled})
		}
	}
	writeJSON(w, 200, list)
}

func (s *Server) createRule(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		Name       string  `json:"name"`
		Metric     string  `json:"metric"`
		Comparator string  `json:"comparator"`
		Threshold  float64 `json:"threshold"`
		WindowSec  int     `json:"window_sec"`
		Severity   string  `json:"severity"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.WindowSec == 0 {
		req.WindowSec = 60
	}
	if req.Severity == "" {
		req.Severity = "warning"
	}
	req.Name, req.Metric = strings.TrimSpace(req.Name), strings.TrimSpace(req.Metric)
	if req.Name == "" || req.Metric == "" || len(req.Name) > 200 || len(req.Metric) > 128 ||
		!oneOf(req.Comparator, ">", "<") || !oneOf(req.Severity, "info", "warning", "critical") || req.WindowSec < 10 {
		fail(w, 400, "name, metric, comparator (> veya <), severity ve window_sec (en az 10) geçerli olmalı")
		return
	}
	var id int64
	if err := s.DB.QueryRowContext(r.Context(),
		`INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		a.TenantID, req.Name, req.Metric, req.Comparator, req.Threshold, req.WindowSec, req.Severity).Scan(&id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "rule.create", req.Name)
	writeJSON(w, 201, map[string]any{"id": id})
}

// updateRule bir alarm kuralını düzenler (ad, metrik, koşul, eşik, pencere, önem, açık/kapalı).
// Kural değişince açık alarmları kapatılır; koşul hâlâ sağlanıyorsa bir sonraki değerlendirmede yeniden açılır.
func (s *Server) updateRule(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Name       string  `json:"name"`
		Metric     string  `json:"metric"`
		Comparator string  `json:"comparator"`
		Threshold  float64 `json:"threshold"`
		WindowSec  int     `json:"window_sec"`
		Severity   string  `json:"severity"`
		Enabled    *bool   `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Name, req.Metric = strings.TrimSpace(req.Name), strings.TrimSpace(req.Metric)
	if req.Name == "" || req.Metric == "" || len(req.Name) > 200 || len(req.Metric) > 128 ||
		!oneOf(req.Comparator, ">", "<") || !oneOf(req.Severity, "info", "warning", "critical") || req.WindowSec < 10 {
		fail(w, 400, "name, metric, comparator (> veya <), severity ve window_sec (en az 10) geçerli olmalı")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(),
		`UPDATE alert_rules SET name=$1, metric=$2, comparator=$3, threshold=$4, window_sec=$5, severity=$6, enabled=$7 WHERE id=$8 AND tenant_id=$9`,
		req.Name, req.Metric, req.Comparator, req.Threshold, req.WindowSec, req.Severity, enabled, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "kural bulunamadı")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE alerts SET state = 'resolved', resolved_at = now() WHERE rule_id = $1 AND state = 'open'`, id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "rule.update", req.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteRule(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM alert_rules WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "kural bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "rule.delete", strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request, a Auth) {
	state := r.URL.Query().Get("state")
	if state != "open" && state != "resolved" {
		state = "open"
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT al.id, r.name, r.severity, a.name, r.metric, r.comparator, r.threshold, al.value, al.opened_at, al.resolved_at
		 FROM alerts al JOIN alert_rules r ON r.id = al.rule_id JOIN assets a ON a.id = al.asset_id
		 WHERE al.tenant_id = $1 AND al.state = $2 ORDER BY al.opened_at DESC LIMIT 200`, a.TenantID, state)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var rule, sev, asset, metric, cmp string
		var thr, val float64
		var opened time.Time
		var resolved sql.NullTime
		if rows.Scan(&id, &rule, &sev, &asset, &metric, &cmp, &thr, &val, &opened, &resolved) != nil {
			continue
		}
		it := map[string]any{"id": id, "rule": rule, "severity": sev, "asset": asset, "metric": metric,
			"comparator": cmp, "threshold": thr, "value": val, "opened_at": opened}
		if resolved.Valid {
			it["resolved_at"] = resolved.Time
		}
		list = append(list, it)
	}
	writeJSON(w, 200, list)
}

// ---- Lisans ----

func (s *Server) getLicense(w http.ResponseWriter, r *http.Request, a Auth) {
	var limit, used int
	var mode string
	var validUntil sql.NullTime
	if err := s.DB.QueryRowContext(r.Context(), `SELECT asset_limit, valid_until, mode FROM licenses WHERE tenant_id = $1`, a.TenantID).Scan(&limit, &validUntil, &mode); err != nil {
		fail(w, 500, "lisans okunamadı")
		return
	}
	s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM assets WHERE tenant_id = $1`, a.TenantID).Scan(&used)
	out := map[string]any{"mode": mode, "asset_limit": limit, "assets_used": used}
	if validUntil.Valid {
		out["valid_until"] = validUntil.Time
	}
	writeJSON(w, 200, out)
}

// PUT /api/license - ilk sürümde yönetici tarafından elle güncellenir.
// Üretimde on-premise için imzalı lisans dosyası, SaaS için faturalama sistemi bu ucu çağırmalıdır.
func (s *Server) putLicense(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		AssetLimit int        `json:"asset_limit"`
		ValidUntil *time.Time `json:"valid_until"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.AssetLimit < 0 || req.AssetLimit > 10000000 {
		fail(w, 400, "asset_limit geçersiz")
		return
	}
	if _, err := s.DB.ExecContext(r.Context(),
		`UPDATE licenses SET asset_limit = $2, valid_until = $3, updated_at = now() WHERE tenant_id = $1`,
		a.TenantID, req.AssetLimit, req.ValidUntil); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "license.update", fmt.Sprintf("asset_limit=%d", req.AssetLimit))
	s.getLicense(w, r, a)
}

// ---- API anahtarları ----

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, label, role, created_at FROM api_keys WHERE tenant_id = $1 ORDER BY id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var label, role string
		var created time.Time
		if rows.Scan(&id, &label, &role, &created) == nil {
			list = append(list, map[string]any{"id": id, "label": label, "role": role, "created_at": created})
		}
	}
	writeJSON(w, 200, list)
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct{ Label, Role string }
	if !decode(w, r, &req) {
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Role == "" {
		req.Role = "agent"
	}
	if req.Label == "" || len(req.Label) > 100 || !oneOf(req.Role, "admin", "agent") {
		fail(w, 400, "label zorunlu, role admin veya agent olmalı")
		return
	}
	key := NewKey()
	var id int64
	if err := s.DB.QueryRowContext(r.Context(),
		`INSERT INTO api_keys (tenant_id, label, role, key_hash) VALUES ($1,$2,$3,$4) RETURNING id`,
		a.TenantID, req.Label, req.Role, HashKey(key)).Scan(&id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "key.create", req.Label+" ("+req.Role+")")
	// Anahtar yalnızca bu yanıtta görünür; veritabanında yalnızca özeti saklanır.
	writeJSON(w, 201, map[string]any{"id": id, "key": key})
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	// Son yönetici anahtarının silinmesi sistemi yönetilemez bırakır.
	res, err := s.DB.ExecContext(r.Context(),
		`DELETE FROM api_keys k WHERE k.id = $1 AND k.tenant_id = $2
		   AND (k.role <> 'admin' OR EXISTS (SELECT 1 FROM api_keys o WHERE o.tenant_id = $2 AND o.role = 'admin' AND o.id <> k.id))`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "anahtar bulunamadı veya son yönetici anahtarı silinemez")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "key.delete", strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusNoContent)
}

// ---- Denetim günlüğü ----

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT actor, action, detail, at FROM audit_log WHERE tenant_id = $1 ORDER BY id DESC LIMIT 200`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var actor, action, detail string
		var at time.Time
		if rows.Scan(&actor, &action, &detail, &at) == nil {
			list = append(list, map[string]any{"actor": actor, "action": action, "detail": detail, "at": at})
		}
	}
	writeJSON(w, 200, list)
}

// updateProbe kayıtlı bir probe'u değiştirir. Tür ve asset sabittir. Gizli alanlar (başlıklar, gövde, SNMP topluluğu,
// parola içeren hedef) istekte yoksa/boşsa korunur.
func (s *Server) updateProbe(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Target          *string            `json:"target"`
		IntervalSec     int                `json:"interval_sec"`
		TimeoutMS       int                `json:"timeout_ms"`
		Enabled         *bool              `json:"enabled"`
		HTTPMethod      string             `json:"http_method"`
		Headers         *map[string]string `json:"headers"`
		Body            *string            `json:"body"`
		ExpectStatus    string             `json:"expect_status"`
		ExpectText      string             `json:"expect_text"`
		VerifyTLS       *bool              `json:"verify_tls"`
		FollowRedirects *bool              `json:"follow_redirects"`
		Options         *probe.Options     `json:"options"`
	}
	if !decode(w, r, &req) {
		return
	}
	var typ, target, method, body, expStatus, expText string
	var interval, timeout int
	var enabled, verify, follow bool
	var headersRaw, optsRaw []byte
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT type, target, interval_sec, timeout_ms, enabled, http_method, headers, body, expect_status, expect_text, verify_tls, follow_redirects, options
		 FROM probes WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).
		Scan(&typ, &target, &interval, &timeout, &enabled, &method, &headersRaw, &body, &expStatus, &expText, &verify, &follow, &optsRaw)
	if err != nil {
		fail(w, 404, "probe bulunamadı")
		return
	}
	if req.Target != nil {
		t := strings.TrimSpace(*req.Target)
		if t != "" && t != maskTarget(typ, target) { // maskelenmiş hedef değişmedi demektir
			target = t
		}
	}
	if err := validateTarget(typ, target); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.IntervalSec != 0 {
		interval = req.IntervalSec
	}
	if req.TimeoutMS != 0 {
		timeout = req.TimeoutMS
	}
	if interval < 5 || timeout < 100 || timeout > 60000 {
		fail(w, 400, "interval_sec en az 5, timeout_ms 100 ile 60000 arasında olmalı")
		return
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.VerifyTLS != nil {
		verify = *req.VerifyTLS
	}
	headers := map[string]string{}
	json.Unmarshal(headersRaw, &headers)
	if typ == "http" {
		if req.HTTPMethod != "" {
			method = strings.ToUpper(strings.TrimSpace(req.HTTPMethod))
		}
		if req.Headers != nil && len(*req.Headers) > 0 {
			headers = *req.Headers
		}
		if req.Body != nil && *req.Body != "" {
			body = *req.Body
		}
		expStatus, expText = strings.TrimSpace(req.ExpectStatus), req.ExpectText
		if req.FollowRedirects != nil {
			follow = *req.FollowRedirects
		}
		switch {
		case !probe.ValidMethod(method):
			fail(w, 400, "http_method "+strings.Join(probe.AllowedMethods, ", ")+" olmalı")
			return
		case len(body) > 8192:
			fail(w, 400, "gövde en fazla 8192 karakter olabilir")
			return
		case len(expText) > 200:
			fail(w, 400, "beklenen metin en fazla 200 karakter olabilir")
			return
		case method == "HEAD" && expText != "":
			fail(w, 400, "HEAD yanıtında gövde olmadığı için metin doğrulaması yapılamaz")
			return
		}
		if err := probe.ValidateStatusSpec(expStatus); err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err := probe.ValidateHeaders(headers); err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	var opts probe.Options
	json.Unmarshal(optsRaw, &opts)
	if req.Options != nil && typ != "http" && typ != "postgres" {
		n := *req.Options
		if typ == "snmp" && n.Community == "" {
			n.Community = opts.Community // boş bırakılırsa eski topluluk adı korunur
		}
		opts = n
	}
	if err := validateOptions(typ, &opts); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hj, _ := json.Marshal(headers)
	oj, _ := json.Marshal(opts)
	if _, err := s.DB.ExecContext(r.Context(),
		`UPDATE probes SET target = $1, interval_sec = $2, timeout_ms = $3, enabled = $4, http_method = $5, headers = $6, body = $7,
		        expect_status = $8, expect_text = $9, verify_tls = $10, follow_redirects = $11, options = $12 WHERE id = $13 AND tenant_id = $14`,
		target, interval, timeout, enabled, method, string(hj), body, expStatus, expText, verify, follow, string(oj), id, a.TenantID); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "probe.update", typ+" "+maskTarget(typ, target))
	w.WriteHeader(http.StatusNoContent)
}
