package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"
)

var errPaused = errors.New("izleme duraklatılmış; önce Genel bakıştan izlemeyi başlatın")

// monitoringState kiracının izleme durumunu döndürür.
func (s *Server) monitoringState(ctx context.Context, tenant int64) map[string]any {
	var paused bool
	var at sql.NullTime
	var by string
	if s.DB != nil {
		s.DB.QueryRowContext(ctx, `SELECT paused, paused_at, paused_by FROM tenants WHERE id = $1`, tenant).Scan(&paused, &at, &by)
	}
	out := map[string]any{"paused": paused}
	if paused && at.Valid {
		out["since"], out["by"] = at.Time, by
	}
	return out
}

// isPaused izleme duraklatılmışsa true döner (veritabanı yoksa false).
func (s *Server) isPaused(ctx context.Context, tenant int64) bool {
	return s.DB != nil && s.monitoringState(ctx, tenant)["paused"] == true
}

// GET /api/monitoring
func (s *Server) getMonitoring(w http.ResponseWriter, r *http.Request, a Auth) {
	out := s.monitoringState(r.Context(), a.TenantID)
	var pr, sc int
	s.DB.QueryRowContext(r.Context(), `SELECT (SELECT count(*) FROM probes WHERE tenant_id = $1 AND enabled), (SELECT count(*) FROM scenarios WHERE tenant_id = $1 AND enabled)`, a.TenantID).Scan(&pr, &sc)
	out["probes_enabled"], out["scenarios_enabled"] = pr, sc
	writeJSON(w, 200, out)
}

// PUT /api/monitoring {paused: bool} — tek tek probe/senaryo ayarlarına dokunmaz; kapatınca eski hâliyle devam eder.
func (s *Server) setMonitoring(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		Paused   *bool  `json:"paused"`
		Password string `json:"password"`
		APIKey   string `json:"api_key"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Paused == nil {
		fail(w, 400, "paused alanı gerekli")
		return
	}
	if code, err := s.reauth(r, a, req.Password, req.APIKey); err != nil {
		fail(w, code, err.Error())
		return
	}
	var err error
	if *req.Paused {
		_, err = s.DB.ExecContext(r.Context(), `UPDATE tenants SET paused = true, paused_at = $2, paused_by = $3 WHERE id = $1`, a.TenantID, time.Now(), a.Label)
	} else {
		_, err = s.DB.ExecContext(r.Context(), `UPDATE tenants SET paused = false, paused_at = NULL, paused_by = '' WHERE id = $1`, a.TenantID)
		// duraklatma süresince biriken vadesi geçmiş işler bir anda çalışmasın diye zamanlayıcıyı yeniden başlat
		s.DB.ExecContext(r.Context(), `UPDATE probes SET last_run = now() WHERE tenant_id = $1 AND enabled`, a.TenantID)
		s.DB.ExecContext(r.Context(), `UPDATE scenarios SET last_run = now() WHERE tenant_id = $1 AND enabled`, a.TenantID)
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	act := "monitoring.resume"
	if *req.Paused {
		act = "monitoring.pause"
	}
	s.audit(r.Context(), a.TenantID, a.Label, act, "")
	s.getMonitoring(w, r, a)
}
