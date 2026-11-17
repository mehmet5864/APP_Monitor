package app

import (
	"encoding/base64"
	"encoding/binary"
	"strings"
	"testing"

	"appmon/internal/probe"
)

func opensshKey(cipher string) string {
	var b []byte
	b = append(b, "openssh-key-v1\x00"...)
	l := make([]byte, 4)
	binary.BigEndian.PutUint32(l, uint32(len(cipher)))
	b = append(b, l...)
	b = append(b, cipher...)
	b = append(b, make([]byte, 16)...)
	return "-----BEGIN OPENSSH PRIVATE KEY-----\n" + base64.StdEncoding.EncodeToString(b) + "\n-----END OPENSSH PRIVATE KEY-----\n"
}

func TestValidateSecretValue(t *testing.T) {
	if v, err := validateSecretValue("ssh_key", strings.TrimRight(opensshKey("none"), "\n")); err != nil || !strings.HasSuffix(v, "\n") {
		t.Fatalf("parolasız anahtar kabul edilmeli: %v", err)
	}
	if _, err := validateSecretValue("ssh_key", opensshKey("aes256-ctr")); err == nil {
		t.Fatal("parola korumalı OpenSSH anahtarı reddedilmeli")
	}
	if _, err := validateSecretValue("ssh_key", "-----BEGIN RSA PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nabc\n-----END RSA PRIVATE KEY-----"); err == nil {
		t.Fatal("şifreli PEM reddedilmeli")
	}
	if _, err := validateSecretValue("ssh_key", "merhaba"); err == nil {
		t.Fatal("anahtar olmayan değer reddedilmeli")
	}
	if _, err := validateSecretValue("password", "  "); err == nil {
		t.Fatal("boş değer reddedilmeli")
	}
	if _, err := validateSecretValue("token", strings.Repeat("x", maxSecretLen+1)); err == nil {
		t.Fatal("çok uzun değer reddedilmeli")
	}
	if v, err := validateSecretValue("password", "p@ss\r\nword"); err != nil || strings.Contains(v, "\r") {
		t.Fatalf("CRLF normalleşmeli: %q %v", v, err)
	}
}

func TestValidateOptionsSSH(t *testing.T) {
	ok := probe.Options{Method: "ssh", SSHUser: "appmon", SecretID: 3, Metrics: []string{"disk"}, Community: "gizli", HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}
	o := ok
	if err := validateOptions("host", &o); err != nil {
		t.Fatal(err)
	}
	ok.SSHLegacy = true
	if o2 := ok; validateOptions("host", &o2) != nil || !o2.SSHLegacy {
		t.Fatalf("ssh_legacy korunmalı: %+v", o2)
	}
	if o.Method != "ssh" || o.SecretID != 3 || o.Community != "" || len(o.Metrics) != 1 || o.HostKey == "" {
		t.Fatalf("seçenekler beklenmedik: %+v", o)
	}
	for name, mut := range map[string]func(*probe.Options){
		"kötü kullanıcı": func(x *probe.Options) { x.SSHUser = "root;ls" },
		"anahtarsız":     func(x *probe.Options) { x.SecretID = 0 },
		"kötü host key":  func(x *probe.Options) { x.HostKey = "bozuk" },
	} {
		b := ok
		mut(&b)
		if err := validateOptions("host", &b); err == nil {
			t.Errorf("%s reddedilmeliydi", name)
		}
	}
	// snmp yöntemi ssh alanlarını temizler
	s := probe.Options{Method: "snmp", SSHUser: "x", SecretID: 9}
	if err := validateOptions("host", &s); err != nil || s.SecretID != 0 || s.SSHUser != "" {
		t.Fatalf("snmp'de ssh alanları temizlenmeli: %+v %v", s, err)
	}
}

func TestValidateOptionsSSHCollect(t *testing.T) {
	base := probe.Options{Method: "ssh", SSHUser: "appmon", SecretID: 3, Metrics: []string{"cpu"}}
	o := base
	o.Collect = []string{"cpu", "load", "swap", "cpu"}
	o.Mounts = []string{" /var ", "/", ""}
	if err := validateOptions("host", &o); err != nil {
		t.Fatal(err)
	}
	if len(o.Collect) != 3 || len(o.Mounts) != 2 || o.Mounts[0] != "/var" || o.Metrics != nil {
		t.Fatalf("beklenmedik: %+v", o)
	}
	for name, mut := range map[string]func(*probe.Options){
		"geçersiz veri":  func(x *probe.Options) { x.Collect = []string{"proc"} },
		"/ ile başlamaz": func(x *probe.Options) { x.Mounts = []string{"var"} },
		"çok dizin": func(x *probe.Options) {
			x.Mounts = make([]string, 41)
			for i := range x.Mounts {
				x.Mounts[i] = "/a"
			}
		},
	} {
		x := base
		mut(&x)
		if validateOptions("host", &x) == nil {
			t.Errorf("%s reddedilmeli", name)
		}
	}
	// snmp yönteminde collect/mounts taşınmaz
	s := probe.Options{Method: "snmp", Collect: []string{"cpu"}, Mounts: []string{"/"}}
	if err := validateOptions("host", &s); err != nil || len(s.Collect) != 0 || len(s.Mounts) != 0 {
		t.Fatalf("snmp: %+v %v", s, err)
	}
}
