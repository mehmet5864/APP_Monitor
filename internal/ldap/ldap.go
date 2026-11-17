// Package ldap yalnızca "simple bind" ile kimlik doğrulama yapan küçük bir LDAP istemcisidir (arama yapmaz).
// Dizin sunucusu parolayı doğrular; roller APP_MON'da tutulur. Parola düz metin gönderildiği için ldaps:// veya StartTLS zorunludur.
package ldap

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Config LDAP bağlantı ayarlarıdır.
type Config struct {
	URL        string // ldaps://dc.sirket.local:636 veya ldap://dc:389 (StartTLS ile)
	DNTemplate string // %s kullanıcı adıyla değişir: "%s@sirket.local" veya "uid=%s,ou=people,dc=sirket,dc=local"
	TLS        *tls.Config
	Timeout    time.Duration
	AllowPlain bool // yalnızca deneme: şifresiz ldap:// (önerilmez)
}

var userRe = regexp.MustCompile(`^[A-Za-z0-9._@-]{1,64}$`)

// ValidUser DN şablonuna enjeksiyonu önlemek için kullanıcı adı karakterlerini sınırlar.
func ValidUser(u string) bool { return userRe.MatchString(u) }

var ErrInvalidCredentials = errors.New("kullanıcı adı veya parola hatalı")

// ---- BER yardımcıları ----

func lenBytes(n int) []byte {
	if n < 128 {
		return []byte{byte(n)}
	}
	if n < 256 {
		return []byte{0x81, byte(n)}
	}
	return []byte{0x82, byte(n >> 8), byte(n)}
}

func tlv(tag byte, c []byte) []byte {
	out := append([]byte{tag}, lenBytes(len(c))...)
	return append(out, c...)
}

func readTLV(b []byte) (tag byte, val, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, nil, errors.New("kısa LDAP yanıtı")
	}
	tag, l, off := b[0], int(b[1]), 2
	if l&0x80 != 0 {
		nb := l & 0x7f
		if nb == 0 || nb > 3 || len(b) < 2+nb {
			return 0, nil, nil, errors.New("geçersiz uzunluk")
		}
		l = 0
		for i := 0; i < nb; i++ {
			l = l<<8 | int(b[2+i])
		}
		off = 2 + nb
	}
	if len(b) < off+l {
		return 0, nil, nil, errors.New("eksik LDAP yanıtı")
	}
	return tag, b[off : off+l], b[off+l:], nil
}

func bindRequest(id byte, dn, pass string) []byte {
	req := tlv(0x60, append(append(tlv(0x02, []byte{3}), tlv(0x04, []byte(dn))...), tlv(0x80, []byte(pass))...))
	return tlv(0x30, append(tlv(0x02, []byte{id}), req...))
}

func startTLSRequest(id byte) []byte {
	return tlv(0x30, append(tlv(0x02, []byte{id}), tlv(0x77, tlv(0x80, []byte("1.3.6.1.4.1.1466.20037")))...))
}

// readMessage tek bir LDAPMessage okur.
func readMessage(c net.Conn) ([]byte, error) {
	hdr := make([]byte, 2)
	if _, err := readFull(c, hdr); err != nil {
		return nil, err
	}
	n := int(hdr[1])
	full := append([]byte{}, hdr...)
	if n&0x80 != 0 {
		nb := n & 0x7f
		if nb == 0 || nb > 3 {
			return nil, errors.New("geçersiz uzunluk")
		}
		lb := make([]byte, nb)
		if _, err := readFull(c, lb); err != nil {
			return nil, err
		}
		full = append(full, lb...)
		n = 0
		for _, b := range lb {
			n = n<<8 | int(b)
		}
	}
	if n > 1<<16 {
		return nil, errors.New("LDAP yanıtı çok büyük")
	}
	body := make([]byte, n)
	if _, err := readFull(c, body); err != nil {
		return nil, err
	}
	return append(full, body...), nil
}

func readFull(c net.Conn, b []byte) (int, error) {
	t := 0
	for t < len(b) {
		n, err := c.Read(b[t:])
		t += n
		if err != nil {
			return t, err
		}
	}
	return t, nil
}

// resultCode LDAPMessage içindeki LDAPResult.resultCode değerini döndürür.
func resultCode(msg []byte, wantOp byte) (int, error) {
	_, body, _, err := readTLV(msg)
	if err != nil {
		return 0, err
	}
	_, _, body, err = readTLV(body) // messageID
	if err != nil {
		return 0, err
	}
	op, res, _, err := readTLV(body)
	if err != nil {
		return 0, err
	}
	if op != wantOp {
		return 0, fmt.Errorf("beklenmeyen LDAP yanıtı (0x%x)", op)
	}
	_, code, _, err := readTLV(res)
	if err != nil || len(code) == 0 {
		return 0, errors.New("LDAP sonuç kodu okunamadı")
	}
	return int(code[len(code)-1]), nil
}

// Authenticate kullanıcıyı dizin sunucusunda bind ile doğrular. Boş parola asla gönderilmez
// (LDAP'ta boş parolalı bind "anonim" sayılıp başarılı olabilir).
func Authenticate(cfg Config, user, pass string) error {
	if pass == "" {
		return ErrInvalidCredentials
	}
	if !ValidUser(user) {
		return ErrInvalidCredentials
	}
	if !strings.Contains(cfg.DNTemplate, "%s") {
		return errors.New("LDAP DN şablonu %s içermeli")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "ldap" && u.Scheme != "ldaps") || u.Host == "" {
		return errors.New("LDAP adresi ldap:// veya ldaps:// olmalı")
	}
	host := u.Host
	if u.Port() == "" {
		if u.Scheme == "ldaps" {
			host = net.JoinHostPort(u.Hostname(), "636")
		} else {
			host = net.JoinHostPort(u.Hostname(), "389")
		}
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	tc := cfg.TLS
	if tc == nil {
		tc = &tls.Config{}
	}
	tc = tc.Clone()
	if tc.ServerName == "" {
		tc.ServerName = u.Hostname()
	}
	d := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	if u.Scheme == "ldaps" {
		conn, err = tls.DialWithDialer(d, "tcp", host, tc)
	} else {
		conn, err = d.Dial("tcp", host)
	}
	if err != nil {
		return fmt.Errorf("LDAP sunucusuna bağlanılamadı: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if u.Scheme == "ldap" && !cfg.AllowPlain {
		if _, err := conn.Write(startTLSRequest(1)); err != nil {
			return err
		}
		msg, err := readMessage(conn)
		if err != nil {
			return err
		}
		if code, err := resultCode(msg, 0x78); err != nil || code != 0 {
			return errors.New("LDAP sunucusu StartTLS'i kabul etmedi (ldaps:// kullanın)")
		}
		tconn := tls.Client(conn, tc)
		if err := tconn.Handshake(); err != nil {
			return fmt.Errorf("LDAP TLS el sıkışması başarısız: %w", err)
		}
		conn = tconn
		conn.SetDeadline(time.Now().Add(timeout))
	}
	dn := strings.Replace(cfg.DNTemplate, "%s", user, 1)
	if _, err := conn.Write(bindRequest(2, dn, pass)); err != nil {
		return err
	}
	msg, err := readMessage(conn)
	if err != nil {
		return err
	}
	code, err := resultCode(msg, 0x61)
	if err != nil {
		return err
	}
	switch code {
	case 0:
		return nil
	case 49: // invalidCredentials
		return ErrInvalidCredentials
	default:
		return fmt.Errorf("LDAP bind reddedildi (kod %d)", code)
	}
}
