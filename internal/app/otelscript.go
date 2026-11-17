package app

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
)

var hostRe = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]{1,255}$`)

// baseURL betik ve belgelerde gösterilecek dış adresi döndürür (APM_PUBLIC_URL, yoksa isteğin adresi).
func (s *Server) baseURL(r *http.Request) string {
	if s.PublicURL != "" {
		return s.PublicURL
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if !hostRe.MatchString(host) {
		host = "APPMON-SUNUCUSU"
	}
	return scheme + "://" + host
}

// otelScript OpenTelemetry kurulum betiklerini sunucu adresi gömülü olarak verir.
// Betikler sır içermez (anahtar parametre olarak verilir), bu yüzden kimlik doğrulaması gerekmez.
func (s *Server) otelScript(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.Static, "kurulum/"+name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		out := strings.ReplaceAll(string(b), "__APPMON_URL__", s.baseURL(r))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(out))
	}
}
