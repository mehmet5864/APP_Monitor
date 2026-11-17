//go:build windows

package main

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// listenSockets "netstat -ano -p tcp" çıktısından dinleyen soketleri okur (loopback ise kapsam local).
func listenSockets() []listenSock {
	winNames = nil // her toplamada süreç adları yeniden okunur
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return nil
	}
	out := []listenSock{}
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) < 5 || !strings.EqualFold(f[0], "TCP") || !strings.EqualFold(f[3], "LISTENING") {
			continue
		}
		c := strings.LastIndexByte(f[1], ':')
		pid, e1 := strconv.Atoi(f[4])
		if c < 0 || e1 != nil {
			continue
		}
		port, e2 := strconv.Atoi(f[1][c+1:])
		if e2 != nil {
			continue
		}
		host := strings.Trim(f[1][:c], "[]")
		sc := "net"
		if host == "::1" || strings.HasPrefix(host, "127.") {
			sc = "local"
		}
		out = append(out, listenSock{pid: pid, port: port, scope: sc})
	}
	return out
}

var winNames map[int]string

// procInfoFor Windows'ta yalnızca süreç adını verir (ikili yolu ve çalışma dizini için ek yetki/WMI gerekir).
func procInfoFor(pid int) procDetail {
	if winNames == nil {
		winNames = map[int]string{}
		for _, p := range readProcs() {
			winNames[p.pid] = p.name
		}
	}
	return procDetail{comm: winNames[pid]}
}
