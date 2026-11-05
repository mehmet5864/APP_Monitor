package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"

	"appmon/internal/notify"
)

// ---- Bildirim kanalları (yalnızca yönetici) ----

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, name, kind, config, enabled, min_severity, on_resolve, last_error, last_sent FROM notification_channels WHERE tenant_id = $1 ORDER BY id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, kind, minSev, lastErr string
		var raw []byte
		var enabled, onRes bool
		var sent *time.Time
		if rows.Scan(&id, &name, &kind, &raw, &enabled, &minSev, &onRes, &lastErr, &sent) != nil {
			continue
		}
		var cfg notify.Config
		json.Unmarshal(raw, &cfg)
		red, has := cfg.Redacted()
		it := map[string]any{"id": id, "name": name, "kind": kind, "config": red, "has": has, "enabled": enabled,
			"min_severity": minSev, "on_resolve": onRes, "last_error": lastErr}
		if sent != nil {
			it["last_sent"] = *sent
		}
		list = append(list, it)
	}
	writeJSON(w, 200, list)
}

type channelReq struct {
	Name        string        `json:"name"`
	Kind        string        `json:"kind"`
	Config      notify.Config `json:"config"`
	Enabled     *bool         `json:"enabled"`
	MinSeverity string        `json:"min_severity"`
	OnResolve   *bool         `json:"on_resolve"`
}

func (c *channelReq) check(w http.ResponseWriter) bool {
	c.Name = strings.TrimSpace(c.Name)
	if c.MinSeverity == "" {
		c.MinSeverity = "warning"
	}
	if c.Name == "" || len(c.Name) > 100 || !oneOf(c.MinSeverity, "info", "warning", "critical") {
		fail(w, 400, "ad zorunlu; en düşük önem info, warning veya critical olmalı")
		return false
	}
	if err := notify.Validate(c.Kind, c.Config); err != nil {
		fail(w, 400, err.Error())
		return false
	}
	return true
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request, a Auth) {
	var req channelReq
	if !decode(w, r, &req) || !req.check(w) {
		return
	}
	cj, _ := json.Marshal(req.Config)
	enabled, onRes := req.Enabled == nil || *req.Enabled, req.OnResolve == nil || *req.OnResolve
	var id int64
	if err := s.DB.QueryRowContext(r.Context(),
		`INSERT INTO notification_channels (tenant_id, name, kind, config, enabled, min_severity, on_resolve) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		a.TenantID, req.Name, req.Kind, string(cj), enabled, req.MinSeverity, onRes).Scan(&id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "channel.create", req.Kind+" "+req.Name)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) loadChannel(r *http.Request, a Auth, id int64) (kind string, cfg notify.Config, ok bool) {
	var raw []byte
	if s.DB.QueryRowContext(r.Context(), `SELECT kind, config FROM notification_channels WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&kind, &raw) != nil {
		return "", cfg, false
	}
	json.Unmarshal(raw, &cfg)
	return kind, cfg, true
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req channelReq
	if !decode(w, r, &req) {
		return
	}
	_, old, found := s.loadChannel(r, a, id)
	if !found {
		fail(w, 404, "kanal bulunamadı")
		return
	}
	req.Config.Keep(old) // boş bırakılan gizli alanlar eski değeri korur
	if !req.check(w) {
		return
	}
	cj, _ := json.Marshal(req.Config)
	enabled, onRes := req.Enabled == nil || *req.Enabled, req.OnResolve == nil || *req.OnResolve
	if _, err := s.DB.ExecContext(r.Context(),
		`UPDATE notification_channels SET name=$1, kind=$2, config=$3, enabled=$4, min_severity=$5, on_resolve=$6 WHERE id=$7 AND tenant_id=$8`,
		req.Name, req.Kind, string(cj), enabled, req.MinSeverity, onRes, id, a.TenantID); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "channel.update", req.Kind+" "+req.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM notification_channels WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "kanal bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "channel.delete", "")
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/notification-channels/{id}/test: kayıtlı kanala deneme mesajı gönderir ve sonucu döndürür.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	kind, cfg, found := s.loadChannel(r, a, id)
	if !found {
		fail(w, 404, "kanal bulunamadı")
		return
	}
	m := notify.Message{Event: "test", Severity: "info", Subject: "APP_MON deneme bildirimi", Text: "Bu bir deneme mesajıdır. Bunu görüyorsanız kanal doğru çalışıyor.",
		Asset: "deneme", Rule: "Deneme", Value: "—", When: time.Now(), Link: s.publicLink()}
	if err := s.sendOne(r.Context(), id, kind, cfg, m); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- Bakım pencereleri ----

type maintReq struct {
	Name      string    `json:"name"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Repeat    string    `json:"repeat"`
	AssetIDs  []int64   `json:"asset_ids"`
	GroupName string    `json:"group"`
	Tag       string    `json:"tag"`
}

func (m *maintReq) check(w http.ResponseWriter) bool {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len(m.Name) > 200 || m.StartsAt.IsZero() || m.EndsAt.IsZero() || !m.EndsAt.After(m.StartsAt) || !oneOf(m.Repeat, "", "daily", "weekly") {
		fail(w, 400, "ad, başlangıç ve bitiş (bitiş başlangıçtan sonra) zorunlu; tekrar boş, daily veya weekly olmalı")
		return false
	}
	if m.Repeat == "daily" && m.EndsAt.Sub(m.StartsAt) >= 24*time.Hour || m.Repeat == "weekly" && m.EndsAt.Sub(m.StartsAt) >= 7*24*time.Hour {
		fail(w, 400, "tekrarlayan pencerenin süresi tekrar aralığından kısa olmalı")
		return false
	}
	if m.AssetIDs == nil {
		m.AssetIDs = []int64{}
	}
	return true
}

func (s *Server) listMaintenance(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, name, starts_at, ends_at, repeat, asset_ids, group_name, tag FROM maintenance_windows WHERE tenant_id = $1 ORDER BY starts_at DESC LIMIT 200`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, rep, grp, tag string
		var st, en time.Time
		var ids []int64
		if rows.Scan(&id, &name, &st, &en, &rep, pq.Array(&ids), &grp, &tag) != nil {
			continue
		}
		if ids == nil {
			ids = []int64{}
		}
		list = append(list, map[string]any{"id": id, "name": name, "starts_at": st, "ends_at": en, "repeat": rep, "asset_ids": ids, "group": grp, "tag": tag})
	}
	writeJSON(w, 200, list)
}

func (s *Server) createMaintenance(w http.ResponseWriter, r *http.Request, a Auth) {
	var req maintReq
	if !decode(w, r, &req) || !req.check(w) {
		return
	}
	var id int64
	if err := s.DB.QueryRowContext(r.Context(),
		`INSERT INTO maintenance_windows (tenant_id, name, starts_at, ends_at, repeat, asset_ids, group_name, tag) VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		a.TenantID, req.Name, req.StartsAt, req.EndsAt, req.Repeat, pq.Array(req.AssetIDs), req.GroupName, req.Tag).Scan(&id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "maintenance.create", req.Name)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) deleteMaintenance(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM maintenance_windows WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "pencere bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "maintenance.delete", "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- Alarm üstlenme ----

// POST /api/alerts/{id}/ack {"note": "..."}; DELETE aynı yol: üstlenmeyi kaldırır.
func (s *Server) ackAlert(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Note string `json:"note"`
	}
	if r.ContentLength != 0 && !decode(w, r, &req) {
		return
	}
	if len(req.Note) > 500 {
		fail(w, 400, "not en fazla 500 karakter olabilir")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `UPDATE alerts SET ack_by = $1, ack_at = now(), ack_note = $2 WHERE id = $3 AND tenant_id = $4 AND state = 'open'`, a.Label, req.Note, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "açık alarm bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "alert.ack", req.Note)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unackAlert(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	s.DB.ExecContext(r.Context(), `UPDATE alerts SET ack_by = '', ack_at = NULL, ack_note = '' WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	w.WriteHeader(http.StatusNoContent)
}

// ---- Asset grup ve etiketleri ----

// PUT /api/assets/{id}/meta {"group": "Üretim", "tags": ["web", "kritik"]}
func (s *Server) putAssetMeta(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Group string   `json:"group"`
		Tags  []string `json:"tags"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Group = strings.TrimSpace(req.Group)
	tags := []string{}
	seen := map[string]bool{}
	for _, t := range req.Tags {
		if t = strings.TrimSpace(t); t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	if len(req.Group) > 60 || len(tags) > 20 {
		fail(w, 400, "grup en fazla 60 karakter, en fazla 20 etiket olabilir")
		return
	}
	for _, t := range tags {
		if len(t) > 40 {
			fail(w, 400, "etiket en fazla 40 karakter olabilir")
			return
		}
	}
	var name string
	if s.DB.QueryRowContext(r.Context(), `UPDATE assets SET grp = $1, tags = $2 WHERE id = $3 AND tenant_id = $4 RETURNING name`, req.Group, pq.Array(tags), id, a.TenantID).Scan(&name) != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "asset.meta", name)
	w.WriteHeader(http.StatusNoContent)
}
