//go:build windows

package main

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	pOpenProcess     = k32.NewProc("OpenProcess")
	pGetProcessTimes = k32.NewProc("GetProcessTimes")
	pGetProcMemInfo  = k32.NewProc("K32GetProcessMemoryInfo")
)

type procMemCounters struct {
	cb, pageFaults                           uint32
	peakWS, ws, qpPeakPaged, qpPaged         uintptr
	qpPeakNon, qpNon, pagefile, peakPagefile uintptr
}

// readProcs Toolhelp ile süreçleri sayar; erişilebilen süreçler için CPU süresi ve çalışma kümesi (RSS) okunur.
func readProcs() []procSample {
	h, _, _ := pSnapshot.Call(2, 0) // TH32CS_SNAPPROCESS
	if h == 0 || h == ^uintptr(0) {
		return nil
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	var e procEntry
	e.Size = uint32(unsafe.Sizeof(e))
	var out []procSample
	r, _, _ := pProcFirst.Call(h, uintptr(unsafe.Pointer(&e)))
	for r != 0 {
		pid := int(e.PID)
		name := strings.TrimSuffix(strings.ToLower(syscall.UTF16ToString(e.Exe[:])), ".exe")
		if pid > 4 { // Idle ve System atlanır
			s := procSample{pid: pid, name: name}
			if ph, _, _ := pOpenProcess.Call(0x1000, 0, uintptr(pid)); ph != 0 { // PROCESS_QUERY_LIMITED_INFORMATION
				var c, x, k, u syscall.Filetime
				if ok, _, _ := pGetProcessTimes.Call(ph, uintptr(unsafe.Pointer(&c)), uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u))); ok != 0 {
					s.cpuSec = float64(ft(k)+ft(u)) / 1e7
				}
				var m procMemCounters
				m.cb = uint32(unsafe.Sizeof(m))
				if ok, _, _ := pGetProcMemInfo.Call(ph, uintptr(unsafe.Pointer(&m)), uintptr(m.cb)); ok != 0 {
					s.rssKB = uint64(m.ws) / 1024
				}
				syscall.CloseHandle(syscall.Handle(ph))
			}
			out = append(out, s)
		}
		r, _, _ = pProcNext.Call(h, uintptr(unsafe.Pointer(&e)))
	}
	return out
}

// listenPorts "netstat -ano -p tcp" çıktısından dinleyen portları süreç kimliğine eşler.
func listenPorts() map[int][]int {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return nil
	}
	out := map[int][]int{}
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) < 5 || !strings.EqualFold(f[0], "TCP") || !strings.EqualFold(f[3], "LISTENING") {
			continue
		}
		c := strings.LastIndexByte(f[1], ':')
		pid, e1 := strconv.Atoi(f[4])
		port, e2 := strconv.Atoi(f[1][c+1:])
		if c < 0 || e1 != nil || e2 != nil {
			continue
		}
		out[pid] = append(out[pid], port)
	}
	return out
}

var scStateRe = regexp.MustCompile(`:\s*\d+\s+([A-Z_]+)`)

// listServices çalışan Windows servislerini ("sc query") listeler; çıktı dili değişse de anahtar sözcükler aynıdır.
func listServices() []appInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "sc.exe", "query", "type=", "service", "state=", "active").Output()
	if err != nil {
		return nil
	}
	var out []appInfo
	name := ""
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "SERVICE_NAME:") {
			name = strings.TrimSpace(strings.TrimPrefix(ln, "SERVICE_NAME:"))
			continue
		}
		if name != "" {
			if m := scStateRe.FindStringSubmatch(ln); m != nil && m[1] == "RUNNING" {
				out = append(out, appInfo{Name: name, Kind: "service", State: "running"})
				name = ""
			}
		}
	}
	return out
}

// Windows'ta otomatik günlük okuma yok (Olay Günlüğü desteklenmez); -log ile dosya günlükleri izlenebilir.
func autoLogDefs() []autoLogDef     { return nil }
func pickLogPath(_ []string) string { return "" }
