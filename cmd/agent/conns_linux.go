package main

import (
	"net"
	"os"
	"strconv"
	"strings"
)

// hexIP /proc/net/tcp'deki küçük-endian onaltılık adresi IP'ye çevirir (IPv4: 8 hane, IPv6: 32 hane).
func hexIP(h string) string {
	b := make([]byte, len(h)/2)
	for i := range b {
		v, err := strconv.ParseUint(h[2*i:2*i+2], 16, 8)
		if err != nil {
			return ""
		}
		b[i] = byte(v)
	}
	switch len(b) {
	case 4:
		return net.IPv4(b[3], b[2], b[1], b[0]).String()
	case 16:
		var ip [16]byte
		for w := 0; w < 4; w++ { // her 4 baytlık sözcük küçük-endian
			ip[w*4], ip[w*4+1], ip[w*4+2], ip[w*4+3] = b[w*4+3], b[w*4+2], b[w*4+1], b[w*4]
		}
		return net.IP(ip[:]).String()
	}
	return ""
}

type inodeSock struct {
	lip, rip     string
	lport, rport int
}

func parseProcNet(b []byte) map[string]inodeSock {
	out := map[string]inodeSock{}
	for i, ln := range strings.Split(string(b), "\n") {
		if i == 0 {
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 10 || f[3] != "01" { // 01 = ESTABLISHED
			continue
		}
		la, ra := strings.Split(f[1], ":"), strings.Split(f[2], ":")
		if len(la) != 2 || len(ra) != 2 {
			continue
		}
		lp, e1 := strconv.ParseUint(la[1], 16, 32)
		rp, e2 := strconv.ParseUint(ra[1], 16, 32)
		if e1 != nil || e2 != nil || f[9] == "0" {
			continue
		}
		out[f[9]] = inodeSock{lip: hexIP(la[0]), rip: hexIP(ra[0]), lport: int(lp), rport: int(rp)}
	}
	return out
}

// readSockets ESTABLISHED TCP yuvalarını ve sahibi süreci (/proc/<pid>/fd üzerinden) okur.
// Başka kullanıcıların süreçleri için root gerekir; okunamayan yuvalar süreçsiz olduğundan atlanır.
func readSockets() []sock {
	inodes := map[string]inodeSock{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for k, v := range parseProcNet(b) {
			inodes[k] = v
		}
	}
	if len(inodes) == 0 {
		return []sock{}
	}
	var out []sock
	ents, _ := os.ReadDir("/proc")
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir("/proc/" + e.Name() + "/fd")
		if err != nil {
			continue
		}
		for _, fd := range fds {
			ln, err := os.Readlink("/proc/" + e.Name() + "/fd/" + fd.Name())
			if err != nil || !strings.HasPrefix(ln, "socket:[") {
				continue
			}
			if s, ok := inodes[strings.TrimSuffix(strings.TrimPrefix(ln, "socket:["), "]")]; ok {
				out = append(out, sock{lip: s.lip, rip: s.rip, lport: s.lport, rport: s.rport, pid: pid})
			}
		}
	}
	return out
}
