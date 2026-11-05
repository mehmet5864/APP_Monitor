package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"appmon/internal/siem"
)

func (s *Server) siemEnv() siem.Env { return siem.Env{Guard: probeGuard(s.BlockPrivate)} }

// siemEmit olayı etkin SIEM hedeflerine arka planda iletir.
func (s *Server) siemEmit(tenant int64, e siem.Event) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		rows, err := s.DB.QueryContext(ctx, `SELECT id, config FROM siem_targets WHERE tenant_id = $1 AND enabled`, tenant)
		if err != nil {
			return
		}
		type tg struct {
			id  int64
			cfg siem.Config
		}
		var list []tg
		for rows.Next() {
			var t tg
			var raw []byte
			if rows.Scan(&t.id, &raw) == nil && json.Unmarshal(raw, &t.cfg) == nil {
				list = append(list, t)
			}
		}
		rows.Close()
		for _, t := range list {
			if t.cfg.Wanted(e) {
				s.siemSend(ctx, t.id, t.cfg, e)
			}
		}
	}()
}

func (s *Server) siemSend(ctx context.Context, id int64, cfg siem.Config, e siem.Event) error {
	err := siem.Send(ctx, cfg, e, s.siemEnv())
	if err == nil {
		s.DB.ExecContext(ctx, `UPDATE siem_targets SET last_error = '', last_sent = now() WHERE id = $1`, id)
		return nil
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300]
	}
	log.Printf("SIEM hedefi #%d başarısız: %v", id, err)
	s.DB.ExecContext(ctx, `UPDATE siem_targets SET last_error = $2 WHERE id = $1`, id, msg)
	return err
}

// siemAlert alarm açılış/kapanışını SIEM'e iletir (bakımda olsa da; detayda belirtilir).
func (s *Server) siemAlert(tenant int64, event string, asset int64, rule, sev string, val, thr float64, cmp string, maint bool) {
	var name string
	if s.DB.QueryRow(`SELECT name FROM assets WHERE id = $1`, asset).Scan(&name) != nil {
		return
	}
	detail := fmt.Sprintf("değer %s (%s %s)", strconv.FormatFloat(val, 'f', -1, 64), cmp, strconv.FormatFloat(thr, 'f', -1, 64))
	if maint {
		detail += "; bakım penceresinde"
	}
	s.siemEmit(tenant, siem.Event{Type: "alert", Severity: sev, Action: event, Asset: name, Rule: rule, Value: detail, Link: s.publicLink()})
}

func (s *Server) listSIEM(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, name, config, enabled, last_error, last_sent FROM siem_targets WHERE tenant_id = $1 ORDER BY id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, lastErr string
		var raw []byte
		var enabled bool
		var sent *time.Time
		if rows.Scan(&id, &name, &raw, &enabled, &lastErr, &sent) != nil {
			continue
		}
		var cfg siem.Config
		json.Unmarshal(raw, &cfg)
		red, has := cfg.Redacted()
		it := map[string]any{"id": id, "name": name, "config": red, "has": has, "enabled": enabled, "last_error": lastErr}
		if sent != nil {
			it["last_sent"] = *sent
		}
		list = append(list, it)
	}
	writeJSON(w, 200, list)
}

type siemReq struct {
	Name    string      `json:"name"`
	Config  siem.Config `json:"config"`
	Enabled *bool       `json:"enabled"`
}

func (q *siemReq) check(w http.ResponseWriter) bool {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" || len(q.Name) > 100 {
		fail(w, 400, "ad zorunlu (en çok 100 karakter)")
		return false
	}
	if err := siem.Validate(&q.Config); err != nil {
		fail(w, 400, err.Error())
		return false
	}
	return true
}

func (s *Server) createSIEM(w http.ResponseWriter, r *http.Request, a Auth) {
	var req siemReq
	if !decode(w, r, &req) || !req.check(w) {
		return
	}
	cj, _ := json.Marshal(req.Config)
	var id int64
	if err := s.DB.QueryRowContext(r.Context(), `INSERT INTO siem_targets (tenant_id, name, config, enabled) VALUES ($1,$2,$3,$4) RETURNING id`,
		a.TenantID, req.Name, string(cj), req.Enabled == nil || *req.Enabled).Scan(&id); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "siem.create", req.Config.Protocol+" "+req.Name)
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) loadSIEM(r *http.Request, a Auth, id int64) (cfg siem.Config, ok bool) {
	var raw []byte
	if s.DB.QueryRowContext(r.Context(), `SELECT config FROM siem_targets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&raw) != nil {
		return cfg, false
	}
	json.Unmarshal(raw, &cfg)
	return cfg, true
}

func (s *Server) updateSIEM(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req siemReq
	if !decode(w, r, &req) {
		return
	}
	old, found := s.loadSIEM(r, a, id)
	if !found {
		fail(w, 404, "hedef bulunamadı")
		return
	}
	req.Config.Keep(old)
	if !req.check(w) {
		return
	}
	cj, _ := json.Marshal(req.Config)
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE siem_targets SET name=$1, config=$2, enabled=$3 WHERE id=$4 AND tenant_id=$5`,
		req.Name, string(cj), req.Enabled == nil || *req.Enabled, id, a.TenantID); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "siem.update", req.Config.Protocol+" "+req.Name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteSIEM(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM siem_targets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "hedef bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "siem.delete", "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testSIEM(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	cfg, found := s.loadSIEM(r, a, id)
	if !found {
		fail(w, 404, "hedef bulunamadı")
		return
	}
	e := siem.Event{Type: "test", Severity: "info", Actor: a.Label, Action: "test", Detail: "APP_MON SIEM deneme olayı", Link: s.publicLink()}
	if err := s.siemSend(r.Context(), id, cfg, e); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
