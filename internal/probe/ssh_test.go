package probe

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleOut = `cpu  1000 0 500 8000 100 0 0 0 0 0
cpu  1100 0 550 8300 100 0 0 0 0 0
---MEM
MemTotal:       16000000 kB
MemFree:         1000000 kB
MemAvailable:    4000000 kB
Buffers:          100000 kB
Cached:          2000000 kB
---UP
12345.67 40000.00
---DF
Filesystem     Type     1024-blocks      Used Available Capacity Mounted on
/dev/mapper/rhel-root xfs 52428800 26214400 26214400 50% /
/dev/sda1      xfs         1048576    524288    524288  50% /boot
/dev/mapper/rhel-var xfs  104857600  94371840  10485760  90% /var
/dev/sdb1      ext4       2097152    1024    2096128   1% /mnt/my data
tmpfs          tmpfs       100000     0   100000   0% /dev/shm
`

func TestParseSSHOutput(t *testing.T) {
	m, err := parseSSHOutput(sampleOut, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// cpu: dtotal=450 didle=300 -> %33.33
	if v := m["host.cpu_pct"]; v < 33 || v > 33.4 {
		t.Errorf("cpu=%v", v)
	}
	if v := m["host.mem_pct"]; v != 75 {
		t.Errorf("mem=%v", v)
	}
	if v := m["host.disk_pct"]; v != 90 {
		t.Errorf("disk max=%v", v)
	}
	if v := m["host.disk_pct./var"]; v != 90 {
		t.Errorf("/var=%v", v)
	}
	if v := m["host.disk_used_gb./"]; v != 25 {
		t.Errorf("/ used=%v", v)
	}
	if v := m["host.disk_total_gb./"]; v != 50 {
		t.Errorf("/ total=%v", v)
	}
	if _, ok := m["host.disk_pct./mnt/my_data"]; !ok {
		t.Errorf("boşluklu bağlama noktası yok: %v", m)
	}
	for k := range m {
		if strings.Contains(k, "shm") {
			t.Errorf("sahte dosya sistemi sızdı: %s", k)
		}
	}
	if m["host.uptime_sec"] != 12345.67 {
		t.Errorf("uptime=%v", m["host.uptime_sec"])
	}
	// yalnızca disk istendiğinde cpu/mem yok
	m2, _ := parseSSHOutput(sampleOut, Options{Metrics: []string{"disk"}})
	if _, ok := m2["host.cpu_pct"]; ok {
		t.Error("cpu istenmedi")
	}
	if _, err := parseSSHOutput("saçma", Options{}); err == nil {
		t.Error("boş çıktı hata vermeli")
	}
}

func TestParseDFWithoutType(t *testing.T) {
	r := parseDF([]string{"Filesystem 1024-blocks Used Available Capacity Mounted on", "/dev/sda1 1000 250 750 25% /"})
	if r["/"].pct != 25 {
		t.Fatalf("%v", r)
	}
}

func TestSSHTargetAndFingerprint(t *testing.T) {
	for _, c := range []struct {
		in, h, p string
		ok       bool
	}{{"10.0.0.5", "10.0.0.5", "22", true}, {"srv.local:2222", "srv.local", "2222", true}, {"[::1]:22", "::1", "22", true},
		{"-oProxyCommand=x", "", "", false}, {"a b", "", "", false}, {"h:99999", "", "", false}, {"", "", "", false}} {
		h, p, err := SplitSSHTarget(c.in)
		if (err == nil) != c.ok || (c.ok && (h != c.h || p != c.p)) {
			t.Errorf("%q -> %q %q %v", c.in, h, p, err)
		}
	}
	if ValidSSHUser("root; rm") || ValidSSHUser("-oX") || !ValidSSHUser("appmon") {
		t.Error("kullanıcı doğrulaması hatalı")
	}
	// ssh-keygen: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIA== örneği değil; yalnızca biçim
	if fp := KeyFingerprint("ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"); !strings.HasPrefix(fp, "ssh-ed25519 SHA256:") {
		t.Errorf("fp=%q", fp)
	}
}

func fakeSSH(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "ssh")
	os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755)
	old := SSHBin
	SSHBin = p
	t.Cleanup(func() { SSHBin = old })
}

func TestSSHProbeEndToEndWithFakeSSH(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.txt")
	os.WriteFile(out, []byte(sampleOut), 0o600)
	// UserKnownHostsFile=... argümanını bulup bir anahtar satırı yazar; argümanları kaydeder.
	argsLog := filepath.Join(t.TempDir(), "args.txt")
	fakeSSH(t, `printf '%s\n' "$@" > `+argsLog+`
for a in "$@"; do case "$a" in UserKnownHostsFile=*) kh="${a#UserKnownHostsFile=}";; esac; done
if [ ! -s "$kh" ]; then echo "10.0.0.5 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl" >> "$kh"; fi
cat `+out+`
`)
	var learned string
	spec := Spec{Type: "host", Target: "10.0.0.5", Timeout: 5 * time.Second,
		Opts:      Options{Method: "ssh", SSHUser: "appmon", SSHKey: []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----")},
		OnHostKey: func(l string) { learned = l }}
	m, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if m["host.disk_pct"] != 90 || learned == "" || !strings.HasPrefix(learned, "ssh-ed25519 AAAA") {
		t.Fatalf("m=%v learned=%q", m, learned)
	}
	a, _ := os.ReadFile(argsLog)
	for _, want := range []string{"StrictHostKeyChecking=accept-new", "BatchMode=yes", "-l\nappmon", "--\n10.0.0.5"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("argüman yok: %q\n%s", want, a)
		}
	}
	// sabitlenmiş anahtarla: strict=yes ve geri çağırma yok
	learned = ""
	spec.Opts.HostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	if _, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false); err != nil {
		t.Fatal(err)
	}
	a, _ = os.ReadFile(argsLog)
	if !strings.Contains(string(a), "StrictHostKeyChecking=yes") || learned != "" {
		t.Errorf("sabitleme çalışmadı: %s learned=%q", a, learned)
	}
}

func TestSSHProbeErrors(t *testing.T) {
	ctx := context.Background()
	base := Spec{Type: "host", Target: "10.0.0.5", Timeout: 3 * time.Second, Opts: Options{Method: "ssh", SSHUser: "u", SSHKey: []byte("k")}}
	cases := map[string]string{
		"echo 'user@h: Permission denied (publickey).' >&2; exit 255":             "kimlik doğrulaması reddedildi",
		"echo 'ssh: connect to host h port 22: Connection refused' >&2; exit 255": "reddedildi",
		"echo 'Host key verification failed.' >&2; exit 255":                      "sabitlenen anahtarla uyuşmuyor",
		"echo 'Load key: error in libcrypto' >&2; exit 255":                       "okunamadı",
	}
	for body, want := range cases {
		fakeSSH(t, body)
		s := base
		s.Opts.HostKey = "ssh-ed25519 AAAA"
		_, err := sshHostProbe(ctx, &net.Dialer{}, s, false)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q -> %v (beklenen %q)", body, err, want)
		}
	}
	s := base
	s.Opts.SSHKey = nil
	if _, err := sshHostProbe(ctx, &net.Dialer{}, s, false); err == nil {
		t.Error("anahtarsız çalışmamalı")
	}
	s = base
	s.Opts.SSHUser = "x;y"
	if _, err := sshHostProbe(ctx, &net.Dialer{}, s, false); err == nil {
		t.Error("kötü kullanıcı reddedilmeli")
	}
	// SaaS: özel ağ engeli
	fakeSSH(t, "exit 0")
	if _, err := sshHostProbe(ctx, &net.Dialer{}, base, true); err == nil {
		t.Error("blok modunda özel IP reddedilmeli")
	}
}

func TestSSHProbePasswordAuth(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.txt")
	os.WriteFile(out, []byte(sampleOut), 0o600)
	argsLog := filepath.Join(t.TempDir(), "args.txt")
	// askpass betiğini çalıştırıp parolayı ve ortam değişkenlerini kaydeder (gerçek ssh'ın yaptığı gibi).
	fakeSSH(t, `printf '%s\n' "$@" > `+argsLog+`
echo "ASKPASS=$SSH_ASKPASS REQ=$SSH_ASKPASS_REQUIRE" >> `+argsLog+`
echo "PW=$("$SSH_ASKPASS")" >> `+argsLog+`
cat `+out+`
`)
	spec := Spec{Type: "host", Target: "10.0.0.5", Timeout: 5 * time.Second,
		Opts: Options{Method: "ssh", SSHUser: "appmon", SSHPass: []byte("p@ss w0rd'x")}}
	m, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false)
	if err != nil || m["host.disk_pct"] != 90 {
		t.Fatalf("m=%v err=%v", m, err)
	}
	a, _ := os.ReadFile(argsLog)
	for _, want := range []string{"PasswordAuthentication=yes", "BatchMode=no", "PubkeyAuthentication=no", "REQ=force", "PW=p@ss w0rd'x"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("yok: %q\n%s", want, a)
		}
	}
	if strings.Contains(string(a), "-i\n") {
		t.Error("parola modunda anahtar dosyası verilmemeli")
	}
	for _, ln := range strings.Split(string(a), "\n") {
		if strings.Contains(ln, "p@ss") && !strings.HasPrefix(ln, "PW=") {
			t.Errorf("parola argümanlarda sızdı: %q", ln)
		}
	}
}

func TestSSHLegacyOptionAndNegotiationHint(t *testing.T) {
	argsLog := filepath.Join(t.TempDir(), "args.txt")
	out := filepath.Join(t.TempDir(), "out.txt")
	os.WriteFile(out, []byte(sampleOut), 0o600)
	fakeSSH(t, `printf '%s\n' "$@" > `+argsLog+`
cat `+out+`
`)
	spec := Spec{Type: "host", Target: "10.0.0.5", Timeout: 5 * time.Second,
		Opts: Options{Method: "ssh", SSHUser: "appmon", SSHKey: []byte("k"), HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}}
	if _, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(argsLog)
	if strings.Contains(string(a), "diffie-hellman-group1-sha1") || strings.Contains(string(a), "ssh-rsa") {
		t.Fatalf("eski algoritmalar varsayılan olarak açık olmamalı:\n%s", a)
	}
	spec.Opts.SSHLegacy = true
	if _, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false); err != nil {
		t.Fatal(err)
	}
	a, _ = os.ReadFile(argsLog)
	for _, want := range []string{"KexAlgorithms=+diffie-hellman-group14-sha1,diffie-hellman-group1-sha1", "HostKeyAlgorithms=+ssh-rsa,ssh-dss", "PubkeyAcceptedKeyTypes=+ssh-rsa", "Ciphers=+aes128-cbc", "MACs=+hmac-sha1"} {
		if !strings.Contains(string(a), want) {
			t.Errorf("argüman yok: %q\n%s", want, a)
		}
	}
	msg := "Unable to negotiate with 10.0.0.5 port 22: no matching key exchange method found. Their offer: diffie-hellman-group1-sha1\n"
	e1 := sshError(msg, errors.New("exit 255"), false, false)
	if !strings.Contains(e1.Error(), "Eski SSH sunucusu uyumluluğu") || !strings.Contains(e1.Error(), "diffie-hellman-group1-sha1") {
		t.Errorf("ipucu eksik: %v", e1)
	}
	e2 := sshError(msg, errors.New("exit 255"), false, true)
	if !strings.Contains(e2.Error(), "update-crypto-policies") {
		t.Errorf("legacy açıkken politika ipucu eksik: %v", e2)
	}
}

func TestSSHRemoteExitCodeTolerated(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.txt")
	os.WriteFile(out, []byte(sampleOut), 0o600)
	// uzaktaki son komut hata verdi (ssh çıkış kodu 4) ama ölçümler geldi: başarılı sayılmalı
	fakeSSH(t, "cat "+out+"; exit 4")
	spec := Spec{Type: "host", Target: "10.0.0.5", Timeout: 5 * time.Second,
		Opts: Options{Method: "ssh", SSHUser: "u", SSHKey: []byte("k"), HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}}
	m, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false)
	if err != nil || m["host.disk_pct"] != 90 {
		t.Fatalf("m=%v err=%v", m, err)
	}
	// çıktı yoksa anlaşılır mesaj
	fakeSSH(t, "exit 4")
	if _, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false); err == nil || !strings.Contains(err.Error(), "çıkış kodu 4") {
		t.Fatalf("beklenen çıkış kodu mesajı: %v", err)
	}
	// 255 = ssh'ın kendi hatası: tolere edilmez
	fakeSSH(t, "cat "+out+"; echo 'Permission denied (publickey).' >&2; exit 255")
	if _, err := sshHostProbe(context.Background(), &net.Dialer{}, spec, false); err == nil {
		t.Fatal("255 hata sayılmalı")
	}
	if !strings.HasSuffix(sshScript, "; true") || !strings.HasSuffix(buildSSHScript(Options{Collect: []string{"cpu"}}), "; true") {
		t.Error("betik her zaman 0 ile bitmeli")
	}
}

func TestSSHCollectSelection(t *testing.T) {
	// varsayılan (eski) betik: load ve swap yok, bağlantı/dinleyen/adres var
	def := buildSSHScript(Options{})
	for _, w := range []string{"---MEM", "---UP", "---DF", "---CONN", "---LISTEN", "---ADDR"} {
		if !strings.Contains(def, w) {
			t.Errorf("varsayılan betikte %s yok", w)
		}
	}
	if strings.Contains(def, "LOAD") || strings.Contains(def, "SWAP") || strings.Contains(def, "'") {
		t.Errorf("varsayılan betik beklenmedik: %s", def)
	}
	// seçili: yalnızca cpu + load + swap
	o := Options{Collect: []string{"cpu", "load", "swap"}}
	sc := buildSSHScript(o)
	for _, w := range []string{"/proc/stat", "---LOAD", "---SWAP"} {
		if !strings.Contains(sc, w) {
			t.Errorf("betikte %s yok: %s", w, sc)
		}
	}
	for _, w := range []string{"---MEM", "---DF", "---CONN", "---LISTEN", "---ADDR", "---UP", "hostname"} {
		if strings.Contains(sc, w) {
			t.Errorf("seçilmeyen komut betikte: %s", w)
		}
	}
	if !strings.HasSuffix(sc, "; true") || strings.Contains(sc, "'") {
		t.Errorf("betik sonu/tırnak: %s", sc)
	}
	out := "cpu  100 0 100 800 0 0 0 0 0 0\ncpu  200 0 200 1600 0 0 0 0 0 0\n---SWAP\nSwapTotal:       2097152 kB\nSwapFree:        1048576 kB\n---LOAD\n0.52 0.40 0.33 1/200 1234\n"
	m, err := parseSSHOutput(out, o)
	if err != nil {
		t.Fatal(err)
	}
	if m["host.load1"] != 0.52 || m["host.load15"] != 0.33 || m["host.swap_pct"] != 50 || m["host.swap_total_gb"] != 2 {
		t.Fatalf("m=%v", m)
	}
	if _, ok := m["host.mem_pct"]; ok {
		t.Error("seçilmeyen mem yazılmamalı")
	}
	// yalnızca çalışma süresi seçilince hata vermemeli
	if m2, err := parseSSHOutput("---UP\n1000.5 900\n", Options{Collect: []string{"uptime"}}); err != nil || m2["host.uptime_sec"] != 1000.5 {
		t.Errorf("uptime tek başına: %v %v", m2, err)
	}
	// çekirdek veri seçili ama çıktı bozuksa hata
	if _, err := parseSSHOutput("saçma\n", Options{Collect: []string{"cpu", "mem"}}); err == nil {
		t.Error("çözümlenemeyen çıktı hata vermeli")
	}
}

func TestSSHMountFilter(t *testing.T) {
	df := "---DF\nFilesystem     Type 1024-blocks Used Available Capacity Mounted on\n/dev/sda1      xfs   10485760 5242880 5242880 50% /\n/dev/sdb1      xfs   20971520 2097152 18874368 10% /var\n/dev/sdc1      xfs   20971520 18874368 2097152 90% /data\n"
	out := "cpu  100 0 100 800 0 0 0 0 0 0\ncpu  200 0 200 1600 0 0 0 0 0 0\n" + df
	m, err := parseSSHOutput(out, Options{Collect: []string{"cpu", "disk"}, Mounts: []string{"/", "/data"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["host.disk_pct./var"]; ok {
		t.Errorf("/var filtrelenmeliydi: %v", m)
	}
	if m["host.disk_pct./data"] != 90 || m["host.disk_pct./"] != 50 || m["host.disk_pct"] != 90 {
		t.Errorf("m=%v", m)
	}
	all, _ := parseSSHOutput(out, Options{Collect: []string{"cpu", "disk"}})
	if _, ok := all["host.disk_pct./var"]; !ok {
		t.Error("filtre boşken tüm dizinler gelmeli")
	}
}
