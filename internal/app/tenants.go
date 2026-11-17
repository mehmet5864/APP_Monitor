package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"appmon/internal/ldap"
)

// Çok kiracılı işletim (v2.0.0).
//   - Kök (platform) kiracı: Bootstrap'ın oluşturduğu "default". Yöneticileri platform yöneticisidir:
//     kiracı açar/durdurur/siler, kota ve lisansı değiştirir, sunucuyu günceller.
//   - Diğer kiracıların yöneticileri yalnızca kendi kiracılarını yönetir; başka kiracının verisini göremez.
//   - Kullanıcı adları tüm kiracılarda tekildir (giriş ekranında kiracı seçimi yoktur).

const defaultMaxTenants = 10

var slugRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)

// Quotas bir kiracının lisans ve kotalarıdır.
type Quotas struct {
	Mode          string     `json:"mode"`
	AssetLimit    int        `json:"asset_limit"`
	ProbeLimit    int        `json:"probe_limit"`
	UserLimit     int        `json:"user_limit"`
	MinInterval   int        `json:"min_interval_sec"`
	RetentionDays int        `json:"retention_days"`
	AllowBrowser  bool       `json:"allow_browser"`
	AllowCrawl    bool       `json:"allow_crawl"`
	AllowSSH      bool       `json:"allow_ssh"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
}

var (
	errQuotaProbes = errors.New("kiracının probe kotasına ulaşıldı; yeni probe eklenemez")
	errQuotaUsers  = errors.New("kiracının kullanıcı kotasına ulaşıldı")
)

func (s *Server) quotasOf(ctx context.Context, tenant int64) (Quotas, error) {
	var q Quotas
	if s.DB == nil {
		return q, sql.ErrConnDone
	}
	var vu sql.NullTime
	err := s.DB.QueryRowContext(ctx, `SELECT mode, asset_limit, probe_limit, user_limit, min_interval_sec, retention_days, allow_browser, allow_crawl, allow_ssh, valid_until
		FROM licenses WHERE tenant_id = $1`, tenant).Scan(&q.Mode, &q.AssetLimit, &q.ProbeLimit, &q.UserLimit, &q.MinInterval, &q.RetentionDays, &q.AllowBrowser, &q.AllowCrawl, &q.AllowSSH, &vu)
	if vu.Valid {
		q.ValidUntil = &vu.Time
	}
	if q.MinInterval < 5 {
		q.MinInterval = 5
	}
	return q, err
}

// probeQuotaOK yeni n probe için kota payı olup olmadığını denetler.
func (s *Server) probeQuotaOK(ctx context.Context, tenant int64, n int) error {
	q, err := s.quotasOf(ctx, tenant)
	if err != nil || q.ProbeLimit == 0 {
		return err
	}
	var used int
	s.DB.QueryRowContext(ctx, `SELECT count(*) FROM probes WHERE tenant_id = $1`, tenant).Scan(&used)
	if used+n > q.ProbeLimit {
		return errQuotaProbes
	}
	return nil
}

func (s *Server) maxTenants() int {
	if s.MaxTenants > 0 {
		return s.MaxTenants
	}
	return defaultMaxTenants
}

func tenantFeatureErr(what string) error {
	return fmt.Errorf("%s bu kiracı için kapalı (platform yöneticisi açabilir)", what)
}

// ---- Platform yönetimi ----

// GET /api/tenants
func (s *Server) listTenants(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT t.id, t.name, COALESCE(t.slug,''), t.disabled, t.is_root, t.created_at,
		       l.mode, l.asset_limit, l.probe_limit, l.user_limit, l.min_interval_sec, l.retention_days, l.allow_browser, l.allow_crawl, l.allow_ssh, l.valid_until,
		       (SELECT count(*) FROM assets WHERE tenant_id = t.id),
		       (SELECT count(*) FROM probes WHERE tenant_id = t.id),
		       (SELECT count(*) FROM users WHERE tenant_id = t.id),
		       (SELECT count(*) FROM scenarios WHERE tenant_id = t.id),
		       (SELECT count(*) FROM alerts WHERE tenant_id = t.id AND state = 'open'),
		       (SELECT max(at) FROM audit_log WHERE tenant_id = t.id)
		FROM tenants t LEFT JOIN licenses l ON l.tenant_id = t.id ORDER BY t.is_root DESC, lower(t.name)`)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, slug string
		var disabled, root bool
		var created time.Time
		var q Quotas
		var mode sql.NullString
		var al, pl, ul, mi, rd sql.NullInt64
		var ab, ac, as sql.NullBool
		var vu, lastAct sql.NullTime
		var na, np, nu, ns, nal int
		if err := rows.Scan(&id, &name, &slug, &disabled, &root, &created, &mode, &al, &pl, &ul, &mi, &rd, &ab, &ac, &as, &vu, &na, &np, &nu, &ns, &nal, &lastAct); err != nil {
			continue
		}
		q = Quotas{Mode: mode.String, AssetLimit: int(al.Int64), ProbeLimit: int(pl.Int64), UserLimit: int(ul.Int64), MinInterval: int(mi.Int64), RetentionDays: int(rd.Int64),
			AllowBrowser: ab.Bool, AllowCrawl: ac.Bool, AllowSSH: as.Bool}
		if vu.Valid {
			q.ValidUntil = &vu.Time
		}
		it := map[string]any{"id": id, "name": name, "slug": slug, "disabled": disabled, "root": root, "created_at": created, "quotas": q,
			"usage": map[string]int{"assets": na, "probes": np, "users": nu, "scenarios": ns, "open_alerts": nal}}
		if lastAct.Valid {
			it["last_activity"] = lastAct.Time
		}
		out = append(out, it)
	}
	lic := map[int64]string{}
	if lr, err := s.DB.QueryContext(r.Context(), `SELECT tenant_id, lic_customer FROM licenses WHERE lic_id <> ''`); err == nil {
		for lr.Next() {
			var tid int64
			var c string
			if lr.Scan(&tid, &c) == nil {
				lic[tid] = c
			}
		}
		lr.Close()
	}
	for _, it := range out {
		if c, ok := lic[it["id"].(int64)]; ok {
			it["licensed"], it["license_customer"] = true, c
		}
	}
	writeJSON(w, 200, map[string]any{"tenants": out, "max": s.maxTenants()})
}

type tenantReq struct {
	Name          string     `json:"name"`
	Slug          string     `json:"slug"`
	AdminUser     string     `json:"admin_username"`
	AdminPassword string     `json:"admin_password"`
	Disabled      *bool      `json:"disabled"`
	AssetLimit    *int       `json:"asset_limit"`
	ProbeLimit    *int       `json:"probe_limit"`
	UserLimit     *int       `json:"user_limit"`
	MinInterval   *int       `json:"min_interval_sec"`
	RetentionDays *int       `json:"retention_days"`
	AllowBrowser  *bool      `json:"allow_browser"`
	AllowCrawl    *bool      `json:"allow_crawl"`
	AllowSSH      *bool      `json:"allow_ssh"`
	ValidUntil    *time.Time `json:"valid_until"`
	ClearExpiry   bool       `json:"clear_expiry"`
}

func (q *tenantReq) validate() error {
	chk := func(v *int, lo, hi int, name string) error {
		if v != nil && (*v < lo || *v > hi) {
			return fmt.Errorf("%s %d ile %d arasında olmalı", name, lo, hi)
		}
		return nil
	}
	for _, e := range []error{chk(q.AssetLimit, 0, 100000, "asset_limit"), chk(q.ProbeLimit, 0, 1000000, "probe_limit"), chk(q.UserLimit, 0, 10000, "user_limit"),
		chk(q.MinInterval, 5, 86400, "min_interval_sec"), chk(q.RetentionDays, 0, 3650, "retention_days")} {
		if e != nil {
			return e
		}
	}
	return nil
}

func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case strings.ContainsRune("çÇ", r):
			b.WriteRune('c')
			dash = false
		case strings.ContainsRune("ğĞ", r):
			b.WriteRune('g')
			dash = false
		case strings.ContainsRune("ıİ", r):
			b.WriteRune('i')
			dash = false
		case strings.ContainsRune("öÖ", r):
			b.WriteRune('o')
			dash = false
		case strings.ContainsRune("şŞ", r):
			b.WriteRune('s')
			dash = false
		case strings.ContainsRune("üÜ", r):
			b.WriteRune('u')
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteRune('-')
				dash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// POST /api/tenants — kiracıyı, lisansını ve ilk yöneticisini oluşturur.
func (s *Server) createTenant(w http.ResponseWriter, r *http.Request, a Auth) {
	var req tenantReq
	if !decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if l := len([]rune(req.Name)); l < 2 || l > 60 {
		fail(w, 400, "kiracı adı 2-60 karakter olmalı")
		return
	}
	if req.Slug == "" {
		req.Slug = slugify(req.Name)
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if !slugRe.MatchString(req.Slug) || req.Slug == "default" || req.Slug == "api" {
		fail(w, 400, "kısa ad (slug) 1-32 karakter; küçük harf, rakam ve tire olmalı ve 'default'/'api' olamaz")
		return
	}
	req.AdminUser = strings.TrimSpace(req.AdminUser)
	if !ldap.ValidUser(req.AdminUser) {
		fail(w, 400, "yönetici kullanıcı adı 1-64 karakter; harf, rakam ve . _ @ - kullanılabilir")
		return
	}
	if len(req.AdminPassword) < minPasswordLen || len(req.AdminPassword) > 256 {
		fail(w, 400, fmt.Sprintf("yönetici parolası en az %d karakter olmalı", minPasswordLen))
		return
	}
	if err := req.validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	hash, err := hashPassword(req.AdminPassword)
	if err != nil {
		fail(w, 500, "parola işlenemedi")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	tx.ExecContext(r.Context(), `LOCK TABLE tenants IN SHARE ROW EXCLUSIVE MODE`)
	var n int
	tx.QueryRowContext(r.Context(), `SELECT count(*) FROM tenants`).Scan(&n)
	if n >= s.maxTenants() {
		fail(w, 409, fmt.Sprintf("en fazla %d kiracı açılabilir", s.maxTenants()))
		return
	}
	var id int64
	if err := tx.QueryRowContext(r.Context(), `INSERT INTO tenants (name, slug) VALUES ($1,$2) ON CONFLICT DO NOTHING RETURNING id`, req.Name, req.Slug).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 409, "bu ad veya kısa ad zaten kullanılıyor")
		} else {
			fail(w, 500, "veritabanı hatası")
		}
		return
	}
	var mode string
	tx.QueryRowContext(r.Context(), `SELECT mode FROM licenses WHERE tenant_id = $1`, a.TenantID).Scan(&mode)
	if mode == "" {
		mode = s.Mode
	}
	val := func(p *int, d int) int {
		if p != nil {
			return *p
		}
		return d
	}
	flag := func(p *bool, d bool) bool {
		if p != nil {
			return *p
		}
		return d
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO licenses (tenant_id, mode, asset_limit, valid_until, probe_limit, user_limit, min_interval_sec, retention_days, allow_browser, allow_crawl, allow_ssh)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, id, mode, val(req.AssetLimit, 50), req.ValidUntil, val(req.ProbeLimit, 0), val(req.UserLimit, 25), val(req.MinInterval, 10), val(req.RetentionDays, 0),
		flag(req.AllowBrowser, false), flag(req.AllowCrawl, false), flag(req.AllowSSH, false)); err != nil {
		fail(w, 500, "lisans oluşturulamadı")
		return
	}
	var uid int64
	if err := tx.QueryRowContext(r.Context(), `INSERT INTO users (tenant_id, username, pass_hash, role, source) VALUES ($1,$2,$3,'admin','local') ON CONFLICT DO NOTHING RETURNING id`,
		id, req.AdminUser, hash).Scan(&uid); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 409, "bu kullanıcı adı başka bir kiracıda kullanılıyor; kullanıcı adları tüm kiracılarda tekildir")
		} else {
			fail(w, 500, "veritabanı hatası")
		}
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "tenant.create", fmt.Sprintf("%s (#%d, yönetici %s)", req.Name, id, req.AdminUser))
	writeJSON(w, 201, map[string]any{"id": id, "slug": req.Slug})
}

// PUT /api/tenants/{id} — ad, kısa ad, durdurma ve kotalar.
func (s *Server) updateTenant(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req tenantReq
	if !decode(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var root bool
	var oldName string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT is_root, name FROM tenants WHERE id = $1`, id).Scan(&root, &oldName); err != nil {
		fail(w, 404, "kiracı bulunamadı")
		return
	}
	if root && req.Disabled != nil && *req.Disabled {
		fail(w, 400, "platform (kök) kiracı durdurulamaz")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	if req.Name != "" || req.Slug != "" || req.Disabled != nil {
		name := strings.TrimSpace(req.Name)
		if name != "" {
			if l := len([]rune(name)); l < 2 || l > 60 {
				fail(w, 400, "kiracı adı 2-60 karakter olmalı")
				return
			}
		}
		slug := strings.ToLower(strings.TrimSpace(req.Slug))
		if slug != "" && (!slugRe.MatchString(slug) || (slug == "api") || (slug == "default" && !root)) {
			fail(w, 400, "kısa ad geçersiz")
			return
		}
		if root {
			slug = "" // kök kiracının kısa adı değişmez
		}
		if _, err := tx.ExecContext(r.Context(), `UPDATE tenants SET name = COALESCE(NULLIF($2,''), name), slug = COALESCE(NULLIF($3,''), slug), disabled = COALESCE($4, disabled) WHERE id = $1`,
			id, name, slug, req.Disabled); err != nil {
			fail(w, 409, "bu ad veya kısa ad zaten kullanılıyor")
			return
		}
	}
	hasQuota := req.AssetLimit != nil || req.ProbeLimit != nil || req.UserLimit != nil || req.MinInterval != nil || req.RetentionDays != nil ||
		req.AllowBrowser != nil || req.AllowCrawl != nil || req.AllowSSH != nil || req.ValidUntil != nil || req.ClearExpiry
	if hasQuota && s.licensed(r.Context(), id) {
		fail(w, 409, "bu kiracının kotaları imzalı .lic dosyasıyla yönetiliyor; yeni bir lisans dosyası üretip kiracıya iletin")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE licenses SET
		asset_limit = COALESCE($2, asset_limit), probe_limit = COALESCE($3, probe_limit), user_limit = COALESCE($4, user_limit),
		min_interval_sec = COALESCE($5, min_interval_sec), retention_days = COALESCE($6, retention_days),
		allow_browser = COALESCE($7, allow_browser), allow_crawl = COALESCE($8, allow_crawl), allow_ssh = COALESCE($9, allow_ssh),
		valid_until = CASE WHEN $11 THEN NULL WHEN $10::timestamptz IS NOT NULL THEN $10 ELSE valid_until END, updated_at = now()
		WHERE tenant_id = $1`, id, req.AssetLimit, req.ProbeLimit, req.UserLimit, req.MinInterval, req.RetentionDays, req.AllowBrowser, req.AllowCrawl, req.AllowSSH, req.ValidUntil, req.ClearExpiry); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if req.Disabled != nil && *req.Disabled { // açık oturumları kapat
		s.DB.ExecContext(r.Context(), `DELETE FROM api_keys WHERE tenant_id = $1 AND user_id IS NOT NULL`, id)
	}
	s.audit(r.Context(), a.TenantID, a.Label, "tenant.update", fmt.Sprintf("%s (#%d)", oldName, id))
	writeJSON(w, 200, map[string]any{"id": id})
}

// DELETE /api/tenants/{id} {confirm: "<kiracı adı>", password|api_key} — kiracı ve tüm verisi kalıcı silinir.
func (s *Server) deleteTenant(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Confirm  string `json:"confirm"`
		Password string `json:"password"`
		APIKey   string `json:"api_key"`
	}
	if !decode(w, r, &req) {
		return
	}
	var name string
	var root bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT name, is_root FROM tenants WHERE id = $1`, id).Scan(&name, &root); err != nil {
		fail(w, 404, "kiracı bulunamadı")
		return
	}
	if root {
		fail(w, 400, "platform (kök) kiracı silinemez")
		return
	}
	if req.Confirm != name {
		fail(w, 400, "onay için kiracı adını aynen yazın")
		return
	}
	if code, err := s.reauth(r, a, req.Password, req.APIKey); err != nil {
		fail(w, code, err.Error())
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM tenants WHERE id = $1 AND NOT is_root`, id); err != nil {
		fail(w, 500, "kiracı silinemedi: "+err.Error())
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "tenant.delete", fmt.Sprintf("%s (#%d)", name, id))
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/tenants/{id}/admins {username, password} — kiracıya yönetici ekler (parolasını unutan yönetici için kurtarma).
func (s *Server) addTenantAdmin(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct{ Username, Password string }
	if !decode(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !ldap.ValidUser(req.Username) || len(req.Password) < minPasswordLen || len(req.Password) > 256 {
		fail(w, 400, fmt.Sprintf("kullanıcı adı geçersiz veya parola %d karakterden kısa", minPasswordLen))
		return
	}
	var name string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT name FROM tenants WHERE id = $1`, id).Scan(&name); err != nil {
		fail(w, 404, "kiracı bulunamadı")
		return
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		fail(w, 500, "parola işlenemedi")
		return
	}
	var uid int64
	err = s.DB.QueryRowContext(r.Context(), `INSERT INTO users (tenant_id, username, pass_hash, role, source) VALUES ($1,$2,$3,'admin','local') ON CONFLICT DO NOTHING RETURNING id`, id, req.Username, hash).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 409, "bu kullanıcı adı zaten kullanılıyor")
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "tenant.admin", fmt.Sprintf("%s kiracısına yönetici %s eklendi", name, req.Username))
	writeJSON(w, 201, map[string]any{"id": uid})
}

// tenantBySlug herkese açık durum sayfası için kiracıyı bulur ("" = kök kiracı).
func (s *Server) tenantBySlug(ctx context.Context, slug string) (int64, bool) {
	if slug == "" {
		return s.defaultTenant(ctx), true
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM tenants WHERE lower(slug) = lower($1) AND NOT disabled`, slug).Scan(&id)
	return id, err == nil
}

func atoiDef(s string, d int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return d
}
