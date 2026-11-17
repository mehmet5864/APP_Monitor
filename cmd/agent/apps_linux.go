//go:build linux

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// appLabel yorumlayıcı/çalışma zamanı süreçlerini (java, python, node...) betik ya da jar adıyla ayırır.
func appLabel(comm string, args []string) string {
	base := func(s string) string {
		s = filepath.Base(s)
		if len(s) > 28 {
			s = s[:28]
		}
		return s
	}
	switch {
	case comm == "java" || strings.HasPrefix(comm, "java"):
		for i, a := range args {
			if a == "-jar" && i+1 < len(args) {
				return "java:" + base(args[i+1])
			}
		}
		for i := 1; i < len(args); i++ {
			a := args[i]
			if strings.HasPrefix(a, "-") {
				if a == "-cp" || a == "-classpath" || a == "--class-path" {
					i++
				}
				continue
			}
			if i > 0 {
				return "java:" + base(strings.ReplaceAll(a, ".", "/"))
			}
		}
	case strings.HasPrefix(comm, "python") || comm == "node" || comm == "nodejs" || comm == "ruby" || comm == "perl" || comm == "php":
		for i := 1; i < len(args); i++ {
			if !strings.HasPrefix(args[i], "-") {
				return comm + ":" + base(args[i])
			}
		}
	}
	return comm
}

func readProcs() []procSample {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	page := uint64(os.Getpagesize())
	var out []procSample
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(b)
		l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if l < 0 || r < l || r+2 > len(s) {
			continue
		}
		comm := strings.ToLower(s[l+1 : r])
		f := strings.Fields(s[r+2:])
		if len(f) < 22 {
			continue
		}
		rss, _ := strconv.ParseUint(f[21], 10, 64)
		if rss == 0 { // çekirdek iş parçacığı
			continue
		}
		ut, _ := strconv.ParseUint(f[11], 10, 64)
		st, _ := strconv.ParseUint(f[12], 10, 64)
		name := comm
		switch {
		case comm == "java" || strings.HasPrefix(comm, "python") || comm == "node" || comm == "nodejs" || comm == "ruby" || comm == "perl" || comm == "php":
			if cb, err := os.ReadFile("/proc/" + e.Name() + "/cmdline"); err == nil {
				name = strings.ToLower(appLabel(comm, strings.Split(strings.TrimRight(string(cb), "\x00"), "\x00")))
			}
		}
		out = append(out, procSample{pid: pid, name: name, cpuSec: float64(ut+st) / 100, rssKB: rss * page / 1024})
	}
	return out
}

// listenPorts dinleyen TCP portlarını süreç kimliğine eşler (başka kullanıcıların fd dizinleri için root gerekir).
func listenPorts() map[int][]int {
	inodes := map[string]int{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for i, ln := range strings.Split(string(b), "\n") {
			if i == 0 {
				continue
			}
			p := strings.Fields(ln)
			if len(p) < 10 || p[3] != "0A" {
				continue
			}
			if c := strings.LastIndexByte(p[1], ':'); c >= 0 {
				if port, err := strconv.ParseUint(p[1][c+1:], 16, 32); err == nil {
					inodes[p[9]] = int(port)
				}
			}
		}
	}
	out := map[int][]int{}
	if len(inodes) == 0 {
		return out
	}
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
			if port, ok := inodes[strings.TrimSuffix(strings.TrimPrefix(ln, "socket:["), "]")]; ok {
				out[pid] = append(out[pid], port)
			}
		}
	}
	return out
}

// listServices çalışan ve hatalı systemd servislerini döndürür.
func listServices() []appInfo {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "systemctl", "list-units", "--type=service", "--state=running,failed", "--no-legend", "--plain", "--no-pager").Output()
	if err != nil {
		return nil
	}
	var out []appInfo
	for _, ln := range strings.Split(string(b), "\n") {
		p := strings.Fields(ln)
		if len(p) < 4 || !strings.HasSuffix(p[0], ".service") {
			continue
		}
		out = append(out, appInfo{Name: strings.TrimSuffix(p[0], ".service"), Kind: "service", State: p[3]})
	}
	return out
}

func autoLogDefs() []autoLogDef {
	return []autoLogDef{
		{app: "nginx", procs: []string{"nginx"}, paths: []string{"/var/log/nginx/error.log"}},
		{app: "httpd", procs: []string{"httpd", "apache2"}, paths: []string{"/var/log/httpd/error_log", "/var/log/apache2/error.log"}},
		{app: "postgresql", procs: []string{"postgres", "postmaster"}, paths: []string{"/var/log/postgresql/*.log", "/var/lib/pgsql/data/log/*.log", "/var/lib/pgsql/*/data/log/*.log"}},
		{app: "mysql", procs: []string{"mysqld", "mariadbd"}, paths: []string{"/var/log/mysql/error.log", "/var/log/mariadb/mariadb.log", "/var/log/mysqld.log"}},
		{app: "redis", procs: []string{"redis-server"}, paths: []string{"/var/log/redis/redis-server.log", "/var/log/redis/redis.log"}},
		{app: "mongodb", procs: []string{"mongod"}, paths: []string{"/var/log/mongodb/mongod.log"}},
		{app: "sshd", procs: []string{"sshd"}, paths: []string{"/var/log/secure", "/var/log/auth.log"}},
		{app: "system", always: true, paths: []string{"/var/log/messages", "/var/log/syslog"}},
	}
}

// pickLogPath desenlerden ilk eşleşeni seçer; birden çok dosya varsa en yeni değiştirileni.
func pickLogPath(patterns []string) string {
	for _, pat := range patterns {
		ms, _ := filepath.Glob(pat)
		best, bt := "", time.Time{}
		for _, m := range ms {
			if fi, err := os.Stat(m); err == nil && fi.Mode().IsRegular() && (best == "" || fi.ModTime().After(bt)) {
				best, bt = m, fi.ModTime()
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}
