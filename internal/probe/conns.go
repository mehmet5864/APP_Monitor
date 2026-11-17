package probe

import (
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Conn bir makinedeki kurulu TCP bağlantılarının (peer, port) bazında toplanmış halidir.
type Conn struct {
	Dir  string  // "out": bu makine peer'a bağlanıyor (port = peer'ın hizmet portu); "in": peer bu makineye bağlanıyor (port = yerel dinleme portu)
	Peer string  // karşı tarafın IP adresi
	Port int     // hizmet portu
	N    int     // bağlantı sayısı
	RTT  float64 // ortalama TCP RTT (ms); bilinmiyorsa -1
}

// ConnSnapshot bir SSH turunda okunan bağlantı özetidir.
type ConnSnapshot struct {
	Addrs []string // makinenin kendi IP adresleri (hostname -I)
	Conns []Conn
	// Dışarıdan erişilebilir (loopback olmayan) dinleyen TCP portları; ListenOK, ss çıktısı alınabildiyse true.
	Listen   []int
	ListenOK bool
	// SSH probe'unda bağlantı/adres toplama seçilmediyse true: mevcut kayıtlar silinmez, ölçüm yazılmaz.
	SkipConns, SkipAddrs bool
}

var rttRe = regexp.MustCompile(`\brtt:([0-9.]+)/`)

// splitHostPort ss çıktısındaki "10.0.0.5:52344", "[::1]:22", "[fe80::1%eth0]:22" biçimlerini ayırır.
func splitHostPort(s string) (string, int, bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, false
	}
	host, ps := s[:i], s[i+1:]
	port, err := strconv.Atoi(ps)
	if err != nil {
		return "", 0, false
	}
	host = strings.Trim(host, "[]")
	if j := strings.Index(host, "%"); j >= 0 {
		host = host[:j]
	}
	host = strings.TrimPrefix(host, "::ffff:")
	if net.ParseIP(host) == nil {
		return "", 0, false
	}
	return host, port, true
}

// ParseConns sshScript'in ---CONN, ---LISTEN ve ---ADDR bölümlerini bağlantı özetine çevirir.
// Dinleme portlarına gelen bağlantılar "in", diğerleri "out" sayılır. Loopback ve kendi adreslerine olanlar atlanır.
func ParseConns(conn, listen, addr []string) *ConnSnapshot {
	snap := &ConnSnapshot{}
	own := map[string]bool{}
	for _, ln := range addr {
		for _, f := range strings.Fields(ln) {
			if ip := net.ParseIP(f); ip != nil {
				snap.Addrs = append(snap.Addrs, ip.String())
				own[ip.String()] = true
			}
		}
	}
	listening := map[int]bool{}
	exposed := map[int]bool{}
	for _, ln := range listen {
		f := strings.Fields(ln)
		if len(f) < 4 || strings.HasPrefix(f[0], "State") {
			continue
		}
		if h, p, ok := splitHostPort(f[3]); ok {
			listening[p] = true
			snap.ListenOK = true
			if ip := net.ParseIP(h); ip != nil && !ip.IsLoopback() {
				exposed[p] = true
			}
		}
	}
	for p := range exposed {
		snap.Listen = append(snap.Listen, p)
	}
	sort.Ints(snap.Listen)
	type key struct {
		dir, peer string
		port      int
	}
	type agg struct {
		n, rn int
		rsum  float64
	}
	m := map[key]*agg{}
	var cur *key
	for _, ln := range conn {
		if ln == "" {
			continue
		}
		if ln[0] == ' ' || ln[0] == '\t' { // ayrıntı satırı (tcp -i bilgisi)
			if cur != nil {
				if mm := rttRe.FindStringSubmatch(ln); mm != nil {
					if v, err := strconv.ParseFloat(mm[1], 64); err == nil {
						a := m[*cur]
						a.rsum += v
						a.rn++
					}
				}
			}
			continue
		}
		cur = nil
		f := strings.Fields(ln)
		if len(f) > 0 && (strings.HasPrefix(f[0], "Recv-Q") || strings.HasPrefix(f[0], "State")) {
			continue
		}
		if len(f) > 0 && strings.EqualFold(f[0], "ESTAB") {
			f = f[1:]
		}
		if len(f) < 4 {
			continue
		}
		lip, lport, ok1 := splitHostPort(f[2])
		pip, pport, ok2 := splitHostPort(f[3])
		if !ok1 || !ok2 {
			continue
		}
		pi := net.ParseIP(pip)
		if pi.IsLoopback() || own[pip] || pi.IsUnspecified() {
			continue
		}
		_ = lip
		k := key{"out", pip, pport}
		if listening[lport] {
			k = key{"in", pip, lport}
		}
		a := m[k]
		if a == nil {
			a = &agg{}
			m[k] = a
		}
		a.n++
		cur = &k
	}
	for k, a := range m {
		c := Conn{Dir: k.dir, Peer: k.peer, Port: k.port, N: a.n, RTT: -1}
		if a.rn > 0 {
			c.RTT = a.rsum / float64(a.rn)
		}
		snap.Conns = append(snap.Conns, c)
	}
	sort.Slice(snap.Conns, func(i, j int) bool {
		if snap.Conns[i].N != snap.Conns[j].N {
			return snap.Conns[i].N > snap.Conns[j].N
		}
		a, b := snap.Conns[i], snap.Conns[j]
		return a.Dir+a.Peer < b.Dir+b.Peer
	})
	if len(snap.Conns) > 300 {
		snap.Conns = snap.Conns[:300]
	}
	return snap
}

// fillConns ham ssh çıktısından bağlantı özetini üretir (parseSSHOutput ile aynı bölüm biçimi).
func fillConns(out string) *ConnSnapshot {
	secs := map[string][]string{}
	cur := ""
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.HasPrefix(ln, "---") {
			cur = strings.ToLower(strings.TrimPrefix(ln, "---"))
			continue
		}
		if cur == "conn" || cur == "listen" || cur == "addr" {
			secs[cur] = append(secs[cur], ln)
		}
	}
	return ParseConns(secs["conn"], secs["listen"], secs["addr"])
}
