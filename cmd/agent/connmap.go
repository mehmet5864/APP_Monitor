package main

import (
	"net"
	"sort"
	"strconv"
	"strings"
)

// Bağlantı tabanlı servis haritası (agent 1.2.0): makinedeki süreçlerin giden TCP bağlantılarını
// (süreç → hedef adres:port) toplar. Yalnızca ESTABLISHED bağlantıların uç noktaları ve sahibi süreç adı okunur;
// paket içeriği yakalanmaz. Hedef aynı makinedeki bir dinleyen portsa dinleyen sürecin adı da eklenir.

type sock struct {
	lip, rip     string
	lport, rport int
	pid          int
}

type connRec struct {
	Src  string `json:"src"`
	Dst  string `json:"dst,omitempty"`
	Addr string `json:"addr"`
	Port int    `json:"port"`
	N    int    `json:"n"`
}

type connIn struct {
	Addr string `json:"addr"` // bağlanan uzak IP
	Port int    `json:"port"` // bu makinede dinlenen port
	N    int    `json:"n"`
}

type connReport struct {
	IPs   []string  `json:"ips"`
	Edges []connRec `json:"edges"`
	In    []connIn  `json:"in,omitempty"` // gelen bağlantılar (Performans › Bağlantılar)
}

const maxConnRecs = 300

func isLoopback(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsLoopback()
}

// buildConns açık bağlantıları (süreç adı, hedef) gruplarına dönüştürür.
//   - pidName: süreç kimliği → ad; listen: süreç kimliği → dinlediği portlar; local: bu makinenin IP'leri.
//   - Yerel portu dinleyen bir port ise bağlantı sunucu tarafıdır ve atlanır (istemci tarafı karşı uçtan görülür).
func buildConns(socks []sock, pidName map[int]string, listen map[int][]int, local map[string]bool) []connRec {
	listener := map[int]string{} // dinlenen port → süreç adı
	for pid, ports := range listen {
		for _, p := range ports {
			if n := pidName[pid]; n != "" {
				listener[p] = n
			}
		}
	}
	listening := map[int]bool{}
	for _, ports := range listen {
		for _, p := range ports {
			listening[p] = true
		}
	}
	type key struct {
		src, dst, addr string
		port           int
	}
	agg := map[key]int{}
	for _, s := range socks {
		if listening[s.lport] { // sunucu tarafı
			continue
		}
		src := pidName[s.pid]
		if src == "" || s.rport < 1 {
			continue
		}
		dst := ""
		if isLoopback(s.rip) || local[s.rip] {
			dst = listener[s.rport]
			if dst == "" && isLoopback(s.rip) {
				continue // yerel hedef ama sahibi bilinmiyor (başka kullanıcının süreci olabilir)
			}
		}
		agg[key{src, dst, s.rip, s.rport}]++
	}
	out := make([]connRec, 0, len(agg))
	for k, n := range agg {
		out = append(out, connRec{Src: k.src, Dst: k.dst, Addr: k.addr, Port: k.port, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		a, b := out[i], out[j]
		return a.Src+a.Dst+a.Addr+strconv.Itoa(a.Port) < b.Src+b.Dst+b.Addr+strconv.Itoa(b.Port)
	})
	if len(out) > maxConnRecs {
		out = out[:maxConnRecs]
	}
	return out
}

// buildInbound dinlenen portlara gelen (loopback olmayan) bağlantıları uzak IP ve port bazında sayar.
func buildInbound(socks []sock, listen map[int][]int) []connIn {
	listening := map[int]bool{}
	for _, ports := range listen {
		for _, p := range ports {
			listening[p] = true
		}
	}
	type key struct {
		addr string
		port int
	}
	agg := map[key]int{}
	for _, s := range socks {
		if listening[s.lport] && !isLoopback(s.rip) {
			agg[key{s.rip, s.lport}]++
		}
	}
	out := make([]connIn, 0, len(agg))
	for k, n := range agg {
		out = append(out, connIn{Addr: k.addr, Port: k.port, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Addr+strconv.Itoa(out[i].Port) < out[j].Addr+strconv.Itoa(out[j].Port)
	})
	if len(out) > maxConnRecs {
		out = out[:maxConnRecs]
	}
	return out
}

// localIPs makinenin loopback ve link-local olmayan adreslerini döndürür.
func localIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() && len(out) < 20 {
			out = append(out, n.IP.String())
		}
	}
	return out
}

// parseNetstat "netstat -ano -p tcp" çıktısındaki ESTABLISHED satırlarını çözer (Windows).
func parseNetstat(out string) []sock {
	var socks []sock
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 5 || !strings.EqualFold(f[0], "TCP") || !strings.EqualFold(f[3], "ESTABLISHED") {
			continue
		}
		lh, lp, e1 := net.SplitHostPort(f[1])
		rh, rp, e2 := net.SplitHostPort(f[2])
		pid, e3 := strconv.Atoi(f[4])
		lport, e4 := strconv.Atoi(lp)
		rport, e5 := strconv.Atoi(rp)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
			continue
		}
		socks = append(socks, sock{lip: normIP(lh), rip: normIP(rh), lport: lport, rport: rport, pid: pid})
	}
	return socks
}

func normIP(s string) string {
	if i := strings.IndexByte(s, '%'); i >= 0 { // IPv6 bölge kimliği
		s = s[:i]
	}
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return s
}

// collectConns tüm adımları çalıştırır: yuvaları okur, süreç adlarını ve dinleyen portları eşler.
func collectConns() *connReport {
	socks := readSockets()
	if socks == nil {
		return nil
	}
	pidName := map[int]string{}
	for _, p := range readProcs() {
		pidName[p.pid] = safeName(p.name)
	}
	ips := localIPs()
	local := map[string]bool{}
	for _, ip := range ips {
		local[ip] = true
	}
	lp := listenPorts()
	return &connReport{IPs: ips, Edges: buildConns(socks, pidName, lp, local), In: buildInbound(socks, lp)}
}
