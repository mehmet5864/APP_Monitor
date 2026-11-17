package probe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// SSHBin ssh istemcisinin yoludur (boş = PATH'te "ssh"). Testlerde sahte betikle değiştirilir.
var SSHBin = ""

// SSHCollectKeys SSH ile toplanabilen veri gruplarıdır (probe seçeneği "collect").
var SSHCollectKeys = []string{"cpu", "mem", "swap", "disk", "uptime", "load", "conns", "listen", "addr"}

// wantSSH bir veri grubunun toplanıp toplanmayacağını söyler. "collect" boşsa eski davranış:
// cpu/mem/disk "metrics" seçimine uyar; uptime, conns, listen, addr her zaman toplanır; load ve swap kapalıdır.
func wantSSH(o Options, k string) bool {
	if len(o.Collect) > 0 {
		for _, x := range o.Collect {
			if x == k {
				return true
			}
		}
		return false
	}
	switch k {
	case "cpu", "mem", "disk":
		return want(o, k)
	case "uptime", "conns", "listen", "addr":
		return true
	}
	return false
}

// buildSSHScript uzak makinede çalışan SABİT, salt-okunur komutu kurar; yalnızca seçilen veri grupları için
// komut eklenir ve kullanıcıdan hiçbir girdi içermez. Tek tırnak içermez (dış sh -c '...' için); son komut
// "true" olduğu için eski makinelerde son komutun hatası çıkış kodunu bozmaz.
func buildSSHScript(o Options) string {
	var p []string
	if wantSSH(o, "cpu") {
		p = append(p, "head -n1 /proc/stat; sleep 1; head -n1 /proc/stat")
	}
	if wantSSH(o, "mem") {
		p = append(p, `echo ---MEM; grep -E "^(MemTotal|MemAvailable|MemFree|Buffers|Cached):" /proc/meminfo`)
	}
	if wantSSH(o, "swap") {
		p = append(p, `echo ---SWAP; grep -E "^(SwapTotal|SwapFree):" /proc/meminfo`)
	}
	if wantSSH(o, "uptime") {
		p = append(p, "echo ---UP; cat /proc/uptime")
	}
	if wantSSH(o, "load") {
		p = append(p, "echo ---LOAD; cat /proc/loadavg")
	}
	if wantSSH(o, "disk") {
		p = append(p, "echo ---DF; df -PkT -x tmpfs -x devtmpfs -x squashfs -x overlay -x efivarfs -x proc -x sysfs 2>/dev/null || df -Pk")
	}
	if wantSSH(o, "conns") {
		p = append(p, "echo ---CONN; ss -tni state established 2>/dev/null | head -n 6000")
	}
	if wantSSH(o, "listen") {
		p = append(p, "echo ---LISTEN; ss -tln 2>/dev/null | head -n 500")
	}
	if wantSSH(o, "addr") {
		p = append(p, "echo ---ADDR; hostname -I 2>/dev/null")
	}
	return strings.Join(append(p, "true"), "; ")
}

// sshScript varsayılan (eski) seçimle kurulan betiktir.
var sshScript = buildSSHScript(Options{})

var (
	sshUserRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,31}$`)
	sshHostRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
)

// ValidSSHUser kullanıcı adının güvenli olup olmadığını söyler.
func ValidSSHUser(u string) bool { return sshUserRe.MatchString(u) }

// SplitSSHTarget "host" veya "host:port" biçimini ayırır (IPv6 için [::1]:22).
func SplitSSHTarget(t string) (host, port string, err error) {
	t = strings.TrimSpace(t)
	host, port = t, "22"
	if strings.HasPrefix(t, "[") {
		h, p, e := net.SplitHostPort(t)
		if e != nil {
			return "", "", errors.New("geçersiz SSH hedefi")
		}
		host, port = h, p
	} else if strings.Count(t, ":") == 1 {
		host, port, _ = strings.Cut(t, ":")
	}
	if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 {
		return "", "", errors.New("geçersiz SSH portu")
	}
	if host == "" || (!sshHostRe.MatchString(host) && net.ParseIP(host) == nil) {
		return "", "", errors.New("geçersiz SSH hedefi")
	}
	return host, port, nil
}

// KeyFingerprint "ssh-ed25519 AAAA..." satırının SHA256 parmak izini verir (ssh-keygen -l biçimi).
func KeyFingerprint(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(f[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return f[0] + " SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func sshHostProbe(ctx context.Context, d *net.Dialer, spec Spec, block bool) (map[string]float64, error) {
	o := spec.Opts
	if !ValidSSHUser(o.SSHUser) {
		return nil, errors.New("SSH kullanıcı adı geçersiz veya boş")
	}
	usePass := len(o.SSHKey) == 0 && len(o.SSHPass) > 0
	if len(o.SSHKey) == 0 && !usePass {
		return nil, errors.New("SSH anahtarı/parolası kasadan alınamadı (kayıt seçilmemiş, silinmiş veya ana anahtar eşleşmiyor)")
	}
	host, port, err := SplitSSHTarget(spec.Target)
	if err != nil {
		return nil, err
	}
	// Adres bir kez çözülür ve doğrudan IP'ye bağlanılır (DNS rebinding'e karşı).
	ip, err := resolveIP(ctx, host, block)
	if err != nil {
		return nil, err
	}
	bin := SSHBin
	if bin == "" {
		p, e := exec.LookPath("ssh")
		if e != nil {
			return nil, errors.New("ssh istemcisi bulunamadı: sunucuya OpenSSH istemcisi kurun (RHEL: dnf install openssh-clients)")
		}
		bin = p
	}
	dir, err := os.MkdirTemp("", "appmon-ssh-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	keyFile := filepath.Join(dir, "id")
	if !usePass {
		key := o.SSHKey
		if !bytes.HasSuffix(key, []byte("\n")) {
			key = append(append([]byte{}, key...), '\n')
		}
		if err := os.WriteFile(keyFile, key, 0o600); err != nil {
			return nil, err
		}
	}
	// Parola: SSH_ASKPASS betiği 0600 bir dosyadan okur (komut satırında/ortamda görünmez).
	askpass := filepath.Join(dir, "askpass.sh")
	if usePass {
		if err := os.WriteFile(filepath.Join(dir, "pw"), append(append([]byte{}, o.SSHPass...), '\n'), 0o600); err != nil {
			return nil, err
		}
		if err := os.WriteFile(askpass, []byte("#!/bin/sh\nexec cat '"+filepath.Join(dir, "pw")+"'\n"), 0o700); err != nil {
			return nil, err
		}
	}
	kh := filepath.Join(dir, "known_hosts")
	pinned := strings.TrimSpace(o.HostKey)
	strict := "accept-new"
	if pinned != "" {
		strict = "yes"
		if err := os.WriteFile(kh, []byte("* "+pinned+"\n"), 0o600); err != nil {
			return nil, err
		}
	} else if err := os.WriteFile(kh, nil, 0o600); err != nil {
		return nil, err
	}
	secs := int(spec.Timeout.Seconds())
	if secs < 5 {
		secs = 5
	}
	authArgs := []string{"-i", keyFile, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "PreferredAuthentications=publickey",
		"-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no"}
	if usePass {
		authArgs = []string{"-o", "BatchMode=no", "-o", "PreferredAuthentications=password,keyboard-interactive",
			"-o", "PasswordAuthentication=yes", "-o", "KbdInteractiveAuthentication=yes", "-o", "NumberOfPasswordPrompts=1",
			"-o", "PubkeyAuthentication=no"}
	}
	args := []string{"-F", "/dev/null", "-p", port, "-l", o.SSHUser}
	args = append(args, authArgs...)
	if o.SSHLegacy {
		args = append(args, sshLegacyArgs...)
	}
	args = append(args,
		"-o", "StrictHostKeyChecking="+strict, "-o", "UserKnownHostsFile="+kh, "-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "HashKnownHosts=no", "-o", "UpdateHostKeys=no", "-o", "CheckHostIP=no",
		"-o", "ConnectTimeout="+strconv.Itoa(secs), "-o", "LogLevel=ERROR",
		"-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-T",
		"--", ip.String(),
		"env LC_ALL=C sh -c '"+buildSSHScript(o)+"'")
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "LC_ALL=C"}
	if usePass {
		// Terminalsiz (yeni oturum) çalışınca ssh parolayı SSH_ASKPASS'tan ister; DISPLAY eski sürümler için gerekir.
		cmd.Env = append(cmd.Env, "SSH_ASKPASS="+askpass, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=appmon:0")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}
	var stdout, stderr limitBuf
	stdout.max, stderr.max = 256<<10, 8<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	remoteExit := 0
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("SSH zaman aşımı")
		}
		// ssh kendi hatalarında 255 döner; başka kod uzaktaki komutun çıkış kodudur (eski makinelerde son
		// komut, ör. hostname -I, hata verebilir). Çıktı geldiyse ölçümler yine de okunur.
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() != 255 && ee.ExitCode() > 0 && stdout.b.Len() > 0 {
			remoteExit = ee.ExitCode()
		} else {
			return nil, sshError(stderr.String(), err, pinned != "", o.SSHLegacy)
		}
	}
	if pinned == "" && spec.OnHostKey != nil {
		if b, e := os.ReadFile(kh); e == nil {
			for _, ln := range strings.Split(string(b), "\n") {
				f := strings.Fields(ln)
				if len(f) >= 3 {
					spec.OnHostKey(f[len(f)-2] + " " + f[len(f)-1])
					break
				}
			}
		}
	}
	if spec.ConnOut != nil {
		*spec.ConnOut = *fillConns(stdout.String())
		spec.ConnOut.SkipConns = !wantSSH(o, "conns")
		spec.ConnOut.SkipAddrs = !wantSSH(o, "addr")
		if !wantSSH(o, "listen") {
			spec.ConnOut.ListenOK = false
		}
	}
	m, perr := parseSSHOutput(stdout.String(), o)
	if perr != nil && remoteExit != 0 {
		return nil, fmt.Errorf("uzak makinedeki komut hata verdi (çıkış kodu %d) ve ölçümler okunamadı: %v (kullanıcının /proc ve df okuyabildiğinden emin olun)", remoteExit, perr)
	}
	return m, perr
}

type limitBuf struct {
	b   bytes.Buffer
	max int
}

func (l *limitBuf) Write(p []byte) (int, error) {
	if room := l.max - l.b.Len(); room > 0 {
		if len(p) > room {
			l.b.Write(p[:room])
		} else {
			l.b.Write(p)
		}
	}
	return len(p), nil
}
func (l *limitBuf) String() string { return l.b.String() }

// sshLegacyArgs eski sshd'lere (RHEL 5/6, eski ağ cihazları) bağlanmak için modern ssh'ın varsayılan
// olarak kapattığı algoritmaları EK olarak açar ("+" öneki: modern olanlar yerinde kalır; istemci
// derlemesinde bulunmayan adlar sessizce atlanır). Yalnızca probe'da bilinçli seçilirse kullanılır.
var sshLegacyArgs = []string{
	"-o", "KexAlgorithms=+diffie-hellman-group14-sha1,diffie-hellman-group1-sha1,diffie-hellman-group-exchange-sha1",
	"-o", "HostKeyAlgorithms=+ssh-rsa,ssh-dss",
	"-o", "PubkeyAcceptedKeyTypes=+ssh-rsa,ssh-dss",
	"-o", "Ciphers=+aes128-cbc,aes192-cbc,aes256-cbc,3des-cbc",
	"-o", "MACs=+hmac-sha1",
}

// sshNegotiationHint "no matching ... found" hatasını yönlendirici bir mesaja çevirir.
func sshNegotiationHint(stderr string, legacy bool) string {
	lm := strings.ToLower(stderr)
	if !strings.Contains(lm, "no matching") && !strings.Contains(lm, "unable to negotiate") && !strings.Contains(lm, "no hostkey alg") {
		return ""
	}
	offer := ""
	if i := strings.Index(lm, "their offer:"); i >= 0 {
		offer = strings.TrimSpace(stderr[i+len("their offer:"):])
		if j := strings.IndexAny(offer, "\r\n"); j >= 0 {
			offer = offer[:j]
		}
		if len(offer) > 120 {
			offer = offer[:120]
		}
		offer = " (sunucunun sunduğu: " + offer + ")"
	}
	if !legacy {
		return "sunucu eski SSH algoritmaları sunuyor" + offer + "; probe ayarlarında \"Eski SSH sunucusu uyumluluğu\" kutusunu işaretleyin"
	}
	return "eski SSH uyumluluğu açık olduğu hâlde algoritma eşleşmedi" + offer + "; APP_MON sunucusunda SHA-1 kapalı olabilir (RHEL 9: sudo update-crypto-policies --set DEFAULT:SHA1) ya da sunucu çok eskidir"
}

func sshError(stderr string, err error, pinned bool, legacy bool) error {
	m := strings.TrimSpace(stderr)
	lm := strings.ToLower(m)
	if hint := sshNegotiationHint(m, legacy); hint != "" {
		return errors.New("SSH algoritma uyuşmazlığı: " + hint)
	}
	switch {
	case strings.Contains(lm, "host key verification failed") || strings.Contains(lm, "remote host identification has changed"):
		if pinned {
			return errors.New("sunucu anahtarı sabitlenen anahtarla uyuşmuyor (sunucu yeniden kurulduysa Probe ayrıntısından parmak izini sıfırlayın; aksi halde ortadaki adam saldırısı olabilir)")
		}
		return errors.New("sunucu anahtarı doğrulanamadı")
	case strings.Contains(lm, "permission denied"):
		return errors.New("SSH kimlik doğrulaması reddedildi (kullanıcı adı veya anahtar yanlış; anahtar parolalıysa parolasız bir anahtar kullanın)")
	case strings.Contains(lm, "connection refused"):
		return errors.New("SSH bağlantısı reddedildi (sshd çalışıyor ve port doğru mu?)")
	case strings.Contains(lm, "timed out"):
		return errors.New("SSH bağlantı zaman aşımı")
	case strings.Contains(lm, "no route to host") || strings.Contains(lm, "network is unreachable"):
		return errors.New("SSH hedefine ağ yolu yok")
	case strings.Contains(lm, "error in libcrypto") || strings.Contains(lm, "invalid format"):
		return errors.New("SSH özel anahtarı okunamadı (biçim hatalı; OpenSSH/PEM biçiminde, parolasız olmalı)")
	}
	var ee *exec.ExitError
	if m == "" && errors.As(err, &ee) && ee.ExitCode() != 255 {
		return fmt.Errorf("uzak makinedeki komut çıkış kodu %d ile bitti ve çıktı vermedi (kullanıcının shell'i kısıtlı olabilir: /bin/sh ve /proc erişimi gerekir)", ee.ExitCode())
	}
	if m == "" {
		m = err.Error()
	}
	if len(m) > 200 {
		m = m[:200]
	}
	return fmt.Errorf("SSH hatası: %s", m)
}

// parseSSHOutput sshScript çıktısını ölçümlere çevirir.
func parseSSHOutput(out string, o Options) (map[string]float64, error) {
	secs := map[string][]string{"stat": nil}
	cur := "stat"
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if strings.HasPrefix(ln, "---") {
			cur = strings.ToLower(strings.TrimPrefix(ln, "---"))
			continue
		}
		if strings.TrimSpace(ln) != "" {
			secs[cur] = append(secs[cur], ln)
		}
	}
	m := map[string]float64{}
	if wantSSH(o, "cpu") {
		if st := secs["stat"]; len(st) >= 2 {
			if t1, i1, ok1 := cpuTimes(st[0]); ok1 {
				if t2, i2, ok2 := cpuTimes(st[1]); ok2 && t2 > t1 {
					p := 100 * (1 - (i2-i1)/(t2-t1))
					m["host.cpu_pct"] = clampPct(p)
				}
			}
		}
	}
	if wantSSH(o, "uptime") && len(secs["up"]) > 0 {
		if f := strings.Fields(secs["up"][0]); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				m["host.uptime_sec"] = v
			}
		}
	}
	if wantSSH(o, "mem") {
		kv := map[string]float64{}
		for _, ln := range secs["mem"] {
			f := strings.Fields(ln)
			if len(f) >= 2 {
				if v, err := strconv.ParseFloat(f[1], 64); err == nil {
					kv[strings.TrimSuffix(f[0], ":")] = v // kB
				}
			}
		}
		total := kv["MemTotal"]
		if total > 0 {
			avail, ok := kv["MemAvailable"]
			if !ok {
				avail = kv["MemFree"] + kv["Buffers"] + kv["Cached"]
			}
			used := total - avail
			if used < 0 {
				used = 0
			}
			m["host.mem_pct"] = clampPct(100 * used / total)
			m["host.mem_used_gb"] = round2(used / 1048576)
			m["host.mem_total_gb"] = round2(total / 1048576)
		}
	}
	if wantSSH(o, "swap") {
		kv := map[string]float64{}
		for _, ln := range secs["swap"] {
			f := strings.Fields(ln)
			if len(f) >= 2 {
				if v, err := strconv.ParseFloat(f[1], 64); err == nil {
					kv[strings.TrimSuffix(f[0], ":")] = v // kB
				}
			}
		}
		if total := kv["SwapTotal"]; total > 0 {
			used := total - kv["SwapFree"]
			if used < 0 {
				used = 0
			}
			m["host.swap_pct"] = clampPct(100 * used / total)
			m["host.swap_used_gb"] = round2(used / 1048576)
			m["host.swap_total_gb"] = round2(total / 1048576)
		} else if _, ok := kv["SwapTotal"]; ok {
			m["host.swap_pct"] = 0 // swap tanımlı değil
		}
	}
	if wantSSH(o, "load") && len(secs["load"]) > 0 {
		if f := strings.Fields(secs["load"][0]); len(f) >= 3 {
			for i, k := range []string{"host.load1", "host.load5", "host.load15"} {
				if v, err := strconv.ParseFloat(f[i], 64); err == nil {
					m[k] = v
				}
			}
		}
	}
	if wantSSH(o, "disk") {
		mounts := parseDF(secs["df"])
		var max float64 = -1
		names := make([]string, 0, len(mounts))
		for k := range mounts {
			names = append(names, k)
		}
		sort.Strings(names)
		kept := 0
		for _, name := range names {
			if kept >= 40 { // metrik sayısını sınırla
				break
			}
			if !mountWanted(o, name) {
				continue
			}
			kept++
			mt := mounts[name]
			m["host.disk_pct."+name] = mt.pct
			m["host.disk_used_gb."+name] = mt.usedGB
			m["host.disk_total_gb."+name] = mt.totalGB
			if mt.pct > max {
				max = mt.pct
			}
		}
		if max >= 0 {
			m["host.disk_pct"] = max
		}
	}
	core := wantSSH(o, "cpu") || wantSSH(o, "mem") || wantSSH(o, "disk")
	_, c1 := m["host.cpu_pct"]
	_, c2 := m["host.mem_pct"]
	_, c3 := m["host.disk_pct"]
	if len(m) == 0 || (core && !c1 && !c2 && !c3) {
		return nil, errors.New("SSH çıktısı çözümlenemedi (uzak makine Linux değil veya /proc erişilemiyor)")
	}
	return m, nil
}

// mountWanted dizin filtresine uyup uymadığını söyler (boş filtre = hepsi). Adlar df çıktısındaki
// bağlama noktasıdır; metrik adlarında olduğu gibi aynı biçimde temizlenerek karşılaştırılır.
func mountWanted(o Options, name string) bool {
	if len(o.Mounts) == 0 {
		return true
	}
	for _, m := range o.Mounts {
		n := diskNameRe.ReplaceAllString(strings.TrimSpace(m), "_")
		if len(n) > 60 {
			n = n[:60]
		}
		if n == name {
			return true
		}
	}
	return false
}

type mountInfo struct{ pct, usedGB, totalGB float64 }

func cpuTimes(line string) (total, idle float64, ok bool) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, s := range f[1:] {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, 0, false
		}
		if i >= 8 { // guest alanları zaten user/nice içinde
			break
		}
		total += v
		if i == 3 || i == 4 { // idle, iowait
			idle += v
		}
	}
	return total, idle, true
}

var pseudoFS = map[string]bool{"tmpfs": true, "devtmpfs": true, "squashfs": true, "overlay": true, "proc": true, "sysfs": true,
	"efivarfs": true, "cgroup": true, "cgroup2": true, "devpts": true, "mqueue": true, "configfs": true, "debugfs": true, "tracefs": true}

// parseDF `df -PkT` (veya `df -Pk`) çıktısını bağlama noktası → doluluk eşlemesine çevirir.
func parseDF(lines []string) map[string]mountInfo {
	res := map[string]mountInfo{}
	typed := len(lines) > 0 && strings.Contains(lines[0], "Type")
	for i, ln := range lines {
		if i == 0 && (strings.HasPrefix(ln, "Filesystem") || strings.HasPrefix(ln, "Dosya")) {
			continue
		}
		f := strings.Fields(ln)
		// Dosya sistemi; [tür]; 1K-blok; kullanılan; boş; yüzde; bağlama noktası (boşluk içerebilir)
		base := 5
		if typed {
			base = 6
		}
		if len(f) < base+1 {
			continue
		}
		if typed && pseudoFS[f[1]] {
			continue
		}
		off := 1
		if typed {
			off = 2
		}
		total, e1 := strconv.ParseFloat(f[off], 64)
		used, e2 := strconv.ParseFloat(f[off+1], 64)
		if e1 != nil || e2 != nil || total <= 0 {
			continue
		}
		mount := strings.Join(f[base:], " ")
		if strings.HasPrefix(mount, "/proc") || strings.HasPrefix(mount, "/sys") || strings.HasPrefix(mount, "/dev") || strings.HasPrefix(mount, "/run") {
			continue
		}
		name := diskNameRe.ReplaceAllString(mount, "_")
		if len(name) > 60 {
			name = name[:60]
		}
		if _, dup := res[name]; dup {
			continue
		}
		res[name] = mountInfo{pct: clampPct(100 * used / total), usedGB: round2(used / 1048576), totalGB: round2(total / 1048576)}
	}
	return res
}

func clampPct(p float64) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return round2(p)
}
func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }
