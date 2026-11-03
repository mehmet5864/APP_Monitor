//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

type collector struct{ prevTotal, prevIdle uint64 }

func newCollector() *collector {
	c := &collector{}
	c.prevTotal, c.prevIdle = readCPU()
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

func (c *collector) collect() []metric {
	out := []metric{}
	total, idle := readCPU()
	if dt := total - c.prevTotal; dt > 0 && total >= c.prevTotal {
		out = append(out, metric{"host.cpu_pct", 100 * float64(dt-(idle-c.prevIdle)) / float64(dt)})
	}
	c.prevTotal, c.prevIdle = total, idle

	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		var totalKB, availKB float64
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
			}
		}
		if totalKB > 0 {
			out = append(out, metric{"host.mem_pct", 100 * (totalKB - availKB) / totalKB})
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				out = append(out, metric{"host.load1", v})
			}
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				out = append(out, metric{"host.uptime_s", v})
			}
		}
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs("/", &st); err == nil && st.Blocks > 0 {
		out = append(out, metric{"host.disk_pct", 100 * float64(st.Blocks-st.Bavail) / float64(st.Blocks)})
	}
	return out
}
