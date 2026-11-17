//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
)

// hexLoopback: /proc/net/tcp adres alanı (küçük sonlu onaltılık) loopback mu.
func hexLoopback(addr string) bool {
	switch len(addr) {
	case 8: // IPv4: 127.x.y.z -> son bayt 7F
		return strings.HasSuffix(strings.ToUpper(addr), "7F")
	case 32:
		a := strings.ToUpper(addr)
		return a == "00000000000000000000000001000000" || // ::1
			(strings.HasPrefix(a, "0000000000000000FFFF0000") && strings.HasSuffix(a, "7F")) // ::ffff:127.x
	}
	return false
}

// listenSockets dinleyen TCP soketlerini süreç kimliği, port ve kapsamla (net/local) döndürür.
// Hiçbir soket süreçle eşleşemezse (yetki yok) boş liste döner; /proc okunamazsa nil.
func listenSockets() []listenSock {
	type ent struct {
		port  int
		scope string
	}
	inodes := map[string]ent{}
	readAny := false
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		readAny = true
		for i, ln := range strings.Split(string(b), "\n") {
			if i == 0 {
				continue
			}
			p := strings.Fields(ln)
			if len(p) < 10 || p[3] != "0A" {
				continue
			}
			c := strings.LastIndexByte(p[1], ':')
			if c < 0 {
				continue
			}
			port, err := strconv.ParseUint(p[1][c+1:], 16, 32)
			if err != nil {
				continue
			}
			sc := "net"
			if hexLoopback(p[1][:c]) {
				sc = "local"
			}
			inodes[p[9]] = ent{int(port), sc}
		}
	}
	if !readAny {
		return nil
	}
	out := []listenSock{}
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
			if en, ok := inodes[strings.TrimSuffix(strings.TrimPrefix(ln, "socket:["), "]")]; ok {
				out = append(out, listenSock{pid: pid, port: en.port, scope: en.scope})
			}
		}
	}
	return out
}

// procInfoFor sürecin adı, ikili yolu, komut satırı, çalışma dizini ve kapsayıcıda olup olmadığını okur.
func procInfoFor(pid int) procDetail {
	base := "/proc/" + strconv.Itoa(pid)
	pd := procDetail{}
	if b, err := os.ReadFile(base + "/comm"); err == nil {
		pd.comm = strings.ToLower(strings.TrimSpace(string(b)))
	}
	if b, err := os.ReadFile(base + "/cmdline"); err == nil && len(b) > 0 {
		pd.args = strings.Split(strings.TrimRight(string(b), "\x00"), "\x00")
	}
	if p, err := os.Readlink(base + "/exe"); err == nil && !strings.HasSuffix(p, " (deleted)") {
		pd.exeReal = p
		pd.exe = base + "/exe"
	}
	if p, err := os.Readlink(base + "/cwd"); err == nil {
		pd.cwd = p
	}
	if b, err := os.ReadFile(base + "/cgroup"); err == nil {
		s := string(b)
		pd.container = strings.Contains(s, "docker") || strings.Contains(s, "kubepods") || strings.Contains(s, "containerd") || strings.Contains(s, "libpod") || strings.Contains(s, "lxc")
	}
	return pd
}
