package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMaskLine(t *testing.T) {
	cases := map[string]string{
		"login failed password=hunter2 user=ali":                         "password=***",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123456789ABCD": "***",
		"api_key: SECRETVALUE end":                                       "api_key: ***",
	}
	for in, want := range cases {
		got := maskLine(in)
		if !strings.Contains(got, want) || strings.Contains(got, "hunter2") || strings.Contains(got, "SECRETVALUE") || strings.Contains(got, "abcdefghijklmnopqrstuvwxyz") {
			t.Errorf("maskLine(%q) = %q", in, got)
		}
	}
	long := maskLine(strings.Repeat("x ", 300))
	if n := len([]rune(long)); n > 201 {
		t.Errorf("örnek 200 karakterle sınırlı olmalı: %d", n)
	}
}

func TestSafeName(t *testing.T) {
	if safeName("Java:My App.jar") != "java_my_app.jar" || safeName("///") != "" || len(safeName(strings.Repeat("a", 90))) != 40 {
		t.Fatalf("safeName beklenmedik: %q", safeName("Java:My App.jar"))
	}
}

func TestAggregateCPUAndMemory(t *testing.T) {
	s := newAppScanner()
	t0 := time.Now()
	s.aggregate([]procSample{{pid: 1, name: "nginx", cpuSec: 10, rssKB: 2048}, {pid: 2, name: "nginx", cpuSec: 5, rssKB: 1024}, {pid: 3, name: "db", cpuSec: 1, rssKB: 4096}}, t0)
	got := s.aggregate([]procSample{{pid: 1, name: "nginx", cpuSec: 12, rssKB: 2048}, {pid: 2, name: "nginx", cpuSec: 6, rssKB: 1024}, {pid: 4, name: "yeni", cpuSec: 100, rssKB: 512}}, t0.Add(10*time.Second))
	n := got["nginx"]
	if n == nil || n.Count != 2 || n.MemMB != 3 || n.CPU < 29.9 || n.CPU > 30.1 { // (2+1) sn / 10 sn = %30
		t.Fatalf("nginx: %+v", n)
	}
	if y := got["yeni"]; y == nil || y.CPU != 0 { // önceki örnek yok: CPU hesaplanmaz
		t.Fatalf("yeni süreç CPU'su 0 olmalı: %+v", y)
	}
	if got["db"] != nil {
		t.Fatal("kapanan süreç listede kalmamalı")
	}
}

func TestAutoLogCountsWarnsAndSample(t *testing.T) {
	p := filepath.Join(t.TempDir(), "error.log")
	os.WriteFile(p, []byte("eski error satırı\n"), 0o600)
	w := &logWatch{name: "auto_x", path: p, re: errLineRe, warnRe: warnLineRe, sampleOn: true}
	w.poll() // geçmiş sayılmaz
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("[warn] yavaş\n[error] bağlantı failed password=abc123\n[info] tamam\n[crit] disk\n")
	f.Close()
	m, l, ok := w.poll()
	if !ok || m != 2 || l != 4 || w.lastWarn != 1 || !w.readable {
		t.Fatalf("hata=%d satır=%d uyarı=%d ok=%v", m, l, w.lastWarn, ok)
	}
	if w.sample != "[crit] disk" || strings.Contains(w.sample, "abc123") {
		t.Fatalf("örnek son hata satırı olmalı: %q", w.sample)
	}
	os.Remove(p)
	if _, _, ok := w.poll(); ok || w.readable {
		t.Fatal("dosya yoksa okunamıyor olmalı")
	}
}

func TestAutoLogsRefreshUsesRunningApps(t *testing.T) {
	a := newAutoLogs()
	a.refresh(map[string]bool{}) // hiçbir uygulama yok: yalnızca "always" tanımlar denenir; yoksa boş kalır
	for app := range a.w {
		if app != "system" {
			t.Fatalf("çalışmayan uygulama için izleyici açılmamalı: %s", app)
		}
	}
}
