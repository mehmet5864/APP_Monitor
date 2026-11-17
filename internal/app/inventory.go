package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Saldırı yüzeyi envanteri: ajanın gönderdiği dinleyen portlar (kapsam, süreç, çalışma zamanı, sürüm) ve uygulama
// bağımlılıkları. Gelen veri güvenilmez sayılır: sayılar ve uzunluklar sınırlanır, ekosistemler beyaz listeyle doğrulanır.

type InvListener struct {
	Port      int    `json:"port"`
	Scope     string `json:"scope"`
	Proc      string `json:"proc"`
	Runtime   string `json:"runtime,omitempty"`
	Version   string `json:"version,omitempty"`
	App       string `json:"app,omitempty"`
	Container bool   `json:"container,omitempty"`
}

type InvDep struct {
	Eco     string `json:"eco"`
	Name    string `json:"name"`
	Version string `json:"version"`
	App     string `json:"app"`
	Src     string `json:"src,omitempty"`
}

type AgentInventory struct {
	Listeners []InvListener `json:"listeners"`
	Deps      []InvDep      `json:"deps"`
}

const (
	maxInvListenersSrv = 200
	maxInvDepsSrv      = 2500
)

func (inv *AgentInventory) sanitize() {
	ls := make([]InvListener, 0, len(inv.Listeners))
	for _, l := range inv.Listeners {
		if l.Port < 1 || l.Port > 65535 || len(ls) >= maxInvListenersSrv {
			continue
		}
		if l.Scope != "net" && l.Scope != "local" {
			l.Scope = "net"
		}
		l.Proc, l.Runtime, l.Version, l.App = cleanText(l.Proc, 40), cleanText(l.Runtime, 20), cleanText(l.Version, 40), cleanText(l.App, 60)
		ls = append(ls, l)
	}
	ds := make([]InvDep, 0, len(inv.Deps))
	for _, d := range inv.Deps {
		if !advEcos[d.Eco] || d.Name == "" || d.Version == "" || len(ds) >= maxInvDepsSrv {
			continue
		}
		d.Name, d.Version, d.App = cleanText(d.Name, 200), cleanText(d.Version, 60), cleanText(d.App, 60)
		if d.Src != "lock" && d.Src != "manifest" && d.Src != "jar" {
			d.Src = ""
		}
		ds = append(ds, d)
	}
	inv.Listeners, inv.Deps = ls, ds
}

func storeInventory(ctx context.Context, tx *sql.Tx, tenant, asset int64, inv *AgentInventory) error {
	lb, _ := json.Marshal(inv.Listeners)
	db, _ := json.Marshal(inv.Deps)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO asset_inventory (asset_id, tenant_id, listeners, deps, updated_at) VALUES ($1, $2, $3::jsonb, $4::jsonb, now())
		 ON CONFLICT (asset_id) DO UPDATE SET listeners = EXCLUDED.listeners, deps = EXCLUDED.deps, updated_at = now()`,
		asset, tenant, string(lb), string(db))
	return err
}

// surfaceFromInventory ağdan erişilebilir dinleyen portları saldırı yüzeyi referansına işler (yalnızca loopback olanlar sayılmaz).
func (s *Server) surfaceFromInventory(ctx context.Context, tenant, asset int64, inv *AgentInventory) {
	items := []string{}
	det := map[string]string{}
	for _, l := range inv.Listeners {
		if l.Scope != "net" {
			continue
		}
		k := strconv.Itoa(l.Port)
		items = append(items, k)
		if n, ok := riskyPorts[k]; ok {
			det[k] = n
		} else if l.Proc != "" {
			det[k] = l.Proc
		}
	}
	s.surfaceObserve(ctx, tenant, asset, "port", items, det, surfOpts{removeAfter: 3})
}

type invVuln struct {
	Eco      string   `json:"eco"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	App      string   `json:"app"`
	Src      string   `json:"src,omitempty"`
	ID       string   `json:"id"`
	Aliases  []string `json:"aliases,omitempty"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary,omitempty"`
	Fixed    string   `json:"fixed,omitempty"`
}

type invListenerOut struct {
	InvListener
	EOL eolInfo `json:"eol"`
}

type invView struct {
	Listeners []invListenerOut
	Vulns     []invVuln
	EOLCount  int
	Crit      int
	High      int
}

var vulnRank = map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3, "unknown": 4}

// inventoryView dinleyen servislere EOL durumunu ekler ve bağımlılıkları zafiyet kayıtlarıyla eşler.
func (s *Server) inventoryView(listeners []InvListener, deps []InvDep) invView {
	now := time.Now()
	v := invView{Listeners: []invListenerOut{}, Vulns: []invVuln{}}
	for _, l := range listeners {
		o := invListenerOut{InvListener: l, EOL: eolInfo{Status: "unknown"}}
		if l.Runtime != "" && l.Version != "" {
			o.EOL = s.eolLookup(l.Runtime, l.Version, now)
		}
		if o.EOL.Status == "eol" {
			v.EOLCount++
		}
		v.Listeners = append(v.Listeners, o)
	}
	seen := map[string]bool{}
	addVulns := func(eco, name, ver, app, src string) {
		for _, m := range s.advMatch(eco, name, ver) {
			k := app + "|" + name + "|" + ver + "|" + m.ID
			if seen[k] {
				continue
			}
			seen[k] = true
			v.Vulns = append(v.Vulns, invVuln{Eco: eco, Name: name, Version: ver, App: app, Src: src, ID: m.ID, Aliases: m.Aliases, Severity: m.Severity, Summary: m.Summary, Fixed: m.Fixed})
			switch m.Severity {
			case "critical":
				v.Crit++
			case "high":
				v.High++
			}
		}
	}
	for _, d := range deps {
		addVulns(d.Eco, d.Name, d.Version, d.App, d.Src)
	}
	for _, l := range listeners { // Go ikilisinin standart kütüphanesi
		if l.Runtime == "go" && l.Version != "" {
			addVulns("Go", "stdlib", l.Version, l.App, "lock")
		}
	}
	sort.SliceStable(v.Vulns, func(i, j int) bool { return vulnRank[v.Vulns[i].Severity] < vulnRank[v.Vulns[j].Severity] })
	return v
}

// inventoryMetrics envanterden alarm kurallarında kullanılabilecek sayıları üretir.
func inventoryMetrics(v invView, at time.Time) []point {
	return []point{{"inventory.eol_count", float64(v.EOLCount), at}, {"inventory.vuln_critical", float64(v.Crit), at}, {"inventory.vuln_high", float64(v.High), at}}
}

// GET /api/inventory -> asset başına dinleyen servisler, EOL durumu, bağımlılık sayısı ve eşleşen zafiyetler.
func (s *Server) inventoryList(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT i.asset_id, a.name, i.listeners, i.deps, i.updated_at FROM asset_inventory i JOIN assets a ON a.id = i.asset_id
		 WHERE i.tenant_id = $1 ORDER BY a.name LIMIT 500`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	tot := map[string]int{"assets": 0, "listeners": 0, "exposed": 0, "eol": 0, "deps": 0, "critical": 0, "high": 0}
	for rows.Next() {
		var id int64
		var name string
		var lb, db []byte
		var at time.Time
		if rows.Scan(&id, &name, &lb, &db, &at) != nil {
			continue
		}
		var ls []InvListener
		var ds []InvDep
		json.Unmarshal(lb, &ls)
		json.Unmarshal(db, &ds)
		v := s.inventoryView(ls, ds)
		vulns := v.Vulns
		if len(vulns) > 200 {
			vulns = vulns[:200]
		}
		exposed := 0
		for _, l := range ls {
			if l.Scope == "net" {
				exposed++
			}
		}
		tot["assets"]++
		tot["listeners"] += len(ls)
		tot["exposed"] += exposed
		tot["eol"] += v.EOLCount
		tot["deps"] += len(ds)
		tot["critical"] += v.Crit
		tot["high"] += v.High
		list = append(list, map[string]any{"asset_id": id, "asset": name, "updated_at": at, "listeners": v.Listeners, "deps_count": len(ds),
			"vulns": vulns, "vuln_total": len(v.Vulns), "eol_count": v.EOLCount, "critical": v.Crit, "high": v.High})
	}
	writeJSON(w, 200, map[string]any{"assets": list, "totals": tot, "advisory": s.advInfo()})
}

// GET /api/inventory/{id}/deps -> asset'in bağımlılıkları (zafiyet kimlikleriyle).
func (s *Server) inventoryDeps(w http.ResponseWriter, r *http.Request, a Auth) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "geçersiz kimlik")
		return
	}
	var db []byte
	var at time.Time
	if err := s.DB.QueryRowContext(r.Context(), `SELECT deps, updated_at FROM asset_inventory WHERE asset_id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&db, &at); err != nil {
		writeJSON(w, 200, map[string]any{"deps": []any{}})
		return
	}
	var ds []InvDep
	json.Unmarshal(db, &ds)
	out := make([]map[string]any, 0, len(ds))
	for _, d := range ds {
		ids := []string{}
		for _, m := range s.advMatch(d.Eco, d.Name, d.Version) {
			ids = append(ids, m.ID)
		}
		out = append(out, map[string]any{"eco": d.Eco, "name": d.Name, "version": d.Version, "app": d.App, "src": d.Src, "vulns": ids})
	}
	writeJSON(w, 200, map[string]any{"deps": out, "updated_at": at})
}

// POST /api/inventory/advisories/reload (yönetici) -> zafiyet kayıtlarını dizinden yeniden yükler (otomatik indirme açıksa önce indirir).
func (s *Server) inventoryReload(w http.ResponseWriter, r *http.Request, a Auth) {
	if s.AdvisoryDir == "" {
		fail(w, 400, "zafiyet kayıt dizini tanımlı değil (APM_ADVISORY_DIR)")
		return
	}
	go func() {
		if s.AdvisoryAuto {
			s.downloadAdvisories(context.Background())
		}
		s.loadAdvisories()
	}()
	s.audit(r.Context(), a.TenantID, a.Label, "inventory.advisories_reload", "")
	writeJSON(w, 202, s.advInfo())
}
