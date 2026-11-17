package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"appmon/internal/probe"
)

const maxSecretLen = 16 << 10

var secretKinds = map[string]string{"ssh_key": "SSH özel anahtarı", "password": "Parola", "token": "Token / API anahtarı"}

func secretAAD(tenant, id int64) string { return fmt.Sprintf("appmon-secret:%d:%d", tenant, id) }

// vaultReady kasa kullanılabilir değilse nedenini döndürür.
func (s *Server) vaultReady() error {
	if s.Vault == nil {
		msg := s.VaultErr
		if msg == "" {
			msg = "kasa kapalı"
		}
		return errors.New("kasa kullanılamıyor: " + msg)
	}
	return nil
}

// secretValue kasadaki bir değeri çözer (yalnızca sunucu içinde, probe çalışırken kullanılır).
func (s *Server) secretValue(ctx context.Context, tenant, id int64) ([]byte, string, error) {
	if err := s.vaultReady(); err != nil {
		return nil, "", err
	}
	var blob []byte
	var kind string
	if err := s.DB.QueryRowContext(ctx, `SELECT blob, kind FROM secrets WHERE id = $1 AND tenant_id = $2`, id, tenant).Scan(&blob, &kind); err != nil {
		return nil, "", errors.New("kasada bu kayıt yok (silinmiş olabilir)")
	}
	v, err := s.Vault.Open(blob, secretAAD(tenant, id))
	return v, kind, err
}

// checkSSHOptions ssh yöntemli host probe'unun kasa ve mod kısıtlarını denetler.
func (s *Server) checkSSHOptions(ctx context.Context, tenant int64, typ string, o probe.Options) error {
	if typ != "host" || o.Method != "ssh" {
		return nil
	}
	if s.BlockPrivate {
		return errors.New("SaaS modunda SSH ile ölçüm kapalıdır")
	}
	if q, err := s.quotasOf(ctx, tenant); err == nil && !q.AllowSSH {
		return tenantFeatureErr("SSH ile ölçüm")
	}
	var kind string
	if err := s.DB.QueryRowContext(ctx, `SELECT kind FROM secrets WHERE id = $1 AND tenant_id = $2`, o.SecretID, tenant).Scan(&kind); err != nil {
		return errors.New("seçilen kasa kaydı bulunamadı")
	}
	if kind != "ssh_key" && kind != "password" {
		return errors.New("seçilen kasa kaydı bir SSH anahtarı veya parola olmalı")
	}
	return nil
}

// validateSecretValue türe göre değeri doğrular ve normalleştirir.
func validateSecretValue(kind, v string) (string, error) {
	v = strings.ReplaceAll(v, "\r\n", "\n")
	if strings.TrimSpace(v) == "" {
		return "", errors.New("değer boş olamaz")
	}
	if len(v) > maxSecretLen {
		return "", errors.New("değer en fazla 16 KB olabilir")
	}
	if kind == "ssh_key" {
		v = strings.TrimSpace(v) + "\n"
		if !strings.HasPrefix(v, "-----BEGIN ") || !strings.Contains(v, "PRIVATE KEY-----") {
			return "", errors.New("OpenSSH/PEM biçiminde bir özel anahtar yapıştırın (-----BEGIN ... PRIVATE KEY-----)")
		}
		if strings.Contains(v, "ENCRYPTED") {
			return "", errors.New("parola korumalı anahtar desteklenmiyor; parolasız bir anahtar üretin (ssh-keygen -t ed25519 -N \"\")")
		}
		if strings.HasPrefix(v, "-----BEGIN OPENSSH PRIVATE KEY-----") && opensshEncrypted(v) {
			return "", errors.New("parola korumalı anahtar desteklenmiyor; parolasız bir anahtar üretin (ssh-keygen -t ed25519 -N \"\")")
		}
	}
	return v, nil
}

// opensshEncrypted openssh-key-v1 gövdesindeki şifre adı "none" değilse true döner.
func opensshEncrypted(pem string) bool {
	var b strings.Builder
	for _, ln := range strings.Split(pem, "\n") {
		if strings.HasPrefix(ln, "-----") || strings.TrimSpace(ln) == "" {
			continue
		}
		b.WriteString(strings.TrimSpace(ln))
	}
	raw, err := base64.StdEncoding.DecodeString(b.String())
	magic := "openssh-key-v1\x00"
	if err != nil || len(raw) < len(magic)+4 || string(raw[:len(magic)]) != magic {
		return false // tanınmayan biçim: ssh kendisi reddeder
	}
	raw = raw[len(magic):]
	n := binary.BigEndian.Uint32(raw)
	if int(n) > len(raw)-4 {
		return false
	}
	return string(raw[4:4+n]) != "none"
}

// GET /api/vault (yönetici): kasa durumu.
func (s *Server) vaultStatus(w http.ResponseWriter, r *http.Request, a Auth) {
	out := map[string]any{"enabled": s.Vault != nil, "error": s.VaultErr}
	var total, mismatch int
	if s.Vault != nil {
		out["key_id"], out["source"], out["generated"] = s.Vault.KeyID, s.Vault.Source, s.Vault.Generated
		if s.Vault.Source != "env" {
			out["path"] = s.Vault.Path
		}
		s.DB.QueryRowContext(r.Context(), `SELECT count(*), count(*) FILTER (WHERE key_id <> $2) FROM secrets WHERE tenant_id = $1`, a.TenantID, s.Vault.KeyID).Scan(&total, &mismatch)
	} else {
		s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM secrets WHERE tenant_id = $1`, a.TenantID).Scan(&total)
	}
	out["secrets"], out["mismatch"] = total, mismatch
	out["kinds"] = secretKinds
	writeJSON(w, 200, out)
}

// GET /api/secrets: değerler asla dönmez.
func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request, a Auth) {
	keyID := ""
	if s.Vault != nil {
		keyID = s.Vault.KeyID
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT s.id, s.name, s.kind, s.note, s.created_by, s.created_at, s.updated_at, s.key_id,
		        (SELECT count(*) FROM probes p WHERE p.tenant_id = s.tenant_id AND p.options->>'secret_id' = s.id::text)
		 FROM secrets s WHERE s.tenant_id = $1 ORDER BY s.name`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, kind, note, by, kid string
		var created, updated sql.NullTime
		var used int
		if rows.Scan(&id, &name, &kind, &note, &by, &created, &updated, &kid, &used) != nil {
			continue
		}
		list = append(list, map[string]any{"id": id, "name": name, "kind": kind, "kind_label": secretKinds[kind], "note": note,
			"created_by": by, "created_at": created.Time, "updated_at": updated.Time, "in_use": used, "key_ok": keyID != "" && kid == keyID})
	}
	writeJSON(w, 200, list)
}

type secretReq struct {
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`
	Note  string  `json:"note"`
	Value *string `json:"value"`
}

func cleanSecretMeta(req *secretReq) error {
	req.Name, req.Note = strings.TrimSpace(req.Name), strings.TrimSpace(req.Note)
	if req.Name == "" || len(req.Name) > 80 {
		return errors.New("ad zorunlu ve en fazla 80 karakter olmalı")
	}
	if len(req.Note) > 300 {
		return errors.New("not en fazla 300 karakter olabilir")
	}
	return nil
}

// POST /api/secrets (yönetici)
func (s *Server) createSecret(w http.ResponseWriter, r *http.Request, a Auth) {
	if err := s.vaultReady(); err != nil {
		fail(w, 503, err.Error())
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req secretReq
	if !decode(w, r, &req) {
		return
	}
	if _, ok := secretKinds[req.Kind]; !ok {
		fail(w, 400, "tür ssh_key, password veya token olmalı")
		return
	}
	if err := cleanSecretMeta(&req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if req.Value == nil {
		fail(w, 400, "değer zorunlu")
		return
	}
	val, err := validateSecretValue(req.Kind, *req.Value)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	var n int
	s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM secrets WHERE tenant_id = $1`, a.TenantID).Scan(&n)
	if n >= 200 {
		fail(w, 400, "en fazla 200 kasa kaydı oluşturulabilir")
		return
	}
	tx, err := s.DB.BeginTx(r.Context(), nil)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer tx.Rollback()
	var id int64
	err = tx.QueryRowContext(r.Context(),
		`INSERT INTO secrets (tenant_id, name, kind, note, blob, key_id, created_by) VALUES ($1,$2,$3,$4,'\x00',$5,$6) RETURNING id`,
		a.TenantID, req.Name, req.Kind, req.Note, s.Vault.KeyID, a.Label).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			fail(w, 409, "bu adla bir kayıt zaten var")
		} else {
			fail(w, 500, "veritabanı hatası")
		}
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE secrets SET blob = $1 WHERE id = $2`, s.Vault.Seal([]byte(val), secretAAD(a.TenantID, id)), id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if err := tx.Commit(); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "vault.create", fmt.Sprintf("%s (%s)", req.Name, req.Kind))
	writeJSON(w, 201, map[string]any{"id": id})
}

// PUT /api/secrets/{id} (yönetici): ad/not değişir; value verilirse yenilenir.
func (s *Server) updateSecret(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req secretReq
	if !decode(w, r, &req) {
		return
	}
	if err := cleanSecretMeta(&req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	var kind, oldName string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT kind, name FROM secrets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&kind, &oldName); err != nil {
		fail(w, 404, "kayıt bulunamadı")
		return
	}
	rotated := false
	if req.Value != nil && *req.Value != "" {
		if err := s.vaultReady(); err != nil {
			fail(w, 503, err.Error())
			return
		}
		val, err := validateSecretValue(kind, *req.Value)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if _, err := s.DB.ExecContext(r.Context(), `UPDATE secrets SET blob = $1, key_id = $2 WHERE id = $3 AND tenant_id = $4`,
			s.Vault.Seal([]byte(val), secretAAD(a.TenantID, id)), s.Vault.KeyID, id, a.TenantID); err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		rotated = true
	}
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE secrets SET name = $1, note = $2, updated_at = now() WHERE id = $3 AND tenant_id = $4`, req.Name, req.Note, id, a.TenantID); err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			fail(w, 409, "bu adla bir kayıt zaten var")
		} else {
			fail(w, 500, "veritabanı hatası")
		}
		return
	}
	act := "vault.update"
	if rotated {
		act = "vault.rotate"
	}
	s.audit(r.Context(), a.TenantID, a.Label, act, oldName)
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/secrets/{id} (yönetici, yeniden doğrulama ister); kullanımdaysa silinmez.
func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Password string `json:"password"`
		APIKey   string `json:"api_key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if !decode(w, r, &req) {
		return
	}
	var name string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT name FROM secrets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&name); err != nil {
		fail(w, 404, "kayıt bulunamadı")
		return
	}
	var used int
	s.DB.QueryRowContext(r.Context(), `SELECT count(*) FROM probes WHERE tenant_id = $1 AND options->>'secret_id' = $2`, a.TenantID, fmt.Sprint(id)).Scan(&used)
	if used > 0 {
		fail(w, 409, fmt.Sprintf("bu kayıt %d probe tarafından kullanılıyor; önce probe'ları başka bir kayda geçirin veya silin", used))
		return
	}
	if code, err := s.reauth(r, a, req.Password, req.APIKey); err != nil {
		fail(w, code, err.Error())
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM secrets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "vault.delete", name)
	w.WriteHeader(http.StatusNoContent)
}

// learnHostKey ssh probe'u sunucu anahtarını ilk kez gördüğünde onu probe'a sabitler (yalnızca henüz boşsa).
func (s *Server) learnHostKey(p dueProbe) func(string) {
	return func(line string) {
		b, _ := json.Marshal(line)
		res, err := s.DB.Exec(`UPDATE probes SET options = jsonb_set(options, '{host_key}', $2::jsonb)
		    WHERE id = $1 AND COALESCE(options->>'host_key', '') = ''`, p.id, string(b))
		if err == nil {
			if n, _ := res.RowsAffected(); n > 0 {
				s.audit(context.Background(), p.tenant, "sistem", "probe.hostkey", fmt.Sprintf("probe #%d %s sunucu anahtarı sabitlendi: %s", p.id, p.target, probe.KeyFingerprint(line)))
			}
		}
	}
}

// GET /api/assets/{id}/disks: makinenin tüm bağlama noktalarının son doluluk değerleri.
func (s *Server) assetDisks(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var name string
	if err := s.DB.QueryRowContext(r.Context(), `SELECT name FROM assets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&name); err != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT DISTINCT ON (name) name, value, time FROM metrics
		 WHERE tenant_id = $1 AND asset_id = $2 AND time > now() - interval '30 minutes'
		   AND (name LIKE 'host.disk\_pct.%' OR name LIKE 'host.disk\_used\_gb.%' OR name LIKE 'host.disk\_total\_gb.%')
		 ORDER BY name, time DESC`, a.TenantID, id)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	type disk struct {
		Mount   string   `json:"mount"`
		Pct     *float64 `json:"pct,omitempty"`
		UsedGB  *float64 `json:"used_gb,omitempty"`
		TotalGB *float64 `json:"total_gb,omitempty"`
	}
	by := map[string]*disk{}
	var order []string
	var at sql.NullTime
	for rows.Next() {
		var n string
		var v float64
		var t sql.NullTime
		if rows.Scan(&n, &v, &t) != nil {
			continue
		}
		if t.Time.After(at.Time) {
			at = t
		}
		var mount string
		var set func(d *disk)
		switch {
		case strings.HasPrefix(n, "host.disk_pct."):
			mount, set = strings.TrimPrefix(n, "host.disk_pct."), func(d *disk) { d.Pct = &v }
		case strings.HasPrefix(n, "host.disk_used_gb."):
			mount, set = strings.TrimPrefix(n, "host.disk_used_gb."), func(d *disk) { d.UsedGB = &v }
		default:
			mount, set = strings.TrimPrefix(n, "host.disk_total_gb."), func(d *disk) { d.TotalGB = &v }
		}
		d := by[mount]
		if d == nil {
			d = &disk{Mount: mount}
			by[mount] = d
			order = append(order, mount)
		}
		set(d)
	}
	list := make([]*disk, 0, len(order))
	for _, m := range order {
		list = append(list, by[m])
	}
	sort.Slice(list, func(i, j int) bool {
		pi, pj := -1.0, -1.0
		if list[i].Pct != nil {
			pi = *list[i].Pct
		}
		if list[j].Pct != nil {
			pj = *list[j].Pct
		}
		if pi != pj {
			return pi > pj
		}
		return list[i].Mount < list[j].Mount
	})
	out := map[string]any{"asset": name, "disks": list}
	if at.Valid {
		out["time"] = at.Time
	}
	writeJSON(w, 200, out)
}
