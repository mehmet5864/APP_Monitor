package app

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"appmon/internal/ldap"
	"appmon/internal/probe"
)

const (
	pbkdf2Iter     = 600000
	sessionTTL     = 12 * time.Hour
	maxUserFails   = 5
	userLockFor    = 5 * time.Minute
	minPasswordLen = 10
)

// LDAPSettings LDAP girişi ayarlarıdır (APM_LDAP_*). URL boşsa LDAP kapalıdır.
type LDAPSettings struct {
	URL, DNTemplate, DefaultRole string
}

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	k, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iter, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(k)), nil
}

func checkPassword(pw, enc string) bool {
	p := strings.Split(enc, "$")
	if len(p) != 4 || p[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(p[1])
	if err != nil || iter < 1000 || iter > 5000000 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(p[2])
	want, e2 := base64.RawStdEncoding.DecodeString(p[3])
	if e1 != nil || e2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash kullanıcı yokken de aynı işlem süresini harcamak için kullanılır.
var dummyHash, _ = hashPassword("dummy-password-for-timing")

func validRole(r string) bool { return r == "admin" || r == "operator" || r == "viewer" }

// IP başına başarısız giriş sınırı (bellek içi): 10 dakikada 20 hata.
var ipFails = struct {
	sync.Mutex
	m map[string][]time.Time
}{m: map[string][]time.Time{}}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() { // Apache/ters vekil arkasında
		if xf := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(xf) != nil {
			return xf
		}
	}
	return host
}

func ipBlocked(ip string) bool {
	ipFails.Lock()
	defer ipFails.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	var keep []time.Time
	for _, t := range ipFails.m[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	ipFails.m[ip] = keep
	return len(keep) >= 20
}

func ipFail(ip string) {
	ipFails.Lock()
	ipFails.m[ip] = append(ipFails.m[ip], time.Now())
	ipFails.Unlock()
}

func (s *Server) ldapConfig() (ldap.Config, bool) {
	if s.LDAP.URL == "" || s.LDAP.DNTemplate == "" {
		return ldap.Config{}, false
	}
	tc := &tls.Config{}
	if probe.ExtraRoots != nil {
		tc.RootCAs = probe.ExtraRoots
	}
	return ldap.Config{URL: s.LDAP.URL, DNTemplate: s.LDAP.DNTemplate, TLS: tc}, true
}

func (s *Server) newSession(ctx context.Context, tenant, userID int64, label, role string) (string, time.Time, error) {
	tok := NewKey()
	exp := time.Now().Add(sessionTTL)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO api_keys (tenant_id, label, role, key_hash, user_id, expires_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		tenant, label, role, HashKey(tok), userID, exp)
	s.DB.ExecContext(ctx, `DELETE FROM api_keys WHERE expires_at IS NOT NULL AND expires_at < now()`)
	return tok, exp, err
}

// login POST /api/login {username, password} (herkese açık). Başarılıysa süreli oturum anahtarı döner.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct{ Username, Password string }
	if !decode(w, r, &req) {
		return
	}
	ip := clientIP(r)
	if ipBlocked(ip) {
		fail(w, http.StatusTooManyRequests, "çok fazla başarısız deneme; birkaç dakika sonra tekrar deneyin")
		return
	}
	user := strings.TrimSpace(req.Username)
	bad := func(reason string) {
		ipFail(ip)
		s.audit(r.Context(), s.defaultTenant(r.Context()), user, "login.fail", reason+" ("+ip+")")
		fail(w, http.StatusUnauthorized, "kullanıcı adı veya parola hatalı")
	}
	if !ldap.ValidUser(user) || req.Password == "" || len(req.Password) > 256 {
		checkPassword(req.Password, dummyHash)
		bad("geçersiz istek")
		return
	}
	var id, tenant int64
	var hash, role, source, canon string
	var enabled bool
	var locked sql.NullTime
	err := s.DB.QueryRowContext(r.Context(),
		`SELECT id, tenant_id, pass_hash, role, source, enabled, locked_until, username FROM users WHERE lower(username) = lower($1) ORDER BY id LIMIT 1`, user).
		Scan(&id, &tenant, &hash, &role, &source, &enabled, &locked, &canon)
	found := err == nil
	if err != nil && err != sql.ErrNoRows {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if found && locked.Valid && locked.Time.After(time.Now()) {
		fail(w, http.StatusTooManyRequests, "hesap geçici olarak kilitlendi; birkaç dakika sonra tekrar deneyin")
		return
	}
	okPw := false
	switch {
	case found && source == "local":
		okPw = checkPassword(req.Password, hash)
	case found && source == "ldap":
		if cfg, on := s.ldapConfig(); on {
			okPw = ldap.Authenticate(cfg, user, req.Password) == nil
		}
	case !found:
		checkPassword(req.Password, dummyHash)
		if cfg, on := s.ldapConfig(); on && ldap.Authenticate(cfg, user, req.Password) == nil {
			// İlk LDAP girişi: hesabı varsayılan rolle oluştur (rolü yönetici sonra değiştirir).
			tenant = s.defaultTenant(r.Context())
			role = s.LDAP.DefaultRole
			if !validRole(role) {
				role = "viewer"
			}
			if err := s.DB.QueryRowContext(r.Context(),
				`INSERT INTO users (tenant_id, username, role, source) VALUES ($1,$2,$3,'ldap')
				 ON CONFLICT (tenant_id, lower(username)) DO UPDATE SET username = users.username RETURNING id, role, enabled`,
				tenant, user, role).Scan(&id, &role, &enabled); err != nil {
				fail(w, 500, "veritabanı hatası")
				return
			}
			found, okPw = true, true
		}
	}
	if !found || !okPw || !enabled {
		if found {
			n := 0
			s.DB.QueryRowContext(r.Context(), `UPDATE users SET fail_count = fail_count + 1 WHERE id = $1 RETURNING fail_count`, id).Scan(&n)
			if n >= maxUserFails {
				s.DB.ExecContext(r.Context(), `UPDATE users SET fail_count = 0, locked_until = now() + $2::interval WHERE id = $1`, id, fmt.Sprintf("%d seconds", int(userLockFor.Seconds())))
			}
		}
		bad("hatalı bilgi")
		return
	}
	var tenantOff bool
	s.DB.QueryRowContext(r.Context(), `SELECT disabled FROM tenants WHERE id = $1`, tenant).Scan(&tenantOff)
	if tenantOff {
		s.audit(r.Context(), tenant, user, "login.fail", "kiracı durdurulmuş ("+ip+")")
		fail(w, http.StatusForbidden, "kiracınız durdurulmuş; platform yöneticisiyle görüşün")
		return
	}
	s.DB.ExecContext(r.Context(), `UPDATE users SET fail_count = 0, locked_until = NULL, last_login = now() WHERE id = $1`, id)
	if canon != "" {
		user = canon
	}
	tok, exp, err := s.newSession(r.Context(), tenant, id, user, role)
	if err != nil {
		fail(w, 500, "oturum oluşturulamadı")
		return
	}
	s.audit(r.Context(), tenant, user, "login", ip)
	writeJSON(w, 200, map[string]any{"key": tok, "role": role, "username": user, "expires_at": exp})
}

func (s *Server) defaultTenant(ctx context.Context) int64 {
	var id int64
	s.DB.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&id)
	return id
}

// logout oturum anahtarını siler (API anahtarlarına dokunmaz).
func (s *Server) logout(w http.ResponseWriter, r *http.Request, a Auth) {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		key = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	s.DB.ExecContext(r.Context(), `DELETE FROM api_keys WHERE key_hash = $1 AND user_id IS NOT NULL`, HashKey(key))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	var n int
	s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM users WHERE enabled`).Scan(&n)
	_, ldapOn := s.ldapConfig()
	writeJSON(w, 200, map[string]any{"users": n > 0 || ldapOn, "ldap": ldapOn})
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, username, role, source, enabled, created_at, last_login, locked_until FROM users WHERE tenant_id = $1 ORDER BY lower(username)`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, role, src string
		var en bool
		var created time.Time
		var last, lock sql.NullTime
		if rows.Scan(&id, &name, &role, &src, &en, &created, &last, &lock) == nil {
			m := map[string]any{"id": id, "username": name, "role": role, "source": src, "enabled": en, "created_at": created, "locked": lock.Valid && lock.Time.After(time.Now())}
			if last.Valid {
				m["last_login"] = last.Time
			}
			list = append(list, m)
		}
	}
	_, ldapOn := s.ldapConfig()
	writeJSON(w, 200, map[string]any{"users": list, "ldap": ldapOn, "ldap_default_role": s.LDAP.DefaultRole})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct{ Username, Password, Role string }
	if !decode(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !ldap.ValidUser(req.Username) {
		fail(w, 400, "kullanıcı adı 1-64 karakter; harf, rakam ve . _ @ - kullanılabilir")
		return
	}
	if !validRole(req.Role) {
		fail(w, 400, "rol admin, operator veya viewer olmalı")
		return
	}
	if len(req.Password) < minPasswordLen || len(req.Password) > 256 {
		fail(w, 400, fmt.Sprintf("parola en az %d karakter olmalı", minPasswordLen))
		return
	}
	if q, qe := s.quotasOf(r.Context(), a.TenantID); qe == nil && q.UserLimit > 0 {
		var n int
		s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM users WHERE tenant_id = $1`, a.TenantID).Scan(&n)
		if n >= q.UserLimit {
			fail(w, http.StatusPaymentRequired, errQuotaUsers.Error())
			return
		}
	}
	hash, err := hashPassword(req.Password)
	if err != nil {
		fail(w, 500, "parola işlenemedi")
		return
	}
	var id int64
	err = s.DB.QueryRowContext(r.Context(), `INSERT INTO users (tenant_id, username, pass_hash, role, source) VALUES ($1,$2,$3,$4,'local')
		ON CONFLICT DO NOTHING RETURNING id`, a.TenantID, req.Username, hash, req.Role).Scan(&id)
	if err == sql.ErrNoRows {
		fail(w, 409, "bu kullanıcı adı zaten var")
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "user.create", req.Username+" ("+req.Role+")")
	writeJSON(w, 201, map[string]any{"id": id})
}

// updateUser: rol, etkinlik, parola (yerel) veya kilidi kaldırma. Değişiklikte açık oturumlar kapatılır.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Role     *string `json:"role"`
		Enabled  *bool   `json:"enabled"`
		Password *string `json:"password"`
		Unlock   bool    `json:"unlock"`
	}
	if !decode(w, r, &req) {
		return
	}
	var name, src string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT username, source FROM users WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&name, &src); err != nil {
		fail(w, 404, "kullanıcı bulunamadı")
		return
	}
	if req.Role != nil {
		if !validRole(*req.Role) {
			fail(w, 400, "geçersiz rol")
			return
		}
		s.DB.ExecContext(r.Context(), `UPDATE users SET role = $1 WHERE id = $2`, *req.Role, id)
	}
	if req.Enabled != nil {
		s.DB.ExecContext(r.Context(), `UPDATE users SET enabled = $1 WHERE id = $2`, *req.Enabled, id)
	}
	if req.Password != nil {
		if src != "local" {
			fail(w, 400, "LDAP kullanıcılarının parolası dizin sunucusunda değiştirilir")
			return
		}
		if len(*req.Password) < minPasswordLen || len(*req.Password) > 256 {
			fail(w, 400, fmt.Sprintf("parola en az %d karakter olmalı", minPasswordLen))
			return
		}
		h, err := hashPassword(*req.Password)
		if err != nil {
			fail(w, 500, "parola işlenemedi")
			return
		}
		s.DB.ExecContext(r.Context(), `UPDATE users SET pass_hash = $1 WHERE id = $2`, h, id)
	}
	if req.Unlock {
		s.DB.ExecContext(r.Context(), `UPDATE users SET fail_count = 0, locked_until = NULL WHERE id = $1`, id)
	}
	if req.Role != nil || req.Enabled != nil || req.Password != nil {
		s.DB.ExecContext(r.Context(), `DELETE FROM api_keys WHERE user_id = $1`, id)
	}
	s.audit(r.Context(), a.TenantID, a.Label, "user.update", name)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var name string
	if err := s.DB.QueryRowContext(r.Context(), `DELETE FROM users WHERE id = $1 AND tenant_id = $2 RETURNING username`, id, a.TenantID).Scan(&name); err != nil {
		fail(w, 404, "kullanıcı bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "user.delete", name)
	w.WriteHeader(http.StatusNoContent)
}

// changeOwnPassword POST /api/me/password {old, new} (yalnızca yerel hesaplar).
func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct{ Old, New string }
	if !decode(w, r, &req) {
		return
	}
	if a.UserID == 0 {
		fail(w, 400, "bu oturum bir kullanıcı hesabına ait değil (API anahtarı)")
		return
	}
	var hash, src string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT pass_hash, source FROM users WHERE id = $1`, a.UserID).Scan(&hash, &src); err != nil {
		fail(w, 404, "kullanıcı bulunamadı")
		return
	}
	if src != "local" {
		fail(w, 400, "LDAP hesabının parolası dizin sunucusunda değiştirilir")
		return
	}
	if !checkPassword(req.Old, hash) {
		fail(w, 403, "mevcut parola hatalı")
		return
	}
	if len(req.New) < minPasswordLen || len(req.New) > 256 {
		fail(w, 400, fmt.Sprintf("yeni parola en az %d karakter olmalı", minPasswordLen))
		return
	}
	h, err := hashPassword(req.New)
	if err != nil {
		fail(w, 500, "parola işlenemedi")
		return
	}
	s.DB.ExecContext(r.Context(), `UPDATE users SET pass_hash = $1 WHERE id = $2`, h, a.UserID)
	s.audit(r.Context(), a.TenantID, a.Label, "user.password", "")
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

var _ = errors.New
