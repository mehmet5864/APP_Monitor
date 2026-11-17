package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// İmzalı lisans dosyası (.lic): {"format":"APPMON-LIC-1","payload":"<base64 JSON>","sig":"<base64 Ed25519>"}.
// İmzalama anahtarı ana sunucuda, kasa ile mühürlenmiş olarak saklanır; dosya yalnızca adı geçen kiracıya yüklenebilir.
const licFormat = "APPMON-LIC-1"

type licPayload struct {
	ID           string     `json:"id"`
	Customer     string     `json:"customer"`
	Tenant       string     `json:"tenant"` // kiracının kısa adı (slug)
	IssuedAt     time.Time  `json:"issued_at"`
	ValidUntil   *time.Time `json:"valid_until,omitempty"`
	AssetLimit   int        `json:"asset_limit"`
	ProbeLimit   int        `json:"probe_limit"`
	UserLimit    int        `json:"user_limit"`
	MinInterval  int        `json:"min_interval_sec"`
	Retention    int        `json:"retention_days"`
	AllowBrowser bool       `json:"allow_browser"`
	AllowCrawl   bool       `json:"allow_crawl"`
	AllowSSH     bool       `json:"allow_ssh"`
}

type licFile struct {
	Format  string `json:"format"`
	Payload string `json:"payload"`
	Sig     string `json:"sig"`
}

const licKeyName = "license-signing-key"

// signingKey imzalama anahtarını yükler; yoksa üretip kasa ile mühürleyerek saklar.
func (s *Server) signingKey(ctx context.Context) (ed25519.PrivateKey, error) {
	if s.Vault == nil {
		return nil, errors.New("lisans üretmek için kasa (ana anahtar) etkin olmalı")
	}
	var blob []byte
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM platform_secrets WHERE name = $1`, licKeyName).Scan(&blob)
	if err == nil {
		seed, err := s.Vault.Open(blob, licKeyName)
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, errors.New("lisans imza anahtarı çözülemedi (kasa ana anahtarı değişmiş olabilir)")
		}
		return ed25519.NewKeyFromSeed(seed), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO platform_secrets (name, value) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING`, licKeyName, s.Vault.Seal(seed, licKeyName)); err != nil {
		return nil, err
	}
	return s.signingKey(ctx) // eşzamanlı üretimde kazanan anahtar okunur
}

func signLicense(key ed25519.PrivateKey, p licPayload) ([]byte, error) {
	pb, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	f := licFile{Format: licFormat, Payload: base64.StdEncoding.EncodeToString(pb), Sig: base64.StdEncoding.EncodeToString(ed25519.Sign(key, pb))}
	return json.MarshalIndent(f, "", "  ")
}

func verifyLicense(key ed25519.PrivateKey, raw []byte) (*licPayload, error) {
	var f licFile
	if err := json.Unmarshal(raw, &f); err != nil || f.Format != licFormat {
		return nil, errors.New("geçerli bir APP_MON lisans dosyası değil")
	}
	pb, e1 := base64.StdEncoding.DecodeString(f.Payload)
	sig, e2 := base64.StdEncoding.DecodeString(f.Sig)
	if e1 != nil || e2 != nil || !ed25519.Verify(key.Public().(ed25519.PublicKey), pb, sig) {
		return nil, errors.New("lisans imzası geçersiz: dosya değiştirilmiş veya bu sunucu için üretilmemiş")
	}
	var p licPayload
	if json.Unmarshal(pb, &p) != nil || p.ID == "" || p.Tenant == "" {
		return nil, errors.New("lisans içeriği bozuk")
	}
	return &p, nil
}

// POST /api/tenants/{id}/license-file (platform) — kiracı için .lic üretir ve indirtir (sunucuda saklanmaz).
func (s *Server) issueLicenseFile(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Customer string `json:"customer"`
		tenantReq
	}
	if !decode(w, r, &req) {
		return
	}
	if err := req.validate(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var slug, name string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT slug, name FROM tenants WHERE id = $1`, id).Scan(&slug, &name); err != nil {
		fail(w, 404, "kiracı bulunamadı")
		return
	}
	def := func(p *int, d int) int {
		if p == nil {
			return d
		}
		return *p
	}
	flag := func(p *bool) bool { return p != nil && *p }
	p := licPayload{Customer: strings.TrimSpace(req.Customer), Tenant: slug, IssuedAt: time.Now().UTC().Truncate(time.Second),
		AssetLimit: def(req.AssetLimit, 50), ProbeLimit: def(req.ProbeLimit, 0), UserLimit: def(req.UserLimit, 25), MinInterval: def(req.MinInterval, 10), Retention: def(req.RetentionDays, 0),
		AllowBrowser: flag(req.AllowBrowser), AllowCrawl: flag(req.AllowCrawl), AllowSSH: flag(req.AllowSSH)}
	if p.Customer == "" {
		p.Customer = name
	}
	if req.ValidUntil != nil {
		v := req.ValidUntil.UTC()
		if v.Before(time.Now()) {
			fail(w, 400, "geçerlilik tarihi geçmişte")
			return
		}
		p.ValidUntil = &v
	}
	if p.AssetLimit < 1 || p.AssetLimit > 10000000 {
		fail(w, 400, "asset limiti 1 veya daha büyük olmalı")
		return
	}
	b := make([]byte, 8)
	rand.Read(b)
	p.ID = hex.EncodeToString(b)
	key, err := s.signingKey(r.Context())
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	out, err := signLicense(key, p)
	if err != nil {
		fail(w, 500, "lisans üretilemedi")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "license.issue", fmt.Sprintf("%s (%s) id=%s asset=%d", name, slug, p.ID, p.AssetLimit))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="appmon-%s-%s.lic"`, slug, p.IssuedAt.Format("20060102")))
	w.Write(out)
}

// POST /api/license/file (admin) — gövde: .lic dosyası. Yalnızca kendi kiracısı için üretilmiş dosya kabul edilir.
func (s *Server) uploadLicenseFile(w http.ResponseWriter, r *http.Request, a Auth) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		fail(w, 400, "dosya okunamadı veya çok büyük")
		return
	}
	key, err := s.signingKey(r.Context())
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	p, err := verifyLicense(key, raw)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var slug string
	var oldIssued sql.NullTime
	var oldID string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT t.slug, l.lic_issued_at, l.lic_id FROM tenants t JOIN licenses l ON l.tenant_id = t.id WHERE t.id = $1`, a.TenantID).Scan(&slug, &oldIssued, &oldID); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if !strings.EqualFold(p.Tenant, slug) {
		fail(w, 400, "bu lisans başka bir kiracı için üretilmiş")
		return
	}
	if p.ValidUntil != nil && p.ValidUntil.Before(time.Now()) {
		fail(w, 400, "lisansın süresi dolmuş")
		return
	}
	if oldIssued.Valid && p.IssuedAt.Before(oldIssued.Time) {
		fail(w, 409, "daha eski tarihli bir lisans yüklenemez")
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE licenses SET asset_limit=$2, probe_limit=$3, user_limit=$4, min_interval_sec=$5, retention_days=$6,
		allow_browser=$7, allow_crawl=$8, allow_ssh=$9, valid_until=$10, lic_id=$11, lic_customer=$12, lic_issued_at=$13, updated_at=now() WHERE tenant_id=$1`,
		a.TenantID, p.AssetLimit, p.ProbeLimit, p.UserLimit, maxInt(p.MinInterval, 5), p.Retention, p.AllowBrowser, p.AllowCrawl, p.AllowSSH, p.ValidUntil, p.ID, p.Customer, p.IssuedAt); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "license.install", fmt.Sprintf("id=%s müşteri=%s asset=%d", p.ID, p.Customer, p.AssetLimit))
	s.getLicense(w, r, a)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// licensed kiracının kotaları imzalı lisansla geldiyse true döner (elle değiştirilemez).
func (s *Server) licensed(ctx context.Context, tenant int64) bool {
	var id string
	s.DB.QueryRowContext(ctx, `SELECT lic_id FROM licenses WHERE tenant_id = $1`, tenant).Scan(&id)
	return id != ""
}
