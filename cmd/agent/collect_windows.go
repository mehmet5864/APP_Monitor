//go:build windows

package main

import (
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	k32 = syscall.NewLazyDLL("kernel32.dll")
	ipH = syscall.NewLazyDLL("iphlpapi.dll")

	pGetSystemTimes     = k32.NewProc("GetSystemTimes")
	pGlobalMemoryStatus = k32.NewProc("GlobalMemoryStatusEx")
	pGetTickCount64     = k32.NewProc("GetTickCount64")
	pGetLogicalDrives   = k32.NewProc("GetLogicalDrives")
	pGetDriveType       = k32.NewProc("GetDriveTypeW")
	pGetDiskFreeSpaceEx = k32.NewProc("GetDiskFreeSpaceExW")
	pSnapshot           = k32.NewProc("CreateToolhelp32Snapshot")
	pProcFirst          = k32.NewProc("Process32FirstW")
	pProcNext           = k32.NewProc("Process32NextW")
	pGetIfTable         = ipH.NewProc("GetIfTable")
)

type collector struct {
	prevIdle, prevTotal uint64
	prevRx, prevTx      uint32
	rxAcc, txAcc        uint64
	prevAt              time.Time
	netOK               bool
}

func ft(f syscall.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }

func readCPU() (idle, total uint64, ok bool) {
	var i, k, u syscall.Filetime
	r, _, _ := pGetSystemTimes.Call(uintptr(unsafe.Pointer(&i)), uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&u)))
	if r == 0 {
		return 0, 0, false
	}
	return ft(i), ft(k) + ft(u), true // kernel süresi boşta zamanını da içerir
}

// readNet fiziksel/sanal ağ arayüzlerinin 32 bit sayaç toplamlarını okur (loopback hariç).
func readNet() (rx, tx uint32, ok bool) {
	size := uint32(16 * 1024)
	var buf []byte
	for try := 0; try < 3; try++ {
		buf = make([]byte, size)
		r, _, _ := pGetIfTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
		if r == 0 {
			break
		}
		if r != 122 || try == 2 { // ERROR_INSUFFICIENT_BUFFER
			return 0, 0, false
		}
	}
	const rowSize, off = 860, 4 // MIB_IFROW boyutu; MIB_IFTABLE başlığı (dwNumEntries)
	n := int(*(*uint32)(unsafe.Pointer(&buf[0])))
	for i := 0; i < n && off+(i+1)*rowSize <= len(buf); i++ {
		row := buf[off+i*rowSize:]
		typ := *(*uint32)(unsafe.Pointer(&row[516]))
		oper := *(*uint32)(unsafe.Pointer(&row[544]))
		if typ == 24 || (oper != 4 && oper != 5) { // loopback ya da bağlı olmayan
			continue
		}
		rx += *(*uint32)(unsafe.Pointer(&row[552]))
		tx += *(*uint32)(unsafe.Pointer(&row[576]))
	}
	return rx, tx, true
}

func newCollector() *collector {
	c := &collector{}
	c.prevIdle, c.prevTotal, _ = readCPU()
	c.prevRx, c.prevTx, c.netOK = readNet()
	c.prevAt = time.Now()
	return c
}

type memStatusEx struct {
	Length                                             uint32
	MemoryLoad                                         uint32
	TotalPhys, AvailPhys, TotalPageFile, AvailPageFile uint64
	TotalVirtual, AvailVirtual, AvailExtendedVirtual   uint64
}

func diskMetrics() []metric {
	mask, _, _ := pGetLogicalDrives.Call()
	var out []metric
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		p, _ := syscall.UTF16PtrFromString(string(rune('A'+i)) + `:\`)
		if t, _, _ := pGetDriveType.Call(uintptr(unsafe.Pointer(p))); t != 3 { // yalnızca sabit diskler
			continue
		}
		var free, total, totalFree uint64
		r, _, _ := pGetDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
		if r == 0 || total == 0 {
			continue
		}
		out = append(out, metric{Name: "host.disk_pct:" + string(rune('A'+i)) + ":", Value: 100 * float64(total-free) / float64(total)})
	}
	if len(out) == 0 {
		return nil
	}
	// host.disk_pct: sistem sürücüsü (genelde C:), yoksa ilk sabit disk.
	sys := strings.ToUpper(strings.TrimSuffix(syscallGetenv("SystemDrive"), `\`))
	main := out[0].Value
	for _, m := range out {
		if strings.EqualFold(strings.TrimPrefix(m.Name, "host.disk_pct:"), sys) {
			main = m.Value
		}
	}
	return append(out, metric{Name: "host.disk_pct", Value: main})
}

func syscallGetenv(k string) string { v, _ := syscall.Getenv(k); return v }

func (c *collector) collect() []metric {
	out := []metric{}
	if idle, total, ok := readCPU(); ok {
		if dt := total - c.prevTotal; dt > 0 && total >= c.prevTotal {
			out = append(out, metric{Name: "host.cpu_pct", Value: 100 * float64(dt-(idle-c.prevIdle)) / float64(dt)})
		}
		c.prevIdle, c.prevTotal = idle, total
	}
	var ms memStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	if r, _, _ := pGlobalMemoryStatus.Call(uintptr(unsafe.Pointer(&ms))); r != 0 && ms.TotalPhys > 0 {
		out = append(out, metric{Name: "host.mem_pct", Value: 100 * float64(ms.TotalPhys-ms.AvailPhys) / float64(ms.TotalPhys)})
		if ms.TotalPageFile > ms.TotalPhys {
			out = append(out, metric{Name: "host.swap_pct", Value: 100 * float64(ms.TotalPageFile-ms.AvailPageFile) / float64(ms.TotalPageFile)})
		}
	}
	if r, _, _ := pGetTickCount64.Call(); r != 0 {
		out = append(out, metric{Name: "host.uptime_s", Value: float64(r) / 1000})
	}
	out = append(out, diskMetrics()...)

	if rx, tx, ok := readNet(); ok && c.netOK {
		now := time.Now()
		if dt := now.Sub(c.prevAt).Seconds(); dt > 0 {
			// 32 bit sayaçlar sarabilir; uint32 farkı doğru sonucu verir.
			out = append(out, metric{Name: "host.net_rx_bps", Value: float64(rx-c.prevRx) * 8 / dt}, metric{Name: "host.net_tx_bps", Value: float64(tx-c.prevTx) * 8 / dt})
		}
		c.prevRx, c.prevTx, c.prevAt = rx, tx, now
	} else if ok {
		c.prevRx, c.prevTx, c.prevAt, c.netOK = rx, tx, time.Now(), true
	}
	out = append(out, metric{Name: "host.procs", Value: float64(totalProcs())})
	return out
}

type procEntry struct {
	Size, Usage, PID        uint32
	_                       uint32
	HeapID                  uintptr
	ModuleID, Threads, PPID uint32
	PriClass                int32
	Flags                   uint32
	Exe                     [260]uint16
}

func snapshotProcs(fn func(name string)) int {
	h, _, _ := pSnapshot.Call(2, 0) // TH32CS_SNAPPROCESS
	if h == 0 || h == ^uintptr(0) {
		return 0
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	var e procEntry
	e.Size = uint32(unsafe.Sizeof(e))
	n := 0
	r, _, _ := pProcFirst.Call(h, uintptr(unsafe.Pointer(&e)))
	for r != 0 {
		n++
		if fn != nil {
			fn(syscall.UTF16ToString(e.Exe[:]))
		}
		r, _, _ = pProcNext.Call(h, uintptr(unsafe.Pointer(&e)))
	}
	return n
}

func totalProcs() int { return snapshotProcs(nil) }

// processCounts süreç adı (küçük harf, .exe'siz) -> adet.
func processCounts() map[string]int {
	m := map[string]int{}
	snapshotProcs(func(name string) { m[strings.TrimSuffix(strings.ToLower(name), ".exe")]++ })
	return m
}
