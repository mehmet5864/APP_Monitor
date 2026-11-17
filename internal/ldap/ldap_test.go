package ldap

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, pool
}

// sahte dizin sunucusu: "uid=ali,..." / "gizli" geçerli
func fakeServer(t *testing.T, cert tls.Certificate) (string, *int) {
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	binds := new(int)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				msg, err := readMessage(c)
				if err != nil {
					return
				}
				_, body, _, _ := readTLV(msg)
				_, idv, body, _ := readTLV(body)
				_, op, _, _ := readTLV(body)
				_, _, op, _ = readTLV(op) // sürüm
				_, dn, op, _ := readTLV(op)
				_, pw, _, _ := readTLV(op)
				*binds++
				code := byte(49)
				if string(dn) == "uid=ali,ou=people" && string(pw) == "gizli" {
					code = 0
				}
				res := append(append(tlv(0x0a, []byte{code}), tlv(0x04, nil)...), tlv(0x04, nil)...)
				c.Write(tlv(0x30, append(tlv(0x02, idv), tlv(0x61, res)...)))
			}(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String(), binds
}

func TestAuthenticate(t *testing.T) {
	cert, pool := selfSigned(t)
	addr, binds := fakeServer(t, cert)
	cfg := Config{URL: "ldaps://" + addr, DNTemplate: "uid=%s,ou=people", TLS: &tls.Config{RootCAs: pool, ServerName: "localhost"}}
	if err := Authenticate(cfg, "ali", "gizli"); err != nil {
		t.Fatal(err)
	}
	if err := Authenticate(cfg, "ali", "yanlis"); err != ErrInvalidCredentials {
		t.Fatalf("yanlış parola: %v", err)
	}
	before := *binds
	if err := Authenticate(cfg, "ali", ""); err != ErrInvalidCredentials {
		t.Fatal("boş parola reddedilmeli")
	}
	if err := Authenticate(cfg, "ali,ou=admin", "x"); err != ErrInvalidCredentials {
		t.Fatal("DN enjeksiyonu reddedilmeli")
	}
	if *binds != before {
		t.Fatal("boş parola / geçersiz ad sunucuya gitmemeli")
	}
	bad := cfg
	bad.TLS = &tls.Config{ServerName: "localhost"} // güvenilmeyen sertifika
	if err := Authenticate(bad, "ali", "gizli"); err == nil {
		t.Fatal("güvenilmeyen sertifika kabul edildi")
	}
}
