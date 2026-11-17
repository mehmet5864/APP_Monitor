package app

import (
	"context"
	"encoding/json"
	"github.com/lib/pq"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"appmon/internal/probe"
	"appmon/internal/siem"
)

// StartTraps UDP üzerinde SNMP v1/v2c trap alıcısını başlatır. Yalnızca topluluk adı eşleşen paketler kabul edilir.
// Kaynak IP, bir probe hedefiyle eşleşirse trap o asset'e bağlanır ve "snmp.trap" metriği (1) yazılır.
func (s *Server) StartTraps(ctx context.Context, addr, community string, tenant int64) error {
	bound, err := probe.ListenTraps(ctx, addr, func(src net.IP, t probe.Trap) {
		if t.Community != community {
			return
		}
		s.recordTrap(ctx, tenant, src, t)
	})
	if err != nil {
		return err
	}
	log.Printf("SNMP trap alıcısı dinliyor: %s", bound)
	return nil
}

func (s *Server) recordTrap(ctx context.Context, tenant int64, src net.IP, t probe.Trap) {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var asset *int64
	var name string
	var id int64
	err := s.DB.QueryRowContext(cctx,
		`SELECT a.id, a.name FROM probes p JOIN assets a ON a.id = p.asset_id
		  WHERE p.tenant_id = $1 AND p.type IN ('snmp','snmpx','host','icmp')
		    AND (p.target = $2 OR p.target LIKE $2 || ':%') ORDER BY p.id LIMIT 1`, tenant, src.String()).Scan(&id, &name)
	if err == nil {
		asset = &id
	}
	vars, _ := json.Marshal(t.Vars)
	if _, err := s.DB.ExecContext(cctx,
		`INSERT INTO snmp_traps (tenant_id, asset_id, source, version, trap_oid, vars) VALUES ($1,$2,$3,$4,$5,$6)`,
		tenant, asset, src.String(), t.Version+1, t.OID, string(vars)); err != nil {
		log.Printf("trap kaydedilemedi: %v", err)
		return
	}
	if asset != nil {
		s.DB.ExecContext(cctx, `INSERT INTO metrics (time, tenant_id, asset_id, name, value) VALUES (now(),$1,$2,'snmp.trap',1)`, tenant, id)
	} else {
		name = src.String()
	}
	s.siemEmit(tenant, siem.Event{Type: "alert", Severity: "warning", Action: "snmp_trap", Asset: name, Rule: "SNMP trap", Value: t.OID, Detail: "kaynak " + src.String(), Link: s.publicLink()})
}

func (s *Server) listTraps(w http.ResponseWriter, r *http.Request, a Auth) {
	rows, err := s.DB.QueryContext(r.Context(),
		`SELECT t.id, t.at, t.source, t.version, t.trap_oid, t.vars, COALESCE(a.name, '')
		   FROM snmp_traps t LEFT JOIN assets a ON a.id = t.asset_id
		  WHERE t.tenant_id = $1 ORDER BY t.id DESC LIMIT 200`, a.TenantID)
	if err != nil {
		fail(w, 500, "veritabanı hatası")
		return
	}
	defer rows.Close()
	list := []map[string]any{}
	for rows.Next() {
		var id int64
		var at time.Time
		var src, oid, vars, asset string
		var ver int
		if rows.Scan(&id, &at, &src, &ver, &oid, &vars, &asset) == nil {
			list = append(list, map[string]any{"id": id, "at": at, "source": src, "version": ver, "trap_oid": oid, "vars": json.RawMessage(vars), "asset": strings.TrimSpace(asset)})
		}
	}
	writeJSON(w, 200, map[string]any{"enabled": s.TrapsOn, "traps": list})
}

// traceNow isteğe bağlı yol izleme: POST /api/trace {target, max_hops}
func (s *Server) traceNow(w http.ResponseWriter, r *http.Request, a Auth) {
	var req struct {
		Target  string `json:"target"`
		MaxHops int    `json:"max_hops"`
		DSCP    int    `json:"dscp"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Target = strings.TrimSpace(req.Target)
	if !probe.ValidHost(req.Target) {
		fail(w, 400, "hedef geçerli bir ad veya IP olmalı")
		return
	}
	if req.DSCP < 0 || req.DSCP > 63 {
		fail(w, 400, "DSCP değeri 0-63 arasında olmalı")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	res, err := probe.Trace(ctx, req.Target, req.MaxHops, 3, 600*time.Millisecond, s.BlockPrivate, req.DSCP)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.tagHopAssets(ctx, a.TenantID, res.Hops)
	s.audit(r.Context(), a.TenantID, a.Label, "path_trace", req.Target)
	writeJSON(w, 200, res)
}

// tagHopAssets sıçrama IP'leri APP_MON'da tanımlı bir asset'e aitse (yalnızca benzersiz eşleşmelerde) asset adını yazar.
func (s *Server) tagHopAssets(ctx context.Context, tenant int64, hops []probe.Hop) {
	ips := []string{}
	for _, h := range hops {
		if h.IP != "" {
			ips = append(ips, h.IP)
		}
	}
	if len(ips) == 0 {
		return
	}
	names := map[string]string{}
	dup := map[string]bool{}
	put := func(ip, name string) {
		if old, ok := names[ip]; ok && old != name {
			dup[ip] = true
		}
		names[ip] = name
	}
	if rows, err := s.DB.QueryContext(ctx, `SELECT ha.ip, a.name FROM host_addrs ha JOIN assets a ON a.id = ha.asset_id WHERE ha.tenant_id = $1 AND ha.ip = ANY($2)`, tenant, pq.Array(ips)); err == nil {
		for rows.Next() {
			var ip, name string
			if rows.Scan(&ip, &name) == nil {
				put(ip, name)
			}
		}
		rows.Close()
	}
	// probe hedefi düz IP olan asset'ler (ping/SNMP ile izlenen ağ cihazları)
	if rows, err := s.DB.QueryContext(ctx, `SELECT p.target, a.name FROM probes p JOIN assets a ON a.id = p.asset_id WHERE p.tenant_id = $1 AND p.type IN ('icmp','snmp','snmpx','host','tcp','trace')`, tenant); err == nil {
		want := map[string]bool{}
		for _, ip := range ips {
			want[ip] = true
		}
		for rows.Next() {
			var t, name string
			if rows.Scan(&t, &name) == nil {
				if h := hostPart(t); want[h] {
					put(h, name)
				}
			}
		}
		rows.Close()
	}
	for i := range hops {
		if n, ok := names[hops[i].IP]; ok && !dup[hops[i].IP] {
			hops[i].Asset = n
		}
	}
}
