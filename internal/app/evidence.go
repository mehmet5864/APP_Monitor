package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// Kanıt zinciri: denetim günlüğü kiracı başına SHA-256 hash zinciriyle bağlanır; aylık raporlar Ed25519 ile imzalanır
// ve zincir başı (son kayıt kimliği + hash) rapora yazılır. Böylece kayıtlardan biri sonradan değiştirilir, araya kayıt
// eklenir veya silinirse zincir kırılır; raporu alıp sunucudan bağımsız saklayan biri, kayıtların sonradan
// değiştirilmediğini ispatlayabilir. Veritabanı yöneticisi tüm zinciri yeniden üretebilir: bu yüzden raporların
// sunucu dışında (ör. e-posta/arşiv) saklanması gerekir; doğrulama bu raporla karşılaştırır.

const evidenceFormat = "appmon-evidence-v1"

// auditHash bir denetim kaydının zincir hash'ini üretir. Alanlar uzunluk ön ekiyle kodlanır (kaydırma saldırısına karşı).
func auditHash(prev string, id, tenant int64, actor, action, detail string, atMicro int64) string {
	h := sha256.New()
	w := func(s string) { fmt.Fprintf(h, "%d:%s|", len(s), s) }
	w("appmon-audit-v1")
	w(prev)
	w(strconv.FormatInt(id, 10))
	w(strconv.FormatInt(tenant, 10))
	w(actor)
	w(action)
	w(detail)
	w(strconv.FormatInt(atMicro, 10))
	return hex.EncodeToString(h.Sum(nil))
}

// auditChained kaydı kiracının zincirine ekler (kiracı başına danışma kilidi altında).
func (s *Server) auditChained(ctx context.Context, tenant int64, actor, action, detail string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, 6100000000+tenant); err != nil {
		return err
	}
	var prev string
	err = tx.QueryRowContext(ctx, `SELECT hash FROM audit_head WHERE tenant_id = $1`, tenant).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT nextval(pg_get_serial_sequence('audit_log', 'id'))`).Scan(&id); err != nil {
		return err
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	hash := auditHash(prev, id, tenant, actor, action, detail, at.UnixMicro())
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log (id, tenant_id, actor, action, detail, at, prev_hash, hash) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, tenant, actor, action, detail, at, prev, hash); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_head (tenant_id, last_id, hash, entries) VALUES ($1,$2,$3,1)
		ON CONFLICT (tenant_id) DO UPDATE SET last_id = EXCLUDED.last_id, hash = EXCLUDED.hash, entries = audit_head.entries + 1, updated_at = now()`,
		tenant, id, hash); err != nil {
		return err
	}
	return tx.Commit()
}

// ChainStatus zincir doğrulama sonucudur.
type ChainStatus struct {
	OK       bool   `json:"ok"`
	Checked  int    `json:"checked"`
	Legacy   int    `json:"legacy"` // zincir öncesi (hash'siz) kayıtlar
	FirstID  int64  `json:"first_id"`
	LastID   int64  `json:"last_id"`
	HeadHash string `json:"head_hash"`
	BrokenID int64  `json:"broken_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// verifyChain kiracının zincirini baştan sona doğrular. İlk hash'li kaydın prev_hash'i başlangıç (çıpa) kabul edilir:
// eski kayıtlar saklama süresi nedeniyle silinmiş olabilir.
func (s *Server) verifyChain(ctx context.Context, tenant int64) (ChainStatus, error) {
	st := ChainStatus{OK: true}
	s.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND hash IS NULL`, tenant).Scan(&st.Legacy)
	rows, err := s.DB.QueryContext(ctx, `SELECT id, actor, action, detail, at, prev_hash, hash FROM audit_log WHERE tenant_id = $1 AND hash IS NOT NULL ORDER BY id`, tenant)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	var prev string
	first := true
	for rows.Next() {
		var id int64
		var actor, action, detail, ph, h string
		var at time.Time
		if err := rows.Scan(&id, &actor, &action, &detail, &at, &ph, &h); err != nil {
			return st, err
		}
		if first {
			st.FirstID, prev, first = id, ph, false
		}
		st.Checked++
		if ph != prev {
			st.OK, st.BrokenID, st.Reason = false, id, "önceki kayıtla bağlantı kopuk (araya kayıt eklenmiş veya silinmiş olabilir)"
			break
		}
		if auditHash(ph, id, tenant, actor, action, detail, at.UnixMicro()) != h {
			st.OK, st.BrokenID, st.Reason = false, id, "kayıt içeriği hash ile uyuşmuyor (kayıt değiştirilmiş)"
			break
		}
		prev, st.LastID, st.HeadHash = h, id, h
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	if st.OK && st.Checked > 0 {
		var hid int64
		var hh string
		if err := s.DB.QueryRowContext(ctx, `SELECT last_id, hash FROM audit_head WHERE tenant_id = $1`, tenant).Scan(&hid, &hh); err != nil {
			st.OK, st.Reason = false, "zincir başı kaydı yok"
		} else if hid != st.LastID || hh != st.HeadHash {
			st.OK, st.BrokenID, st.Reason = false, hid, "zincirin son kayıtları silinmiş veya zincir başı değiştirilmiş"
		}
	}
	return st, nil
}

// GET /api/evidence/chain -> zinciri doğrular.
func (s *Server) evidenceChain(w http.ResponseWriter, r *http.Request, a Auth) {
	st, err := s.verifyChain(r.Context(), a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	writeJSON(w, 200, st)
}

// ---- İmza anahtarı ----

type evKey struct {
	id   string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func keyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// evidenceKey sunucunun imza anahtarını getirir; yoksa üretir (özel anahtar kasa ile şifrelenir).
func (s *Server) evidenceKey(ctx context.Context) (*evKey, error) {
	if s.Vault == nil {
		return nil, errors.New("imza anahtarı için kasa (ana anahtar) etkin olmalı")
	}
	var kid string
	var pub, sealed []byte
	err := s.DB.QueryRowContext(ctx, `SELECT key_id, public_key, private_sealed FROM evidence_keys ORDER BY id LIMIT 1`).Scan(&kid, &pub, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		p, priv, gerr := ed25519.GenerateKey(rand.Reader)
		if gerr != nil {
			return nil, gerr
		}
		kid = keyID(p)
		sealed = s.Vault.Seal(priv, "appmon-evidence-key:"+kid)
		tx, terr := s.DB.BeginTx(ctx, nil)
		if terr != nil {
			return nil, terr
		}
		defer tx.Rollback()
		tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(6100000001)`)
		var existing int
		tx.QueryRowContext(ctx, `SELECT count(*) FROM evidence_keys`).Scan(&existing)
		if existing == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO evidence_keys (key_id, public_key, private_sealed) VALUES ($1,$2,$3)`, kid, []byte(p), sealed); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		err = s.DB.QueryRowContext(ctx, `SELECT key_id, public_key, private_sealed FROM evidence_keys ORDER BY id LIMIT 1`).Scan(&kid, &pub, &sealed)
	}
	if err != nil {
		return nil, err
	}
	raw, err := s.Vault.Open(sealed, "appmon-evidence-key:"+kid)
	if err != nil {
		return nil, err
	}
	return &evKey{kid, ed25519.PrivateKey(raw), ed25519.PublicKey(pub)}, nil
}

// GET /api/evidence/public-key -> imza doğrulama için PEM (PKIX) genel anahtar.
func (s *Server) evidencePublicKey(w http.ResponseWriter, r *http.Request, a Auth) {
	k, err := s.evidenceKey(r.Context())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	der, _ := x509.MarshalPKIXPublicKey(k.pub)
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="appmon-evidence-`+k.id+`.pem"`)
	pem.Encode(w, &pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

// ---- Rapor ----

type evidenceReport struct {
	Format      string           `json:"format"`
	TenantID    int64            `json:"tenant_id"`
	Tenant      string           `json:"tenant"`
	Label       string           `json:"label"` // "2026-10"
	From        string           `json:"from"`
	To          string           `json:"to"` // son gün (dahil)
	Partial     bool             `json:"partial"`
	GeneratedAt time.Time        `json:"generated_at"`
	GeneratedBy string           `json:"generated_by"`
	SLA         []slaRow         `json:"sla"`
	Incidents   []map[string]any `json:"incidents"`
	Changes     []map[string]any `json:"changes"`
	ChangesAll  int              `json:"changes_total"`
	Chain       ChainStatus      `json:"chain"`
	HeadID      int64            `json:"audit_head_id"`   // dönem sonundaki son hash'li denetim kaydı
	HeadHash    string           `json:"audit_head_hash"` // ve hash'i
}

var periodRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func (s *Server) buildEvidence(ctx context.Context, a Auth, label string) (*evidenceReport, error) {
	if !periodRe.MatchString(label) {
		return nil, errors.New("dönem YYYY-AA biçiminde olmalı")
	}
	start, _ := time.ParseInLocation("2006-01", label, time.Local)
	end := start.AddDate(0, 1, 0)
	if start.After(time.Now()) {
		return nil, errors.New("gelecekteki dönem için rapor üretilemez")
	}
	var tname string
	if err := s.DB.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id = $1`, a.TenantID).Scan(&tname); err != nil {
		return nil, err
	}
	rep := &evidenceReport{Format: evidenceFormat, TenantID: a.TenantID, Tenant: tname, Label: label, From: start.Format("2006-01-02"),
		To: end.AddDate(0, 0, -1).Format("2006-01-02"), Partial: end.After(time.Now()), GeneratedAt: time.Now().UTC().Truncate(time.Second), GeneratedBy: a.Label}
	var err error
	if rep.SLA, err = s.slaReport(ctx, a.TenantID, start, end.AddDate(0, 0, -1)); err != nil {
		return nil, err
	}
	rep.Incidents = []map[string]any{}
	rows, err := s.DB.QueryContext(ctx, `SELECT a.name, r.name, r.severity, al.opened_at, al.resolved_at, al.ack_by
		FROM alerts al JOIN alert_rules r ON r.id = al.rule_id JOIN assets a ON a.id = al.asset_id
		WHERE al.tenant_id = $1 AND al.opened_at >= $2 AND al.opened_at < $3 ORDER BY al.opened_at LIMIT 1000`, a.TenantID, start, end)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var asset, rule, sev, ack string
		var op time.Time
		var rs sql.NullTime
		if rows.Scan(&asset, &rule, &sev, &op, &rs, &ack) != nil {
			continue
		}
		m := map[string]any{"asset": asset, "rule": rule, "severity": sev, "opened_at": op.UTC().Format(time.RFC3339)}
		if rs.Valid {
			m["resolved_at"] = rs.Time.UTC().Format(time.RFC3339)
			m["duration_s"] = int(rs.Time.Sub(op).Seconds())
		}
		if ack != "" {
			m["ack_by"] = ack
		}
		rep.Incidents = append(rep.Incidents, m)
	}
	rows.Close()
	rep.Changes = []map[string]any{}
	s.DB.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id = $1 AND at >= $2 AND at < $3`, a.TenantID, start, end).Scan(&rep.ChangesAll)
	crow, err := s.DB.QueryContext(ctx, `SELECT id, at, actor, action, detail FROM audit_log WHERE tenant_id = $1 AND at >= $2 AND at < $3 ORDER BY id LIMIT 2000`, a.TenantID, start, end)
	if err != nil {
		return nil, err
	}
	for crow.Next() {
		var id int64
		var at time.Time
		var actor, action, detail string
		if crow.Scan(&id, &at, &actor, &action, &detail) == nil {
			rep.Changes = append(rep.Changes, map[string]any{"id": id, "at": at.UTC().Format(time.RFC3339), "actor": actor, "action": action, "detail": detail})
		}
	}
	crow.Close()
	if rep.Chain, err = s.verifyChain(ctx, a.TenantID); err != nil {
		return nil, err
	}
	// Dönem sonundaki (veya şimdiye kadarki) zincir başı.
	s.DB.QueryRowContext(ctx, `SELECT id, hash FROM audit_log WHERE tenant_id = $1 AND hash IS NOT NULL AND at < $2 ORDER BY id DESC LIMIT 1`, a.TenantID, end).Scan(&rep.HeadID, &rep.HeadHash)
	return rep, nil
}

type evidencePackage struct {
	Format     string `json:"format"`
	ReportJSON string `json:"report_json"` // imzalanan tam bayt dizisi (JSON metni)
	Signature  string `json:"signature"`   // base64 Ed25519
	PublicKey  string `json:"public_key"`  // base64 ham 32 bayt
	KeyID      string `json:"key_id"`
}

func packageOf(k *evKey, reportJSON, sig string) evidencePackage {
	return evidencePackage{Format: evidenceFormat, ReportJSON: reportJSON, Signature: sig, PublicKey: base64.StdEncoding.EncodeToString(k.pub), KeyID: k.id}
}

// POST /api/evidence/reports {"period":"2026-10"} -> rapor üretir, imzalar, saklar.
func (s *Server) createEvidence(w http.ResponseWriter, r *http.Request, a Auth) {
	var body struct {
		Period string `json:"period"`
	}
	if !decode(w, r, &body) {
		return
	}
	k, err := s.evidenceKey(r.Context())
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	rep, err := s.buildEvidence(r.Context(), a, body.Period)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	raw, _ := json.Marshal(rep)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(k.priv, raw))
	var id int64
	if err := s.DB.QueryRowContext(r.Context(), `INSERT INTO evidence_reports (tenant_id, label, created_by, head_id, head_hash, key_id, report_json, signature)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, a.TenantID, rep.Label, a.Label, rep.Chain.LastID, rep.Chain.HeadHash, k.id, string(raw), sig).Scan(&id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "evidence.create", rep.Label+" #"+strconv.FormatInt(id, 10))
	writeJSON(w, 201, map[string]any{"id": id, "label": rep.Label, "chain_ok": rep.Chain.OK})
}

// GET /api/evidence/reports
func (s *Server) listEvidence(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, label, created_at, created_by, head_id, head_hash, key_id, (report_json::jsonb #>> '{chain,ok}')
		FROM evidence_reports WHERE tenant_id = $1 ORDER BY id DESC LIMIT 100`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id, hid int64
		var label, by, hh, kid string
		var at time.Time
		var ok sql.NullString
		if rows.Scan(&id, &label, &at, &by, &hid, &hh, &kid, &ok) == nil {
			list = append(list, map[string]any{"id": id, "label": label, "created_at": at, "created_by": by, "head_id": hid, "head_hash": hh, "key_id": kid, "chain_ok": ok.String == "true"})
		}
	}
	writeJSON(w, 200, list)
}

// GET /api/evidence/reports/{id}/package -> indirilebilir imzalı paket (JSON).
func (s *Server) downloadEvidence(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var label, rj, sig, kid string
	var pub []byte
	err := s.DB.QueryRowContext(r.Context(), `SELECT e.label, e.report_json, e.signature, e.key_id, k.public_key FROM evidence_reports e JOIN evidence_keys k ON k.key_id = e.key_id
		WHERE e.id = $1 AND e.tenant_id = $2`, id, a.TenantID).Scan(&label, &rj, &sig, &kid, &pub)
	if err != nil {
		fail(w, 404, "rapor bulunamadı")
		return
	}
	pkg := evidencePackage{Format: evidenceFormat, ReportJSON: rj, Signature: sig, PublicKey: base64.StdEncoding.EncodeToString(pub), KeyID: kid}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="appmon-kanit-`+label+`.json"`)
	json.NewEncoder(w).Encode(pkg)
}

// POST /api/evidence/verify (gövde: indirilen paket) -> imzayı ve (aynı kiracıysa) zincir başını doğrular.
func (s *Server) verifyEvidence(w http.ResponseWriter, r *http.Request, a Auth) {
	var pkg evidencePackage
	if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&pkg); err != nil || pkg.Format != evidenceFormat {
		fail(w, 400, "geçerli bir kanıt paketi değil")
		return
	}
	pub, err1 := base64.StdEncoding.DecodeString(pkg.PublicKey)
	sig, err2 := base64.StdEncoding.DecodeString(pkg.Signature)
	if err1 != nil || err2 != nil || len(pub) != ed25519.PublicKeySize {
		fail(w, 400, "paket bozuk (anahtar veya imza)")
		return
	}
	out := map[string]any{"signature_valid": ed25519.Verify(ed25519.PublicKey(pub), []byte(pkg.ReportJSON), sig)}
	if k, err := s.evidenceKey(r.Context()); err == nil {
		out["key_is_this_server"] = k.id == keyID(ed25519.PublicKey(pub)) && string(k.pub) == string(pub)
	}
	var rep evidenceReport
	if err := json.Unmarshal([]byte(pkg.ReportJSON), &rep); err != nil {
		fail(w, 400, "rapor içeriği okunamadı")
		return
	}
	out["label"], out["tenant"], out["generated_at"], out["head_id"], out["head_hash"] = rep.Label, rep.Tenant, rep.GeneratedAt, rep.HeadID, rep.HeadHash
	out["chain_ok_at_creation"] = rep.Chain.OK
	type head struct {
		id   int64
		hash string
	}
	heads := []head{}
	for _, hd := range []head{{rep.HeadID, rep.HeadHash}, {rep.Chain.LastID, rep.Chain.HeadHash}} {
		if hd.id > 0 {
			heads = append(heads, hd)
		}
	}
	if rep.TenantID == a.TenantID && len(heads) > 0 {
		match := true
		for _, hd := range heads {
			var h sql.NullString
			err := s.DB.QueryRowContext(r.Context(), `SELECT hash FROM audit_log WHERE id = $1 AND tenant_id = $2`, hd.id, a.TenantID).Scan(&h)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				match = false
				out["chain_note"] = "raporun zincir başı kaydı veritabanında yok (silinmiş olabilir)"
			case err != nil:
				fail(w, 500, "veritabanı hatası")
				return
			case h.String != hd.hash:
				match = false
				out["chain_note"] = "kayıt rapordan sonra değiştirilmiş: hash uyuşmuyor"
			}
		}
		out["chain_match"] = match
		st, _ := s.verifyChain(r.Context(), a.TenantID)
		out["chain_ok_now"] = st.OK
	} else {
		out["chain_note"] = "paket başka bir kiracıya ait veya zincir başı yok; yalnızca imza doğrulandı"
	}
	writeJSON(w, 200, out)
}
