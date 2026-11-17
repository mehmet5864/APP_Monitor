//go:build linux

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type collector struct {
	prevTotal, prevIdle uint64
	prevRx, prevTx      uint64
	prevAt              time.Time
}

func newCollector() *collector {
	c := &collector{}
	c.prevTotal, c.prevIdle = readCPU()
	c.prevRx, c.prevTx = readNet()
	c.prevAt = time.Now()
	return c
}

func readCPU() (total, idle uint64) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	f := strings.Fields(line)
	for i, s := range f[1:] {
		v, _ := strconv.ParseUint(s, 10, 64)
		total += v
		if i == 3 || i == 4 { // idle + iowait
			idle += v
		}
	}
	return total, idle
}

func readNet() (rx, tx uint64) {
	b, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	for _, ln := range strings.Split(string(b), "\n")[2:] {
		i := strings.IndexByte(ln, ':')
		if i < 0 {
			continue
		}
		name := strings.TrimSpace(ln[:i])
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") {
			continue
		}
		f := strings.Fields(ln[i+1:])
		if len(f) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(f[0], 10, 64)
		t, _ := strconv.ParseUint(f[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}

var realFS = map[string]bool{"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true, "f2fs": true, "vfat": true, "ntfs": true, "ntfs3": true, "exfat": true, "jfs": true, "reiserfs": true}

func diskMetrics() []metric {
	b, err := os.ReadFile("/proc/mounts")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var mounts []string
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) < 3 || !realFS[f[2]] || seen[f[0]] || strings.HasPrefix(f[1], "/boot/efi") {
			continue
		}
		seen[f[0]] = true
		mounts = append(mounts, strings.ReplaceAll(f[1], `\040`, " "))
	}
	sort.Strings(mounts)
	var out []metric
	for i, m := range mounts {
		if i >= 16 {
			break
		}
		var st syscall.Statfs_t
		if syscall.Statfs(m, &st) != nil || st.Blocks == 0 {
			continue
		}
		pct := 100 * float64(st.Blocks-st.Bavail) / float64(st.Blocks)
		out = append(out, metric{Name: "host.disk_pct:" + m, Value: pct})
		if m == "/" {
			out = append(out, metric{Name: "host.disk_pct", Value: pct})
		}
	}
	if len(out) > 0 {
		hasRoot := false
		for _, m := range out {
			if m.Name == "host.disk_pct" {
				hasRoot = true
			}
		}
		if !hasRoot {
			out = append(out, metric{Name: "host.disk_pct", Value: out[0].Value})
		}
	}
	return out
}

func (c *collector) collect() []metric {
	out := []metric{}
	total, idle := readCPU()
	if dt := total - c.prevTotal; dt > 0 && total >= c.prevTotal {
		out = append(out, metric{Name: "host.cpu_pct", Value: 100 * float64(dt-(idle-c.prevIdle)) / float64(dt)})
	}
	c.prevTotal, c.prevIdle = total, idle

	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availKB, swapT, swapF float64
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			v, _ := strconv.ParseFloat(f[1], 64)
			switch f[0] {
			case "MemTotal:":
				totalKB = v
			case "MemAvailable:":
				availKB = v
			case "SwapTotal:":
				swapT = v
			case "SwapFree:":
				swapF = v
			}
		}
		if totalKB > 0 {
			out = append(out, metric{Name: "host.mem_pct", Value: 100 * (totalKB - availKB) / totalKB})
		}
		if swapT > 0 {
			out = append(out, metric{Name: "host.swap_pct", Value: 100 * (swapT - swapF) / swapT})
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			for i, n := range []string{"host.load1", "host.load5", "host.load15"} {
				if v, err := strconv.ParseFloat(f[i], 64); err == nil {
					out = append(out, metric{Name: n, Value: v})
				}
			}
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				out = append(out, metric{Name: "host.uptime_s", Value: v})
			}
		}
	}
	out = append(out, diskMetrics()...)

	rx, tx := readNet()
	now := time.Now()
	if dt := now.Sub(c.prevAt).Seconds(); dt > 0 && rx >= c.prevRx && tx >= c.prevTx {
		out = append(out, metric{Name: "host.net_rx_bps", Value: float64(rx-c.prevRx) * 8 / dt}, metric{Name: "host.net_tx_bps", Value: float64(tx-c.prevTx) * 8 / dt})
	}
	c.prevRx, c.prevTx, c.prevAt = rx, tx, now

	if ents, err := filepath.Glob("/proc/[0-9]*"); err == nil {
		out = append(out, metric{Name: "host.procs", Value: float64(len(ents))})
	}
	return out
}

// processCounts süreç adı (küçük harf) -> adet.
func processCounts() map[string]int {
	m := map[string]int{}
	ents, _ := filepath.Glob("/proc/[0-9]*/comm")
	for _, p := range ents {
		if b, err := os.ReadFile(p); err == nil {
			m[strings.ToLower(strings.TrimSpace(string(b)))]++
		}
	}
	return m
}
