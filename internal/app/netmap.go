package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"
)

// Bağlantı tabanlı servis haritası (v3.3.0): ajan, makinedeki süreçlerin giden TCP bağlantılarını
// (süreç → hedef adres:port) bildirir; sunucu hedef adresi başka bir ajan makinesine eşleyebilirse
// "süreç@makine" düğümleri arasında kenar çizer. Kod değişikliği veya OpenTelemetry gerekmez;
// istek süresi/hata oranı yoktur, yalnızca "kim kime bağlı" ve bağlantı sayısı.

// AgentConn ajanın bildirdiği tek bir giden bağlantı grubudur.
type AgentConn struct {
	Src  string `json:"src"`  // bağlantıyı açan süreç adı
	Dst  string `json:"dst"`  // hedef aynı makinede ise dinleyen süreç adı
	Addr string `json:"addr"` // hedef IP
	Port int    `json:"port"`
	N    int    `json:"n"` // bağlantı sayısı
}

// AgentConns ajanın bağlantı anlık görüntüsüdür.
type AgentConns struct {
	IPs   []string    `json:"ips"`
	Edges []AgentConn `json:"edges"`
	In    []AgentIn   `json:"in"` // gelen bağlantılar
}

// AgentIn dinlenen bir porta bağlanan uzak adres grubudur.
type AgentIn struct {
	Addr string `json:"addr"`
	Port int    `json:"port"`
	N    int    `json:"n"`
}

const maxConnEdges = 300

func okName(s string) bool { return s != "" && len(s) <= 60 && !strings.ContainsAny(s, "\x00\n\r@") }

// sanitize geçersiz satırları atar ve sınırları uygular.
func (c *AgentConns) sanitize() ([]string, []AgentConn) {
	var ips []string
	for _, s := range c.IPs {
		if ip := net.ParseIP(strings.TrimSpace(s)); ip != nil && len(ips) < 24 {
			ips = append(ips, ip.String())
		}
	}
	var out []AgentConn
	for _, e := range c.Edges {
		ip := net.ParseIP(strings.TrimSpace(e.Addr))
		if ip == nil || !okName(e.Src) || (e.Dst != "" && !okName(e.Dst)) || e.Port < 1 || e.Port > 65535 || e.N < 1 || e.N > 1_000_000 {
			continue
		}
		out = append(out, AgentConn{Src: e.Src, Dst: e.Dst, Addr: ip.String(), Port: e.Port, N: e.N})
		if len(out) >= maxConnEdges {
			break
		}
	}
	return ips, out
}

// storeConns bir ajanın anlık görüntüsünü değiştirir (eski satırlar silinir).
func storeConns(ctx context.Context, tx *sql.Tx, tenant, asset int64, c *AgentConns) error {
	ips, edges := c.sanitize()
	if _, err := tx.ExecContext(ctx, `DELETE FROM net_edges WHERE asset_id = $1`, asset); err != nil {
		return err
	}
	for _, e := range edges {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO net_edges (tenant_id, asset_id, src, dst, addr, port, conns) VALUES ($1,$2,$3,$4,$5,$6,$7)
			 ON CONFLICT (asset_id, src, dst, addr, port) DO UPDATE SET conns = net_edges.conns + EXCLUDED.conns, updated_at = now()`,
			tenant, asset, e.Src, e.Dst, e.Addr, e.Port, e.N); err != nil {
			return err
		}
	}
	// Performans › Bağlantılar için makine bazında özet: giden (hedef IP:port) ve gelen (uzak IP → dinlenen port).
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_host_conns WHERE asset_id = $1`, asset); err != nil {
		return err
	}
	type hk struct {
		dir, ip string
		port    int
	}
	agg := map[hk]int{}
	for _, e := range edges {
		agg[hk{"out", e.Addr, e.Port}] += e.N
	}
	nin := 0
	for _, in := range c.In {
		ip := net.ParseIP(strings.TrimSpace(in.Addr))
		if ip == nil || in.Port < 1 || in.Port > 65535 || in.N < 1 || in.N > 1_000_000 || nin >= maxConnEdges {
			continue
		}
		nin++
		agg[hk{"in", ip.String(), in.Port}] += in.N
	}
	for k, n := range agg {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO agent_host_conns (asset_id, tenant_id, direction, peer_ip, port, conns) VALUES ($1,$2,$3,$4,$5,$6)`,
			asset, tenant, k.dir, k.ip, k.port, n); err != nil {
			return err
		}
	}
	for _, ip := range ips { // bağlantıları başka makinelere eşlemek için makinenin adresleri
		if pip := net.ParseIP(ip); pip != nil && !pip.IsLoopback() && !pip.IsLinkLocalUnicast() {
			if _, err := tx.ExecContext(ctx, `INSERT INTO host_addrs (tenant_id, asset_id, ip) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, asset, ip); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO net_hosts (asset_id, tenant_id, ips, updated_at) VALUES ($1,$2,$3,now())
		 ON CONFLICT (asset_id) DO UPDATE SET ips = EXCLUDED.ips, updated_at = now()`, asset, tenant, pq.Array(ips))
	return err
}

type connEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Conns int    `json:"conns"`
	Ext   bool   `json:"external"` // hedef izlenen bir makine değil (dış adres)
}

var wellKnownPorts = map[int]string{22: "ssh", 25: "smtp", 53: "dns", 80: "http", 110: "pop3", 143: "imap", 389: "ldap", 443: "https", 445: "smb",
	636: "ldaps", 1433: "mssql", 1521: "oracle", 3306: "mysql", 3389: "rdp", 5432: "postgresql", 5672: "amqp", 6379: "redis", 8080: "http", 9092: "kafka",
	9200: "elasticsearch", 11211: "memcached", 27017: "mongodb"}

func extLabel(addr string, port int) string {
	l := net.JoinHostPort(addr, strconv.Itoa(port))
	if n := wellKnownPorts[port]; n != "" {
		return l + " (" + n + ")"
	}
	return l
}

func intToStr(n int) string { return strconv.Itoa(n) }

func node(proc, host string) string {
	if proc == "" {
		return host
	}
	return proc + "@" + host
}

// connMap kiracının bağlantı kenarlarını düğüm adlarına çözer: "süreç@makine" veya "adres:port (tür)".
func (s *Server) connMap(ctx context.Context, tenant int64) ([]connEdge, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT a.id, a.name, e.src, e.dst, e.addr, e.port, e.conns
		   FROM net_edges e JOIN assets a ON a.id = e.asset_id
		  WHERE e.tenant_id = $1 AND e.updated_at > $2 ORDER BY e.conns DESC LIMIT 2000`, tenant, time.Now().Add(-15*time.Minute))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type raw struct {
		asset     int64
		host, src string
		dst, addr string
		port, n   int
	}
	var list []raw
	for rows.Next() {
		var r raw
		if err := rows.Scan(&r.asset, &r.host, &r.src, &r.dst, &r.addr, &r.port, &r.n); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if len(list) == 0 {
		return []connEdge{}, nil
	}
	// IP -> makine
	type hostInfo struct {
		id   int64
		name string
	}
	ipHost := map[string]hostInfo{}
	hr, err := s.DB.QueryContext(ctx, `SELECT h.asset_id, a.name, h.ips FROM net_hosts h JOIN assets a ON a.id = h.asset_id WHERE h.tenant_id = $1 AND h.updated_at > $2`, tenant, time.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	for hr.Next() {
		var id int64
		var name string
		var ips pq.StringArray
		if hr.Scan(&id, &name, &ips) == nil {
			for _, ip := range ips {
				ipHost[ip] = hostInfo{id, name}
			}
		}
	}
	hr.Close()
	// makine -> port -> dinleyen süreç adı (asset_apps)
	listen := map[int64]map[int]string{}
	ar, err := s.DB.QueryContext(ctx, `SELECT asset_id, apps FROM asset_apps WHERE tenant_id = $1`, tenant)
	if err == nil {
		for ar.Next() {
			var id int64
			var raw []byte
			if ar.Scan(&id, &raw) != nil {
				continue
			}
			var apps []struct {
				Name  string `json:"name"`
				Ports []int  `json:"ports"`
			}
			if json.Unmarshal(raw, &apps) == nil {
				m := map[int]string{}
				for _, a := range apps {
					for _, p := range a.Ports {
						m[p] = a.Name
					}
				}
				listen[id] = m
			}
		}
		ar.Close()
	}
	agg := map[[2]string]*connEdge{}
	for _, r := range list {
		from := node(r.src, r.host)
		var to string
		ext := false
		switch {
		case r.dst != "":
			to = node(r.dst, r.host)
		default:
			if h, ok := ipHost[r.addr]; ok {
				to = node(listen[h.id][r.port], h.name)
				if listen[h.id][r.port] == "" {
					to = h.name + ":" + intToStr(r.port)
				}
			} else {
				to, ext = extLabel(r.addr, r.port), true
			}
		}
		if from == to {
			continue
		}
		k := [2]string{from, to}
		if agg[k] == nil {
			agg[k] = &connEdge{From: from, To: to, Ext: ext}
		}
		agg[k].Conns += r.n
	}
	out := make([]connEdge, 0, len(agg))
	for _, e := range agg {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Conns != out[j].Conns {
			return out[i].Conns > out[j].Conns
		}
		return out[i].From+out[i].To < out[j].From+out[j].To
	})
	if len(out) > 300 {
		out = out[:300]
	}
	return out, nil
}
