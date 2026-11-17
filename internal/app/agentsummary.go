package app

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
)

// GET /api/agent/summary (yönetici): 500 ajanlık filoda tek bakışta durum — sürüm dağılımı, güncelleme ve veri ayarı özeti (E1).
func (s *Server) agentSummary(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT a.status, a.labels, a.agent_update_req, COALESCE(c.version, 0)
		   FROM assets a LEFT JOIN agent_config c ON c.asset_id = a.id
		  WHERE a.tenant_id = $1 AND a.collection = 'agent'`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	byStatus := map[string]int{}
	versions := map[string]int{}
	upd := map[string]int{"current": 0, "available": 0, "requested": 0, "unsupported": 0, "errors": 0}
	cfg := map[string]int{"remote_applied": 0, "remote_pending": 0, "locked": 0, "local": 0, "unsupported": 0}
	total := 0
	for rows.Next() {
		var status string
		var raw []byte
		var requested bool
		var cver int
		if rows.Scan(&status, &raw, &requested, &cver) != nil {
			continue
		}
		total++
		l := map[string]string{}
		_ = json.Unmarshal(raw, &l)
		byStatus[status]++
		v := l["agent_version"]
		if v == "" {
			v = "eski"
		}
		versions[v]++
		switch {
		case l["agent_sha"] == "":
			upd["unsupported"]++
		default:
			if b, ok := s.agentBinary(l["os"], l["arch"]); ok && b.SHA256 == l["agent_sha"] {
				upd["current"]++
			} else if ok {
				upd["available"]++
			}
		}
		if requested {
			upd["requested"]++
		}
		if l["update_error"] != "" {
			upd["errors"]++
		}
		_, sup := l["cfg_send"]
		switch {
		case !sup:
			cfg["unsupported"]++
		case l["cfg_lock"] == "1":
			cfg["locked"]++
		case cver > 0 && l["cfg_v"] == strconv.Itoa(cver):
			cfg["remote_applied"]++
		case cver > 0:
			cfg["remote_pending"]++
		default:
			cfg["local"]++
		}
	}
	type vc struct {
		Version string `json:"version"`
		Count   int    `json:"count"`
	}
	vs := make([]vc, 0, len(versions))
	for k, n := range versions {
		vs = append(vs, vc{k, n})
	}
	sort.Slice(vs, func(i, j int) bool {
		return vs[i].Count > vs[j].Count || vs[i].Count == vs[j].Count && vs[i].Version > vs[j].Version
	})
	writeJSON(w, 200, map[string]any{
		"total": total, "by_status": byStatus, "versions": vs, "update": upd, "config": cfg,
		"offline_after_sec": s.offlineAgentSec(), "warmup_left_sec": int(s.warmupLeft().Seconds()),
	})
}
