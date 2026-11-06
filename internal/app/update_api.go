package app

import (
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"appmon/internal/update"
	"appmon/internal/version"
)

func (s *Server) updateSupport() (bool, string) {
	switch {
	case s.UpdateDir == "":
		return false, "Bu kurulumda arayüzden güncelleme kapalı (Docker kurulumunda .\\guncelle-docker.bat kullanın; Linux/RHEL kurulumunda güncelleme paketindeki guncelle-apache.sh betiğini bir kez çalıştırarak etkinleştirin)."
	case s.UpdatePubBad:
		return false, "Güncelleme imza anahtarı (APM_UPDATE_PUBKEY) okunamadı; güvenlik için güncelleme kapalı."
	case !update.Ready(s.UpdateDir):
		return false, "Güncelleme hizmeti henüz kurulu değil: güncelleme paketindeki guncelle-apache.sh betiğini sunucuda bir kez çalıştırın."
	}
	return true, ""
}

// GET /api/system/update
func (s *Server) updateInfo(w http.ResponseWriter, r *http.Request, a Auth) {
	ok, why := s.updateSupport()
	out := map[string]any{"supported": ok, "reason": why, "version": version.Version, "arch": runtime.GOARCH, "signature_required": s.UpdatePub != nil}
	if ok {
		if st, err := update.Load(s.UpdateDir); err == nil && st != nil {
			out["staged"] = st
		}
		out["pending"] = update.Pending(s.UpdateDir)
		if stt := update.ReadStatus(s.UpdateDir); stt != nil {
			out["status"] = stt
		}
	}
	writeJSON(w, 200, out)
}

// POST /api/system/update  — gövde: .tar.gz paketin kendisi (ham yükleme)
func (s *Server) updateUpload(w http.ResponseWriter, r *http.Request, a Auth) {
	if ok, why := s.updateSupport(); !ok {
		fail(w, 409, why)
		return
	}
	if update.Pending(s.UpdateDir) {
		fail(w, 409, "bir güncelleme zaten işleniyor")
		return
	}
	// Büyük yüklemeler için sunucunun genel okuma süresini uzat.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(10 * time.Minute))
	_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))
	st, err := update.Stage(r.Body, s.UpdateDir, runtime.GOARCH, s.UpdatePub, version.Version)
	if err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			fail(w, 413, "paket çok büyük")
			return
		}
		fail(w, 400, err.Error())
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "system.update.stage", fmt.Sprintf("%s -> %s", version.Version, st.Version))
	writeJSON(w, 201, map[string]any{"staged": st, "version": version.Version})
}

// POST /api/system/update/apply  {"id": "..."}
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request, a Auth) {
	if ok, why := s.updateSupport(); !ok {
		fail(w, 409, why)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &req) {
		return
	}
	st, err := update.Load(s.UpdateDir)
	if err != nil || st == nil || st.ID != req.ID {
		fail(w, 404, "hazırlanmış paket bulunamadı; paketi yeniden yükleyin")
		return
	}
	if err := update.RequestApply(s.UpdateDir, req.ID, a.Label); err != nil {
		fail(w, 409, err.Error())
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "system.update.apply", fmt.Sprintf("%s -> %s", version.Version, st.Version))
	writeJSON(w, 202, map[string]any{"ok": true, "message": "Güncelleme başlatıldı; servis yeniden başlayacak."})
}

// DELETE /api/system/update  — hazırlanmış paketi iptal eder
func (s *Server) updateCancel(w http.ResponseWriter, r *http.Request, a Auth) {
	if ok, why := s.updateSupport(); !ok {
		fail(w, 409, why)
		return
	}
	if update.Pending(s.UpdateDir) {
		fail(w, 409, "güncelleme başlatıldı, iptal edilemez")
		return
	}
	update.Clear(s.UpdateDir)
	w.WriteHeader(http.StatusNoContent)
}
