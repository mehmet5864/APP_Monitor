package app

import (
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/lib/pq"
)

// rootCauses sağlıksız (down/offline) asset'ler arasından, doğrudan veya dolaylı bağımlılıklarının
// hiçbiri sağlıksız olmayanları döndürür: zincirin en altındaki, olası kök nedenler.
// deps[a] = a'nın bağımlı olduğu asset'ler.
func rootCauses(status map[int64]string, deps map[int64][]int64) []int64 {
	unhealthy := func(id int64) bool { s := status[id]; return s == "down" || s == "offline" }
	out := []int64{}
	for id := range status {
		if unhealthy(id) && !hasUnhealthyDependency(id, deps, unhealthy) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func hasUnhealthyDependency(start int64, deps map[int64][]int64, unhealthy func(int64) bool) bool {
	seen := map[int64]bool{start: true}
	stack := append([]int64(nil), deps[start]...)
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[n] {
			continue
		}
		seen[n] = true
		if unhealthy(n) {
			return true
		}
		stack = append(stack, deps[n]...)
	}
	return false
}

// impactedBy sağlıklı görünen her asset için, doğrudan veya dolaylı olarak bağımlı olduğu (üzerinde çalıştığı
// sunucu, kullandığı veritabanı vb.) çalışmayan asset'leri döndürür. Yalnızca etkilenenler yer alır;
// kendisi down/offline olanlar zaten kırmızıdır. deps[a] = a'nın bağımlı olduğu asset'ler.
func impactedBy(status map[int64]string, deps map[int64][]int64) map[int64][]int64 {
	unhealthy := func(id int64) bool { s := status[id]; return s == "down" || s == "offline" }
	out := map[int64][]int64{}
	for id := range status {
		if unhealthy(id) {
			continue
		}
		seen := map[int64]bool{id: true}
		stack := append([]int64(nil), deps[id]...)
		var bad []int64
		for len(stack) > 0 {
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[n] {
				continue
			}
			seen[n] = true
			if unhealthy(n) {
				bad = append(bad, n) // altındakilere inmeye gerek yok: zaten çalışmayan bir halka bulundu
				continue
			}
			stack = append(stack, deps[n]...)
		}
		if len(bad) > 0 {
			sort.Slice(bad, func(i, j int) bool { return bad[i] < bad[j] })
			out[id] = bad
		}
	}
	return out
}

// GET /api/topology -> düğümler, kenarlar ve olası kök nedenler.
func (s *Server) getTopology(w http.ResponseWriter, r *http.Request, a Auth) {
	ctx := r.Context()
	alerts := map[int64]int{}
	if rows, err := s.DB.QueryContext(ctx,
		`SELECT asset_id, count(*) FROM alerts WHERE tenant_id = $1 AND state = 'open' GROUP BY asset_id`, a.TenantID); err == nil {
		for rows.Next() {
			var id int64
			var n int
			if rows.Scan(&id, &n) == nil {
				alerts[id] = n
			}
		}
		rows.Close()
	}

	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, kind, language, collection, status FROM assets WHERE tenant_id = $1 ORDER BY name`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	nodes := []map[string]any{}
	status := map[int64]string{}
	for rows.Next() {
		var id int64
		var name, kind, lang, coll, st string
		if rows.Scan(&id, &name, &kind, &lang, &coll, &st) != nil {
			continue
		}
		status[id] = st
		nodes = append(nodes, map[string]any{"id": id, "name": name, "kind": kind, "language": lang,
			"collection": coll, "status": st, "open_alerts": alerts[id]})
	}
	rows.Close()

	erows, err := s.DB.QueryContext(ctx,
		`SELECT id, source_asset_id, target_asset_id, kind FROM topology_edges WHERE tenant_id = $1 ORDER BY id`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer erows.Close()
	edges := []map[string]any{}
	deps := map[int64][]int64{}
	for erows.Next() {
		var id, src, dst int64
		var kind string
		if erows.Scan(&id, &src, &dst, &kind) != nil {
			continue
		}
		edges = append(edges, map[string]any{"id": id, "source": src, "target": dst, "kind": kind})
		deps[src] = append(deps[src], dst)
	}
	// Tarayıcı senaryolarının otomatik keşfettiği ağ bağımlılıkları ve sayfa değişikliği işaretleri.
	changed := map[int64]bool{}
	if crows, err := s.DB.QueryContext(ctx,
		`SELECT DISTINCT asset_id FROM scenarios WHERE tenant_id = $1 AND last_changed_at > now() - interval '24 hours'`, a.TenantID); err == nil {
		for crows.Next() {
			var id int64
			if crows.Scan(&id) == nil {
				changed[id] = true
			}
		}
		crows.Close()
	}
	for _, n := range nodes {
		if changed[n["id"].(int64)] {
			n["page_changed"] = true
		}
	}
	if hrows, err := s.DB.QueryContext(ctx,
		`SELECT sc.asset_id, h.host, h.requests, h.failed, h.errors
		 FROM scenario_hosts h JOIN scenarios sc ON sc.id = h.scenario_id
		 WHERE sc.tenant_id = $1 AND h.last_seen > now() - interval '7 days' ORDER BY h.requests DESC LIMIT 80`, a.TenantID); err == nil {
		seen := map[string]bool{}
		for hrows.Next() {
			var asset int64
			var host string
			var req, failed, errs int
			if hrows.Scan(&asset, &host, &req, &failed, &errs) != nil {
				continue
			}
			ext := "ext:" + host
			if !seen[ext] && len(seen) < 40 {
				seen[ext] = true
				st := "up"
				if failed > 0 {
					st = "down"
				}
				nodes = append(nodes, map[string]any{"id": ext, "name": host, "kind": "external", "language": "none",
					"collection": "discovered", "status": st, "open_alerts": 0, "requests": req, "failed": failed, "errors": errs})
			}
			if seen[ext] {
				edges = append(edges, map[string]any{"id": "auto:" + strconv.FormatInt(asset, 10) + ":" + host, "source": asset, "target": ext, "kind": "calls", "auto": true})
			}
		}
		hrows.Close()
	}
	// Bağımlılığı çalışmayan asset'ler (ör. sunucusu düşen uygulama ve onun veritabanı) haritada da kırmızı görünür.
	names := map[int64]string{}
	for _, n := range nodes {
		if id, ok := n["id"].(int64); ok {
			names[id], _ = n["name"].(string)
		}
	}
	for id, bad := range impactedBy(status, deps) {
		for _, n := range nodes {
			if nid, ok := n["id"].(int64); ok && nid == id {
				by := []string{}
				for i, b := range bad {
					if i < 3 {
						by = append(by, names[b])
					}
				}
				n["impacted"], n["impacted_by"] = true, by
			}
		}
	}
	// NetFlow'dan öğrenilen konuşmalar (yalnızca iki ucu da tanımlı asset olanlar): bağımlılık sayılmaz, isteğe bağlı kaplama olarak çizilir.
	flowEdges := []map[string]any{}
	if frows, err := s.DB.QueryContext(ctx,
		`SELECT src_asset, dst_asset, sum(bytes) b FROM flow_agg
		  WHERE tenant_id = $1 AND bucket > now() - interval '24 hours' AND src_asset IS NOT NULL AND dst_asset IS NOT NULL AND src_asset <> dst_asset
		  GROUP BY 1, 2 ORDER BY b DESC LIMIT 100`, a.TenantID); err == nil {
		for frows.Next() {
			var sa, da, b int64
			if frows.Scan(&sa, &da, &b) == nil {
				flowEdges = append(flowEdges, map[string]any{"id": "flow:" + strconv.FormatInt(sa, 10) + ":" + strconv.FormatInt(da, 10), "source": sa, "target": da, "kind": "flow", "auto": true, "bytes": b})
			}
		}
		frows.Close()
	}
	writeJSON(w, 200, map[string]any{"nodes": nodes, "edges": edges, "flow_edges": flowEdges, "root_causes": rootCauses(status, deps)})
}

func (s *Server) createEdge(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		Source int64  `json:"source"`
		Target int64  `json:"target"`
		Kind   string `json:"kind"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = "depends_on"
	}
	if req.Source <= 0 || req.Target <= 0 || req.Source == req.Target || !oneOf(req.Kind, "depends_on", "runs_on") {
		fail(w, 400, "source ve target farklı birer asset olmalı; kind depends_on veya runs_on olmalı")
		return
	}
	var n int
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT count(*) FROM assets WHERE tenant_id = $1 AND id IN ($2, $3)`, a.TenantID, req.Source, req.Target).Scan(&n); err != nil || n != 2 {
		fail(w, 404, "asset bulunamadı")
		return
	}
	var id int64
	err := s.DB.QueryRowContext(r.Context(),
		`INSERT INTO topology_edges (tenant_id, source_asset_id, target_asset_id, kind) VALUES ($1,$2,$3,$4) RETURNING id`,
		a.TenantID, req.Source, req.Target, req.Kind).Scan(&id)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		fail(w, http.StatusConflict, "bu bağlantı zaten var")
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "topology.edge_create", strconv.FormatInt(req.Source, 10)+" -> "+strconv.FormatInt(req.Target, 10))
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) deleteEdge(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	res, err := s.DB.ExecContext(r.Context(), `DELETE FROM topology_edges WHERE id = $1 AND tenant_id = $2`, id, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		fail(w, 404, "bağlantı bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "topology.edge_delete", strconv.FormatInt(id, 10))
	w.WriteHeader(http.StatusNoContent)
}

// traverse özyinelemeli CTE ile bir asset'ten başlayarak grafı gezer (döngülere ve 10 seviyeye karşı korumalı).
// from/to sütun adları sabit değerlerdir, kullanıcı girdisi değildir.
func (s *Server) traverse(r *http.Request, tenant, asset int64, from, to string) ([]map[string]any, error) {
	q := `WITH RECURSIVE walk AS (
	        SELECT e.` + to + ` AS asset_id, 1 AS depth, ARRAY[e.` + from + `, e.` + to + `] AS path
	        FROM topology_edges e WHERE e.tenant_id = $1 AND e.` + from + ` = $2
	      UNION ALL
	        SELECT e.` + to + `, w.depth + 1, w.path || e.` + to + `
	        FROM topology_edges e JOIN walk w ON e.` + from + ` = w.asset_id
	        WHERE e.tenant_id = $1 AND NOT (e.` + to + ` = ANY(w.path)) AND w.depth < 10)
	      SELECT a.id, a.name, a.status, min(w.depth)
	      FROM walk w JOIN assets a ON a.id = w.asset_id
	      GROUP BY a.id, a.name, a.status ORDER BY min(w.depth), a.name`
	rows, err := s.DB.QueryContext(r.Context(), q, tenant, asset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, st string
		var depth int
		if rows.Scan(&id, &name, &st, &depth) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "status": st, "depth": depth})
		}
	}
	return out, nil
}

// GET /api/assets/{id}/impact -> bu asset düşerse etkilenenler ve bu asset'in (dolaylı) bağımlılıkları.
func (s *Server) assetImpact(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var one int
	if err := s.DB.QueryRowContext(r.Context(), `SELECT 1 FROM assets WHERE id = $1 AND tenant_id = $2`, id, a.TenantID).Scan(&one); err != nil {
		fail(w, 404, "asset bulunamadı")
		return
	}
	impacted, err1 := s.traverse(r, a.TenantID, id, "target_asset_id", "source_asset_id")
	deps, err2 := s.traverse(r, a.TenantID, id, "source_asset_id", "target_asset_id")
	if err1 != nil || err2 != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	writeJSON(w, 200, map[string]any{"impacted": impacted, "dependencies": deps})
}

// updateEdge mevcut bir bağlantının kaynağını, hedefini veya ilişki türünü değiştirir.
func (s *Server) updateEdge(w http.ResponseWriter, r *http.Request, a Auth) {
	id, ok := pathID(r)
	if !ok {
		fail(w, 400, "geçersiz id")
		return
	}
	var req struct {
		Source int64  `json:"source"`
		Target int64  `json:"target"`
		Kind   string `json:"kind"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Kind == "" {
		req.Kind = "depends_on"
	}
	if req.Source <= 0 || req.Target <= 0 || req.Source == req.Target || !oneOf(req.Kind, "depends_on", "runs_on") {
		fail(w, 400, "source ve target farklı birer asset olmalı; kind depends_on veya runs_on olmalı")
		return
	}
	var n int
	if err := s.DB.QueryRowContext(r.Context(),
		`SELECT count(*) FROM assets WHERE tenant_id = $1 AND id IN ($2, $3)`, a.TenantID, req.Source, req.Target).Scan(&n); err != nil || n != 2 {
		fail(w, 404, "asset bulunamadı")
		return
	}
	res, err := s.DB.ExecContext(r.Context(),
		`UPDATE topology_edges SET source_asset_id = $1, target_asset_id = $2, kind = $3 WHERE id = $4 AND tenant_id = $5`,
		req.Source, req.Target, req.Kind, id, a.TenantID)
	var pe *pq.Error
	if errors.As(err, &pe) && pe.Code == "23505" {
		fail(w, http.StatusConflict, "bu bağlantı zaten var")
		return
	}
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	if c, _ := res.RowsAffected(); c == 0 {
		fail(w, 404, "bağlantı bulunamadı")
		return
	}
	s.audit(r.Context(), a.TenantID, a.Label, "topology.edge_update", strconv.FormatInt(req.Source, 10)+" -> "+strconv.FormatInt(req.Target, 10))
	w.WriteHeader(http.StatusNoContent)
}
