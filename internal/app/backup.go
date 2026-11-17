package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"

	"appmon/internal/probe"
)

const exportFormat = "appmon-config/1"

type exAsset struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Language  string   `json:"language,omitempty"`
	Group     string   `json:"group,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	SLATarget *float64 `json:"sla_target,omitempty"`
}

type exProbe struct {
	Asset           string        `json:"asset"`
	Type            string        `json:"type"`
	Target          string        `json:"target"`
	IntervalSec     int           `json:"interval_sec"`
	TimeoutMS       int           `json:"timeout_ms"`
	Enabled         bool          `json:"enabled"`
	HTTPMethod      string        `json:"http_method,omitempty"`
	ExpectStatus    string        `json:"expect_status,omitempty"`
	ExpectText      string        `json:"expect_text,omitempty"`
	VerifyTLS       bool          `json:"verify_tls"`
	FollowRedirects bool          `json:"follow_redirects"`
	Options         probe.Options `json:"options"`
	SecretsRemoved  bool          `json:"secrets_removed,omitempty"` // gizli değerler yedeğe konmaz; içe aktarırken probe kapalı gelir
}

type exRule struct {
	Name       string   `json:"name"`
	Metric     string   `json:"metric"`
	Comparator string   `json:"comparator"`
	Threshold  float64  `json:"threshold"`
	WindowSec  int      `json:"window_sec"`
	Severity   string   `json:"severity"`
	Enabled    bool     `json:"enabled"`
	Assets     []string `json:"assets,omitempty"`
	Group      string   `json:"group,omitempty"`
	Tag        string   `json:"tag,omitempty"`
}

type exMaint struct {
	Name     string    `json:"name"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Repeat   string    `json:"repeat,omitempty"`
	Assets   []string  `json:"assets,omitempty"`
	Group    string    `json:"group,omitempty"`
	Tag      string    `json:"tag,omitempty"`
}

type configExport struct {
	Format     string    `json:"format"`
	Version    string    `json:"app_version"`
	ExportedAt time.Time `json:"exported_at"`
	Note       string    `json:"note"`
	Assets     []exAsset `json:"assets"`
	Probes     []exProbe `json:"probes"`
	Rules      []exRule  `json:"alert_rules"`
	Maint      []exMaint `json:"maintenance_windows"`
}

func hasSecret(typ, target string, o probe.Options, hdrs string) bool {
	if o.Community != "" || o.AuthPass != "" || o.PrivPass != "" {
		return true
	}
	if (typ == "postgres" || typ == "pgstats") && strings.Contains(target, "@") {
		return true
	}
	return hdrs != "" && hdrs != "{}" && hdrs != "null"
}

func (s *Server) buildExport(ctx context.Context, tenant int64) (*configExport, error) {
	ex := &configExport{Format: exportFormat, ExportedAt: time.Now().UTC(), Assets: []exAsset{}, Probes: []exProbe{}, Rules: []exRule{}, Maint: []exMaint{},
		Note: "Parolalar, SNMP toplulukları, HTTP başlıkları ve bildirim/SIEM kanalı ayarları yedeğe konmaz. Senaryolar ve metrik geçmişi için veritabanı yedeği (pg_dump) alın."}
	rows, err := s.DB.QueryContext(ctx, `SELECT name, kind, language, grp, tags, sla_target FROM assets WHERE tenant_id = $1 ORDER BY name`, tenant)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var a exAsset
		var tags pq.StringArray
		if err := rows.Scan(&a.Name, &a.Kind, &a.Language, &a.Group, &tags, &a.SLATarget); err != nil {
			rows.Close()
			return nil, err
		}
		a.Tags = tags
		ex.Assets = append(ex.Assets, a)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT a.name, p.type, p.target, p.interval_sec, p.timeout_ms, p.enabled, p.http_method, p.expect_status, p.expect_text,
		p.verify_tls, p.follow_redirects, p.options, p.headers::text FROM probes p JOIN assets a ON a.id = p.asset_id WHERE p.tenant_id = $1 ORDER BY p.id`, tenant)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p exProbe
		var optRaw []byte
		var hdrs string
		if err := rows.Scan(&p.Asset, &p.Type, &p.Target, &p.IntervalSec, &p.TimeoutMS, &p.Enabled, &p.HTTPMethod, &p.ExpectStatus, &p.ExpectText, &p.VerifyTLS, &p.FollowRedirects, &optRaw, &hdrs); err != nil {
			rows.Close()
			return nil, err
		}
		var o probe.Options
		json.Unmarshal(optRaw, &o)
		p.SecretsRemoved = hasSecret(p.Type, p.Target, o, hdrs)
		p.Options = o.Redacted()
		if p.Type == "postgres" || p.Type == "pgstats" {
			p.Target = maskTarget(p.Type, p.Target)
		}
		ex.Probes = append(ex.Probes, p)
	}
	rows.Close()
	names := func(ctx context.Context, ids []int64) []string {
		out := []string{}
		for _, id := range ids {
			var n string
			if s.DB.QueryRowContext(ctx, `SELECT name FROM assets WHERE id = $1 AND tenant_id = $2`, id, tenant).Scan(&n) == nil {
				out = append(out, n)
			}
		}
		return out
	}
	rows, err = s.DB.QueryContext(ctx, `SELECT name, metric, comparator, threshold, window_sec, severity, enabled, asset_ids, group_name, tag FROM alert_rules WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r exRule
		var ids pq.Int64Array
		if err := rows.Scan(&r.Name, &r.Metric, &r.Comparator, &r.Threshold, &r.WindowSec, &r.Severity, &r.Enabled, &ids, &r.Group, &r.Tag); err != nil {
			rows.Close()
			return nil, err
		}
		r.Assets = names(ctx, ids)
		ex.Rules = append(ex.Rules, r)
	}
	rows.Close()
	rows, err = s.DB.QueryContext(ctx, `SELECT name, starts_at, ends_at, repeat, asset_ids, group_name, tag FROM maintenance_windows WHERE tenant_id = $1 ORDER BY id`, tenant)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var m exMaint
		var ids pq.Int64Array
		if err := rows.Scan(&m.Name, &m.StartsAt, &m.EndsAt, &m.Repeat, &ids, &m.Group, &m.Tag); err != nil {
			rows.Close()
			return nil, err
		}
		m.Assets = names(ctx, ids)
		ex.Maint = append(ex.Maint, m)
	}
	rows.Close()
	return ex, nil
}

func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request, a Auth) {
	ex, err := s.buildExport(r.Context(), a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "config.export", fmt.Sprintf("%d asset, %d probe", len(ex.Assets), len(ex.Probes)))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="appmon-yapilandirma-`+time.Now().Format("20060102-1504")+`.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(ex)
}

type importResult struct {
	Assets, Probes, Rules, Maint int
	Skipped                      []string
	Disabled                     int
}

// importConfig yalnızca eksik olanları ekler (var olan hiçbir şeyi değiştirmez veya silmez).
func (s *Server) importConfig(w http.ResponseWriter, r *http.Request, a Auth) {
	var ex configExport
	if !decode(w, r, &ex) {
		return
	}
	if ex.Format != exportFormat {
		fail(w, 400, "bu dosya APP_MON yapılandırma yedeği değil (biçim: "+ex.Format+")")
		return
	}
	if len(ex.Assets) > 5000 || len(ex.Probes) > 20000 || len(ex.Rules) > 2000 || len(ex.Maint) > 2000 {
		fail(w, 400, "yedek çok büyük")
		return
	}
	ctx := r.Context()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	var res importResult
	skip := func(f string, args ...any) {
		if len(res.Skipped) < 50 {
			res.Skipped = append(res.Skipped, fmt.Sprintf(f, args...))
		}
	}
	ids := map[string]int64{}
	assetID := func(name, kind, lang string) (int64, error) {
		if id, ok := ids[name]; ok {
			return id, nil
		}
		if !validKind(kind) {
			kind = "service"
		}
		id, created, err := s.ensureAsset(ctx, tx, a.TenantID, name, kind, normalizeLanguage(lang), "agentless")
		if err != nil {
			return 0, err
		}
		if created {
			res.Assets++
		}
		ids[name] = id
		return id, nil
	}
	for _, x := range ex.Assets {
		name := strings.TrimSpace(x.Name)
		if name == "" || len(name) > 200 {
			continue
		}
		id, err := assetID(name, x.Kind, x.Language)
		if err != nil {
			if code, ok := licenseStatus(err); ok {
				fail(w, code, err.Error()+" (içe aktarma geri alındı)")
				return
			}
			log.Printf("içe aktarma (asset %q): %v", name, err)
			fail(w, 500, "veritabanı hatası")
			return
		}
		tags := pq.StringArray(x.Tags)
		if tags == nil {
			tags = pq.StringArray{}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE assets SET grp = CASE WHEN grp = '' THEN $1::text ELSE grp END, tags = CASE WHEN cardinality(tags) = 0 THEN $2::text[] ELSE tags END,
			sla_target = COALESCE(sla_target, $3::double precision) WHERE id = $4`, x.Group, tags, x.SLATarget, id); err != nil {
			log.Printf("içe aktarma (asset ayrıntıları %q): %v", name, err)
			fail(w, 500, "veritabanı hatası")
			return
		}
	}
	for _, p := range ex.Probes {
		if err := validateTarget(p.Type, p.Target); err != nil {
			skip("%s %s: %v", p.Type, p.Target, err)
			continue
		}
		if p.SecretsRemoved && strings.Contains(p.Target, ":gizli@") {
			skip("%s %s: parola yedekte yok; probe'u elle ekleyin", p.Type, p.Target)
			continue
		}
		o := p.Options
		if err := validateOptions(p.Type, &o); err != nil {
			if p.SecretsRemoved {
				err = fmt.Errorf("gizli bilgiler yedekte yok, probe'u elle ekleyin (%v)", err)
			}
			skip("%s %s: %v", p.Type, p.Target, err)
			continue
		}
		if o.Method == "ssh" {
			skip("%s %s: SSH anahtarı kasada tutulur ve yedeğe girmez; probe'u elle ekleyin", p.Type, p.Target)
			continue
		}
		if p.IntervalSec < 5 || p.TimeoutMS < 100 || p.TimeoutMS > 60000 {
			skip("%s %s: aralık/zaman aşımı geçersiz", p.Type, p.Target)
			continue
		}
		if q, err := s.quotasOf(ctx, a.TenantID); err == nil {
			if p.IntervalSec < q.MinInterval {
				skip("%s %s: aralık bu kiracının en kısa süresinden (%d sn) kısa", p.Type, p.Target, q.MinInterval)
				continue
			}
			if err := s.probeQuotaOK(ctx, a.TenantID, 1); err != nil { // tx içinde eklenenler henüz görünmez; tek tek denetlenir
				skip("%s %s: %v", p.Type, p.Target, err)
				continue
			}
		}
		id, err := assetID(strings.TrimSpace(p.Asset), "", "")
		if err != nil {
			if code, ok := licenseStatus(err); ok {
				fail(w, code, err.Error()+" (içe aktarma geri alındı)")
				return
			}
			fail(w, 500, "veritabanı hatası")
			return
		}
		var exists int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM probes WHERE tenant_id = $1 AND asset_id = $2 AND type = $3 AND target = $4`, a.TenantID, id, p.Type, p.Target).Scan(&exists) == nil {
			continue
		}
		enabled := p.Enabled
		if p.SecretsRemoved {
			enabled = false
			res.Disabled++
		}
		method := strings.ToUpper(p.HTTPMethod)
		if p.Type == "http" && !probe.ValidMethod(method) {
			method = "GET"
		}
		if p.Type != "http" {
			method = "GET"
		}
		oj, _ := json.Marshal(o)
		if _, err := tx.ExecContext(ctx, `INSERT INTO probes (tenant_id, asset_id, type, target, interval_sec, timeout_ms, enabled, http_method, headers, body,
			expect_status, expect_text, verify_tls, follow_redirects, options) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'{}','',$9,$10,$11,$12,$13)`,
			a.TenantID, id, p.Type, strings.TrimSpace(p.Target), p.IntervalSec, p.TimeoutMS, enabled, method, p.ExpectStatus, p.ExpectText, p.VerifyTLS, p.FollowRedirects, string(oj)); err != nil {
			log.Printf("içe aktarma (probe %s): %v", p.Target, err)
			skip("%s %s: kaydedilemedi", p.Type, p.Target)
			continue
		}
		res.Probes++
	}
	idList := func(names []string) pq.Int64Array {
		out := pq.Int64Array{}
		for _, n := range names {
			var id int64
			if tx.QueryRowContext(ctx, `SELECT id FROM assets WHERE tenant_id = $1 AND name = $2`, a.TenantID, n).Scan(&id) == nil {
				out = append(out, id)
			}
		}
		return out
	}
	for _, ru := range ex.Rules {
		if ru.Name == "" || (ru.Comparator != ">" && ru.Comparator != "<") || ru.WindowSec < 10 || sevRank[ru.Severity] == 0 || len(ru.Metric) == 0 || len(ru.Metric) > 200 {
			skip("kural %q geçersiz", ru.Name)
			continue
		}
		var n int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM alert_rules WHERE tenant_id = $1 AND name = $2`, a.TenantID, ru.Name).Scan(&n) == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO alert_rules (tenant_id, name, metric, comparator, threshold, window_sec, severity, enabled, asset_ids, group_name, tag)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, a.TenantID, ru.Name, ru.Metric, ru.Comparator, ru.Threshold, ru.WindowSec, ru.Severity, ru.Enabled, idList(ru.Assets), ru.Group, ru.Tag); err == nil {
			res.Rules++
		}
	}
	for _, m := range ex.Maint {
		if m.Name == "" || !m.EndsAt.After(m.StartsAt) || (m.Repeat != "" && m.Repeat != "daily" && m.Repeat != "weekly") {
			skip("bakım penceresi %q geçersiz", m.Name)
			continue
		}
		var n int
		if tx.QueryRowContext(ctx, `SELECT 1 FROM maintenance_windows WHERE tenant_id = $1 AND name = $2 AND starts_at = $3`, a.TenantID, m.Name, m.StartsAt).Scan(&n) == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_windows (tenant_id, name, starts_at, ends_at, repeat, asset_ids, group_name, tag) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			a.TenantID, m.Name, m.StartsAt, m.EndsAt, m.Repeat, idList(m.Assets), m.Group, m.Tag); err == nil {
			res.Maint++
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("içe aktarma commit: %v", err)
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(ctx, a.TenantID, a.Label, "config.import", fmt.Sprintf("%d asset, %d probe, %d kural, %d bakım", res.Assets, res.Probes, res.Rules, res.Maint))
	writeJSON(w, 200, map[string]any{"assets": res.Assets, "probes": res.Probes, "rules": res.Rules, "maintenance": res.Maint, "disabled_for_secrets": res.Disabled, "skipped": append([]string{}, res.Skipped...)})
}
