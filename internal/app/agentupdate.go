package app

import (
	"appmon/internal/agentver"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

// Uzaktan ajan güncelleme (v2.7.4): ajan heartbeat'inde kendi SHA-256'sını bildirir (labels.agent_sha). Sunucudaki ajan dosyası
// (APM_AGENT_DIR) farklıysa ve yönetici "güncelle" dediyse (veya kiracıda otomatik güncelleme açıksa) yanıta "update" eklenir;
// ajan dosyayı /agent/dl/ adresinden indirir, SHA-256'yı doğrular, kendini değiştirir ve yeniden başlar.

type agentBin struct {
	Name   string
	SHA256 string
	Size   int64
}

type binCacheEntry struct {
	mtime int64
	size  int64
	bin   agentBin
}

var (
	binCacheMu sync.Mutex
	binCache   = map[string]binCacheEntry{}
)

// agentBinary sunucudaki ajan dosyasını (os/arch için) ve SHA-256'sını verir; yoksa ok=false.
func (s *Server) agentBinary(goos, arch string) (agentBin, bool) {
	if s.AgentDir == "" || (goos != "linux" && goos != "windows") || (arch != "amd64" && arch != "arm64") {
		return agentBin{}, false
	}
	name := "appmon-agent-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	p := filepath.Join(s.AgentDir, name)
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return agentBin{}, false
	}
	binCacheMu.Lock()
	defer binCacheMu.Unlock()
	if c, ok := binCache[p]; ok && c.mtime == st.ModTime().UnixNano() && c.size == st.Size() {
		return c.bin, true
	}
	f, err := os.Open(p)
	if err != nil {
		return agentBin{}, false
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return agentBin{}, false
	}
	b := agentBin{Name: name, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
	binCache[p] = binCacheEntry{st.ModTime().UnixNano(), st.Size(), b}
	return b, true
}

// updateOffer, ajana gönderilecek güncelleme teklifini üretir (yoksa nil).
func (s *Server) updateOffer(labels map[string]string, requested, auto bool) map[string]any {
	cur := labels["agent_sha"]
	if cur == "" || !(requested || auto) {
		return nil
	}
	b, ok := s.agentBinary(labels["os"], labels["arch"])
	if !ok || b.SHA256 == cur {
		return nil
	}
	return map[string]any{"name": b.Name, "sha256": b.SHA256, "size": b.Size}
}

type agentUpdateRow struct {
	AssetID   int64  `json:"asset_id"`
	Name      string `json:"name"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	Version   string `json:"version"`
	Supported bool   `json:"supported"` // ajan uzaktan güncellemeyi biliyor (agent_sha bildiriyor)
	Current   bool   `json:"current"`   // sunucudaki dosyayla aynı
	Available bool   `json:"available"` // sunucuda farklı (yeni) dosya var
	Requested bool   `json:"requested"`
	Error     string `json:"error,omitempty"`
}

// GET /api/agent/updates (yönetici)
func (s *Server) agentUpdates(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT id, name, labels, agent_update_req FROM assets WHERE tenant_id = $1 AND collection = 'agent' ORDER BY name`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	out := []agentUpdateRow{}
	for rows.Next() {
		var row agentUpdateRow
		var raw []byte
		if err := rows.Scan(&row.AssetID, &row.Name, &raw, &row.Requested); err != nil {
			continue
		}
		l := map[string]string{}
		_ = json.Unmarshal(raw, &l)
		row.OS, row.Arch, row.Version, row.Error = l["os"], l["arch"], l["agent_version"], l["update_error"]
		row.Supported = l["agent_sha"] != ""
		if b, ok := s.agentBinary(l["os"], l["arch"]); ok && row.Supported {
			row.Current = b.SHA256 == l["agent_sha"]
			row.Available = !row.Current
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	var auto bool
	_ = s.DB.QueryRowContext(r.Context(), `SELECT agent_auto_update FROM tenants WHERE id = $1`, a.TenantID).Scan(&auto)
	writeJSON(w, 200, map[string]any{"enabled": s.AgentDir != "", "auto": auto, "agents": out, "bundled_version": agentver.Agent})
}

// POST /api/agent/update (yönetici): {"asset_ids":[…]} veya {"all":true}; "cancel":true isteği geri alır.
func (s *Server) agentUpdateRequest(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		AssetIDs []int64 `json:"asset_ids"`
		All      bool    `json:"all"`
		Cancel   bool    `json:"cancel"`
	}
	if !decode(w, r, &req) {
		return
	}
	if s.AgentDir == "" {
		fail(w, 400, "bu sunucuda ajan dosyası yok (APM_AGENT_DIR); sunucu paketiyle gelir")
		return
	}
	if !req.All && len(req.AssetIDs) == 0 {
		fail(w, 400, "asset_ids veya all gerekli")
		return
	}
	if len(req.AssetIDs) > 2000 {
		fail(w, 400, "en fazla 2000 asset")
		return
	}
	val := !req.Cancel
	var n int64
	if req.All {
		// Yalnızca güncellemeyi destekleyen (agent_sha bildiren) ajanlar.
		res, err := s.DB.ExecContext(r.Context(),
			`UPDATE assets SET agent_update_req = $2 WHERE tenant_id = $1 AND collection = 'agent' AND labels ? 'agent_sha'`, a.TenantID, val)
		if err != nil {
			fail(w, 500, "veritabanı hatası")
			return
		}
		n, _ = res.RowsAffected()
	} else {
		for _, id := range req.AssetIDs {
			res, err := s.DB.ExecContext(r.Context(),
				`UPDATE assets SET agent_update_req = $3 WHERE id = $1 AND tenant_id = $2 AND collection = 'agent' AND labels ? 'agent_sha'`, id, a.TenantID, val)
			if err == nil {
				k, _ := res.RowsAffected()
				n += k
			}
		}
	}
	act := "agent.update_request"
	if req.Cancel {
		act = "agent.update_cancel"
	}
	s.audit(r.Context(), a.TenantID, a.Label, act, strconv.FormatInt(n, 10)+" ajan")
	writeJSON(w, 200, map[string]any{"updated": n})
}

// PUT /api/agent/auto-update (yönetici): {"enabled":bool}
func (s *Server) agentAutoUpdate(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE tenants SET agent_auto_update = $2 WHERE id = $1`, a.TenantID, req.Enabled); err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	d := "kapalı"
	if req.Enabled {
		d = "açık"
	}
	s.audit(r.Context(), a.TenantID, a.Label, "agent.auto_update", d)
	writeJSON(w, 200, map[string]any{"enabled": req.Enabled})
}
