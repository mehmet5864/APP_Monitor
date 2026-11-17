package app

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Ajanın gönderdiği uygulama keşfi (v2.7.3): süreç/servis listesi ve otomatik günlük özeti.
// Gelen veri güvenilmez sayılır: sayı ve uzunluklar sınırlanır, kontrol karakterleri atılır.

type AgentApp struct {
	Name  string  `json:"name"`
	Kind  string  `json:"kind"`
	State string  `json:"state,omitempty"`
	Count int     `json:"count,omitempty"`
	CPU   float64 `json:"cpu_pct"`
	MemMB float64 `json:"mem_mb"`
	Ports []int   `json:"ports,omitempty"`
}

type AgentLog struct {
	App      string `json:"app"`
	Path     string `json:"path"`
	Readable bool   `json:"readable"`
	Sample   string `json:"sample,omitempty"`
}

type AgentApps struct {
	Apps []AgentApp `json:"apps"`
	Logs []AgentLog `json:"logs"`
}

const (
	maxAgentApps = 160
	maxAgentLogs = 20
)

func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func finite(v float64) float64 {
	if v != v || v > 1e9 || v < 0 { // NaN, aşırı veya negatif
		return 0
	}
	return v
}

// sanitize listeleri sınırlayıp JSON olarak döndürür (apps, logs).
func (a *AgentApps) sanitize() (string, string) {
	apps := make([]AgentApp, 0, len(a.Apps))
	for _, x := range a.Apps {
		if len(apps) >= maxAgentApps {
			break
		}
		x.Name = cleanText(x.Name, 80)
		if x.Name == "" {
			continue
		}
		if x.Kind != "service" {
			x.Kind = "process"
		}
		x.State = cleanText(x.State, 20)
		if x.Count < 0 || x.Count > 100000 {
			x.Count = 0
		}
		x.CPU, x.MemMB = finite(x.CPU), finite(x.MemMB)
		ports := make([]int, 0, len(x.Ports))
		for _, p := range x.Ports {
			if p > 0 && p < 65536 && len(ports) < 12 {
				ports = append(ports, p)
			}
		}
		x.Ports = ports
		apps = append(apps, x)
	}
	logs := make([]AgentLog, 0, len(a.Logs))
	for _, l := range a.Logs {
		if len(logs) >= maxAgentLogs {
			break
		}
		l.App, l.Path, l.Sample = cleanText(l.App, 60), cleanText(l.Path, 200), cleanText(l.Sample, 260)
		if l.App == "" {
			continue
		}
		logs = append(logs, l)
	}
	ab, _ := json.Marshal(apps)
	lb, _ := json.Marshal(logs)
	return string(ab), string(lb)
}

// GET /api/assets/{id}/apps - ajanın bildirdiği uygulamalar, servisler ve günlük özeti (son 1 saatlik hata/uyarı sayısı).
func (s *Server) assetApps(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var name string
	if s.DB.QueryRowContext(r.Context(), `SELECT name FROM assets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&name) != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	out := map[string]any{"asset": name, "apps": []AgentApp{}, "logs": []map[string]any{}}
	var apps, logs []byte
	var at time.Time
	if err := s.DB.QueryRowContext(r.Context(), `SELECT apps, logs, updated_at FROM asset_apps WHERE asset_id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&apps, &logs, &at); err != nil {
		writeJSON(w, 200, out) // ajan henüz uygulama listesi göndermedi
		return
	}
	var al []AgentApp
	var ll []AgentLog
	json.Unmarshal(apps, &al)
	json.Unmarshal(logs, &ll)
	sort.SliceStable(al, func(i, j int) bool {
		if (al[i].Kind == "service") != (al[j].Kind == "service") {
			return al[i].Kind != "service"
		}
		return al[i].MemMB+al[i].CPU*10 > al[j].MemMB+al[j].CPU*10
	})
	// Son 1 saatte günlüklerde sayılan hata ve uyarı satırları (ajan her turda log.auto_<uygulama>.count/.warn gönderir).
	sums := map[string]float64{}
	if rows, err := s.DB.QueryContext(r.Context(),
		`SELECT name, sum(value) FROM metrics WHERE tenant_id = $1 AND asset_id = $2 AND time > now() - interval '1 hour' AND name LIKE 'log.auto\_%'
		 AND (name LIKE '%.count' OR name LIKE '%.warn') GROUP BY name`, a.TenantID, id); err == nil {
		for rows.Next() {
			var n string
			var v float64
			if rows.Scan(&n, &v) == nil {
				sums[n] = v
			}
		}
		rows.Close()
	}
	lg := make([]map[string]any, 0, len(ll))
	for _, l := range ll {
		pre := "log.auto_" + strings.TrimPrefix(strings.TrimSpace(l.App), "auto_")
		lg = append(lg, map[string]any{"app": strings.TrimPrefix(l.App, "auto_"), "path": l.Path, "readable": l.Readable, "sample": l.Sample,
			"errors_1h": int64(sums[pre+".count"]), "warns_1h": int64(sums[pre+".warn"])})
	}
	out["apps"], out["logs"], out["updated_at"] = al, lg, at
	writeJSON(w, 200, out)
}
